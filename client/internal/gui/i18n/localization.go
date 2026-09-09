package i18n

// LocalizedStrings contains all UI text strings
type LocalizedStrings struct {
	// Main Window
	AppTitle                string
	ServerAddress           string
	Token                   string
	ConnectButton           string
	DisconnectButton        string
	DisconnectAllButton     string
	VideoStreamActiveButton string
	TabDevices              string
	TabControl              string
	TabSnapshots            string

	// Connection Manager
	ConnectionManager         string
	SavedConnections          string
	ConnectionManagement      string
	AddressAndTokenHint       string
	ConnectionHeroEyebrow     string
	ConnectionPanelHint       string
	ConnectionNameLabel       string
	ConnectionNamePlaceholder string
	SaveButton                string
	DeleteButton              string
	EditButton                string
	QRScannerButton           string
	EditConnectionTitle       string
	AddConnectionTitle        string
	AddNewDeviceTitle         string
	NoSavedConnections        string
	NoSavedConnectionsHint    string
	OnboardingStepConnect     string
	OnboardingStepIP          string
	OnboardingStepScan        string
	DeleteConnectionTitle     string
	DeleteConnectionConfirm   string
	TailscaleRegisterLabel    string
	// ConnectingToConnection is the bottom toast's message while a Connect
	// attempt is in flight (view.ShowConnectingToast) -- %s is the
	// connection's own saved name.
	ConnectingToConnection string

	// Video Widget
	VideoNotStarted      string
	StartVideoButton     string
	StopVideoButton      string
	FullscreenButton     string
	StartingVideoCapture string
	WaitingServerStart   string
	VideoActive          string
	StoppingVideoCapture string
	VideoStopped         string
	ErrorNoConnection    string
	ErrorWindowNotInit   string

	// Backup Widget
	BackupFlash              string
	CurrentFlashAndSnapshots string
	CurrentFlash             string
	ReadyToWork              string
	LoadingSnapshots         string
	WaitingConnection        string
	ErrorLoadingSnapshots    string
	LoadedSnapshots          string
	MountingFlash            string
	MountingSnapshot         string
	FlashMounted             string
	SnapshotMounted          string
	ErrorMounting            string
	ErrorNotConnected        string
	ErrorFlashNotFound       string
	FreeDeviceSlotRequired   string // "Free up one device slot to mount a backup/snapshot"
	ErrorLoadingLocalDevices string
	ErrorMountingFlashMsg    string
	ErrorMountingSnapshotMsg string
	ErrorStatusFormat        string

	// Snapshot details dialog
	SnapshotDetailsTitle   string // "Snapshot: %s"
	SnapshotDetailsDate    string // "Date: %s"
	SnapshotDetailsSize    string // "Size: %s"
	SnapshotChangelogTitle string // "Changes (changelog)"
	SnapshotChangelogEmpty string // "No changelog available"
	SnapshotTempFile       string // "temporary file"
	OK                     string // "OK"
	Copy                   string // "Copy"

	// Changelog operations (btrfs)
	ChangelogOpSnapshot string
	ChangelogOpUtimes   string
	ChangelogOpMkfile   string
	ChangelogOpRename   string
	ChangelogOpTruncate string
	ChangelogOpClone    string
	ChangelogOpChown    string
	ChangelogOpChmod    string

	// Disk Widget
	Devices                           string
	AllAvailableDevices               string
	DevicesSectionStorage             string
	DevicesSectionStorageEyebrow      string
	DevicesSectionStorageHint         string
	DevicesSectionBackup              string
	DevicesSectionBackupEyebrow       string
	DevicesSectionBackupHint          string
	DevicesSectionControl             string
	DevicesSectionControlEyebrow      string
	DevicesSectionControlHint         string
	DevicesSectionConnectivity        string
	DevicesSectionConnectivityEyebrow string
	DevicesSectionConnectivityHint    string
	DevicesSectionAudio               string
	DevicesSectionAudioHint           string
	LocalDrives                       string
	NetworkDrives                     string
	MountButton                       string
	MountButtonCompact                string
	UnmountButton                     string
	UnmountButtonCompact              string
	AddImageButton                    string
	RefreshButton                     string
	LoadingFromCloud                  string
	CloudFilesDetected                string
	AndroidBuffering                  string // "Android is buffering cloud files"
	PreparingToMount                  string // "Preparing to mount"
	MayTake30Seconds                  string
	PleaseWait                        string
	MaxDevicesReached                 string // "Maximum of 5 devices selected"
	UnmountAllConfirm                 string // "Disconnect all mounted devices?"
	UnmountSelectedConfirm            string // "Disconnect selected devices?"
	NoMountedDevices                  string // "No mounted devices to unmount"
	SelectDevicesToMount              string // "Select devices to mount"
	StoppingAllDevices                string // "Stopping all devices..."
	StoppingNBDServers                string // "Stopping NBD servers..."
	AllDevicesUnmounted               string // "All devices disconnected"

	// Status Messages
	StatusConnected    string
	StatusDisconnected string
	StatusConnecting   string
	StatusError        string

	// QR Scanner
	QRCodeScanned           string
	Apply                   string
	Cancel                  string
	ServerAddressLabel      string
	TokenLabel              string
	ScanSuccess             string
	ErrorOpeningFile        string
	ErrorDecodingImage      string
	ErrorProcessingImage    string
	ErrorLaunchingQRScanner string
	ImageNotLoaded          string
	QRCodeNotFound          string
	InvalidQRFormat         string
	HostCannotBeEmpty       string
	QRExampleText           string
	CopyText                string
	TextCopiedToClipboard   string
	TestQRCode              string
	QRCodeForConnection     string
	QRCodeLabel             string
	PointCameraAtQR         string
	QRScanning              string
	ErrorStartingCamera     string
	ErrorSunshineNoWebRTC   string

	// Dialogs
	Yes                          string
	No                           string
	Error                        string
	Information                  string
	Confirmation                 string
	TailscaleLogoutConfirm       string
	Done                         string
	Success                      string
	Close                        string
	VideoSettingsApplied         string
	StoragePermissionRequired    string
	StoragePermissionMessage     string
	StoragePermissionSteps       string
	ErrorSelectingFile           string
	UnsupportedFileFormat        string
	FileAlreadyAdded             string
	SelectDiskImage              string
	DeleteImageTitle             string
	DeleteImageConfirm           string
	DeleteImageFromDeviceConfirm string
	UploadImageTitle             string
	UploadImageConfirm           string
	ImageUploadedSuccess         string
	ImageDeletedSuccess          string
	ErrorOpeningFileForUpload    string
	ErrorUploadingImage          string
	ErrorDeletingImage           string
	ConnectViaLink               string
	DeepLinkError                string
	ConnectionLost               string
	SAFFilePicker                string
	SAFInstructions              string
	FileSelected                 string
	NBDImageSelectedGB           string
	NBDAllowLAN                  string
	NBDStarted                   string
	NBDStartedInstructions       string
	NBDStopped                   string
	NBDStoppedSuccess            string
	NBDStatusStopped             string
	NBDStatusRunning             string
	NBDStatusError               string
	NBDInstructions              string
	NBDStartFailed               string
	NBDStopError                 string
	ConnectionTitle              string

	// Auto-update
	UpdateAvailableTitle     string
	UpdateAvailableMessage   string // %s = new version, %s = current version
	UpdateNowButton          string
	UpdateLaterButton        string
	UpdateDownloadingTitle   string
	UpdateDownloadingMessage string // %s = new version

	// Video Settings/Dialogs
	VideoQualitySettings string
	Width                string
	Height               string
	FPS                  string
	FramesPerSecond      string
	Quality              string
	Bitrate              string
	InvalidWidth         string
	InvalidHeight        string
	InvalidFPS           string
	InvalidQuality       string
	InvalidBitrate       string
	WidthRange           string
	HeightRange          string
	FPSRange             string
	QualityRange         string
	BitrateRange         string

	// Video Start Dialog
	VideoParameters             string
	Resolution                  string
	FrameRate                   string
	StreamMode                  string
	StartVideo                  string
	Starting                    string
	SwitchingDevice             string
	ConnectingRTP               string
	VideoLaunchFailed           string
	CancelVideoStart            string
	CaptureDevice               string
	VideoDevicesNotFound        string
	VideoDeviceEmpty            string
	VideoDeviceSelected         string
	VideoDeviceUnavailable      string
	VideoDeviceCurrent          string
	SettingsAction              string
	FullscreenAction            string
	VideoJPEGRTPHint            string
	VideoRawYUYVHint            string
	VideoModeH264Name           string
	VideoModeH264Description    string
	VideoModeH265Name           string
	VideoModeH265Description    string
	VideoModeAV1Name            string
	VideoModeAV1Description     string
	VideoModeJPEGName           string
	VideoModeJPEGDescription    string
	VideoModeRawYUYVName        string
	VideoModeRawYUYVDescription string

	FramesDropped  string
	LowLatencyMode string

	// Language
	Language          string
	LanguageEnglish   string
	LanguageSpanish   string
	LanguageUkrainian string

	// Connection names
	ConnectionNumber         string // "Connection %d"
	BackupFlashName          string // "Backup Flash"
	PromoBenefitsTitle       string
	PromoFeatureBIOS         string
	PromoFeatureSnapshots    string
	PromoFeaturePowerControl string
	PromoFeatureL0Control    string

	// NBD Server (Android)
	NBDServerManagement string // "NBD Server - Management"
	NBDServerForAndroid string // "NBD Server for Android"
	NBDImageNotSelected string // "Image not selected"
	NBDImageSelected    string // "Selected: %s\nSize: %d MB"
	NBDSelectImage      string // "Select image (.iso/.img)"
	NBDStartServer      string // "Start NBD server"
	NBDStopServer       string // "Stop NBD server"
	NBDRefreshStatus    string // "Refresh status"
	NBDListenAddress    string // "Listen address:"

	// Virtual keyboard
	VirtualKeyboard string // "Virtual keyboard"

	// Device names
	DeviceKeyboard            string // "Keyboard"
	DeviceTouchPad            string // "TouchPad" (relative touchpad mode)
	DeviceMouse               string // "Mouse" (USB pointer device name)
	DeviceTouch               string // "Touch" (touchscreen mode)
	DeviceAbsolute            string // "Absolute" (absolute pointing mode)
	DeviceAbsoluteLeft2       string // "Abs L/2" (absolute, left display of 2)
	DeviceAbsoluteRight2      string // "Abs R/2" (absolute, right display of 2)
	DeviceVirtualCursor       string // "Cursor" (virtual cursor mode, Android only)
	DeviceGyroMouse           string // "GyroMouse" (gyroscope cursor mode, Android only)
	DeviceNetworkCard         string // "Network Card (RNDIS)"
	DeviceGamepad             string // "Gamepad"
	DeviceDirectInput         string // "DirectInput"
	DeviceXInput              string // "XInput"
	XInputIncompatibleWithHID string // error: XInput + keyboard/mouse
	ShowMouseCursor           string // "Show Mouse" (show cursor in captured video)
	ClipboardSyncEnabled      string // "Shared Clipboard" (toggle clipboard sync with the agent)
	EnableVSync               string // "VSync" (enable vsync for capture card)
	AIVision                  string // "AI Vision" (live Set-of-Mark detection overlay checkbox)
	AIVisionHint              string // hint shown under the AI Vision checkbox
	MuteAudio                 string // "Mute Audio"
	UnmuteAudio               string // "Unmute Audio"
	DeviceAudio               string // "Audio"
	DeviceUSBAudio            string // "USB Audio Codec"
	AudioDeviceUAC1           string // "UAC1"
	AudioDeviceUAC2           string // "UAC2"
	DriveModeDisk             string // "USB Stick"
	DriveModeCDROM            string // "CD-ROM"

	// Deep link handler
	DeepLinkServerAddress string // "Server address:"
	DeepLinkToken         string // "Master Key:"
	DeepLinkConnectPrompt string // "Do you want to connect to this server?\n\nChoose action:"
	DeepLinkConnect       string // "Connect"
	DeepLinkSave          string // "Save"

	// Error messages
	ErrorMessage               string // "Error: %v"
	ErrorNotConnectedShort     string // "Error: not connected to USBridge"
	ErrorVideoStart            string // "Error starting video: %v"
	ErrorVideoInfo             string // "Error getting video information"
	VideoWaitingConnection     string // "Waiting for connection..."
	VideoInfoReceived          string // "Video information received"
	VideoInfoUnavailable       string // "Video information unavailable"
	VirtualKeyboardClickToType string // "click to type"
	UnitPx                     string // "px"
	UnitPercent                string // "%"
	UnitKbps                   string // "kbps"
	UnitMbps                   string // "Mbps"
	DeviceRowTemplateName      string // placeholder for device list row
	DeviceRowTemplateStatus    string // placeholder for device list row
	FullscreenWindowTitle      string // "USBridge - Fullscreen"
	SnapshotRowTemplateSize    string // placeholder for snapshot list row
	SnapshotRowTemplateDate    string // placeholder for snapshot list row

	// Time formats
	TimeFormat     string
	DateFormat     string
	DateTimeFormat string

	// PC Panel (Power/Reset buttons, LED indicators)
	PCPanelPowerTitle            string // "Power"
	PCPanelPowerConfirm          string // "Press Power button?"
	PCPanelActionConfirm         string // "Confirm action?"
	PCPanelPowerExecuteConfirm   string // "Execute Power Off?"
	PCPanelPowerHoldTime         string // "Hold time (seconds):"
	PCPanelPowerShortPress       string // "Short press"
	PCPanelPowerLongPress        string // "Long press (%d s)"
	PCPanelResetTitle            string // "Reset"
	PCPanelResetConfirm          string // "Press Reset button?"
	PCPanelResetExecuteConfirm   string // "Execute Reset?"
	PCPanelPowerLED              string // "Power LED"
	PCPanelHDDLED                string // "HDD LED"
	PCPanelLongPressNotSupported string // "Long press: will be supported in future"

	// Moonlight manual pairing (shown when the host has no usbridge auto-pair
	// endpoint -- a stock Sunshine or real NVIDIA GameStream host)
	PairingPINTitle   string // "Pairing Required"
	PairingPINMessage string // "Enter this PIN on your host's pairing page:"
	PairingPINWaiting string // "Waiting for the host to accept it..."
}

// EN returns English localization
func EN() *LocalizedStrings {
	return &LocalizedStrings{
		// Main Window
		AppTitle:                "USBridge Client",
		ServerAddress:           "Server Address",
		Token:                   "Master Key",
		ConnectButton:           "Connect",
		DisconnectButton:        "Disconnect",
		DisconnectAllButton:     "Disconnect All",
		VideoStreamActiveButton: "Open Control",
		TabDevices:              "💽 Device",
		TabControl:              "📺 Control",
		TabSnapshots:            "💾 Snapshots",

		// Connection Manager
		ConnectionManager:         "🔌 Connection Manager",
		SavedConnections:          "Connections",
		ConnectionManagement:      "💾 Connection Management",
		AddressAndTokenHint:       "💡 Address and Master Key are entered in the bar above",
		ConnectionHeroEyebrow:     "USBRIDGE ACCESS",
		ConnectionPanelHint:       "Launch a saved profile or create a new one.",
		ConnectionNameLabel:       "Name for saving:",
		ConnectionNamePlaceholder: "Connection name (e.g.: My PC)",
		SaveButton:                "💾 Save",
		DeleteButton:              "Delete",
		EditButton:                "✏️",
		QRScannerButton:           "📷 QR",
		EditConnectionTitle:       "Edit connection",
		AddConnectionTitle:        "Add connection",
		AddNewDeviceTitle:         "Add a new device",
		NoSavedConnections:        "No saved connections yet",
		NoSavedConnectionsHint:    "Use QR or add a connection below to get started.",
		OnboardingStepConnect:     "Plug usb-c into target host for power and hid. Connect video path from target hdmi to capture dongle.",
		OnboardingStepIP:          "Navigate to settings -> internet to connect your network.",
		OnboardingStepScan:        "Navigate to the Master Key section and scan the QR code. You can also enter the IP and Master Key manually.",
		DeleteConnectionTitle:     "Delete connection",
		DeleteConnectionConfirm:   "Are you sure you want to delete connection \"%s\"?",
		TailscaleRegisterLabel:    "Register in Tailscale",
		ConnectingToConnection:    "Connecting to \"%s\"…",

		// Video Widget
		VideoNotStarted:      "Video not started",
		StartVideoButton:     "▶️",
		StopVideoButton:      "⏹️",
		FullscreenButton:     "🔍",
		StartingVideoCapture: "Starting video capture...",
		WaitingServerStart:   "Waiting for server to start...",
		VideoActive:          "✅ Video capture active",
		StoppingVideoCapture: "Stopping video capture...",
		VideoStopped:         "🛑 Video capture stopped",
		ErrorNoConnection:    "Error: not connected to USBridge",
		ErrorWindowNotInit:   "Error: window not initialized",

		// Backup Widget
		BackupFlash:              "💾 Backup Flash",
		CurrentFlashAndSnapshots: "Current flash and available snapshots",
		CurrentFlash:             "Current Flash",
		ReadyToWork:              "Ready to work",
		LoadingSnapshots:         "Loading snapshots...",
		WaitingConnection:        "Waiting for connection...",
		ErrorLoadingSnapshots:    "Error loading snapshots",
		LoadedSnapshots:          "Loaded %d snapshots",
		MountingFlash:            "Mounting flash %s...",
		MountingSnapshot:         "Mounting snapshot %s...",
		FlashMounted:             "Flash %s mounted",
		SnapshotMounted:          "Snapshot %s mounted",
		ErrorMounting:            "Mounting error: %v",
		ErrorNotConnected:        "not connected to USBridge",
		ErrorFlashNotFound:       "current flash not found",
		FreeDeviceSlotRequired:   "To connect backup or snapshot, free one device slot first (on device screen)",
		ErrorLoadingLocalDevices: "Error loading local devices: %v",
		ErrorMountingFlashMsg:    "Error mounting current flash: %v",
		ErrorMountingSnapshotMsg: "Error mounting snapshot: %v",
		ErrorStatusFormat:        "Error: %v",

		SnapshotDetailsTitle:   "Snapshot: %s",
		SnapshotDetailsDate:    "Date: %s",
		SnapshotDetailsSize:    "Size: %s",
		SnapshotChangelogTitle: "Changes (changelog)",
		SnapshotChangelogEmpty: "Changelog unavailable",
		SnapshotTempFile:       "temporary file",
		OK:                     "OK",
		Copy:                   "Copy",

		ChangelogOpSnapshot: "snapshot creation",
		ChangelogOpUtimes:   "time update",
		ChangelogOpMkfile:   "file creation",
		ChangelogOpRename:   "rename",
		ChangelogOpTruncate: "file truncation",
		ChangelogOpClone:    "clone",
		ChangelogOpChown:    "owner change",
		ChangelogOpChmod:    "permissions change",

		// Disk Widget
		Devices:                           "💽 Devices",
		AllAvailableDevices:               "Organized into storage, console control, and connectivity.",
		DevicesSectionStorage:             "Storage",
		DevicesSectionStorageEyebrow:      "VIRTUAL DISK",
		DevicesSectionStorageHint:         "ISO and IMG media for upload, swapping, and on-demand mounting.",
		DevicesSectionBackup:              "Backup Device",
		DevicesSectionBackupEyebrow:       "BACKUP + MTP",
		DevicesSectionBackupHint:          "MTP-connected backup storage exposed by the device service.",
		DevicesSectionControl:             "Control Console",
		DevicesSectionControlEyebrow:      "VIDEO + INPUT",
		DevicesSectionControlHint:         "Capture, keyboard, and pointer devices that operate together as the KVM console.",
		DevicesSectionConnectivity:        "Connectivity",
		DevicesSectionConnectivityEyebrow: "NETWORK",
		DevicesSectionConnectivityHint:    "RNDIS bridge and channel infrastructure used to link the remote host.",
		DevicesSectionAudio:               "Audio",
		DevicesSectionAudioHint:           "Audio capture sources and USB Audio Codec gadget.",
		LocalDrives:                       "Local Drives",
		NetworkDrives:                     "Network Drives",
		MountButton:                       "🔌 Mount",
		MountButtonCompact:                "🔌 Mount",
		UnmountButton:                     "🔴 Unmount",
		UnmountButtonCompact:              "❌",
		AddImageButton:                    "➕",
		RefreshButton:                     "🔄",
		LoadingFromCloud:                  "Loading from cloud",
		CloudFilesDetected:                "Google Drive files detected among selected files.",
		AndroidBuffering:                  "Android is buffering cloud files to the device.",
		PreparingToMount:                  "Preparing to mount",
		MayTake30Seconds:                  "This may take up to 30 seconds per file.",
		PleaseWait:                        "Please wait...",
		MaxDevicesReached:                 "Maximum 5 devices can be selected",
		UnmountAllConfirm:                 "Unmount all connected devices?",
		UnmountSelectedConfirm:            "Unmount selected devices?",
		NoMountedDevices:                  "No connected devices to unmount",
		SelectDevicesToMount:              "Select devices to connect",
		StoppingAllDevices:                "Stopping all devices...",
		StoppingNBDServers:                "Stopping NBD servers...",
		AllDevicesUnmounted:               "All devices unmounted",

		// Status Messages
		StatusConnected:    "Connected",
		StatusDisconnected: "Disconnected",
		StatusConnecting:   "Connecting...",
		StatusError:        "Error",

		// QR Scanner
		QRCodeScanned:           "✓ QR code scanned",
		Apply:                   "Apply",
		Cancel:                  "Cancel",
		ServerAddressLabel:      "Server address:",
		TokenLabel:              "Master Key:",
		ScanSuccess:             "✓ QR code successfully scanned!\n\nCheck the data and press 'Apply' to fill in the fields.",
		ErrorOpeningFile:        "Error opening file: %v",
		ErrorDecodingImage:      "Error decoding image: %v",
		ErrorProcessingImage:    "Error processing image: %v",
		ErrorLaunchingQRScanner: "Error launching QR scanner: %v",
		ImageNotLoaded:          "Image not loaded",
		QRCodeNotFound:          "QR code not found in image.\n\nMake sure that:\n• Image contains QR code\n• QR code is clear and visible\n• Image has sufficient resolution",
		InvalidQRFormat:         "Invalid QR code format.\nExpected: host:master_key\nReceived: %s",
		HostCannotBeEmpty:       "Host address cannot be empty",
		QRExampleText:           "QR code example:\n\n%s\n\nUse a QR code generator to create an image with this text.",
		CopyText:                "📋 Copy text",
		TextCopiedToClipboard:   "Text copied to clipboard!",
		TestQRCode:              "📱 Test QR code",
		QRCodeForConnection:     "QR code for connection:",
		QRCodeLabel:             "QR code: %s",
		PointCameraAtQR:         "Point the camera at QR code...",
		QRScanning:              "QR code scanning",
		ErrorStartingCamera:     "Failed to start camera: %v",
		ErrorSunshineNoWebRTC:   "This device is running Sunshine, which doesn't support WebRTC video in the browser. Switch it to RustShine (available with a subscription) to watch and control it from the web client.",

		// Dialogs
		Yes:                          "Yes",
		No:                           "No",
		Error:                        "Error",
		Information:                  "Information",
		Confirmation:                 "Confirmation",
		TailscaleLogoutConfirm:       "Do you want to sign out of Tailscale?",
		Done:                         "Done",
		Success:                      "Success",
		Close:                        "Close",
		VideoSettingsApplied:         "Video quality settings applied",
		StoragePermissionRequired:    "Storage permission required",
		StoragePermissionMessage:     "To select files, storage permission is required.",
		StoragePermissionSteps:       "Please:\n1. Open Android Settings\n2. Apps → USBridge Client\n3. Permissions → Storage\n4. Allow access\n\nThen try again.",
		ErrorSelectingFile:           "Error selecting file: %v",
		UnsupportedFileFormat:        "Unsupported file format. Supported: %s",
		FileAlreadyAdded:             "This file is already in the list",
		SelectDiskImage:              "Select disk image (ISO, IMG, VMDK, VDI)",
		DeleteImageTitle:             "Delete image",
		DeleteImageConfirm:           "Are you sure you want to delete the image:\n%s\n\nThe file on disk will not be deleted.",
		DeleteImageFromDeviceConfirm: "Are you sure you want to delete the image from the device?\n%s\n\nThis action cannot be undone.",
		UploadImageTitle:             "Upload image",
		UploadImageConfirm:           "Do you want to upload the image to the device?\n%s\n\nThis may take some time.",
		ImageUploadedSuccess:         "Image %s successfully uploaded to device",
		ImageDeletedSuccess:          "Image %s successfully deleted from device",
		ErrorOpeningFileForUpload:    "Failed to open file: %v",
		ErrorUploadingImage:          "Error uploading image: %v",
		ErrorDeletingImage:           "Error deleting image: %v",
		ConnectViaLink:               "Connect via link",
		DeepLinkError:                "Error processing link: %v",
		ConnectionLost:               "Connection lost: %v",
		SAFFilePicker:                "SAF File Picker",
		SAFInstructions:              "To select file:\n\n1. Press OK\n2. In Android file manager select .iso/.img file\n3. The app will receive fd and call callback\n\nAfter selecting the file you will see its info here.",
		FileSelected:                 "File selected",
		NBDImageSelectedGB:           "Image: %s\nSize: %.2f GB",
		NBDAllowLAN:                  "Allow LAN access (0.0.0.0)",
		NBDStarted:                   "NBD started",
		NBDStartedInstructions:       "NBD server started on %s\n\nConnect from computer:\nsudo nbd-client PHONE_IP 10809 /dev/nbd0 -read-only\n\nPhone IP can be found in: Settings → Network → Wi-Fi",
		NBDStopped:                   "NBD stopped",
		NBDStoppedSuccess:            "NBD server successfully stopped",
		NBDStatusStopped:             "Status: Stopped",
		NBDStatusRunning:             "Status: Running on %s",
		NBDStatusError:               "Status: Error - %s",
		NBDInstructions:              "Instructions:\n\n1. Select image file (.iso/.img) from microSD via SAF\n2. Configure address (default 127.0.0.1:10809)\n3. Press 'Start NBD server'\n4. On computer connect:\n   sudo nbd-client PHONE_IP 10809 /dev/nbd0 -read-only\n\nSecurity:\n• Default: local access only (127.0.0.1)\n• For network access enable 'LAN mode'\n• Image is mounted read-only\n\nServer runs in background even when screen is off",
		NBDStartFailed:               "Failed to start NBD backend: %v",
		NBDStopError:                 "Error stopping: %v",
		ConnectionTitle:              "Connection",

		// Auto-update
		UpdateAvailableTitle:     "Update Available",
		UpdateAvailableMessage:   "Version %s is available (you have %s). Update now?",
		UpdateNowButton:          "Update",
		UpdateLaterButton:        "Not Now",
		UpdateDownloadingTitle:   "Updating…",
		UpdateDownloadingMessage: "Downloading version %s…",

		// Video Settings/Dialogs
		VideoQualitySettings: "Video Quality Settings",
		Width:                "Width:",
		Height:               "Height:",
		FPS:                  "FPS:",
		FramesPerSecond:      "frames/sec",
		Quality:              "Quality:",
		Bitrate:              "Bitrate:",
		InvalidWidth:         "invalid width value: %v",
		InvalidHeight:        "invalid height value: %v",
		InvalidFPS:           "invalid FPS value: %v",
		InvalidQuality:       "invalid quality value: %v",
		InvalidBitrate:       "invalid bitrate value: %v",
		WidthRange:           "width must be from 320 to 1920 pixels",
		HeightRange:          "height must be from 240 to 1080 pixels",
		FPSRange:             "FPS must be from 1 to 120",
		QualityRange:         "quality must be from 1 to 100 percent",
		BitrateRange:         "bitrate must be from 100 to 150000 kbps",

		// Video Start Dialog
		VideoParameters:             "Video Parameters",
		Resolution:                  "Resolution",
		FrameRate:                   "Frame Rate",
		StreamMode:                  "Streaming Mode",
		StartVideo:                  "Start",
		Starting:                    "Starting...",
		SwitchingDevice:             "Switching device...",
		ConnectingRTP:               "Connecting to RTP/UDP (%d/%d)...",
		VideoLaunchFailed:           "Failed to connect after %d attempts",
		CancelVideoStart:            "Cancel video start",
		CaptureDevice:               "Capture device",
		VideoDevicesNotFound:        "Capture video devices not found",
		VideoDeviceEmpty:            "Video device is empty",
		VideoDeviceSelected:         "Selected",
		VideoDeviceUnavailable:      "Unavailable",
		VideoDeviceCurrent:          "Current",
		SettingsAction:              "Settings",
		FullscreenAction:            "Fullscreen",
		VideoJPEGRTPHint:            "JPEG RTP: for MJPEG sources the server forwards JPEG directly; for YUYV sources it encodes JPEG before sending.",
		VideoRawYUYVHint:            "RAW YUYV: uncompressed video over RTP. Very high bandwidth, use only on fast local links.",
		VideoModeH264Name:           "H.264",
		VideoModeH264Description:    "UVC capture → H.264 encode → RTP/UDP",
		VideoModeH265Name:           "H.265",
		VideoModeH265Description:    "HEVC hardware encode — better quality at lower bitrate",
		VideoModeAV1Name:            "AV1",
		VideoModeAV1Description:     "AV1 hardware encode — best compression (requires Apple Silicon)",
		VideoModeJPEGName:           "JPEG RTP",
		VideoModeJPEGDescription:    "MJPEG direct / YUYV encode → RTP",
		VideoModeRawYUYVName:        "RAW YUYV",
		VideoModeRawYUYVDescription: "YUYV (Uncompressed) → RTP → Direct Render",

		FramesDropped:  "Dropped",
		LowLatencyMode: "Low latency mode",

		// Language
		Language:          "Language",
		LanguageEnglish:   "English",
		LanguageSpanish:   "Spanish",
		LanguageUkrainian: "Ukrainian",

		// Connection names
		ConnectionNumber:         "Connection %d",
		BackupFlashName:          "Backup Flash",
		PromoBenefitsTitle:       "Benefits you'll get",
		PromoFeatureBIOS:         "BIOS-in-Terminal",
		PromoFeatureSnapshots:    "Snapshots",
		PromoFeaturePowerControl: "Power Control",
		PromoFeatureL0Control:    "L0 Control",

		// NBD Server (Android)
		NBDServerManagement: "NBD Server - Management",
		NBDServerForAndroid: "NBD Server for Android",
		NBDImageNotSelected: "Image not selected",
		NBDImageSelected:    "Selected: %s\nSize: %d MB",
		NBDSelectImage:      "Select image (.iso/.img)",
		NBDStartServer:      "Start NBD server",
		NBDStopServer:       "Stop NBD server",
		NBDRefreshStatus:    "Refresh status",
		NBDListenAddress:    "Listen address:",

		// Virtual keyboard
		VirtualKeyboard: "Virtual keyboard",

		// Device names
		DeviceKeyboard:            "Keyboard",
		DeviceTouchPad:            "TouchPad",
		DeviceMouse:               "Mouse",
		DeviceTouch:               "TouchScreen",
		DeviceAbsolute:            "Absolute",
		DeviceAbsoluteLeft2:       "Abs L/2",
		DeviceAbsoluteRight2:      "Abs R/2",
		DeviceVirtualCursor:       "Cursor",
		DeviceGyroMouse:           "GyroMouse",
		DeviceNetworkCard:         "Network Card (RNDIS)",
		DeviceGamepad:             "Gamepad",
		DeviceDirectInput:         "DirectInput",
		DeviceXInput:              "XInput",
		XInputIncompatibleWithHID: "XInput gamepad cannot be used together with keyboard or mouse. Connect gamepad separately.",
		ShowMouseCursor:           "Show Mouse",
		ClipboardSyncEnabled:      "Shared Clipboard",
		EnableVSync:               "VSync",
		AIVision:                  "AI Vision",
		AIVisionHint:              "Overlays live object detection (Set-of-Mark boxes + hex ids) on the video, as an agent's ui.parse call would see it.",
		MuteAudio:                 "Mute Audio",
		UnmuteAudio:               "Unmute Audio",
		DeviceAudio:               "Audio",
		DeviceUSBAudio:            "USB Audio Codec",
		AudioDeviceUAC1:           "UAC1",
		AudioDeviceUAC2:           "UAC2",
		DriveModeDisk:             "USB Stick",
		DriveModeCDROM:            "CD-ROM",

		// Deep link handler
		DeepLinkServerAddress: "Server address:",
		DeepLinkToken:         "Master Key:",
		DeepLinkConnectPrompt: "Do you want to connect to this server?\n\nChoose action:",
		DeepLinkConnect:       "Connect",
		DeepLinkSave:          "Save",

		// Error messages
		ErrorMessage:               "Error: %v",
		ErrorNotConnectedShort:     "Error: not connected to USBridge",
		ErrorVideoStart:            "Error starting video: %v",
		ErrorVideoInfo:             "Error getting video information",
		VideoWaitingConnection:     "Waiting for connection...",
		VideoInfoReceived:          "Video information received",
		VideoInfoUnavailable:       "Video information unavailable",
		VirtualKeyboardClickToType: "click to type",
		UnitPx:                     "px",
		UnitPercent:                "%",
		UnitKbps:                   "kbps",
		UnitMbps:                   "Mbps",
		DeviceRowTemplateName:      "—",
		DeviceRowTemplateStatus:    "—",
		FullscreenWindowTitle:      "USBridge - Fullscreen",
		SnapshotRowTemplateSize:    "—",
		SnapshotRowTemplateDate:    "—",

		// Time formats
		TimeFormat:     "15:04:05",
		DateFormat:     "02.01.2006",
		DateTimeFormat: "02.01.2006 15:04",

		// PC Panel
		PCPanelPowerTitle:            "Power",
		PCPanelPowerConfirm:          "Press Power button?",
		PCPanelActionConfirm:         "Confirm action?",
		PCPanelPowerExecuteConfirm:   "Execute Power Off?",
		PCPanelPowerHoldTime:         "Hold time (seconds):",
		PCPanelPowerShortPress:       "Short press",
		PCPanelPowerLongPress:        "Long press (%d s)",
		PCPanelResetTitle:            "Reset",
		PCPanelResetConfirm:          "Press Reset button?",
		PCPanelResetExecuteConfirm:   "Execute Reset?",
		PCPanelPowerLED:              "Power LED",
		PCPanelHDDLED:                "HDD LED",
		PCPanelLongPressNotSupported: "Long press: will be supported in future",

		// Moonlight manual pairing
		PairingPINTitle:   "Pairing Required",
		PairingPINMessage: "Enter this PIN on your host's pairing page:",
		PairingPINWaiting: "Waiting for the host to accept it...",
	}
}

// ES returns the Spanish locale.
func ES() *LocalizedStrings {
	locale := EN()
	locale.AppTitle = "Cliente USBridge"
	locale.ServerAddress = "Direccion del servidor"
	locale.Token = "Master Key"
	locale.ConnectButton = "Conectar"
	locale.DisconnectButton = "Desconectar"
	locale.DisconnectAllButton = "Desconectar todo"
	locale.VideoStreamActiveButton = "Abrir control"
	locale.TabDevices = "Dispositivos"
	locale.TabControl = "Control"
	locale.TabSnapshots = "Instantaneas"
	locale.ConnectionManager = "Administrador de conexiones"
	locale.SavedConnections = "Conexiones"
	locale.ConnectionManagement = "Gestion de conexiones"
	locale.ConnectionHeroEyebrow = "ACCESO USBRIDGE"
	locale.ConnectionPanelHint = "Inicia un perfil guardado o crea uno nuevo."
	locale.EditConnectionTitle = "Editar conexion"
	locale.AddConnectionTitle = "Agregar conexion"
	locale.AddNewDeviceTitle = "Agregar un nuevo dispositivo"
	locale.NoSavedConnections = "Todavia no hay conexiones guardadas"
	locale.NoSavedConnectionsHint = "Usa QR o agrega una conexion para comenzar."
	locale.PromoBenefitsTitle = "Beneficios que recibiras"
	locale.PromoFeatureBIOS = "BIOS-in-Terminal"
	locale.PromoFeatureSnapshots = "Snapshots"
	locale.PromoFeaturePowerControl = "Power Control"
	locale.PromoFeatureL0Control = "L0 Control"
	locale.OnboardingStepConnect = "Conecta USB-C al host de destino para energia y HID. Conecta la salida HDMI al capturador."
	locale.OnboardingStepIP = "Abre configuracion de red y conecta el dispositivo a tu red."
	locale.OnboardingStepScan = "Escanea el codigo QR del Master Key o introduce IP y Master Key manualmente."
	locale.SaveButton = "Guardar"
	locale.DeleteButton = "Eliminar"
	locale.DeleteConnectionTitle = "Eliminar conexion"
	locale.DeleteConnectionConfirm = "Seguro que deseas eliminar la conexion \"%s\"?"
	locale.ConnectingToConnection = "Conectando a \"%s\"…"
	locale.Devices = "Dispositivos"
	locale.DevicesSectionStorage = "Almacenamiento"
	locale.DevicesSectionBackup = "Dispositivo de respaldo"
	locale.DevicesSectionControl = "Consola de control"
	locale.DevicesSectionConnectivity = "Conectividad"
	locale.LocalDrives = "Unidades locales"
	locale.NetworkDrives = "Unidades de red"
	locale.MountButton = "Montar"
	locale.UnmountButton = "Desmontar"
	locale.RefreshButton = "Actualizar"
	locale.LoadingFromCloud = "Cargando desde la nube"
	locale.StatusConnected = "Conectado"
	locale.StatusDisconnected = "Desconectado"
	locale.StatusConnecting = "Conectando..."
	locale.StatusError = "Error"
	locale.Apply = "Aplicar"
	locale.Cancel = "Cancelar"
	locale.Yes = "Si"
	locale.No = "No"
	locale.Error = "Error"
	locale.Information = "Informacion"
	locale.Confirmation = "Confirmacion"
	locale.TailscaleLogoutConfirm = "Deseas cerrar sesion en Tailscale?"
	locale.TailscaleRegisterLabel = "Registrar en Tailscale"
	locale.Success = "Exito"
	locale.Done = "Hecho"
	locale.Close = "Cerrar"
	locale.ConnectionTitle = "Conexion"
	locale.UpdateAvailableTitle = "Actualizacion disponible"
	locale.UpdateAvailableMessage = "La version %s esta disponible (tienes %s). Actualizar ahora?"
	locale.UpdateNowButton = "Actualizar"
	locale.UpdateLaterButton = "Ahora no"
	locale.UpdateDownloadingTitle = "Actualizando…"
	locale.UpdateDownloadingMessage = "Descargando la version %s…"
	locale.VideoQualitySettings = "Configuracion de calidad de video"
	locale.Resolution = "Resolucion"
	locale.FrameRate = "Frecuencia"
	locale.StreamMode = "Modo de transmision"
	locale.StartVideo = "Iniciar"
	locale.SettingsAction = "Configuracion"
	locale.FullscreenAction = "Pantalla completa"
	locale.Language = "Idioma"
	locale.LanguageEnglish = "English"
	locale.LanguageSpanish = "Spanish"
	locale.LanguageUkrainian = "Ukrainian"
	locale.DeviceKeyboard = "Teclado"
	locale.DeviceTouchPad = "TouchPad"
	locale.DeviceMouse = "Raton"
	locale.DeviceTouch = "Tactil"
	locale.DeviceAbsolute = "Absoluto"
	locale.DeviceAbsoluteLeft2 = "Abs I/2"
	locale.DeviceVirtualCursor = "Cursor"
	locale.DeviceGyroMouse = "GyroMouse"
	locale.DeviceAbsoluteRight2 = "Abs D/2"
	locale.DeviceNetworkCard = "Tarjeta de red (RNDIS)"
	locale.ShowMouseCursor = "Mostrar ratón"
	locale.ClipboardSyncEnabled = "Portapapeles compartido"
	locale.EnableVSync = "VSync"
	locale.DeepLinkServerAddress = "Direccion del servidor:"
	locale.DeepLinkToken = "Master Key:"
	locale.DeepLinkConnectPrompt = "Deseas conectarte a este servidor?\n\nElige una accion:"
	locale.DeepLinkConnect = "Conectar"
	locale.DeepLinkSave = "Guardar"
	locale.VirtualKeyboard = "Teclado virtual"
	locale.FullscreenWindowTitle = "USBridge - Pantalla completa"
	locale.PCPanelPowerTitle = "Encendido"
	locale.PCPanelResetTitle = "Reinicio"
	return locale
}

// UK returns the Ukrainian locale.
func UK() *LocalizedStrings {
	locale := EN()
	locale.SavedConnections = "З'єднання"
	locale.EditConnectionTitle = "Редагувати з'єднання"
	locale.AddConnectionTitle = "Додати з'єднання"
	locale.AddNewDeviceTitle = "Додати новий пристрій"
	locale.PromoBenefitsTitle = "Переваги, які ви отримаєте"
	locale.PromoFeatureBIOS = "BIOS-in-Terminal"
	locale.PromoFeatureSnapshots = "Snapshots"
	locale.PromoFeaturePowerControl = "Power Control"
	locale.PromoFeatureL0Control = "L0 Control"
	locale.DeleteButton = "Видалити"
	locale.DeepLinkConnect = "Підключити"
	locale.DeepLinkSave = "Зберегти"
	locale.DeleteConnectionTitle = "Видалити з'єднання"
	locale.Language = "Мова"
	locale.LanguageEnglish = "Англійська"
	locale.LanguageSpanish = "Іспанська"
	locale.LanguageUkrainian = "Українська"
	locale.SettingsAction = "Налаштування"
	locale.FullscreenAction = "На весь екран"
	locale.DeviceKeyboard = "Клавіатура"
	locale.DeviceMouse = "Миша"
	locale.DeviceTouch = "Дотик"
	locale.DeviceAbsolute = "Абсолютний"
	locale.DeviceAbsoluteLeft2 = "Абс Л/2"
	locale.DeviceAbsoluteRight2 = "Абс П/2"
	locale.DeviceVirtualCursor = "Курсор"
	locale.DeviceGyroMouse = "ГіроМиша"
	locale.Close = "Закрити"
	locale.Cancel = "Скасувати"
	locale.Yes = "Так"
	locale.No = "Ні"
	locale.TailscaleLogoutConfirm = "Вийти з Tailscale?"
	return locale
}

// UKProper returns the Ukrainian locale.
func UKProper() *LocalizedStrings {
	locale := EN()
	locale.AppTitle = "Клієнт USBridge"
	locale.ServerAddress = "Адреса сервера"
	locale.Token = "Master Key"
	locale.ConnectButton = "Підключити"
	locale.DisconnectButton = "Відключити"
	locale.DisconnectAllButton = "Відключити все"
	locale.VideoStreamActiveButton = "Відкрити керування"
	locale.TabDevices = "Пристрої"
	locale.TabControl = "Керування"
	locale.TabSnapshots = "Знімки"
	locale.ConnectionManager = "Менеджер з'єднань"
	locale.SavedConnections = "З'єднання"
	locale.ConnectionManagement = "Керування з'єднаннями"
	locale.ConnectionHeroEyebrow = "ДОСТУП USBRIDGE"
	locale.ConnectionPanelHint = "Запустіть збережений профіль або створіть новий."
	locale.EditConnectionTitle = "Редагувати з'єднання"
	locale.AddConnectionTitle = "Додати з'єднання"
	locale.AddNewDeviceTitle = "Додати новий пристрій"
	locale.NoSavedConnections = "Ще немає збережених з'єднань"
	locale.NoSavedConnectionsHint = "Скористайтеся QR або додайте з'єднання вручну."
	locale.PromoBenefitsTitle = "Переваги, які ви отримаєте"
	locale.PromoFeatureBIOS = "BIOS-in-Terminal"
	locale.PromoFeatureSnapshots = "Snapshots"
	locale.PromoFeaturePowerControl = "Power Control"
	locale.PromoFeatureL0Control = "L0 Control"
	locale.OnboardingStepConnect = "Підключіть USB-C до цільового хоста для живлення та HID. Підключіть HDMI до пристрою захоплення."
	locale.OnboardingStepIP = "Відкрийте налаштування мережі та підключіть пристрій до вашої мережі."
	locale.OnboardingStepScan = "Відскануйте QR-код Master Key або введіть IP і Master Key вручну."
	locale.SaveButton = "Зберегти"
	locale.DeleteButton = "Видалити"
	locale.DeleteConnectionTitle = "Видалити з'єднання"
	locale.DeleteConnectionConfirm = "Ви впевнені, що хочете видалити з'єднання \"%s\"?"
	locale.ConnectingToConnection = "Підключення до \"%s\"…"
	locale.Devices = "Пристрої"
	locale.DevicesSectionStorage = "Сховище"
	locale.DevicesSectionBackup = "Резервний пристрій"
	locale.DevicesSectionControl = "Консоль керування"
	locale.DevicesSectionConnectivity = "Мережа"
	locale.LocalDrives = "Локальні диски"
	locale.NetworkDrives = "Мережеві диски"
	locale.MountButton = "Підключити"
	locale.UnmountButton = "Відключити"
	locale.RefreshButton = "Оновити"
	locale.LoadingFromCloud = "Завантаження з хмари"
	locale.StatusConnected = "Підключено"
	locale.StatusDisconnected = "Відключено"
	locale.StatusConnecting = "Підключення..."
	locale.StatusError = "Помилка"
	locale.Apply = "Застосувати"
	locale.Cancel = "Скасувати"
	locale.Yes = "Так"
	locale.No = "Ні"
	locale.Error = "Помилка"
	locale.Information = "Інформація"
	locale.Confirmation = "Підтвердження"
	locale.TailscaleLogoutConfirm = "Вийти з Tailscale?"
	locale.TailscaleRegisterLabel = "Зареєструвати в Tailscale"
	locale.Success = "Успіх"
	locale.Done = "Готово"
	locale.Close = "Закрити"
	locale.ConnectionTitle = "З'єднання"
	locale.UpdateAvailableTitle = "Доступне оновлення"
	locale.UpdateAvailableMessage = "Доступна версія %s (у вас %s). Оновити зараз?"
	locale.UpdateNowButton = "Оновити"
	locale.UpdateLaterButton = "Не зараз"
	locale.UpdateDownloadingTitle = "Оновлення…"
	locale.UpdateDownloadingMessage = "Завантаження версії %s…"
	locale.VideoQualitySettings = "Налаштування якості відео"
	locale.Resolution = "Роздільна здатність"
	locale.FrameRate = "Частота кадрів"
	locale.StreamMode = "Режим потоку"
	locale.StartVideo = "Запустити"
	locale.SettingsAction = "Налаштування"
	locale.FullscreenAction = "На весь екран"
	locale.Language = "Мова"
	locale.LanguageEnglish = "English"
	locale.LanguageSpanish = "Spanish"
	locale.LanguageUkrainian = "Ukrainian"
	locale.DeviceKeyboard = "Клавіатура"
	locale.DeviceTouchPad = "TouchPad"
	locale.DeviceMouse = "Миша"
	locale.DeviceTouch = "Дотик"
	locale.DeviceAbsolute = "Абсолютний"
	locale.DeviceAbsoluteLeft2 = "Абс Л/2"
	locale.DeviceAbsoluteRight2 = "Абс П/2"
	locale.DeviceVirtualCursor = "Курсор"
	locale.DeviceGyroMouse = "ГіроМиша"
	locale.DeviceNetworkCard = "Мережева карта (RNDIS)"
	locale.ShowMouseCursor = "Показувати курсор"
	locale.ClipboardSyncEnabled = "Спільний буфер обміну"
	locale.EnableVSync = "VSync"
	locale.DeepLinkServerAddress = "Адреса сервера:"
	locale.DeepLinkToken = "Master Key:"
	locale.DeepLinkConnectPrompt = "Хочете підключитися до цього сервера?\n\nВиберіть дію:"
	locale.DeepLinkConnect = "Підключити"
	locale.DeepLinkSave = "Зберегти"
	locale.VirtualKeyboard = "Віртуальна клавіатура"
	locale.FullscreenWindowTitle = "USBridge - Повний екран"
	locale.PCPanelPowerTitle = "Живлення"
	locale.PCPanelResetTitle = "Скидання"
	return locale
}

// Current holds the current active localization
var Current *LocalizedStrings

// Init initializes the localization system
func Init(language string) {
	switch language {
	case "es", "ES":
		Current = ES()
	case "uk", "UK", "ua", "UA":
		Current = UKProper()
	default:
		Current = EN()
	}
}

// SetLanguage changes the current language
func SetLanguage(language string) {
	Init(language)
}
