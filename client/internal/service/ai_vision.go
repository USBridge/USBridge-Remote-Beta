package service

import (
	"bytes"
	"image"
	"image/png"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	usbapi "usbridge-client/internal/api"
	"usbridge-client/internal/localui"
)

// AI Vision is an optional live overlay, off by default: when turned on
// from the video settings popup (next to resolution/bitrate), it burns
// the same Set-of-Mark detection an agent's ui.parse MCP call would get --
// red/green boxes around UI elements and text, each tagged with its hex
// id -- directly into the live video frame, right before that frame
// reaches the native Vulkan/Metal/GL renderer. The operator sees exactly
// what an agent sees and how it would address each element, overlaid on
// the moving picture instead of a frozen screenshot.
//
// It reuses internal/localui's client-side ONNX pipeline (the same one
// backing the local ui.parse MCP offload, see internal/api/local_ui_init.go)
// rather than shipping a second copy of the models. That pipeline runs in
// the neighborhood of a second or more per frame -- nowhere close to video
// frame rate -- so detection does NOT run every frame: aiVisionInterval
// paces how often a fresh pass is kicked off, and every frame in between
// keeps drawing the most recently completed result. Disabled, the whole
// feature costs one atomic load per frame (see ApplyAIVisionOverlay).
const aiVisionInterval = 2 * time.Second

var (
	aiVisionEnabled atomic.Bool
	aiVisionBusy    atomic.Bool
	aiVisionLastRun atomic.Int64 // UnixNano of the last detection *kickoff*

	// aiVisionLastDurationMs is the wall time the most recently *completed*
	// Parse call took, in milliseconds -- 0 until the first one finishes.
	// Feeds the detection-fps badge under the video icon (see
	// DetectionFPS/main_window_layout.go's updateVideoIconLabel) with a
	// real measured rate instead of the nominal 1/aiVisionInterval, since
	// on a slow machine Parse can (and, per this feature's own doc comment
	// above, routinely does) take longer than the pacing interval.
	aiVisionLastDurationMs atomic.Int64

	aiVisionMu     sync.RWMutex
	aiVisionResult *localui.Result
)

// aiVisionMetalPush and aiVisionMetalClear are wired up by
// metal_video_darwin.go's init() on macOS (Metal's zero-copy decode path
// never produces a CPU-writable frame buffer for drawCachedOverlay to draw
// into -- see that file's pushAIVisionOverlayToMetal doc comment for the
// native-compositor-layer alternative it uses instead). Left nil on every
// other platform, where drawCachedOverlay's in-place pixel drawing is the
// only mechanism and there is nothing else to notify.
var (
	aiVisionMetalPush  func(result *localui.Result, w, h int)
	aiVisionMetalClear func()
)

// SetAIVisionEnabled turns the live detection overlay on or off. Wired to
// the "AI Vision" checkbox in the video settings popup (see
// gui/view/video_start_dialog.go) -- takes effect immediately, independent
// of the Start/Apply button, since it only affects local rendering and
// touches nothing on the device. Disabling drops the cached result right
// away so a stale overlay never lingers after the checkbox is unticked.
func SetAIVisionEnabled(enabled bool) {
	wasEnabled := aiVisionEnabled.Swap(enabled)
	if !enabled {
		aiVisionMu.Lock()
		aiVisionResult = nil
		aiVisionMu.Unlock()
		if clear := aiVisionMetalClear; clear != nil {
			clear()
		}
	}
	if wasEnabled != enabled {
		logrus.Infof("🔎 [AI Vision] %s", map[bool]string{true: "enabled", false: "disabled"}[enabled])
	}
}

// AIVisionEnabled reports the live overlay checkbox's current state.
func AIVisionEnabled() bool {
	return aiVisionEnabled.Load()
}

// DetectionFPS returns the measured rate of the icon_detect stage only
// (1000/aiVisionLastDurationMs), or 0 if the overlay is off or no pass has
// completed yet -- NOT the full Parse/OCR pipeline. Since maybeKickDetection
// now uses ParseStaged (see its doc comment), aiVisionLastDurationMs is set
// from ParseStaged's onIcons callback, i.e. right when icon boxes actually
// hit the screen; the slower dbnet+svtr OCR stage that follows updates text
// labels separately later and deliberately isn't counted here, since it's
// not what determines how fresh the visible grid overlay is. This is the
// *actual* throughput, not the nominal 1/aiVisionInterval pacing target.
// Polled at 1Hz by main_window_layout.go's FPS-changed callback to drive
// the small detection-fps badge under the video icon.
func DetectionFPS() float64 {
	if !aiVisionEnabled.Load() {
		return 0
	}
	ms := aiVisionLastDurationMs.Load()
	if ms <= 0 {
		return 0
	}
	return 1000 / float64(ms)
}

// ApplyAIVisionOverlay is called once per decoded frame from the native
// decode path (see moonlight_cgo_linux.go / moonlight_cgo_apple.go's
// deliver_frame, via the goAIVisionOverlay cgo export in
// moonlight_cgo_wrapper.go) on the tightly-packed RGBA buffer about to be
// handed to the GPU. It sits on the hot path, so the disabled case -- the
// default -- must stay a single atomic load; everything else here (kicking
// off a detection pass, drawing boxes) only runs while the checkbox is on.
//
// maybeServeLiveFrame is checked first and independently of the checkbox:
// it answers a pending local ui.parse call (see
// internal/api/live_frame.go) that wants whatever frame is currently
// decoding, so that call can skip fetching its own screenshot from the
// device. Same one-atomic-load cost as the checkbox path when nobody's
// asking.
func ApplyAIVisionOverlay(rgba []byte, w, h, stride int) {
	maybeServeLiveFrame(rgba, w, h, stride)
	if !aiVisionEnabled.Load() {
		return
	}
	maybeKickDetection(rgba, w, h, stride)
	drawCachedOverlay(rgba, w, h, stride)
}

// maybeServeLiveFrame answers a pending usbapi.RequestLiveFrame call (used
// by local ui.parse to avoid a redundant device screenshot round-trip when
// a video session is already streaming) with this decoded frame, PNG-
// encoded. Checked on every frame from both the plain overlay hot path and
// the macOS Metal fast path's sample callback (goAIVisionSample below);
// usbapi.LiveFrameWanted/SubmitLiveFrame's own CompareAndSwap makes it safe
// to check unconditionally without a busy-guard of its own -- at most one
// frame gets captured+encoded per outstanding request either way.
func maybeServeLiveFrame(rgba []byte, w, h, stride int) {
	if !usbapi.LiveFrameWanted() {
		return
	}
	frame := snapshotRGBA(rgba, w, h, stride)
	var buf bytes.Buffer
	if err := png.Encode(&buf, frame); err != nil {
		logrus.Warnf("🔎 [AI Vision] live-frame encode failed: %v", err)
		return
	}
	usbapi.SubmitLiveFrame(buf.Bytes())
}

// maybeKickDetection copies the current frame and hands it to the local
// ui.parse detector on a background goroutine, at most once per
// aiVisionInterval and never while a previous pass is still running (a
// slow CPU can take longer than the interval -- in that case we simply
// keep showing the last completed result instead of piling up goroutines
// or falling behind on GPU-owned memory).
func maybeKickDetection(rgba []byte, w, h, stride int) {
	now := time.Now().UnixNano()
	if now-aiVisionLastRun.Load() < int64(aiVisionInterval) {
		return
	}
	if !aiVisionBusy.CompareAndSwap(false, true) {
		return
	}
	aiVisionLastRun.Store(now)

	parser := usbapi.GetLocalUIParser()
	if parser == nil {
		aiVisionBusy.Store(false)
		logrus.Warn("🔎 [AI Vision] enabled but the local ui.parse models aren't loaded (Settings ▸ Local ui.parse offload) -- nothing to overlay yet")
		return
	}

	// frame takes its own copy of the pixels: rgba is only valid for the
	// duration of this call (the C caller frees/reuses it right after).
	frame := snapshotRGBA(rgba, w, h, stride)

	go func() {
		defer aiVisionBusy.Store(false)
		var buf bytes.Buffer
		if err := png.Encode(&buf, frame); err != nil {
			logrus.Warnf("🔎 [AI Vision] frame encode failed: %v", err)
			return
		}
		b := frame.Bounds()
		tParse := time.Now()
		result, err := parser.ParseStaged(buf.Bytes(), func(icons []localui.Icon) {
			// Fires as soon as icon_detect is done -- typically a few
			// hundred ms with CoreML -- well before the slower dbnet+svtr
			// OCR stage below even starts. Publishing here is what makes
			// the grid overlay (and the detection-fps badge) track
			// icon_detect's own rate instead of getting held hostage by
			// OCR. See ParseStaged's doc comment for what this does and
			// does NOT fix (the next pass still waits for this one's OCR
			// to finish -- Parser serializes calls behind one mutex).
			aiVisionLastDurationMs.Store(time.Since(tParse).Milliseconds())
			publishAIVisionResult(&localui.Result{Icons: icons, Text: cachedAIVisionText()}, b.Dx(), b.Dy())
		})
		if err != nil {
			logrus.Warnf("🔎 [AI Vision] detection pass failed: %v", err)
			return
		}
		publishAIVisionResult(result, b.Dx(), b.Dy())
	}()
}

// cachedAIVisionText returns whatever text the last *completed* detection
// pass found, so the icon-only publish above doesn't blank out the text
// overlay while this pass's own OCR is still running -- the old text stays
// on screen (possibly for a stale frame's positions) until this pass's OCR
// replaces it a few seconds later.
func cachedAIVisionText() []localui.TextRegion {
	aiVisionMu.RLock()
	defer aiVisionMu.RUnlock()
	if aiVisionResult == nil {
		return nil
	}
	return aiVisionResult.Text
}

// publishAIVisionResult installs result as the cached overlay result and,
// on macOS's Metal fast path, also pushes it to the native compositor
// overlay layer (that path never runs drawCachedOverlay -- see
// aiVisionMetalPush's doc comment above; push is a no-op nil check when
// Metal isn't the active renderer). Called twice per detection pass now:
// once from ParseStaged's onIcons callback (icons only, cached text), once
// with the final, complete result once OCR finishes.
func publishAIVisionResult(result *localui.Result, w, h int) {
	aiVisionMu.Lock()
	aiVisionResult = result
	aiVisionMu.Unlock()
	if push := aiVisionMetalPush; push != nil {
		push(result, w, h)
	}
}

// snapshotRGBA copies a possibly stride-padded RGBA buffer into a
// standalone *image.RGBA that the encoder and background goroutine can
// hold onto safely past this call.
func snapshotRGBA(rgba []byte, w, h, stride int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rowBytes := w * 4
	for y := 0; y < h; y++ {
		srcOff := y * stride
		dstOff := y * img.Stride
		if srcOff+rowBytes > len(rgba) {
			break
		}
		copy(img.Pix[dstOff:dstOff+rowBytes], rgba[srcOff:srcOff+rowBytes])
	}
	return img
}

// drawCachedOverlay burns the most recently completed detection's boxes
// and Set-of-Mark hex tags directly into the live RGBA buffer, in place,
// by wrapping it as an *image.RGBA with zero copy (image.RGBA is just a
// {Pix []byte, Stride int, Rect} view) and reusing localui's own drawing
// code, so the live overlay renders pixel-identical to a ui.parse
// annotated screenshot.
func drawCachedOverlay(rgba []byte, w, h, stride int) {
	aiVisionMu.RLock()
	result := aiVisionResult
	aiVisionMu.RUnlock()
	if result == nil {
		return
	}

	img := &image.RGBA{Pix: rgba, Stride: stride, Rect: image.Rect(0, 0, w, h)}
	for _, icon := range result.Icons {
		localui.DrawDetectionBox(img, icon.Bbox, false)
		localui.DrawDetectionTag(img, icon.ID, icon.Bbox)
	}
	for _, t := range result.Text {
		localui.DrawDetectionBox(img, t.Bbox, true)
		localui.DrawDetectionTag(img, t.ID, t.Bbox)
	}
}
