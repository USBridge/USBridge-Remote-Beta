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
// rather than shipping a second copy of the models. The dbnet+svtr OCR
// stage of that pipeline runs in the neighborhood of a second or more per
// frame -- nowhere close to video frame rate -- but icon_detect alone is
// cheap (CoreML: typically 100-300ms, see localui.NewParser's doc comment).
// Detection therefore runs as two INDEPENDENT loops with their own cadence,
// busy-flag, and Parser lock (see localui.Parser's doc comment on
// iconMu/textMu): maybeKickIconDetection paces icon-only passes fast
// (aiVisionIconInterval) and never waits on OCR; maybeKickOCR paces the
// much slower full icons+text pass separately (aiVisionOCRInterval).
// Before this split, one shared busy-flag meant ANY new pass -- icon
// positions included -- queued up behind however long the previous pass's
// OCR took, which is what made the visible grid noticeably lag behind the
// actual screen even though icon_detect itself was fast. Disabled, the
// whole feature costs one atomic load per frame (see ApplyAIVisionOverlay).
const (
	aiVisionIconInterval = 500 * time.Millisecond
	aiVisionOCRInterval  = 2 * time.Second
)

var (
	aiVisionEnabled atomic.Bool

	aiVisionIconBusy    atomic.Bool
	aiVisionIconLastRun atomic.Int64 // UnixNano of the last icon-only kickoff

	aiVisionOCRBusy    atomic.Bool
	aiVisionOCRLastRun atomic.Int64 // UnixNano of the last OCR-pass kickoff

	// aiVisionLastDurationMs is the wall time the most recently *completed*
	// icon-only pass took, in milliseconds -- 0 until the first one
	// finishes. Feeds the detection-fps badge under the video icon (see
	// DetectionFPS/main_window_layout.go's updateVideoIconLabel) with the
	// real measured rate of what actually determines grid freshness now:
	// the icon loop, not OCR.
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

// DetectionFPS returns the measured rate of the icon-only loop
// (1000/aiVisionLastDurationMs, see maybeKickIconDetection), or 0 if the
// overlay is off or no pass has completed yet -- NOT the OCR pipeline,
// which runs independently and slower (see the package doc comment). This
// is what actually determines how fresh the visible grid overlay is, and
// the *actual* measured throughput, not the nominal 1/aiVisionIconInterval
// pacing target. Polled at 1Hz by main_window_layout.go's FPS-changed
// callback to drive the small detection-fps badge under the video icon.
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
	maybeKickIconDetection(rgba, w, h, stride)
	maybeKickOCR(rgba, w, h, stride)
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

// maybeKickIconDetection copies the current frame and runs icon_detect
// ONLY (localui.Parser.ParseIconsOnly) on a background goroutine, at most
// once per aiVisionIconInterval and never while a previous icon-only pass
// is still running. Independent of maybeKickOCR below -- see the package
// doc comment for why they're split -- so this proceeds on its own fast
// cadence even while an OCR pass from an older frame is still grinding
// away in its own goroutine.
func maybeKickIconDetection(rgba []byte, w, h, stride int) {
	now := time.Now().UnixNano()
	if now-aiVisionIconLastRun.Load() < int64(aiVisionIconInterval) {
		return
	}
	if !aiVisionIconBusy.CompareAndSwap(false, true) {
		return
	}
	aiVisionIconLastRun.Store(now)

	parser := usbapi.GetLocalUIParser()
	if parser == nil {
		aiVisionIconBusy.Store(false)
		return // maybeKickOCR already warns once about missing models
	}

	// frame takes its own copy of the pixels: rgba is only valid for the
	// duration of this call (the C caller frees/reuses it right after).
	frame := snapshotRGBA(rgba, w, h, stride)

	go func() {
		defer aiVisionIconBusy.Store(false)
		var buf bytes.Buffer
		if err := png.Encode(&buf, frame); err != nil {
			logrus.Warnf("🔎 [AI Vision] icon frame encode failed: %v", err)
			return
		}
		tIcon := time.Now()
		icons, err := parser.ParseIconsOnly(buf.Bytes())
		if err != nil {
			logrus.Warnf("🔎 [AI Vision] icon detection failed: %v", err)
			return
		}
		aiVisionLastDurationMs.Store(time.Since(tIcon).Milliseconds())
		b := frame.Bounds()
		publishAIVisionIcons(icons, b.Dx(), b.Dy())
	}()
}

// maybeKickOCR copies the current frame and runs the full icons+text pass
// (localui.Parser.ParseFast) on a background goroutine, at most once per
// aiVisionOCRInterval and never while a previous OCR pass is still
// running. Independent of maybeKickIconDetection above -- see the package
// doc comment.
func maybeKickOCR(rgba []byte, w, h, stride int) {
	now := time.Now().UnixNano()
	if now-aiVisionOCRLastRun.Load() < int64(aiVisionOCRInterval) {
		return
	}
	if !aiVisionOCRBusy.CompareAndSwap(false, true) {
		return
	}
	aiVisionOCRLastRun.Store(now)

	parser := usbapi.GetLocalUIParser()
	if parser == nil {
		aiVisionOCRBusy.Store(false)
		logrus.Warn("🔎 [AI Vision] enabled but the local ui.parse models aren't loaded (Settings ▸ Local ui.parse offload) -- nothing to overlay yet")
		return
	}

	frame := snapshotRGBA(rgba, w, h, stride)

	go func() {
		defer aiVisionOCRBusy.Store(false)
		var buf bytes.Buffer
		if err := png.Encode(&buf, frame); err != nil {
			logrus.Warnf("🔎 [AI Vision] OCR frame encode failed: %v", err)
			return
		}
		result, err := parser.ParseFast(buf.Bytes())
		if err != nil {
			logrus.Warnf("🔎 [AI Vision] detection pass failed: %v", err)
			return
		}
		b := frame.Bounds()
		// Publish only the text: this pass's own icons came from whatever
		// frame it started on, which by the time OCR finishes (multiple
		// seconds later, see the package doc comment) is stale next to
		// whatever the faster icon loop has already published since --
		// overwriting fresher icon positions with this pass's would
		// visibly snap boxes backward. result.Icons[i].Label
		// (associateLabels ran against this pass's own icon snapshot) is
		// discarded along with them -- an acceptable tradeoff; labels
		// reappear next time the two loops' results happen to line up.
		publishAIVisionText(result.Text, b.Dx(), b.Dy())
	}()
}

// publishAIVisionIcons merges fresh icons into the cached overlay result,
// keeping whatever text is already cached (from the independent, slower
// OCR loop) so the icon-only publish doesn't blank out text that's still
// valid. See publishAIVisionText for the OCR loop's side of this.
func publishAIVisionIcons(icons []localui.Icon, w, h int) {
	aiVisionMu.Lock()
	var text []localui.TextRegion
	if aiVisionResult != nil {
		text = aiVisionResult.Text
	}
	merged := &localui.Result{Icons: icons, Text: text}
	aiVisionResult = merged
	aiVisionMu.Unlock()
	if push := aiVisionMetalPush; push != nil {
		push(merged, w, h)
	}
}

// publishAIVisionText merges fresh text into the cached overlay result,
// keeping whatever icons are already cached (see maybeKickOCR's doc
// comment for why this pass's own icons are deliberately NOT used here).
func publishAIVisionText(text []localui.TextRegion, w, h int) {
	aiVisionMu.Lock()
	var icons []localui.Icon
	if aiVisionResult != nil {
		icons = aiVisionResult.Icons
	}
	merged := &localui.Result{Icons: icons, Text: text}
	aiVisionResult = merged
	aiVisionMu.Unlock()
	if push := aiVisionMetalPush; push != nil {
		push(merged, w, h)
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
