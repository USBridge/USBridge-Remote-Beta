//go:build windows

package sasinput

import (
	"fmt"
	"log"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// sasDLL/sendSASProc are resolved lazily (NewLazyDLL/NewProc never touch
// the filesystem until .Call/.Find is actually invoked), so a build/import
// of this package never fails on a machine that happens to lack sas.dll
// (none should exist -- it's shipped with every Windows version since
// Vista -- but there's no reason to make process startup depend on it
// existing either).
var sasDLL = syscall.NewLazyDLL("sas.dll")
var sendSASProc = sasDLL.NewProc("SendSAS")

// SendSecureAttentionSequence calls the undocumented sas.dll!SendSAS(BOOL
// asUser) export to raise a synthetic Ctrl+Alt+Del in whichever session is
// currently active on the console -- the same mechanism RustDesk's Windows
// backend uses (and the same one Windows' own "Ease of Access" on-screen
// keyboard / Narrator use to offer a software Ctrl+Alt+Del button on the
// lock screen). asUser=FALSE: this call is made from the agent's own
// SYSTEM/service identity, not on behalf of/impersonating a particular
// logged-on user, matching RustDesk's own SendSAS(FALSE) and the intended
// "a service is raising this" case SoftwareSASGeneration's "Services" bit
// exists for (see EnsureServicesCanGenerateSAS).
//
// SendSAS returns void in the C API -- there is no success/failure signal
// to check beyond "did the call itself fail to happen" (DLL/proc missing).
// A no-op call (e.g. SoftwareSASGeneration doesn't permit this caller, or
// nothing is listening for SAS right now) fails silently exactly like it
// does for a real hardware Ctrl+Alt+Del press with no consumer -- callers
// needing confirmation must observe the effect (e.g. the lock screen
// actually changing) rather than trust this call's own return.
func SendSecureAttentionSequence() error {
	if err := sendSASProc.Find(); err != nil {
		return fmt.Errorf("sas.dll!SendSAS not found: %w", err)
	}
	// SendSAS takes a single BOOL; golang.org/x/sys/windows' LazyProc.Call
	// takes uintptr args and returns (r1, r2 uintptr, lastErr error) --
	// lastErr is just the errno left over from the underlying syscall
	// return path (SetLastError isn't guaranteed cleared before a void
	// stdcall returns), not a real error signal for this function, so it's
	// deliberately not surfaced as this call's result -- see the doc
	// comment above.
	_, _, _ = sendSASProc.Call(0)
	return nil
}

// EnsureServicesCanGenerateSAS sets the (otherwise off-by-default)
// SoftwareSASGeneration policy so that this process -- a real Windows
// service, per Windows' own bookkeeping (SCM-registered, not just "running
// as SYSTEM") -- is allowed to call SendSAS at all. Without this,
// SendSecureAttentionSequence's call above is accepted but silently does
// nothing: SoftwareSASGeneration defaults to 0 ("None"), and Winlogon
// checks it (plus the caller's own registration as a service/Ease-of-
// Access app) before honoring a software-raised SAS, precisely so an
// arbitrary unprivileged process can't spoof Ctrl+Alt+Del.
//
// Value bits (HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System
// \SoftwareSASGeneration, DWORD): 1 = Services, 2 = Ease of Access
// applications, 3 = both. This only ever *sets* the Services bit (1) --
// never clears the Ease-of-Access bit if an administrator separately
// enabled that, and never touches the key if the Services bit is already
// set (idempotent, no-op on every call after the first).
func EnsureServicesCanGenerateSAS() error {
	const path = `SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`
	const valueName = "SoftwareSASGeneration"
	const servicesBit = 1

	key, _, err := registry.CreateKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open/create %s: %w", path, err)
	}
	defer key.Close()

	current, _, err := key.GetIntegerValue(valueName)
	if err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("read %s: %w", valueName, err)
	}
	if current&servicesBit != 0 {
		return nil // already permitted, nothing to do
	}

	newValue := uint32(current) | servicesBit
	if err := key.SetDWordValue(valueName, newValue); err != nil {
		return fmt.Errorf("set %s=%d: %w", valueName, newValue, err)
	}
	log.Printf("[sasinput] set %s\\%s = %d (added Services bit, was %d) so this service can raise Ctrl+Alt+Del on a locked/secure desktop", path, valueName, newValue, current)
	return nil
}
