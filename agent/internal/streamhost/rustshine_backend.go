// rustshineBackend drives a private "rust-shine" gamestream-server instance
// (bin/gamestream-server in the private itsme228/rust-shine repo) as an
// alternate streamhost.Backend, constructed on demand by
// App.SetStreamBackend once the agent's internal/entitlement package has
// verified a hardware-bound license or trial (see that package, and
// agent/internal/entitlement/download.go for how the binary gets staged
// into stateDir/rustshine/ in the first place -- deliberately not
// exeDir/rustshine/, see that package's StagePath doc comment on why:
// writing into exeDir post-install breaks the signed macOS .app bundle's
// code signature seal, which then breaks TCC permission checks for the
// whole app). Always compiled in — no
// build tag — because this file contains no secrets or bundled binary of
// its own, just an HTTP/CLI client for its admin API; gating happens at
// runtime via the entitlement check, not at compile time.
//
// Protocol facts below (routes, JSON field names, CLI flags, config keys,
// port offsets) were confirmed directly against crates/gamestream-proto and
// bin/gamestream-server's clap CLI in the private repo at the time this was
// written. If that server's API changes, this file needs updating to match.
package streamhost

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"usbridge_agent/internal/hwid"
)

// rustshineProcess abstracts the two ways gamestream-server can end up
// running under this backend: as a plain child process (exec.Cmd -- every
// platform, and the normal Windows case where this agent itself is already
// running interactively) or, on Windows when this agent is running as the
// LocalSystem USBridgeAgent service, as a process re-homed into the active
// console session via internal/sessionlaunch (see
// rustshine_process_windows.go's sessionBrokerLaunchImpl). A LocalSystem
// service is permanently confined to the non-interactive Session 0 -- a
// user logging in later does not move it into their session -- so
// gamestream-server's DXGI Desktop Duplication capture, started directly
// under the service, can only ever see a wrong/fallback monitor list
// (confirmed live: falls back to a 2-entry generic resolution list and
// produces no video, vs. the real monitor list at their native resolution
// when launched inside the actual interactive session). Routing
// Start/Stop/watchProcessExit/Running/Pid through this interface instead
// of *exec.Cmd directly keeps this file buildable on every platform
// despite the second launch path being Windows-only.
type rustshineProcess interface {
	Pid() int
	Kill() error
	Wait() error
}

// execCmdProcess adapts *exec.Cmd to rustshineProcess -- the ordinary path,
// used everywhere except the Windows-service session-broker case above.
type execCmdProcess struct{ cmd *exec.Cmd }

func (p execCmdProcess) Pid() int    { return p.cmd.Process.Pid }
func (p execCmdProcess) Kill() error { return p.cmd.Process.Kill() }
func (p execCmdProcess) Wait() error { return p.cmd.Wait() }

// useSessionBroker reports whether Start should launch gamestream-server via
// sessionBrokerLaunch (re-homing it into the active console session)
// instead of a plain child process. Always false except on Windows when
// this agent is running as the LocalSystem USBridgeAgent service -- see
// rustshine_process_windows.go's init, which overrides both this and
// sessionBrokerLaunch.
var useSessionBroker = func() bool { return false }

// sessionBrokerLaunch launches gamestream-server inside the currently
// active console session instead of whatever (non-interactive) session
// this agent process itself is running in. Only ever called when
// useSessionBroker() is true; nil on every platform except Windows. Returns
// an error satisfying errors.Is(err, sessionlaunch.ErrNoActiveSession) --
// duplicated here as errNoActiveSessionMarker rather than importing
// internal/sessionlaunch (a Windows-only package) into this cross-platform
// file -- when nobody is currently attached to the console (e.g. still at
// boot, before the very first login).
var sessionBrokerLaunch func(exe string, args []string, workDir string, stdout, stderr *os.File) (rustshineProcess, error)

// errNoActiveSessionMarker lets Start() recognize "no active console
// session yet" (see sessionBrokerLaunch's doc comment) without importing
// the Windows-only package that actually defines it -- rustshine_process_windows.go's
// sessionBrokerLaunchImpl wraps sessionlaunch.ErrNoActiveSession so that
// errors.Is(err, errNoActiveSessionMarker) succeeds. This lets Start()
// treat "nobody logged in yet" the same quiet, retry-on-next-tick way it
// already treats other not-ready-yet conditions (e.g. the binary not being
// staged yet), instead of logging it as a real failure every
// sunshineWatchdogInterval until someone logs in.
var errNoActiveSessionMarker = fmt.Errorf("rustshine: no active console session")

const rustshineAdminUser = "usbridge"

type rustshineBackend struct {
	mu          sync.Mutex
	exeDir      string
	stateDir    string
	launchPath  string
	capExecPath string // set via SetCapExecPath once CAP_SYS_ADMIN is granted; see Start
	logPath     string
	proc        rustshineProcess // see the rustshineProcess doc comment above
	watchdog    *exec.Cmd        // macOS only, see rustshine_process_other.go
	onExit      func()    // see SetOnExit

	activeAdminPassword string
	adminPort           int // set by Start; CurrentVideoCodec needs it despite taking no args itself

	// lastLaunchAt/crashStreak implement a restart backoff. app.startSunshine
	// is wired as this backend's onExit callback (see SetOnExit) and is
	// invoked with zero delay on every process exit, crashed or not -- fine
	// for a real crash (recover fast), catastrophic for a process that fails
	// instantly and deterministically every single time (e.g. gamestream-server
	// rejecting a CLI flag the Go side always passes -- confirmed live: an
	// agent whose staged binary predated a newer --hardware-id flag
	// respawned it 10-15x/sec, saturating a CPU core and writing gigabytes
	// to the log file within minutes). That tight loop is also
	// self-perpetuating: entitlement.StageRustShine's os.Rename over the
	// staged binary needs it to sit still for a moment, which a
	// near-100%-duty-cycle respawn loop never allows, so the very update
	// that would fix the crash can never land either. Start() consults
	// these to slow down (not stop -- a real transient crash still needs to
	// recover reasonably fast) once a pattern of near-instant exits shows
	// up, both bounding the damage and giving the auto-updater's rename an
	// actual window to land in.
	lastLaunchAt time.Time
	crashStreak  int

	// updatePaused implements UpdatePauser -- see that interface's doc
	// comment. Checked by ListCaptureDevices' Windows implementation
	// (rustshine_devices_windows.go) before it spawns a fresh
	// --list-capture-devices helper subprocess, which would otherwise be
	// able to open a new handle on this same .exe during exactly the
	// window an in-flight update needs it to stay unlocked for.
	updatePaused bool

	// sharedSecret is the agent's own master key, handed to gamestream-server
	// via --webrtc-shared-secret so its native WebRTC signaling endpoint
	// (POST /webrtc/offer) authenticates requests the same way every other
	// agent /api/* endpoint does — see SetSharedSecret's doc comment for how
	// this gets set/refreshed.
	sharedSecret []byte

	// webrtcDisabled mirrors config.Config.RustShineWebRTCDisabled -- when
	// true, Start passes --webrtc-disable so gamestream-server never opens
	// the WebRTC signaling port at all. Set via SetWebRTCEnabled before
	// Start; a change while already running only takes effect on the next
	// Start (gamestream-server has no live toggle for this, it's a
	// startup-only CLI flag).
	webrtcDisabled bool

	supportedCodecsCache struct {
		mu        sync.Mutex
		codecs    []string
		fetchedAt time.Time
	}
}

var _ Backend = (*rustshineBackend)(nil)

// NewRustshine constructs the rust-shine-backed Backend implementation.
// exeDir/stateDir/logPath have the same meaning as NewSunshine's.
func NewRustshine(exeDir, stateDir, logPath string) Backend {
	return &rustshineBackend{
		exeDir:   exeDir,
		stateDir: stateDir,
		logPath:  logPath,
	}
}

// DisplayName identifies this backend for display purposes only (GUI
// status, /api/status, logs) — see streamhost.Identity.
func (b *rustshineBackend) DisplayName() string { return "RustShine (Proprietary)" }

// SetSharedSecret sets the secret Start() passes to gamestream-server as
// --webrtc-shared-secret. Called via an optional-interface probe from
// app.go right after construction (initial boot / SetStreamBackend) and
// again from RegenerateMasterKey — mirrors api.SecurityMiddleware's
// SetMasterKey hot-swap shape so a rotated master key reaches rustshine on
// the very next Start()/RestartSunshine() without needing a fresh
// rustshineBackend to be constructed (RestartSunshine reuses the existing
// backend object, it doesn't rebuild one — see its own doc comment).
func (b *rustshineBackend) SetSharedSecret(secret []byte) {
	b.mu.Lock()
	b.sharedSecret = secret
	b.mu.Unlock()
}

// SetWebRTCEnabled sets whether Start passes --webrtc-disable. Called via
// the same optional-interface probe pattern as SetSharedSecret (see
// app.applyStreamWebRTCEnabled) -- a no-op for sunshineBackend, which has
// no WebRTC endpoint to disable.
func (b *rustshineBackend) SetWebRTCEnabled(enabled bool) {
	b.mu.Lock()
	b.webrtcDisabled = !enabled
	b.mu.Unlock()
}

// binaryName is bin/gamestream-server's build output name, per its
// Cargo.toml package name — "gamestream-server(.exe)", not "rust-shine".
func binaryName() string {
	if runtime.GOOS == "windows" {
		return "gamestream-server.exe"
	}
	return "gamestream-server"
}

// BinaryPath resolves the staged gamestream-server binary: stateDir/rustshine/
// (see entitlement.StagePath's doc comment for why stateDir and not exeDir),
// falling back to exeDir/rustshine/ for anything staged there by an older
// build of this agent before that fix, then PATH for local dev where it's
// just been cargo-built and symlinked.
func (b *rustshineBackend) BinaryPath() string {
	if b.launchPath != "" {
		return b.launchPath
	}
	if p := filepath.Join(b.stateDir, "rustshine", binaryName()); fileExists(p) {
		return p
	}
	if p := filepath.Join(b.exeDir, "rustshine", binaryName()); fileExists(p) {
		return p
	}
	if path, err := exec.LookPath(binaryName()); err == nil {
		return path
	}
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// capExecPathFor returns the path to the bundled sunshine-capexec launcher
// (cmd/sunshine_capexec — a generic "raise CAP_SYS_ADMIN into ambient caps,
// then exec <target>" wrapper, not Sunshine-specific despite the name), or
// "" if not bundled (non-Linux, or a dev build without the AppImage/staged
// layout). gamestream-server's KMS/DRM capture (crates/capture-kms in the
// private rust-shine repo) needs CAP_SYS_ADMIN exactly like Sunshine's does,
// and for the identical reason a file capability can't go directly on
// gamestream-server itself: it resolves bundled shared libs (e.g.
// libvulkan.so.1) via RPATH=$ORIGIN/../lib, and a file capability would put
// it into secure-execution mode, breaking that resolution. See
// internal/permissions.RequestKMSCapture.
func (b *rustshineBackend) capExecPathFor() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	p := filepath.Join(b.exeDir, "sunshine-capexec")
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		return p
	}
	return ""
}

// runtimeCapExecPath returns the path sunshine-capexec should actually be
// setcap'd from. Inside an AppImage the bundled copy lives on the read-only
// squashfs mount, so `pkexec setcap` on it always fails silently (the
// pkexec prompt succeeds, but the capability is never actually written) —
// stage a writable copy into stateDir first, same fix as sunshineBackend's
// runtimeCapExecPath. Unlike Sunshine, gamestream-server itself does NOT
// need staging: only the file setcap actually writes to (capexec) has to be
// writable — the target binary capexec execs stays wherever it already is,
// its own RPATH resolution is unaffected by where capexec sits.
func (b *rustshineBackend) runtimeCapExecPath() string {
	capexecSrc := b.capExecPathFor()
	if runtime.GOOS != "linux" || capexecSrc == "" || b.stateDir == "" {
		return capexecSrc
	}
	if os.Getenv("APPIMAGE") == "" {
		return capexecSrc
	}
	staged, err := stageCapExecBinary(capexecSrc, filepath.Join(b.stateDir, "rustshine-capexec-runtime"))
	if err != nil {
		log.Printf("[rustshine] failed to stage writable copy for KMS setcap: %v", err)
		return capexecSrc
	}
	return staged
}

// CapExecPath returns the (staged-if-needed) path to the bundled
// sunshine-capexec launcher, or "" if not present/granted yet.
func (b *rustshineBackend) CapExecPath() string { return b.runtimeCapExecPath() }

// SetCapExecPath sets the sunshine-capexec launcher path (see
// runtimeCapExecPath) that Start uses to launch gamestream-server with
// CAP_SYS_ADMIN via ambient capabilities. Only ever set once the capability
// has actually been granted on that path (internal/app), mirroring
// sunshineBackend's SetCapExecPath.
func (b *rustshineBackend) SetCapExecPath(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.capExecPath = path
}

func (b *rustshineBackend) Running() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.proc != nil
}

func (b *rustshineBackend) Pid() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.proc == nil {
		return 0
	}
	return b.proc.Pid()
}

// generatePassword creates a cryptographically-random 20-character hex
// password, same scheme as sunshineBackend's.
func generateRustshinePassword() string {
	buf := make([]byte, 10)
	if _, err := rand.Read(buf); err != nil {
		log.Panicf("[rustshine] crypto/rand unavailable: %v", err)
	}
	return hex.EncodeToString(buf)
}

// credentialsPath is the file gamestream-server itself owns via
// --credentials-path (its own AdminCredentials format — not necessarily
// plaintext, and not something this launcher parses).
func (b *rustshineBackend) credentialsPath() string {
	cp := b.ConfigPath()
	if cp == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(cp), "rustshine_server_credentials")
}

// ourPassFile is a separate sidecar file THIS launcher writes the plaintext
// password to right after a successful --creds bootstrap, so AdminPass can
// recover it in a later process (or after Start's "already running,
// nothing to bootstrap" fast path) without needing to parse
// gamestream-server's own credentials-path file format. Same pattern as
// sunshineBackend's adminPassFile.
func (b *rustshineBackend) ourPassFile() string {
	cp := b.ConfigPath()
	if cp == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(cp), "usbridge_admin_pass")
}

// AdminUser returns the fixed admin-API username this launcher always
// bootstraps via --creds. gamestream-server itself doesn't hardcode a
// username (unlike Sunshine) — any value works, this is just ours.
func (b *rustshineBackend) AdminUser() string { return rustshineAdminUser }

// AdminPass returns the current session admin password, falling back to
// the persisted file from a previous session (same pattern as
// sunshineBackend.AdminPass).
func (b *rustshineBackend) AdminPass() string {
	b.mu.Lock()
	p := b.activeAdminPassword
	b.mu.Unlock()
	if p != "" {
		return p
	}
	if pf := b.ourPassFile(); pf != "" {
		if data, err := os.ReadFile(pf); err == nil {
			if pass := strings.TrimSpace(string(data)); pass != "" {
				return pass
			}
		}
	}
	return ""
}

// Start launches gamestream-server if it isn't already running (by this
// backend, or reachable on adminPort). No-op if the binary can't be found.
func (b *rustshineBackend) Start(adminPort int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.adminPort = adminPort
	if b.proc != nil {
		return nil
	}
	launchPath := b.BinaryPath()
	if launchPath == "" {
		return nil
	}
	if _, err := os.Stat(launchPath); err != nil {
		log.Printf("[rustshine] launch path not found, skipping auto-start: %s", launchPath)
		return nil
	}
	if adminPort > 0 && portReachable(adminPort, 300*time.Millisecond) {
		log.Printf("[rustshine] admin port %d already reachable, assuming gamestream-server is already running", adminPort)
		if pf := b.ourPassFile(); pf != "" {
			if data, err := os.ReadFile(pf); err == nil {
				if pass := strings.TrimSpace(string(data)); pass != "" {
					b.activeAdminPassword = pass
				}
			}
		}
		return nil
	}

	// Backfill adapter_name if capture=kms was persisted without one (e.g.
	// a sunshine_capture_mode:"kms" preference inherited from a previous
	// Sunshine session via app.syncSunshineCaptureMode, written straight
	// into this backend's own conf without going through SetCaptureMode's
	// auto-fill). Belt-and-suspenders: SetCaptureMode fills this in on the
	// path that sets "capture" itself, but a config file that already had
	// "capture = kms" on disk before this backend ever ran wouldn't have
	// gone through that path at all.
	if runtime.GOOS == "linux" && b.CaptureMode() == "kms" && b.ConfigKey("adapter_name") == "" {
		if card := b.firstKmsCardPath(); card != "" {
			if err := b.SetConfigKey("adapter_name", card); err != nil {
				log.Printf("[rustshine] failed to backfill adapter_name: %v", err)
			}
		}
	}

	// Repair a kms_connector that doesn't match any currently enumerable
	// connector. This happens two ways: a bare numeric value written before
	// SetOutputName's stale-index guard existed (efce4fc) is permanent —
	// that guard only stops future bad writes, it never rewrites an
	// already-corrupted config file — and a previously valid connector name
	// can also go stale after a monitor/cable swap or across a distro/driver
	// change (connector names like "HDMI-A-1" vs. "DP-2" vs. "eDP-1" aren't
	// portable). capture-kms's own in-process "fall back to the first
	// connected output" logic does not actually recover from this — it logs
	// the fallback, then still fails the pipeline with "no connected DRM
	// connector found" — so gamestream-server never captures a frame until
	// the config is corrected on disk. There's no universal default
	// connector name to hardcode, so resolve against ListCaptureDevices'
	// live enumeration instead, the same source SetOutputName's numeric-index
	// path already trusts.
	if runtime.GOOS == "linux" && b.CaptureMode() == "kms" {
		connector := b.ConfigKey("kms_connector")
		devices := b.ListCaptureDevices()
		valid := false
		for _, d := range devices {
			if _, c, ok := strings.Cut(d.OutputName, "|"); ok && c == connector {
				valid = true
				break
			}
		}
		if !valid && len(devices) > 0 {
			if err := b.SetOutputName(devices[0].OutputName); err != nil {
				log.Printf("[rustshine] failed to repair invalid kms_connector %q: %v", connector, err)
			} else {
				log.Printf("[rustshine] kms_connector %q not found among live connectors, repaired to %q", connector, devices[0].OutputName)
			}
		}
	}

	basePort := adminPort - 1 // gamestream-server's --http-port is the NvHTTP base port; admin listens on base+1.
	credsPath := b.credentialsPath()

	// Bootstrap a fresh random admin password via --creds (one-shot mode:
	// writes credentials-path and exits) before starting the real server,
	// same reasoning as Sunshine's --creds bootstrap. The plaintext is
	// persisted to our own sidecar file (ourPassFile), not read back from
	// gamestream-server's own credentials-path file — that file's on-disk
	// format is internal to it and not something this launcher parses.
	if credsPath != "" {
		newPass := generateRustshinePassword()
		credsCmd := exec.Command(launchPath, "--credentials-path", credsPath, "--creds", rustshineAdminUser, newPass)
		configureRustshineProcess(credsCmd)
		if out, err := credsCmd.CombinedOutput(); err != nil {
			log.Printf("[rustshine] --creds failed: %v: %s", err, out)
		} else {
			b.activeAdminPassword = newPass
			if pf := b.ourPassFile(); pf != "" {
				_ = os.WriteFile(pf, []byte(newPass), 0600)
			}
			log.Printf("[rustshine] admin password set (user=%s)", rustshineAdminUser)
		}
	}

	args := []string{}
	if cfgPath := b.ConfigPath(); cfgPath != "" {
		args = append(args, cfgPath)
	}
	args = append(args, "--http-port", strconv.Itoa(basePort))
	if credsPath != "" {
		args = append(args, "--credentials-path", credsPath)
	}
	// Path convention duplicated (not imported) from
	// entitlement.TokenFilePath -- see that function's doc comment for why:
	// this package stays entitlement-agnostic, entitlement stays
	// streamhost-agnostic. Always passed; harmless if unused.
	args = append(args, "--entitlement-file", filepath.Join(b.stateDir, "rustshine", "entitlement.token"))
	// This machine's hardware id, exactly as the entitlement token's own
	// `sub` claim was bound to (see entitlement.VerifyForHardware) -- a
	// desktop-entitlement build refuses to start without a matching
	// --hardware-id (see rust-shine's license::entitlement module doc
	// comment for why the token being checked against THIS value, computed
	// independently rather than trusted from the token itself, is what
	// makes a copied token file not work on a different machine). Omitted
	// (not passed as an empty string) if hwid.Get() itself fails -- a
	// desktop-entitlement build then fails closed on its own missing-flag
	// check rather than this package silently sending an empty match-nothing
	// value.
	if id, err := hwid.Get(); err == nil {
		args = append(args, "--hardware-id", id)
	} else {
		log.Printf("[streamhost] warning: could not determine hardware id, rustshine entitlement will fail to start: %v", err)
	}
	// Authenticates rustshine's own native WebRTC signaling endpoint
	// (POST /webrtc/offer) against this agent's master key -- see
	// SetSharedSecret's doc comment for how/when this gets set. Omitted
	// entirely (not passed as an empty string) when unset, matching
	// gamestream-server's own --webrtc-shared-secret doc comment: absent
	// means "standalone/dev use, endpoint stays unauthenticated" there,
	// not "authenticate against an empty secret". Read directly (not via
	// SetSharedSecret's own locking) -- Start already holds b.mu for its
	// whole duration, see the top of this function.
	if len(b.sharedSecret) > 0 {
		args = append(args, "--webrtc-shared-secret", string(b.sharedSecret))
	}
	if b.webrtcDisabled {
		args = append(args, "--webrtc-disable")
	}

	launchDir := filepath.Dir(launchPath)

	// Crash-loop backoff: if the previous launch died almost immediately
	// (< rustshineCrashLoopThreshold alive), sleep an increasing delay
	// before trying again instead of respawning at whatever rate onExit
	// gets invoked at. See crashStreak's doc comment on the struct for why
	// this matters -- both to stop a deterministically-failing binary from
	// pegging a CPU core / the log file, and to give a concurrent
	// entitlement.StageRustShine update (which needs the exe to sit still
	// long enough for os.Rename to land on Windows) an actual window.
	// crashStreak is reset once a launch survives past the threshold (see
	// watchProcessExit), so a real transient crash still recovers quickly.
	if b.crashStreak > 0 {
		delay := rustshineCrashBackoff(b.crashStreak)
		log.Printf("[rustshine] %d consecutive near-instant exits, backing off %s before retrying", b.crashStreak, delay)
		time.Sleep(delay)
	}

	// Shared log destination for both launch paths below. Truncates a log
	// that's grown past a sane cap instead of appending forever -- this
	// file used to reach 1.3GB+ across long-running agent uptimes even
	// before the noisy-line filter existed, since every start appends on
	// top of whatever the previous run(s) left behind with no rotation at
	// all. A fresh start is a natural, low-risk point to reset it: nothing
	// still running depends on the old content, and the alternative (real
	// size-based rotation with .1/.2/... suffixes) is more machinery than
	// a debug log needs.
	var logDest *noisyLineFilterWriter
	if b.logPath != "" {
		const rustshineLogMaxBytesBeforeTruncate = 20 * 1024 * 1024
		if info, statErr := os.Stat(b.logPath); statErr == nil && info.Size() > rustshineLogMaxBytesBeforeTruncate {
			if err := os.Truncate(b.logPath, 0); err != nil {
				log.Printf("[rustshine] failed to truncate oversized log %s (%d bytes): %v", b.logPath, info.Size(), err)
			} else {
				log.Printf("[rustshine] truncated oversized log %s (was %d bytes)", b.logPath, info.Size())
			}
		}
		if err := os.MkdirAll(filepath.Dir(b.logPath), 0o755); err != nil {
			log.Printf("[rustshine] failed to create log dir for %s: %v", b.logPath, err)
		} else if f, err := os.OpenFile(b.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err != nil {
			log.Printf("[rustshine] failed to open log file %s: %v", b.logPath, err)
		} else {
			logDest = newNoisyLineFilterWriter(f)
		}
	}

	var proc rustshineProcess
	if useSessionBroker() {
		sp, err := b.startViaSessionBroker(launchPath, args, launchDir, logDest)
		if err != nil {
			if errors.Is(err, errNoActiveSessionMarker) {
				// Nobody has reached the logon screen's active session
				// yet (fresh boot, fast-user-switch mid-transition, ...).
				// Not a failure -- Start() gets called again on the next
				// sunshineWatchdog tick, and immediately on the next
				// WTS_SESSION_LOGON/WTS_CONSOLE_CONNECT via
				// app.NotifySessionChange -- so this quietly waits rather
				// than logging a "failure" every 15s until someone logs in.
				log.Printf("[rustshine] no active console session yet, deferring start")
				return nil
			}
			return err
		}
		proc = sp
	} else {
		// If a capability-granted sunshine-capexec launcher is set (Linux
		// KMS capture only — see SetCapExecPath), launch gamestream-server
		// through it so it inherits CAP_SYS_ADMIN via ambient capabilities
		// instead of carrying a file capability itself, which would break
		// its RPATH-based library resolution. Mirrors
		// sunshineBackend.Start()'s identical branch.
		var cmd *exec.Cmd
		if b.capExecPath != "" {
			cmd = exec.Command(b.capExecPath, append([]string{launchPath}, args...)...)
		} else {
			cmd = exec.Command(launchPath, args...)
		}
		configureRustshineProcess(cmd)
		if launchDir != "" && launchDir != "." {
			cmd.Dir = launchDir
		}
		if logDest != nil {
			cmd.Stdout = logDest
			cmd.Stderr = logDest
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		log.Printf("[rustshine] started pid=%d launch=%s", cmd.Process.Pid, launchPath)
		rustshineAfterStart(b, cmd)
		proc = execCmdProcess{cmd}
	}

	b.launchPath = launchPath
	b.proc = proc
	b.lastLaunchAt = time.Now()
	go b.watchProcessExit(proc)

	return nil
}

// rustshineCrashLoopThreshold is how long a launch must stay alive to be
// considered a real run rather than an instant failure (a wrong CLI flag, a
// missing DLL, ...). Comfortably above gamestream-server's own startup work
// (device/codec probing etc., normally well under a second) so a genuinely
// slow-but-successful boot never gets mistaken for a crash.
const rustshineCrashLoopThreshold = 2 * time.Second

// rustshineCrashBackoff maps a consecutive-near-instant-exit count to a
// delay before the next retry: 0.5s, 1s, 2s, 4s, ... capped at 30s so a
// deterministically-failing binary still gets retried often enough to
// recover promptly once whatever's wrong (e.g. a stale staged build) is
// fixed out from under it, without pegging a CPU core or the log file in
// the meantime.
func rustshineCrashBackoff(streak int) time.Duration {
	const maxBackoff = 30 * time.Second
	d := 500 * time.Millisecond
	for i := 1; i < streak && d < maxBackoff; i++ {
		d *= 2
	}
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}

// startViaSessionBroker launches gamestream-server into the active console
// session (see sessionBrokerLaunch's doc comment) instead of directly under
// this (Session-0-bound) service process. logDest, if non-nil, receives
// gamestream-server's stdout/stderr through an OS pipe -- CreateProcessAsUser
// hands the child a real inherited file handle, so there's no Go-side
// io.Writer to redirect into the way exec.Cmd.Stdout/Stderr work; a pipe
// read-end pumped into logDest on a background goroutine gets the same
// noisy-line filtering and log-truncation behavior back for this path too.
func (b *rustshineBackend) startViaSessionBroker(launchPath string, args []string, workDir string, logDest *noisyLineFilterWriter) (rustshineProcess, error) {
	var stdout, stderr *os.File
	if logDest != nil {
		pr, pw, err := os.Pipe()
		if err != nil {
			return nil, fmt.Errorf("create log pipe: %w", err)
		}
		stdout, stderr = pw, pw
		go func() {
			_, _ = io.Copy(logDest, pr)
			_ = pr.Close()
		}()
		defer pw.Close() // our copy; the child inherits its own duplicate at launch
	}
	proc, err := sessionBrokerLaunch(launchPath, args, workDir, stdout, stderr)
	if err != nil {
		return nil, err
	}
	log.Printf("[rustshine] started (session-broker) pid=%d launch=%s", proc.Pid(), launchPath)
	return proc, nil
}

func (b *rustshineBackend) watchProcessExit(proc rustshineProcess) {
	err := proc.Wait()
	log.Printf("[rustshine] process exited: %v", err)
	b.mu.Lock()
	wasOurs := b.proc == proc
	if wasOurs {
		b.proc = nil
		// Track near-instant exits so the next Start() backs off instead of
		// respawning as fast as onExit gets invoked -- see crashStreak's
		// doc comment on the struct.
		if !b.lastLaunchAt.IsZero() && time.Since(b.lastLaunchAt) < rustshineCrashLoopThreshold {
			b.crashStreak++
		} else {
			b.crashStreak = 0
		}
	}
	onExit := b.onExit
	b.mu.Unlock()
	// Fires outside the lock -- onExit (see SetOnExit) is app.startSunshine,
	// which itself calls back into Start() and takes this same mutex;
	// calling it while still holding b.mu here would deadlock.
	if wasOurs && onExit != nil {
		onExit()
	}
}

// SetOnExit registers a callback fired the instant watchProcessExit notices
// this backend's own child process has died (any reason: a normal Stop(),
// a crash, or capture_kms's own X11 IOErrorHandler aborting on a dead
// connection -- see that handler's doc comment). Without this, nothing
// actually restarts a crashed gamestream-server until app.sunshineWatchdog's
// own next periodic tick -- confirmed live, that meant up to
// sunshineWatchdogInterval (15s) of a client staring at a frozen stream
// after every crash, even though this backend itself knew the process had
// died essentially instantly via cmd.Wait(). The periodic watchdog still
// runs as a backstop (in case this callback itself is never set, or a
// restart attempt right after exit fails for some transient reason), just
// no longer the *only* path to recovery.
func (b *rustshineBackend) SetOnExit(fn func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onExit = fn
}

// SetUpdateInProgress implements streamhost.UpdatePauser -- see that
// interface's doc comment for why this exists.
func (b *rustshineBackend) SetUpdateInProgress(inProgress bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.updatePaused = inProgress
}

func (b *rustshineBackend) isUpdatePaused() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.updatePaused
}

// Stop terminates a gamestream-server instance started by this backend.
func (b *rustshineBackend) Stop() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	// A deliberate Stop() (switching backends, an update replacing the
	// binary, ...) isn't the crash this backoff exists for -- don't let a
	// prior crash streak delay whatever Start() comes next. watchProcessExit
	// wouldn't count this exit against the streak anyway (b.proc is cleared
	// below before it observes the exit), but reset explicitly so a Start()
	// called before that goroutine even runs doesn't inherit a stale one.
	b.crashStreak = 0
	var err error
	if b.proc != nil {
		log.Printf("[rustshine] stopping pid=%d", b.proc.Pid())
		err = b.proc.Kill()
		b.proc = nil
	} else {
		log.Printf("[rustshine] stopping orphaned process by name")
		if runtime.GOOS == "windows" {
			_ = exec.Command("taskkill", "/F", "/IM", "gamestream-server.exe").Run()
		} else {
			_ = exec.Command("killall", "gamestream-server").Run()
		}
	}
	if b.watchdog != nil && b.watchdog.Process != nil {
		_ = b.watchdog.Process.Kill()
		b.watchdog = nil
	}
	return err
}

// WaitReady polls adminPort until the admin HTTPS listener accepts a
// connection, or the deadline passes. Shares portReachable/adminHost with
// sunshineBackend (same package, same semantics: 127.0.0.1 only).
func (b *rustshineBackend) WaitReady(adminPort int, deadline time.Duration) bool {
	if adminPort <= 0 {
		return true
	}
	start := time.Now()
	for time.Since(start) < deadline {
		if portReachable(adminPort, 250*time.Millisecond) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// Ports returns gamestream-server's fixed TCP ports (HTTPS, HTTP, admin
// HTTPS, RTSP) and UDP ports (video, control/ENet, audio) relative to its
// --http-port base — confirmed identical TCP offsets to Sunshine's, but
// only 3 UDP ports (no separate mic port).
func (b *rustshineBackend) Ports(basePort int) (tcp []int, udp []int) {
	tcp = []int{basePort - 5, basePort, basePort + 1, basePort + 21}
	udp = []int{basePort + 9, basePort + 10, basePort + 11}
	return tcp, udp
}

// noisyLinePrefixes match gamestream-server's per-encoded-frame VAAPI debug
// prints -- raw stdout printf-style lines (no timestamp, no log-level,
// unlike its `tracing`-based INFO/WARN lines), one set per frame at up to
// ~60/sec: "vaBeginPicture returned 0", "VPP vaRenderPicture success",
// "vpp_out_surface: N, ...", "encode_surface returned",
// "Encoded frame size: N bytes (looks good)", and so on. None of these
// carry any diagnostic value beyond confirming "yes, another frame was
// encoded" -- they were the entire reason the log file reached 1.3GB.
// Filtered out here at the point they're written, since there's no
// verbosity flag on gamestream-server's own CLI to ask it not to print
// these (see --help). "suspiciously small ... might be a black screen" is
// deliberately NOT in this list -- that's the one line from the same
// prefix that's actually a real diagnostic signal, and stays.
var noisyLinePrefixes = []string{
	"[gamestream-server] va",
	"[gamestream-server] vpp_out_surface:",
	"[gamestream-server] encode_surface returned",
	"[gamestream-server] Encoded frame size:",
	"[gamestream-server] VPP va",
}

func isNoisyRustshineLogLine(line string) bool {
	for _, p := range noisyLinePrefixes {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

// noisyLineFilterWriter wraps the log file destination and drops lines
// matched by isNoisyRustshineLogLine before they ever hit disk. Writes
// from exec.Cmd's stdout/stderr pipe arrive in arbitrary-sized chunks, not
// necessarily line-aligned, so incomplete lines are buffered until a
// newline completes them; any trailing partial line still in the buffer
// when the process exits is simply dropped (acceptable for a debug log --
// not worth the complexity of a Close/Flush path for one possibly-partial
// line).
type noisyLineFilterWriter struct {
	dest io.Writer
	buf  bytes.Buffer
}

func newNoisyLineFilterWriter(dest io.Writer) *noisyLineFilterWriter {
	return &noisyLineFilterWriter{dest: dest}
}

func (w *noisyLineFilterWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.buf.Write(p)
	for {
		data := w.buf.Bytes()
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			break
		}
		line := data[:idx+1]
		if !isNoisyRustshineLogLine(string(bytes.TrimRight(line, "\r\n"))) {
			if _, err := w.dest.Write(line); err != nil {
				return n, err
			}
		}
		w.buf.Next(idx + 1)
	}
	return n, nil
}
