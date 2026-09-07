package service

import (
	"bytes"
	"image"
	"sync/atomic"
	"testing"

	"usbridge-client/internal/localui"
)

// resetAIVisionState clears every AI Vision package-level var before (and
// restores it after) a test -- ai_vision.go's state is process-wide
// (atomics + a couple of package vars), so tests must not leak it into
// each other or into a real running client.
func resetAIVisionState(t *testing.T) {
	t.Helper()
	savedPush, savedClear := aiVisionMetalPush, aiVisionMetalClear
	clear := func() {
		aiVisionEnabled.Store(false)
		aiVisionIconBusy.Store(false)
		aiVisionIconLastRun.Store(0)
		aiVisionOCRBusy.Store(false)
		aiVisionOCRLastRun.Store(0)
		aiVisionMu.Lock()
		aiVisionResult = nil
		aiVisionMu.Unlock()
	}
	clear()
	t.Cleanup(func() {
		clear()
		aiVisionMetalPush = savedPush
		aiVisionMetalClear = savedClear
	})
}

func TestSetAIVisionEnabledTogglesState(t *testing.T) {
	resetAIVisionState(t)

	if AIVisionEnabled() {
		t.Fatal("AIVisionEnabled() should start false")
	}
	SetAIVisionEnabled(true)
	if !AIVisionEnabled() {
		t.Fatal("AIVisionEnabled() = false after SetAIVisionEnabled(true)")
	}
	SetAIVisionEnabled(false)
	if AIVisionEnabled() {
		t.Fatal("AIVisionEnabled() = true after SetAIVisionEnabled(false)")
	}
}

// TestSetAIVisionEnabledFalseDropsCacheAndClearsMetal pins the two side
// effects turning the checkbox off must have, regardless of platform: the
// cached CPU-path result is dropped (so drawCachedOverlay can't paint a
// stale box after the feature is off) and, on macOS, the native Metal
// compositor overlay is cleared via the aiVisionMetalClear hook (see
// metal_video_darwin.go's init()) so a detection box doesn't linger on
// screen after the checkbox is unticked.
func TestSetAIVisionEnabledFalseDropsCacheAndClearsMetal(t *testing.T) {
	resetAIVisionState(t)

	aiVisionMu.Lock()
	aiVisionResult = &localui.Result{Icons: []localui.Icon{{ID: "1"}}}
	aiVisionMu.Unlock()

	var cleared int32
	aiVisionMetalClear = func() { atomic.AddInt32(&cleared, 1) }

	SetAIVisionEnabled(true)
	SetAIVisionEnabled(false)

	aiVisionMu.RLock()
	res := aiVisionResult
	aiVisionMu.RUnlock()
	if res != nil {
		t.Error("disabling AI Vision must drop the cached detection result")
	}
	if atomic.LoadInt32(&cleared) != 1 {
		t.Errorf("disabling AI Vision must call aiVisionMetalClear once, got %d calls", cleared)
	}
}

// TestApplyAIVisionOverlayDisabledIsNoop pins the hot-path contract
// ApplyAIVisionOverlay's doc comment promises: disabled must cost exactly
// one atomic load and touch nothing else -- no pixel writes, no detection
// goroutine kicked off.
func TestApplyAIVisionOverlayDisabledIsNoop(t *testing.T) {
	resetAIVisionState(t)

	const w, h = 4, 4
	rgba := make([]byte, w*h*4)
	for i := range rgba {
		rgba[i] = 0xAB
	}
	before := append([]byte(nil), rgba...)

	ApplyAIVisionOverlay(rgba, w, h, w*4)

	if !bytes.Equal(rgba, before) {
		t.Error("ApplyAIVisionOverlay must not touch the frame buffer while disabled")
	}
	if aiVisionIconBusy.Load() || aiVisionOCRBusy.Load() {
		t.Error("ApplyAIVisionOverlay must not kick off detection while disabled")
	}
}

// TestApplyAIVisionOverlayNoParserDoesNotWedgeBusy exercises the "enabled
// but no local ui.parse backend loaded" path (GetLocalUIParser() returns
// nil in a fresh test process, same as a client that never enabled Local
// ui.parse offload): both maybeKickIconDetection and maybeKickOCR must
// release their own busy flag instead of leaving it permanently stuck
// true, or every future frame would silently skip kicking off detection
// forever once a parser is loaded.
func TestApplyAIVisionOverlayNoParserDoesNotWedgeBusy(t *testing.T) {
	resetAIVisionState(t)
	SetAIVisionEnabled(true)

	const w, h = 4, 4
	rgba := make([]byte, w*h*4)
	ApplyAIVisionOverlay(rgba, w, h, w*4)

	if aiVisionIconBusy.Load() {
		t.Error("maybeKickIconDetection must release aiVisionIconBusy when no parser is loaded, not wedge it true")
	}
	if aiVisionOCRBusy.Load() {
		t.Error("maybeKickOCR must release aiVisionOCRBusy when no parser is loaded, not wedge it true")
	}
}

// TestMaybeKickIconDetectionRespectsInterval confirms the throttle: a
// second call inside aiVisionIconInterval of the first must not record
// another kickoff, even though the first kickoff bailed out immediately
// (no parser loaded, in this test process).
func TestMaybeKickIconDetectionRespectsInterval(t *testing.T) {
	resetAIVisionState(t)
	SetAIVisionEnabled(true)

	const w, h = 2, 2
	rgba := make([]byte, w*h*4)

	maybeKickIconDetection(rgba, w, h, w*4)
	firstRun := aiVisionIconLastRun.Load()
	if firstRun == 0 {
		t.Fatal("first maybeKickIconDetection call should have recorded a kickoff time")
	}

	maybeKickIconDetection(rgba, w, h, w*4)
	if aiVisionIconLastRun.Load() != firstRun {
		t.Error("a second maybeKickIconDetection call within aiVisionIconInterval must not record a new kickoff")
	}
}

// TestMaybeKickOCRRespectsInterval is TestMaybeKickIconDetectionRespectsInterval's
// counterpart for the OCR loop -- pins that the two loops' throttles are
// genuinely independent state, not the same flag under two names.
func TestMaybeKickOCRRespectsInterval(t *testing.T) {
	resetAIVisionState(t)
	SetAIVisionEnabled(true)

	const w, h = 2, 2
	rgba := make([]byte, w*h*4)

	maybeKickOCR(rgba, w, h, w*4)
	firstRun := aiVisionOCRLastRun.Load()
	if firstRun == 0 {
		t.Fatal("first maybeKickOCR call should have recorded a kickoff time")
	}

	maybeKickOCR(rgba, w, h, w*4)
	if aiVisionOCRLastRun.Load() != firstRun {
		t.Error("a second maybeKickOCR call within aiVisionOCRInterval must not record a new kickoff")
	}
}

// TestSnapshotRGBAHandlesStridePadding covers the case a GPU-owned frame
// buffer commonly has: stride > width*4 (row padding for alignment).
// snapshotRGBA must copy each row from its own offset, not treat the
// buffer as tightly packed.
func TestSnapshotRGBAHandlesStridePadding(t *testing.T) {
	const w, h, stride = 3, 2, 20 // stride well past w*4=12, simulating GPU row padding
	rgba := make([]byte, stride*h)
	set := func(row, x int, r, g, b byte) {
		off := row*stride + x*4
		rgba[off], rgba[off+1], rgba[off+2], rgba[off+3] = r, g, b, 255
	}
	set(0, 0, 10, 20, 30)
	set(0, 2, 11, 21, 31)
	set(1, 0, 110, 120, 130)
	set(1, 2, 111, 121, 131)

	img := snapshotRGBA(rgba, w, h, stride)
	if img.Bounds().Dx() != w || img.Bounds().Dy() != h {
		t.Fatalf("snapshotRGBA size = %dx%d, want %dx%d", img.Bounds().Dx(), img.Bounds().Dy(), w, h)
	}

	check := func(x, y int, r, g, b byte) {
		t.Helper()
		c := img.RGBAAt(x, y)
		if c.R != r || c.G != g || c.B != b || c.A != 255 {
			t.Errorf("pixel (%d,%d) = %+v, want R=%d G=%d B=%d A=255", x, y, c, r, g, b)
		}
	}
	check(0, 0, 10, 20, 30)
	check(2, 0, 11, 21, 31)
	check(0, 1, 110, 120, 130)
	check(2, 1, 111, 121, 131)
}

// TestDrawCachedOverlayDrawsAndSkipsWhenEmpty covers both ends of
// drawCachedOverlay: a cached result burns its boxes into the buffer in
// place, and a nil result (the default, or right after SetAIVisionEnabled
// (false)) leaves the buffer untouched.
func TestDrawCachedOverlayDrawsAndSkipsWhenEmpty(t *testing.T) {
	resetAIVisionState(t)

	const w, h = 40, 40
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 255 // start fully white/opaque
	}

	// No cached result yet -- must be a complete no-op.
	before := append([]byte(nil), img.Pix...)
	drawCachedOverlay(img.Pix, w, h, img.Stride)
	if !bytes.Equal(img.Pix, before) {
		t.Fatal("drawCachedOverlay with no cached result must not touch the buffer")
	}

	// Box placed with enough headroom (Y1=20) that its Set-of-Mark tag --
	// drawn just above the box, see drawMarkTag -- lands entirely above
	// y=20 and doesn't overlap the box border pixels this test checks.
	aiVisionMu.Lock()
	aiVisionResult = &localui.Result{
		Icons: []localui.Icon{{ID: "1", Bbox: localui.Box{X1: 5, Y1: 20, X2: 15, Y2: 30}}},
	}
	aiVisionMu.Unlock()

	drawCachedOverlay(img.Pix, w, h, img.Stride)

	// Bottom-right box border corner: must have turned icon-red.
	if c := img.RGBAAt(15, 30); c.R != 255 || c.G != 0 || c.B != 0 {
		t.Errorf("box border pixel (15,30) = %+v, want icon red {255 0 0 255}", c)
	}
	// Box interior (not on the 2px border, not under the tag above it): untouched white.
	if c := img.RGBAAt(10, 25); c.R != 255 || c.G != 255 || c.B != 255 {
		t.Errorf("box interior pixel (10,25) = %+v, want untouched white", c)
	}
	// Far corner outside the box and its tag: untouched white.
	if c := img.RGBAAt(0, 0); c.R != 255 || c.G != 255 || c.B != 255 {
		t.Errorf("pixel outside any box (0,0) = %+v, want untouched white", c)
	}
}
