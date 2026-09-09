package view

// connection_edit_panel.go -- List's split-edit panel (see
// connection_list_table.go's NewConnectionsListSplit): a standalone widget
// in this app's current visual language, reusing the same compact field
// styling connection_grid_card.go's inline edit already established
// (gridCardFieldTheme/wrapGridCardEntry, newConnectionCardEditableStatsBox,
// the same Save/Delete/Cancel icon trio) -- NOT the old
// connectionDialogEntry-based modal (controller/connection_manager_dialogs.go),
// which Add-connection still uses. Deliberately its own widget, sharing no
// code with that modal, so each can be restyled independently.
//
// First pass -- noticeably more compact than the modal it replaces for
// List (smaller text, tighter padding, no title bar). Fixed-width by
// nature (nothing in its layout is flexible) and never stretched by its
// caller (connection_list_table.go's connectionsListSplitLayout) -- it
// sits at its own natural size next to the row being edited instead of
// filling a reserved half of the window.

import (
	"image/color"
	"strings"

	"usbridge-client/internal/gui/assets"
	"usbridge-client/internal/gui/design"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
)

// ConnectionEditPanelData is what the panel needs to prefill its fields.
type ConnectionEditPanelData struct {
	Name             string
	RemoteOS         string
	LANAddress       string
	TailscaleAddress string
	MasterKey        string
}

// ConnectionEditPanelActions are the events the panel can report.
type ConnectionEditPanelActions struct {
	// OnSave commits the edited Name/LAN/TS/Token fields.
	OnSave func(name, lanAddress, tailscaleAddress, masterKey string)
	// OnDelete removes the connection -- same confirm-then-delete flow the
	// old modal's Delete button used.
	OnDelete func()
	// OnCancel closes the panel (List's split view collapses back to the
	// full table) without saving.
	OnCancel func()
}

// NewConnectionEditPanel builds List's split-edit panel.
func NewConnectionEditPanel(data ConnectionEditPanelData, actions ConnectionEditPanelActions) fyne.CanvasObject {
	statusIndicator := newConnectionEditPanelStatusIndicator(data.RemoteOS)

	nameEntry := NewStyledEntry()
	nameEntry.SetPlaceHolder("Name")
	nameEntry.SetText(strings.TrimSpace(data.Name))
	nameEntry.TextStyle = fyne.TextStyle{Bold: true}

	// NewInset's args are (content, left, right, top, bottom) -- this wants
	// a gap to the right of the icon, before the name field, not a top
	// pad (that pushed the icon down out of vertical center within its
	// Center wrapper).
	topRow := container.NewBorder(nil, nil, container.NewCenter(NewInset(statusIndicator, 0, 8, 0, 0)), nil, wrapGridCardEntry(nameEntry, 13, design.ColorTextLight))

	cancelIcon := fyne.NewStaticResource("connection-cancel-edit.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#c5c8b5"><path d="M18.3 5.71 12 12.01l-6.3-6.3-1.41 1.41 6.3 6.3-6.3 6.3 1.41 1.41 6.3-6.3 6.3 6.3 1.41-1.41-6.3-6.3 6.3-6.3z"/></svg>`))
	cancelBtn := newIconChromeButton(iconChromeButtonSpec{
		NormalFill:   color.Transparent,
		HoverFill:    design.ColorSurfaceLight,
		DisabledFill: connectionActionBlockedFill,
		Stroke:       design.ColorTailscaleChipBorder,
		StrokeWidth:  1,
		CornerRadius: 6,
		NormalIcon:   cancelIcon,
		IconSize:     fyne.NewSize(11, 11),
		ButtonSize:   fyne.NewSize(26, 26),
		OnTapped:     actions.OnCancel,
	})

	statsBox, _, lanEntry, tailscaleEntry, tokenEntry := NewConnectionCardEditableStatsBox(false, "", data.LANAddress, data.TailscaleAddress, data.MasterKey, 160)

	saveIcon := fyne.NewStaticResource("connection-save.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#111111"><path d="M9 16.2 4.8 12l-1.4 1.4L9 19 21 7l-1.4-1.4z"/></svg>`))
	saveBtn := newIconChromeButton(iconChromeButtonSpec{
		NormalFill:   design.ColorConnectionBadgeText,
		HoverFill:    color.NRGBA{R: 0x61, G: 0xf0, B: 0xd3, A: 0xff},
		DisabledFill: connectionActionBlockedFill,
		Stroke:       color.Transparent,
		CornerRadius: 6,
		NormalIcon:   saveIcon,
		IconSize:     fyne.NewSize(13, 13),
		ButtonSize:   fyne.NewSize(26, 26),
		OnTapped: func() {
			if actions.OnSave == nil {
				return
			}
			actions.OnSave(nameEntry.Text, lanEntry.Text, tailscaleEntry.Text, tokenEntry.Text)
		},
	})

	deleteIcon := fyne.NewStaticResource("connection-delete.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#c5c8b5"><path d="M6 19c0 1.1.9 2 2 2h8c1.1 0 2-.9 2-2V7H6v12zM19 4h-3.5l-1-1h-5l-1 1H5v2h14V4z"/></svg>`))
	deleteBtn := newIconChromeButton(iconChromeButtonSpec{
		NormalFill:   color.Transparent,
		HoverFill:    design.ColorSurfaceLight,
		DisabledFill: connectionActionBlockedFill,
		Stroke:       design.ColorTailscaleChipBorder,
		StrokeWidth:  1,
		CornerRadius: 6,
		NormalIcon:   deleteIcon,
		IconSize:     fyne.NewSize(13, 13),
		ButtonSize:   fyne.NewSize(26, 26),
		OnTapped:     actions.OnDelete,
	})

	actionsBox := container.New(&DeviceRowControlsLayout{Gap: 8}, deleteBtn, saveBtn, cancelBtn)
	bottomRow := container.NewBorder(nil, nil, nil, actionsBox)

	// A little breathing room between name/info-box/buttons -- 3px above
	// the info box, 3px above the buttons, and the card's own bottom
	// margin brought down to that same 3px (was 14, matching the other
	// three sides) so the gap below the buttons doesn't read as bigger
	// than the gap above them. tightStatsVBoxLayout (not container.NewVBox,
	// which adds its own hidden inter-child theme padding on top of these
	// -- that stray couple px was exactly why "above" still read bigger
	// than "below" even at matching NewInset values) stacks with only the
	// gap it's told to, so these 3px are the whole story now.
	content := NewInset(container.New(&tightStatsVBoxLayout{Gap: 0},
		topRow,
		NewInset(statsBox, 0, 0, 3, 0),
		NewInset(bottomRow, 0, 0, 3, 0),
	), 14, 14, 14, 3)

	bg := canvas.NewRectangle(design.ColorGray900)
	bg.CornerRadius = design.RadiusLG
	bg.StrokeColor = design.ColorTailscaleChipBorder
	bg.StrokeWidth = 1

	// No width policy applied here on purpose -- this widget just reports
	// its own natural size (governed by its fixed-width internals: the
	// LAN/TS/Token entries' rightAlignedInputLayout, the icon-sized
	// buttons -- nothing in here is flexible). Whether/how that gets
	// stretched or capped is the caller's call: NewConnectionsListSplit's
	// connectionsListSplitLayout gives it exactly this natural width and
	// never stretches it, letting the table absorb whatever room is left
	// instead.
	return container.NewStack(bg, content)
}

func newConnectionEditPanelStatusIndicator(remoteOS string) fyne.CanvasObject {
	const size = float32(20)
	isAgent, isKVM := ClassifyConnectionRemoteOS(remoteOS)
	var res fyne.Resource
	switch {
	case isKVM:
		res = assets.USBridgeOSIconAccent
	case isAgent:
		res = agentOSIconResource(remoteOS)
	}
	if res != nil {
		img := canvas.NewImageFromResource(res)
		img.FillMode = canvas.ImageFillContain
		img.SetMinSize(fyne.NewSize(size, size))
		return container.NewGridWrap(fyne.NewSize(size, size), img)
	}
	dot := canvas.NewCircle(design.ColorBorder)
	dotWrap := container.NewGridWrap(fyne.NewSize(10, 10), dot)
	return container.NewGridWrap(fyne.NewSize(size, size), container.NewCenter(dotWrap))
}
