package assets

import (
	_ "embed"
	"fmt"
	"math"
	"regexp"
	"strings"

	"fyne.io/fyne/v2"
)

var (
	//go:embed arrow-right-333-svgrepo-com.svg
	arrowRightIcon []byte
	//go:embed language-svgrepo-com.svg
	languageIcon []byte
	//go:embed message-chat-square-svgrepo-com.svg
	messageChatSquareIcon []byte
	//go:embed loading-svgrepo-com.svg
	loadingIcon []byte
	//go:embed question-svgrepo-com.svg
	questionIcon []byte
	//go:embed Qr-Code--Streamline-Atlas.svg
	qrCodeIcon []byte
	//go:embed Server-Connect--Streamline-Atlas.svg
	serverConnectIcon []byte
	//go:embed usb-svgrepo-com.svg
	usbTabIcon []byte
	//go:embed monitor-svgrepo-com.svg
	monitorTabIcon []byte
	//go:embed disk-floppy-save-storage-data-svgrepo-com.svg
	snapshotsTabIcon []byte
	//go:embed folder-svgrepo-com.svg
	folderIcon []byte
	//go:embed disc-svgrepo-com.svg
	discIcon []byte
	//go:embed upload-svgrepo-com.svg
	uploadIcon []byte
	//go:embed cam-svgrepo-com.svg
	cameraIcon []byte
	//go:embed keyboard-alt-1-svgrepo-com.svg
	keyboardIcon []byte
	//go:embed mouse-svgrepo-com.svg
	mouseIcon []byte
	//go:embed cursor-pointer.svg
	cursorPointerIcon []byte
	//go:embed gamepad-svgrepo-com.svg
	gamepadIcon []byte
	//go:embed audio-svgrepo-com.svg
	audioIcon []byte
	//go:embed audio-mute-svgrepo-com.svg
	audioMuteIcon []byte
	//go:embed network-backup-svgrepo-com.svg
	networkIcon []byte
	//go:embed sd-card-svgrepo-com.svg
	sdCardIcon []byte
	//go:embed memory-chip-svgrepo-com.svg
	memoryChipIcon []byte
	//go:embed warning-triangle-svgrepo-com.svg
	warningTriangleIcon []byte
	//go:embed warning-svgrepo-com.svg
	warningInfoIcon []byte
	//go:embed info-svgrepo-com.svg
	infoIcon []byte
	//go:embed configuration-vertical-options-svgrepo-com.svg
	configVerticalIcon []byte
	//go:embed connection-internet-network-web-data-storage-svgrepo-com.svg
	connectionStatusIcon []byte
	//go:embed connect-svgrepo-com.svg
	connectIcon []byte
	//go:embed Power-Off-Fill--Streamline-Rounded-Fill-Material-Symbols.svg
	powerOffFillIcon []byte
	//go:embed exit-svgrepo-com.svg
	exitIcon []byte
	//go:embed off.svg
	powerOffIcon []byte
	//go:embed reset.svg
	resetIcon []byte
	//go:embed USBridge.svg
	usbridgeIcon []byte
	//go:embed LogoUSBridge.svg
	logoUSBridgeIcon []byte
	//go:embed LogoUSBridge2.0.svg
	logoUSBridgeLockup []byte
	//go:embed linux-svgrepo-com.svg
	linuxOSIcon []byte
	//go:embed windows-svgrepo-com.svg
	windowsOSIcon []byte
	//go:embed macos-svgrepo-com.svg
	macosOSIcon []byte
	//go:embed link-svgrepo-com.svg
	linkIcon []byte
	//go:embed onboarding/Front_panel.png
	onboardingStep01 []byte
)

var (
	ArrowLeftGray     = fyne.NewStaticResource("arrow-left-gray.svg", colorizeArrow(arrowRightIcon, "#353535", true))
	ArrowLeftWhite    = fyne.NewStaticResource("arrow-left-white.svg", colorizeArrow(arrowRightIcon, "#656565", true))
	ArrowRightGray    = fyne.NewStaticResource("arrow-right-gray.svg", colorizeArrow(arrowRightIcon, "#353535", false))
	ArrowRightWhite   = fyne.NewStaticResource("arrow-right-white.svg", colorizeArrow(arrowRightIcon, "#656565", false))
	DiscordIconDim    = fyne.NewStaticResource("message-chat-square-svgrepo-com-dim.svg", recolorStrokeIcon(messageChatSquareIcon, "#8E8E8E", "1.9"))
	DiscordIcon       = fyne.NewStaticResource("message-chat-square-svgrepo-com.svg", recolorStrokeIcon(messageChatSquareIcon, "#F5F5F5", "1.9"))
	DiscordIconHeader = fyne.NewStaticResource("message-chat-square-svgrepo-com-header.svg", recolorStrokeIcon(messageChatSquareIcon, "#c3c6b4", "1.9"))
	// LanguageIconHeader: language-svgrepo-com.svg is a filled shape (root
	// <svg fill="#000000">, no stroke attribute at all) -- recolorStrokeIcon
	// had nothing to match here, so this rendered solid black regardless of
	// the color passed in. Every other Language* variant already uses
	// recolorFillIcon; this one just hadn't been fixed to match.
	LanguageIconHeader = fyne.NewStaticResource("language-svgrepo-com-header.svg", recolorFillIcon(languageIcon, "#c3c6b4"))
	// QuestionIconHeader: question-svgrepo-com.svg mixes a stroked circle
	// (stroke="#000000") with a filled "?" glyph (fill="#000000") in the same
	// file -- recolorStrokeIcon only recolored the circle, leaving the
	// question mark itself black. recolorMonoIcon recolors both.
	QuestionIconHeader = fyne.NewStaticResource("question-svgrepo-com-header.svg", recolorMonoIcon(questionIcon, "#c3c6b4", "2.6"))
	DiscordIconActive  = fyne.NewStaticResource("message-chat-square-svgrepo-com-active.svg", recolorStrokeIcon(messageChatSquareIcon, "#93C572", "1.9"))
	LanguageIconDim    = fyne.NewStaticResource("language-svgrepo-com-dim.svg", recolorFillIcon(languageIcon, "#8E8E8E"))
	LanguageIconMuted  = fyne.NewStaticResource("language-svgrepo-com-muted.svg", recolorFillIcon(languageIcon, "#C9C9C9"))
	LanguageIcon       = fyne.NewStaticResource("language-svgrepo-com.svg", recolorFillIcon(languageIcon, "#F5F5F5"))
	LanguageIconActive = fyne.NewStaticResource("language-svgrepo-com-active.svg", recolorFillIcon(languageIcon, "#93C572"))
	LoadingGrayFrames  = buildLoadingFrames(loadingIcon, "#111111")
	// VideoConnectingFrames/VideoConnectingGearFrames are the same
	// spinner shapes as LoadingGrayFrames but sized and colored for the
	// video overlay shown while a stream is connecting (VideoWidget's
	// spinnerIcon, see video_widget_spinner.go) -- white-on-transparent
	// rather than the header button's dark fill, since this sits over a
	// black video area rather than a light button background.
	VideoConnectingFrames     = buildLoadingFrames(loadingIcon, "#F5F5F5")
	VideoConnectingGearFrames = buildGearFrames("#F5F5F5")
	// recolorMonoIcon, not recolorStrokeIcon: question-svgrepo-com.svg mixes
	// a stroked circle with a filled "?" glyph -- see QuestionIconHeader's
	// comment above for the full explanation.
	QuestionIconDim   = fyne.NewStaticResource("question-svgrepo-com-dim.svg", recolorMonoIcon(questionIcon, "#8E8E8E", "2.6"))
	QuestionIconMuted = fyne.NewStaticResource("question-svgrepo-com-muted.svg", recolorMonoIcon(questionIcon, "#C9C9C9", "2.6"))
	QuestionIcon      = fyne.NewStaticResource("question-svgrepo-com.svg", recolorMonoIcon(questionIcon, "#F5F5F5", "2.6"))
	QRCodeIcon        = fyne.NewStaticResource("Qr-Code--Streamline-Atlas.svg", qrCodeIcon)
	QRCodeMuted       = fyne.NewStaticResource("Qr-Code--Streamline-Atlas-muted.svg", recolorStrokeIcon(qrCodeIcon, "#656565", "1.3"))
	QRCodeLight       = fyne.NewStaticResource("Qr-Code--Streamline-Atlas-light.svg", recolorStrokeIcon(qrCodeIcon, "#C9C9C9", "1.3"))
	QRCodeAccent      = fyne.NewStaticResource("Qr-Code--Streamline-Atlas-accent.svg", recolorStrokeIcon(qrCodeIcon, "#b6ea93", "1.3"))
	QRCodeTeal        = fyne.NewStaticResource("Qr-Code--Streamline-Atlas-teal.svg", recolorStrokeIcon(qrCodeIcon, "#41e0c3", "1.3"))
	QRCodeBoldBlack   = fyne.NewStaticResource("Qr-Code--Streamline-Atlas-black.svg", recolorStrokeIcon(qrCodeIcon, "#111111", "1.3"))
	// LinkIconMuted/LinkIconLime -- the Paste Link chain-link glyph, shared
	// by the "+" placeholder card's own Paste Link button and the Add
	// Connection dialog's inline paste-link view (both replaced their
	// previous mismatched clipboard/connection glyphs with this one so
	// "paste a link" reads as the same affordance everywhere). Muted is
	// the card's resting-state gray; Lime is the shared hover/accent tint
	// (addConnectionCardHoverColorHex and design.ColorConnectionAddFill
	// are both this same "#c4e77a", so one variant covers both callers).
	LinkIconMuted              = fyne.NewStaticResource("link-svgrepo-com-muted.svg", recolorStrokeIcon(linkIcon, "#c5c8b5", "2"))
	LinkIconLime               = fyne.NewStaticResource("link-svgrepo-com-lime.svg", recolorStrokeIcon(linkIcon, "#c4e77a", "2"))
	ServerConnectIcon          = fyne.NewStaticResource("Server-Connect--Streamline-Atlas.svg", serverConnectIcon)
	ServerConnectMuted         = fyne.NewStaticResource("Server-Connect--Streamline-Atlas-muted.svg", boldenServerIcon(serverConnectIcon, "#656565", "1.9"))
	ServerConnectBold          = fyne.NewStaticResource("Server-Connect--Streamline-Atlas-bold.svg", boldenServerIcon(serverConnectIcon, "#111111", "1.9"))
	ServerConnectGlow          = fyne.NewStaticResource("Server-Connect--Streamline-Atlas-accent-hover.svg", boldenServerIcon(serverConnectIcon, "#b6ea93", "1.9"))
	USBTabIcon                 = fyne.NewStaticResource("usb-svgrepo-com.svg", recolorFillIcon(usbTabIcon, "#F5F5F5"))
	USBTabIconActive           = fyne.NewStaticResource("usb-svgrepo-com-active.svg", recolorFillIcon(usbTabIcon, "#93C572"))
	MonitorTabIcon             = fyne.NewStaticResource("monitor-svgrepo-com.svg", recolorStrokeIcon(monitorTabIcon, "#F5F5F5", "1.9"))
	MonitorTabIconActive       = fyne.NewStaticResource("monitor-svgrepo-com-active.svg", recolorStrokeIcon(monitorTabIcon, "#93C572", "1.9"))
	SnapshotsTabIcon           = fyne.NewStaticResource("disk-floppy-save-storage-data-svgrepo-com.svg", recolorFillIcon(snapshotsTabIcon, "#F5F5F5"))
	SnapshotsTabIconActive     = fyne.NewStaticResource("disk-floppy-save-storage-data-svgrepo-com-active.svg", recolorFillIcon(snapshotsTabIcon, "#93C572"))
	FolderIcon                 = fyne.NewStaticResource("folder-svgrepo-com.svg", recolorFillIcon(folderIcon, "#C9C9C9"))
	FolderIconActive           = fyne.NewStaticResource("folder-svgrepo-com-active.svg", recolorFillIcon(folderIcon, "#93C572"))
	DiscIcon                   = fyne.NewStaticResource("disc-svgrepo-com.svg", recolorFillIcon(discIcon, "#C9C9C9"))
	DiscIconActive             = fyne.NewStaticResource("disc-svgrepo-com-active.svg", recolorFillIcon(discIcon, "#93C572"))
	UploadIcon                 = fyne.NewStaticResource("upload-svgrepo-com.svg", recolorStrokeIcon(uploadIcon, "#F5F5F5", "2"))
	UploadIconMuted            = fyne.NewStaticResource("upload-svgrepo-com-muted.svg", recolorStrokeIcon(uploadIcon, "#8E8E8E", "2"))
	CameraIcon                 = fyne.NewStaticResource("cam-svgrepo-com.svg", recolorStrokeIcon(cameraIcon, "#C9C9C9", "1.8"))
	CameraIconActive           = fyne.NewStaticResource("cam-svgrepo-com-active.svg", recolorStrokeIcon(cameraIcon, "#93C572", "1.8"))
	KeyboardIcon               = fyne.NewStaticResource("keyboard-alt-1-svgrepo-com.svg", recolorStrokeIcon(keyboardIcon, "#C9C9C9", "1.8"))
	KeyboardIconActive         = fyne.NewStaticResource("keyboard-alt-1-svgrepo-com-active.svg", recolorStrokeIcon(keyboardIcon, "#93C572", "1.8"))
	MouseIcon                  = fyne.NewStaticResource("mouse-svgrepo-com.svg", recolorFillIcon(mouseIcon, "#C9C9C9"))
	MouseIconActive            = fyne.NewStaticResource("mouse-svgrepo-com-active.svg", recolorFillIcon(mouseIcon, "#93C572"))
	CursorPointerSVG           = cursorPointerIcon // raw SVG bytes for Vulkan cursor rasterization
	GamepadIcon                = fyne.NewStaticResource("gamepad-svgrepo-com.svg", recolorStrokeIcon(gamepadIcon, "#C9C9C9", "1.8"))
	GamepadIconActive          = fyne.NewStaticResource("gamepad-svgrepo-com-active.svg", recolorStrokeIcon(gamepadIcon, "#93C572", "1.8"))
	AudioIcon                  = fyne.NewStaticResource("audio-svgrepo-com.svg", recolorFillIcon(audioIcon, "#C9C9C9"))
	AudioIconActive            = fyne.NewStaticResource("audio-svgrepo-com-active.svg", recolorFillIcon(audioIcon, "#93C572"))
	AudioMuteIcon              = fyne.NewStaticResource("audio-mute-svgrepo-com.svg", recolorFillIcon(audioMuteIcon, "#C9C9C9"))
	AudioMuteIconActive        = fyne.NewStaticResource("audio-mute-svgrepo-com-active.svg", recolorFillIcon(audioMuteIcon, "#d66d6d"))
	NetworkIcon                = fyne.NewStaticResource("network-backup-svgrepo-com.svg", recolorFillIcon(networkIcon, "#C9C9C9"))
	NetworkIconActive          = fyne.NewStaticResource("network-backup-svgrepo-com-active.svg", recolorFillIcon(networkIcon, "#93C572"))
	SDCardIcon                 = fyne.NewStaticResource("sd-card-svgrepo-com.svg", recolorFillIcon(sdCardIcon, "#C9C9C9"))
	SDCardIconActive           = fyne.NewStaticResource("sd-card-svgrepo-com-active.svg", recolorFillIcon(sdCardIcon, "#93C572"))
	MemoryChipIcon             = fyne.NewStaticResource("memory-chip-svgrepo-com.svg", recolorMemoryChipIcon(memoryChipIcon, "#F5F5F5"))
	WarningTriangleIcon        = fyne.NewStaticResource("warning-triangle-svgrepo-com.svg", recolorFillIcon(warningTriangleIcon, "#F2C14E"))
	WarningInfoIcon            = fyne.NewStaticResource("warning-svgrepo-com.svg", recolorFillIcon(warningInfoIcon, "#4DA3FF"))
	InfoIcon                   = fyne.NewStaticResource("info-svgrepo-com.svg", recolorFillIcon(infoIcon, "#F5F5F5"))
	InfoIconMuted              = fyne.NewStaticResource("info-svgrepo-com-muted.svg", recolorFillIcon(infoIcon, "#8E8E8E"))
	ConfigVerticalIcon         = fyne.NewStaticResource("configuration-vertical-options-svgrepo-com.svg", recolorFillIcon(configVerticalIcon, "#F5F5F5"))
	ConnectionStatusIcon       = fyne.NewStaticResource("connection-internet-network-web-data-storage-svgrepo-com.svg", recolorFillIcon(connectionStatusIcon, "#C9C9C9"))
	ConnectionStatusIconActive = fyne.NewStaticResource("connection-internet-network-web-data-storage-svgrepo-com-active.svg", recolorFillIcon(connectionStatusIcon, "#93C572"))
	ConnectionStatusAccent     = fyne.NewStaticResource("connection-internet-network-web-data-storage-svgrepo-com-accent.svg", recolorFillIcon(connectionStatusIcon, "#b6ea93"))
	ConnectIcon                = fyne.NewStaticResource("connect-svgrepo-com.svg", recolorFillIcon(connectIcon, "#93C572"))
	ConnectIconBoldBlack       = fyne.NewStaticResource("connect-svgrepo-com-black.svg", recolorFillIcon(connectIcon, "#111111"))
	ConnectIconMuted           = fyne.NewStaticResource("connect-svgrepo-com-muted.svg", recolorFillIcon(connectIcon, "#8E8E8E"))
	PowerOffFillRoundIcon      = fyne.NewStaticResource("Power-Off-Fill--Streamline-Rounded-Fill-Material-Symbols.svg", recolorFillIcon(powerOffFillIcon, "#F5F5F5"))
	PowerOffFillRoundIconMuted = fyne.NewStaticResource("Power-Off-Fill--Streamline-Rounded-Fill-Material-Symbols-muted.svg", recolorFillIcon(powerOffFillIcon, "#8E8E8E"))
	ExitIcon                   = fyne.NewStaticResource("exit-svgrepo-com.svg", recolorStrokeIcon(exitIcon, "#d66d6d", "2"))
	PowerOffIcon               = fyne.NewStaticResource("off.svg", recolorFillIcon(powerOffIcon, "#F5F5F5"))
	PowerOffIconActive         = fyne.NewStaticResource("off-active.svg", recolorFillIcon(powerOffIcon, "#111111"))
	ResetIcon                  = fyne.NewStaticResource("reset.svg", recolorFillIcon(resetIcon, "#F5F5F5"))
	ResetIconActive            = fyne.NewStaticResource("reset-active.svg", recolorFillIcon(resetIcon, "#111111"))
	ConnectionEditIconMuted    = fyne.NewStaticResource("connection-edit-muted.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#4E4E4E"><path d="M3 17.25V21h3.75L17.81 9.94l-3.75-3.75L3 17.25zm2.92 2.33H5v-.92l9.06-9.06.92.92L5.92 19.58zM20.71 7.04a1.003 1.003 0 0 0 0-1.42L18.37 3.29a1.003 1.003 0 0 0-1.42 0l-1.13 1.13 3.75 3.75 1.14-1.13z"/></svg>`))
	// GridViewIcon*/ListViewIcon*: connections section header's view-mode
	// toggle (grid layout vs the current list layout) -- inline SVG like
	// ConnectionEditIconMuted above, no source file to embed. Shown next to
	// the "Grid"/"List" text labels, not instead of them.
	GridViewIconAccent = fyne.NewStaticResource("grid-view-accent.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#e9fdbb"><rect x="3" y="3" width="7.5" height="7.5" rx="1.5"/><rect x="13.5" y="3" width="7.5" height="7.5" rx="1.5"/><rect x="3" y="13.5" width="7.5" height="7.5" rx="1.5"/><rect x="13.5" y="13.5" width="7.5" height="7.5" rx="1.5"/></svg>`))
	// Muted variants use #c5c8b5, matching ColorConnectionsSectionMutedText
	// (the same toggle's inactive label color) -- they used to be #656565,
	// the exact same gray as the button's own HoverFill (design.ColorBorder),
	// so the icon visually disappeared into its own hover background.
	GridViewIconMuted  = fyne.NewStaticResource("grid-view-muted.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#c5c8b5"><rect x="3" y="3" width="7.5" height="7.5" rx="1.5"/><rect x="13.5" y="3" width="7.5" height="7.5" rx="1.5"/><rect x="3" y="13.5" width="7.5" height="7.5" rx="1.5"/><rect x="13.5" y="13.5" width="7.5" height="7.5" rx="1.5"/></svg>`))
	ListViewIconAccent = fyne.NewStaticResource("list-view-accent.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="#e9fdbb" stroke-width="2.2" stroke-linecap="round"><line x1="4" y1="6" x2="20" y2="6"/><line x1="4" y1="12" x2="20" y2="12"/><line x1="4" y1="18" x2="20" y2="18"/></svg>`))
	ListViewIconMuted  = fyne.NewStaticResource("list-view-muted.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="#c5c8b5" stroke-width="2.2" stroke-linecap="round"><line x1="4" y1="6" x2="20" y2="6"/><line x1="4" y1="12" x2="20" y2="12"/><line x1="4" y1="18" x2="20" y2="18"/></svg>`))
	USBridgeOSIcon     = fyne.NewStaticResource("USBridge-os.svg", recolorMonoIcon(usbridgeIcon, "#C9C9C9", "1.8"))
	// USBridgeOSIconAccent/*OSIconAgent: the Grid-mode connection card's
	// status icon (see connection_grid_card.go's newConnectionCardStatusIndicator)
	// colored by category instead of the neutral gray above -- KVM (the
	// USBridge hardware itself) in #93c572 (design.ColorAccent's "salad"
	// green), a software Agent's OS glyph in #41e0c3 (design.
	// ColorConnectionBadgeText's teal). Kept as separate resources rather
	// than recoloring at runtime since every other icon here is baked the
	// same way.
	USBridgeOSIconAccent = fyne.NewStaticResource("USBridge-os-accent.svg", recolorMonoIcon(usbridgeIcon, "#93c572", "1.8"))
	LinuxOSIconAgent     = fyne.NewStaticResource("linux-os-agent.svg", recolorMonoIcon(linuxOSIcon, "#41e0c3", "1.8"))
	WindowsOSIconAgent   = fyne.NewStaticResource("windows-os-agent.svg", recolorMonoIcon(windowsOSIcon, "#41e0c3", "1.8"))
	MacOSIconAgent       = fyne.NewStaticResource("macos-os-agent.svg", recolorMonoIcon(macosOSIcon, "#41e0c3", "1.8"))
	LogoUSBridgeIcon     = fyne.NewStaticResource("LogoUSBridge.svg", recolorFillIcon(logoUSBridgeIcon, "#e7fbba"))
	// LogoUSBridgeLockup is the combined logo mark + "USBridge" wordmark
	// (Figma export) used in header bars in place of a separate icon +
	// canvas.Text pair. Already fully colored in the source file -- unlike
	// every other resource here, deliberately embedded raw, with no
	// recolorFillIcon/recolorStrokeIcon pass.
	LogoUSBridgeLockup = fyne.NewStaticResource("LogoUSBridge2.0.svg", logoUSBridgeLockup)
	LinuxOSIcon        = fyne.NewStaticResource("linux-svgrepo-com-os.svg", recolorMonoIcon(linuxOSIcon, "#C9C9C9", "1.8"))
	WindowsOSIcon      = fyne.NewStaticResource("windows-svgrepo-com-os.svg", recolorMonoIcon(windowsOSIcon, "#C9C9C9", "1.8"))
	MacOSIcon          = fyne.NewStaticResource("macos-svgrepo-com-os.svg", recolorMonoIcon(macosOSIcon, "#C9C9C9", "1.8"))
	OnboardingStep01   = fyne.NewStaticResource("Front_panel.png", onboardingStep01)
)

func colorizeArrow(source []byte, fill string, mirror bool) []byte {
	svg := strings.ReplaceAll(string(source), "#000000", fill)
	if !mirror {
		return []byte(svg)
	}

	svg = strings.Replace(svg, `<g id="Page-1"`, `<g transform="translate(11,0) scale(-1,1)" id="Page-1"`, 1)
	return []byte(svg)
}

func recolorFillIcon(source []byte, fill string) []byte {
	svg := strings.ReplaceAll(string(source), "#000000", fill)
	svg = strings.ReplaceAll(svg, "#0F0F0F", fill)
	svg = strings.ReplaceAll(svg, "#222222", fill)
	svg = strings.ReplaceAll(svg, `"black"`, fmt.Sprintf(`"%s"`, fill))
	svg = strings.ReplaceAll(svg, "fill:black", "fill:"+fill)
	svg = strings.ReplaceAll(svg, "currentColor", fill)
	return []byte(svg)
}

func recolorMemoryChipIcon(source []byte, fill string) []byte {
	svg := recolorFillIcon(source, fill)
	out := string(svg)
	out = strings.ReplaceAll(out, "<path ", fmt.Sprintf(`<path fill="%s" `, fill))
	return []byte(out)
}

func recolorStrokeIcon(source []byte, stroke string, width string) []byte {
	svg := string(source)
	strokePattern := regexp.MustCompile(`stroke="(?:#000000|#0F0F0F|#222222|#0C0310|black)"`)
	svg = strokePattern.ReplaceAllString(svg, fmt.Sprintf(`stroke="%s"`, stroke))
	strokeWidthPattern := regexp.MustCompile(`stroke-width="[^"]+"`)
	svg = strokeWidthPattern.ReplaceAllString(svg, fmt.Sprintf(`stroke-width="%s"`, width))
	svg = strings.ReplaceAll(svg, "stroke:black", "stroke:"+stroke)
	return []byte(svg)
}

func recolorMonoIcon(source []byte, color string, strokeWidth string) []byte {
	return recolorStrokeIcon(recolorFillIcon(source, color), color, strokeWidth)
}

func boldenServerIcon(source []byte, stroke string, width string) []byte {
	svg := strings.ReplaceAll(string(source), `stroke="#000000"`, fmt.Sprintf(`stroke="%s"`, stroke))
	svg = strings.ReplaceAll(svg, `stroke-width="1"`, fmt.Sprintf(`stroke-width="%s"`, width))
	return []byte(svg)
}

// buildGearFrames renders a simple rotating gear/cog icon as a sequence of
// SVG frames, one per step around a full rotation -- the same
// procedural-frames approach buildLoadingFrames already uses, so it drives
// through the exact same frame-cycling code (see
// video_widget_spinner.go). Shown in place of the plain dot spinner while
// connecting to a device identified as USBridge/rust-shine hardware
// (isUSBridgeAgentOS) rather than a generic/manual Sunshine host, per the
// distinction that already exists for scripts/backup/pcpanel gating
// elsewhere in this package -- rust-shine is this project's own backend,
// so it gets its own icon instead of the generic Moonlight-style dots.
//
// Deliberately a plain generic gear, not the trademarked Rust logo (which
// is also visually a gear+"R" combination) -- redistributing that mark in
// a commercial client risks a trademark issue neither this shape nor its
// use here needs to run.
// spinnerBackdropSVG is a soft, semi-transparent dark disc baked directly
// into every connecting-spinner frame (gear and dot variants alike), behind
// the actual icon shape. Turns "some dots/a gear floating in an empty rect"
// into a deliberate, modern-looking loading badge, and keeps the spinner
// readable mid-transition (e.g. the instant the DOM video overlay reveals a
// bright frame right where the spinner still sits, one paint before it's
// hidden).
//
// This lives inside the icon's own SVG rather than as a separate Fyne
// canvas.Circle/canvas.Image layered underneath it in
// video_widget_view.go -- an earlier version tried that and produced a
// stray opaque white square around the spinner (a nil-resource
// canvas.Image used purely to give the surrounding Stack a MinSize
// apparently doesn't render as invisible in the wasm canvas backend).
// Baking the backdrop into the same already-proven SVG-resource pipeline
// used for the dots/gear themselves sidesteps that entirely.
const spinnerBackdropSVG = `<circle cx="8" cy="8" r="7.6" fill="#000000" fill-opacity="0.55"/>`

func buildGearFrames(fill string) []fyne.Resource {
	const steps = 12 // animation frames per full rotation
	const teeth = 8  // gear teeth
	const cx, cy = 8.0, 8.0
	const outerR, innerR = 6.6, 4.6 // tooth tip / root radius
	const holeCutoutR = 2.0         // punched-out center hole

	frames := make([]fyne.Resource, steps)
	for frame := range frames {
		angleOffset := float64(frame) * (360.0 / float64(steps))
		var path strings.Builder
		for t := 0; t < teeth*2; t++ {
			angle := (angleOffset + float64(t)*(360.0/float64(teeth*2))) * math.Pi / 180
			r := outerR
			if t%2 == 1 {
				r = innerR
			}
			x := cx + r*math.Cos(angle)
			y := cy + r*math.Sin(angle)
			if t == 0 {
				path.WriteString(fmt.Sprintf("M%.2f %.2f", x, y))
			} else {
				path.WriteString(fmt.Sprintf(" L%.2f %.2f", x, y))
			}
		}
		path.WriteString(" Z")
		// Add inner hole. sweep-flag=0 (CCW) punches a hole using nonzero winding.
		path.WriteString(fmt.Sprintf(" M%.2f %.2f", cx+holeCutoutR, cy))
		path.WriteString(fmt.Sprintf(" A%.2f %.2f 0 1 0 %.2f %.2f", holeCutoutR, holeCutoutR, cx-holeCutoutR, cy))
		path.WriteString(fmt.Sprintf(" A%.2f %.2f 0 1 0 %.2f %.2f", holeCutoutR, holeCutoutR, cx+holeCutoutR, cy))
		path.WriteString(" Z")

		frames[frame] = fyne.NewStaticResource(
			fmt.Sprintf("gear-spinner-%02d.svg", frame),
			[]byte(fmt.Sprintf(
				`<svg viewBox="0 0 16 16" xmlns="http://www.w3.org/2000/svg">`+
					spinnerBackdropSVG+
					`<path d="%s" fill="%s" fill-rule="evenodd"/>`+
					`</svg>`,
				path.String(), fill,
			)),
		)
	}
	return frames
}

func buildLoadingFrames(_ []byte, fill string) []fyne.Resource {
	type dot struct {
		x float32
		y float32
	}

	dots := []dot{
		{8.0, 1.8},
		{11.95, 3.05},
		{14.2, 8.0},
		{11.95, 12.95},
		{8.0, 14.2},
		{4.05, 12.95},
		{1.8, 8.0},
		{4.05, 3.05},
	}
	alphas := []float32{1.0, 0.82, 0.64, 0.46, 0.32, 0.22, 0.16, 0.12}

	frames := make([]fyne.Resource, len(dots))
	for frame := range frames {
		var sb strings.Builder
		sb.WriteString(`<svg viewBox="0 0 16 16" xmlns="http://www.w3.org/2000/svg">`)
		sb.WriteString(spinnerBackdropSVG)
		for idx, point := range dots {
			alpha := alphas[(idx-frame+len(dots))%len(dots)]
			sb.WriteString(fmt.Sprintf(
				`<circle cx="%.2f" cy="%.2f" r="1.55" fill="%s" fill-opacity="%.2f"/>`,
				point.x,
				point.y,
				fill,
				alpha,
			))
		}
		sb.WriteString(`</svg>`)
		frames[frame] = fyne.NewStaticResource(
			fmt.Sprintf("loading-gray-%02d.svg", frame),
			[]byte(sb.String()),
		)
	}
	return frames
}
