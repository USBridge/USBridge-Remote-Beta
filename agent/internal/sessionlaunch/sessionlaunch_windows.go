//go:build windows

// Package sessionlaunch launches a child process inside the currently
// active console session's interactive desktop, even when the calling
// process (this agent, running as the USBridgeAgent Windows service) lives
// in the non-interactive Session 0.
//
// Why this exists: a LocalSystem service is permanently confined to
// Session 0, which has no visible desktop of its own and is never the
// session a logged-in user's desktop lives in — logging in does not move
// an already-running service into the user's session, this isolation is
// permanent for the service's whole lifetime (see Microsoft's "Session 0
// Isolation" docs). gamestream-server's DXGI Desktop Duplication capture
// (capture_dxgi::enumerate_monitors) needs to run inside that real
// interactive session to see real monitors/resolutions; started from
// Session 0 it falls back to a generic, wrong resolution list and produces
// no video. Confirmed live on this exact machine: running
// gamestream-server.exe --list-capture-devices from the interactive user
// session reports the real monitors (e.g. 2560x1600 x2); the same binary
// launched by the LocalSystem service is stuck on a 2-item fallback list.
//
// LaunchInActiveSession re-homes the child process into that session by
// duplicating the active console session's user token and calling
// CreateProcessAsUser with lpDesktop="winsta0\\default", the same
// mechanism Windows' own Task Scheduler uses for "run only when user is
// logged on" tasks. It needs SeTcbPrivilege enabled on the caller's token,
// which LocalSystem services hold but must explicitly enable via
// AdjustTokenPrivileges (present-but-disabled by default).
package sessionlaunch

import (
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ActiveConsoleSessionID returns the session ID currently attached to the
// physical console, or false if none is (e.g. nobody has reached the logon
// screen's session yet, or the console is disconnected — WTS reports this
// as session ID 0xFFFFFFFF).
func ActiveConsoleSessionID() (uint32, bool) {
	id := windows.WTSGetActiveConsoleSessionId()
	if id == 0xFFFFFFFF {
		return 0, false
	}
	return id, true
}

// enableSeTcbPrivilege enables SE_TCB_NAME ("Act as part of the operating
// system") on the current process's token, which WTSQueryUserToken
// requires. LocalSystem's token carries this privilege but it starts
// disabled, same as most powerful privileges on a fresh token — it must be
// turned on for this call, not just held.
func enableSeTcbPrivilege() error {
	var procToken windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &procToken); err != nil {
		return fmt.Errorf("OpenProcessToken: %w", err)
	}
	defer procToken.Close()

	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr("SeTcbPrivilege"), &luid); err != nil {
		return fmt.Errorf("LookupPrivilegeValue(SeTcbPrivilege): %w", err)
	}

	tp := windows.Tokenprivileges{
		PrivilegeCount: 1,
		Privileges: [1]windows.LUIDAndAttributes{
			{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED},
		},
	}
	if err := windows.AdjustTokenPrivileges(procToken, false, &tp, 0, nil, nil); err != nil {
		return fmt.Errorf("AdjustTokenPrivileges(SeTcbPrivilege): %w", err)
	}
	// AdjustTokenPrivileges can report success (nil err) while still not
	// having applied anything if the privilege isn't held at all --
	// GetLastError distinguishes that case (ERROR_NOT_ALL_ASSIGNED).
	if err := windows.GetLastError(); err != nil && err != windows.ERROR_SUCCESS {
		return fmt.Errorf("AdjustTokenPrivileges(SeTcbPrivilege): not all privileges assigned: %w", err)
	}
	return nil
}

// buildSystemSessionToken returns a primary token that is (a) SYSTEM's own
// identity/privileges and (b) stamped with sessionID, so a process launched
// with it via CreateProcessAsUser both lands in the right session (for
// DXGI/GPU/audio access, same reason LaunchInActiveSession exists at all --
// see this package's doc comment) and, critically, is actually *allowed* to
// call OpenInputDesktop on WinSta0\Winlogon or a UAC secure desktop.
//
// This used to duplicate the session's real logged-in user's own token
// instead (via WTSQueryUserToken), which is enough for the ordinary
// unlocked desktop but not for anything past it: Winlogon and the anonymous
// UAC secure desktop are deliberately locked down to SYSTEM only -- an
// ordinary user token gets ERROR_ACCESS_DENIED from OpenInputDesktop there
// no matter what privileges it holds, by design (that lockdown *is* Secure
// Desktop's whole security property). It also fails outright with no
// logged-in user in the target session at all (a bare logon screen,
// WTSQueryUserToken returns ERROR_NO_TOKEN). Stamping SYSTEM's own token
// with the target SessionId instead covers every case uniformly: bare logon
// screen, ordinary logged-in desktop, and locked/UAC secure desktop -- the
// same architecture RustDesk's Windows backend uses for exactly this reason
// (see WindowsService/create_process_as_user in their src/platform/windows.rs).
// capture_dxgi's attach_input_desktop (crates/capture-dxgi/src/windows.rs)
// and enet-input's matching helper (crates/enet-input/src/sendinput.rs) are
// the other half of this: they re-resolve and attach to whatever desktop is
// actually active, at runtime, on the SYSTEM identity this now provides.
func buildSystemSessionToken(sessionID uint32) (windows.Token, error) {
	var procToken windows.Token
	if err := windows.OpenProcessToken(
		windows.CurrentProcess(),
		windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ADJUST_DEFAULT|windows.TOKEN_ASSIGN_PRIMARY,
		&procToken,
	); err != nil {
		return 0, fmt.Errorf("OpenProcessToken: %w", err)
	}
	defer procToken.Close()

	var primaryToken windows.Token
	if err := windows.DuplicateTokenEx(
		procToken,
		windows.MAXIMUM_ALLOWED,
		nil,
		windows.SecurityImpersonation,
		windows.TokenPrimary,
		&primaryToken,
	); err != nil {
		return 0, fmt.Errorf("DuplicateTokenEx: %w", err)
	}

	sid := sessionID
	if err := windows.SetTokenInformation(
		primaryToken,
		windows.TokenSessionId,
		(*byte)(unsafe.Pointer(&sid)),
		uint32(unsafe.Sizeof(sid)),
	); err != nil {
		primaryToken.Close()
		return 0, fmt.Errorf("SetTokenInformation(TokenSessionId=%d): %w", sessionID, err)
	}
	return primaryToken, nil
}

// Handle wraps a process started via CreateProcessAsUser. exec.Cmd can't
// represent this (Go's os/exec has no CreateProcessAsUser support), so
// this provides the minimal Pid/Kill/Wait surface callers need instead.
type Handle struct {
	pid           uint32
	processHandle windows.Handle
	threadHandle  windows.Handle
}

func (h *Handle) Pid() int { return int(h.pid) }

func (h *Handle) Kill() error {
	return windows.TerminateProcess(h.processHandle, 1)
}

// Wait blocks until the process exits and returns its exit code.
func (h *Handle) Wait() (int, error) {
	s, err := windows.WaitForSingleObject(h.processHandle, windows.INFINITE)
	if err != nil {
		return -1, err
	}
	if s != windows.WAIT_OBJECT_0 {
		return -1, fmt.Errorf("WaitForSingleObject: unexpected status %d", s)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(h.processHandle, &code); err != nil {
		return -1, err
	}
	return int(code), nil
}

func (h *Handle) Close() {
	windows.CloseHandle(h.threadHandle)
	windows.CloseHandle(h.processHandle)
}

// LaunchInActiveSession starts exe (with args) inside the currently active
// console session's interactive desktop, running as SYSTEM stamped into
// that session (not the logged-in user's own token -- see
// buildSystemSessionToken's doc comment for why). workDir may be "".
// stdout/stderr, if non-nil, are inherited by the child (pass *os.File with
// SetInheritable-safe handles -- os.OpenFile-returned files work directly).
// Returns an error wrapping ErrNoActiveSession if nobody is currently
// attached to the console.
func LaunchInActiveSession(exe string, args []string, workDir string, stdout, stderr *os.File, extraEnv map[string]string) (*Handle, error) {
	sessionID, ok := ActiveConsoleSessionID()
	if !ok {
		return nil, ErrNoActiveSession
	}

	if err := enableSeTcbPrivilege(); err != nil {
		return nil, fmt.Errorf("enable SeTcbPrivilege: %w", err)
	}

	primaryToken, err := buildSystemSessionToken(sessionID)
	if err != nil {
		return nil, err
	}
	defer primaryToken.Close()

	var envBlock *uint16
	if err := windows.CreateEnvironmentBlock(&envBlock, primaryToken, false); err != nil {
		return nil, fmt.Errorf("CreateEnvironmentBlock: %w", err)
	}
	defer windows.DestroyEnvironmentBlock(envBlock)

	envPtr := envBlock
	if len(extraEnv) > 0 {
		merged, err := appendEnvVars(envBlock, extraEnv)
		if err != nil {
			return nil, fmt.Errorf("appendEnvVars: %w", err)
		}
		envPtr = merged
	}

	cmdLine := buildCommandLine(exe, args)
	cmdLinePtr, err := windows.UTF16PtrFromString(cmdLine)
	if err != nil {
		return nil, err
	}

	var dirPtr *uint16
	if workDir != "" {
		dirPtr, err = windows.UTF16PtrFromString(workDir)
		if err != nil {
			return nil, err
		}
	}

	desktopPtr, err := windows.UTF16PtrFromString(`winsta0\default`)
	if err != nil {
		return nil, err
	}

	si := windows.StartupInfo{
		Desktop: desktopPtr,
	}
	inheritHandles := false
	if stdout != nil || stderr != nil {
		si.Flags |= windows.STARTF_USESTDHANDLES
		// os.OpenFile's underlying CreateFile call marks the handle
		// non-inheritable by default (Go does this deliberately so a
		// plain os/exec.Command doesn't leak unrelated open files into
		// children) -- CreateProcessAsUser's bInheritHandles=true only
		// propagates handles that are individually flagged inheritable,
		// so without this the child's stdout/stderr silently end up
		// invalid and nothing lands in the file (confirmed live: omitting
		// this produced a launched process with no output at all, no
		// error either, since CreateProcessAsUser itself doesn't
		// validate the handles it's handed).
		if stdout != nil {
			h := windows.Handle(stdout.Fd())
			_ = windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT)
			si.StdOutput = h
		}
		if stderr != nil {
			h := windows.Handle(stderr.Fd())
			_ = windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT)
			si.StdErr = h
		}
		inheritHandles = true
	}
	si.Cb = uint32(unsafe.Sizeof(si))

	var pi windows.ProcessInformation
	const createUnicodeEnvironment = 0x00000400
	const createNoWindow = 0x08000000
	flags := uint32(createUnicodeEnvironment | createNoWindow)

	if err := windows.CreateProcessAsUser(
		primaryToken,
		nil,
		cmdLinePtr,
		nil,
		nil,
		inheritHandles,
		flags,
		envPtr,
		dirPtr,
		&si,
		&pi,
	); err != nil {
		return nil, fmt.Errorf("CreateProcessAsUser: %w", err)
	}

	return &Handle{
		pid:           pi.ProcessId,
		processHandle: pi.Process,
		threadHandle:  pi.Thread,
	}, nil
}

// buildCommandLine quotes exe and args into a single Win32 command line
// string, same convention CreateProcess itself expects (and the same
// quoting exec.Cmd builds internally, duplicated here since
// CreateProcessAsUser takes a raw command line, not argv).
func buildCommandLine(exe string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, syscall.EscapeArg(exe))
	for _, a := range args {
		parts = append(parts, syscall.EscapeArg(a))
	}
	return strings.Join(parts, " ")
}

var ErrNoActiveSession = fmt.Errorf("sessionlaunch: no active console session")

// RunAndCaptureOutput runs exe (with args) inside the active console
// session, like LaunchInActiveSession, but waits for it to exit and returns
// its combined stdout+stderr instead of streaming to caller-supplied files.
// Meant for short, one-shot commands (e.g. gamestream-server's
// --list-capture-devices, which shells out on every device-list refresh —
// this needs the same session re-homing LaunchInActiveSession's caller uses
// for the long-running server itself, or it reports the same
// wrong/virtualized monitor list a Session-0-bound direct exec.Command
// would -- confirmed live), not long-running processes. Kills the child and
// returns an error if it hasn't exited within timeout.
func RunAndCaptureOutput(exe string, args []string, workDir string, extraEnv map[string]string, timeout time.Duration) ([]byte, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create output pipe: %w", err)
	}
	h, err := LaunchInActiveSession(exe, args, workDir, pw, pw, extraEnv)
	pw.Close() // our copy; the child inherits its own duplicate at launch
	if err != nil {
		pr.Close()
		return nil, err
	}
	defer h.Close()

	type result struct {
		out []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		out, readErr := io.ReadAll(pr)
		ch <- result{out, readErr}
	}()

	select {
	case res := <-ch:
		pr.Close()
		_, _ = h.Wait()
		return res.out, res.err
	case <-time.After(timeout):
		_ = h.Kill()
		pr.Close()
		return nil, fmt.Errorf("timed out after %s waiting for %s", timeout, exe)
	}
}

// appendEnvVars returns a new CREATE_UNICODE_ENVIRONMENT-format block
// (VAR=value\0 ... \0\0) built from block's existing entries plus extra,
// with extra's keys overriding any same-named existing entry. block is a
// Win32 environment block as returned by CreateEnvironmentBlock: a single
// UTF-16 buffer of NUL-terminated "VAR=value" strings, itself terminated
// by an extra NUL (i.e. an empty string marks the end).
func appendEnvVars(block *uint16, extra map[string]string) (*uint16, error) {
	entries := []string{}
	seen := make(map[string]bool, len(extra))
	for _, s := range decodeEnvBlock(block) {
		key := s
		if i := indexByte(s, '='); i >= 0 {
			key = s[:i]
		}
		if v, ok := extra[key]; ok {
			entries = append(entries, key+"="+v)
			seen[key] = true
		} else {
			entries = append(entries, s)
		}
	}
	for k, v := range extra {
		if !seen[k] {
			entries = append(entries, k+"="+v)
		}
	}

	var u16 []uint16
	for _, e := range entries {
		u16 = append(u16, windows.StringToUTF16(e)...) // includes the trailing NUL
	}
	u16 = append(u16, 0) // final NUL terminates the whole block
	return &u16[0], nil
}

// decodeEnvBlock splits a Win32 environment block (see appendEnvVars) back
// into individual "VAR=value" strings.
func decodeEnvBlock(block *uint16) []string {
	var out []string
	p := unsafe.Pointer(block)
	for {
		s := windows.UTF16PtrToString((*uint16)(p))
		if s == "" {
			break
		}
		out = append(out, s)
		// advance past this string's UTF-16 units, including the NUL
		// terminator that UTF16FromString's length already counts.
		p = unsafe.Add(p, len(utf16FromString(s))*2)
	}
	return out
}

func utf16FromString(s string) []uint16 {
	// windows.UTF16FromString never errors on a string with no embedded
	// NUL, which env var entries never contain.
	u, _ := windows.UTF16FromString(s)
	return u
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
