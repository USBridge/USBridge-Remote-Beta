//go:build !windows

package app

import "os/exec"

// maybeHideWindow is a no-op off Windows -- see hidewindow_windows.go.
func maybeHideWindow(cmd *exec.Cmd) {}
