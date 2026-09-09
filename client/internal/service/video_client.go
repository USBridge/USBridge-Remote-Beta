package service

import (
	"errors"
	"image"
	"os"

	"usbridge-client/internal/models"
)

// ErrStreamerUnsupportedWebRTC is wrapped into ConnectToMoonlight's
// returned error (web/wasm build only -- see webrtc_video_client_wasm.go)
// when the agent's active streamer backend doesn't implement WebRTC at
// all: plain Sunshine has no WebRTC support whatsoever, only RustShine
// does. Declared here rather than in the wasm-only file so
// video_widget_ui.go's connect-retry loop (shared across every platform)
// can check for it via errors.Is without a build-tag split of its own, to
// fail fast with a clear message instead of retrying 20x, 500ms apart,
// against a /webrtc/offer endpoint that will never start responding on a
// Sunshine-backed agent.
var ErrStreamerUnsupportedWebRTC = errors.New("agent streamer does not support WebRTC")

// VideoClient defines the common interface for video receiving and rendering services.
type VideoClient interface {
	ConnectToMoonlight() error
	ConnectToUDPViaPipe(pipeReader *os.File) error
	Disconnect() error
	Reconnect() error

	SetOnFrameReceived(callback func(image.Image))
	SetOnStateChanged(callback func(string))
	SetOnError(callback func(error))
	// SetOnPairingPINRequired/SetOnPairingPINResolved: see MoonlightService's
	// own doc comments. Together they bracket the window during which the
	// UI should display the pairing PIN for the user to enter on the host
	// themselves -- needed against any host that isn't this project's own
	// agent (stock Sunshine, real NVIDIA GameStream), which has no endpoint
	// to auto-submit the PIN to.
	SetOnPairingPINRequired(callback func(pin string))
	SetOnPairingPINResolved(callback func())

	IsConnected() bool
	GetStats() map[string]interface{}
	GetConfig() *models.AppConfig

	GetBindHost() string
	UpdateHost(host string)
	UpdateVideoPort(port int)
	UpdateVideoUDPPort(port int)

	SetVideoMode(mode string)
	SetExpectedVideoSize(width, height int)
	SetFPS(fps int)
	SetBitrate(kbps int)
	// SetColor444 requests RustShine Pro 4:4:4 chroma for the next
	// ConnectToMoonlight -- see models.VideoStartRequest.Color444's doc
	// comment for why this is a pure client-side hint (moonlight-common-c's
	// own ANNOUNCE negotiation), not something sent to the agent's REST
	// API. Only takes effect for VideoModeH265; a no-op otherwise.
	SetColor444(enabled bool)

	// NegotiatedVideoCodecName returns the codec the server actually
	// negotiated for the current session (from moonlight-common-c's
	// dr_setup callback), and whether a session has reported one yet.
	// This reflects reality, not what was merely requested.
	NegotiatedVideoCodecName() (string, bool)

	SupportsNativeFullscreen() bool
	IsNativeFullscreenActive() bool
	StartNativeFullscreen() error
	StopNativeFullscreen() error

	ResetRuntimeDecoderFallback()
	SetAutoReconnect(enabled bool)
	SetMaxReconnectAttempts(max int)
}

// MoonlightInputSender is implemented by MoonlightService when a Moonlight stream is
// active. It routes input through LiSend* APIs instead of WebSocket HID.
type MoonlightInputSender interface {
	SendMoonlightKey(vkCode int16, action int8, modifiers int8)
	SendMoonlightMouseMove(dx, dy int16)
	// SendMoonlightMousePosition sends an absolute cursor position via
	// LiSendMousePositionEvent. x/y are in 0..refW/refH range.
	SendMoonlightMousePosition(x, y, refW, refH int16)
	SendMoonlightMouseButton(action int8, button int)
	SendMoonlightScroll(clicks int8)
	SendMoonlightControllerEvent(controllerNumber uint16, activeGamepadMask uint16, buttons uint16, leftTrigger uint8, rightTrigger uint8, leftStickX int16, leftStickY int16, rightStickX int16, rightStickY int16)
	// SendMoonlightUtf8Text sends a UTF-8 text event via LiSendUtf8TextEvent.
	// Use for IME / soft-keyboard rune input where no VK code is available.
	SendMoonlightUtf8Text(text string)
	// IsInputActive returns true only when the underlying Moonlight stream is fully
	// set up and LiSend* calls will actually transmit.
	IsInputActive() bool
}

// Moonlight protocol constants matching Limelight.h KEY_ACTION_* / BUTTON_ACTION_* / BUTTON_*.
const (
	LiKeyActionDown      = int8(0x03)
	LiKeyActionUp        = int8(0x04)
	LiMouseButtonPress   = int8(0x07)
	LiMouseButtonRelease = int8(0x08)
	LiMouseButtonLeft    = 1
	LiMouseButtonMiddle  = 2
	LiMouseButtonRight   = 3

	// Gamepad button flags matching Limelight.h (same as the USB HID report bits).
	LiGamepadDpadUp    = uint16(0x0001)
	LiGamepadDpadDown  = uint16(0x0002)
	LiGamepadDpadLeft  = uint16(0x0004)
	LiGamepadDpadRight = uint16(0x0008)
	LiGamepadStart     = uint16(0x0010)
	LiGamepadBack      = uint16(0x0020)
	LiGamepadLS        = uint16(0x0040)
	LiGamepadRS        = uint16(0x0080)
	LiGamepadLB        = uint16(0x0100)
	LiGamepadRB        = uint16(0x0200)
	LiGamepadGuide     = uint16(0x0400)
	LiGamepadA         = uint16(0x1000)
	LiGamepadB         = uint16(0x2000)
	LiGamepadX         = uint16(0x4000)
	LiGamepadY         = uint16(0x8000)
)
