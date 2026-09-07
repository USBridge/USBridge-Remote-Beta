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
// same as every other screen here.
//
// The edit pencil no longer opens the modal editor as an overlay -- it
// switches the whole List view into a split layout (NewConnectionsListSplit):
// the table -- NETWORK, ROUTE BRIDGE and ACTIONS columns dropped, see
// buildConnectionsListTable's compact mode -- docked left, the edit panel
// (view.NewConnectionEditPanel, built by the controller) docked right.
// Unlike a plain HSplit, the panel doesn't stretch to the table's height --
// connectionsListSplitLayout pins it at a fixed height, positioned next to
// whichever row is being edited (so a long, scrolled table still opens the
// panel near that row instead of at the top). That row also gets a
// rounded teal outline (newConnectionListRow's highlighted flag) so it's
// obvious which one the panel belongs to. "X" or Save/Delete on the panel
// exits back to the normal full table.

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

// connectionListColumnLabels/connectionListColumnWidths are the full
// (non-editing) table's six columns, in the order
// newConnectionListHeaderRow/newConnectionListRow build cells in. 0 means
// "flexible, absorb whatever room the fixed columns and gaps leave" -- only
// NETWORK does. connectionListCompactColumn{Labels,Widths} is the same
// table with NETWORK, ROUTE BRIDGE and ACTIONS dropped -- used while a row
// is being edited (see NewConnectionsListSplit): none of the three are
// relevant while editing (the connection method and Connect/Delete belong
// to the un-edited state), and there's no room for them once the table is
// squeezed into half the width besides. Header and data rows always share
// whichever pair is active (via connectionsTableRowLayout) so columns line
// up.
var (
	connectionListColumnLabels = []string{"OS", "NAME", "STATE", "NETWORK", "ROUTE BRIDGE", "ACTIONS"}
	connectionListColumnWidths = []float32{32, 130, 70, 0, 100, 150}

	connectionListCompactColumnLabels = []string{"OS", "NAME", "STATE"}
	connectionListCompactColumnWidths = []float32{32, 0, 70}
)

const connectionListColumnGap float32 = 16

// connectionListSplitGap is the horizontal gap between the compact table
// and the edit panel in NewConnectionsListSplit.
const connectionListSplitGap float32 = 16

// NewConnectionsListTable builds the whole List-mode table as one
// CanvasObject: a header row of column titles, then one row per item,
// separated by hairline dividers, all inside a single card.
func NewConnectionsListTable(items []ConnectionListItem) fyne.CanvasObject {
	table, _, _ := buildConnectionsListTable(items, false, -1)
	return table
}

// NewConnectionsListSplit is List's edit-mode layout: the table with its
// NETWORK/ROUTE BRIDGE/ACTIONS columns dropped (buildConnectionsListTable's
// compact mode, editIndex's row also getting a teal highlight outline)
// docked to the left half, editPanel -- built by the controller, since this
// package can't import controller -- pinned next to that row at its own
// fixed height (connectionsListSplitLayout) rather than stretched to match
// the table, so it opens near the row being edited instead of always at the
// top or spanning the whole table's height.
func NewConnectionsListSplit(items []ConnectionListItem, editIndex int, editPanel fyne.CanvasObject) fyne.CanvasObject {
	table, rowY, rowHeight := buildConnectionsListTable(items, true, editIndex)
	return container.New(&connectionsListSplitLayout{
		editRowY:      rowY,
		editRowHeight: rowHeight,
		gap:           connectionListSplitGap,
	}, table, editPanel)
}

// buildConnectionsListTable builds the table itself. highlightIndex, when
// >= 0, both gets that item's row a teal outline (newConnectionListRow's
// highlighted flag) and makes this return that row's Y offset/height
// within the returned table object (0/0 otherwise) -- computed from the
// same real MinSize() calls tightStatsVBoxLayout's own Layout will use to
// actually stack these rows, so it lands exactly where the row ends up
// once rendered, not just an estimate.
func buildConnectionsListTable(items []ConnectionListItem, compact bool, highlightIndex int) (table fyne.CanvasObject, highlightY, highlightHeight float32) {
	labels, widths := connectionListColumnLabels, connectionListColumnWidths
	if compact {
		labels, widths = connectionListCompactColumnLabels, connectionListCompactColumnWidths
	}

	dividerColor := color.NRGBA{R: 0x29, G: 0x2d, B: 0x27, A: 0xff}
	newDivider := func() fyne.CanvasObject {
		sep := canvas.NewRectangle(dividerColor)
		sep.SetMinSize(fyne.NewSize(1, 1))
		return NewInset(sep, 0, 0, 2, 2)
	}

	header := newConnectionListHeaderRow(labels, widths)
	children := []fyne.CanvasObject{header}
	y := header.MinSize().Height

	for i, item := range items {
		div := newDivider()
		row := newConnectionListRow(item, widths, compact, i == highlightIndex)
		divHeight := div.MinSize().Height
		rowHeightVal := row.MinSize().Height
		if i == highlightIndex {
			// +12 for the outer NewInset's own top padding below -- this
			// offset needs to be relative to the whole `table` object's
			// bounds, not just the inner rowsCol's.
			highlightY = y + divHeight + 12
			highlightHeight = rowHeightVal
		}
		children = append(children, div, row)
		y += divHeight + rowHeightVal
	}
	rowsCol := container.New(&tightStatsVBoxLayout{Gap: 0}, children...)

	bg := canvas.NewRectangle(design.ColorGray900)
	bg.CornerRadius = design.RadiusLG
	bg.StrokeColor = design.ColorTailscaleChipBorder
	bg.StrokeWidth = 1

	table = container.NewStack(bg, NewInset(rowsCol, 16, 16, 12, 12))
	return table, highlightY, highlightHeight
}

func newConnectionListHeaderRow(labels []string, widths []float32) fyne.CanvasObject {
	cells := make([]fyne.CanvasObject, len(labels))
	for i, l := range labels {
		t := canvas.NewText(l, design.ColorConnectionsSectionSubtitle)
		t.TextSize = 9
		t.TextStyle.Monospace = true

		switch l {
		case "OS", "STATE":
			t.Alignment = fyne.TextAlignCenter
		case "ROUTE BRIDGE", "ACTIONS":
			t.Alignment = fyne.TextAlignTrailing
		}

		cells[i] = t
	}
	return container.New(&connectionsTableRowLayout{Widths: widths, Gap: connectionListColumnGap}, cells...)
}

// newConnectionListRow builds one data row. highlighted wraps it in a
// rounded teal outline spanning every column (the split-edit layout's way
// of showing which row editPanel belongs to -- see
// NewConnectionsListSplit/buildConnectionsListTable); false for every row
// everywhere else.
func newConnectionListRow(item ConnectionListItem, widths []float32, compact bool, highlighted bool) fyne.CanvasObject {
	data := item.Data
	isAgent, isKVM := ClassifyConnectionRemoteOS(data.RemoteOS)

	osCell := container.NewCenter(newConnectionCardStatusIndicator(data.RemoteOS))
	nameCell := newConnectionListNameCell(data, item.Actions.OnEdit, isAgent, isKVM)
	stateCell := container.NewCenter(newConnectionListStateCell(isAgent, isKVM))

	cells := []fyne.CanvasObject{osCell, nameCell, stateCell}
	if !compact {
		networkCell := newConnectionListNetworkCell(data.LANAddress, data.TailscaleAddress)
		routeCell := container.NewBorder(nil, nil, nil, newConnectionListRouteCell(data, item.Actions.OnProtocolChange, item.State))
		actionsCell := container.NewBorder(nil, nil, nil, newConnectionListActionsCell(item))
		cells = append(cells, networkCell, routeCell, actionsCell)
	}

	row := container.New(&connectionsTableRowLayout{Widths: widths, Gap: connectionListColumnGap}, cells...)
	if !highlighted {
		return row
	}

	highlightBorder := canvas.NewRectangle(color.Transparent)
	highlightBorder.StrokeColor = design.ColorConnectionBadgeText
	highlightBorder.StrokeWidth = 1
	highlightBorder.CornerRadius = 8
	return container.NewStack(highlightBorder, NewInset(row, 8, 8, 4, 4))
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

	deleteIcon := fyne.NewStaticResource("connection-delete.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#c5c8b5"><path d="M6 19c0 1.1.9 2 2 2h8c1.1 0 2-.9 2-2V7H6v12zM19 4h-3.5l-1-1h-5l-1 1H5v2h14V4z"/></svg>`))
	deleteBtn := newIconChromeButton(iconChromeButtonSpec{
		NormalFill:   color.Transparent,
		HoverFill:    design.ColorSurfaceLight,
		DisabledFill: connectionActionBlockedFill,
		Stroke:       design.ColorTailscaleChipBorder,
		StrokeWidth:  1,
		CornerRadius: 6,
		NormalIcon:   deleteIcon,
		IconSize:     fyne.NewSize(11, 11),
		ButtonSize:   fyne.NewSize(23, 23),
		OnTapped:     item.Actions.OnDelete,
	})
	deleteBtn.SetDisabled(item.State.Disabled)

	return container.New(&DeviceRowControlsLayout{Gap: 6}, deleteBtn, connectBtn)
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

// connectionsListSplitLayout is List's split-edit layout (see
// NewConnectionsListSplit): the compact table docked left at half the
// available width and its own natural (unstretched) height; the edit panel
// pinned at editRowY -- the Y within the table the row being edited starts
// at -- vertically centered on that row using its own fixed, natural
// height (never stretched to match however tall the table happens to be).
type connectionsListSplitLayout struct {
	editRowY      float32
	editRowHeight float32
	gap           float32
}

func (l *connectionsListSplitLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) < 2 {
		return fyne.NewSize(0, 0)
	}
	table := objects[0].MinSize()
	panel := objects[1].MinSize()
	height := table.Height
	// The panel can stick out past the table's own bottom when the edited
	// row is near the end of a short table -- make sure the container
	// reports enough height to actually show all of it.
	if panelBottom := l.editRowY + l.editRowHeight/2 + panel.Height/2; panelBottom > height {
		height = panelBottom
	}
	return fyne.NewSize(table.Width+l.gap+panel.Width, height)
}

func (l *connectionsListSplitLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 2 {
		return
	}
	table := objects[0]
	panel := objects[1]

	leftWidth := size.Width / 2
	tableHeight := table.MinSize().Height
	table.Move(fyne.NewPos(0, 0))
	table.Resize(fyne.NewSize(leftWidth, tableHeight))

	panelMin := panel.MinSize()
	rightWidth := size.Width - leftWidth - l.gap
	if rightWidth < panelMin.Width {
		rightWidth = panelMin.Width
	}

	panelY := l.editRowY + l.editRowHeight/2 - panelMin.Height/2
	if panelY < 0 {
		panelY = 0
	}
	if maxY := tableHeight - panelMin.Height; maxY < 0 {
		// Panel is taller than the whole table (e.g. one short connection
		// list) -- just pin it to the top rather than a negative position.
		panelY = 0
	} else if panelY > maxY {
		panelY = maxY
	}

	panel.Move(fyne.NewPos(leftWidth+l.gap, panelY))
	panel.Resize(fyne.NewSize(rightWidth, panelMin.Height))
}
