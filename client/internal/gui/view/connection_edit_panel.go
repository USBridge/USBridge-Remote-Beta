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
// First pass -- sized for sitting in an HSplit's right half rather than a
// centered popup, so noticeably more compact than the modal it replaces
// for List (smaller text, tighter padding, no title bar).

import (
	"image/color"
	"strings"

	"usbridge-client/internal/gui/design"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
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
	statusIndicator := newConnectionCardStatusIndicator(data.RemoteOS)

	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("Name")
	nameEntry.SetText(strings.TrimSpace(data.Name))
	nameEntry.TextStyle = fyne.TextStyle{Bold: true}

	cancelIcon := fyne.NewStaticResource("connection-cancel-edit.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#c5c8b5"><path d="M18.3 5.71 12 12.01l-6.3-6.3-1.41 1.41 6.3 6.3-6.3 6.3 1.41 1.41 6.3-6.3 6.3 6.3 1.41-1.41-6.3-6.3 6.3-6.3z"/></svg>`))
	cancelBtn := newIconChromeButton(iconChromeButtonSpec{
		NormalFill:   color.Transparent,
		HoverFill:    design.ColorSurfaceLight,
		Stroke:       design.ColorTailscaleChipBorder,
		StrokeWidth:  1,
		CornerRadius: 3,
		NormalIcon:   cancelIcon,
		IconSize:     fyne.NewSize(11, 11),
		ButtonSize:   fyne.NewSize(22, 22),
		OnTapped:     actions.OnCancel,
	})

	topRow := container.NewBorder(nil, nil, statusIndicator, cancelBtn, wrapGridCardEntry(nameEntry, 13, design.ColorTextLight))

	statsBox, lanEntry, tailscaleEntry, tokenEntry := newConnectionCardEditableStatsBox(data.LANAddress, data.TailscaleAddress, data.MasterKey)

	connectColor := color.NRGBA{R: 0xc4, G: 0xe7, B: 0x7a, A: 0xff}
	connectHover := color.NRGBA{R: 0xd4, G: 0xf7, B: 0x8a, A: 0xff}
	saveIcon := fyne.NewStaticResource("connection-save.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#4c6803"><path d="M9 16.2 4.8 12l-1.4 1.4L9 19 21 7l-1.4-1.4z"/></svg>`))
	saveBtn := newIconChromeButton(iconChromeButtonSpec{
		NormalFill:   connectColor,
		HoverFill:    connectHover,
		Stroke:       color.Transparent,
		CornerRadius: 6,
		NormalIcon:   saveIcon,
		IconSize:     fyne.NewSize(12, 12),
		ButtonSize:   fyne.NewSize(0, 28),
		LabelColor:   color.NRGBA{R: 0x4c, G: 0x68, B: 0x03, A: 0xff},
		LabelBold:    true,
		OnTapped: func() {
			if actions.OnSave == nil {
				return
			}
			actions.OnSave(nameEntry.Text, lanEntry.Text, tailscaleEntry.Text, tokenEntry.Text)
		},
	})
	saveBtn.SetText("Save")

	deleteIcon := fyne.NewStaticResource("connection-delete.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#c5c8b5"><path d="M6 19c0 1.1.9 2 2 2h8c1.1 0 2-.9 2-2V7H6v12zM19 4h-3.5l-1-1h-5l-1 1H5v2h14V4z"/></svg>`))
	deleteBtn := newIconChromeButton(iconChromeButtonSpec{
		NormalFill:   color.Transparent,
		HoverFill:    design.ColorSurfaceLight,
		Stroke:       design.ColorTailscaleChipBorder,
		StrokeWidth:  1,
		CornerRadius: 6,
		NormalIcon:   deleteIcon,
		IconSize:     fyne.NewSize(13, 13),
		ButtonSize:   fyne.NewSize(34, 28),
		OnTapped:     actions.OnDelete,
	})

	bottomRow := container.NewBorder(nil, nil, deleteBtn, nil, NewInset(saveBtn, 8, 0, 0, 0))

	content := NewInset(container.NewVBox(topRow, statsBox, bottomRow), 14, 14, 14, 14)

	bg := canvas.NewRectangle(design.ColorGray900)
	bg.CornerRadius = design.RadiusLG
	bg.StrokeColor = design.ColorTailscaleChipBorder
	bg.StrokeWidth = 1

	return container.NewStack(bg, content)
}
