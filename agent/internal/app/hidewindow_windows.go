//go:build windows

package app

import (
	"os/exec"
	"syscall"
)

// maybeHideWindow suppresses the console window a console-subsystem child
// (taskkill.exe, tasklist.exe -- both invoked by stopRustShineForUpdate and
// processRunning) would otherwise flash up: this app has no console of its
// own, so without this each such spawn briefly pops a visible window.
// Mirrors client/internal/service/command_windows.go's helper of the same
// name (duplicated rather than shared -- different module, same fix).
func maybeHideWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags = 0x08000000 // CREATE_NO_WINDOW
}
