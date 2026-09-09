package streamhost

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// realSessionPreamble is the capability-probe phase Sunshine (itsme228/Sunshine
// fork, macOS/VideoToolbox backend) logs before every negotiation: it tries
// every codec it might use, regardless of what the client actually asked
// for or what ends up streaming. Trimmed verbatim from a real
// ~/.config/sunshine/sunshine.log capture on this machine, with only the
// per-line properties (color coding/depth/range, warnings) removed for
// brevity — the codec-identifying lines are unchanged.
const realSessionPreamble = `[2026-07-19 09:28:19.827]: Info: CLIENT DISCONNECTED
[2026-07-19 09:28:20.926]: Info: Display device configuration is disabled. Reverting any active display device configuration.
[2026-07-19 09:28:20.929]: Info: // Testing for available encoders, this may generate errors. You can safely ignore those errors. //
[2026-07-19 09:28:20.929]: Info: Trying encoder [videotoolbox]
[2026-07-19 09:28:20.930]: Info: Configuring selected display (1) to stream
[2026-07-19 09:28:20.931]: Info: Creating encoder [h264_videotoolbox]
[2026-07-19 09:28:21.172]: Info: Creating encoder [hevc_videotoolbox]
[2026-07-19 09:28:21.343]: Info: Creating encoder [av1_videotoolbox]
[2026-07-19 09:28:21.344]: Error: Couldn't open [av1_videotoolbox]
[2026-07-19 09:28:21.345]: Info: Configuring selected display (1) to stream
[2026-07-19 09:28:21.347]: Info: Creating encoder [hevc_videotoolbox]
[2026-07-19 09:28:21.529]: Info: Found H.264 encoder: h264_videotoolbox [videotoolbox]
[2026-07-19 09:28:21.529]: Info: Found HEVC encoder: hevc_videotoolbox [videotoolbox]
[2026-07-19 09:28:21.529]: Info: Executing [Desktop]`

func realSession(codecEncoderLine string) string {
	return realSessionPreamble + "\n" +
		`[2026-07-19 09:28:21.636]: Info: New streaming session started [active sessions: 1]
[2026-07-19 09:28:21.662]: Info: CLIENT CONNECTED
[2026-07-19 09:28:21.708]: Info: Configuring selected display (1) to stream
` + codecEncoderLine + `
[2026-07-19 09:30:06.855]: Info: CLIENT DISCONNECTED`
}

func newTestBackend(t *testing.T, logContent string) *sunshineBackend {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "sunshine.log")
	if err := os.WriteFile(logPath, []byte(logContent), 0o644); err != nil {
		t.Fatalf("write fixture log: %v", err)
	}
	return &sunshineBackend{logPath: logPath}
}

// This is the exact regression this fix targets: on this fork, Sunshine logs
// a "Creating encoder [...]" line for EVERY codec it capability-probes, not
// just the one the session actually used. The pre-fix code did an unanchored
// scan for the last "codec"/"encoder" line in the tail, so a real H264
// session followed by an unrelated later probe (e.g. the client's start
// dialog fetching /serverinfo without starting a stream) would misreport
// H265 — even though the actual, currently-running session is plain H264.
func TestCurrentVideoCodec_IgnoresProbeNoiseAfterRealSession(t *testing.T) {
	log := realSession(`[2026-07-19 09:28:21.709]: Info: Creating encoder [h264_videotoolbox]`) + "\n" +
		`[2026-07-19 09:31:00.001]: Info: // Testing for available encoders, this may generate errors. You can safely ignore those errors. //
[2026-07-19 09:31:00.002]: Info: Trying encoder [videotoolbox]
[2026-07-19 09:31:00.010]: Info: Creating encoder [h264_videotoolbox]
[2026-07-19 09:31:00.180]: Info: Creating encoder [hevc_videotoolbox]`

	b := newTestBackend(t, log)
	if got := b.CurrentVideoCodec(); got != "h264" {
		t.Fatalf("CurrentVideoCodec() = %q, want %q (must reflect the real session, not the trailing HEVC probe noise)", got, "h264")
	}
}

func TestCurrentVideoCodec_RealSessionPickedHEVC(t *testing.T) {
	log := realSession(`[2026-07-19 09:28:21.709]: Info: Creating encoder [hevc_videotoolbox]`)

	b := newTestBackend(t, log)
	if got := b.CurrentVideoCodec(); got != "h265" {
		t.Fatalf("CurrentVideoCodec() = %q, want %q", got, "h265")
	}
}

// AV1 fails to open on this Mac's hardware (real capture: "Error: Couldn't
// open [av1_videotoolbox]"). The session actually streamed H264. Detection
// must not be fooled by the AV1 probe attempt earlier in the same window.
func TestCurrentVideoCodec_AV1UnavailableFallsBackToH264InRealSession(t *testing.T) {
	log := realSession(`[2026-07-19 09:28:21.709]: Info: Creating encoder [h264_videotoolbox]`)
	if !strings.Contains(log, "Couldn't open [av1_videotoolbox]") {
		t.Fatal("fixture sanity check failed: expected AV1 probe failure line")
	}

	b := newTestBackend(t, log)
	if got := b.CurrentVideoCodec(); got != "h264" {
		t.Fatalf("CurrentVideoCodec() = %q, want %q", got, "h264")
	}
}

// No client has ever connected yet (only the startup capability probe is in
// the log) — there's no session to anchor on, so this exercises the
// unanchored best-effort fallback path.
func TestCurrentVideoCodec_NoSessionYetUsesUnanchoredFallback(t *testing.T) {
	b := newTestBackend(t, realSessionPreamble)
	// Last "Creating encoder [...]" line in the preamble is the 10-bit HEVC
	// re-probe, so the best-effort fallback should report h265.
	if got := b.CurrentVideoCodec(); got != "h265" {
		t.Fatalf("CurrentVideoCodec() = %q, want %q", got, "h265")
	}
}

func TestCurrentVideoCodec_EmptyLogDefaultsToH264(t *testing.T) {
	b := newTestBackend(t, "")
	if got := b.CurrentVideoCodec(); got != "h264" {
		t.Fatalf("CurrentVideoCodec() = %q, want %q", got, "h264")
	}
}

func TestCurrentVideoCodec_NoLogPathDefaultsToH264(t *testing.T) {
	b := &sunshineBackend{}
	if got := b.CurrentVideoCodec(); got != "h264" {
		t.Fatalf("CurrentVideoCodec() = %q, want %q", got, "h264")
	}
}

func TestCreatingEncoderCodec(t *testing.T) {
	cases := []struct {
		line  string
		codec string
		ok    bool
	}{
		{`[t]: Info: Creating encoder [h264_videotoolbox]`, "h264", true},
		{`[t]: Info: Creating encoder [hevc_videotoolbox]`, "h265", true},
		{`[t]: Info: Creating encoder [av1_videotoolbox]`, "av1", true},
		{`[t]: Info: Creating encoder [h264_nvenc]`, "h264", true},
		{`[t]: Info: Creating encoder [hevc_nvenc]`, "h265", true},
		{`[t]: Info: Creating encoder [av1_nvenc]`, "av1", true},
		{`[t]: Info: Creating encoder [h264_qsv]`, "h264", true},
		{`[t]: Info: Creating encoder [hevc_vaapi]`, "h265", true},
		{`[t]: Info: Creating encoder [av1_amf]`, "av1", true},
		{`[t]: Info: Creating encoder [libx264]`, "h264", true},
		{`[t]: Info: Creating encoder [libx265]`, "h265", true},
		{`[t]: Info: Creating encoder [libsvtav1]`, "av1", true},
		{`[t]: Info: Trying encoder [videotoolbox]`, "", false},
		{`[t]: Info: Found H.264 encoder: h264_videotoolbox [videotoolbox]`, "", false},
		{`some unrelated line`, "", false},
	}
	for _, c := range cases {
		codec, ok := creatingEncoderCodec(c.line)
		if codec != c.codec || ok != c.ok {
			t.Errorf("creatingEncoderCodec(%q) = (%q, %v), want (%q, %v)", c.line, codec, ok, c.codec, c.ok)
		}
	}
}

// newServerInfoStub starts an httptest server that serves the given
// ServerCodecModeSupport value at /serverinfo, exactly like Sunshine's
// unauthenticated NvHTTP endpoint. Returns the adminPort to pass to
// fetchSupportedVideoCodecs/SupportedVideoCodecs so nvhttpPort (adminPort-1)
// resolves back to this stub.
func newServerInfoStub(t *testing.T, codecModeSupport int) (adminPort int, closeFn func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/serverinfo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<root status_code="200">
  <hostname>test</hostname>
  <appversion>1.0.0.0</appversion>
  <GfeVersion>3.23.0.74</GfeVersion>
  <uniqueid>test</uniqueid>
  <mac>00:00:00:00:00:00</mac>
  <ServerCodecModeSupport>%d</ServerCodecModeSupport>
  <PairStatus>0</PairStatus>
  <currentgame>0</currentgame>
  <state>SUNSHINE_SERVER_FREE</state>
</root>`, codecModeSupport)
	})
	srv := httptest.NewServer(mux)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse stub URL: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse stub port: %v", err)
	}
	// fetchSupportedVideoCodecs computes nvhttpPort = adminPort - 1, so give
	// it adminPort = stub port + 1 to land back on the stub.
	return port + 1, srv.Close
}

func TestFetchSupportedVideoCodecs(t *testing.T) {
	cases := []struct {
		name             string
		codecModeSupport int
		want             []string
	}{
		{"h264 only (this Mac's real AV1-unavailable flags)", scmH264 | scmHEVC, []string{"h264", "h265"}},
		{"h264+hevc+av1, all supported", scmH264 | scmHEVC | scmAV1Main8, []string{"h264", "h265", "av1"}},
		{"h264 only, no hevc no av1", scmH264, []string{"h264"}},
		{"hevc main10 still counts as hevc support", scmH264 | scmHEVCMain10, []string{"h264", "h265"}},
		{"av1 high10 444 still counts as av1 support", scmH264 | scmAV1High10444, []string{"h264", "av1"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			adminPort, closeFn := newServerInfoStub(t, c.codecModeSupport)
			defer closeFn()

			got := fetchSupportedVideoCodecs(adminPort)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("fetchSupportedVideoCodecs() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestFetchSupportedVideoCodecs_UnreachableFallsBackToH264Only(t *testing.T) {
	// Nothing listening on this port.
	got := fetchSupportedVideoCodecs(1)
	if !reflect.DeepEqual(got, []string{"h264"}) {
		t.Errorf("fetchSupportedVideoCodecs(unreachable) = %v, want [h264]", got)
	}
}

func TestSupportedVideoCodecs_Caches(t *testing.T) {
	b := &sunshineBackend{}

	calls := 0
	adminPort, closeFn := newServerInfoStubCounting(t, scmH264|scmHEVC, &calls)
	defer closeFn()

	first := b.SupportedVideoCodecs(adminPort)
	second := b.SupportedVideoCodecs(adminPort)

	if !reflect.DeepEqual(first, second) {
		t.Errorf("cached results differ: %v vs %v", first, second)
	}
	if calls != 1 {
		t.Errorf("expected exactly 1 HTTP call within the cache TTL, got %d", calls)
	}
}

func newServerInfoStubCounting(t *testing.T, codecModeSupport int, calls *int) (adminPort int, closeFn func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/serverinfo", func(w http.ResponseWriter, r *http.Request) {
		*calls++
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprintf(w, `<root><ServerCodecModeSupport>%d</ServerCodecModeSupport></root>`, codecModeSupport)
	})
	srv := httptest.NewServer(mux)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse stub URL: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse stub port: %v", err)
	}
	return port + 1, srv.Close
}

// This is the exact bug behind "Sunshine crashed, and the popup/API just
// silently stopped updating until the whole agent was restarted": Start()'s
// "already running, no-op" fast path only reads b.proc, which exec.Cmd
// never clears on its own when the OS process dies out from under it.
// Nothing used to notice the child had exited, so a crashed Sunshine (e.g.
// the ENet null-pointer-dereference in host_create — see itsme228/
// Sunshine's network.cpp) stayed "believed alive" forever.
func TestWatchProcessExit_ClearsCmdOnExit(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start test process: %v", err)
	}
	proc := sunshineExecCmdProcess{cmd}
	b := &sunshineBackend{proc: proc}
	if !b.Running() {
		t.Fatal("sanity check: Running() should be true immediately after Start()")
	}

	done := make(chan struct{})
	go func() {
		b.watchProcessExit(proc)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watchProcessExit did not return after the watched process exited")
	}

	if b.Running() {
		t.Error("Running() should report false once the watched process has exited — this is what lets Start() relaunch a crashed Sunshine instead of believing it's still alive forever")
	}
}

// A stale watcher for an OLD proc (already replaced by Stop()+a fresh
// Start()) must not clobber the newer, still-running b.proc.
func TestWatchProcessExit_DoesNotClobberNewerCmd(t *testing.T) {
	oldCmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := oldCmd.Start(); err != nil {
		t.Fatalf("start old test process: %v", err)
	}
	newCmd := exec.Command("/bin/sh", "-c", "sleep 30")
	if err := newCmd.Start(); err != nil {
		t.Fatalf("start new test process: %v", err)
	}
	defer func() {
		_ = newCmd.Process.Kill()
		_, _ = newCmd.Process.Wait()
	}()

	oldProc := sunshineExecCmdProcess{oldCmd}
	newProc := sunshineExecCmdProcess{newCmd}
	b := &sunshineBackend{proc: newProc} // simulates: oldCmd already replaced by a fresh Start()

	done := make(chan struct{})
	go func() {
		b.watchProcessExit(oldProc)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watchProcessExit(oldCmd) did not return")
	}

	b.mu.Lock()
	got := b.proc
	b.mu.Unlock()
	if got != newProc {
		t.Error("watchProcessExit for a stale/replaced cmd must not clear a newer b.proc")
	}
}

// The actual live latency bug SetOnExit fixes (see its own doc comment):
// nothing used to notice a crashed process until sunshineWatchdog's next
// periodic tick, up to sunshineWatchdogInterval (15s) later. Confirms the
// callback fires, and specifically that it fires without deadlocking --
// watchProcessExit must release b.mu *before* calling it, since a real
// caller's callback (app.startSunshine) calls back into Start(), which
// takes this same mutex.
func TestWatchProcessExit_FiresOnExitCallback(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start test process: %v", err)
	}
	proc := sunshineExecCmdProcess{cmd}
	b := &sunshineBackend{proc: proc}

	fired := make(chan struct{})
	b.SetOnExit(func() {
		// Exercises the deadlock hazard directly: if watchProcessExit
		// still held b.mu when invoking this, this would hang forever
		// instead of the test passing.
		b.mu.Lock()
		b.mu.Unlock()
		close(fired)
	})

	done := make(chan struct{})
	go func() {
		b.watchProcessExit(proc)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watchProcessExit did not return")
	}
	select {
	case <-fired:
	case <-time.After(3 * time.Second):
		t.Fatal("onExit callback was never invoked (or deadlocked)")
	}
}

// A stale watcher's exit must not fire onExit at all -- only the current,
// still-tracked process's exit should trigger a restart attempt.
func TestWatchProcessExit_DoesNotFireOnExitForStaleCmd(t *testing.T) {
	oldCmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := oldCmd.Start(); err != nil {
		t.Fatalf("start old test process: %v", err)
	}
	newCmd := exec.Command("/bin/sh", "-c", "sleep 30")
	if err := newCmd.Start(); err != nil {
		t.Fatalf("start new test process: %v", err)
	}
	defer func() {
		_ = newCmd.Process.Kill()
		_, _ = newCmd.Process.Wait()
	}()

	b := &sunshineBackend{proc: sunshineExecCmdProcess{newCmd}}
	fired := false
	b.SetOnExit(func() { fired = true })

	done := make(chan struct{})
	go func() {
		b.watchProcessExit(sunshineExecCmdProcess{oldCmd})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watchProcessExit(oldCmd) did not return")
	}

	if fired {
		t.Error("onExit must not fire for a stale/replaced cmd's exit")
	}
}

// sunshineDataDir must never collide with legacyDataDir (Sunshine's own
// generic per-OS default, e.g. $HOME/.config/sunshine) — that collision,
// shared with any independently-installed standalone Sunshine on the same
// machine, is the actual reported bug this whole change fixes.
func TestSunshineDataDir_NeverSharedWithLegacyDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("legacyDataDir keys off b.windowsDir on Windows, not $HOME — trivially distinct from stateDir/sunshine by construction there")
	}
	t.Setenv("HOME", t.TempDir())

	b := &sunshineBackend{stateDir: t.TempDir()}
	if got, legacy := b.sunshineDataDir(), b.legacyDataDir(); got == legacy {
		t.Fatalf("sunshineDataDir() must never equal legacyDataDir(), both were %q", got)
	}
	if !strings.HasPrefix(b.sunshineDataDir(), b.stateDir) {
		t.Errorf("sunshineDataDir() = %q, want it nested under this agent's own stateDir %q", b.sunshineDataDir(), b.stateDir)
	}
}

// The config-file path must be sunshineConfigArgs' first element — verified
// against the actual bundled Sunshine binary that passing it after --creds
// silently drops it (CLI11 swallows it as extra input to --creds instead of
// the separate positional config argument) and every path falls back to
// Sunshine's own default directory, defeating the whole point of this
// isolation. See sunshineConfigArgs' doc comment.
func TestSunshineConfigArgs_ConfigPathFirstThenIsolatedOverrides(t *testing.T) {
	b := &sunshineBackend{stateDir: t.TempDir()}
	args := b.sunshineConfigArgs()

	wantLen := 6 // config path + 5 "key=value" overrides
	if len(args) != wantLen {
		t.Fatalf("sunshineConfigArgs() = %v, want %d elements", args, wantLen)
	}
	if args[0] != b.ConfigPath() {
		t.Errorf("args[0] = %q, want the config path %q (must be first, see doc comment)", args[0], b.ConfigPath())
	}

	dataDir := b.sunshineDataDir()
	wantPrefixes := []string{"credentials_file=", "file_apps=", "log_path=", "pkey=", "cert="}
	for i, prefix := range wantPrefixes {
		got := args[i+1]
		if !strings.HasPrefix(got, prefix) {
			t.Errorf("args[%d] = %q, want prefix %q", i+1, got, prefix)
			continue
		}
		if path := strings.TrimPrefix(got, prefix); !strings.HasPrefix(path, dataDir) {
			t.Errorf("args[%d] = %q, want its path under sunshineDataDir() %q", i+1, got, dataDir)
		}
	}
}

// A backend with no stateDir (shouldn't happen via NewSunshine in practice,
// but defensive) must produce no args at all rather than a partially-built
// command line pointing at "" paths.
func TestSunshineConfigArgs_EmptyWithoutStateDir(t *testing.T) {
	b := &sunshineBackend{}
	if args := b.sunshineConfigArgs(); args != nil {
		t.Errorf("sunshineConfigArgs() with no stateDir = %v, want nil", args)
	}
}

// The core migration guarantee: an upgrading install's existing
// config/credentials get copied into the new isolated sunshineDataDir, and
// the original legacy location is left completely untouched — required
// both to preserve existing pairings across the upgrade and, more
// importantly, because legacyDataDir might actually belong to a separate,
// independent Sunshine install rather than this agent's own prior data (the
// exact scenario the bug report describes), so it must never be modified.
func TestMigrateLegacyData_CopiesWithoutTouchingOriginal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("legacyDataDir keys off b.windowsDir on Windows, not $HOME")
	}
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	legacyDir := filepath.Join(fakeHome, ".config", "sunshine")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const wantConf = "port = 47989\n"
	if err := os.WriteFile(filepath.Join(legacyDir, "sunshine.conf"), []byte(wantConf), 0o644); err != nil {
		t.Fatal(err)
	}
	const wantState = `{"username":"sunshine"}`
	if err := os.WriteFile(filepath.Join(legacyDir, "sunshine_state.json"), []byte(wantState), 0o644); err != nil {
		t.Fatal(err)
	}

	b := &sunshineBackend{stateDir: t.TempDir()}
	b.migrateLegacyData()

	gotConf, err := os.ReadFile(b.ConfigPath())
	if err != nil {
		t.Fatalf("migrated sunshine.conf missing: %v", err)
	}
	if string(gotConf) != wantConf {
		t.Errorf("migrated sunshine.conf = %q, want %q", gotConf, wantConf)
	}
	gotState, err := os.ReadFile(b.credentialsFilePath())
	if err != nil {
		t.Fatalf("migrated sunshine_state.json missing: %v", err)
	}
	if string(gotState) != wantState {
		t.Errorf("migrated sunshine_state.json = %q, want %q", gotState, wantState)
	}

	// The original must be untouched — same content, still there.
	origConf, err := os.ReadFile(filepath.Join(legacyDir, "sunshine.conf"))
	if err != nil || string(origConf) != wantConf {
		t.Errorf("legacy sunshine.conf must remain untouched: content=%q err=%v", origConf, err)
	}
}

// No legacy data to migrate (fresh install, or a machine that never had
// Sunshine's default location populated) must be a silent no-op, not an
// error or a panic.
func TestMigrateLegacyData_NoopWhenNothingToMigrate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("legacyDataDir keys off b.windowsDir on Windows, not $HOME")
	}
	t.Setenv("HOME", t.TempDir())

	b := &sunshineBackend{stateDir: t.TempDir()}
	b.migrateLegacyData()

	if _, err := os.Stat(b.ConfigPath()); err == nil {
		t.Error("expected no sunshine.conf to appear when there was nothing to migrate")
	}
}

// Once sunshineDataDir already has its own sunshine.conf (already migrated,
// or a fresh install that already ran once), migrateLegacyData must not
// overwrite it — even if a legacy location also happens to exist (e.g. a
// separate standalone Sunshine install that appeared after this agent had
// already started using its own isolated directory).
func TestMigrateLegacyData_NoopWhenAlreadyMigrated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("legacyDataDir keys off b.windowsDir on Windows, not $HOME")
	}
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	legacyDir := filepath.Join(fakeHome, ".config", "sunshine")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "sunshine.conf"), []byte("port = 11111\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := &sunshineBackend{stateDir: t.TempDir()}
	if err := os.MkdirAll(b.sunshineDataDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	const alreadyThere = "port = 47989\n"
	if err := os.WriteFile(b.ConfigPath(), []byte(alreadyThere), 0o644); err != nil {
		t.Fatal(err)
	}

	b.migrateLegacyData()

	got, err := os.ReadFile(b.ConfigPath())
	if err != nil || string(got) != alreadyThere {
		t.Errorf("already-migrated sunshine.conf must not be overwritten: content=%q err=%v", got, err)
	}
}
