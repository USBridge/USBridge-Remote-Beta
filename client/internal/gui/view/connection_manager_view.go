package view

import (
	"image/color"
	"strings"
	"sync"
	"time"

	"usbridge-client/internal/gui/assets"
	"usbridge-client/internal/gui/design"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// appVersion is set once at startup (see SetAppVersion) so the connections
// screen can show a small build-version tag without importing package gui,
// which would create an import cycle (gui already imports view).
var appVersion string

// SetAppVersion records the running build's version string, shown as a small
// "vX.Y.Z" tag in the bottom-right corner of the connections screen.
func SetAppVersion(version string) {
	appVersion = strings.TrimSpace(version)
}

type ConnectionManagerUI struct {
	Container         *fyne.Container
	ConnectionsScroll *container.Scroll
	ConnectionsBox    *fyne.Container

	contentArea *fyne.Container
	topHelpBtn  fyne.CanvasObject

	headerActions connectionsHeaderActions
	headerButtons *connectionsHeaderButtons
	onHelp        func()
	onPromo       func()

	// viewMode/lastRows/lastCards/lastSummary/hasRows back the Grid/List
	// toggle: SetRows caches both renderings so flipping the toggle
	// (setViewMode) can swap ConnectionsBox's content without needing a
	// fresh call from the controller.
	viewMode    string
	lastRows    []*fyne.Container
	lastCards   []fyne.CanvasObject
	lastSummary ConnectionsSummary
	hasRows     bool
}

type ConnectionRowData struct {
	Name            string
	AddressSummary  string
	ProtocolBadge   string
	ProtocolOptions []string
	// HideProtocolSelector omits the AUTO/TS/LAN dropdown entirely (set by
	// the controller on wasm -- see connection_manager_ui.go's
	// createConnectionRow) instead of just disabling it: a browser tab has
	// no embedded tsnet to dial Tailscale with at all (same reasoning as
	// newConnectionHeader's own Tailscale-toggle omission on wasm, in package
	// gui), so every web connection is LAN-only regardless of what this
	// control shows -- leaving it visible but non-functional would just be a
	// dropdown users could fiddle with for no effect.
	HideProtocolSelector bool
	RegisterChecked      bool
	RegisterVisible      bool
	RemoteOS             string
}

type ConnectionRowState struct {
	Disabled bool
	Loading  bool
	// Editing puts a Grid-mode card (NewConnectionGridCard) into its inline
	// edit layout -- Name/LAN/TS/Token become entries, the protocol
	// picker/Connect button swap for Save/Delete icon buttons, and the
	// platform chip row hides to make room. List rows (NewConnectionRow)
	// ignore this field; List's pencil still opens the modal editor
	// (ConnectionManager.showEditDialog) regardless of it.
	Editing bool
}

type ConnectionRowActions struct {
	OnSelect         func()
	OnUse            func()
	OnEdit           func()
	OnProtocolChange func(string)
	OnRegisterChange func(bool)
}

const (
	promoImageAspectRatio       float32 = 1744.0 / 1317.0
	connectionCompactActionSize float32 = 30
	connectionCompactActionGap  float32 = 2
	connectionNameEditGap       float32 = 10
	connectionTitleEditGap      float32 = 4
	deviceControlGap            float32 = 10
)

type DeviceRowLayout struct {
	Gap float32
}

func (l *DeviceRowLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 3 {
		return
	}

	left := objects[0]
	center := objects[1]
	right := objects[2]

	leftSize := left.MinSize()
	rightSize := right.MinSize()

	leftY := (size.Height - leftSize.Height) / 2
	if leftY < 0 {
		leftY = 0
	}
	left.Move(fyne.NewPos(0, leftY))
	left.Resize(leftSize)

	rightY := (size.Height - rightSize.Height) / 2
	if rightY < 0 {
		rightY = 0
	}
	rightX := size.Width - rightSize.Width
	if rightX < leftSize.Width+l.Gap {
		rightX = leftSize.Width + l.Gap
	}
	right.Move(fyne.NewPos(rightX, rightY))
	right.Resize(rightSize)

	centerX := leftSize.Width + l.Gap
	centerWidth := rightX - centerX - l.Gap
	if centerWidth < 0 {
		centerWidth = 0
	}
	center.Move(fyne.NewPos(centerX, 0))
	center.Resize(fyne.NewSize(centerWidth, size.Height))
}

func (l *DeviceRowLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) < 3 {
		return fyne.NewSize(0, 0)
	}

	left := objects[0].MinSize()
	center := objects[1].MinSize()
	right := objects[2].MinSize()

	width := left.Width + center.Width + right.Width + (l.Gap * 2)
	height := left.Height
	if center.Height > height {
		height = center.Height
	}
	if right.Height > height {
		height = right.Height
	}
	return fyne.NewSize(width, height)
}

type DeviceRowControlsLayout struct {
	Gap float32
}

func (l *DeviceRowControlsLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	x := float32(0)
	for _, obj := range objects {
		if obj == nil || !obj.Visible() {
			continue
		}
		childSize := obj.MinSize()
		y := (size.Height - childSize.Height) / 2
		if y < 0 {
			y = 0
		}
		obj.Move(fyne.NewPos(x, y))
		obj.Resize(childSize)
		x += childSize.Width + l.Gap
	}
}

func (l *DeviceRowControlsLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	width := float32(0)
	height := float32(0)
	visibleCount := 0
	for _, obj := range objects {
		if obj == nil || !obj.Visible() {
			continue
		}
		childSize := obj.MinSize()
		width += childSize.Width
		if childSize.Height > height {
			height = childSize.Height
		}
		visibleCount++
	}
	if visibleCount > 1 {
		width += float32(visibleCount-1) * l.Gap
	}
	return fyne.NewSize(width, height)
}

type DeviceNameRowLayout struct {
	Gap float32
}

func (l *DeviceNameRowLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 3 {
		return
	}

	left := objects[0]
	center := objects[1]
	right := objects[2]

	leftWidth := float32(0)
	rightWidth := float32(0)
	if left.Visible() {
		leftSize := left.MinSize()
		leftWidth = leftSize.Width
		leftY := (size.Height-leftSize.Height)/2 + 2
		if leftY < 0 {
			leftY = 0
		}
		left.Move(fyne.NewPos(0, leftY))
		left.Resize(leftSize)
	}
	if right.Visible() {
		rightSize := right.MinSize()
		rightWidth = rightSize.Width
		right.Move(fyne.NewPos(size.Width-rightWidth, (size.Height-rightSize.Height)/2))
		right.Resize(rightSize)
	}

	centerX := float32(0)
	if left.Visible() {
		centerX += leftWidth + l.Gap
	}
	centerRight := size.Width
	if right.Visible() {
		centerRight -= rightWidth + l.Gap
	}
	centerWidth := centerRight - centerX
	if centerWidth < 0 {
		centerWidth = 0
	}
	center.Move(fyne.NewPos(centerX, 2))
	center.Resize(fyne.NewSize(centerWidth, size.Height))
}

func (l *DeviceNameRowLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) < 3 {
		return fyne.NewSize(0, 0)
	}

	left := objects[0]
	center := objects[1]
	right := objects[2]

	width := center.MinSize().Width
	height := center.MinSize().Height
	visibleSideCount := 0

	if left.Visible() {
		size := left.MinSize()
		width += size.Width
		if size.Height > height {
			height = size.Height
		}
		visibleSideCount++
	}
	if right.Visible() {
		size := right.MinSize()
		width += size.Width
		if size.Height > height {
			height = size.Height
		}
		visibleSideCount++
	}
	if visibleSideCount > 0 {
		width += l.Gap * float32(visibleSideCount)
	}
	return fyne.NewSize(width, height)
}

var connectionActionBlockedFill = design.ColorGray900

func NewConnectionManagerUI(onQR func(), onAdd func(), onHelp func(), onPromo func(), onPasteLink func()) *ConnectionManagerUI {
	connectionsBox := container.NewVBox()
	// Side margins match connectionsHeaderSideMargin (the section header
	// above) so List rows/Grid cards line up with the header's own edges
	// instead of hugging the window's raw edge.
	connectionsScroll := container.NewVScroll(NewInset(connectionsBox, connectionsHeaderSideMargin, connectionsHeaderSideMargin, 8, 12))
	connectionsScroll.SetMinSize(fyne.NewSize(0, 0))

	var topHelpBtn fyne.CanvasObject
	if onHelp != nil {
		topHelpBtn = NewFooterIconButton(
			assets.QuestionIconDim,
			assets.QuestionIcon,
			fyne.NewSize(13, 13),
			onHelp,
		)
	}
	contentArea := container.NewMax()

	bg := canvas.NewRectangle(design.ColorGray950)
	root := container.NewStack(bg, contentArea)
	if v := strings.TrimSpace(appVersion); v != "" {
		versionLabel := canvas.NewText("v"+v, design.ColorTextMuted)
		versionLabel.TextSize = 10
		versionLabel.Alignment = fyne.TextAlignTrailing
		versionCorner := container.NewBorder(nil, NewInset(container.NewHBox(layout.NewSpacer(), versionLabel), 0, 6, 0, 6), nil, nil, nil)
		root.Add(versionCorner)
	}

	ui := &ConnectionManagerUI{
		Container:         root,
		ConnectionsScroll: connectionsScroll,
		ConnectionsBox:    connectionsBox,
		contentArea:       contentArea,
		topHelpBtn:        topHelpBtn,
		viewMode:          "list",
		onHelp:            onHelp,
		onPromo:           onPromo,
	}
	ui.headerActions = connectionsHeaderActions{
		OnAdd:            onAdd,
		OnQR:             onQR,
		OnPasteLink:      onPasteLink,
		OnViewModeChange: ui.setViewMode,
	}
	ui.contentArea.Objects = []fyne.CanvasObject{
		layout.NewSpacer(),
	}

	return ui
}

// setViewMode is the Grid/List toggle's callback (connectionsHeaderActions.
// OnViewModeChange). It re-renders ConnectionsBox from the cached
// lastRows/lastCards -- no fresh data needed from the controller.
func (ui *ConnectionManagerUI) setViewMode(mode string) {
	if ui.viewMode == mode {
		return
	}
	ui.viewMode = mode
	if !ui.hasRows {
		return
	}
	ui.applyConnectionsContent()
}

// applyConnectionsContent rebuilds ConnectionsBox's children from the cached
// lastRows (list mode) or lastCards (grid mode), per the current viewMode.
func (ui *ConnectionManagerUI) applyConnectionsContent() {
	stopCanvasAnimations(ui.ConnectionsBox)
	ui.ConnectionsBox.RemoveAll()
	if ui.viewMode == "grid" && len(ui.lastCards) > 0 {
		// Each card sits inset by half the gap on every side, so adjacent
		// cards end up connectionCardGridGap apart without needing a custom
		// grid layout -- GridWrap itself has no configurable spacing.
		const gap = connectionCardGridGap
		padded := make([]fyne.CanvasObject, len(ui.lastCards))
		for i, card := range ui.lastCards {
			padded[i] = NewInset(card, gap/2, gap/2, gap/2, gap/2)
		}
		cellSize := fyne.NewSize(connectionCardWidth+gap, connectionCardHeight+gap)
		grid := container.NewGridWrap(cellSize, padded...)
		ui.ConnectionsBox.Add(grid)
	} else {
		for _, row := range ui.lastRows {
			ui.ConnectionsBox.Add(row)
		}
	}
	ui.ConnectionsBox.Refresh()
}

func (ui *ConnectionManagerUI) SetEmptyState() {
	stopCanvasAnimations(ui.ConnectionsBox)
	ui.ConnectionsBox.RemoveAll()
	ui.hasRows = false
	ui.lastRows = nil
	ui.lastCards = nil

	// No connections to count -- zero-valued ConnectionsSummary renders no
	// badges.
	header, buttons := newConnectionsHeader(ConnectionsSummary{}, ui.headerActions, ui.viewMode)
	ui.headerButtons = buttons

	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(1, 100))

	ui.contentArea.Objects = []fyne.CanvasObject{
		container.NewBorder(header, nil, nil, nil, spacer),
	}
	ui.contentArea.Refresh()
}

// NewEmptyStatePromoCard builds the "no saved connections" hardware promo
// card. Exported so other screens (e.g. Snapshots) can show the identical
// placeholder when their feature set doesn't apply to the connected agent.
func NewEmptyStatePromoCard(onLearnMore func()) fyne.CanvasObject {
	bgImage := canvas.NewImageFromResource(assets.OnboardingStep01)
	bgImage.FillMode = canvas.ImageFillContain

	bgFrame := canvas.NewRectangle(color.Transparent)
	bgFrame.SetMinSize(fyne.NewSize(1, 340))

	title := container.New(&emptyStatePromoTitleLayout{},
		NewBrandText("USBridge-KVM 2.0", 22, design.ColorTextLight, true),
		NewBrandText("", 22, design.ColorTextLight, true),
	)

	subtitle := widget.NewLabel("Hardware-grade security and remote management.")
	subtitle.Alignment = fyne.TextAlignCenter
	subtitle.Wrapping = fyne.TextWrapWord
	subtitleTheme := container.NewThemeOverride(subtitle, newForegroundOverrideTheme(design.NewBrandTheme(), design.ColorTextMuted))
	subtitle.TextStyle = fyne.TextStyle{}

	cta := NewConnectionPrimaryButton("Upgrade to Hardware", onLearnMore)
	cta.SetAccent(false)
	cta.SetPromoStyle(true)
	ctaWrap := container.NewCenter(cta)

	overlay := container.New(
		&emptyStatePromoOverlayLayout{},
		title,
		subtitleTheme,
		ctaWrap,
	)

	card := container.NewStack(
		container.New(&emptyStatePromoBackgroundLayout{maxWidth: 500, minHeight: 340, maxImageWidth: 470, maxImageHeight: 300}, bgFrame, bgImage),
		overlay,
	)

	return container.New(&emptyStatePromoCardLayout{maxWidth: 500, minHeight: 340}, card)
}

type emptyStatePromoCardLayout struct {
	maxWidth  float32
	minHeight float32
}

func (l *emptyStatePromoCardLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	child := objects[0]
	width := minFloat32(size.Width, l.maxWidth)
	if width < 0 {
		width = 0
	}
	height := maxFloat32(l.minHeight, child.MinSize().Height)
	if size.Height > height {
		if size.Width >= 720 {
			height = size.Height
		} else {
			extraHeight := float32(24)
			if size.Width > 420 {
				extraHeight += (size.Width - 420) * 0.28
			}
			maxHeight := height + extraHeight
			if maxHeight > size.Height {
				maxHeight = size.Height
			}
			height = maxHeight
		}
	}
	x := (size.Width - width) / 2
	if x < 0 {
		x = 0
	}
	child.Move(fyne.NewPos(x, 0))
	child.Resize(fyne.NewSize(width, height))
}

func (l *emptyStatePromoCardLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(160, l.minHeight)
}

type emptyStatePromoBackgroundLayout struct {
	maxWidth       float32
	minHeight      float32
	maxImageWidth  float32
	maxImageHeight float32
}

func (l *emptyStatePromoBackgroundLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 2 {
		return
	}
	frame := objects[0]
	image := objects[1]
	width := minFloat32(size.Width, l.maxWidth)
	if width < 0 {
		width = 0
	}
	height := maxFloat32(l.minHeight, size.Height)
	x := (size.Width - width) / 2
	if x < 0 {
		x = 0
	}
	frame.Move(fyne.NewPos(x, 0))
	frame.Resize(fyne.NewSize(width, height))

	imageWidth := width
	if l.maxImageWidth > 0 && imageWidth > l.maxImageWidth {
		imageWidth = l.maxImageWidth
	}
	imageHeight := imageWidth / promoImageAspectRatio
	if l.maxImageHeight > 0 && imageHeight > l.maxImageHeight {
		imageHeight = l.maxImageHeight
		imageWidth = imageHeight * promoImageAspectRatio
	}
	imageX := x + (width-imageWidth)/2
	imageY := (height - imageHeight) / 2
	if imageY < 0 {
		imageY = 0
	}
	image.Move(fyne.NewPos(imageX, imageY))
	image.Resize(fyne.NewSize(imageWidth, imageHeight))
}

func (l *emptyStatePromoBackgroundLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(1, l.minHeight)
}

type emptyStatePromoOverlayLayout struct{}

func (l *emptyStatePromoOverlayLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 3 {
		return
	}
	title := objects[0]
	subtitle := objects[1]
	cta := objects[2]

	if titleBox, ok := title.(*fyne.Container); ok && len(titleBox.Objects) >= 2 {
		line1, _ := titleBox.Objects[0].(*canvas.Text)
		line2, _ := titleBox.Objects[1].(*canvas.Text)
		if line1 != nil && line2 != nil {
			line1.Text = "USBridge-KVM 2.0"
			line2.Text = ""
			line1.Alignment = fyne.TextAlignCenter
			line2.Alignment = fyne.TextAlignCenter
			line1.Refresh()
			line2.Refresh()
			titleBox.Refresh()
		}
	}

	titleMin := title.MinSize()
	subtitleMin := subtitle.MinSize()
	ctaMin := cta.MinSize()

	topInset := float32(26)
	sideInset := float32(20)
	titleWidth := maxFloat32(0, size.Width-sideInset*2)
	titleHeight := titleMin.Height
	subtitleWidth := minFloat32(size.Width-sideInset*2, 420)
	if subtitleWidth < 120 {
		subtitleWidth = maxFloat32(0, size.Width-sideInset*2)
	}
	subtitleHeight := subtitleMin.Height
	titleY := topInset
	subtitleY := titleY + titleHeight - 6
	ctaY := size.Height - ctaMin.Height - 18

	title.Move(fyne.NewPos(sideInset, titleY))
	title.Resize(fyne.NewSize(titleWidth, titleHeight))

	subtitleX := (size.Width - subtitleWidth) / 2
	if subtitleX < sideInset {
		subtitleX = sideInset
	}
	maxSubtitleHeight := ctaY - 20 - subtitleY
	if subtitleHeight > maxSubtitleHeight {
		subtitleHeight = maxFloat32(0, maxSubtitleHeight)
	}
	subtitle.Move(fyne.NewPos(subtitleX, subtitleY))
	subtitle.Resize(fyne.NewSize(subtitleWidth, subtitleHeight))
	cta.Move(fyne.NewPos(0, ctaY))
	cta.Resize(fyne.NewSize(size.Width, ctaMin.Height))
}

func (l *emptyStatePromoOverlayLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	height := float32(340)
	return fyne.NewSize(1, height)
}

type emptyStatePromoTitleLayout struct{}

func (l *emptyStatePromoTitleLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	y := float32(0)
	for _, obj := range objects {
		min := obj.MinSize()
		if txt, ok := obj.(*canvas.Text); ok && txt.Text == "" {
			obj.Move(fyne.NewPos(0, y))
			obj.Resize(fyne.NewSize(size.Width, 0))
			continue
		}
		obj.Move(fyne.NewPos(0, y))
		obj.Resize(fyne.NewSize(size.Width, min.Height))
		y += min.Height
	}
}

func (l *emptyStatePromoTitleLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	width := float32(1)
	height := float32(0)
	for _, obj := range objects {
		if txt, ok := obj.(*canvas.Text); ok && txt.Text == "" {
			continue
		}
		min := obj.MinSize()
		if min.Width > width {
			width = min.Width
		}
		height += min.Height
	}
	return fyne.NewSize(width, height)
}

// SetRows renders the connections list. cards is the same connections
// rendered as Grid-mode cards (see NewConnectionGridCard) -- both are cached
// so the Grid/List toggle (setViewMode) can switch between them without a
// fresh call from the controller. cards may be nil/empty until the caller
// wires up grid-card construction; the toggle then just has nothing to show
// in grid mode yet.
func (ui *ConnectionManagerUI) SetRows(rows []*fyne.Container, cards []fyne.CanvasObject, summary ConnectionsSummary) {
	ui.lastRows = rows
	ui.lastCards = cards
	ui.lastSummary = summary
	ui.hasRows = true
	ui.applyConnectionsContent()

	header, buttons := newConnectionsHeader(summary, ui.headerActions, ui.viewMode)
	ui.headerButtons = buttons

	ui.contentArea.Objects = []fyne.CanvasObject{
		container.NewBorder(header, nil, nil, nil, ui.ConnectionsScroll),
	}
	ui.ConnectionsScroll.Refresh()
	ui.contentArea.Refresh()
}

func (ui *ConnectionManagerUI) SetActionButtonsDisabled(disabled bool) {
	ui.headerButtons.SetDisabled(disabled)
}

type foregroundOverrideTheme struct {
	base       fyne.Theme
	foreground color.Color
}

func newForegroundOverrideTheme(base fyne.Theme, foreground color.Color) fyne.Theme {
	return &foregroundOverrideTheme{
		base:       base,
		foreground: foreground,
	}
}

func (t *foregroundOverrideTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameForeground {
		return t.foreground
	}
	return t.base.Color(name, variant)
}

func (t *foregroundOverrideTheme) Font(style fyne.TextStyle) fyne.Resource {
	return t.base.Font(style)
}

func (t *foregroundOverrideTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return t.base.Icon(name)
}

func (t *foregroundOverrideTheme) Size(name fyne.ThemeSizeName) float32 {
	return t.base.Size(name)
}

var (
	_ fyne.Tappable     = (*iconChromeButton)(nil)
	_ desktop.Hoverable = (*iconChromeButton)(nil)
	_ fyne.Widget       = (*iconChromeButton)(nil)
	_ fyne.Tappable     = (*ConnectionPrimaryButton)(nil)
	_ desktop.Hoverable = (*ConnectionPrimaryButton)(nil)
	_ fyne.Widget       = (*ConnectionPrimaryButton)(nil)
)

func NewConnectionRow(data ConnectionRowData, state ConnectionRowState, actions ConnectionRowActions) *fyne.Container {
	nameBlock := newConnectionNameButton(data.Name, data.AddressSummary, data.RemoteOS, actions.OnEdit)
	nameBlock.SetDisabled(state.Disabled)

	useBtn := newConnectionActionIconButton(actions.OnUse)
	useBtn.SetDisabled(state.Disabled)
	useBtn.SetLoading(state.Loading)

	left := canvas.NewRectangle(color.Transparent)
	left.SetMinSize(fyne.NewSize(1, 1))
	center := container.New(&connectionCompactContentLayout{}, nameBlock)

	var rightItems []fyne.CanvasObject
	if !data.HideProtocolSelector {
		protocolBtn := NewHeaderDropdown(data.ProtocolOptions, data.ProtocolBadge, func(value string) {
			if actions.OnProtocolChange != nil {
				actions.OnProtocolChange(value)
			}
		})
		protocolBtn.Compact = true
		protocolBtn.SetSelected(data.ProtocolBadge)
		protocolBtn.SetDisabled(state.Disabled)
		rightItems = append(rightItems, protocolBtn)
	}
	rightItems = append(rightItems, useBtn)

	right := container.New(&DeviceRowControlsLayout{Gap: deviceControlGap}, rightItems...)
	row := container.New(&DeviceRowLayout{Gap: 6}, left, center, right)
	return newConnectionRowCard(row)
}

func newConnectionRowCard(content fyne.CanvasObject) *fyne.Container {
	card := NewCompactSurfacePanel(
		NewInset(content, 8, 8, 3, 3),
		design.ColorGray900,
		design.RadiusMD+1,
	)
	return NewInset(card, 0, 0, 0, 1)
}

func osIconResource(os string) fyne.Resource {
	normalized := strings.ToLower(strings.TrimSpace(os))
	switch {
	case strings.Contains(normalized, "usbridge"):
		return assets.USBridgeOSIcon
	case strings.Contains(normalized, "linux"):
		return assets.LinuxOSIcon
	case strings.Contains(normalized, "windows"):
		return assets.WindowsOSIcon
	case strings.Contains(normalized, "darwin"), strings.Contains(normalized, "mac"):
		return assets.MacOSIcon
	default:
		return nil
	}
}

type connectionNameButton struct {
	widget.BaseWidget

	title    string
	subtitle string
	remoteOS string
	onTapped func()
	disabled bool
	hovered  bool

	bg       *canvas.Rectangle
	titleTxt *adaptiveNameText
	subTxt   *fyne.Container
	subLines []*canvas.Text
	osIcon   *canvas.Image
	icon     *canvas.Image
}

func newConnectionNameButton(title, subtitle, remoteOS string, onTapped func()) *connectionNameButton {
	b := &connectionNameButton{
		title:    title,
		subtitle: subtitle,
		remoteOS: remoteOS,
		onTapped: onTapped,
	}
	b.ExtendBaseWidget(b)
	return b
}

func (b *connectionNameButton) SetDisabled(disabled bool) {
	b.disabled = disabled
	if disabled {
		b.hovered = false
	}
	b.refreshVisuals()
}

func (b *connectionNameButton) Tapped(*fyne.PointEvent) {
	if b.disabled || b.onTapped == nil {
		return
	}
	b.onTapped()
}

func (b *connectionNameButton) TappedSecondary(*fyne.PointEvent) {}

func (b *connectionNameButton) MouseIn(*desktop.MouseEvent) {
	if b.disabled {
		return
	}
	b.hovered = true
	b.refreshVisuals()
}

func (b *connectionNameButton) MouseMoved(*desktop.MouseEvent) {}

func (b *connectionNameButton) MouseOut() {
	if !b.hovered {
		return
	}
	b.hovered = false
	b.refreshVisuals()
}

func (b *connectionNameButton) MinSize() fyne.Size {
	title := fyne.MeasureText("Conn...ion", 14, fyne.TextStyle{Bold: true})
	subWidth := float32(0)
	subLines := strings.Split(b.subtitle, "\n")
	for _, line := range subLines {
		size := fyne.MeasureText(line, 9, fyne.TextStyle{})
		if size.Width > subWidth {
			subWidth = size.Width
		}
	}
	lineHeight := fyne.MeasureText("TS: 100.100.100.100", 9, fyne.TextStyle{}).Height
	subLineCount := len(subLines)
	if subLineCount < 1 {
		subLineCount = 1
	}
	subHeight := lineHeight * float32(subLineCount)
	titleWidth := title.Width + 13 + connectionTitleEditGap
	width := maxFloat32(titleWidth, subWidth+b.subtitleIconOffset()) + 20
	height := title.Height + subHeight + 12
	return fyne.NewSize(width, height)
}

func (b *connectionNameButton) preferredWidth() float32 {
	title := fyne.MeasureText(b.title, 14, fyne.TextStyle{Bold: true})
	subWidth := float32(0)
	for _, line := range strings.Split(b.subtitle, "\n") {
		size := fyne.MeasureText(line, 9, fyne.TextStyle{})
		if size.Width > subWidth {
			subWidth = size.Width
		}
	}
	titleWidth := title.Width + 13 + connectionTitleEditGap
	return maxFloat32(titleWidth, subWidth+b.subtitleIconOffset()) + 20
}

func (b *connectionNameButton) subtitleHeight() float32 {
	subLineCount := len(strings.Split(b.subtitle, "\n"))
	if subLineCount < 1 {
		subLineCount = 1
	}
	lineHeight := fyne.MeasureText("TS: 100.100.100.100", 9, fyne.TextStyle{}).Height
	gap := float32(2)
	extraGaps := 0
	if subLineCount > 1 {
		extraGaps = subLineCount - 1
	}
	return lineHeight*float32(subLineCount) + gap*float32(extraGaps)
}

func (b *connectionNameButton) subtitleIconOffset() float32 {
	if osIconResource(b.remoteOS) == nil {
		return 0
	}
	return 28
}

func (b *connectionNameButton) rebuildSubtitle() {
	lines := strings.Split(b.subtitle, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}

	objects := make([]fyne.CanvasObject, 0, len(lines))
	b.subLines = make([]*canvas.Text, 0, len(lines))
	for _, line := range lines {
		txt := canvas.NewText(line, design.ColorTextMuted)
		txt.TextSize = 9
		txt.Alignment = fyne.TextAlignLeading
		b.subLines = append(b.subLines, txt)
		objects = append(objects, txt)
	}

	if b.subTxt == nil {
		b.subTxt = container.New(&connectionSubtitleLayout{gap: 2}, objects...)
		return
	}
	b.subTxt.Objects = objects
	b.subTxt.Refresh()
}

func (b *connectionNameButton) CreateRenderer() fyne.WidgetRenderer {
	b.bg = canvas.NewRectangle(color.Transparent)
	b.bg.CornerRadius = design.RadiusMD

	b.titleTxt = newAdaptiveNameText()
	b.titleTxt.textSize = 14
	b.titleTxt.style = fyne.TextStyle{Bold: true}
	b.titleTxt.SetColor(design.ColorTextLight)
	b.titleTxt.SetText(b.title)
	b.rebuildSubtitle()
	b.osIcon = canvas.NewImageFromResource(nil)
	b.osIcon.FillMode = canvas.ImageFillContain
	b.osIcon.SetMinSize(fyne.NewSize(18, 18))
	b.icon = canvas.NewImageFromResource(assets.ConnectionEditIconMuted)
	b.icon.FillMode = canvas.ImageFillContain
	b.icon.SetMinSize(fyne.NewSize(13, 13))

	r := &connectionNameButtonRenderer{
		button:  b,
		objects: []fyne.CanvasObject{b.bg, b.titleTxt, b.subTxt, b.osIcon, b.icon},
	}
	r.Refresh()
	return r
}

func (b *connectionNameButton) refreshVisuals() {
	if b.bg == nil || b.titleTxt == nil || b.subTxt == nil || b.osIcon == nil || b.icon == nil {
		return
	}

	b.bg.FillColor = color.Transparent
	b.titleTxt.SetText(b.title)
	b.titleTxt.SetColor(design.ColorTextLight)
	b.rebuildSubtitle()
	if res := osIconResource(b.remoteOS); res != nil {
		b.osIcon.Resource = res
		b.osIcon.Show()
		b.osIcon.Translucency = 0
	} else {
		b.osIcon.Hide()
	}
	b.icon.Translucency = 0

	if b.disabled {
		b.titleTxt.SetColor(design.ColorBorder)
		if b.osIcon.Visible() {
			b.osIcon.Translucency = 0.35
		}
		b.icon.Translucency = 0.35
	}
	subColor := design.ColorTextMuted
	if b.disabled {
		subColor = design.ColorBorder
	}
	for _, line := range b.subLines {
		line.Color = subColor
		line.Refresh()
	}
	if !b.disabled && b.hovered {
		b.bg.FillColor = design.ColorSurfaceLight
	}

	b.bg.Refresh()
	b.subTxt.Refresh()
	b.osIcon.Refresh()
	b.icon.Refresh()
}

type connectionNameButtonRenderer struct {
	button  *connectionNameButton
	objects []fyne.CanvasObject
}

func (r *connectionNameButtonRenderer) Layout(size fyne.Size) {
	r.button.bg.Resize(size)

	iconSize := fyne.NewSize(13, 13)
	titleX := float32(8)
	titleY := float32(3)
	titleAvailableWidth := maxFloat32(0, size.Width-16)
	titleWidth := maxFloat32(0, titleAvailableWidth-iconSize.Width-connectionTitleEditGap)
	r.button.titleTxt.Move(fyne.NewPos(8, 3))
	r.button.titleTxt.Resize(fyne.NewSize(titleWidth, r.button.titleTxt.MinSize().Height))

	measuredTitleWidth := fyne.MeasureText(r.button.title, 14, fyne.TextStyle{Bold: true}).Width
	visibleTitleWidth := minFloat32(measuredTitleWidth, titleWidth)
	editX := titleX + visibleTitleWidth + connectionTitleEditGap
	maxEditX := maxFloat32(titleX, size.Width-iconSize.Width-8)
	if editX > maxEditX {
		editX = maxEditX
	}
	r.button.icon.Resize(iconSize)
	r.button.icon.Move(fyne.NewPos(editX, titleY+1))

	subY := float32(24)
	subX := float32(8)
	if r.button.osIcon.Visible() {
		osIconSize := fyne.NewSize(18, 18)
		iconY := subY + maxFloat32(0, (r.button.subtitleHeight()-osIconSize.Height)/2)
		r.button.osIcon.Resize(osIconSize)
		r.button.osIcon.Move(fyne.NewPos(8, iconY))
		subX += r.button.subtitleIconOffset()
	}
	r.button.subTxt.Move(fyne.NewPos(subX, subY))
	r.button.subTxt.Resize(fyne.NewSize(maxFloat32(0, size.Width-subX-8), r.button.subtitleHeight()))
}

func (r *connectionNameButtonRenderer) MinSize() fyne.Size {
	return r.button.MinSize()
}

func (r *connectionNameButtonRenderer) Refresh() {
	r.button.refreshVisuals()
	r.Layout(r.button.Size())
}

func (r *connectionNameButtonRenderer) BackgroundColor() color.Color {
	return color.Transparent
}

func (r *connectionNameButtonRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *connectionNameButtonRenderer) Destroy() {}

type connectionSubtitleLayout struct {
	gap float32
}

func (l *connectionSubtitleLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for _, obj := range objects {
		min := obj.MinSize()
		obj.Move(fyne.NewPos(0, y))
		obj.Resize(fyne.NewSize(size.Width, min.Height))
		y += min.Height + l.gap
	}
}

func (l *connectionSubtitleLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) == 0 {
		return fyne.NewSize(0, 0)
	}
	width := float32(0)
	height := float32(0)
	for i, obj := range objects {
		min := obj.MinSize()
		if min.Width > width {
			width = min.Width
		}
		height += min.Height
		if i < len(objects)-1 {
			height += l.gap
		}
	}
	return fyne.NewSize(width, height)
}

type connectionCompactContentLayout struct{}

func (l *connectionCompactContentLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	child := objects[0]
	min := child.MinSize()
	width := min.Width
	if btn, ok := child.(*connectionNameButton); ok {
		width = btn.preferredWidth()
	}
	width = minFloat32(size.Width, width)
	if width < 0 {
		width = 0
	}
	y := (size.Height - min.Height) / 2
	if y < 0 {
		y = 0
	}
	child.Move(fyne.NewPos(0, y))
	child.Resize(fyne.NewSize(width, minFloat32(size.Height, maxFloat32(min.Height, size.Height))))
}

func (l *connectionCompactContentLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) == 0 {
		return fyne.NewSize(0, 0)
	}
	return objects[0].MinSize()
}

type connectionNameLayout struct{}

func newConnectionNameLayout() fyne.Layout {
	return &connectionNameLayout{}
}

func (l *connectionNameLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 2 {
		return
	}

	textBlock := objects[0]
	editBtn := objects[1]
	editMin := editBtn.MinSize()
	textMin := textBlock.MinSize()
	textWidth := minFloat32(textMin.Width, size.Width-editMin.Width-connectionNameEditGap)
	if textWidth < 0 {
		textWidth = 0
	}

	textBlock.Move(fyne.NewPos(0, 0))
	textBlock.Resize(fyne.NewSize(textWidth, size.Height))

	editY := float32(0)
	if size.Height > editMin.Height {
		editY = maxFloat32(0, (size.Height-editMin.Height)/2-8)
	}
	editBtn.Move(fyne.NewPos(textWidth+connectionNameEditGap, editY))
	editBtn.Resize(editMin)
}

func (l *connectionNameLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) < 2 {
		return fyne.NewSize(0, 0)
	}

	textMin := objects[0].MinSize()
	editMin := objects[1].MinSize()
	return fyne.NewSize(textMin.Width+connectionNameEditGap+editMin.Width, maxFloat32(textMin.Height, editMin.Height))
}

func newConnectionSquareIconButton(icon fyne.Resource, onTapped func(), disabled bool) fyne.CanvasObject {
	return newIconChromeButton(iconChromeButtonSpec{
		Disabled:     disabled,
		DisabledFill: connectionActionBlockedFill,
		NormalFill:   design.ColorGray900,
		HoverFill:    design.ColorSurfaceLight,
		Stroke:       design.ColorBorder,
		StrokeWidth:  1,
		NormalIcon:   icon,
		HoverIcon:    icon,
		DisabledIcon: icon,
		IconSize:     fyne.NewSize(18, 18),
		ButtonSize:   fyne.NewSize(40, 40),
		OnTapped:     onTapped,
	})
}

func newConnectionInlineIconButton(icon fyne.Resource, onTapped func(), disabled bool) fyne.CanvasObject {
	return newIconChromeButton(iconChromeButtonSpec{
		Disabled:     disabled,
		NormalFill:   color.Transparent,
		HoverFill:    design.ColorSurfaceLight,
		Stroke:       color.Transparent,
		StrokeWidth:  0,
		NormalIcon:   icon,
		HoverIcon:    icon,
		DisabledIcon: theme.NewDisabledResource(icon),
		IconSize:     fyne.NewSize(15, 15),
		ButtonSize:   fyne.NewSize(connectionCompactActionSize, connectionCompactActionSize),
		OnTapped:     onTapped,
	})
}

// SpinnerAnimator drives the small looping icon-swap animation shared by
// every button-like widget that shows a loading spinner -- the ones in this
// file (connection use button, primary button, icon-chrome button) and the
// Tailscale header toggle in package gui (connection_header.go). Embed it
// and call Start/Stop instead of hand-rolling a ticker goroutine per widget.
// Exported so it can be reused outside this package.
type SpinnerAnimator struct {
	mu   sync.Mutex
	stop chan struct{}
	step int
}

// Start begins looping through frames at a fixed interval, invoking onFrame
// (on the Fyne UI goroutine) for each frame, starting immediately with frame
// 0. Calling Start again while already running replaces the animation and
// restarts at frame 0; use IsRunning to avoid that when a caller wants
// repeated Start calls to be a no-op instead.
func (s *SpinnerAnimator) Start(frames []fyne.Resource, onFrame func(fyne.Resource)) {
	if len(frames) == 0 || onFrame == nil {
		return
	}
	s.Stop()

	stop := make(chan struct{})
	s.mu.Lock()
	s.stop = stop
	s.step = 0
	s.mu.Unlock()

	onFrame(frames[0])

	go func() {
		ticker := time.NewTicker(140 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				fyne.Do(func() {
					s.mu.Lock()
					active := s.stop == stop
					if active {
						s.step = (s.step + 1) % len(frames)
					}
					step := s.step
					s.mu.Unlock()
					if !active {
						return
					}
					onFrame(frames[step])
				})
			case <-stop:
				return
			}
		}
	}()
}

// Stop ends any running animation. Safe to call when nothing is running.
func (s *SpinnerAnimator) Stop() {
	s.mu.Lock()
	stop := s.stop
	s.stop = nil
	s.mu.Unlock()

	if stop != nil {
		close(stop)
	}
}

// IsRunning reports whether an animation is currently looping.
func (s *SpinnerAnimator) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stop != nil
}

type connectionActionIconButton struct {
	widget.BaseWidget

	onTapped func()
	disabled bool
	loading  bool
	hovered  bool
	bg       *canvas.Rectangle
	icon     *canvas.Image
	anim     SpinnerAnimator
}

func newConnectionActionIconButton(onTapped func()) *connectionActionIconButton {
	btn := &connectionActionIconButton{onTapped: onTapped}
	btn.ExtendBaseWidget(btn)
	return btn
}

func (b *connectionActionIconButton) SetDisabled(disabled bool) {
	b.disabled = disabled
	if disabled {
		b.hovered = false
	}
	b.refreshVisuals()
}

func (b *connectionActionIconButton) SetLoading(loading bool) {
	b.loading = loading
	if loading {
		b.hovered = false
	}
	b.refreshVisuals()
}

func (b *connectionActionIconButton) Tapped(*fyne.PointEvent) {
	if b.disabled || b.loading || b.onTapped == nil {
		return
	}
	b.onTapped()
}

func (b *connectionActionIconButton) TappedSecondary(*fyne.PointEvent) {}

func (b *connectionActionIconButton) MouseIn(*desktop.MouseEvent) {
	if b.disabled || b.loading {
		return
	}
	b.hovered = true
	b.refreshVisuals()
}

func (b *connectionActionIconButton) MouseMoved(*desktop.MouseEvent) {}

func (b *connectionActionIconButton) MouseOut() {
	if !b.hovered {
		return
	}
	b.hovered = false
	b.refreshVisuals()
}

func (b *connectionActionIconButton) MinSize() fyne.Size {
	return fyne.NewSize(deviceControlUnitWidth, deviceControlHeight)
}

func (b *connectionActionIconButton) CreateRenderer() fyne.WidgetRenderer {
	b.bg = canvas.NewRectangle(design.ColorSurfaceLight)
	b.bg.CornerRadius = design.RadiusMD

	b.icon = canvas.NewImageFromResource(assets.ConnectIcon)
	b.icon.FillMode = canvas.ImageFillContain
	b.icon.SetMinSize(fyne.NewSize(18, 18))

	b.refreshVisuals()
	return widget.NewSimpleRenderer(container.NewMax(b.bg, container.NewCenter(b.icon)))
}

func (b *connectionActionIconButton) refreshVisuals() {
	if b.bg == nil || b.icon == nil {
		return
	}

	fill := design.ColorSurfaceLight
	var resource fyne.Resource = assets.ConnectIcon
	translucency := float64(0)

	switch {
	case b.loading:
		resource = assets.LoadingGrayFrames[0]
	case b.disabled:
		fill = connectionActionBlockedFill
		resource = assets.ConnectIconMuted
		translucency = 0.18
	case b.hovered:
		fill = design.ColorBorder
	}

	b.bg.FillColor = fill
	b.bg.Refresh()
	b.icon.Resource = resource
	b.icon.Translucency = translucency
	b.icon.Refresh()

	if b.loading {
		b.anim.Start(assets.LoadingGrayFrames, func(frame fyne.Resource) {
			if b.icon == nil {
				return
			}
			b.icon.Resource = frame
			b.icon.Refresh()
		})
		return
	}
	b.anim.Stop()
}

func (b *connectionActionIconButton) StopAnimations() {
	b.anim.Stop()
}

type transparentTapOverlay struct {
	widget.BaseWidget

	onTapped func()
	disabled bool
}

func newTransparentTapOverlay(onTapped func()) *transparentTapOverlay {
	overlay := &transparentTapOverlay{onTapped: onTapped}
	overlay.ExtendBaseWidget(overlay)
	return overlay
}

func (o *transparentTapOverlay) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

func (o *transparentTapOverlay) Tapped(*fyne.PointEvent) {
	if o.disabled || o.onTapped == nil {
		return
	}

	o.onTapped()
}

func (o *transparentTapOverlay) TappedSecondary(*fyne.PointEvent) {}

func (o *transparentTapOverlay) SetDisabled(disabled bool) {
	o.disabled = disabled
}

type animationStopper interface {
	StopAnimations()
}

func stopCanvasAnimations(obj fyne.CanvasObject) {
	if obj == nil {
		return
	}

	if stopper, ok := obj.(animationStopper); ok {
		stopper.StopAnimations()
	}

	if containerObj, ok := obj.(*fyne.Container); ok {
		for _, child := range containerObj.Objects {
			stopCanvasAnimations(child)
		}
	}
}

type ConnectionPrimaryButton struct {
	widget.BaseWidget

	labelText string
	onTapped  func()
	accent    bool
	promo     bool
	disabled  bool
	loading   bool
	hovered   bool

	bg    *canvas.Rectangle
	label *canvas.Text
	icon  *canvas.Image
	anim  SpinnerAnimator
}

func NewConnectionPrimaryButton(label string, onTapped func()) *ConnectionPrimaryButton {
	btn := &ConnectionPrimaryButton{
		labelText: label,
		onTapped:  onTapped,
		accent:    true,
	}
	btn.ExtendBaseWidget(btn)
	return btn
}

func (b *ConnectionPrimaryButton) SetDisabled(disabled bool) {
	b.disabled = disabled
	if disabled {
		b.hovered = false
	}
	b.refreshVisuals()
}

func (b *ConnectionPrimaryButton) SetLoading(loading bool) {
	b.loading = loading
	if loading {
		b.hovered = false
	}
	b.refreshVisuals()
}

func (b *ConnectionPrimaryButton) SetLabel(label string) {
	b.labelText = label
	if b.label != nil {
		b.label.Text = label
		b.label.Refresh()
	}
	b.Refresh()
}

func (b *ConnectionPrimaryButton) SetAccent(accent bool) {
	b.accent = accent
	b.refreshVisuals()
}

func (b *ConnectionPrimaryButton) SetPromoStyle(promo bool) {
	b.promo = promo
	b.refreshVisuals()
}

func (b *ConnectionPrimaryButton) Tapped(*fyne.PointEvent) {
	if b.disabled || b.loading || b.onTapped == nil {
		return
	}

	b.onTapped()
}

func (b *ConnectionPrimaryButton) TappedSecondary(*fyne.PointEvent) {}

func (b *ConnectionPrimaryButton) MouseIn(*desktop.MouseEvent) {
	if b.disabled || b.loading {
		return
	}

	b.hovered = true
	b.refreshVisuals()
}

func (b *ConnectionPrimaryButton) MouseMoved(*desktop.MouseEvent) {}

func (b *ConnectionPrimaryButton) MouseOut() {
	if !b.hovered {
		return
	}

	b.hovered = false
	b.refreshVisuals()
}

func (b *ConnectionPrimaryButton) MinSize() fyne.Size {
	measure := canvas.NewText(b.labelText, design.ColorBackground)
	measure.TextSize = 14
	measure.TextStyle.Bold = true
	labelSize := measure.MinSize()
	width := labelSize.Width + 28
	if width < 104 {
		width = 104
	}
	return fyne.NewSize(width, 40)
}

func (b *ConnectionPrimaryButton) CreateRenderer() fyne.WidgetRenderer {
	b.bg = canvas.NewRectangle(design.ColorAccent)
	b.bg.CornerRadius = design.RadiusMD
	b.bg.StrokeColor = color.Transparent
	b.bg.StrokeWidth = 0

	b.label = canvas.NewText(b.labelText, design.ColorBackground)
	b.label.TextSize = 14
	b.label.TextStyle.Bold = true
	b.label.Alignment = fyne.TextAlignCenter

	b.icon = canvas.NewImageFromResource(nil)
	b.icon.FillMode = canvas.ImageFillContain
	b.icon.SetMinSize(fyne.NewSize(18, 18))

	b.refreshVisuals()
	return widget.NewSimpleRenderer(container.NewMax(b.bg, container.NewCenter(container.NewStack(b.icon, b.label))))
}

func (b *ConnectionPrimaryButton) refreshVisuals() {
	if b.bg == nil || b.label == nil || b.icon == nil {
		return
	}

	fill := design.ColorAccent
	fillHover := design.ColorAccentHover
	labelColor := design.ColorBackground
	if !b.accent {
		fill = design.ColorSurfaceLight
		fillHover = design.ColorBorder
		labelColor = design.ColorTextLight
	}
	if b.promo {
		fill = design.ColorAlphaWhite07
		fillHover = design.ColorAlphaWhite12
		labelColor = design.ColorTextLight
	}
	if b.loading {
		labelColor = design.ColorBackground
	} else if b.disabled {
		fill = connectionActionBlockedFill
		labelColor = design.ColorBorder
	} else if b.hovered {
		fill = fillHover
	}

	b.bg.FillColor = fill
	b.bg.StrokeColor = color.Transparent
	b.bg.StrokeWidth = 0
	b.bg.Refresh()

	b.label.Color = labelColor
	b.label.Refresh()

	if b.loading {
		b.label.Hide()
		b.icon.Show()
		b.anim.Start(assets.LoadingGrayFrames, func(frame fyne.Resource) {
			if b.icon == nil {
				return
			}
			b.icon.Resource = frame
			b.icon.Refresh()
		})
		return
	}

	b.anim.Stop()
	b.icon.Hide()
	b.label.Show()
}

func (b *ConnectionPrimaryButton) StopAnimations() {
	b.anim.Stop()
}

type iconChromeButtonSpec struct {
	Disabled     bool
	DisabledFill color.Color
	NormalFill   color.Color
	HoverFill    color.Color
	Stroke       color.Color
	StrokeWidth  float32
	NormalIcon   fyne.Resource
	HoverIcon    fyne.Resource
	DisabledIcon fyne.Resource
	IconSize     fyne.Size
	ButtonSize   fyne.Size
	OnTapped     func()
	// LabelColor overrides the text color used when SetText gives this
	// button a label instead of an icon. Defaults to design.ColorTextLight
	// (every existing caller's behavior) when left nil.
	LabelColor color.Color
	// LabelBold makes SetText's label bold. false (the zero value) is the
	// only case in use right now (the connections section header's Add
	// button) -- flip to true per spec if some future caller wants it back.
	LabelBold bool
	// CornerRadius overrides design.RadiusMD for this button's background.
	// 0 (the zero value) means "use the default" -- there's no way to
	// request literally-0/square corners through this field, only a
	// smaller-than-default rounding.
	CornerRadius float32
	OnHover      func(bool)
	LabelSize    float32
}

type iconChromeButton struct {
	widget.BaseWidget

	spec    iconChromeButtonSpec
	hovered bool
	loading bool
	bg      *canvas.Rectangle
	border  *canvas.Rectangle
	icon    *canvas.Image
	label   *canvas.Text
	text    string
	anim    SpinnerAnimator
}

func newIconChromeButton(spec iconChromeButtonSpec) *iconChromeButton {
	btn := &iconChromeButton{spec: spec}
	btn.ExtendBaseWidget(btn)
	return btn
}

func (b *iconChromeButton) CreateRenderer() fyne.WidgetRenderer {
	b.bg = canvas.NewRectangle(b.spec.NormalFill)
	b.bg.CornerRadius = design.RadiusMD
	if b.spec.CornerRadius > 0 {
		b.bg.CornerRadius = b.spec.CornerRadius
	}

	b.border = canvas.NewRectangle(color.Transparent)
	b.border.CornerRadius = design.RadiusMD
	b.border.StrokeColor = b.spec.Stroke
	b.border.StrokeWidth = b.spec.StrokeWidth

	b.icon = canvas.NewImageFromResource(b.spec.NormalIcon)
	b.icon.FillMode = canvas.ImageFillContain
	b.icon.SetMinSize(b.spec.IconSize)

	b.label = canvas.NewText("", design.ColorTextLight)
	b.label.TextSize = 12
	if b.spec.LabelSize > 0 {
		b.label.TextSize = b.spec.LabelSize
	}
	b.label.TextStyle = fyne.TextStyle{Bold: b.spec.LabelBold}
	b.label.Alignment = fyne.TextAlignCenter

	b.refreshVisuals()
	// DeviceRowControlsLayout (not NewMax's stacked centering) so an icon and
	// a label can sit side by side when both are visible -- e.g. the
	// connections section header's Grid/List toggle. It already skips
	// hidden children, so icon-only/label-only callers are unaffected.
	iconLabelRow := container.New(&DeviceRowControlsLayout{Gap: iconChromeButtonIconLabelGap}, b.icon, b.label)
	return widget.NewSimpleRenderer(container.NewMax(b.bg, container.NewCenter(iconLabelRow), b.border))
}

// iconChromeButtonIconLabelGap is the gap between icon and label for a
// button showing both (see CreateRenderer/MinSize) -- kept tight, since the
// Grid/List toggle is the only caller so far and wants a dense look.
const iconChromeButtonIconLabelGap float32 = 4

func (b *iconChromeButton) MinSize() fyne.Size {
	if b.spec.ButtonSize.Width > 0 && b.spec.ButtonSize.Height > 0 {
		return b.spec.ButtonSize
	}
	if b.text != "" {
		// Text-labeled button with only a height given (spec.ButtonSize.Width
		// <= 0, e.g. the connections section header's Add/Grid/List
		// buttons): size the width to the label (plus the icon and its gap,
		// if this one shows both -- see CreateRenderer's iconLabelRow)
		// instead of falling through to the generic icon-button default
		// below.
		measure := canvas.NewText(b.text, design.ColorTextLight)
		measure.TextSize = 12
		if b.spec.LabelSize > 0 {
			measure.TextSize = b.spec.LabelSize
		}
		measure.TextStyle = fyne.TextStyle{Bold: b.spec.LabelBold}
		width := measure.MinSize().Width + 18
		if b.spec.NormalIcon != nil {
			width += b.spec.IconSize.Width + iconChromeButtonIconLabelGap
		}
		height := b.spec.ButtonSize.Height
		if height <= 0 {
			height = 48
		}
		return fyne.NewSize(width, height)
	}
	return fyne.NewSize(48, 48)
}

func (b *iconChromeButton) SetLoading(loading bool) {
	b.loading = loading
	if loading {
		b.hovered = false
	}
	b.refreshVisuals()
}

func (b *iconChromeButton) StopAnimations() {
	b.anim.Stop()
}

func (b *iconChromeButton) Tapped(*fyne.PointEvent) {
	if b.spec.Disabled || b.loading {
		return
	}

	if b.spec.OnTapped != nil {
		b.spec.OnTapped()
	}
}

func (b *iconChromeButton) TappedSecondary(*fyne.PointEvent) {}

func (b *iconChromeButton) SetDisabled(disabled bool) {
	b.spec.Disabled = disabled
	b.hovered = false
	b.refreshVisuals()
}

func (b *iconChromeButton) SetOnTapped(onTapped func()) {
	b.spec.OnTapped = onTapped
}

func (b *iconChromeButton) SetIcons(normalIcon fyne.Resource, hoverIcon fyne.Resource, disabledIcon fyne.Resource) {
	b.spec.NormalIcon = normalIcon
	b.spec.HoverIcon = hoverIcon
	b.spec.DisabledIcon = disabledIcon
	b.refreshVisuals()
}

func (b *iconChromeButton) SetText(text string) {
	b.text = text
	b.refreshVisuals()
}

// SetLabelColor changes the text color set by SetText after construction --
// e.g. the connections section header's Grid/List toggle recoloring
// whichever side is "active" on tap.
func (b *iconChromeButton) SetLabelColor(labelColor color.Color) {
	b.spec.LabelColor = labelColor
	b.refreshVisuals()
}

func (b *iconChromeButton) MouseIn(*desktop.MouseEvent) {
	if b.spec.Disabled || b.loading {
		return
	}

	b.hovered = true
	b.refreshVisuals()
	if b.spec.OnHover != nil {
		b.spec.OnHover(true)
	}
}

func (b *iconChromeButton) MouseMoved(*desktop.MouseEvent) {}

func (b *iconChromeButton) MouseOut() {
	if b.spec.Disabled {
		return
	}

	b.hovered = false
	b.refreshVisuals()
	if b.spec.OnHover != nil {
		b.spec.OnHover(false)
	}
}

func (b *iconChromeButton) refreshVisuals() {
	if b.bg == nil || b.border == nil || b.icon == nil || b.label == nil {
		return
	}

	b.bg.FillColor = b.spec.NormalFill
	b.icon.Resource = b.spec.NormalIcon
	b.icon.Translucency = 0
	b.label.Text = b.text
	b.label.Color = design.ColorTextLight
	if b.spec.LabelColor != nil {
		b.label.Color = b.spec.LabelColor
	}

	switch {
	case b.loading:
		if len(assets.LoadingGrayFrames) > 0 {
			b.icon.Resource = assets.LoadingGrayFrames[0]
		}
	case b.spec.Disabled:
		if b.spec.DisabledFill != nil {
			b.bg.FillColor = b.spec.DisabledFill
		}
		if b.spec.DisabledIcon != nil {
			b.icon.Resource = b.spec.DisabledIcon
		}
		b.icon.Translucency = 0.18
		b.label.Color = design.ColorTextMuted
	case b.hovered:
		b.bg.FillColor = b.spec.HoverFill
		if b.spec.HoverIcon != nil {
			b.icon.Resource = b.spec.HoverIcon
		}
	}

	if b.loading {
		b.anim.Start(assets.LoadingGrayFrames, func(frame fyne.Resource) {
			if b.icon == nil {
				return
			}
			b.icon.Resource = frame
			b.icon.Refresh()
		})
	} else {
		b.anim.Stop()
	}

	// Independent, not either/or: the Grid/List toggle shows an icon next to
	// its label (via CreateRenderer's iconLabelRow), while every other
	// caller so far only ever sets one of the two.
	if b.spec.NormalIcon != nil {
		b.icon.Show()
	} else {
		b.icon.Hide()
	}
	if b.text != "" {
		b.label.Show()
	} else {
		b.label.Hide()
	}

	b.bg.Refresh()
	b.border.Refresh()
	b.icon.Refresh()
	b.label.Refresh()
}

func NewFooterIconButton(normalIcon fyne.Resource, hoverIcon fyne.Resource, iconSize fyne.Size, onTapped func()) fyne.CanvasObject {
	return newIconChromeButton(iconChromeButtonSpec{
		NormalFill: color.Transparent,
		HoverFill:  design.ColorSurfaceLight,
		NormalIcon: normalIcon,
		HoverIcon:  hoverIcon,
		IconSize:   iconSize,
		ButtonSize: fyne.NewSize(28, 28),
		OnTapped:   onTapped,
	})
}

func minFloat32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func maxFloat32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func clampFloat32(value, minValue, maxValue float32) float32 {
	if maxValue < minValue {
		maxValue = minValue
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

// ForceMobileDesign was a dev-only switch for previewing the mobile design
// on a desktop OS. Nothing reads it any more -- this screen's last
// isMobile-conditional branches (in SetEmptyState/SetRows/
// NewConnectionManagerUI) were removed when the connections section header
// (newConnectionsHeader) replaced them with one unconditional layout.
// Left in place, exported, rather than deleted outright: flag before
// removing, since another in-progress branch may still reference it.
var ForceMobileDesign = false
