package view

// connection_list_table.go -- the List-mode connections table: one shared
// card (a header row of column titles, then one compact row per
// connection, hairline dividers between rows) instead of List's old
// per-connection card (NewConnectionRow in connection_manager_view.go --
// superseded here, see that function's own doc comment).
//
// Columns: OS (status dot) | NAME (name + edit pencil, a device-platform
// label under it) | STATE (KVM/Agent/Unknown, colored text) | NETWORK
// (LAN/TS) | ROUTE BRIDGE (protocol picker) | ACTIONS (Connect). Modeled on
// a reference screenshot -- widths/spacing are a first pass pending review,
// same as every other screen here. The edit pencil still opens the modal
// editor (ConnectionManager.showEditDialog) -- unchanged from before.

import (
	"image/color"
	"strings"

	"usbridge-client/internal/gui/assets"
	"usbridge-client/internal/gui/design"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
)

// ConnectionListItem bundles one connection's List-table row -- the same
// per-item (data, state, actions) triple NewConnectionGridCard takes,
// reused here since List and Grid render the same underlying data.
type ConnectionListItem struct {
	Data    ConnectionRowData
	State   ConnectionRowState
	Actions ConnectionRowActions
}

// connectionListColumnWidths are the six columns' target widths in the
// order NewConnectionsListTable/newConnectionListHeaderRow/
// newConnectionListRow all build their cells in. 0 means "flexible, absorb
// whatever room the fixed columns and gaps leave" -- only NAME does; the
// header row and every data row share this (and connectionsTableRowLayout)
// so columns line up across rows.
var connectionListColumnWidths = []float32{32, 130, 70, 0, 100, 116}

const connectionListColumnGap float32 = 16

// NewConnectionsListTable builds the whole List-mode table as one
// CanvasObject: a header row of column titles, then one row per item,
// separated by hairline dividers, all inside a single card.
func NewConnectionsListTable(items []ConnectionListItem) fyne.CanvasObject {
	dividerColor := color.NRGBA{R: 0x29, G: 0x2d, B: 0x27, A: 0xff}
	newDivider := func() fyne.CanvasObject {
		sep := canvas.NewRectangle(dividerColor)
		sep.SetMinSize(fyne.NewSize(1, 1))
		return NewInset(sep, 0, 0, 2, 2)
	}

	children := []fyne.CanvasObject{newConnectionListHeaderRow()}
	for _, item := range items {
		children = append(children, newDivider(), newConnectionListRow(item))
	}
	rowsCol := container.New(&tightStatsVBoxLayout{Gap: 0}, children...)

	bg := canvas.NewRectangle(design.ColorGray900)
	bg.CornerRadius = design.RadiusLG
	bg.StrokeColor = design.ColorTailscaleChipBorder
	bg.StrokeWidth = 1

	return container.NewStack(bg, NewInset(rowsCol, 16, 16, 12, 12))
}

func newConnectionListHeaderRow() fyne.CanvasObject {
	labels := []string{"OS", "NAME", "STATE", "NETWORK", "ROUTE BRIDGE", "ACTIONS"}
	cells := make([]fyne.CanvasObject, len(labels))
	for i, l := range labels {
		t := canvas.NewText(l, design.ColorConnectionsSectionSubtitle)
		t.TextSize = 9
		t.TextStyle.Monospace = true

		switch i {
		case 0, 2:
			t.Alignment = fyne.TextAlignCenter
		case 4, 5:
			t.Alignment = fyne.TextAlignTrailing
		}

		cells[i] = t
	}
	return container.New(&connectionsTableRowLayout{Widths: connectionListColumnWidths, Gap: connectionListColumnGap}, cells...)
}

func newConnectionListRow(item ConnectionListItem) fyne.CanvasObject {
	data := item.Data
	isAgent, isKVM := ClassifyConnectionRemoteOS(data.RemoteOS)

	osCell := container.NewCenter(newConnectionCardStatusIndicator(data.RemoteOS))
	nameCell := newConnectionListNameCell(data, item.Actions.OnEdit, isAgent, isKVM)
	stateCell := container.NewCenter(newConnectionListStateCell(isAgent, isKVM))
	networkCell := newConnectionListNetworkCell(data.LANAddress, data.TailscaleAddress)
	routeCell := container.NewBorder(nil, nil, nil, newConnectionListRouteCell(data, item.Actions.OnProtocolChange, item.State))
	actionsCell := container.NewBorder(nil, nil, nil, newConnectionListActionsCell(item))

	row := container.New(&connectionsTableRowLayout{Widths: connectionListColumnWidths, Gap: connectionListColumnGap},
		osCell, nameCell, stateCell, networkCell, routeCell, actionsCell)

	return row
}

func newConnectionListNameCell(data ConnectionRowData, onEdit func(), isAgent, isKVM bool) fyne.CanvasObject {
	nameText := NewBrandText(strings.TrimSpace(data.Name), 11, design.ColorTextLight, true)

	editIcon := fyne.NewStaticResource("connection-edit-title.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#c5c8b5"><path d="M3 17.25V21h3.75L17.81 9.94l-3.75-3.75L3 17.25zm2.92 2.33H5v-.92l9.06-9.06.92.92L5.92 19.58zM20.71 7.04a1.003 1.003 0 0 0 0-1.42L18.37 3.29a1.003 1.003 0 0 0-1.42 0l-1.13 1.13 3.75 3.75 1.14-1.13z"/></svg>`))
	editBtn := newIconChromeButton(iconChromeButtonSpec{
		NormalFill: color.Transparent,
		HoverFill:  design.ColorSurfaceLight,
		Stroke:     color.Transparent,
		NormalIcon: editIcon,
		IconSize:   fyne.NewSize(11, 11),
		ButtonSize: fyne.NewSize(16, 16),
		OnTapped:   onEdit,
	})

	nameRow := container.New(&DeviceRowControlsLayout{Gap: 6}, nameText, editBtn)

	accent := color.Color(design.ColorConnectionAddFill)
	if isAgent {
		accent = design.ColorConnectionBadgeText
	}
	platformLabel := connectionListPlatformLabel(isAgent, isKVM)
	platformPlaque := newConnectionCardChipsRow(platformLabel, "", accent)

	return container.New(&tightStatsVBoxLayout{Gap: 2}, nameRow, platformPlaque)
}

// connectionListPlatformLabel is the small muted line under the name --
// the same "nothing real to source this from yet" situation
// NewConnectionGridCard's chipsRow/PlatformLabel doc comment describes;
// mirrors its exact fallback text so List and Grid agree until a real
// per-model field exists.
func connectionListPlatformLabel(isAgent, isKVM bool) string {
	switch {
	case isKVM:
		return "Radxa"
	case isAgent:
		return "Opensource/Pro"
	default:
		return "Awaiting connection..."
	}
}

func newConnectionListStateCell(isAgent, isKVM bool) fyne.CanvasObject {
	accent := color.Color(design.ColorConnectionAddFill)
	if isAgent {
		accent = design.ColorConnectionBadgeText
	}
	return newConnectionTypeBadge(isAgent, isKVM, accent)
}

func newConnectionListNetworkCell(lanAddress, tailscaleAddress string) fyne.CanvasObject {
	tsValueColor := color.NRGBA{R: 0xeb, G: 0xff, B: 0xbc, A: 0xff}
	lanRow := newConnectionListNetworkLine("LAN", connectionCardAddressOrNone(lanAddress), design.ColorTextLight)
	tsRow := newConnectionListNetworkLine("TS", connectionCardAddressOrNone(tailscaleAddress), tsValueColor)
	return container.New(&tightStatsVBoxLayout{Gap: 2}, lanRow, tsRow)
}

func newConnectionListNetworkLine(label, value string, valueColor color.Color) fyne.CanvasObject {
	c5c8b5Color := color.NRGBA{R: 0xc5, G: 0xc8, B: 0xb5, A: 0xff}
	labelText := canvas.NewText(label, c5c8b5Color)
	labelText.TextSize = 9
	labelText.TextStyle.Monospace = true

	valueText := canvas.NewText(value, valueColor)
	valueText.TextSize = 9
	valueText.TextStyle.Monospace = true

	return container.New(&DeviceRowControlsLayout{Gap: 6}, labelText, valueText)
}

func newConnectionListRouteCell(data ConnectionRowData, onChange func(string), state ConnectionRowState) fyne.CanvasObject {
	if data.HideProtocolSelector {
		// No embedded tsnet in a browser tab (see ConnectionRowData.
		// HideProtocolSelector's own doc comment) -- an invisible spacer
		// keeps this column's width so ACTIONS still lines up with the
		// header.
		spacer := canvas.NewRectangle(color.Transparent)
		spacer.SetMinSize(fyne.NewSize(1, 1))
		return spacer
	}
	dropdown := NewHeaderDropdown(data.ProtocolOptions, data.ProtocolBadge, onChange)
	dropdown.UltraCompact = true
	dropdown.CornerRadius = 6
	dropdown.BorderColor = design.ColorTailscaleChipBorder
	dropdown.TextColor = design.ColorConnectionBadgeText
	dropdown.IconColor = color.NRGBA{R: 0xc5, G: 0xc8, B: 0xb5, A: 0xff}
	dropdown.TextSize = 10
	dropdown.HoverBorderColor = design.ColorConnectionBadgeText
	dropdown.HoverFillColor = design.ColorGray900
	dropdown.SetSelected(data.ProtocolBadge)
	dropdown.SetDisabled(state.Disabled)
	return dropdown
}

func newConnectionListActionsCell(item ConnectionListItem) fyne.CanvasObject {
	connectColor := color.NRGBA{R: 0xc4, G: 0xe7, B: 0x7a, A: 0xff}
	connectHover := color.NRGBA{R: 0xd4, G: 0xf7, B: 0x8a, A: 0xff}

	connectIconColored := strings.ReplaceAll(string(assets.ConnectIconBoldBlack.Content()), "#111111", "#4c6803")
	connectIconResource := fyne.NewStaticResource("custom-connect.svg", []byte(connectIconColored))

	connectBtn := newIconChromeButton(iconChromeButtonSpec{
		NormalFill:   connectColor,
		HoverFill:    connectHover,
		DisabledFill: connectionActionBlockedFill,
		Stroke:       color.Transparent,
		LabelColor:   color.NRGBA{R: 0x4c, G: 0x68, B: 0x03, A: 0xff},
		LabelBold:    true,
		LabelSize:    10,
		CornerRadius: 6,
		NormalIcon:   connectIconResource,
		IconSize:     fyne.NewSize(10, 10),
		ButtonSize:   fyne.NewSize(0, 23),
		OnTapped:     item.Actions.OnUse,
	})
	connectBtn.SetText("Connect")
	connectBtn.SetDisabled(item.State.Disabled)
	connectBtn.SetLoading(item.State.Loading)
	return connectBtn
}

// connectionsTableRowLayout lays out N columns left-to-right per Widths,
// where a <=0 width means "flexible" -- absorb whatever room is left after
// the fixed columns and gaps. Shared by the header row and every data row
// (see connectionListColumnWidths) so columns line up.
type connectionsTableRowLayout struct {
	Widths []float32
	Gap    float32
}

func (l *connectionsTableRowLayout) columnCount(objects []fyne.CanvasObject) int {
	n := len(objects)
	if len(l.Widths) < n {
		n = len(l.Widths)
	}
	return n
}

func (l *connectionsTableRowLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	n := l.columnCount(objects)
	if n == 0 {
		return fyne.NewSize(0, 0)
	}
	var width, height float32
	for i := 0; i < n; i++ {
		w := l.Widths[i]
		childMin := objects[i].MinSize()
		if w <= 0 {
			w = childMin.Width
		}
		width += w
		if childMin.Height > height {
			height = childMin.Height
		}
	}
	if n > 1 {
		width += float32(n-1) * l.Gap
	}
	return fyne.NewSize(width, height)
}

func (l *connectionsTableRowLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	n := l.columnCount(objects)
	if n == 0 {
		return
	}

	fixedTotal := float32(0)
	flexCount := 0
	for i := 0; i < n; i++ {
		if l.Widths[i] > 0 {
			fixedTotal += l.Widths[i]
		} else {
			flexCount++
		}
	}
	gapTotal := float32(0)
	if n > 1 {
		gapTotal = float32(n-1) * l.Gap
	}
	remaining := size.Width - fixedTotal - gapTotal
	if remaining < 0 {
		remaining = 0
	}
	flexWidth := float32(0)
	if flexCount > 0 {
		flexWidth = remaining / float32(flexCount)
	}

	x := float32(0)
	for i := 0; i < n; i++ {
		w := l.Widths[i]
		if w <= 0 {
			w = flexWidth
		}
		obj := objects[i]
		objHeight := obj.MinSize().Height
		y := (size.Height - objHeight) / 2
		if y < 0 {
			y = 0
		}
		obj.Move(fyne.NewPos(x, y))
		obj.Resize(fyne.NewSize(w, objHeight))
		x += w + l.Gap
	}
}
