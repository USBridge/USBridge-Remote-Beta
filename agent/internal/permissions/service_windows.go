//go:build windows

package permissions

import (
	"fmt"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type Service struct{}

func New() *Service                                      { return &Service{} }
func (s *Service) AccessibilityGranted() bool            { return true }
func (s *Service) ScreenRecordingGranted() bool          { return true }
func (s *Service) RequestAccessibility() bool            { return true }
func (s *Service) RequestScreenRecording() bool          { return true }
func (s *Service) RequestMissing()                       {}
func (s *Service) OpenPrivacySettings() error            { return nil }
func (s *Service) OpenScreenRecordingSettings() error    { return nil }
func (s *Service) KMSCaptureGranted(binPath string) bool { return false }
func (s *Service) RequestKMSCapture(binPath string) bool { return false }

// ClipboardToolAvailable/RequestClipboardTool are Linux-only -- see
// service_linux.go; Windows clipboard sync talks to the Win32 clipboard
// API directly, so there's nothing to install.
func (s *Service) ClipboardToolAvailable() bool    { return true }
func (s *Service) RequestClipboardTool() bool      { return true }
func (s *Service) ClipboardInstallPreview() string { return "" }

// GPUClockLockSupported is always true on Windows -- whether it actually
// *works* on a given machine depends on having an NVIDIA GPU and a driver
// new enough for NVML's clock-lock calls, which
// gamestream-server's own --gpu-clock-lock-daemon mode discovers and
// reports (via its own log / this session's own remember.md) at the point
// it actually tries, not something this package can know in advance.
func (s *Service) GPUClockLockSupported() bool { return true }

// GPUClockLockElevated reports whether *this* process (the agent itself)
// is already running elevated. In practice this is almost always false --
// the agent deliberately never requires elevation just to launch normally
// (see RequestGPUClockLock's own docs for why the lock itself always goes
// through a separate, freshly-elevated helper process instead) -- exposed
// mainly so the UI can decide whether to explain *why* a click is about to
// trigger a UAC prompt.
func (s *Service) GPUClockLockElevated() bool {
	var token windows.Token
	process, err := windows.GetCurrentProcess()
	if err != nil {
		return false
	}
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()

	var elevation uint32
	var outLen uint32
	infoErr := windows.GetTokenInformation(token, windows.TokenElevation, (*byte)(unsafe.Pointer(&elevation)), uint32(unsafe.Sizeof(elevation)), &outLen)
	if infoErr != nil {
		return false
	}
	return elevation != 0
}

var (
	shell32           = syscall.NewLazyDLL("shell32.dll")
	procShellExecuteW = shell32.NewProc("ShellExecuteW")
)

// SW_HIDE keeps the elevated helper's own console window (it's a
// console-subsystem binary) from flashing up — it does nothing but poll a
// PID and hold an NVML lock, there's nothing useful to show.
const swHide = 0

// RequestGPUClockLock launches binPath (gamestream-server.exe) as
// `binPath --gpu-clock-lock-daemon --watch-pid <watchPID>` via
// ShellExecuteW's "runas" verb, which is what actually triggers the UAC
// consent prompt. There is no Windows equivalent of Linux's one-time
// `setcap`/CAP_SYS_ADMIN grant (see permissions.Service.RequestKMSCapture on
// Linux for that contrast) -- NVML's clock-lock calls simply require the
// *calling process itself* to be elevated, so this has to launch a fresh
// elevated helper (and thus show a fresh UAC prompt) every time it's armed.
// Callers should pass a long-lived watchPID (the agent's own, not a
// per-session stream host's) so that only happens once per agent run --
// see app.applyGPUClockLock's doc for why: a UAC prompt runs on the secure
// desktop, unreachable from a remote session, so re-arming on every
// stream-host restart would strand a remote client mid-switch. The daemon
// polls watchPID and drops the lock once it exits, see rust-shine's own
// `platform::windows::run_gpu_clock_lock_daemon` docs. Returns once the
// helper has launched, not once it exits.
func RequestGPUClockLock(binPath string, watchPID int) error {
	dir := filepath.Dir(binPath)
	params := fmt.Sprintf("--gpu-clock-lock-daemon --watch-pid %d", watchPID)

	verbPtr, err := syscall.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	filePtr, err := syscall.UTF16PtrFromString(binPath)
	if err != nil {
		return err
	}
	paramsPtr, err := syscall.UTF16PtrFromString(params)
	if err != nil {
		return err
	}
	dirPtr, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}

	// ShellExecuteW returns an HINSTANCE-shaped value: > 32 on success,
	// <= 32 on failure (this includes the user clicking "No" on the UAC
	// prompt, which comes back as SE_ERR_ACCESSDENIED).
	ret, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verbPtr)),
		uintptr(unsafe.Pointer(filePtr)),
		uintptr(unsafe.Pointer(paramsPtr)),
		uintptr(unsafe.Pointer(dirPtr)),
		uintptr(swHide),
	)
	if ret <= 32 {
		return fmt.Errorf("ShellExecuteW(runas) failed with code %d (the UAC prompt may have been declined)", ret)
	}
	return nil
}

// RequestGPUClockLock is the *Service method the rest of the app calls
// (matching every other permission-request method's shape) -- thin wrapper
// around the package-level function above, which is also called directly
// by streamhost.rustshineBackend.Start (no *Service instance handy there).
func (s *Service) RequestGPUClockLock(binPath string, watchPID int) error {
	return RequestGPUClockLock(binPath, watchPID)
}

var procShellExecuteExW = shell32.NewProc("ShellExecuteExW")

// shellExecuteInfoW mirrors Win32's SHELLEXECUTEINFOW -- unlike plain
// ShellExecuteW (see RequestGPUClockLock above), the Ex form can hand back a
// waitable process handle via hProcess (when fMask carries
// seeMaskNoCloseProcess), which is the whole reason this function exists:
// KillGamestreamServerElevated needs to block until the elevated helper
// actually *exits*, not just launches, before its caller retries staging.
// Field order/types must match the C struct exactly -- Go's default struct
// layout already produces the same padding the unmodified (no #pragma
// pack) Win32 struct gets, as long as every field here has the same size as
// its C counterpart.
type shellExecuteInfoW struct {
	cbSize         uint32
	fMask          uint32
	hwnd           uintptr
	lpVerb         *uint16
	lpFile         *uint16
	lpParameters   *uint16
	lpDirectory    *uint16
	nShow          int32
	hInstApp       uintptr
	lpIDList       uintptr
	lpClass        *uint16
	hkeyClass      uintptr
	dwHotKey       uint32
	hIconOrMonitor uintptr
	hProcess       uintptr
}

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsync        = 0x00000100
)

// KillGamestreamServerElevated force-kills every gamestream-server.exe
// process by image name via an elevated (UAC-prompted)
// `taskkill /F /IM gamestream-server.exe /T` -- the escalation of last
// resort for the one confirmed-live case a same-level taskkill can't reach
// on its own: an orphaned instance (root cause not fully pinned down --
// see agent/internal/app/app.go's stopRustShineForUpdate doc comment for
// the full investigation) that survived even the calling agent's own
// plain, same-user taskkill sweep with "Access is denied", blocking
// RustShine updates indefinitely by keeping the staged binary's rename
// target locked. Unlike RequestGPUClockLock's fire-and-forget daemon
// helper, this blocks (bounded by a fixed timeout) until the elevated
// taskkill actually exits -- the whole point is knowing the file is
// unlocked before the caller retries staging, not just that a prompt
// appeared. A declined UAC prompt or a non-interactive session (no desktop
// for the prompt to land on, e.g. the true Session-0 Windows-service path)
// both surface as an error here; both are non-fatal to the caller, which
// already falls back to relaunching the old binary and retrying at the
// next interval regardless of how this returns.
func (s *Service) KillGamestreamServerElevated() error {
	const killTimeout = 15 * time.Second

	verbPtr, err := syscall.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	filePtr, err := syscall.UTF16PtrFromString("taskkill.exe")
	if err != nil {
		return err
	}
	paramsPtr, err := syscall.UTF16PtrFromString("/F /IM gamestream-server.exe /T")
	if err != nil {
		return err
	}

	info := shellExecuteInfoW{
		fMask:        seeMaskNoCloseProcess | seeMaskNoAsync,
		lpVerb:       verbPtr,
		lpFile:       filePtr,
		lpParameters: paramsPtr,
		nShow:        swHide,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))

	ret, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if ret == 0 {
		return fmt.Errorf("ShellExecuteExW(runas, taskkill.exe): %v (the UAC prompt may have been declined)", callErr)
	}
	if info.hProcess == 0 {
		// Launched but handed back no waitable handle -- nothing more to
		// verify here, but not itself a failure.
		return nil
	}
	h := windows.Handle(info.hProcess)
	defer windows.CloseHandle(h)

	event, waitErr := windows.WaitForSingleObject(h, uint32(killTimeout/time.Millisecond))
	if waitErr == windows.WAIT_TIMEOUT {
		return fmt.Errorf("elevated taskkill did not finish within %s", killTimeout)
	}
	if waitErr != nil {
		return fmt.Errorf("WaitForSingleObject: %w", waitErr)
	}
	if event != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("WaitForSingleObject: unexpected status %d", event)
	}

	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err == nil && code != 0 {
		return fmt.Errorf("elevated taskkill exited with code %d", code)
	}
	return nil
}
