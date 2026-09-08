package main

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// asyncLogWriterQueueSize is how many not-yet-written log lines can queue
// up before Write starts dropping -- generous enough to absorb a real
// multi-second loss-storm burst (confirmed live: a couple hundred lines
// over 2-3s from moonlight_cgo_wrapper.go's goVTLog, even before that
// function's own repeat-collapsing throttle was added) many times over,
// while still bounding worst-case memory if the underlying writer
// genuinely stalls.
const asyncLogWriterQueueSize = 8192

// asyncLogWriter decouples every logrus call from the actual (potentially
// blocking) I/O of writing a log line out. setupLogging wraps whatever
// writer it built (stdout, the log file, or both via io.MultiWriter) in
// one of these before handing it to logrus.SetOutput -- without it, every
// single log call is a synchronous write(2) (this project sets no
// buffering of its own around the *os.File it opens), and a real network-
// loss storm can produce well over a hundred log lines in a couple of
// seconds (confirmed live) -- exactly competing for CPU/scheduler time
// with the CGO video/depacketizer thread that's simultaneously trying to
// recover the stream, since that thread is the same one calling into
// goVTLog for most of those lines.
//
// Write() never blocks the caller: it copies the line (logrus reuses its
// own internal formatting buffer between calls, so the bytes handed to
// Write are only guaranteed valid for the duration of that call) into a
// bounded channel and returns immediately. A single background goroutine
// drains that channel and does the real, potentially-blocking write to the
// underlying writer, so log line *ordering* is still preserved (logrus
// itself already serializes calls into Out.Write via its own internal
// mutex, so this writer only ever sees one Write() in flight at a time --
// the channel doesn't need to do any of its own reordering-prevention
// work, just FIFO, which is what it already is).
type asyncLogWriter struct {
	inner     io.Writer
	lines     chan []byte
	stop      chan struct{}
	done      chan struct{}
	dropped   uint64 // atomic
	closeOnce sync.Once
}

func newAsyncLogWriter(inner io.Writer) *asyncLogWriter {
	w := &asyncLogWriter{
		inner: inner,
		lines: make(chan []byte, asyncLogWriterQueueSize),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go w.run()
	return w
}

func (w *asyncLogWriter) run() {
	defer close(w.done)
	for {
		select {
		case line := <-w.lines:
			// Best-effort: a write error here has nowhere left to report
			// to (this already IS the logging path) -- matches how a
			// dropped line (queue full, in Write below) is handled, just
			// discovered a step later.
			_, _ = w.inner.Write(line)
		case <-w.stop:
			// Drain whatever's already queued without blocking on more --
			// Close() is waiting on w.done and callers may still be
			// calling Write() concurrently (see Write's own doc comment
			// on why that's safe), so this is a best-effort final flush,
			// not a guarantee every last line gets out.
			for {
				select {
				case line := <-w.lines:
					_, _ = w.inner.Write(line)
				default:
					return
				}
			}
		}
	}
}

// Write is what logrus actually calls. If the queue is ever full (the
// underlying writer falling far enough behind that thousands of lines have
// backed up -- not something normal disk I/O should ever produce, but a
// stalled/full disk or a redirected pipe with a stuck reader could), the
// new line is dropped rather than blocking the caller: losing a debug log
// line is a vastly better outcome than stalling whatever part of the app
// just tried to log something.
//
// Safe to call even after Close(): `lines` itself is never closed (only
// the separate `stop` signal is, exactly so this never risks a "send on
// closed channel" panic no matter how Write and Close race) -- a call
// after the background goroutine has already exited just buffers into the
// channel harmlessly (never read, GC'd with the process) until the queue
// limit above is reached, at which point it starts dropping same as ever.
// This matters concretely for main()'s panic-recovery path, which must
// still be able to log (and have a real chance of that line reaching the
// file) after Close() already ran once elsewhere in the same unwind --
// see main()'s own doc comments on that exact sequence.
func (w *asyncLogWriter) Write(p []byte) (int, error) {
	cp := make([]byte, len(p))
	copy(cp, p)
	select {
	case w.lines <- cp:
	default:
		atomic.AddUint64(&w.dropped, 1)
	}
	return len(p), nil
}

// Close stops the background writer goroutine, waits (up to timeout) for
// its final drain to finish, and reports how many lines were ever dropped
// along the way (0 in the overwhelmingly common case) by writing directly
// to the underlying writer. Safe to call more than once (the actual stop
// signal only ever fires once, via sync.Once; later calls just wait on the
// already-closed `done` and return immediately) -- main.go's own panic-
// recovery path relies on this: it calls Close() unconditionally in two
// places that both run during a single panic unwind (LIFO defer order --
// see main()'s comments), and neither is allowed to assume it's the only
// caller.
func (w *asyncLogWriter) Close(timeout time.Duration) {
	w.closeOnce.Do(func() { close(w.stop) })
	select {
	case <-w.done:
	case <-time.After(timeout):
	}
	if n := atomic.LoadUint64(&w.dropped); n > 0 {
		_, _ = io.WriteString(w.inner, fmt.Sprintf("async log writer: %d line(s) dropped (queue full)\n", n))
	}
}
