package models

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// USBStatus USBridge 2 status
type USBStatus struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    *StatusData `json:"data"`
}

// StatusData status data
type StatusData struct {
	Service *ServiceStatus `json:"service"`
	NBD     *NBDStatus     `json:"nbd"`
	USB     *USBDeviceInfo `json:"usb"`
	Kernel  *KernelInfo    `json:"kernel"`
	Video   *VideoStatus   `json:"video"`
	OS      string         `json:"os,omitempty"`
}

// ServiceStatus service status
type ServiceStatus struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Uptime    string    `json:"uptime"`
}

// NBDStatus NBD connection status
type NBDStatus struct {
	Connected bool   `json:"connected"`
	Device    string `json:"device"`
	Server    string `json:"server"`
	Port      int    `json:"port"`
	Export    string `json:"export"`
}

// USBDeviceInfo USB device information
type USBDeviceInfo struct {
	Connected       bool   `json:"connected"`
	GadgetName      string `json:"gadget_name"`
	UDCName         string `json:"udc_name"`
	VendorID        string `json:"vendor_id"`
	ProductID       string `json:"product_id"`
	KeyboardEnabled bool   `json:"keyboard_enabled"`
}

// KernelInfo kernel information
type KernelInfo struct {
	ModulesLoaded bool     `json:"modules_loaded"`
	Modules       []string `json:"modules"`
}

// VideoStatus video status
type VideoStatus struct {
	Enabled           bool                 `json:"enabled"`
	Device            string               `json:"device"`
	Width             int                  `json:"width"`
	Height            int                  `json:"height"`
	FPS               int                  `json:"fps"`
	Quality           int                  `json:"quality"`
	Bitrate           string               `json:"bitrate"`
	BufferSize        int                  `json:"buffer_size"`
	Mode              string               `json:"mode"`
	Transport         string               `json:"transport"`
	Encoding          string               `json:"encoding"`
	SourceFormat      string               `json:"source_format"`
	DefaultPixelFormat string              `json:"default_pixel_format,omitempty"`
	ServerDecodesJPEG bool                 `json:"server_decodes_jpeg"`
	CaptureModes      []VideoCaptureMode   `json:"capture_modes,omitempty"`
	SupportedModes    []VideoTransportMode `json:"supported_modes,omitempty"`
	ClientsCount      int                  `json:"clients_count"`
	Streaming         bool                 `json:"streaming"`
	// Color444Active is whether the most recently started (or currently
	// running) session actually negotiated RustShine Pro 4:4:4 chroma --
	// post-fallback truth, the same way Encoding is (see agent's
	// Application.Color444Status doc comment). Color444Available is
	// whether the agent could offer 4:4:4 right now at all (hardware probe
	// AND license tier) -- the video-settings popup shows/enables its
	// 4:4:4 checkbox based on this, before the user has ever started
	// streaming.
	Color444Active    bool `json:"color_444_active"`
	Color444Available bool `json:"color_444_available"`
}

const (
	VideoModeH264    = "h264"
	VideoModeH265    = "h265"
	VideoModeAV1     = "av1"
	VideoModeJPEGRTP = "jpeg_rtp"
	VideoModeRawYUYV = "raw_yuyv"
)

type VideoTransportMode struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	Transport         string `json:"transport"`
	Encoding          string `json:"encoding"`
	ServerDecodesJPEG bool   `json:"server_decodes_jpeg"`
}

type VideoCaptureMode struct {
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	FPS         []int  `json:"fps"`
	PixelFormat string `json:"pixel_format"`
}

type VideoInfoData struct {
	VideoStatus
	UDPPort          int            `json:"udp_port"`
	StreamURL        string         `json:"stream_url"`
	UDPListenerReady bool           `json:"udp_listener_ready"`
	AvailableDevices []SystemDevice `json:"available_devices,omitempty"`
}

func ParseVideoInfoData(data interface{}) (*VideoInfoData, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal video info data: %w", err)
	}

	var parsed VideoInfoData
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse video info data: %w", err)
	}

	if parsed.Transport == "" {
		parsed.Transport = "rtp"
	}
	return &parsed, nil
}

// PCPanelLedsData PC Panel LED status data
type PCPanelLedsData struct {
	Power bool `json:"power"`
	HDD   bool `json:"hdd"`
}

// PCPanelLedsResponse API response for /api/pcpanel/leds
type PCPanelLedsResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    PCPanelLedsData `json:"data"`
}

// PCPanelButtonRequest power/reset button press request
type PCPanelButtonRequest struct {
	Button string `json:"button"` // "power" or "reset"
}

// APIResponse standard API response
type APIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

type CursorState struct {
	Visible  bool    `json:"visible"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Width    int     `json:"width,omitempty"`
	Height   int     `json:"height,omitempty"`
	HotspotX int     `json:"hotspot_x,omitempty"`
	HotspotY int     `json:"hotspot_y,omitempty"`
	Source   string  `json:"source,omitempty"`
}

type MouseResponseData struct {
	Cursor *CursorState `json:"cursor,omitempty"`
}

type TailscaleRegistrationRequest struct {
	DeviceToken string `json:"device_token"`
	AuthKey     string `json:"auth_key"`
	Hostname    string `json:"hostname,omitempty"`
}

type TailscaleStatus struct {
	Running  bool   `json:"running"`
	LoggedIn bool   `json:"logged_in"`
	Backend  string `json:"backend"`
	DNSName  string `json:"dns_name,omitempty"`
	HostName string `json:"host_name,omitempty"`
	IP4      string `json:"ip4,omitempty"`
	AuthURL  string `json:"auth_url,omitempty"`
}

// KeyboardRequest key send request
type KeyboardRequest struct {
	Action    string `json:"action"` // key, combo, text
	KeyCode   int    `json:"key_code,omitempty"`
	Modifiers int    `json:"modifiers,omitempty"`
	Text      string `json:"text,omitempty"`
}

// MouseRequest mouse or touchscreen control request
type MouseRequest struct {
	Action      string `json:"action"`                 // move, click, scroll, action (mouse) or touch (touchscreen)
	DX          int    `json:"dx,omitempty"`           // X offset (from -128 to 127)
	DY          int    `json:"dy,omitempty"`           // Y offset (from -128 to 127)
	X           *int   `json:"x,omitempty"`            // X for touchscreen (0..4095); pointer needed so 0 doesn't fall out of JSON
	Y           *int   `json:"y,omitempty"`            // Y for touchscreen (0..4095); pointer needed so 0 doesn't fall out of JSON
	Tip         bool   `json:"tip"`                    // for action "touch": true = touch, false = release (must be passed; no omitempty so false doesn't drop in JSON)
	Button      int    `json:"button,omitempty"`       // Mouse button (1=left, 2=right, 3=middle)
	Scroll      int    `json:"scroll,omitempty"`       // Mouse wheel scroll (from -127 to 127)
	ButtonState int    `json:"button_state,omitempty"` // Button bitmask (bit0=L, bit1=R, bit2=M) for absolute_event
}

// GamepadRequest gamepad state sent over /api/gamepad/ws at ~60 Hz.
type GamepadRequest struct {
	Buttons      uint16 `json:"buttons"`
	LeftX        int8   `json:"lx"`
	LeftY        int8   `json:"ly"`
	RightX       int8   `json:"rx"`
	RightY       int8   `json:"ry"`
	LeftTrigger  uint8  `json:"lt"`
	RightTrigger uint8  `json:"rt"`
}

// DeviceStartRequest device start request (old format - deprecated)
type DeviceStartRequest struct {
	Device                  string `json:"device"`                               // keyboard, drive, mouse, etc.
	Type                    string `json:"type,omitempty"`                       // For mouse: "mouse" (touchpad), "touchscreen" or "absolute"
	DisplayIndex            int    `json:"display_index,omitempty"`              // For mouse absolute: 0-based display index
	DisplayCount            int    `json:"display_count,omitempty"`              // For mouse absolute: total display count (0/1 = single)
	RNDISMode               string `json:"rndis_mode,omitempty"`                 // For RNDIS: "auto", "wifirouter", "etherouter", "etherbridge"
	Server                  string `json:"server,omitempty"`                     // Server IP for NBD
	Port                    int    `json:"port,omitempty"`                       // Server port for NBD
	ExportName              string `json:"export_name,omitempty"`                // Export name for API
	NBDHandshakeEmptyExport bool   `json:"nbd_handshake_empty_export,omitempty"` // true = empty name in NBD handshake (qemu-nbd)
	ReadOnly                bool   `json:"read_only"`                            // true = read-only
	DriveMode               string `json:"drive_mode,omitempty"`                 // "" = auto, "cdrom" = CD-ROM, "disk" = USB stick
	VendorID                string `json:"vendor_id"`                            // Vendor ID
	ProductID               string `json:"product_id"`                           // Product ID
	ProductName             string `json:"product_name"`                         // Product name
	Manufacturer            string `json:"manufacturer"`                         // Manufacturer
	KeyboardMode            bool   `json:"keyboard_mode,omitempty"`              // Use -k parameter for keyboard
}

// DeviceStartBatchRequest request to start multiple devices (old API - deprecated)
type DeviceStartBatchRequest []DeviceStartRequest

// DeviceStartRequestNew new format for device start request via sources
type DeviceStartRequestNew struct {
	Sources  []string `json:"sources"`  // List of sources (nbd://, mtp://, /path/to/file)
	Keyboard bool     `json:"keyboard"` // Enable keyboard
	Mouse    bool     `json:"mouse"`    // Enable mouse
	RNDIS    bool     `json:"rndis"`    // Enable network card
}

// DeviceStopRequest device stop request
type DeviceStopRequest struct {
	ID int `json:"id"` // Device ID to stop
}

// DeviceInfo device information
type DeviceInfo struct {
	ID           int       `json:"id"`
	Device       string    `json:"device"` // disk:file_name, keyboard, etc.
	Status       string    `json:"status"` // connected, disconnected
	VendorID     string    `json:"vendor_id"`
	ProductID    string    `json:"product_id"`
	ProductName  string    `json:"product_name"`
	Manufacturer string    `json:"manufacturer"`
	CreatedAt    time.Time `json:"created_at"`
	Type         string    `json:"type"`                 // nbd, local, keyboard
	Name         string    `json:"name"`                 // Device/file name
	DriveMode    string    `json:"drive_mode,omitempty"` // "cdrom" or "disk"
}

// DeviceInfoResponse device information response
type DeviceInfoResponse struct {
	Devices         []DeviceInfo `json:"devices"`
	Count           int          `json:"count"`
	MountInProgress bool         `json:"mount_in_progress"` // true - mounting is in progress in background
	LastMountError  string       `json:"last_mount_error"`  // last mount error
	AgentOS         string       `json:"agent_os,omitempty"`
	AgentDisplay    string       `json:"agent_display,omitempty"`
}

// DeviceStatusResponse device status response (new API)
type DeviceStatusResponse struct {
	Available         bool     `json:"available"`
	UnmountAvailable  bool     `json:"unmount_available"`
	ConnectedDevices  []string `json:"connected_devices"`
	KeyboardAvailable bool     `json:"keyboard_available"`
}

// VideoStartRequest video streaming start request
type VideoStartRequest struct {
	VideoDevice        string `json:"video_device,omitempty"`
	VideoWidth         int    `json:"video_width"`
	VideoHeight        int    `json:"video_height"`
	VideoFPS           int    `json:"video_fps"`
	VideoQuality       int    `json:"video_quality"`
	VideoBitrate       string `json:"video_bitrate"`
	VideoMode          string `json:"video_mode,omitempty"`
	CapturePixelFormat string `json:"capture_pixel_format,omitempty"`
	EnableVSync        bool   `json:"enable_vsync,omitempty"`
	// Color444 requests RustShine Pro 4:4:4 chroma for this session --
	// purely a local hint to the client's own Moonlight connection setup
	// (VideoWidget.startVideoWithParamsInternal -> VideoClient.SetColor444),
	// never sent to the agent's capture-card REST API: the real
	// negotiation happens entirely client-side via moonlight-common-c's
	// own ANNOUNCE (chromaSamplingType), gated on the agent/RustShine
	// actually advertising SCM_HEVC_REXT8_444 in /serverinfo. Ignored
	// (silently has no effect) for any mode other than "h265", the only
	// codec this project's hardware encode path wires 4:4:4 up for.
	Color444 bool `json:"-"`
	// ClientPort - client port to receive UDP stream (server will take IP from HTTP)
	ClientHost string `json:"client_host,omitempty"`
	ClientPort int    `json:"client_port,omitempty"`
	ShowMouse  bool   `json:"show_mouse,omitempty"`
	TraceID    string `json:"-"`
}

// SystemDevice device visible on the bridge side via /api/devices.
type SystemDevice struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Bus         string `json:"bus,omitempty"`
	Index       int    `json:"index,omitempty"`
	Connected   bool   `json:"connected"`
	Description string `json:"description"`
}

// AudioStartRequest request to start audio capture.
type AudioStartRequest struct {
	DevicePath string `json:"device_path"`
}

// AudioInfoResponse audio streaming status.
type AudioInfoResponse struct {
	Streaming  bool   `json:"streaming"`
	DevicePath string `json:"device_path"`
	DeviceName string `json:"device_name"`
	Muted      bool   `json:"muted"`
}

// VideoDeviceConfig video start config saved by client for a specific /dev/video*.
type VideoDeviceConfig struct {
	DevicePath    string `json:"device_path"`
	DeviceName    string `json:"device_name,omitempty"`
	VideoWidth    int    `json:"video_width"`
	VideoHeight   int    `json:"video_height"`
	VideoFPS      int    `json:"video_fps"`
	VideoQuality  int    `json:"video_quality"`
	VideoBitrate  string `json:"video_bitrate"`
	VideoMode          string `json:"video_mode"`
	CapturePixelFormat string `json:"capture_pixel_format,omitempty"`
	ShowMouse          bool   `json:"show_mouse,omitempty"`
	EnableVSync        bool   `json:"enable_vsync,omitempty"`
	// Color444 persists the user's RustShine Pro 4:4:4 checkbox choice for
	// this capture device -- see VideoStartRequest.Color444's doc comment.
	Color444      bool  `json:"color_444,omitempty"`
	LastAppliedAt int64 `json:"last_applied_at,omitempty"`
}

func (c VideoDeviceConfig) ToVideoStartRequest() *VideoStartRequest {
	return &VideoStartRequest{
		VideoDevice:  c.DevicePath,
		VideoWidth:   c.VideoWidth,
		VideoHeight:  c.VideoHeight,
		VideoFPS:     c.VideoFPS,
		VideoQuality: c.VideoQuality,
		VideoBitrate:       c.VideoBitrate,
		VideoMode:          c.VideoMode,
		CapturePixelFormat: c.CapturePixelFormat,
		ShowMouse:          c.ShowMouse,
		EnableVSync:        c.EnableVSync,
		Color444:           c.Color444,
	}
}

// ConfigRequest configuration update request
type ConfigRequest struct {
	NBDDevice       string           `json:"nbd_device,omitempty"`
	NBDServer       string           `json:"nbd_server,omitempty"`
	NBDPort         int              `json:"nbd_port,omitempty"`
	ExportName      string           `json:"export_name,omitempty"`
	GadgetName      string           `json:"gadget_name,omitempty"`
	UDCName         string           `json:"udc_name,omitempty"`
	VendorID        string           `json:"vendor_id,omitempty"`
	ProductID       string           `json:"product_id,omitempty"`
	ProductName     string           `json:"product_name,omitempty"`
	Manufacturer    string           `json:"manufacturer,omitempty"`
	KeyboardEnabled bool             `json:"keyboard_enabled,omitempty"`
	VideoEnabled    bool             `json:"video_enabled,omitempty"`
	VideoDevice     string           `json:"video_device,omitempty"`
	VideoWidth      int              `json:"video_width,omitempty"`
	VideoHeight     int              `json:"video_height,omitempty"`
	VideoFPS        int              `json:"video_fps,omitempty"`
	VideoQuality    int              `json:"video_quality,omitempty"`
	VideoBitrate    string           `json:"video_bitrate,omitempty"`
	VideoBufferSize int              `json:"video_buffer_size,omitempty"`
	WebServer       *WebServerConfig `json:"web_server,omitempty"`
	CheckInterval   int              `json:"check_interval,omitempty"`
}

// WebServerConfig web server configuration
type WebServerConfig struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
}

// LocalDrive local drive information
type LocalDrive struct {
	Name        string    `json:"name"`         // File name
	Size        int64     `json:"size"`         // Size in bytes (total volume)
	FreeSpace   int64     `json:"free_space"`   // Free space in bytes (0 = unknown)
	SizeHuman   string    `json:"size_human"`   // Size in human-readable format (can be "used / total")
	Modified    time.Time `json:"modified"`     // Modification date
	IsMounted   bool      `json:"is_mounted"`   // Is device mounted
	MountDevice string    `json:"mount_device"` // Mount device
	FileType    string    `json:"file_type"`    // File type (ISO, IMG, MTP, etc.)
	IsValid     bool      `json:"is_valid"`     // Is file valid
	SourceType  string    `json:"source_type"`  // Source type (images, data, mtp)
	SourcePath  string    `json:"source_path"`  // Source path
}

// LocalDrivesResponse response with local drives
type LocalDrivesResponse struct {
	Drives []LocalDrive `json:"drives"`
	Count  int          `json:"count"`
	Path   string       `json:"path"` // Path to devices folder
}

// FormatSize formats size to human-readable string for LocalDrive
func (ld *LocalDrive) FormatSize() string {
	const unit = 1024
	if ld.Size < unit {
		return fmt.Sprintf("%d B", ld.Size)
	}
	div, exp := int64(unit), 0
	for n := ld.Size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(ld.Size)/float64(div), "KMGTPE"[exp])
}

// parseSizeHumanPair parses string formatted like "X GB / Y GB" or "X B / Y GB", returns (used, total) in bytes.
func parseSizeHumanPair(s string) (used, total int64) {
	parts := strings.SplitN(strings.TrimSpace(s), "/", 2)
	if len(parts) != 2 {
		return -1, -1
	}
	used = parseHumanSize(strings.TrimSpace(parts[0]))
	total = parseHumanSize(strings.TrimSpace(parts[1]))
	return used, total
}

func parseHumanSize(s string) int64 {
	re := regexp.MustCompile(`(?i)^([\d.]+)\s*([KMGTPE]?B?)$`)
	m := re.FindStringSubmatch(strings.TrimSpace(s))
	if len(m) < 3 {
		return -1
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return -1
	}
	unit := strings.ToUpper(m[2])
	mult := int64(1)
	switch {
	case strings.HasPrefix(unit, "K"):
		mult = 1024
	case strings.HasPrefix(unit, "M"):
		mult = 1024 * 1024
	case strings.HasPrefix(unit, "G"):
		mult = 1024 * 1024 * 1024
	case strings.HasPrefix(unit, "T"):
		mult = 1024 * 1024 * 1024 * 1024
	case strings.HasPrefix(unit, "P"):
		mult = 1024 * 1024 * 1024 * 1024 * 1024
	case strings.HasPrefix(unit, "E"):
		mult = 1024 * 1024 * 1024 * 1024 * 1024 * 1024
	}
	return int64(val * float64(mult))
}

// StorageDisplay returns (totalBytes, freeBytes, usedPercent) for display.
// freeBytes=0 and usedPercent=0 if data is unavailable.
func (ld *LocalDrive) StorageDisplay() (totalBytes, freeBytes int64, usedPercent float64) {
	totalBytes = ld.Size
	if totalBytes <= 0 {
		return 0, 0, 0
	}
	if ld.FreeSpace > 0 {
		freeBytes = ld.FreeSpace
		usedBytes := totalBytes - freeBytes
		if totalBytes > 0 {
			usedPercent = float64(usedBytes) / float64(totalBytes) * 100
		}
		return totalBytes, freeBytes, usedPercent
	}
	// Try to parse SizeHuman formatted like "used / total" (e.g. "5.2 GB / 32 GB")
	if ld.SizeHuman != "" {
		used, total := parseSizeHumanPair(ld.SizeHuman)
		if used >= 0 && total > 0 {
			totalBytes = total
			freeBytes = total - used
			usedPercent = float64(used) / float64(total) * 100
			return totalBytes, freeBytes, usedPercent
		}
	}
	return totalBytes, 0, 0
}

// FormatSizeShort formats bytes shortly (for display in header).
func FormatSizeShort(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// FormatStorageCompact returns compact string for indicator: "43% 66/119 GB" with MB/GB/TB by size.
func FormatStorageCompact(available, total int64, usedPct float64) string {
	const unit = 1024
	if total < unit {
		return fmt.Sprintf("%.0f%% %d/%d B", usedPct, available, total)
	}
	// Select unit by total: KB, MB, GB, TB
	div, exp := int64(unit), 0
	for n := total / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	if exp > 3 {
		exp = 3
		div = unit * unit * unit * unit
	}
	unitStr := string([]byte{'K', 'M', 'G', 'T'}[exp]) + "B"
	av := float64(available) / float64(div)
	tot := float64(total) / float64(div)
	var avStr, totStr string
	if tot >= 100 {
		avStr = fmt.Sprintf("%.0f", av)
		totStr = fmt.Sprintf("%.0f", tot)
	} else if tot >= 10 {
		avStr = fmt.Sprintf("%.1f", av)
		totStr = fmt.Sprintf("%.1f", tot)
	} else {
		avStr = fmt.Sprintf("%.1f", av)
		totStr = fmt.Sprintf("%.1f", tot)
	}
	return fmt.Sprintf("%.0f%% %s/%s %s", usedPct, avStr, totStr, unitStr)
}

// FormatStorageSizeOnly returns only occupied space: "53/119 GB" (used/total, for small text under percent).
func FormatStorageSizeOnly(used, total int64) string {
	const unit = 1024
	if total < unit {
		return fmt.Sprintf("%d/%d B", used, total)
	}
	div, exp := int64(unit), 0
	for n := total / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	if exp > 3 {
		exp = 3
		div = unit * unit * unit * unit
	}
	unitStr := string([]byte{'K', 'M', 'G', 'T'}[exp]) + "B"
	usedF := float64(used) / float64(div)
	tot := float64(total) / float64(div)
	var usedStr, totStr string
	if tot >= 100 {
		usedStr = fmt.Sprintf("%.0f", usedF)
		totStr = fmt.Sprintf("%.0f", tot)
	} else if tot >= 10 {
		usedStr = fmt.Sprintf("%.1f", usedF)
		totStr = fmt.Sprintf("%.1f", tot)
	} else {
		usedStr = fmt.Sprintf("%.1f", usedF)
		totStr = fmt.Sprintf("%.1f", tot)
	}
	return fmt.Sprintf("%s/%s %s", usedStr, totStr, unitStr)
}

// StorageInfo detail storage information
type StorageInfo struct {
	Total      int64   `json:"total"`
	Used       int64   `json:"used"`
	Free       int64   `json:"free"`
	Percent    float64 `json:"percent"`
	TotalHuman string  `json:"total_human"`
	UsedHuman  string  `json:"used_human"`
	FreeHuman  string  `json:"free_human"`
	Mounted    bool    `json:"mounted"`
}

// StorageStatusData detailed storage status.
//
// SDCard/EMMC are always the literal /mnt/sdcard and /mnt/emmc mountpoints
// on the device. BootDeviceIsSD is true when the appliance is currently
// running off its SD slot -- in that case there's no separate card slot
// left for a removable card (SDCard stays unmounted/zero), and EMMC is
// actually *this same SD card's own* extra partition, not a physically
// separate onboard eMMC chip. UI code should attribute the EMMC figures to
// "the SD card" (not "eMMC") whenever this is true -- see
// StorageDisplayInfo below.
type StorageStatusData struct {
	SDCard         StorageInfo `json:"sdcard"`
	EMMC           StorageInfo `json:"emmc"`
	BootDeviceIsSD bool        `json:"boot_device_is_sd"`
}

// StorageDisplayInfo returns the StorageInfo that genuinely represents "the
// SD card" for a single-figure, SD-labeled display (e.g. the header
// progress bar, which only has an SD card icon -- there's no separate
// eMMC one). ok is false when there's nothing that qualifies: a normal
// eMMC-boot device with no removable card inserted has real onboard eMMC
// usage, but that's not "the SD card" and showing it under an SD icon
// would be its own mislabeling -- callers should just hide the display in
// that case, same as before this field existed.
func (s *StorageStatusData) StorageDisplayInfo() (info StorageInfo, ok bool) {
	if s.BootDeviceIsSD {
		// No separate card slot free right now -- /mnt/emmc is actually
		// this same SD card's own storage (see the struct doc comment).
		return s.EMMC, true
	}
	if s.SDCard.Mounted {
		return s.SDCard, true
	}
	return StorageInfo{}, false
}

// ScriptInfo information about a script
type ScriptInfo struct {
	Name        string `json:"name"`
	Description string `json:"desc"`
	Path        string `json:"path"`
}

// ScriptRunRequest request to run a script
type ScriptRunRequest struct {
	Path string `json:"path"`
}

// ScriptSaveRequest request to save a script
type ScriptSaveRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// ScriptRunStatus status of a running or recently finished script
type ScriptRunStatus struct {
	Path      string `json:"path"`
	Running   bool   `json:"running"`
	StartedAt string `json:"started_at"`
	Error     string `json:"error,omitempty"`
}

// ScriptLogResponse output lines from a script
type ScriptLogResponse struct {
	Lines []string `json:"lines"`
	Total int      `json:"total"`
}
