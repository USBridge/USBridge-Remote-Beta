//go:build !(js && wasm)

package view

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// pasteClipboardIntoEntry reads the system clipboard and replaces entry's
// whole content with it, via Fyne's own (synchronous, OS-native) Clipboard
// API -- see connection_grid_card_paste_wasm.go's counterpart for why the
// browser build needs a different, async path instead. This is the Grid
// card's own copy of controller.pasteClipboardInto's split (view can't
// import controller).
func pasteClipboardIntoEntry(entry *widget.Entry) {
	if entry == nil {
		return
	}
	text := fyne.CurrentApp().Clipboard().Content()
	if text == "" {
		return
	}
	entry.SetText(text)
}
