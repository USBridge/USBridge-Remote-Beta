//go:build !windows

package streamhost

import "fmt"

// isAccessDenied, elevatedKillByPID and elevatedKillByName are Windows-only
// concepts (UAC elevation) — see elevate_windows.go. On macOS/Linux the
// agent runs as the same user that launched Sunshine/RustShine in the first
// place (or as root), so an access-denied kill isn't something a UAC-style
// re-prompt could fix the same way; callers just fall back to their
// existing behavior.
func isAccessDenied(err error) bool { return false }

func elevatedKillByPID(pid int) error {
	return fmt.Errorf("elevated kill not supported on this platform")
}

func elevatedKillByName(imageName string) error {
	return fmt.Errorf("elevated kill not supported on this platform")
}
