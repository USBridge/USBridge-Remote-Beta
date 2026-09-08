//go:build windows

package streamhost

import (
	"bufio"
	"log"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"usbridge_agent/internal/sessionlaunch"
)

// rustshineDeviceLineRe matches one row of
// `gamestream-server.exe --list-capture-devices` on Windows: confirmed
// columns, in order, are index (u32), device_name, adapter_name, vendor
// (hex u32), resolution ("{width}x{height}"). The exact whitespace/column
// separator wasn't pinned down beyond "plain text table" — this assumes
// whitespace-separated fields with the resolution as a trailing "WxH" token
// and should be checked against real output before relying on it.
var rustshineDeviceLineRe = regexp.MustCompile(`^\s*(\d+)\s+(.+?)\s+(\S+)\s+(0x[0-9a-fA-F]+)\s+(\d+)x(\d+)\s*$`)

// ListCaptureDevices shells out to gamestream-server.exe
// --list-capture-devices (implemented via capture_dxgi::enumerate_monitors)
// and parses each row. Key is left "" since Windows identifies monitors
// directly by index (via the "monitor_index" config key, see
// rustshine_config.go's SetOutputName) rather than by name correlation.
func (b *rustshineBackend) ListCaptureDevices() []CaptureDevice {
	binPath := b.BinaryPath()
	if binPath == "" {
		return nil
	}
	if b.isUpdatePaused() {
		// See streamhost.UpdatePauser's doc comment: an in-flight update
		// needs gamestream-server.exe to stay completely unopened for its
		// stop-download-rename sequence to land -- confirmed live as a real
		// failure mode, not just a theoretical one: even with the main
		// process's own restart race already closed, a --list-capture-devices
		// spawned by an ordinary /api/video/devices poll (a client's GUI
		// keeps making these regardless of what the update flow is doing)
		// could still win a race against the rename by momentarily
		// re-locking the file the instant the main process released it.
		// Nothing downstream distinguishes "no devices reported yet" from
		// "genuinely none" here, so a caller polling during the (brief)
		// update window just sees a transient empty/stale result, same as
		// it would if this call happened to land a few hundred ms earlier.
		log.Printf("[rustshine] --list-capture-devices skipped -- update in progress")
		return nil
	}

	var out []byte
	if useSessionBroker() {
		// Same reasoning as Start()'s session-broker branch: this process
		// (the USBridgeAgent service) is confined to Session 0, so a plain
		// exec.Command here would shell out --list-capture-devices from
		// Session 0 too and get the same wrong/virtualized monitor list
		// Start() used to (confirmed live) -- this is exactly what fed the
		// "only 2 resolutions" list into /api/video/info's available_devices
		// even after Start()'s own launch was already fixed to use the
		// session broker, since this is a wholly separate subprocess call.
		o, err := sessionlaunch.RunAndCaptureOutput(binPath, []string{"--list-capture-devices"}, filepath.Dir(binPath), gamestreamServerCompatEnv, 3*time.Second)
		if err != nil {
			log.Printf("[rustshine] --list-capture-devices via session broker failed: %v", err)
			return nil
		}
		out = o
	} else {
		ctx, cancel := execTimeout(3 * time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binPath, "--list-capture-devices")
		configureRustshineProcess(cmd) // hide the console window this pops up
		o, err := cmd.Output()
		if err != nil {
			return nil
		}
		out = o
	}

	var devices []CaptureDevice
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		m := rustshineDeviceLineRe.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		width, _ := strconv.Atoi(m[5])
		height, _ := strconv.Atoi(m[6])
		devices = append(devices, CaptureDevice{
			OutputName:  m[1], // monitor_index expects the numeric index, stringified
			DisplayName: strings.TrimSpace(m[2]),
			Width:       width,
			Height:      height,
		})
	}
	return devices
}

// firstKmsCardPath: KMS/DRM is Linux-only — see rustshine_devices_linux.go.
func (b *rustshineBackend) firstKmsCardPath() string { return "" }
