package gui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"github.com/sirupsen/logrus"
)

// windowShrinkGuardMargin is the slack (in Fyne units) between a size and
// the content's own MinSize before observeContentResize treats a resize as
// "basically at minimum" -- both for deciding a size is worth remembering
// as lastGoodWindowSize, and for deciding a later resize down to ~MinSize
// is the glitch rather than the user genuinely shrinking the window close
// to its floor.
const windowShrinkGuardMargin float32 = 40

// windowResizeGuard is a pass-through fyne.Layout for a single-child
// wrapper container -- its only job is observing every resize the canvas
// applies, via the Layout call every fyne.Layout gets. fyne.Window and
// fyne.Canvas expose no minimize/restore or resize event of their own (see
// their interfaces in package fyne), so this is the only place application
// code can see this happen at all.
//
// Why this exists: on Windows, minimizing this window and then restoring
// it (from the taskbar) can snap it down to its content's bare MinSize
// instead of the size it actually had before minimizing -- a real,
// reproducible bug, confirmed present both on the plain connections
// screen and while a video stream is active (so it's not specific to this
// app's own Vulkan overlay code). It's a known class of bug in Fyne's
// GLFW driver on Windows -- see e.g. https://github.com/fyne-io/fyne/issues/300
// -- not something fixable from here except by working around its
// symptom: noticing the too-small resize and asking the window to resize
// back to what it was.
type windowResizeGuard struct {
	mw *MainWindow
}

func (g *windowResizeGuard) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) == 0 {
		return fyne.NewSize(0, 0)
	}
	return objects[0].MinSize()
}

func (g *windowResizeGuard) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	objects[0].Resize(size)
	objects[0].Move(fyne.NewPos(0, 0))
	g.mw.observeContentResize(size, objects[0].MinSize())
}

// wrapWithResizeGuard is what every mw.window.SetContent(...) call site for
// the main window should pass its content through, so the guard stays
// active no matter which screen (connections vs. main) is currently shown.
func (mw *MainWindow) wrapWithResizeGuard(content fyne.CanvasObject) fyne.CanvasObject {
	return container.New(&windowResizeGuard{mw: mw}, content)
}

// observeContentResize is windowResizeGuard's actual detection logic: track
// the largest reasonable size the window has actually had (lastGoodWindowSize),
// and if a later resize snaps to ~MinSize while that's a big drop from the
// last known-good size, treat it as the minimize/restore glitch and ask the
// window to resize back -- once, shortly after this layout pass settles
// (resizing synchronously from inside Layout would re-enter the layout
// system mid-pass).
func (mw *MainWindow) observeContentResize(size, minSize fyne.Size) {
	if mw == nil {
		return
	}

	tooSmall := size.Width <= minSize.Width+1 && size.Height <= minSize.Height+1
	hadGoodSize := mw.lastGoodWindowSize.Width > 0 && mw.lastGoodWindowSize.Height > 0
	shrankALot := mw.lastGoodWindowSize.Width > size.Width+windowShrinkGuardMargin ||
		mw.lastGoodWindowSize.Height > size.Height+windowShrinkGuardMargin

	if tooSmall && hadGoodSize && shrankALot {
		if mw.resizeGuardPending {
			return // a correction is already queued, don't stack more
		}
		mw.resizeGuardPending = true
		restoreTo := mw.lastGoodWindowSize
		logrus.Warnf("🪟 [ResizeGuard] window snapped to ~MinSize (%v) after being %v -- likely a Windows minimize/restore glitch, restoring", size, restoreTo)
		time.AfterFunc(150*time.Millisecond, func() {
			fyne.Do(func() {
				mw.resizeGuardPending = false
				if mw.window != nil {
					mw.window.Resize(restoreTo)
				}
			})
		})
		return
	}

	if size.Width > minSize.Width+windowShrinkGuardMargin && size.Height > minSize.Height+windowShrinkGuardMargin {
		mw.lastGoodWindowSize = size
	}
}
