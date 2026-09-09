package view

// styled_entry.go -- StyledEntry is a widget.Entry whose right-click context
// menu (Cut/Copy/Paste/Select All) matches this app's own popups
// (HeaderDropdown's protocol/route-bridge picker, the header's language
// menu -- both built on ShowStyledMenu) instead of Fyne's default
// TappedSecondary, which opens widget.NewPopUpMenu styled by the raw Fyne
// theme and reads as unstyled next to everything else in this app. Every
// text field this package hands out for editing (Grid's inline Name field,
// the LAN/TS/Token box NewConnectionCardEditableStatsBox builds -- shared by
// Grid's inline edit, List's split-edit panel, and the Add Connection
// dialog) should be one of these, not a plain widget.NewEntry().

import (
	"usbridge-client/internal/gui/design"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// StyledEntry is widget.Entry with ShowEntryContextMenu wired into
// TappedSecondary instead of Fyne's own unstyled popup.
type StyledEntry struct {
	widget.Entry
}

// NewStyledEntry builds an empty StyledEntry -- the drop-in replacement for
// widget.NewEntry() everywhere in this package.
func NewStyledEntry() *StyledEntry {
	e := &StyledEntry{}
	e.ExtendBaseWidget(e)
	return e
}

func (e *StyledEntry) TappedSecondary(*fyne.PointEvent) {
	if c := fyne.CurrentApp().Driver().CanvasForObject(e); c != nil {
		c.Focus(e)
	}
	ShowEntryContextMenu(&e.Entry, e, e.TypedShortcut)
}

var _ fyne.SecondaryTappable = (*StyledEntry)(nil)

// EntryContextMenuItems is the Cut/Copy/Paste/Select All row set for entry,
// mirroring widget.Entry's own TappedSecondary logic (which item set applies
// while disabled/password) but built as StyledMenuItem so a caller can hand
// them to ShowStyledMenu instead of Fyne's default popup. typedShortcut is
// entry's own TypedShortcut method value -- callers with a TypedShortcut
// override of their own (e.g. controller's connectionDialogEntry, which also
// notifies OnChanged on Cut/Paste) pass that override instead of entry's
// plain widget.Entry.TypedShortcut, so Cut/Paste from this menu behave
// exactly like the same shortcut typed on the keyboard would.
func EntryContextMenuItems(entry *widget.Entry, typedShortcut func(fyne.Shortcut)) []StyledMenuItem {
	clipboard := fyne.CurrentApp().Clipboard()
	cut := StyledMenuItem{Label: "Cut", OnTap: func() {
		typedShortcut(&fyne.ShortcutCut{Clipboard: clipboard})
	}}
	copyItem := StyledMenuItem{Label: "Copy", OnTap: func() {
		typedShortcut(&fyne.ShortcutCopy{Clipboard: clipboard})
	}}
	paste := StyledMenuItem{Label: "Paste", OnTap: func() {
		typedShortcut(&fyne.ShortcutPaste{Clipboard: clipboard})
	}}
	selectAll := StyledMenuItem{Label: "Select All", OnTap: func() {
		typedShortcut(&fyne.ShortcutSelectAll{})
	}}

	switch {
	case entry.Disabled():
		return []StyledMenuItem{copyItem, selectAll}
	case entry.Password:
		return []StyledMenuItem{paste, selectAll}
	default:
		return []StyledMenuItem{cut, copyItem, paste, selectAll}
	}
}

// ShowEntryContextMenu opens EntryContextMenuItems(entry, typedShortcut) as
// a styled popup anchored to anchor (normally entry itself, or the outer
// widget wrapping it) -- see StyledEntry.TappedSecondary and controller's
// connectionDialogEntry, the two current callers. Same teal/10px look as
// ShowStyledMenuTeal (the header's language menu, the per-connection
// protocol dropdown's own popup) rather than default ShowStyledMenu's
// 14px/light-text rows, and IgnoreAnchorWidth so it sizes to its own
// longest row (Select All) instead of stretching to match a wide field --
// unlike a dropdown, a text entry's context menu isn't picking a value for
// that field, so there's no reason it should share its width.
func ShowEntryContextMenu(entry *widget.Entry, anchor fyne.CanvasObject, typedShortcut func(fyne.Shortcut)) {
	items := EntryContextMenuItems(entry, typedShortcut)
	if len(items) == 0 {
		return
	}
	showStyledMenu(anchor, items, StyledMenuOptions{
		TextColor:         design.ColorConnectionBadgeText,
		TextSize:          10,
		RowHeight:         26,
		IgnoreAnchorWidth: true,
	})
}
