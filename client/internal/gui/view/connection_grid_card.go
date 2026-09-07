package view

// connection_grid_card.go -- the Grid-mode connection card (see the
// connections section header's Grid/List toggle), modeled on a reference
// screenshot: dot+name+edit on top, a platform/capability chip row, a dark
// LAN/TS box, a divider, and a bottom row of protocol picker + Connect
// button.
//
// First pass -- colors/sizes are placeholders pending review, same as every
// other "let's sketch it" first draft this screen has gone through so far.
// A few fields from the original reference (an online/health-check status
// pill, a console/terminal quick-access button) were cut before this ever
// shipped -- see git history on this file if either comes back later.

import (
	"image/color"
	"strings"
	"time"

	"usbridge-client/internal/gui/assets"
	"usbridge-client/internal/gui/design"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ConnectionCardData is what one grid card needs to render. RemoteOS reuses
// the same value/classification ConnectionRowData.RemoteOS and
// ClassifyConnectionRemoteOS already use -- KVM vs Agent isn't a separate
// concept here, it only changes the accent color (KVM: ColorAccent, Agent:
// ColorConnectionBadgeText -- both placeholders pending review).
type ConnectionCardData struct {
	Name     string
	RemoteOS string

	// PlatformLabel/CapabilityText: the chip row under the title
	// ("Radxa" * some capability note in the reference). Neither has a real
	// data source yet -- PlatformLabel falls back to the literal "Radxa"
	// (the only platform that exists right now) when empty; CapabilityText
	// just hides that half of the row when empty, nothing to guess at there.
	PlatformLabel  string
	CapabilityText string

	LANAddress       string
	TailscaleAddress string
	// MasterKey backs the inline "Token" field shown while the card is in
	// its edit layout (ConnectionRowState.Editing) -- read-only otherwise.
	MasterKey string

	ProtocolBadge   string
	ProtocolOptions []string
}

// ConnectionCardActions are the events a grid card can report.
type ConnectionCardActions struct {
	OnSelect         func()
	OnEdit           func()
	OnUse            func()
	OnProtocolChange func(string)
	// OnSave commits the inline-edited Name/LAN/TS/Token fields (only wired
	// up while ConnectionRowState.Editing is set -- see the Save icon
	// button in the card's edit layout). Same shape as the modal editor's
	// onSave (connectionDialogSpec.onSave) minus the Tailscale-register
	// toggle, which the compact card has no room for.
	OnSave func(name, lanAddress, tailscaleAddress, masterKey string)
	// OnDelete removes this connection (Editing mode only) -- the card's
	// Delete icon button, wired to the same confirm-then-delete flow the
	// modal editor's Delete button uses.
	OnDelete func()
	// OnCancel exits the edit layout without saving (Editing mode only) --
	// the card's "X" icon button, the only way out of Editing besides
	// Save/Delete.
	OnCancel func()
}

// connectionCardWidth/Height are the card's target size -- "roughly square,
// 3-4 per screen row" per the brief, though the reference screenshot's
// cards actually read closer to a 6:5 rectangle than a true square. The
// caller arranges N of these in a container.NewGridWrap(fyne.NewSize(
// connectionCardWidth, connectionCardHeight), ...) (or similar) to actually
// get that many per row.
const (
	connectionCardWidth  float32 = 280
	connectionCardHeight float32 = 205
	// connectionCardGridGap is the empty space ConnectionManagerUI.
	// applyConnectionsContent leaves between adjacent cards (and between a
	// card and the grid's own edge) -- GridWrap itself has no configurable
	// spacing, so each card gets inset by half of this on every side.
	connectionCardGridGap float32 = 16
)

// NewConnectionGridCard builds one Grid-mode connection card.
func NewConnectionGridCard(data ConnectionCardData, state ConnectionRowState, actions ConnectionCardActions) fyne.CanvasObject {
	isAgent, isKVM := ClassifyConnectionRemoteOS(data.RemoteOS)
	accent := design.ColorAccent // KVM and the unclassified fallback
	if isAgent {
		accent = design.ColorConnectionBadgeText
	}

	statusIndicator := newConnectionCardStatusIndicator(data.RemoteOS)
	typeBadge := newConnectionTypeBadge(isAgent, isKVM, accent)
	editing := state.Editing

	// connectColor/connectHover are the Connect button's lime fill in the
	// normal layout -- reused as-is for the Save button in the edit layout
	// (see below) so the two "commit" actions read as the same affordance.
	connectColor := color.NRGBA{R: 0xc4, G: 0xe7, B: 0x7a, A: 0xff}
	connectHover := color.NRGBA{R: 0xd4, G: 0xf7, B: 0x8a, A: 0xff}

	var topRow fyne.CanvasObject
	var editBtn *iconChromeButton
	var nameEntry *widget.Entry
	if editing {
		nameEntry = widget.NewEntry()
		nameEntry.SetPlaceHolder("Name")
		nameEntry.SetText(strings.TrimSpace(data.Name))
		nameEntry.TextStyle = fyne.TextStyle{Bold: true}
		// Border layout so the entry stretches to fill whatever room is
		// left between the fixed-size status dot and type badge, instead
		// of sitting at its own small MinSize the way DeviceRowControlsLayout
		// (the non-editing leftTopControls' layout) would leave it.
		topRow = container.NewBorder(nil, nil, statusIndicator, typeBadge, wrapGridCardEntry(nameEntry, 12))
	} else {
		nameText := NewBrandText(strings.TrimSpace(data.Name), 12, design.ColorTextLight, true)

		editIcon := fyne.NewStaticResource("connection-edit-title.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#c5c8b5"><path d="M3 17.25V21h3.75L17.81 9.94l-3.75-3.75L3 17.25zm2.92 2.33H5v-.92l9.06-9.06.92.92L5.92 19.58zM20.71 7.04a1.003 1.003 0 0 0 0-1.42L18.37 3.29a1.003 1.003 0 0 0-1.42 0l-1.13 1.13 3.75 3.75 1.14-1.13z"/></svg>`))
		editBtn = newIconChromeButton(iconChromeButtonSpec{
			NormalFill: color.Transparent,
			HoverFill:  design.ColorSurfaceLight,
			Stroke:     color.Transparent,
			NormalIcon: editIcon,
			IconSize:   fyne.NewSize(11, 11),
			ButtonSize: fyne.NewSize(20, 20),
			OnTapped:   actions.OnEdit,
		})

		leftTopControls := container.New(&DeviceRowControlsLayout{Gap: 8}, statusIndicator, nameText, editBtn)
		topRow = container.NewBorder(nil, nil, leftTopControls, typeBadge)
	}

	// chipsRow (the "Radxa"/"Opensource" platform plaque) hides in the edit
	// layout -- there's nothing to edit there, and the freed space is what
	// makes room for the Token field the edit box grows by (see
	// newConnectionCardEditableStatsBox) without the card changing size.
	var chipsRow fyne.CanvasObject
	if !editing {
		platformLabel := strings.TrimSpace(data.PlatformLabel)
		if platformLabel == "" {
			switch {
			case isKVM:
				// Only known KVM platform right now.
				platformLabel = "Radxa"
			case isAgent:
				// Agent variant (Opensource vs Pro) isn't reported yet --
				// show both until that distinction actually exists.
				platformLabel = "Opensource/Pro"
			default:
				// No RemoteOS yet -- this connection has never successfully
				// connected, so there's nothing real to classify.
				platformLabel = "Awaiting connection..."
			}
		}
		chipsRow = NewInset(newConnectionCardChipsRow(platformLabel, strings.TrimSpace(data.CapabilityText), accent), 0, 0, 4, 8)
	}

	var statsBox fyne.CanvasObject
	var lanEntry, tailscaleEntry, tokenEntry *widget.Entry
	if editing {
		statsBox, lanEntry, tailscaleEntry, tokenEntry = newConnectionCardEditableStatsBox(data.LANAddress, data.TailscaleAddress, data.MasterKey)
	} else {
		statsBox = newConnectionCardStatsBox(data.LANAddress, data.TailscaleAddress)
	}

	dividerColor := color.NRGBA{R: 0x29, G: 0x2d, B: 0x27, A: 0xff}
	dividerLine := canvas.NewRectangle(dividerColor)
	dividerLine.SetMinSize(fyne.NewSize(1, 1))
	divider := NewInset(dividerLine, 0, 0, 4, 4)

	var bottomRow fyne.CanvasObject
	var protocolDropdown *HeaderDropdown
	var connectBtn *iconChromeButton
	var saveBtn, deleteBtn, cancelBtn *iconChromeButton
	if editing {
		// Protocol picker and Connect hide while editing (see
		// ConnectionCardActions.OnSave/OnDelete's doc comment) -- Delete,
		// Save and Cancel take their place as a small right-aligned icon
		// row (in that order) instead of full-width buttons, since that's
		// all this action needs.
		deleteIcon := fyne.NewStaticResource("connection-delete.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#c5c8b5"><path d="M6 19c0 1.1.9 2 2 2h8c1.1 0 2-.9 2-2V7H6v12zM19 4h-3.5l-1-1h-5l-1 1H5v2h14V4z"/></svg>`))
		deleteBtn = newIconChromeButton(iconChromeButtonSpec{
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
		deleteBtn.SetDisabled(state.Disabled)

		saveIcon := fyne.NewStaticResource("connection-save.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#4c6803"><path d="M9 16.2 4.8 12l-1.4 1.4L9 19 21 7l-1.4-1.4z"/></svg>`))
		saveBtn = newIconChromeButton(iconChromeButtonSpec{
			NormalFill:   connectColor,
			HoverFill:    connectHover,
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
		saveBtn.SetDisabled(state.Disabled)

		cancelIcon := fyne.NewStaticResource("connection-cancel-edit.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#c5c8b5"><path d="M18.3 5.71 12 12.01l-6.3-6.3-1.41 1.41 6.3 6.3-6.3 6.3 1.41 1.41 6.3-6.3 6.3 6.3 1.41-1.41-6.3-6.3 6.3-6.3z"/></svg>`))
		cancelBtn = newIconChromeButton(iconChromeButtonSpec{
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
		cancelBtn.SetDisabled(state.Disabled)

		bottomRow = container.NewBorder(nil, nil, nil, container.New(&DeviceRowControlsLayout{Gap: 8}, deleteBtn, saveBtn, cancelBtn))
	} else {
		protocolDropdown = NewHeaderDropdown(data.ProtocolOptions, data.ProtocolBadge, actions.OnProtocolChange)
		protocolDropdown.UltraCompact = true
		protocolDropdown.CornerRadius = 6
		protocolDropdown.BorderColor = design.ColorTailscaleChipBorder
		protocolDropdown.TextColor = design.ColorConnectionBadgeText
		protocolDropdown.IconColor = color.NRGBA{R: 0xc5, G: 0xc8, B: 0xb5, A: 0xff}
		protocolDropdown.TextSize = 10
		protocolDropdown.HoverBorderColor = design.ColorConnectionBadgeText
		protocolDropdown.HoverFillColor = design.ColorGray900
		protocolDropdown.SetSelected(data.ProtocolBadge)
		protocolDropdown.SetDisabled(state.Disabled)

		connectIconColored := strings.ReplaceAll(string(assets.ConnectIconBoldBlack.Content()), "#111111", "#4c6803")
		connectIconResource := fyne.NewStaticResource("custom-connect.svg", []byte(connectIconColored))

		connectBtn = newIconChromeButton(iconChromeButtonSpec{
			NormalFill:   connectColor,
			HoverFill:    connectHover,
			DisabledFill: connectionActionBlockedFill,
			Stroke:       color.Transparent,
			LabelColor:   color.NRGBA{R: 0x4c, G: 0x68, B: 0x03, A: 0xff},
			LabelBold:    true,
			CornerRadius: 6,
			NormalIcon:   connectIconResource,
			IconSize:     fyne.NewSize(14, 14),
			ButtonSize:   fyne.NewSize(0, 26),
			OnTapped:     actions.OnUse,
		})
		connectBtn.SetText("Connect")
		connectBtn.SetDisabled(state.Disabled)
		connectBtn.SetLoading(state.Loading)

		bottomRow = container.New(&gridBottomRowLayout{}, protocolDropdown, connectBtn)
	}

	children := []fyne.CanvasObject{topRow}
	if chipsRow != nil {
		children = append(children, chipsRow)
	}
	children = append(children, statsBox, divider, bottomRow)
	content := NewInset(container.NewVBox(children...), 12, 12, 12, 8)

	cardBg := canvas.NewRectangle(design.ColorGray900)
	cardBg.CornerRadius = design.RadiusLG
	cardBg.StrokeColor = design.ColorTailscaleChipBorder
	cardBg.StrokeWidth = 1
	cardBg.SetMinSize(fyne.NewSize(connectionCardWidth, connectionCardHeight))

	card := container.NewStack(cardBg, content)

	var hoverTimer *time.Timer
	setCardHovered := func(hovered bool) {
		if hovered {
			if hoverTimer != nil {
				hoverTimer.Stop()
				hoverTimer = nil
			}
			cardBg.StrokeColor = design.ColorConnectionBadgeText
			cardBg.Refresh()
		} else {
			if hoverTimer != nil {
				hoverTimer.Stop()
			}
			hoverTimer = time.AfterFunc(50*time.Millisecond, func() {
				cardBg.StrokeColor = design.ColorTailscaleChipBorder
				cardBg.Refresh()
			})
		}
	}

	if editing {
		saveBtn.spec.OnHover = setCardHovered
		deleteBtn.spec.OnHover = setCardHovered
		cancelBtn.spec.OnHover = setCardHovered
	} else {
		protocolDropdown.OnHover = setCardHovered
		connectBtn.spec.OnHover = setCardHovered
		editBtn.spec.OnHover = setCardHovered
	}

	// While editing, a tap on the card's own background shouldn't also
	// fill the main connect form (OnSelect) -- the user is mid-edit, not
	// picking a connection.
	var overlayOnSelect func()
	if !editing {
		overlayOnSelect = actions.OnSelect
	}
	overlay := newConnectionCardOverlay(overlayOnSelect, setCardHovered)

	return container.NewStack(overlay, card)
}

// connectionCardOverlay is the grid card's invisible top layer: reports taps
// (OnSelect) and hover (used to swap the card's border to the brand teal --
// see NewConnectionGridCard). A dedicated type rather than reusing
// transparentTapOverlay since that one has no hover support and is used
// nowhere else that would benefit from gaining it.
type connectionCardOverlay struct {
	widget.BaseWidget

	onTapped func()
	onHover  func(hovered bool)
}

func newConnectionCardOverlay(onTapped func(), onHover func(hovered bool)) *connectionCardOverlay {
	o := &connectionCardOverlay{onTapped: onTapped, onHover: onHover}
	o.ExtendBaseWidget(o)
	return o
}

func (o *connectionCardOverlay) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

func (o *connectionCardOverlay) Tapped(*fyne.PointEvent) {
	if o.onTapped != nil {
		o.onTapped()
	}
}

func (o *connectionCardOverlay) TappedSecondary(*fyne.PointEvent) {}

func (o *connectionCardOverlay) MouseIn(*desktop.MouseEvent) {
	if o.onHover != nil {
		o.onHover(true)
	}
}

func (o *connectionCardOverlay) MouseMoved(*desktop.MouseEvent) {}

func (o *connectionCardOverlay) MouseOut() {
	if o.onHover != nil {
		o.onHover(false)
	}
}

var (
	_ fyne.Tappable     = (*connectionCardOverlay)(nil)
	_ desktop.Hoverable = (*connectionCardOverlay)(nil)
	_ fyne.Widget       = (*connectionCardOverlay)(nil)
)

// newConnectionCardStatusIndicator is the small mark to the left of the
// card's title: the same per-connection icon the List row shows (same OS/KVM
// classification as osIconResource), but colored by category instead of
// List's neutral gray -- KVM in accent's "salad" green, a known Agent OS in
// the teal Agent color -- or a plain gray dot when RemoteOS is still empty
// (no successful connect yet, so nothing to classify).
func newConnectionCardStatusIndicator(remoteOS string) fyne.CanvasObject {
	const size = float32(16)
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
	dotWrap := container.NewGridWrap(fyne.NewSize(8, 8), dot)
	return container.NewGridWrap(fyne.NewSize(size, size), container.NewCenter(dotWrap))
}

// agentOSIconResource picks the Agent-colored OS glyph (assets.LinuxOSIconAgent
// et al) for a known agent RemoteOS -- same substring matching as
// osIconResource, just the teal-tinted variants instead of List's gray ones.
func agentOSIconResource(remoteOS string) fyne.Resource {
	normalized := strings.ToLower(strings.TrimSpace(remoteOS))
	switch {
	case strings.Contains(normalized, "linux"):
		return assets.LinuxOSIconAgent
	case strings.Contains(normalized, "windows"):
		return assets.WindowsOSIconAgent
	case strings.Contains(normalized, "darwin"), strings.Contains(normalized, "mac"):
		return assets.MacOSIconAgent
	default:
		return nil
	}
}

func newConnectionCardChipsRow(platformLabel, capabilityText string, accent color.Color) fyne.CanvasObject {
	items := []fyne.CanvasObject{newConnectionPlatformChip(platformLabel)}
	if capabilityText != "" {
		bullet := canvas.NewText("•", design.ColorTailscaleChipBorder)
		bullet.TextSize = 8
		capText := canvas.NewText(capabilityText, accent)
		capText.TextSize = 8
		items = append(items, bullet, capText)
	}
	return container.New(&DeviceRowControlsLayout{Gap: 8}, items...)
}

type tightPlatformChipLayout struct{}

func (l *tightPlatformChipLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) < 2 {
		return fyne.NewSize(0, 0)
	}
	labelSize := objects[1].MinSize()
	return fyne.NewSize(labelSize.Width+12, 14) // hardcode tight 14px height
}

func (l *tightPlatformChipLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 2 {
		return
	}
	objects[0].Resize(size)
	objects[0].Move(fyne.NewPos(0, 0))

	labelSize := objects[1].MinSize()
	// Visually center the text vertically, ignoring its large bounding box
	objects[1].Resize(labelSize)
	objects[1].Move(fyne.NewPos(6, (size.Height-labelSize.Height)/2))
}

func newConnectionPlatformChip(text string) fyne.CanvasObject {
	c5c8b5Color := color.NRGBA{R: 0xc5, G: 0xc8, B: 0xb5, A: 0xff}
	label := canvas.NewText(text, c5c8b5Color)
	label.TextSize = 8
	label.TextStyle.Monospace = true

	bg := canvas.NewRectangle(color.Transparent)
	bg.CornerRadius = 3
	bg.StrokeColor = design.ColorTailscaleChipBorder
	bg.StrokeWidth = 1

	chip := container.New(&tightPlatformChipLayout{}, bg, label)
	return container.NewCenter(chip)
}

// newConnectionCardStatsBox is the dark LAN/TS readout.
func newConnectionCardStatsBox(lanAddress, tailscaleAddress string) fyne.CanvasObject {
	tsValueColor := color.NRGBA{R: 0xeb, G: 0xff, B: 0xbc, A: 0xff}
	lanRow := newConnectionStatRow("LAN", connectionCardAddressOrNone(lanAddress), design.ColorTextLight)
	tsRow := newConnectionStatRow("TS", connectionCardAddressOrNone(tailscaleAddress), tsValueColor)

	dividerColor := color.NRGBA{R: 0x29, G: 0x2d, B: 0x27, A: 0xff}
	sep := canvas.NewRectangle(dividerColor)
	sep.SetMinSize(fyne.NewSize(1, 1))

	rows := []fyne.CanvasObject{lanRow, sep, tsRow}

	bg := canvas.NewRectangle(design.ColorGray950)
	bg.CornerRadius = 6
	bg.StrokeColor = design.ColorTailscaleChipBorder
	bg.StrokeWidth = 1

	return container.NewStack(bg, NewInset(container.NewVBox(rows...), 12, 12, 8, 8))
}

// gridCardFieldTheme strips a widget.Entry's own input chrome (background,
// border, focus ring) and shrinks its text to textSize, so an inline-editing
// entry blends into the card's dark surface instead of looking like a
// standalone form field. Used for every entry in the card's edit layout
// (ConnectionRowState.Editing) -- the Name entry (topRow) and the LAN/TS/
// Token entries (newConnectionCardEditableStatsBox).
type gridCardFieldTheme struct {
	fyne.Theme
	textSize float32
}

func (t *gridCardFieldTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameInputBackground, theme.ColorNameInputBorder, theme.ColorNameFocus, theme.ColorNameShadow:
		return color.Transparent
	case theme.ColorNameForeground:
		return design.ColorTextLight
	case theme.ColorNamePlaceHolder:
		return design.ColorTextMuted
	case theme.ColorNameSelection:
		return design.ColorSurfaceLight
	}
	return t.Theme.Color(name, variant)
}

func (t *gridCardFieldTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameText:
		return t.textSize
	case theme.SizeNameInputBorder:
		return 0
	case theme.SizeNamePadding:
		return 2
	}
	return t.Theme.Size(name)
}

// wrapGridCardEntry applies gridCardFieldTheme to entry via a ThemeOverride,
// same trick controller.noInputBgTheme uses for the modal editor's fields,
// just local to this package (view can't import controller).
func wrapGridCardEntry(entry *widget.Entry, textSize float32) fyne.CanvasObject {
	return container.NewThemeOverride(entry, &gridCardFieldTheme{Theme: design.NewBrandTheme(), textSize: textSize})
}

// newConnectionCardEditableStatsBox is newConnectionCardStatsBox's edit-mode
// counterpart: the same dark LAN/TS box, its two read-only rows swapped for
// entries, plus a third Token row (the connection's master key) that the
// hidden chipsRow (see NewConnectionGridCard) makes room for. Returns the
// three entries so the caller's Save button can read their live values.
func newConnectionCardEditableStatsBox(lanAddress, tailscaleAddress, masterKey string) (box fyne.CanvasObject, lanEntry, tailscaleEntry, tokenEntry *widget.Entry) {
	lanEntry = newConnectionCardFieldEntry(lanAddress, "LAN address")
	tailscaleEntry = newConnectionCardFieldEntry(tailscaleAddress, "Tailscale address")
	tokenEntry = newConnectionCardFieldEntry(masterKey, "Token")

	lanRow := newConnectionStatEditRow("LAN", lanEntry)
	tsRow := newConnectionStatEditRow("TS", tailscaleEntry)
	tokenRow := newConnectionStatEditRow("Token", tokenEntry)

	dividerColor := color.NRGBA{R: 0x29, G: 0x2d, B: 0x27, A: 0xff}
	sep1 := canvas.NewRectangle(dividerColor)
	sep1.SetMinSize(fyne.NewSize(1, 1))
	sep2 := canvas.NewRectangle(dividerColor)
	sep2.SetMinSize(fyne.NewSize(1, 1))

	rows := []fyne.CanvasObject{lanRow, sep1, tsRow, sep2, tokenRow}

	bg := canvas.NewRectangle(design.ColorGray950)
	bg.CornerRadius = 6
	bg.StrokeColor = design.ColorTailscaleChipBorder
	bg.StrokeWidth = 1

	box = container.NewStack(bg, NewInset(container.NewVBox(rows...), 12, 12, 8, 8))
	return box, lanEntry, tailscaleEntry, tokenEntry
}

func newConnectionCardFieldEntry(value, placeholder string) *widget.Entry {
	entry := widget.NewEntry()
	entry.SetPlaceHolder(placeholder)
	entry.SetText(strings.TrimSpace(value))
	return entry
}

// newConnectionStatEditRow mirrors newConnectionStatRow's [label | value]
// shape with an editable entry standing in for the value text.
func newConnectionStatEditRow(label string, entry *widget.Entry) fyne.CanvasObject {
	c5c8b5Color := color.NRGBA{R: 0xc5, G: 0xc8, B: 0xb5, A: 0xff}
	labelText := canvas.NewText(label, c5c8b5Color)
	labelText.TextSize = 10
	labelText.TextStyle.Monospace = true

	return container.NewBorder(nil, nil, NewInset(labelText, 0, 8, 0, 0), nil, wrapGridCardEntry(entry, 10))
}

func newConnectionStatRow(label, value string, valueColor color.Color) fyne.CanvasObject {
	c5c8b5Color := color.NRGBA{R: 0xc5, G: 0xc8, B: 0xb5, A: 0xff}
	labelText := canvas.NewText(label, c5c8b5Color)
	labelText.TextSize = 10
	labelText.TextStyle.Monospace = true

	valueText := canvas.NewText(value, valueColor)
	valueText.TextSize = 10
	valueText.TextStyle.Monospace = true
	valueText.Alignment = fyne.TextAlignTrailing

	return container.NewBorder(nil, nil, labelText, nil, valueText)
}

func connectionCardAddressOrNone(address string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return "none"
	}
	return address
}

type gridBottomRowLayout struct{}

func (l *gridBottomRowLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) < 2 {
		return fyne.NewSize(0, 0)
	}
	return fyne.NewSize(objects[0].MinSize().Width+12+objects[1].MinSize().Width, 28)
}

func (l *gridBottomRowLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 2 {
		return
	}
	left := objects[0]
	right := objects[1]

	leftW := left.MinSize().Width
	left.Resize(fyne.NewSize(leftW, size.Height))
	left.Move(fyne.NewPos(0, 0))

	rightW := size.Width - leftW - 12
	if rightW < 0 {
		rightW = 0
	}
	right.Resize(fyne.NewSize(rightW, size.Height))
	right.Move(fyne.NewPos(leftW+12, 0))
}

type typeBadgeLayout struct{}

func (l *typeBadgeLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) < 3 {
		return fyne.NewSize(0, 0)
	}
	labelSize := objects[2].MinSize()
	return fyne.NewSize(labelSize.Width+20, 18) // 18px height, 20px extra width for padding
}

func (l *typeBadgeLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 3 {
		return
	}
	// bg
	objects[0].Resize(size)
	objects[0].Move(fyne.NewPos(0, 0))

	// dot
	dotSize := fyne.NewSize(4, 4)
	objects[1].Resize(dotSize)
	objects[1].Move(fyne.NewPos(6, (size.Height-dotSize.Height)/2))

	// label
	labelSize := objects[2].MinSize()
	objects[2].Resize(labelSize)
	objects[2].Move(fyne.NewPos(14, (size.Height-labelSize.Height)/2))
}

// newConnectionTypeBadge is the small "KVM"/"Agent"/"Unknown" chip in the
// card's top-right corner. isAgent/isKVM is the same ClassifyConnectionRemoteOS
// result the rest of the card already uses; accent is the caller's already-
// computed Agent/KVM color (design.ColorConnectionBadgeText for an Agent),
// passed in rather than recomputed so this stays in sync with the platform
// chip/Connect button's own coloring.
func newConnectionTypeBadge(isAgent, isKVM bool, accent color.Color) fyne.CanvasObject {
	text := "Unknown"
	badgeColor := color.Color(design.ColorBorder) // gray -- no RemoteOS yet
	switch {
	case isKVM:
		text = "KVM"
		badgeColor = design.ColorConnectionAddFill // same lime as the Add button
	case isAgent:
		text = "Agent"
		badgeColor = accent
	}

	dot := canvas.NewCircle(badgeColor)

	label := canvas.NewText(text, badgeColor)
	label.TextSize = 9
	label.TextStyle.Monospace = true

	bg := canvas.NewRectangle(color.Transparent)
	bg.CornerRadius = 3
	bg.StrokeColor = design.ColorTailscaleChipBorder
	bg.StrokeWidth = 1

	chip := container.New(&typeBadgeLayout{}, bg, dot, label)
	return container.NewCenter(chip)
}
