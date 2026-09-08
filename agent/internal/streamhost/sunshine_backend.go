// sunshineBackend locates the Sunshine (Moonlight GameStream host) binary
// and manages the subset of sunshine.conf that the agent controls. On all
// platforms Sunshine is bundled locally next to the agent binary: as
// Sunshine.app on macOS, sunshine.exe on Windows, and sunshine.AppImage on
// Linux. The agent sets web_bind_address=127.0.0.1 before each start so the
// HTTPS admin UI only listens on localhost (requires itsme228/Sunshine fork).
package streamhost

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AdminUser is the fixed Sunshine web-UI username.
const sunshineAdminUser = "sunshine"

// sunshineProcess abstracts a running Sunshine child process regardless of
// how it was launched -- a plain exec.Cmd (the normal case on every
// platform), or a process re-homed into the active console session via
// internal/sessionlaunch (Windows only, when this agent is itself running
// as the LocalSystem USBridgeAgent service -- see useSunshineSessionBroker's
// doc comment for why that path exists at all). Mirrors rustshineProcess in
// rustshine_backend.go exactly, kept as a separate type only because
// sunshineBackend and rustshineBackend are otherwise fully independent.
type sunshineProcess interface {
	Pid() int
	Kill() error
	Wait() error
}

type sunshineExecCmdProcess struct{ cmd *exec.Cmd }

func (p sunshineExecCmdProcess) Pid() int    { return p.cmd.Process.Pid }
func (p sunshineExecCmdProcess) Kill() error { return p.cmd.Process.Kill() }
func (p sunshineExecCmdProcess) Wait() error { return p.cmd.Wait() }

// useSunshineSessionBroker reports whether Start should launch Sunshine via
// sunshineSessionBrokerLaunch (re-homing it into the active console
// session) instead of as a plain child of this process. Always false except
// on Windows when this agent is itself running as the LocalSystem
// USBridgeAgent service (see sunshine_process_windows.go's init) --
// everywhere else Start()'s own session already has real desktop/display
// access, so the plain exec.Command path works fine.
//
// Why this exists at all: Sunshine enumerates monitors via the Windows CCD
// API (QueryDisplayConfig), which requires access to the calling process's
// window station. A LocalSystem service lives permanently in the
// non-interactive Session 0 (see internal/sessionlaunch's package doc), so
// QueryDisplayConfig fails there with ERROR_ACCESS_DENIED and Sunshine logs
// "Currently available display devices: []", falling back to a single fake
// 1024x768 virtual monitor -- confirmed live on this exact machine.
// rustshineBackend already re-homes gamestream-server into the active
// session for the analogous reason (its DXGI enumeration also needs a real
// session, just for a different underlying API); Sunshine's launch path
// never got the same treatment until now.
var useSunshineSessionBroker = func() bool { return false }

// sunshineSessionBrokerLaunch launches Sunshine inside the currently active
// console session. Set by sunshine_process_windows.go's init on Windows;
// nil (never called, since useSunshineSessionBroker() is always false) on
// every other platform.
var sunshineSessionBrokerLaunch func(exe string, args []string, workDir string, stdout, stderr *os.File) (sunshineProcess, error)

// errSunshineNoActiveSessionMarker lets Start() recognize "no active
// console session yet" (nobody has reached the logon screen, or a fast-
// user-switch is mid-transition) without importing internal/sessionlaunch
// directly -- mirrors errNoActiveSessionMarker in rustshine_backend.go.
var errSunshineNoActiveSessionMarker = fmt.Errorf("sunshine: no active console session")

// sunshineBackend implements Backend against a bundled Sunshine instance.
// All state that used to live as package-level globals is an instance field
// here instead, so a Backend value is self-contained and safe to construct
// more than once (e.g. in tests).
type sunshineBackend struct {
	mu          sync.Mutex
	exeDir      string
	stateDir    string
	launchPath  string
	capExecPath string
	logPath     string
	proc        sunshineProcess
	// watchdog is only used on macOS (see sunshine_process_other.go) — a
	// detached helper process that kills Sunshine if the agent disappears.
	// Left nil, and never referenced, on Linux/Windows where the OS itself
	// (Pdeathsig / a Job Object) enforces the same guarantee.
	watchdog *exec.Cmd
	onExit   func() // see SetOnExit

	// activeAdminPassword holds the per-session randomly generated admin
	// password. Set in Start() via --creds before Sunshine launches.
	activeAdminPassword string

	// windowsDir holds the directory containing sunshine.exe. Only used for
	// legacyDataDir (one-time migration off Sunshine's pre-sunshineDataDir
	// default location, ./config/sunshine.conf next to the exe on Windows)
	// — every other path now comes from sunshineDataDir instead.
	windowsDir string

	supportedCodecsCache struct {
		mu        sync.Mutex
		codecs    []string
		fetchedAt time.Time
	}
}

var _ Backend = (*sunshineBackend)(nil)

// supportedCodecsCacheTTL is documented on SupportedVideoCodecs.
const supportedCodecsCacheTTL = 30 * time.Minute

// NewSunshine constructs the Sunshine-backed Backend implementation. exeDir
// is the agent binary's own directory (used to locate the bundled Sunshine
// tree); stateDir is the agent's persistent state directory (used to stage
// a writable copy of Sunshine when running from a read-only AppImage mount,
// and to persist the admin password across restarts); logPath captures
// Sunshine's stdout/stderr.
func NewSunshine(exeDir, stateDir, logPath string) Backend {
	b := &sunshineBackend{
		exeDir:   exeDir,
		stateDir: stateDir,
		logPath:  logPath,
	}
	b.launchPath = b.runtimeBinaryPath()
	if runtime.GOOS == "windows" && b.launchPath != "" {
		b.windowsDir = filepath.Dir(b.launchPath)
	}
	return b
}

// adminPass returns the in-memory admin password set this session (internal).
func (b *sunshineBackend) adminPass() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.activeAdminPassword
}

// AdminUser returns the fixed Sunshine web-UI username.
func (b *sunshineBackend) AdminUser() string { return sunshineAdminUser }

// AdminPass returns the current session admin password. Falls back to the
// persisted file from the previous session if bootstrap is still in
// progress (it waits up to 20s for Sunshine to start).
func (b *sunshineBackend) AdminPass() string {
	if p := b.adminPass(); p != "" {
		return p
	}
	if pf := b.adminPassFile(); pf != "" {
		if data, err := os.ReadFile(pf); err == nil {
			if p := strings.TrimSpace(string(data)); p != "" {
				return p
			}
		}
	}
	return ""
}

// adminPassFile returns the path where the current admin password is
// persisted so the next launch can use it to rotate to a new one.
func (b *sunshineBackend) adminPassFile() string {
	cp := b.ConfigPath()
	if cp == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(cp), "usbridge_admin_pass")
}

// generatePassword creates a cryptographically-random 20-character hex
// password. crypto/rand.Read never fails on supported OS (macOS/Linux/
// Windows all provide a reliable entropy source), so no fallback to a known
// string is needed.
func generatePassword() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		// Should be unreachable: OS-level RNG failure is fatal.
		log.Panicf("[sunshine] crypto/rand unavailable: %v", err)
	}
	return hex.EncodeToString(b)
}

// binaryPath returns the path to the sunshine binary, or "" if it can't be
// found (not installed/bundled, or unsupported OS).
func (b *sunshineBackend) binaryPath() string {
	exeDir := b.exeDir
	switch runtime.GOOS {
	case "linux":
		// Bundled alongside agent (cmake install tree: sunshine/usr/bin/sunshine).
		// Built with SUNSHINE_BUILD_APPIMAGE=ON — assets are relative to cwd.
		local := filepath.Join(exeDir, "sunshine", "usr", "bin", "sunshine")
		if _, err := os.Stat(local); err == nil {
			return local
		}
		// Inside our AppImage: both binaries share usr/bin/ under $APPDIR.
		inBin := filepath.Join(exeDir, "sunshine")
		if info, err := os.Stat(inBin); err == nil && !info.IsDir() {
			return inBin
		}
		// A previously-staged runtime copy (see stageSunshineRuntime) is still
		// our own bundled build, just relocated out of a read-only AppImage
		// mount — prefer it over anything on system PATH. This also covers
		// dev/test runs of the agent binary launched from outside its normal
		// install tree (e.g. /tmp), where neither check above finds anything,
		// but a real bundled binary was staged here by an earlier run.
		if b.stateDir != "" {
			staged := filepath.Join(b.stateDir, "sunshine-runtime", "usr", "bin", "sunshine")
			if info, err := os.Stat(staged); err == nil && !info.IsDir() {
				return staged
			}
		}
		// Last resort: a system-installed sunshine (e.g. an apt/distro
		// package). This is NOT built from our fork — it will have upstream
		// defaults (tray icon enabled, no web_bind_address localhost patch,
		// etc.) — so only ever reached when no bundled copy exists anywhere.
		if path, err := exec.LookPath("sunshine"); err == nil {
			log.Printf("[sunshine] WARNING: no bundled Sunshine found near %s or in staged runtime; falling back to system PATH: %s (this is NOT our fork's build and may misbehave, e.g. show a tray icon)", exeDir, path)
			return path
		}
		return ""
	case "windows":
		return filepath.Join(exeDir, "sunshine", "sunshine.exe")
	case "darwin":
		return filepath.Join(exeDir, "sunshine", "Sunshine.app", "Contents", "MacOS", "sunshine")
	default:
		return ""
	}
}

// capExecPathFor returns the path to the bundled sunshine_capexec launcher
// (cmd/sunshine_capexec), or "" if not bundled (non-Linux, or a dev build
// without the AppImage layout). This is what actually carries the
// CAP_SYS_ADMIN file capability for KMS screen capture — never sunshine
// itself, since a file capability on sunshine would break its RPATH-based
// dependency resolution. See RequestKMSCapture in internal/permissions.
func (b *sunshineBackend) capExecPathFor() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	p := filepath.Join(b.exeDir, "sunshine-capexec")
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		return p
	}
	return ""
}

// runtimeCapExecPath returns the path sunshine_capexec should actually be
// setcap'd and launched from, mirroring runtimeBinaryPath: inside an
// AppImage the bundled copy lives on the read-only squashfs mount, so
// pkexec setcap needs the writable staged copy instead. Shares the same
// staging pass as runtimeBinaryPath — both binaries are copied together by
// stageSunshineRuntime — so the two are consistent as long as both are
// called while stageSunshineRuntime's staleness check (keyed off the
// sunshine binary) still holds.
func (b *sunshineBackend) runtimeCapExecPath() string {
	capexecSrc := b.capExecPathFor()
	sunshineSrc := b.binaryPath()
	if runtime.GOOS != "linux" || capexecSrc == "" || sunshineSrc == "" || b.stateDir == "" {
		return capexecSrc
	}
	if os.Getenv("APPIMAGE") == "" {
		return capexecSrc
	}
	if _, err := stageSunshineRuntime(sunshineSrc, b.stateDir); err != nil {
		log.Printf("[sunshine] failed to stage writable copy for KMS setcap: %v", err)
		return capexecSrc
	}
	return filepath.Join(b.stateDir, "sunshine-runtime", "usr", "bin", "sunshine-capexec")
}

// runtimeBinaryPath returns the path Sunshine should actually be launched
// from, and the path `setcap` should target for KMS capture. On Linux, when
// running from inside an AppImage, the bundled binary lives on the
// squashfs/FUSE mount that the AppImage runtime sets up — which is
// read-only, so `pkexec setcap` on it always fails silently (the pkexec
// prompt succeeds, but the capability is never actually written). To fix
// that, the bundled Sunshine tree is copied once into stateDir, which is a
// normal writable directory, and that copy is used instead.
func (b *sunshineBackend) runtimeBinaryPath() string {
	src := b.binaryPath()
	if runtime.GOOS != "linux" || src == "" || b.stateDir == "" {
		return src
	}
	if os.Getenv("APPIMAGE") == "" {
		// Not running from an AppImage mount — the bundled path is already
		// on a normal writable filesystem.
		return src
	}
	dst, err := stageSunshineRuntime(src, b.stateDir)
	if err != nil {
		log.Printf("[sunshine] failed to stage writable copy for KMS setcap: %v", err)
		return src
	}
	return dst
}

// stageSunshineRuntime copies the bundled Sunshine binary, its usr/local/assets
// (shaders/web UI/config templates), and its usr/lib (shared libraries bundled
// by linuxdeploy, which the binary's RPATH=$ORIGIN/../lib resolves against)
// from src's read-only AppImage mount into stateDir/sunshine-runtime, skipping
// the copy if a matching one is already there (compared by size + mtime).
// Omitting usr/lib here would leave the staged binary unable to resolve its
// bundled dependencies (e.g. libminiupnpc.so.17) once it's copied outside the
// AppImage mount, since RPATH is resolved relative to the binary's own path.
func stageSunshineRuntime(src, stateDir string) (string, error) {
	root := filepath.Join(stateDir, "sunshine-runtime")
	dstBin := filepath.Join(root, "usr", "bin", "sunshine")

	srcInfo, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	if dstInfo, err := os.Stat(dstBin); err == nil &&
		dstInfo.Size() == srcInfo.Size() && dstInfo.ModTime().Equal(srcInfo.ModTime()) {
		return dstBin, nil
	}

	// Build the whole tree in a private tmp dir first and only make it
	// visible at `root` via a final rename, instead of RemoveAll(root) +
	// copy-in-place. Two agent instances (e.g. a locally built AppImage and
	// the downloaded release, or two copies of the same one launched by
	// mistake) can both reach this point around the same time; with
	// copy-in-place, one instance's RemoveAll could fire while a Sunshine
	// process spawned by the OTHER instance was mid-read of these same
	// shader/asset files, handing it truncated/empty content (the
	// "ConvertUV.frag: syntax error, unexpected end of file" corruption).
	// Building off to the side means `root` is always either the prior
	// complete tree or the new one, never a partially-written mix.
	tmpRoot, err := os.MkdirTemp(stateDir, "sunshine-runtime.tmp-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpRoot) // no-op once renamed into place below

	tmpBin := filepath.Join(tmpRoot, "usr", "bin", "sunshine")
	if err := os.MkdirAll(filepath.Dir(tmpBin), 0o755); err != nil {
		return "", err
	}
	if err := copyFile(src, tmpBin, srcInfo.Mode()); err != nil {
		return "", err
	}
	if err := os.Chtimes(tmpBin, srcInfo.ModTime(), srcInfo.ModTime()); err != nil {
		return "", err
	}

	// usr/local/assets and usr/lib sit 3 dirs up from usr/bin/sunshine in the
	// source tree (see Start()'s cwd handling, which relies on the same layout).
	appDir := filepath.Dir(filepath.Dir(filepath.Dir(src)))
	srcAssets := filepath.Join(appDir, "usr", "local", "assets")
	if info, err := os.Stat(srcAssets); err == nil && info.IsDir() {
		if err := copyDir(srcAssets, filepath.Join(tmpRoot, "usr", "local", "assets")); err != nil {
			return "", err
		}
	}
	srcLib := filepath.Join(appDir, "usr", "lib")
	if info, err := os.Stat(srcLib); err == nil && info.IsDir() {
		if err := copyDir(srcLib, filepath.Join(tmpRoot, "usr", "lib")); err != nil {
			return "", err
		}
	}

	// sunshine_capexec (cmd/sunshine_capexec) sits alongside sunshine in
	// usr/bin — stage it too so runtimeCapExecPath's writable copy exists
	// for pkexec setcap.
	srcCapExec := filepath.Join(appDir, "usr", "bin", "sunshine-capexec")
	if info, err := os.Stat(srcCapExec); err == nil && !info.IsDir() {
		if err := copyFile(srcCapExec, filepath.Join(tmpRoot, "usr", "bin", "sunshine-capexec"), info.Mode()); err != nil {
			return "", err
		}
	}

	// Swap the fully-built tree into place. os.Rename is atomic when both
	// paths are on the same filesystem (guaranteed: both under stateDir),
	// but can't replace a non-empty directory, so the old tree has to be
	// removed first — this still leaves a brief window where `root` doesn't
	// exist, but it's a single fast syscall pair rather than the many
	// milliseconds a full asset copy used to hold `root` half-written for.
	if err := os.RemoveAll(root); err != nil {
		return "", err
	}
	if err := os.Rename(tmpRoot, root); err != nil {
		return "", err
	}

	return dstBin, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, target, info.Mode())
	})
}

// DisplayName identifies this backend for display purposes only (GUI
// status, /api/status, logs) — see streamhost.Identity.
func (b *sunshineBackend) DisplayName() string { return "Sunshine (Open Source)" }

// BinaryPath returns the (staged-if-needed) path Sunshine is launched from.
func (b *sunshineBackend) BinaryPath() string { return b.launchPath }

// CapExecPath returns the (staged-if-needed) path to the bundled
// sunshine_capexec launcher, or "" if not present.
func (b *sunshineBackend) CapExecPath() string { return b.runtimeCapExecPath() }

// SetCapExecPath sets the sunshine_capexec launcher path (see
// runtimeCapExecPath) that Start uses to launch Sunshine with CAP_SYS_ADMIN
// when the configured capture mode is "kms". A no-op path (empty, or the
// launcher lacking the capability) just means Start launches Sunshine
// directly, same as before KMS capture was requested/granted.
func (b *sunshineBackend) SetCapExecPath(capExecPath string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.capExecPath = capExecPath
}

// Running reports whether this backend's Sunshine instance is currently alive.
func (b *sunshineBackend) Running() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.proc != nil
}

// Pid returns the running Sunshine process's PID, or 0 if not running.
func (b *sunshineBackend) Pid() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.proc == nil {
		return 0
	}
	return b.proc.Pid()
}

// Start launches Sunshine if it isn't already running (by this backend, or
// reachable on adminPort — e.g. a system-installed Sunshine service). No-op
// if the launch path doesn't exist.
func (b *sunshineBackend) Start(adminPort int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.proc != nil {
		return nil
	}
	if b.launchPath == "" {
		return nil
	}
	if _, err := os.Stat(b.launchPath); err != nil {
		log.Printf("[sunshine] launch path not found, skipping auto-start: %s", b.launchPath)
		return nil
	}
	if adminPort > 0 && portReachable(adminPort, 300*time.Millisecond) {
		log.Printf("[sunshine] admin port %d already reachable, assuming Sunshine is already running", adminPort)
		// This backend never ran --creds this session, so activeAdminPassword
		// is still empty. The already-running Sunshine still has whatever
		// password was baked in via --creds the last time IT was launched
		// fresh, which is exactly what's persisted in adminPassFile — load it
		// so SubmitPIN/ListClients/UnpairClient (which read adminPass()
		// directly, not the file-fallback AdminPass()) don't send an empty
		// password and get every request rejected with 401.
		if pf := b.adminPassFile(); pf != "" {
			if data, err := os.ReadFile(pf); err == nil {
				if pass := strings.TrimSpace(string(data)); pass != "" {
					b.activeAdminPassword = pass
				}
			}
		}
		return nil
	}

	// One-time, copy-only migration from Sunshine's own default config
	// location (shared with any independently-installed standalone
	// Sunshine on the same machine — the actual bug being fixed here) into
	// this agent's own isolated sunshineDataDir. See migrateLegacyData's
	// doc comment for why this is safe to run unconditionally on every
	// Start(): it's a no-op once already migrated (or on a fresh install).
	b.migrateLegacyData()
	if dir := b.sunshineDataDir(); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("[sunshine] warning: could not create %s: %v", dir, err)
		}
	}

	// Pin web_bind_address to 127.0.0.1 so the HTTPS admin UI is only reachable
	// on localhost, independently of bind_address (which restricts streaming ports
	// to the VPN/LAN interface). Requires our itsme228/Sunshine fork.
	if err := b.setConfigKey("web_bind_address", "127.0.0.1"); err != nil {
		log.Printf("[sunshine] warning: could not set web_bind_address: %v", err)
	}

	// On Windows the portable build expects sunshine_state.json to already
	// exist before --creds can write into it; create an empty-but-valid
	// template so the file is there when --creds runs.
	if runtime.GOOS == "windows" {
		if err := b.ensureSunshineStateFile(); err != nil {
			log.Printf("[sunshine] warning: could not pre-create sunshine_state.json: %v", err)
		}
	}

	// Set a fresh random admin password before starting Sunshine so the
	// process always starts with credentials we generated (not a stale or
	// default password). --creds writes directly to sunshine_state.json.
	// sunshineConfigArgs pins every path Sunshine touches (config,
	// sunshine_state.json, apps.json, its own log, TLS keypair) to
	// sunshineDataDir instead of Sunshine's own default resolution — see
	// its doc comment for why the config path must come first, before
	// --creds.
	newPass := generatePassword()
	credsCmd := exec.Command(b.launchPath, append(b.sunshineConfigArgs(), "--creds", sunshineAdminUser, newPass)...)
	configureProcess(credsCmd)
	// Sunshine resolves config paths (including sunshine_state.json) relative
	// to its process CWD on Windows, not its exe path. Without setting Dir,
	// --creds runs from the agent's CWD and writes to the wrong location while
	// the main Sunshine process (which has Dir set below) reads from a different
	// path — the credentials never match.
	if sunshineDir := filepath.Dir(b.launchPath); sunshineDir != "" && sunshineDir != "." {
		credsCmd.Dir = sunshineDir
	}
	if out, err := credsCmd.CombinedOutput(); err != nil {
		log.Printf("[sunshine] --creds failed: %v: %s", err, out)
	} else {
		b.activeAdminPassword = newPass
		if pf := b.adminPassFile(); pf != "" {
			_ = os.WriteFile(pf, []byte(newPass), 0600)
		}
		log.Printf("[sunshine] admin password set (user=%s)", sunshineAdminUser)
	}

	// If a capability-granted sunshine_capexec launcher is set (Linux KMS
	// capture only — see SetCapExecPath), launch Sunshine through it so it
	// inherits CAP_SYS_ADMIN via ambient capabilities instead of carrying a
	// file capability itself, which would break its RPATH-based library
	// resolution. b.capExecPath is only ever set once the capability has
	// actually been granted (internal/app), so this exec is expected to
	// succeed whenever it's used.
	var launchExe string
	var launchArgs []string
	if b.capExecPath != "" {
		launchExe = b.capExecPath
		launchArgs = append([]string{b.launchPath}, b.sunshineConfigArgs()...)
	} else {
		launchExe = b.launchPath
		launchArgs = b.sunshineConfigArgs()
	}

	var launchDir string
	switch runtime.GOOS {
	case "linux":
		// Sunshine built with SUNSHINE_BUILD_APPIMAGE=ON uses ./usr/local/assets
		// relative to cwd. Set cwd to the root of the install tree (3 dirs up from
		// usr/bin/sunshine), which is either the AppImage $APPDIR or the staging dir.
		launchDir = filepath.Dir(filepath.Dir(filepath.Dir(b.launchPath)))
	default:
		// Windows (and macOS) Sunshine resolves assets/ relative to its process
		// cwd, not its own exe path. Without this, launching from the agent
		// (whose own cwd may differ) breaks shader/asset lookup with
		// ERROR_PATH_NOT_FOUND while double-clicking sunshine.exe directly
		// works by accident (Explorer sets cwd to the exe's own folder).
		launchDir = filepath.Dir(b.launchPath)
	}

	var logFile *os.File
	if b.logPath != "" {
		if err := os.MkdirAll(filepath.Dir(b.logPath), 0o755); err != nil {
			log.Printf("[sunshine] failed to create log dir for %s: %v", b.logPath, err)
		} else if f, err := os.OpenFile(b.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err != nil {
			log.Printf("[sunshine] failed to open log file %s: %v", b.logPath, err)
		} else {
			logFile = f
		}
	}

	var proc sunshineProcess
	if useSunshineSessionBroker() {
		sp, err := sunshineSessionBrokerLaunch(launchExe, launchArgs, launchDir, logFile, logFile)
		if err != nil {
			if errors.Is(err, errSunshineNoActiveSessionMarker) {
				// Nobody has reached the logon screen's active session yet
				// (fresh boot, fast-user-switch mid-transition, ...). Not a
				// failure -- Start() gets called again on the next
				// sunshineWatchdog tick, so this quietly waits rather than
				// logging a "failure" every 15s until someone logs in.
				log.Printf("[sunshine] no active console session yet, deferring start")
				return nil
			}
			return err
		}
		log.Printf("[sunshine] started (session-broker) pid=%d launch=%s", sp.Pid(), launchExe)
		proc = sp
	} else {
		cmd := exec.Command(launchExe, launchArgs...)
		configureProcess(cmd)
		if launchDir != "" && launchDir != "." {
			cmd.Dir = launchDir
		}
		if logFile != nil {
			cmd.Stdout = logFile
			cmd.Stderr = logFile
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		log.Printf("[sunshine] started pid=%d launch=%s", cmd.Process.Pid, launchExe)
		afterStart(b, cmd)
		proc = sunshineExecCmdProcess{cmd}
	}

	b.proc = proc
	go b.watchProcessExit(proc)

	return nil
}

// watchProcessExit blocks until proc exits (however it exits — clean
// shutdown, killed by Stop(), or a crash such as the ENet null-pointer-
// dereference bug in Sunshine's host_create) and then clears b.proc, so
// Start()'s "already running, no-op" fast path stops believing a dead
// process is still alive. Without this, a crashed Sunshine can only be
// recovered by restarting the whole agent, since nothing else ever notices
// the child died.
//
// Only clears b.proc if it's still THIS proc: a concurrent Stop() or a
// newer Start() may have already replaced it with a different process, and
// this stale watcher must not clobber that.
func (b *sunshineBackend) watchProcessExit(proc sunshineProcess) {
	err := proc.Wait()
	log.Printf("[sunshine] process exited: %v", err)
	b.mu.Lock()
	wasOurs := b.proc == proc
	if wasOurs {
		b.proc = nil
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
// this backend's own child process has died (any reason: a normal Stop() or
// a crash). Without this, nothing actually restarts a crashed Sunshine until
// app.sunshineWatchdog's own next periodic tick -- up to
// sunshineWatchdogInterval (15s) of a client staring at a frozen stream
// after every crash, even though this backend itself knew the process had
// died essentially instantly via cmd.Wait(). The periodic watchdog still
// runs as a backstop, just no longer the *only* path to recovery.
func (b *sunshineBackend) SetOnExit(fn func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onExit = fn
}

// Stop terminates a Sunshine instance started by this backend. No-op if not
// running or if Sunshine wasn't launched by us (e.g. system service).
func (b *sunshineBackend) Stop() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	var err error
	if b.proc != nil {
		log.Printf("[sunshine] stopping pid=%d", b.proc.Pid())
		err = b.proc.Kill()
		b.proc = nil
	}
	if b.watchdog != nil && b.watchdog.Process != nil {
		// Stop the watchdog too: we're already terminating Sunshine
		// ourselves, and leaving the watchdog running risks it waking up
		// later and killing an unrelated process if that PID gets reused.
		_ = b.watchdog.Process.Kill()
		b.watchdog = nil
	}
	return err
}

func portReachable(port int, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(adminHost(), strconv.Itoa(port)), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// WaitReady polls adminPort until Sunshine's HTTPS/NvHTTP listener (the same
// one the client's Launch()/GetServerInfo() calls hit) accepts a connection,
// or the deadline passes. Start only waits for the OS to fork the process,
// not for Sunshine's own bootstrap (config parse, KMS/Wayland enumeration,
// binding its listeners) to finish — a caller that restarts Sunshine and
// then immediately lets a client reconnect (e.g. switching the captured
// monitor) races that bootstrap. Returns true once reachable, false if it
// never became reachable within deadline.
func (b *sunshineBackend) WaitReady(adminPort int, deadline time.Duration) bool {
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

// adminHost returns the host for Sunshine admin API calls. web_bind_address
// is always set to 127.0.0.1 before Sunshine starts, so the admin HTTPS
// server only listens on localhost.
func adminHost() string { return "127.0.0.1" }

// ensureSunshineStateFile creates sunshine_state.json with a valid template
// if it does not already exist, or if the existing file is corrupt (fails to
// parse as a JSON object). On Windows the portable Sunshine build will not
// create this file itself — --creds only *updates* an existing one, so the
// file must pre-exist with valid JSON or credential bootstrap silently
// no-ops: confirmed live on a machine where this file had somehow been
// zeroed out (265 bytes, all NUL -- exact cause unconfirmed, but plausibly
// an interrupted write from a prior crash/AV scan) -- every subsequent
// --creds run reported success (exit 0, activeAdminPassword and
// usbridge_admin_pass both updated) while sunshine_state.json's mtime never
// moved, because Sunshine's --creds merges into the existing file rather
// than overwriting it wholesale and silently gives up when that file isn't
// parseable JSON. The result: Sunshine's real on-disk username/password
// never changed, every admin API call (including the PIN pairing relay)
// failed Basic Auth ("Web UI: [127.0.0.1] -- not authorized" in Sunshine's
// own log) forever after, and the Moonlight client fell back to showing the
// PIN on-screen for manual entry instead of auto-pairing. A stat-only
// existence check can never catch or heal this -- validate the content too.
//
// uniqueid must be a non-empty UUID: Sunshine treats an empty uniqueid as
// "first run" and regenerates the entire file on startup, wiping any
// credentials that --creds wrote before the main process started.
func (b *sunshineBackend) ensureSunshineStateFile() error {
	stateFile := b.credentialsFilePath()
	if stateFile == "" {
		return nil
	}
	if data, err := os.ReadFile(stateFile); err == nil {
		var probe map[string]any
		if json.Unmarshal(data, &probe) == nil && len(probe) > 0 {
			return nil // already exists and is valid JSON
		}
		// Corrupt: back up for forensics (best-effort) before overwriting so
		// a fresh template can replace it below.
		backup := stateFile + ".corrupt-" + strconv.FormatInt(time.Now().Unix(), 10)
		if err := os.Rename(stateFile, backup); err != nil {
			log.Printf("[sunshine] warning: could not back up corrupt %s to %s: %v", stateFile, backup, err)
		} else {
			log.Printf("[sunshine] %s was corrupt (invalid JSON) -- backed up to %s and regenerating", stateFile, backup)
		}
	}
	if err := os.MkdirAll(filepath.Dir(stateFile), 0o755); err != nil {
		return err
	}
	uid, err := randomUUID()
	if err != nil {
		return err
	}
	content := `{
    "username": "sunshine",
    "salt": "",
    "password": "",
    "root": {
        "uniqueid": "` + uid + `",
        "named_devices": []
    }
}
`
	return os.WriteFile(stateFile, []byte(content), 0o644)
}

// randomUUID returns a random UUID v4 string (xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx).
func randomUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant bits
	return hex.EncodeToString(b[0:4]) + "-" + hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" + hex.EncodeToString(b[10:16]), nil
}

// Ports returns Sunshine's fixed TCP ports (HTTPS, HTTP, web-UI HTTPS, RTSP)
// and UDP ports (video, control, audio, mic) relative to its NvHTTP base
// port (Sunshine's "port" config value), per Sunshine's documented
// port-offset scheme.
func (b *sunshineBackend) Ports(basePort int) (tcp []int, udp []int) {
	tcp = []int{basePort - 5, basePort, basePort + 1, basePort + 21}
	udp = []int{basePort + 9, basePort + 10, basePort + 11, basePort + 13}
	return tcp, udp
}
