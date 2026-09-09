package view

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"usbridge-client/internal/gui/design"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type dropdownMenuTheme struct {
	base fyne.Theme
}

func (t *dropdownMenuTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameShadow:
		return color.Transparent
	default:
		return t.base.Color(name, variant)
	}
}

func (t *dropdownMenuTheme) Font(style fyne.TextStyle) fyne.Resource {
	return t.base.Font(style)
}

func (t *dropdownMenuTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return t.base.Icon(name)
}

func (t *dropdownMenuTheme) Size(name fyne.ThemeSizeName) float32 {
	return t.base.Size(name)
}

type HeaderDropdown struct {
	widget.BaseWidget

	Options          []string
	Selected         string
	OnSelected       func(string)
	MinWidth         float32
	Compact          bool
	UltraCompact     bool
	BorderColor      color.Color
	CornerRadius     float32
	TextColor        color.Color
	TextSize         float32
	HoverBorderColor color.Color
	HoverFillColor   color.Color
	IconColor        color.Color
	OnHover          func(bool)

	disabled bool
	hovered  bool
	opened   bool
	hidden   bool

	popup *dropdownPopup

	bg          *canvas.Rectangle
	border      *canvas.Rectangle
	label       *canvas.Text
	icon        *canvas.Image
	iconOpening bool
}

func NewHeaderDropdown(options []string, selected string, onSelected func(string)) *HeaderDropdown {
	d := &HeaderDropdown{
		Options:          append([]string(nil), options...),
		Selected:         selected,
		OnSelected:       onSelected,
		BorderColor:      design.ColorBorder,
		CornerRadius:     design.RadiusMD,
		TextColor:        design.ColorTextLight,
		TextSize:         14,
		HoverBorderColor: design.ColorBorder,
		HoverFillColor:   design.ColorSurfaceLight,
	}
	d.updateMinWidth()
	d.ExtendBaseWidget(d)
	return d
}

func (d *HeaderDropdown) CreateRenderer() fyne.WidgetRenderer {
	d.bg = canvas.NewRectangle(design.ColorSurface)
	d.bg.CornerRadius = d.CornerRadius

	d.border = canvas.NewRectangle(color.Transparent)
	d.border.CornerRadius = d.CornerRadius
	d.border.StrokeColor = d.BorderColor
	d.border.StrokeWidth = 1

	d.label = canvas.NewText(d.Selected, d.TextColor)
	d.label.TextSize = d.TextSize
	if d.UltraCompact {
		d.label.TextStyle.Monospace = true
	}

	var res fyne.Resource = coloredArrowDown(d.IconColor)
	d.icon = canvas.NewImageFromResource(res)
	d.icon.FillMode = canvas.ImageFillContain
	d.icon.SetMinSize(fyne.NewSize(16, 16))

	content := container.NewWithoutLayout(d.bg, d.border, d.label, d.icon)
	r := &headerDropdownRenderer{
		dropdown: d,
		objects:  []fyne.CanvasObject{content},
	}
	r.Refresh()
	return r
}

func (d *HeaderDropdown) MinSize() fyne.Size {
	height := d.controlHeight()
	if d.MinWidth > 0 {
		return fyne.NewSize(d.MinWidth, height)
	}
	label := canvas.NewText(d.Selected, d.TextColor)
	label.TextSize = d.TextSize
	if d.UltraCompact {
		label.TextStyle.Monospace = true
	}
	width := label.MinSize().Width + d.horizontalPadding()
	minWidth := d.minimumWidth()
	if width < minWidth {
		width = minWidth
	}
	return fyne.NewSize(width, height)
}

func (d *HeaderDropdown) Tapped(*fyne.PointEvent) {
	if d.disabled {
		return
	}
	if d.popup != nil && d.popup.Visible() {
		d.closePopup()
		return
	}
	d.openPopup()
}

func (d *HeaderDropdown) TappedSecondary(*fyne.PointEvent) {}

func (d *HeaderDropdown) MouseIn(*desktop.MouseEvent) {
	d.hovered = true
	d.refreshVisuals()
	if d.OnHover != nil {
		d.OnHover(true)
	}
}

func (d *HeaderDropdown) MouseMoved(*desktop.MouseEvent) {}

func (d *HeaderDropdown) MouseOut() {
	d.hovered = false
	d.refreshVisuals()
	if d.OnHover != nil {
		d.OnHover(false)
	}
}

func (d *HeaderDropdown) SetOptions(options []string) {
	d.Options = append([]string(nil), options...)
	d.updateMinWidth()
	d.Refresh()
}

func (d *HeaderDropdown) SetSelected(value string) {
	d.Selected = value
	d.updateMinWidth()
	d.Refresh()
}

func (d *HeaderDropdown) updateMinWidth() {
	longest := strings.TrimSpace(d.Selected)
	for _, option := range d.Options {
		option = strings.TrimSpace(option)
		if len(option) > len(longest) {
			longest = option
		}
	}

	label := canvas.NewText(longest, d.TextColor)
	label.TextSize = d.TextSize
	if d.UltraCompact {
		label.TextStyle.Monospace = true
	}
	width := label.MinSize().Width + d.horizontalPadding()
	if width < d.minimumWidth() {
		width = d.minimumWidth()
	}
	d.MinWidth = width
}

func (d *HeaderDropdown) SetDisabled(disabled bool) {
	d.disabled = disabled
	if disabled {
		d.closePopup()
	}
	d.Refresh()
}

func (d *HeaderDropdown) Disabled() bool {
	return d.disabled
}

func (d *HeaderDropdown) Hide() {
	d.hidden = true
	d.closePopup()
	d.BaseWidget.Hide()
}

func (d *HeaderDropdown) Show() {
	d.hidden = false
	d.BaseWidget.Show()
}

func (d *HeaderDropdown) openPopup() {
	if d.hidden {
		return
	}

	rows := make([]fyne.CanvasObject, 0, len(d.Options))
	for _, option := range d.Options {
		value := option
		item := newDropdownItem(value, "", value == d.Selected, func() {
			d.SetSelected(value)
			d.closePopup()
			if d.OnSelected != nil {
				d.OnSelected(value)
			}
		})
		item.textColor = d.TextColor
		item.textSize = d.TextSize
		item.monospace = d.UltraCompact
		rows = append(rows, item)
	}

	menuBG := canvas.NewRectangle(design.ColorGray950)
	menuBG.CornerRadius = design.RadiusMD

	menuBorder := canvas.NewRectangle(color.Transparent)
	menuBorder.CornerRadius = design.RadiusMD
	menuBorder.StrokeColor = d.BorderColor
	menuBorder.StrokeWidth = 1

	menuList := container.NewVBox(rows...)
	inset := d.menuInset()
	menuContent := fyne.CanvasObject(NewInset(menuList, inset, inset, inset, inset))
	menu := container.NewThemeOverride(
		container.NewStack(menuBG, menuContent, menuBorder),
		&dropdownMenuTheme{base: design.NewBrandTheme()},
	)

	canvasForObj := fyne.CurrentApp().Driver().CanvasForObject(d)
	if canvasForObj == nil {
		return
	}

	menuWidth := menu.MinSize().Width
	if d.Size().Width > menuWidth {
		menuWidth = d.Size().Width
	}
	for _, option := range d.Options {
		label := canvas.NewText(option, design.ColorTextLight)
		label.TextSize = d.TextSize
		if d.UltraCompact {
			label.TextStyle.Monospace = true
		}
		optionWidth := label.MinSize().Width + d.menuOptionPadding()
		if optionWidth > menuWidth {
			menuWidth = optionWidth
		}
	}
	if d.Size().Width > menuWidth {
		menuWidth = d.Size().Width
	}

	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(d)
	gap := float32(6)
	canvasSize := canvasForObj.Size()
	menuHeight := menu.MinSize().Height
	belowSpace := canvasSize.Height - (pos.Y + d.Size().Height + gap)
	aboveSpace := pos.Y - gap
	openAbove := menuHeight > belowSpace && aboveSpace > belowSpace

	popupContent := fyne.CanvasObject(menu)
	popupHeight := menuHeight
	chosenSpace := belowSpace
	if openAbove {
		chosenSpace = aboveSpace
	}

	maxPopupHeight := chosenSpace
	if maxPopupHeight > canvasSize.Height-12 {
		maxPopupHeight = canvasSize.Height - 12
	}
	if maxPopupHeight < 80 {
		maxPopupHeight = 80
	}
	if popupHeight > maxPopupHeight {
		scroll := container.NewVScroll(menuList)
		scroll.SetMinSize(fyne.NewSize(menuWidth-18, maxPopupHeight-12))
		menuContent = NewInset(scroll, inset, inset+4, inset, inset)
		popupContent = container.NewThemeOverride(container.NewStack(menuBG, menuContent, menuBorder), &dropdownMenuTheme{base: design.NewBrandTheme()})
		popupHeight = maxPopupHeight
	}

	popupX := pos.X
	if popupX+menuWidth > canvasSize.Width {
		popupX = canvasSize.Width - menuWidth - 4
	}
	if popupX < 4 {
		popupX = 4
	}

	popupY := pos.Y + d.Size().Height + gap
	if openAbove {
		popupY = pos.Y - popupHeight - gap
	}
	if popupY < 4 {
		popupY = 4
	}
	if popupY+popupHeight > canvasSize.Height-4 {
		popupY = canvasSize.Height - popupHeight - 4
	}

	d.popup = newDropdownPopup(
		popupContent,
		canvasForObj,
		fyne.NewSize(menuWidth, popupHeight),
		d.popupDismissed,
	)

	downIcon := coloredArrowDown(d.IconColor)
	upIcon := coloredArrowUp(d.IconColor)

	d.popup.ShowAtPosition(fyne.NewPos(popupX, popupY))
	d.opened = true
	d.animateArrow(downIcon, upIcon)
	d.Refresh()
}

func (d *HeaderDropdown) closePopup() {
	if d.popup != nil {
		d.popup.onDismiss = nil
		d.popup.Hide()
		d.popup = nil
	}

	downIcon := coloredArrowDown(d.IconColor)
	upIcon := coloredArrowUp(d.IconColor)

	d.opened = false
	d.hovered = false
	d.animateArrow(upIcon, downIcon)
	d.Refresh()
}

func (d *HeaderDropdown) popupDismissed() {
	d.popup = nil
	d.opened = false
	d.hovered = false
	d.animateArrow(theme.Icon(theme.IconNameArrowDropUp), theme.Icon(theme.IconNameArrowDropDown))
	d.Refresh()
}

func (d *HeaderDropdown) refreshVisuals() {
	if d.bg == nil || d.border == nil || d.label == nil || d.icon == nil {
		return
	}

	var fill color.Color = design.ColorGray900
	textColor := d.TextColor
	borderColor := d.BorderColor
	var iconResource fyne.Resource
	if d.opened {
		iconResource = coloredArrowUp(d.IconColor)
	} else {
		iconResource = coloredArrowDown(d.IconColor)
	}
	iconTranslucency := float64(0)

	switch {
	case d.disabled:
		fill = design.ColorGray900
		textColor = design.ColorBorder
		iconTranslucency = 0.35
	case d.opened:
		fill = d.HoverFillColor
		borderColor = d.HoverBorderColor
	case d.hovered:
		fill = d.HoverFillColor
		borderColor = d.HoverBorderColor
	}

	d.bg.FillColor = fill
	d.border.StrokeColor = borderColor
	d.label.Text = d.Selected
	d.label.Color = textColor
	d.icon.Resource = iconResource
	d.icon.Translucency = iconTranslucency
	d.bg.Refresh()
	d.border.Refresh()
	d.label.Refresh()
	d.icon.Refresh()
}

func (d *HeaderDropdown) controlHeight() float32 {
	if d.UltraCompact {
		return 26
	}
	if d.Compact {
		return 32
	}
	return 36
}

func (d *HeaderDropdown) horizontalPadding() float32 {
	if d.UltraCompact {
		return 32
	}
	if d.Compact {
		return 40
	}
	return 52
}

func (d *HeaderDropdown) minimumWidth() float32 {
	if d.UltraCompact {
		return 64
	}
	if d.Compact {
		return 74
	}
	return 88
}

func (d *HeaderDropdown) menuInset() float32 {
	if d.Compact {
		return 4
	}
	return 6
}

func (d *HeaderDropdown) menuOptionPadding() float32 {
	if d.UltraCompact {
		return 16
	}
	if d.Compact {
		return 28
	}
	return 40
}

func (d *HeaderDropdown) animateArrow(from fyne.Resource, to fyne.Resource) {
	if d.icon == nil || d.iconOpening {
		return
	}
	app := fyne.CurrentApp()
	if app == nil || !app.Settings().ShowAnimations() {
		d.icon.Resource = to
		d.icon.Translucency = 0
		d.icon.Refresh()
		return
	}

	d.iconOpening = true
	swapped := false
	anim := fyne.NewAnimation(120*time.Millisecond, func(done float32) {
		switch {
		case done < 0.5:
			d.icon.Translucency = float64(done * 2)
		default:
			if !swapped {
				d.icon.Resource = to
				swapped = true
			}
			d.icon.Translucency = float64((1 - done) * 2)
		}

		if done >= 1 {
			d.icon.Resource = to
			d.icon.Translucency = 0
			d.iconOpening = false
		}
		d.icon.Refresh()
	})
	anim.Curve = fyne.AnimationEaseInOut
	d.icon.Resource = from
	d.icon.Translucency = 0
	d.icon.Refresh()
	anim.Start()
}

type dropdownItem struct {
	widget.BaseWidget

	text      string
	secondary string
	selected  bool
	hovered   bool
	onTap     func()

	bg             *canvas.Rectangle
	label          *canvas.Text
	secondaryLabel *canvas.Text
	textColor      color.Color
	textSize       float32
	monospace      bool
}

func newDropdownItem(text, secondary string, selected bool, onTap func()) *dropdownItem {
	i := &dropdownItem{
		text:      text,
		secondary: secondary,
		selected:  selected,
		onTap:     onTap,
		textColor: design.ColorTextLight,
		textSize:  14,
	}
	i.ExtendBaseWidget(i)
	return i
}

func (i *dropdownItem) CreateRenderer() fyne.WidgetRenderer {
	i.bg = canvas.NewRectangle(color.Transparent)
	i.bg.CornerRadius = 4
	i.label = canvas.NewText(i.text, i.textColor)
	i.label.TextSize = i.textSize
	if i.monospace {
		i.label.TextStyle.Monospace = true
	}
	i.secondaryLabel = canvas.NewText(i.secondary, design.ColorTextMuted)
	i.secondaryLabel.TextSize = i.textSize
	if i.monospace {
		i.secondaryLabel.TextStyle.Monospace = true
	}
	r := &dropdownItemRenderer{
		item:    i,
		objects: []fyne.CanvasObject{container.NewWithoutLayout(i.bg, i.label, i.secondaryLabel)},
	}
	r.Refresh()
	return r
}

func (i *dropdownItem) MinSize() fyne.Size {
	label := canvas.NewText(i.text, i.textColor)
	label.TextSize = i.textSize
	if i.monospace {
		label.TextStyle.Monospace = true
	}

	padding := float32(28)
	if i.monospace {
		padding = 16
	}
	width := label.MinSize().Width + padding

	if i.secondary != "" {
		secondary := canvas.NewText(i.secondary, design.ColorTextMuted)
		secondary.TextSize = i.textSize
		if i.monospace {
			secondary.TextStyle.Monospace = true
		}
		width += secondary.MinSize().Width + 18
	}

	minWidth := float32(72)
	height := float32(36)

	if i.monospace {
		minWidth = 0
		height = 24
	} else if len(i.text) <= 4 && i.secondary == "" {
		height = 32
		minWidth = 64
	}

	if width < minWidth {
		width = minWidth
	}
	return fyne.NewSize(width, height)
}

func (i *dropdownItem) Tapped(*fyne.PointEvent) {
	if i.onTap != nil {
		i.onTap()
	}
}

func (i *dropdownItem) TappedSecondary(*fyne.PointEvent) {}

func (i *dropdownItem) MouseIn(*desktop.MouseEvent) {
	i.hovered = true
	i.Refresh()
}

func (i *dropdownItem) MouseMoved(*desktop.MouseEvent) {}

func (i *dropdownItem) MouseOut() {
	i.hovered = false
	i.Refresh()
}

func (i *dropdownItem) refreshVisuals() {
	if i.bg == nil || i.label == nil || i.secondaryLabel == nil {
		return
	}

	var fill color.Color = color.Transparent
	if i.selected {
		fill = design.ColorGray900
	}
	if i.hovered {
		fill = design.ColorSurfaceLight
	}

	i.bg.FillColor = fill
	i.bg.Refresh()
	i.label.Refresh()
	i.secondaryLabel.Refresh()
}

type headerDropdownRenderer struct {
	dropdown *HeaderDropdown
	objects  []fyne.CanvasObject
}

func (r *headerDropdownRenderer) Layout(size fyne.Size) {
	d := r.dropdown
	d.bg.Resize(size)
	d.border.Resize(size)

	labelMin := d.label.MinSize()
	labelWidth := size.Width - d.horizontalPadding()
	if labelWidth < 24 {
		labelWidth = 24
	}
	labelX := float32(16)
	if d.UltraCompact {
		labelX = 8
	} else if d.Compact {
		labelX = 12
	}
	d.label.Move(fyne.NewPos(labelX, (size.Height-labelMin.Height)/2))
	d.label.Resize(fyne.NewSize(labelWidth, labelMin.Height))

	iconSize := fyne.NewSize(16, 16)
	d.icon.Resize(iconSize)
	iconX := size.Width - 28
	if d.UltraCompact {
		iconX = size.Width - 20
	} else if d.Compact {
		iconX = size.Width - 24
	}
	d.icon.Move(fyne.NewPos(iconX, (size.Height-iconSize.Height)/2))
}

func (r *headerDropdownRenderer) MinSize() fyne.Size {
	return r.dropdown.MinSize()
}

func (r *headerDropdownRenderer) Refresh() {
	r.dropdown.refreshVisuals()
	r.Layout(r.dropdown.Size())
	canvas.Refresh(r.dropdown)
}

func (r *headerDropdownRenderer) BackgroundColor() color.Color {
	return color.Transparent
}

func (r *headerDropdownRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *headerDropdownRenderer) Destroy() {}

type dropdownPopup struct {
	widget.BaseWidget

	content   fyne.CanvasObject
	canvas    fyne.Canvas
	pos       fyne.Position
	size      fyne.Size
	shown     bool
	onDismiss func()
}

type StyledMenuItem struct {
	Label          string
	SecondaryLabel string
	Selected       bool
	OnTap          func()
}

type StyledMenuOptions struct {
	OpenAbove bool
	Centered  bool
	Width     float32
	MaxHeight float32
	// TextColor/TextSize override each row's default (design.ColorTextLight,
	// 14) -- nil/0 keeps the default. Used to make a menu read like a
	// HeaderDropdown's own popup (e.g. the header's language menu wants the
	// same teal/small-size look the per-connection protocol dropdown's
	// AUTO/TS/LAN popup already has) without dragging every other
	// ShowStyledMenu caller's look along with it.
	TextColor color.Color
	TextSize  float32
}

func newDropdownPopup(content fyne.CanvasObject, canvas fyne.Canvas, size fyne.Size, onDismiss func()) *dropdownPopup {
	p := &dropdownPopup{
		content:   content,
		canvas:    canvas,
		size:      size,
		onDismiss: onDismiss,
	}
	p.ExtendBaseWidget(p)
	return p
}

func ShowStyledMenu(anchor fyne.CanvasObject, items []StyledMenuItem) {
	showStyledMenu(anchor, items, StyledMenuOptions{})
}

func ShowStyledMenuAbove(anchor fyne.CanvasObject, items []StyledMenuItem) {
	showStyledMenu(anchor, items, StyledMenuOptions{OpenAbove: true})
}

func ShowStyledMenuCentered(anchor fyne.CanvasObject, items []StyledMenuItem, width, maxHeight float32) {
	showStyledMenu(anchor, items, StyledMenuOptions{
		Centered:  true,
		Width:     width,
		MaxHeight: maxHeight,
	})
}

// ShowStyledMenuTeal is ShowStyledMenu with rows recolored/resized to match
// the per-connection protocol dropdown's own AUTO/TS/LAN popup (teal text,
// design.ColorConnectionBadgeText, at 10px) -- for a menu that should read
// as "the same style" as that dropdown without becoming one itself (see the
// header's language menu, gui.ConnectionManager.showLanguageMenu).
func ShowStyledMenuTeal(anchor fyne.CanvasObject, items []StyledMenuItem) {
	showStyledMenu(anchor, items, StyledMenuOptions{
		TextColor: design.ColorConnectionBadgeText,
		TextSize:  10,
	})
}

func showStyledMenu(anchor fyne.CanvasObject, items []StyledMenuItem, options StyledMenuOptions) {
	if anchor == nil || len(items) == 0 {
		return
	}

	rows := make([]fyne.CanvasObject, 0, len(items))
	// rowCallbacks mirrors rows 1:1 -- the exact same tap closure each row
	// is built with below, kept separately so the wasm touch-scroll bridge
	// (attachTouchScroll) can invoke the right one directly by index when
	// it hit-tests a non-drag touch, instead of depending on Fyne's own
	// (missing, under wasm) touch-to-tap dispatch. See that function's doc
	// comment.
	rowCallbacks := make([]func(), 0, len(items))
	var popup *dropdownPopup
	for _, item := range items {
		menuItem := item
		onTap := func() {
			if popup != nil {
				popup.Hide()
			}
			if menuItem.OnTap != nil {
				menuItem.OnTap()
			}
		}
		row := newDropdownItem(menuItem.Label, menuItem.SecondaryLabel, menuItem.Selected, onTap)
		if options.TextColor != nil {
			row.textColor = options.TextColor
		}
		if options.TextSize > 0 {
			row.textSize = options.TextSize
		}
		rows = append(rows, row)
		rowCallbacks = append(rowCallbacks, onTap)
	}

	menuBG := canvas.NewRectangle(design.ColorGray950)
	menuBG.CornerRadius = design.RadiusMD

	menuBorder := canvas.NewRectangle(color.Transparent)
	menuBorder.CornerRadius = design.RadiusMD
	menuBorder.StrokeColor = design.ColorBorder
	menuBorder.StrokeWidth = 1

	menuList := container.NewVBox(rows...)
	menuContent := fyne.CanvasObject(NewInset(menuList, 6, 6, 6, 6))
	menu := container.NewThemeOverride(
		container.NewStack(menuBG, menuContent, menuBorder),
		&dropdownMenuTheme{base: design.NewBrandTheme()},
	)

	canvasForObj := fyne.CurrentApp().Driver().CanvasForObject(anchor)
	if canvasForObj == nil {
		return
	}

	rowTextSize := float32(14)
	if options.TextSize > 0 {
		rowTextSize = options.TextSize
	}

	menuMin := menu.MinSize()
	width := menuMin.Width
	for _, option := range items {
		label := canvas.NewText(option.Label, design.ColorTextLight)
		label.TextSize = rowTextSize
		optionWidth := label.MinSize().Width + 40
		if optionWidth > width {
			width = optionWidth
		}
	}
	if anchor.Size().Width > width {
		width = anchor.Size().Width
	}
	if options.Width > width {
		width = options.Width
	}

	height := menuMin.Height
	maxHeight := options.MaxHeight
	if maxHeight <= 0 {
		maxHeight = canvasForObj.Size().Height - 16
	}
	if maxHeight < 80 {
		maxHeight = 80
	}

	var scroll *container.Scroll
	content := fyne.CanvasObject(menu)
	if height > maxHeight {
		scroll = container.NewVScroll(menuList)
		scroll.SetMinSize(fyne.NewSize(width-18, maxHeight-12))
		menuContent = NewInset(scroll, 6, 10, 6, 6)
		content = container.NewThemeOverride(container.NewStack(menuBG, menuContent, menuBorder), &dropdownMenuTheme{base: design.NewBrandTheme()})
		height = maxHeight
	}

	popup = newDropdownPopup(
		content,
		canvasForObj,
		fyne.NewSize(width, height),
		// detachTouchScroll is a no-op if attachTouchScroll (below) was
		// never called for this popup, i.e. the plain (non-scrolling)
		// case -- safe to call unconditionally on every dismiss.
		detachTouchScroll,
	)

	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(anchor)
	popupPos := fyne.NewPos(
		pos.X+(anchor.Size().Width-width)/2,
		pos.Y+anchor.Size().Height+6,
	)
	if options.Centered {
		canvasSize := canvasForObj.Size()
		popupPos = fyne.NewPos(
			maxFloat32(8, (canvasSize.Width-width)/2),
			maxFloat32(8, pos.Y),
		)
		if popupPos.Y+height > canvasSize.Height-8 {
			popupPos.Y = canvasSize.Height - height - 8
		}
	} else if options.OpenAbove {
		popupPos = fyne.NewPos(
			pos.X+(anchor.Size().Width-width)/2,
			pos.Y-height-6,
		)
		if popupPos.Y < 0 {
			popupPos.Y = 0
		}
	}
	canvasSize := canvasForObj.Size()
	if popupPos.X < 8 {
		popupPos.X = 8
	}
	if popupPos.X+width > canvasSize.Width-8 {
		popupPos.X = canvasSize.Width - width - 8
	}
	if popupPos.Y+height > canvasSize.Height-8 && !options.OpenAbove && !options.Centered {
		popupPos.Y = canvasSize.Height - height - 8
	}
	if popupPos.Y < 8 {
		popupPos.Y = 8
	}
	popup.ShowAtPosition(popupPos)

	if scroll != nil {
		// Wire up swipe-to-scroll for this popup's list -- see
		// attachTouchScroll's doc comment for why this is needed at all
		// (Fyne's wasm driver has no touch-to-scroll translation) and a
		// no-op on every other platform (real touch/mouse dispatch
		// already scrolls container.Scroll there).
		//
		// Deliberately reusing popupPos/(width,height) -- the exact
		// values ShowAtPosition itself was just called with, so
		// guaranteed correct since they're what actually put the popup
		// on screen -- rather than re-deriving scroll's own rect via
		// Driver().AbsolutePositionForObject(scroll). That looked like
		// the more precise choice (covers only the list, not the
		// popup's padding/border too) but consistently returned (0,0)
		// here -- confirmed live via CDP against a real Android Chrome
		// session: the touch overlay ended up pinned to the screen's
		// top-left corner instead of over the list, so every swipe
		// landed on Fyne's canvas instead of the overlay and did
		// nothing. dropdownPopup is this package's own custom overlay
		// widget (canvas.Overlays().Add(p), not Fyne's built-in
		// widget.PopUp), and whatever AbsolutePositionForObject needs to
		// resolve position through overlay content apparently isn't
		// satisfied for an object nested this deep inside it at the
		// point ShowAtPosition returns. Covering the whole popup panel
		// instead of just the list viewport is harmless: a touch
		// landing on the padding/border either scrolls (no visible
		// content there to scroll into, clamped at the bounds) or misses
		// every row's hit-test in onScrollTouchEnd and is a no-op.
		attachTouchScroll(scroll, rows, rowCallbacks, popupPos, fyne.NewSize(width, height))
	}
}

func (p *dropdownPopup) CreateRenderer() fyne.WidgetRenderer {
	return &dropdownPopupRenderer{
		popup:   p,
		objects: []fyne.CanvasObject{p.content},
	}
}

func (p *dropdownPopup) MinSize() fyne.Size {
	return p.size
}

func (p *dropdownPopup) Tapped(ev *fyne.PointEvent) {
	if p.isInside(ev.Position) {
		return
	}
	p.Hide() // onDismiss and overlayHide are called inside Hide()
}

func (p *dropdownPopup) TappedSecondary(ev *fyne.PointEvent) {
	p.Tapped(ev)
}

func (p *dropdownPopup) ShowAtPosition(pos fyne.Position) {
	p.pos = pos
	if !p.shown {
		p.canvas.Overlays().Add(p)
		p.shown = true
		overlayShow()
	}
	p.BaseWidget.Resize(p.canvas.Size())
	p.Show()
	p.Refresh()
}

func (p *dropdownPopup) Hide() {
	if !p.shown {
		p.BaseWidget.Hide()
		return
	}
	p.canvas.Overlays().Remove(p)
	p.shown = false
	p.BaseWidget.Hide()
	dismiss := p.onDismiss
	if dismiss != nil {
		dismiss()
	}
	overlayHide()
}

func (p *dropdownPopup) isInside(pos fyne.Position) bool {
	return pos.X >= p.pos.X &&
		pos.Y >= p.pos.Y &&
		pos.X <= p.pos.X+p.size.Width &&
		pos.Y <= p.pos.Y+p.size.Height
}

type dropdownPopupRenderer struct {
	popup   *dropdownPopup
	objects []fyne.CanvasObject
}

func (r *dropdownPopupRenderer) Layout(size fyne.Size) {
	r.popup.content.Move(r.popup.pos)
	r.popup.content.Resize(r.popup.size)
}

func (r *dropdownPopupRenderer) MinSize() fyne.Size {
	return r.popup.size
}

func (r *dropdownPopupRenderer) Refresh() {
	if r.popup.canvas.Size() != r.popup.Size() {
		r.popup.BaseWidget.Resize(r.popup.canvas.Size())
	}
	r.Layout(r.popup.Size())
	r.popup.content.Refresh()
	canvas.Refresh(r.popup)
}

func (r *dropdownPopupRenderer) BackgroundColor() color.Color {
	return color.Transparent
}

func (r *dropdownPopupRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *dropdownPopupRenderer) Destroy() {}

type dropdownItemRenderer struct {
	item    *dropdownItem
	objects []fyne.CanvasObject
}

func (r *dropdownItemRenderer) Layout(size fyne.Size) {
	r.item.bg.Resize(size)
	labelMin := r.item.label.MinSize()
	labelX := float32(14)
	if r.item.monospace {
		labelX = 8
	} else if len(r.item.text) <= 4 && r.item.secondary == "" {
		labelX = 12
	}
	r.item.label.Move(fyne.NewPos(labelX, (size.Height-labelMin.Height)/2))
	r.item.label.Resize(labelMin)

	if r.item.secondary != "" {
		rightMin := r.item.secondaryLabel.MinSize()
		rightX := size.Width - rightMin.Width - 14
		if rightX < labelX+labelMin.Width+12 {
			rightX = labelX + labelMin.Width + 12
		}
		r.item.secondaryLabel.Show()
		r.item.secondaryLabel.Move(fyne.NewPos(rightX, (size.Height-rightMin.Height)/2))
		r.item.secondaryLabel.Resize(rightMin)
		return
	}

	r.item.secondaryLabel.Hide()
}

func (r *dropdownItemRenderer) MinSize() fyne.Size {
	return r.item.MinSize()
}

func (r *dropdownItemRenderer) Refresh() {
	r.item.refreshVisuals()
	r.Layout(r.item.Size())
	canvas.Refresh(r.item)
}

func (r *dropdownItemRenderer) BackgroundColor() color.Color {
	return color.Transparent
}

func (r *dropdownItemRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *dropdownItemRenderer) Destroy() {}

var (
	_ fyne.Tappable     = (*HeaderDropdown)(nil)
	_ desktop.Hoverable = (*HeaderDropdown)(nil)
	_ fyne.Widget       = (*HeaderDropdown)(nil)
	_ fyne.Tappable     = (*dropdownPopup)(nil)
	_ fyne.Widget       = (*dropdownPopup)(nil)
	_ fyne.Tappable     = (*dropdownItem)(nil)
	_ desktop.Hoverable = (*dropdownItem)(nil)
)

func coloredArrowDown(c color.Color) fyne.Resource {
	if c == nil {
		return theme.Icon(theme.IconNameArrowDropDown)
	}
	r, g, b, _ := c.RGBA()
	colorStr := fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
	svg := fmt.Sprintf("<svg viewBox=\"0 0 24 24\" fill=\"%s\"><path d=\"M7 10l5 5 5-5z\"/></svg>", colorStr)
	return fyne.NewStaticResource("custom_arrow_down.svg", []byte(svg))
}

func coloredArrowUp(c color.Color) fyne.Resource {
	if c == nil {
		return theme.Icon(theme.IconNameArrowDropUp)
	}
	r, g, b, _ := c.RGBA()
	colorStr := fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
	svg := fmt.Sprintf("<svg viewBox=\"0 0 24 24\" fill=\"%s\"><path d=\"M7 14l5-5 5 5z\"/></svg>", colorStr)
	return fyne.NewStaticResource("custom_arrow_up.svg", []byte(svg))
}
