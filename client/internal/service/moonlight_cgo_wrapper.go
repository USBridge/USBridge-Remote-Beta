//go:build (darwin || ios || linux) && !android && cgo

package service

/*
// Minimal declarations — definitions live in the platform CGO file (same package).
// The linker resolves them from moonlight_cgo_apple.go or moonlight_cgo_linux.go.
#include <stdint.h>
#include <stdlib.h>

extern int do_li_start(
    const char *address, const char *appVersion, const char *gfeVersion,
    const char *rtspSessionUrl, int serverCodecModeSupport, int videoFormat,
    int width, int height, int fps, int bitrate,
    const unsigned char *rikey, int rikeyid, int pipeFd);
extern void do_li_stop(void);
extern void do_li_interrupt(void);
extern void set_audio_pipe_fd(int fd);
extern void set_audio_muted(int muted);
extern void do_send_key(short vkCode, char action, char modifiers);
extern void do_send_mouse_move(short dx, short dy);
extern void do_send_mouse_position(short x, short y, short refW, short refH);
extern void do_send_mouse_button(char action, int button);
extern void do_send_scroll(signed char clicks);
extern void do_send_multi_controller(
    unsigned short controllerNumber, unsigned short activeGamepadMask,
    unsigned short buttons,
    unsigned char leftTrigger, unsigned char rightTrigger,
    short leftStickX, short leftStickY,
    short rightStickX, short rightStickY);
extern void do_send_utf8_text(const char *text, unsigned int len);
extern void do_get_rtp_video_stats(uint32_t *out);
*/
import "C"

import (
	"fmt"
	"image"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/sirupsen/logrus"

	usbapi "usbridge-client/internal/api"
	"usbridge-client/internal/models"
)

// RTPVideoStats mirrors Limelight.h's RTP_VIDEO_STATS. FecFailed > 0 means a
// frame's FEC redundancy wasn't enough to recover a lost packet -- the
// depacketizer discards that decode unit and (per moonlight-common-c's
// RtpVideoQueue) whatever reference-frame chain it belonged to, which is
// exactly the failure mode that shows up as a corrupted P-frame on screen
// until the next IDR.
type RTPVideoStats struct {
	PacketCountVideo        uint32
	PacketCountFec          uint32
	PacketCountFecRecovered uint32
	PacketCountFecFailed    uint32
	PacketCountOOS          uint32
	PacketCountInvalid      uint32
	PacketCountFecInvalid   uint32
}

// GetRTPVideoStats reads moonlight-common-c's running RTP video counters.
// Safe to call at any time (even with no active session -- moonlight-common-c
// zero-initializes these statics), but only meaningful once a stream is up.
func GetRTPVideoStats() RTPVideoStats {
	var raw [7]C.uint32_t
	C.do_get_rtp_video_stats(&raw[0])
	return RTPVideoStats{
		PacketCountVideo:        uint32(raw[0]),
		PacketCountFec:          uint32(raw[1]),
		PacketCountFecRecovered: uint32(raw[2]),
		PacketCountFecFailed:    uint32(raw[3]),
		PacketCountOOS:          uint32(raw[4]),
		PacketCountInvalid:      uint32(raw[5]),
		PacketCountFecInvalid:   uint32(raw[6]),
	}
}

// startRTPStatsLoggerIfEnabled logs GetRTPVideoStats() periodically for the
// lifetime of `done` when USBRIDGE_LOG_RTP_STATS is set -- opt-in so it never
// runs in normal client builds.
func startRTPStatsLoggerIfEnabled(done <-chan struct{}) {
	if os.Getenv("USBRIDGE_LOG_RTP_STATS") == "" {
		return
	}
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		var prev RTPVideoStats
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				s := GetRTPVideoStats()
				logrus.Infof("🌕 [Moonlight/RTPStats] video=%d fec=%d recovered=%d failed=%d(+%d) oos=%d invalid=%d fecInvalid=%d",
					s.PacketCountVideo, s.PacketCountFec, s.PacketCountFecRecovered,
					s.PacketCountFecFailed, s.PacketCountFecFailed-prev.PacketCountFecFailed,
					s.PacketCountOOS, s.PacketCountInvalid, s.PacketCountFecInvalid)
				prev = s
			}
		}
	}()
}

// vtFrameCallback receives decoded RGBA frames from the hardware decoder.
// Set by the platform-specific player file before StartStream is called.
var (
	vtFrameCallback   func(image.Image)
	vtFrameCallbackMu sync.Mutex
)

var liStartConnectionActive atomic.Bool

// negotiatedVideoFormat holds the VIDEO_FORMAT_* value moonlight-common-c
// reported via dr_setup(NegotiatedVideoFormat, ...) — the server's actual
// codec choice for the current session, as opposed to what the client asked
// for. -1 means "no session has reported a negotiated format yet".
var negotiatedVideoFormat atomic.Int32

func init() {
	negotiatedVideoFormat.Store(-1)
}

var (
	activeStreamDone    chan struct{}
	activeStreamOnce    sync.Once
	activeStreamTermErr error
)

// liStreamMu serializes LiStopConnection / LiStartConnection so they never
// run concurrently. liStreamGen is a generation counter that lets the goroutine
// detect whether it is still the "current" stream before touching shared state.
var (
	liStreamMu  sync.Mutex
	liStartMu   sync.Mutex // Ensures C.do_li_start is never executed concurrently
	liStreamGen atomic.Uint64
)

func closeActiveStreamDone() {
	activeStreamOnce.Do(func() { close(activeStreamDone) })
}

// stopConnectionSafely tears down the current connection without racing an
// in-flight LiStartConnection() on another goroutine. LiStartConnection()
// and LiStopConnection() are documented (Limelight.h) as NOT safe to call
// concurrently with each other -- only LiInterruptConnection() is safe to
// call at any time. Interrupting first unblocks any in-progress
// LiStartConnection() quickly, then waiting on liStartMu guarantees
// do_li_start has fully returned before do_li_stop() touches
// moonlight-common-c's shared static state.
func stopConnectionSafely() {
	C.do_li_interrupt()
	liStartMu.Lock()
	defer liStartMu.Unlock()
	C.do_li_stop()
}

// MoonlightCgoWrapper wraps LiStartConnection from moonlight-common-c.
type MoonlightCgoWrapper struct {
	host           string
	pipeWrite      *os.File
	audioPipeWrite *os.File
	audioMuted     bool
}

func NewMoonlightCgoWrapper(host string) *MoonlightCgoWrapper {
	return &MoonlightCgoWrapper{host: host}
}

func (w *MoonlightCgoWrapper) StartStream(
	rtspSessionUrl string,
	rikey []byte,
	appVersion, gfeVersion string,
	serverCodecModeSupport int,
	videoFormat int,
	width, height, fps, bitrate int,
	pipeWrite *os.File,
	audioPipeWrite *os.File,
	onStop func(error),
) error {
	w.pipeWrite = pipeWrite
	w.audioPipeWrite = audioPipeWrite

	// Hold the stream mutex while stopping any previous connection and resetting
	// state.  This blocks until any in-progress LiStopConnection (from a prior
	// goroutine or from StopStream) has fully returned, preventing concurrent
	// LiStartConnection + LiStopConnection which corrupts moonlight-common-c
	// static state and causes SIGSEGV.
	liStreamMu.Lock()
	stopConnectionSafely()
	myGen := liStreamGen.Add(1)
	activeStreamDone = make(chan struct{})
	activeStreamOnce = sync.Once{}
	activeStreamTermErr = nil
	liStreamMu.Unlock()

	host := C.CString(w.host)
	appVer := C.CString(appVersion)
	gfeVer := C.CString(gfeVersion)
	rtsp := C.CString("rtsp://" + rtspSessionUrl)

	var cRikey *C.uchar
	if len(rikey) == 16 {
		cRikey = (*C.uchar)(C.CBytes(rikey))
	}

	pipeFd := C.int(-1)
	if pipeWrite != nil {
		pipeFd = C.int(pipeWrite.Fd())
	}

	go func() {
		defer C.free(unsafe.Pointer(host))
		defer C.free(unsafe.Pointer(appVer))
		defer C.free(unsafe.Pointer(gfeVer))
		defer C.free(unsafe.Pointer(rtsp))
		if cRikey != nil {
			defer C.free(unsafe.Pointer(cRikey))
		}

		logrus.Infof("🌕 [Moonlight/CGO] LiStartConnection: host=%s %dx%d@%d bitrate=%d",
			w.host, width, height, fps, bitrate)

		if audioPipeWrite != nil {
			C.set_audio_pipe_fd(C.int(audioPipeWrite.Fd()))
		}

		liStartMu.Lock()
		// If another StartStream or StopStream occurred while we waited for
		// the lock, abort this stale attempt.
		if liStreamGen.Load() != myGen {
			liStartMu.Unlock()
			logrus.Info("🌕 [Moonlight/CGO] Aborting stale stream start")
			return
		}

		ret := C.do_li_start(
			host, appVer, gfeVer, rtsp,
			C.int(serverCodecModeSupport), C.int(videoFormat),
			C.int(width), C.int(height), C.int(fps), C.int(bitrate),
			cRikey, C.int(1),
			pipeFd,
		)
		liStartMu.Unlock()

		if int(ret) != 0 {
			logrus.Errorf("🌕 [Moonlight/CGO] LiStartConnection FAILED: code=%d", int(ret))
			C.set_audio_pipe_fd(-1)
			if pipeWrite != nil {
				_ = pipeWrite.Close()
			}
			if audioPipeWrite != nil {
				_ = audioPipeWrite.Close()
			}
			if onStop != nil && liStreamGen.Load() == myGen {
				onStop(fmt.Errorf("LiStartConnection error code %d", int(ret)))
			}
			return
		}

		logrus.Info("🌕 [Moonlight/CGO] ✅ LiStartConnection setup done — streams active")
		// Unconditionally true -- see the Android cgo file's identical fix
		// for why gating this the same way as the generation-checked reset
		// below caused a real bug: under reconnect races this branch could
		// run for a stale generation and skip the store, leaving
		// IsInputActive() stuck false (and every mouse/keyboard send, all
		// gated on it, silently dropped) even though video/audio kept
		// streaming fine. This goroutine's own do_li_start really did just
		// succeed, so the store is always correct and idempotent here.
		liStartConnectionActive.Store(true)
		startRTPStatsLoggerIfEnabled(activeStreamDone)

		<-activeStreamDone

		logrus.Info("🌕 [Moonlight/CGO] termination received — stopping streams")
		// Call LiStopConnection under the mutex so that the next StartStream
		// cannot call LiStartConnection until this stop is fully complete.
		liStreamMu.Lock()
		stopConnectionSafely()
		liStreamMu.Unlock()

		C.set_audio_pipe_fd(-1)

		// Only clear shared state if we are still the current generation;
		// a newer StartStream may have already reset these.
		if liStreamGen.Load() == myGen {
			vtFrameCallbackMu.Lock()
			vtFrameCallback = nil
			vtFrameCallbackMu.Unlock()
			liStartConnectionActive.Store(false)
		}

		if pipeWrite != nil {
			_ = pipeWrite.Close()
		}
		if audioPipeWrite != nil {
			_ = audioPipeWrite.Close()
		}

		if onStop != nil && liStreamGen.Load() == myGen {
			onStop(activeStreamTermErr)
		}
	}()

	return nil
}

func (w *MoonlightCgoWrapper) StopStream() {
	logrus.Info("🌕 [Moonlight/CGO] StopStream: stopping")
	liStreamMu.Lock()
	stopConnectionSafely()
	liStreamMu.Unlock()
	if activeStreamDone != nil {
		closeActiveStreamDone()
	}
}

func (w *MoonlightCgoWrapper) SetAudioMuted(muted bool) {
	w.audioMuted = muted
	if muted {
		C.set_audio_muted(1)
	} else {
		C.set_audio_muted(0)
	}
}
func (w *MoonlightCgoWrapper) GetAudioMuted() bool { return w.audioMuted }

// ── Input methods ─────────────────────────────────────────────────────────────

func (w *MoonlightCgoWrapper) SendMoonlightKey(vkCode int16, action int8, modifiers int8) {
	if !liStartConnectionActive.Load() {
		logrus.Warnf("🌕 [Moonlight/CGO] SendMoonlightKey failed: liStartConnectionActive is false")
		return
	}
	logrus.Debugf("🌕 [Moonlight/CGO] SendMoonlightKey vkCode=0x%04X action=%d modifiers=%d", uint16(vkCode), action, modifiers)
	C.do_send_key(C.short(vkCode), C.char(action), C.char(modifiers))
}

func (w *MoonlightCgoWrapper) SendMoonlightMouseMove(dx, dy int16) {
	if !liStartConnectionActive.Load() {
		return
	}
	C.do_send_mouse_move(C.short(dx), C.short(dy))
}

func (w *MoonlightCgoWrapper) SendMoonlightMousePosition(x, y, refW, refH int16) {
	if !liStartConnectionActive.Load() {
		return
	}
	C.do_send_mouse_position(C.short(x), C.short(y), C.short(refW), C.short(refH))
}

func (w *MoonlightCgoWrapper) SendMoonlightMouseButton(action int8, button int) {
	if !liStartConnectionActive.Load() {
		return
	}
	C.do_send_mouse_button(C.char(action), C.int(button))
}

func (w *MoonlightCgoWrapper) SendMoonlightScroll(clicks int8) {
	if !liStartConnectionActive.Load() {
		return
	}
	C.do_send_scroll(C.schar(clicks))
}

func (w *MoonlightCgoWrapper) SendMoonlightControllerEvent(
	controllerNumber uint16, activeGamepadMask uint16, buttons uint16,
	leftTrigger uint8, rightTrigger uint8,
	leftStickX int16, leftStickY int16,
	rightStickX int16, rightStickY int16,
) {
	if !liStartConnectionActive.Load() {
		return
	}
	C.do_send_multi_controller(
		C.ushort(controllerNumber), C.ushort(activeGamepadMask), C.ushort(buttons),
		C.uchar(leftTrigger), C.uchar(rightTrigger),
		C.short(leftStickX), C.short(leftStickY),
		C.short(rightStickX), C.short(rightStickY),
	)
}

func (w *MoonlightCgoWrapper) IsInputActive() bool {
	return liStartConnectionActive.Load()
}

func (w *MoonlightCgoWrapper) SendMoonlightUtf8Text(text string) {
	if !liStartConnectionActive.Load() || len(text) == 0 {
		return
	}
	cs := C.CString(text)
	defer C.free(unsafe.Pointer(cs))
	C.do_send_utf8_text(cs, C.uint(len(text)))
}

// ── CGO-exported Go callbacks ─────────────────────────────────────────────────

var stageNames = []string{
	"none", "platform-init", "name-resolution", "audio-stream-init",
	"rtsp-handshake", "control-stream-init", "video-stream-init",
	"input-stream-init", "control-stream-start", "video-stream-start",
	"audio-stream-start", "input-stream-start",
}

//export goMoonlightStage
func goMoonlightStage(stage, result, errCode C.int) {
	name := "unknown"
	if int(stage) < len(stageNames) {
		name = stageNames[stage]
	}
	switch int(result) {
	case 0:
		logrus.Infof("🌕 [Moonlight] ► %s …", name)
	case 1:
		logrus.Infof("🌕 [Moonlight] ✅ %s", name)
	default:
		logrus.Errorf("🌕 [Moonlight] ❌ %s failed (err=%d)", name, int(errCode))
	}
}

//export goMoonlightConnected
func goMoonlightConnected() {
	logrus.Info("🌕 [Moonlight] stream connected ✅")
}

//export goMoonlightTerminated
func goMoonlightTerminated(errCode C.int) {
	reason := "unknown"
	switch int(errCode) {
	case 0:
		reason = "clean disconnect"
	case -100:
		reason = "connection reset by server"
	case -101:
		reason = "server closed connection"
	case -102:
		reason = "no IDR frame received"
	case -200:
		reason = "video decode failed"
	case -300:
		reason = "control stream error"
	case -400:
		reason = "input stream error"
	}
	logrus.Errorf("🌕 [Moonlight] ❌ terminated: code=%d (%s)", int(errCode), reason)
	activeStreamTermErr = fmt.Errorf("stream terminated: code=%d (%s)", int(errCode), reason)
	// Clear the negotiated codec so a stale value from this session can't be
	// shown as "currently active" once the stream has actually ended.
	negotiatedVideoFormat.Store(-1)
	closeActiveStreamDone()
}

// videoFormatCodecName maps a VIDEO_FORMAT_* bitmask (Limelight.h) to the
// client's codec name constants ("h264"/"h265"/"av1"). Mirrors the bit
// layout moonlightVideoFormat() in moonlight_service.go encodes, plus the
// mask bits the platform CGO files already use to branch HEVC vs H264
// (VIDEO_FORMAT_MASK_H265 = 0x0F00, VIDEO_FORMAT_MASK_AV1 = 0xF000).
func videoFormatCodecName(format int32) (string, bool) {
	switch {
	case format < 0:
		return "", false
	case format&0x0F00 != 0:
		return models.VideoModeH265, true
	case format&0xF000 != 0:
		return models.VideoModeAV1, true
	case format&0x00FF != 0:
		return models.VideoModeH264, true
	default:
		return "", false
	}
}

//export goVideoFormatNegotiated
func goVideoFormatNegotiated(format C.int) {
	negotiatedVideoFormat.Store(int32(format))
	name, ok := videoFormatCodecName(int32(format))
	if !ok {
		logrus.Warnf("🎬 [Moonlight/HW] negotiated video format: unrecognized 0x%04X", int(format))
		return
	}
	logrus.Infof("🎬 [Moonlight/HW] negotiated video format: %s (0x%04X)", name, int(format))
}

// NegotiatedVideoCodecName returns the codec moonlight-common-c actually
// negotiated with the server for the current session (from dr_setup's
// NegotiatedVideoFormat), and whether a session has reported one yet. This
// is the authoritative answer to "what codec is really streaming" — unlike
// the client's requested mode or the agent's best-effort guess, it reflects
// what the server actually accepted.
func (w *MoonlightCgoWrapper) NegotiatedVideoCodecName() (string, bool) {
	if !liStartConnectionActive.Load() {
		return "", false
	}
	return videoFormatCodecName(negotiatedVideoFormat.Load())
}

//export goVTLog
func goVTLog(msg *C.char) {
	logrus.Infof("🎬 [Moonlight/HW] %s", C.GoString(msg))
}

// goAIVisionOverlay is the cgo entry point for the AI Vision live overlay
// (see ai_vision.go): called from deliver_frame in moonlight_cgo_linux.go
// (before the frame reaches vk_video_try_submit/gl_video_try_submit) and
// from the CPU-fallback decode path in moonlight_cgo_apple.go, on the
// exact RGBA buffer that's about to be displayed. wrapRGBA below is a
// zero-copy view over the C-owned memory -- ApplyAIVisionOverlay draws
// into it in place -- valid only for the duration of this call, which
// matches how long the C side guarantees the buffer stays alive.
//
// Not wired into the CVImageBufferRef fast path metal_video_try_submit
// takes on macOS when it succeeds (see goAIVisionShouldSample/goAIVisionSample
// below for that path instead), nor into the AHardwareBuffer path on
// Android/Windows-Vulkan/iOS: those hand decoded frames to the GPU without
// ever producing a CPU-readable buffer on every frame, so overlaying them
// needs an actual native compositing layer (macOS: metal_video_impl_darwin.m's
// g_overlay_layer; Android's cursor uses the same pattern, see
// VulkanOverlayBridge.kt) rather than pixel writes here.
//
//export goAIVisionOverlay
func goAIVisionOverlay(rgba *C.uint8_t, width, height, stride C.int) {
	if rgba == nil || width <= 0 || height <= 0 || stride <= 0 {
		return
	}
	if !aiVisionEnabled.Load() {
		return
	}
	w, h, s := int(width), int(height), int(stride)
	buf := unsafe.Slice((*byte)(unsafe.Pointer(rgba)), s*h)
	ApplyAIVisionOverlay(buf, w, h, s)
}

// goAIVisionShouldSample is a cheap (atomics + time comparisons, no pixel
// access) pre-check called every frame from vt_callback's Metal fast-path
// branch in moonlight_cgo_apple.go: it lets the C side skip the BGRA→RGBA
// CVPixelBuffer readback entirely on the (overwhelming) majority of frames
// where neither the icon nor the OCR loop is due yet (see ai_vision.go's
// package doc comment for why there are two independent loops/cadences),
// so the zero-copy path stays zero-copy except for the rare frame that
// actually needs to feed one of them. Mirrors (but does not replace) the
// authoritative gating inside maybeKickIconDetection/maybeKickOCR's own
// CompareAndSwap -- a false positive here just means one wasted
// conversion, never a correctness issue.
//
//export goAIVisionShouldSample
func goAIVisionShouldSample() C.int {
	// A pending live-frame request (see internal/api/live_frame.go) needs
	// this frame regardless of the checkbox/pacing below -- it's a
	// one-shot, independent consumer of the same sample.
	if usbapi.LiveFrameWanted() {
		return 1
	}
	if !aiVisionEnabled.Load() {
		return 0
	}
	now := time.Now().UnixNano()
	iconDue := !aiVisionIconBusy.Load() && now-aiVisionIconLastRun.Load() >= int64(aiVisionIconInterval)
	ocrDue := !aiVisionOCRBusy.Load() && now-aiVisionOCRLastRun.Load() >= int64(aiVisionOCRInterval)
	if iconDue || ocrDue {
		return 1
	}
	return 0
}

// goAIVisionSample is the macOS Metal fast-path counterpart to
// goAIVisionOverlay: called only on the rare frame goAIVisionShouldSample
// green-lit, with a CPU readback of that one frame converted to RGBA. It
// only feeds the detector (maybeKickIconDetection/maybeKickOCR) -- it must
// NOT draw into buf, unlike goAIVisionOverlay's ApplyAIVisionOverlay,
// because this buffer is a throwaway conversion scratch space, never the
// one actually displayed (Metal renders the CVImageBufferRef's IOSurface
// directly). The completed result reaches the screen via
// pushAIVisionOverlayToMetal's separate compositor-layer path instead (see
// aiVisionMetalPush).
//
//export goAIVisionSample
func goAIVisionSample(rgba *C.uint8_t, width, height, stride C.int) {
	if rgba == nil || width <= 0 || height <= 0 || stride <= 0 {
		return
	}
	w, h, s := int(width), int(height), int(stride)
	buf := unsafe.Slice((*byte)(unsafe.Pointer(rgba)), s*h)
	maybeServeLiveFrame(buf, w, h, s)
	if !aiVisionEnabled.Load() {
		return
	}
	maybeKickIconDetection(buf, w, h, s)
	maybeKickOCR(buf, w, h, s)
}

var vtFrameCount int64

//export goVTFrame
func goVTFrame(rgba *C.uint8_t, width, height, stride C.int) {
	vtFrameCallbackMu.Lock()
	cb := vtFrameCallback
	vtFrameCallbackMu.Unlock()
	if cb == nil {
		return
	}

	cnt := atomic.AddInt64(&vtFrameCount, 1)
	if cnt == 1 {
		logrus.Infof("🎬 [Moonlight/HW] ✅ first RGBA frame — %dx%d", int(width), int(height))
	}

	// rgba is NULL on any call that exists purely to keep Go-side frame
	// counting/FPS/lastFrameTime stats alive while a native overlay (Metal on
	// macOS/iOS, Vulkan's AHardwareBuffer path on Android) already rendered
	// this frame at the C level without ever producing a CPU-readable buffer
	// -- there is no pixel data here to decode, checked first and
	// unconditionally so it can never fall through to the array-pointer
	// conversion below regardless of the frame-count logic (confirmed live:
	// macOS's Metal path always passes NULL, including on frames 1-10 and
	// every 120th, and it used to crash here with a nil-pointer SIGSEGV
	// before this check existed, because that logic assumed a non-nil
	// pointer always accompanied those specific counts).
	if rgba == nil {
		cb(nil)
		return
	}

	// When the native GPU overlay (Metal/GL) is active it already received this
	// frame at the C level via metal_video_try_submit / gl_video_try_submit.
	// Skip the 3.5 MB Go image allocation most of the time — only the Go-level
	// frame count is needed for stats. However, pass a real frame on the first
	// 10 frames and every 120th frame so that handleVideoFrame can run
	// updateFrameContentRect → detectDarkInset to detect letterbox/pillarbox
	// bars embedded in the video stream (e.g. Sunshine pillarboxing 4:3 content
	// into a 16:9 stream). Without this, frameContentX/Y stays 0 and
	// PositionToAbsolute never adjusts for in-stream black bars. Only reachable
	// with a real (non-nil) buffer, e.g. Android's non-hwbuffer GL readback
	// path, which passes real pixels even while its own Vulkan overlay is active.
	if NativeVideoOverlayIsActive() {
		if cnt > 10 && cnt%120 != 0 {
			// Deliver a nil frame to let handleVideoFrame update its own counter.
			cb(nil)
			return
		}
		// Fall through to create a real image for black-bar detection.
	}

	w, h, s := int(width), int(height), int(stride)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rowBytes := w * 4
	if s == rowBytes {
		copy(img.Pix, (*[1 << 30]byte)(unsafe.Pointer(rgba))[:w*h*4:w*h*4])
	} else {
		src := (*[1 << 30]byte)(unsafe.Pointer(rgba))[: h*s : h*s]
		for y := 0; y < h; y++ {
			copy(img.Pix[y*rowBytes:], src[y*s:y*s+rowBytes])
		}
	}
	cb(img)
}
