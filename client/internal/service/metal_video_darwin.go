//go:build darwin && !ios && cgo

package service

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit -framework CoreVideo -framework QuartzCore -framework CoreFoundation

#include <stdint.h>
#include <CoreVideo/CoreVideo.h>

// Implemented in metal_video_impl_darwin.m (compiled as a separate translation unit).
extern int  metal_video_is_active(void);
extern int  metal_video_try_submit(CVImageBufferRef img);
extern int  metal_video_create(uintptr_t nsWinPtr, float x, float y, float w, float h);
extern void metal_video_update_frame(float x, float y, float w, float h);
extern void metal_video_destroy(void);
extern double metal_video_last_fps(void);
extern void metal_video_set_hidden(int hidden);
extern int  metal_video_get_last_frame_rgba(int *outW, int *outH, uint8_t **out);
extern int  metal_video_next_event(int *type_out, float *x_out, float *y_out, int *btn_out);
extern void metal_video_set_overlay(const uint8_t *rgba, int w, int h, int stride);
extern void metal_video_clear_overlay(void);

// Forward declaration matching the CGO-generated export signature (char*, not const char*).
extern void goMetalLog(char *msg, int level);
*/
import "C"

import (
	"image"
	"unsafe"

	"github.com/sirupsen/logrus"

	"usbridge-client/internal/localui"
)

// init wires the AI Vision overlay (ai_vision.go) to this platform's
// zero-copy Metal compositor -- see pushAIVisionOverlayToMetal's doc
// comment for why the CPU pixel-drawing approach ai_vision.go otherwise
// uses (drawCachedOverlay) doesn't reach frames that took the Metal fast
// path. nil on every platform without a metal_video_darwin.go (Linux,
// Windows, Android, iOS), where ai_vision.go's own in-place drawing is the
// only mechanism and these hooks are simply never set.
func init() {
	aiVisionMetalPush = pushAIVisionOverlayToMetal
	aiVisionMetalClear = MetalVideoClearOverlay
}

// pushAIVisionOverlayToMetal renders a just-completed AI Vision detection
// result onto a transparent w×h canvas and hands it to the native
// compositor layer (metal_video_impl_darwin.m's g_overlay_layer), which
// Core Animation then composites on top of the video IOSurface layer at
// zero per-frame CPU cost. Called once per completed detection pass (every
// aiVisionInterval, see ai_vision.go), not per frame -- unlike
// drawCachedOverlay's in-place pixel writes, which only run on paths that
// already produce a CPU-writable buffer (Linux, the CPU decode fallback
// here) and never on this one.
func pushAIVisionOverlayToMetal(result *localui.Result, w, h int) {
	if !MetalVideoIsActive() {
		return // Metal fast path isn't the active renderer right now (e.g. CPU fallback) -- nothing to do.
	}
	img := buildAIVisionOverlayImage(result, w, h)
	MetalVideoSetOverlay(img.Pix, w, h, img.Stride)
}

// buildAIVisionOverlayImage draws result's boxes+tags onto a fully
// transparent w×h RGBA canvas using the exact same drawing code as the
// static ui.parse annotated screenshot and the CPU-buffer live overlay
// (localui.DrawDetectionBox/Tag) -- every color those use is fully opaque
// (alpha 255, see draw.go), so untouched pixels stay alpha 0 and this is
// trivially already in the premultiplied form CGImage needs, no separate
// conversion required.
func buildAIVisionOverlayImage(result *localui.Result, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for _, icon := range result.Icons {
		localui.DrawDetectionBox(img, icon.Bbox, false)
		localui.DrawDetectionTag(img, icon.ID, icon.Bbox)
	}
	for _, t := range result.Text {
		localui.DrawDetectionBox(img, t.Bbox, true)
		if t.ID != "" {
			// Empty ID means this box was published via maybeKickOCR's
			// onTextBoxes before svtr recognized it (see
			// ParseFastNearIconsStaged) -- outline only, no tag yet.
			localui.DrawDetectionTag(img, t.ID, t.Bbox)
		}
	}
	return img
}

// goMetalLog is called from C (metal_video_impl_darwin.m) to log via logrus.
//
//export goMetalLog
func goMetalLog(msg *C.char, level C.int) {
	text := C.GoString(msg)
	switch int(level) {
	case 1:
		logrus.Warnf("[Metal] %s", text)
	case 2:
		logrus.Errorf("[Metal] %s", text)
	default:
		logrus.Infof("[Metal] %s", text)
	}
}

// MetalVideoCreate creates (or replaces) the Metal overlay on the given NSWindow.
// Pass w=0, h=0 to cover the entire contentView (fullscreen mode).
func MetalVideoCreate(nsWin uintptr, x, y, w, h float32) bool {
	return C.metal_video_create(C.uintptr_t(nsWin), C.float(x), C.float(y), C.float(w), C.float(h)) != 0
}

// MetalVideoUpdateFrame repositions the Metal overlay to track the Fyne video widget.
func MetalVideoUpdateFrame(x, y, w, h float32) {
	C.metal_video_update_frame(C.float(x), C.float(y), C.float(w), C.float(h))
}

// MetalVideoDestroy removes the overlay and disables the Metal decode path.
func MetalVideoDestroy() {
	C.metal_video_destroy()
}

// MetalVideoIsActive reports whether the Metal overlay is live.
func MetalVideoIsActive() bool {
	return C.metal_video_is_active() != 0
}

// MetalVideoLastFPS returns the current Metal render FPS from the last ~2s window.
// Returns 0 if not enough frames yet.
func MetalVideoLastFPS() float64 {
	return float64(C.metal_video_last_fps())
}

// MetalVideoGetLastFrameRGBA returns the last rendered VT frame as an image.RGBA.
// Returns nil if no frame has been rendered yet. Called once on stream stop — cost is acceptable.
func MetalVideoGetLastFrameRGBA() *image.RGBA {
	var w, h C.int
	var ptr *C.uint8_t
	if C.metal_video_get_last_frame_rgba(&w, &h, &ptr) == 0 || ptr == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(ptr))
	width, height := int(w), int(h)
	size := width * height * 4
	src := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), size)
	dst := make([]byte, size)
	copy(dst, src)
	return &image.RGBA{
		Pix:    dst,
		Stride: width * 4,
		Rect:   image.Rect(0, 0, width, height),
	}
}

// MetalVideoSetOverlay uploads an AI Vision detection overlay (a mostly-
// transparent RGBA image the same pixel size as the video frame) onto the
// native compositor's overlay CALayer, stacked above the video IOSurface
// layer -- see pushAIVisionOverlayToMetal. rgba must be non-empty and w/h/
// stride must describe it; a no-op call is silently ignored so a stray
// empty detection result can't crash into the cgo boundary.
func MetalVideoSetOverlay(rgba []byte, w, h, stride int) {
	if len(rgba) == 0 || w <= 0 || h <= 0 || stride <= 0 {
		return
	}
	C.metal_video_set_overlay((*C.uint8_t)(unsafe.Pointer(&rgba[0])), C.int(w), C.int(h), C.int(stride))
}

// MetalVideoClearOverlay removes the AI Vision overlay image (checkbox
// turned off, or a fresh detection found nothing) without touching the
// video layer underneath.
func MetalVideoClearOverlay() {
	C.metal_video_clear_overlay()
}

// MetalVideoSetHidden hides or shows the Metal overlay NSView without destroying it.
// Use this to let Fyne popups/menus render on top of the video.
func MetalVideoSetHidden(hidden bool) {
	h := C.int(0)
	if hidden {
		h = 1
	}
	C.metal_video_set_hidden(h)
}

// MetalVideoNextEvent drains one pending pointer event from the Metal overlay view.
// Returns (type, button, x, y, ok). Types: 1=motion 2=button-press 3=button-release.
// Buttons: 1=left 2=middle 3=right 4=wheel-up 5=wheel-down.
// Coordinates are in NSView points with top-left origin (matches Fyne dp directly).
// Safe to call from any goroutine.
func MetalVideoNextEvent() (typ, button int, x, y float32, ok bool) {
	var t, btn C.int
	var ex, ey C.float
	r := C.metal_video_next_event(&t, &ex, &ey, &btn)
	return int(t), int(btn), float32(ex), float32(ey), r != 0
}
