package api

import (
	"sync/atomic"
	"time"
)

// Live-frame bridge for local ui.parse (see tryLocalUIParse in
// local_ui_intercept.go): lets the video decode path
// (internal/service/ai_vision.go, which already imports this package for
// GetLocalUIParser/SetLocalUIParser) hand over its next decoded frame on
// demand, so a local ui.parse call made while a video session is already
// streaming doesn't ALSO pay for a device screenshot round-trip
// (screen.get_image over /api/mcp, plus whatever capture cost the device
// side pays for it) when a frame is already sitting locally, decoded,
// waiting to be displayed.
//
// This lives in package api, not service, specifically to avoid an import
// cycle: service already imports api (for GetLocalUIParser), so the
// dependency has to run this direction.
//
// Deliberately independent of the AI Vision overlay checkbox
// (service.SetAIVisionEnabled) -- that toggle gates a periodic (every
// aiVisionInterval) background detection pass burned into the live
// picture, a completely different concern from "give me whatever frame
// you're decoding right now, once". The video decode hot path (see
// ai_vision.go's ApplyAIVisionOverlay/goAIVisionSample) checks
// LiveFrameWanted on every frame regardless of that checkbox, at the cost
// of one atomic load when nobody's asking.
var (
	liveFrameWanted atomic.Bool
	liveFrameCh     = make(chan []byte, 1)
)

// RequestLiveFrame asks the video decode path for its next frame
// (PNG-encoded, full resolution) and waits up to timeout for it. Returns
// (nil, false) if no video session is actively decoding frames right now
// (nothing answers within timeout) -- the caller should fall back to
// fetching a screenshot from the device in that case, exactly as if this
// didn't exist.
func RequestLiveFrame(timeout time.Duration) ([]byte, bool) {
	// Drain a stale frame left over from a previous call that timed out
	// before anything arrived, so this call doesn't get handed a frame
	// from seconds ago instead of a fresh one.
	select {
	case <-liveFrameCh:
	default:
	}

	liveFrameWanted.Store(true)
	defer liveFrameWanted.Store(false)

	select {
	case png := <-liveFrameCh:
		return png, true
	case <-time.After(timeout):
		return nil, false
	}
}

// LiveFrameWanted reports whether a RequestLiveFrame call is currently
// blocked waiting for a frame -- checked by the decode path's hot-path
// callback (internal/service/ai_vision.go) so it knows whether to bother
// capturing+encoding this frame, without service needing to import api (it
// already does) or api needing to import service (which would cycle).
func LiveFrameWanted() bool {
	return liveFrameWanted.Load()
}

// SubmitLiveFrame delivers one PNG-encoded frame to a pending
// RequestLiveFrame call. Non-blocking and a no-op if nobody's currently
// waiting (LiveFrameWanted was false, or another frame already claimed
// this request) -- the decode thread must never stall on this.
func SubmitLiveFrame(png []byte) {
	if !liveFrameWanted.CompareAndSwap(true, false) {
		return
	}
	select {
	case liveFrameCh <- png:
	default:
	}
}
