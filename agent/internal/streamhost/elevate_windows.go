//go:build windows

package streamhost

import (
	"errors"
	"fmt"
	"log"
	"os/exec"

	"golang.org/x/sys/windows"
)

// isAccessDenied reports whether err (from Process.Kill()/exec.Cmd.Run())
// looks like Windows denied the terminate call for lack of privilege — as
// opposed to "already exited" or some other failure elevation wouldn't fix.
// os.Process.Kill() wraps the underlying TerminateProcess failure in an
// *os.SyscallError, so errors.Is unwraps down to the same windows.Errno
// value TerminateProcess actually returned.
func isAccessDenied(err error) bool {
	return err != nil && errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

// elevatedKillByPID retries terminating pid via a UAC-elevated taskkill,
// for when the agent's own (non-elevated) handle to the process lacks
// PROCESS_TERMINATE — typically because the target was started with higher
// privilege than the agent currently has (e.g. a user manually ran
// gamestream-server.exe/sunshine.exe "as Administrator", or the agent lost
// its own elevation across a restart). "Start-Process -Verb RunAs" pops the
// real UAC consent prompt on the active desktop and blocks until the
// elevated taskkill exits, so — same Session-0-isolation caveat documented
// on internal/sessionlaunch — this only works when there's an interactive
// session to show the prompt on; running as a Windows service with nobody
// logged on has nowhere to prompt and will just fail like any other
// elevation request would.
func elevatedKillByPID(pid int) error {
	log.Printf("[streamhost] plain kill of pid=%d denied — requesting elevation (UAC) to retry", pid)
	cmd := exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden", "-Command",
		fmt.Sprintf("Start-Process taskkill -ArgumentList '/F','/PID','%d' -Verb RunAs -Wait", pid))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("elevated kill of pid=%d failed: %w (%s)", pid, err, out)
	}
	return nil
}

// elevatedKillByName is elevatedKillByPID's counterpart for the "orphaned
// process, no PID on hand" path — same UAC prompt, targets by image name.
func elevatedKillByName(imageName string) error {
	log.Printf("[streamhost] plain kill of %s denied — requesting elevation (UAC) to retry", imageName)
	cmd := exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden", "-Command",
		fmt.Sprintf("Start-Process taskkill -ArgumentList '/F','/IM','%s' -Verb RunAs -Wait", imageName))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("elevated kill of %s failed: %w (%s)", imageName, err, out)
	}
	return nil
}
