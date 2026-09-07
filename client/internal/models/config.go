package models

import (
	"fmt"
	"strings"
	"time"
)

// DefaultVideoUDPPort — UDP port for receiving RTP H.264. 55000 is in the dynamic range (49152-65535),
// free on Windows (nidmsrv/UPnP often occupy 5000), macOS (AirPlay), Linux, Android.
const DefaultVideoUDPPort = 55000

const (
	ConnectionProtocolAuto      = "auto"
	ConnectionProtocolTailscale = "tailscale"
	ConnectionProtocolDirect    = "direct"

	VideoProtocolMoonlight = "moonlight"
)

// AppConfig application configuration
type AppConfig struct {
	// USBridge 2 connection (as client)
	USBPort    int `json:"usb_port" mapstructure:"usb_port"`       // USBridge 2 port (8080)
	APITimeout int `json:"api_timeout" mapstructure:"api_timeout"` // API request timeout

	ConnectionProtocol string `json:"connection_protocol" mapstructure:"connection_protocol"`

	// Tailscale settings (always runs in userspace/tsnet mode, on every platform)
	TailscaleEnabled bool `json:"tailscale_enabled" mapstructure:"tailscale_enabled"`

	// NBD server (as server)
	NBDPort           int      `json:"nbd_port" mapstructure:"nbd_port"`                         // NBD server port (10809)
	MaxClients        int      `json:"max_clients" mapstructure:"max_clients"`                   // Maximum NBD clients
	ScanPaths         []string `json:"scan_paths" mapstructure:"scan_paths"`                     // Paths to scan for devices
	SupportedTypes    []string `json:"supported_types" mapstructure:"supported_types"`           // Supported file types
	NBDExportReadOnly bool     `json:"nbd_export_read_only" mapstructure:"nbd_export_read_only"` // true = read-only export (safe default), false = RW via overlay

	// Video stream host (set from the address bar; not stored in the config)
	VideoHost     string `json:"-"`
	VideoBindHost string `json:"video_bind_host" mapstructure:"video_bind_host"`

	// Video protocol (always "moonlight")
	VideoProtocol string `json:"video_protocol" mapstructure:"video_protocol"`

	// Video UDP (new protocol)
	VideoUDPPort int `json:"video_udp_port" mapstructure:"video_udp_port"` // UDP port for receiving RTP video (55000 — free on Win/Mac/Linux/Android)

	// Video settings
	VideoBitrate   int  `json:"video_bitrate" mapstructure:"video_bitrate"` // kbps
	VideoWidth     int  `json:"video_width" mapstructure:"video_width"`
	VideoHeight    int  `json:"video_height" mapstructure:"video_height"`
	VideoFPS       int  `json:"video_fps" mapstructure:"video_fps"`
	LowLatencyMode bool `json:"low_latency_mode" mapstructure:"low_latency_mode"` // Low latency mode
	BufferSize     int  `json:"buffer_size" mapstructure:"buffer_size"`           // Frame buffer size
	SkipFrameDelay bool `json:"skip_frame_delay" mapstructure:"skip_frame_delay"` // Skip delays between frames

	// Audio settings
	AudioCodec      string `json:"audio_codec" mapstructure:"audio_codec"`     // Opus, G.711
	AudioBitrate    int    `json:"audio_bitrate" mapstructure:"audio_bitrate"` // kbps
	AudioSampleRate int    `json:"audio_sample_rate" mapstructure:"audio_sample_rate"`
	AudioChannels   int    `json:"audio_channels" mapstructure:"audio_channels"`

	// UI settings
	WindowWidth  int    `json:"window_width" mapstructure:"window_width"`
	WindowHeight int    `json:"window_height" mapstructure:"window_height"`
	Theme        string `json:"theme" mapstructure:"theme"` // light, dark
	LogLevel     string `json:"log_level" mapstructure:"log_level"`

	// Clipboard sync (agent <-> client shared clipboard)
	ClipboardSyncEnabled bool  `json:"clipboard_sync_enabled" mapstructure:"clipboard_sync_enabled"`
	ClipboardMaxBytes    int64 `json:"clipboard_max_bytes" mapstructure:"clipboard_max_bytes"` // cap per image/file payload

	// Local ui.parse offload: answer the MCP ui.parse tool locally (ONNX
	// Runtime on this machine's CPU/Intel iGPU) instead of forwarding to
	// the device's RK3566 NPU -- see internal/localui's package doc
	// comment and internal/api/local_ui_intercept.go for the full
	// rationale (device-side ui.parse tiles DBNet 6x at 1920x1080, ~20s;
	// the same models here finish in well under 5s). Opt-in and off by
	// default: it needs the ONNX models + a libonnxruntime.so present on
	// this machine (see scripts/setup_localui.sh), and isn't validated on
	// every platform/GPU this client runs on yet.
	LocalUIParseEnabled  bool   `json:"local_ui_parse_enabled" mapstructure:"local_ui_parse_enabled"`
	LocalUIParseGPU      bool   `json:"local_ui_parse_gpu" mapstructure:"local_ui_parse_gpu"`             // try a GPU/accelerator execution provider -- CoreML (macOS), DirectML (Windows, any NVIDIA/AMD/Intel GPU), OpenVINO (Linux, Intel iGPU); falls back to CPU automatically if unavailable, see localui.acceleratorEP
	LocalUIParseModelDir string `json:"local_ui_parse_model_dir" mapstructure:"local_ui_parse_model_dir"` // dir containing icon_detect.onnx/dbnet.onnx/svtr.onnx; "" resolves to ~/.usbridge/localui/models
	LocalUIParseORTLib   string `json:"local_ui_parse_ort_lib" mapstructure:"local_ui_parse_ort_lib"`     // path to the ONNX Runtime shared lib; "" resolves to ~/.usbridge/localui/runtime/<localui.DefaultRuntimeLibName()>
}

// DefaultConfig returns the default configuration
func DefaultConfig() *AppConfig {
	return &AppConfig{
		// USBridge 2
		USBPort:    8080,
		APITimeout: 15,

		ConnectionProtocol: modelsafeProtocol(ConnectionProtocolAuto),

		TailscaleEnabled: true,

		// NBD server
		NBDPort:           10809,
		MaxClients:        5,
		ScanPaths:         []string{"./isos", "/home/user/isos", "/mnt/isos"},
		SupportedTypes:    []string{".iso", ".img", ".vmdk", ".vdi", ".qcow", ".qcow2", ".raw", ".vmi"},
		NBDExportReadOnly: true, // safe default: RO; false = RW export via overlay (base image stays intact)

		// Video protocol (moonlight by default)
		VideoProtocol: VideoProtocolMoonlight,

		// Video UDP (55000 — dynamic range, doesn't conflict with nidmsrv/UPnP/AirPlay etc.)
		VideoUDPPort:  DefaultVideoUDPPort,
		VideoBindHost: "0.0.0.0",

		// Video
		VideoBitrate:   10000,
		VideoWidth:     1280,
		VideoHeight:    720,
		VideoFPS:       60,
		LowLatencyMode: true, // Enable low latency mode by default
		BufferSize:     2,    // Minimum frame buffer
		SkipFrameDelay: true, // Skip delays between frames

		// Audio
		AudioCodec:      "Opus",
		AudioBitrate:    128,
		AudioSampleRate: 48000,
		AudioChannels:   2,

		// UI — default size is comfortable for modern DPI and scaling
		WindowWidth:  960,
		WindowHeight: 640,
		Theme:        "light",
		LogLevel:     "info",

		// Clipboard sync
		ClipboardSyncEnabled: true,
		ClipboardMaxBytes:    200 * 1024 * 1024,

		// Local ui.parse offload -- off by default (opt-in, see field docs)
		LocalUIParseEnabled: false,
		LocalUIParseGPU:     true,
	}
}

func modelsafeProtocol(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case ConnectionProtocolTailscale, ConnectionProtocolDirect:
		return strings.ToLower(strings.TrimSpace(protocol))
	default:
		return ConnectionProtocolAuto
	}
}

// AppState application state
type AppState struct {
	IsConnected      bool      `json:"is_connected"`
	IsStreaming      bool      `json:"is_streaming"`
	IsNBDRunning     bool      `json:"is_nbd_running"`
	CurrentISO       string    `json:"current_iso"`
	LastConnected    time.Time `json:"last_connected"`
	LastDisconnected time.Time `json:"last_disconnected"`
}

// SnapshotInfo snapshot information
type SnapshotInfo struct {
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	SizeHuman   string    `json:"size_human"` // Human-readable size from the API (e.g. "0 B / 40.0 GB")
	Changelog   string    `json:"changelog"`  // Snapshot changelog (raw, from btrfs)
	CreatedAt   time.Time `json:"created_at"`
	Description string    `json:"description,omitempty"`
	Path        string    `json:"path"`
	Connected   bool      `json:"connected"`
}

// SnapshotJSON structure for parsing the server's JSON response
type SnapshotJSON struct {
	Name       string `json:"name"`
	Date       string `json:"date"`
	Timestamp  int64  `json:"timestamp"`
	Size       int64  `json:"size"`
	SizeHuman  string `json:"size_human"`
	Changelog  string `json:"changelog"`
	FilesCount int    `json:"files_count"`
	IsLatest   bool   `json:"is_latest"`
	Connected  bool   `json:"connected"`
}

// ToSnapshotInfo converts SnapshotJSON into SnapshotInfo
func (sj *SnapshotJSON) ToSnapshotInfo() *SnapshotInfo {
	// Try to parse the date from the string; fall back to the timestamp if that fails
	var createdAt time.Time
	var err error

	if sj.Date != "" {
		// Try different date formats
		formats := []string{
			"2006-01-02 15:04:05",
			"2006-01-02T15:04:05",
			"2006-01-02 15:04:05Z",
			"2006-01-02T15:04:05Z",
			"2006-01-02T15:04:05.000Z",
		}

		for _, format := range formats {
			if createdAt, err = time.Parse(format, sj.Date); err == nil {
				break
			}
		}
	}

	// If the date string couldn't be parsed, use the timestamp
	if err != nil {
		createdAt = time.Unix(sj.Timestamp, 0)
	}

	return &SnapshotInfo{
		Name:        sj.Name,
		Size:        sj.Size,
		SizeHuman:   sj.SizeHuman,
		Changelog:   sj.Changelog,
		CreatedAt:   createdAt,
		Description: "",
		Path:        "",
		Connected:   sj.Connected,
	}
}

// FormatSize formats the size into a human-readable form (from bytes)
func (s *SnapshotInfo) FormatSize() string {
	const unit = 1024
	if s.Size < unit {
		return fmt.Sprintf("%d B", s.Size)
	}
	div, exp := int64(unit), 0
	for n := s.Size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(s.Size)/float64(div), "KMGTPE"[exp])
}

// DisplaySize returns the size for display: size_human from the API takes priority
func (s *SnapshotInfo) DisplaySize() string {
	if s.SizeHuman != "" {
		return s.SizeHuman
	}
	return s.FormatSize()
}

// splitChangelogLine splits a changelog line, preserving escaped spaces (\ )
func splitChangelogLine(line string) []string {
	// Replace \ (backslash-space) with a placeholder so paths with spaces aren't split
	const placeholder = "\u200B" // zero-width space
	line = strings.ReplaceAll(line, "\\ ", placeholder)
	parts := strings.Fields(line)
	for i := range parts {
		parts[i] = strings.ReplaceAll(parts[i], placeholder, " ")
	}
	return parts
}

// isParamPart checks whether part is a key=value parameter (dest=, from=, uuid=, etc.)
func isParamPart(part string) bool {
	idx := strings.Index(part, "=")
	if idx <= 0 {
		return false
	}
	key := part[:idx]
	for _, c := range key {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return len(key) > 0
}

// extractPathFromParts extracts a path that may span multiple parts (when the name contains spaces)
func extractPathFromParts(parts []string) (path string, params []string) {
	if len(parts) < 2 {
		return "", parts
	}
	var pathParts []string
	for i := 1; i < len(parts); i++ {
		if isParamPart(parts[i]) {
			return strings.Join(pathParts, " "), parts[i:]
		}
		pathParts = append(pathParts, parts[i])
	}
	return strings.Join(pathParts, " "), nil
}

// extractPathParam extracts the value of a key= parameter from parts (from=, dest=).
// The value may span multiple parts if the path contains unescaped spaces.
func extractPathParam(parts []string, prefix string) string {
	for i, p := range parts {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		val := strings.TrimPrefix(p, prefix)
		// Collect subsequent parts until the next key=value (in case the value is split)
		for j := i + 1; j < len(parts); j++ {
			next := parts[j]
			if strings.Contains(next, "=") && !strings.HasPrefix(next, prefix) {
				break // Next key=value parameter
			}
			val += " " + next
		}
		val = simplifyPath(val)
		return val
	}
	return ""
}

// simplifyPath keeps only the file name, strips ./ and a trailing /
func simplifyPath(p string) string {
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimSuffix(p, "/")
	if idx := strings.LastIndex(p, "/"); idx >= 0 && idx < len(p)-1 {
		p = p[idx+1:]
	}
	return p
}

// ChangelogFormatOptions changelog formatting options (for localization)
type ChangelogFormatOptions struct {
	OpNames       map[string]string // translations of btrfs operations: "snapshot" -> "snapshot creation"
	TempFileLabel string            // unused, kept for compatibility
}

// formatPathForDisplay returns the path for display (BTRFS names are shown as-is)
func formatPathForDisplay(path, _ string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return path
	}
	return path
}

// FormatChangelog formats the raw btrfs changelog into a human-readable form.
// opts — localized labels (defaults are used if nil).
func (s *SnapshotInfo) FormatChangelog(opts *ChangelogFormatOptions) string {
	raw := strings.TrimSpace(s.Changelog)
	if raw == "" {
		return ""
	}
	opNames := map[string]string{
		"snapshot": "snapshot creation",
		"utimes":   "time update",
		"mkfile":   "file creation",
		"rename":   "rename",
		"truncate": "file truncation",
		"clone":    "clone",
		"chown":    "owner change",
		"chmod":    "permissions change",
	}
	tempFileLabel := "temporary file"
	if opts != nil {
		if opts.OpNames != nil {
			opNames = opts.OpNames
		}
		if opts.TempFileLabel != "" {
			tempFileLabel = opts.TempFileLabel
		}
	}
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := splitChangelogLine(line)
		if len(parts) < 2 {
			lines = append(lines, "• "+line)
			continue
		}
		op := parts[0]
		path, params := extractPathFromParts(parts)
		path = formatPathForDisplay(simplifyPath(path), tempFileLabel)
		if name, ok := opNames[op]; ok {
			op = name
		}
		if len(params) > 0 {
			dest := formatPathForDisplay(extractPathParam(params, "dest="), tempFileLabel)
			from := formatPathForDisplay(extractPathParam(params, "from="), tempFileLabel)
			if dest != "" {
				// rename: source → dest
				lines = append(lines, fmt.Sprintf("• %s: %s → %s", op, path, dest))
			} else if from != "" {
				// clone: from (source) → path (destination)
				lines = append(lines, fmt.Sprintf("• %s: %s → %s", op, from, path))
			} else {
				lines = append(lines, fmt.Sprintf("• %s: %s", op, path))
			}
		} else {
			lines = append(lines, fmt.Sprintf("• %s: %s", op, path))
		}
	}
	return strings.Join(lines, "\n")
}

// SnapshotsResponse response with a list of snapshots
type SnapshotsResponse struct {
	Count     int            `json:"count"`
	Snapshots []SnapshotInfo `json:"snapshots"`
}

// SnapshotsJSONResponse structure for parsing the server's JSON response
type SnapshotsJSONResponse struct {
	Count     int            `json:"count"`
	Total     int            `json:"total"`
	Snapshots []SnapshotJSON `json:"snapshots"`
}

// ToSnapshotsResponse converts SnapshotsJSONResponse into SnapshotsResponse
func (sjr *SnapshotsJSONResponse) ToSnapshotsResponse() *SnapshotsResponse {
	snapshots := make([]SnapshotInfo, len(sjr.Snapshots))
	for i, snapshotJSON := range sjr.Snapshots {
		snapshots[i] = *snapshotJSON.ToSnapshotInfo()
	}

	return &SnapshotsResponse{
		Count:     sjr.Count,
		Snapshots: snapshots,
	}
}

// ISOSpaceInfo information about free space on the SD card (btrfs iso/data/backup partition)
type ISOSpaceInfo struct {
	TotalSpace     int64   `json:"total_space"`
	UsedSpace      int64   `json:"used_space"`
	AvailableSpace int64   `json:"available_space"`
	TotalSpaceGB   string  `json:"total_space_gb"`
	UsedSpaceGB    string  `json:"used_space_gb"`
	AvailableGB    string  `json:"available_gb"`
	UsedPercent    float64 `json:"used_percent"`
	ISODirectory   string  `json:"iso_directory"`
}
