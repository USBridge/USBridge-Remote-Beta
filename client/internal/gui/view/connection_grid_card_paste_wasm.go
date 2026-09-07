//go:build js && wasm

package view

import (
	"syscall/js"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// pasteClipboardIntoEntry is connection_grid_card_paste_default.go's browser
// counterpart -- Fyne's own synchronous Clipboard().Content() is unreliable
// under wasm (see controller.pasteClipboardInto's doc comment for the full
// story, which this mirrors), so this routes through
// navigator.clipboard.readText() instead.
func pasteClipboardIntoEntry(entry *widget.Entry) {
	if entry == nil {
		return
	}
	clipboard := js.Global().Get("navigator").Get("clipboard")
	if clipboard.IsUndefined() || clipboard.IsNull() || clipboard.Get("readText").IsUndefined() {
		return
	}
	promise := clipboard.Call("readText")
	promise.Call("then",
		js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			defer func() { recover() }()
			if len(args) == 0 {
				return nil
			}
			text := args[0].String()
			if text == "" {
				return nil
			}
			fyne.Do(func() {
				entry.SetText(text)
			})
			return nil
		}),
		js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			defer func() { recover() }()
			return nil
		}),
	)
}
