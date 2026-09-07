package api

import (
	"sync"
	"testing"
	"time"
)

// TestRequestLiveFrame_TimesOutWithNoDecodeLoop pins the "no video session
// streaming" case (a headless MCP agent calling ui.parse with nothing
// decoding frames) -- must return promptly with ok=false, not hang.
func TestRequestLiveFrame_TimesOutWithNoDecodeLoop(t *testing.T) {
	png, ok := RequestLiveFrame(30 * time.Millisecond)
	if ok || png != nil {
		t.Fatalf("expected (nil, false) with nothing answering, got (%v, %v)", png, ok)
	}
	if LiveFrameWanted() {
		t.Fatal("LiveFrameWanted must be false again after the call returns")
	}
}

// TestRequestLiveFrame_ReceivesSubmittedFrame simulates the decode path:
// a goroutine polls LiveFrameWanted (as ai_vision.go's hot-path callback
// does) and calls SubmitLiveFrame once it sees a pending request.
func TestRequestLiveFrame_ReceivesSubmittedFrame(t *testing.T) {
	want := []byte("fake-png-bytes")
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if LiveFrameWanted() {
				SubmitLiveFrame(want)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	got, ok := RequestLiveFrame(time.Second)
	<-done
	if !ok {
		t.Fatal("expected a frame to be delivered")
	}
	if string(got) != string(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestSubmitLiveFrame_NoopWhenNobodyWaiting makes sure a decode-thread
// submit outside of any pending request never blocks and never leaves a
// stale frame for a later, unrelated RequestLiveFrame call to pick up.
func TestSubmitLiveFrame_NoopWhenNobodyWaiting(t *testing.T) {
	SubmitLiveFrame([]byte("stale"))

	png, ok := RequestLiveFrame(30 * time.Millisecond)
	if ok {
		t.Fatalf("RequestLiveFrame must not receive a frame submitted before it asked, got %q", png)
	}
}

// TestRequestLiveFrame_ConcurrentCallersDontPanic exercises the shared
// channel/flag under concurrent RequestLiveFrame callers -- at most one is
// meant to "win" a given SubmitLiveFrame, but none may panic or deadlock.
func TestRequestLiveFrame_ConcurrentCallersDontPanic(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			RequestLiveFrame(20 * time.Millisecond)
		}()
	}
	wg.Wait()
}
