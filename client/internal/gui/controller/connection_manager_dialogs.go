package controller

import (
	"fmt"
	"image/color"
	"net/url"
	"strings"

	"usbridge-client/internal/gui/assets"
	"usbridge-client/internal/gui/design"
	"usbridge-client/internal/gui/i18n"
	"usbridge-client/internal/gui/view"
	"usbridge-client/internal/models"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/sirupsen/logrus"
	qrcode "github.com/skip2/go-qrcode"
)

const (
	connectionDialogNameLabel                  = "name"
	connectionDialogInternalHostLabel          = "internal ip address"
	connectionDialogTailscaleHostLabel         = "tailscale address"
	connectionDialogTokenLabel                 = "master key"
	qrScanSuccessText                          = "\u2713 qr code successfully scanned"
	connectionDialogButtonsGap         float32 = 12
)

type connectionDialogSpec struct {
	title    string
	subtitle string
	// icon is the small square tile shown next to title/subtitle in the
	// panel header -- nil hides the tile entirely (only showPrefilledAddDialog
	// sets one right now; showEditDialog, unused, leaves it nil).
	icon                   fyne.Resource
	connectLabel           string
	connectIcon            fyne.Resource
	saveLabel              string
	deleteLabel            string
	nameValue              string
	internalHostValue      string
	tailscaleHostValue     string
	masterKeyValue         string // master key (API secret from QR)
	tailscaleRegisterValue bool
	feedbackText           string
	feedbackColor          color.Color
	onConnect              func(name, internalHost, tailscaleHost, masterKey string, tailscaleRegister bool) bool
	onSave                 func(name, internalHost, tailscaleHost, masterKey string, tailscaleRegister bool) bool
	onDelete               func(close func())
	onQR                   func()
	// startWithPasteLink opens the dialog with the inline paste-link view
	// already showing instead of the Name/LAN/TS/Token fields -- the "+"
	// placeholder card's own Paste Link button jumps straight here instead
	// of stopping at the normal fields view first (see handlePasteLink).
	// Only meaningful alongside onQR, which is what builds that view.
	startWithPasteLink bool
}

type connectionDialogSecondaryButton struct {
	widget.BaseWidget

	labelText   string
	onTapped    func()
	hovered     bool
	fillColor   color.Color
	borderColor color.Color
	textColor   color.Color
	// hoverBorderColor overrides borderColor while hovered -- nil (the zero
	// value) keeps borderColor unchanged on hover, the behavior every
	// existing caller before Scan QR/Paste Link relied on.
	hoverBorderColor color.Color
	hoverFillColor   color.Color
	hoverTextColor   color.Color
	iconRes          fyne.Resource
	hoverIconRes     fyne.Resource
	// compact shrinks the icon/text/height/padding this button renders at --
	// Scan QR/Paste Link only; every other caller (Cancel, the danger
	// Delete button) keeps the original, larger sizing.
	compact bool
	bg      *canvas.Rectangle
	border  *canvas.Rectangle
	label   *canvas.Text
	icon    *canvas.Image
}

// connectionDialogSecondaryButton's size constants -- normal vs. compact
// (Scan QR/Paste Link).
const (
	connectionDialogSecondaryIconSize    = float32(18)
	connectionDialogSecondaryCompactIcon = float32(12)
	connectionDialogSecondaryTextSize    = float32(16)
	connectionDialogSecondaryCompactText = float32(9.5)
	connectionDialogSecondaryHeight      = float32(36)
	connectionDialogSecondaryCompactH    = float32(32)
	connectionDialogSecondaryPadX        = float32(16) // each side
	connectionDialogSecondaryCompactPadX = float32(15)
)

func (b *connectionDialogSecondaryButton) iconSize() float32 {
	if b.compact {
		return connectionDialogSecondaryCompactIcon
	}
	return connectionDialogSecondaryIconSize
}

func (b *connectionDialogSecondaryButton) textSize() float32 {
	if b.compact {
		return connectionDialogSecondaryCompactText
	}
	return connectionDialogSecondaryTextSize
}

func (b *connectionDialogSecondaryButton) height() float32 {
	if b.compact {
		return connectionDialogSecondaryCompactH
	}
	return connectionDialogSecondaryHeight
}

func (b *connectionDialogSecondaryButton) padX() float32 {
	if b.compact {
		return connectionDialogSecondaryCompactPadX
	}
	return connectionDialogSecondaryPadX
}

type connectionDialogEntry struct {
	widget.Entry

	onFocusChanged func(bool)
	OnChanged      func(string)
}

func (cm *ConnectionManager) setLanguage(langCode string) {
	cm.app.Preferences().SetString("language", langCode)
	i18n.SetLanguage(langCode)
	logrus.Infof("Language changed to: %s", langCode)
	if cm.onLanguageChange != nil {
		cm.onLanguageChange()
	}
}

func (e *connectionDialogEntry) FocusGained() {
	e.Entry.FocusGained()
	if e.onFocusChanged != nil {
		e.onFocusChanged(true)
	}
}

func (e *connectionDialogEntry) FocusLost() {
	e.Entry.FocusLost()
	if e.onFocusChanged != nil {
		e.onFocusChanged(false)
	}
}

func (e *connectionDialogEntry) TypedRune(r rune) {
	e.Entry.TypedRune(r)
	if e.OnChanged != nil {
		e.OnChanged(e.Text)
	}
}

func (e *connectionDialogEntry) TypedKey(k *fyne.KeyEvent) {
	e.Entry.TypedKey(k)
	if e.OnChanged != nil {
		e.OnChanged(e.Text)
	}
}

func (e *connectionDialogEntry) SetText(text string) {
	e.Entry.SetText(text)
	if e.OnChanged != nil {
		e.OnChanged(e.Text)
	}
}

func newConnectionDialogLabel(text string) fyne.CanvasObject {
	label := canvas.NewText(text, design.ColorTextMuted)
	label.TextSize = 12
	return label
}

func newConnectionDialogSecondaryButton(label string, onTapped func()) *connectionDialogSecondaryButton {
	btn := &connectionDialogSecondaryButton{
		labelText:      label,
		onTapped:       onTapped,
		fillColor:      color.Transparent,
		borderColor:    design.ColorAccent,
		textColor:      design.ColorAccent,
		hoverFillColor: design.ColorAccent,
		hoverTextColor: design.ColorBackground,
	}
	btn.ExtendBaseWidget(btn)
	return btn
}

func newConnectionDialogDangerSecondaryButton(label string, icon fyne.Resource, onTapped func()) *connectionDialogSecondaryButton {
	btn := &connectionDialogSecondaryButton{
		labelText:      label,
		onTapped:       onTapped,
		fillColor:      design.ColorSurfaceLight,
		borderColor:    color.Transparent,
		textColor:      design.ColorTextLight,
		hoverFillColor: design.ColorBorder,
		hoverTextColor: design.ColorTextLight,
		iconRes:        theme.NewThemedResource(icon),
		hoverIconRes:   theme.NewThemedResource(icon),
	}
	btn.ExtendBaseWidget(btn)
	return btn
}

func newConnectionDialogPrimaryButton(label string, icon fyne.Resource, onTapped func()) *view.ConnectionPrimaryButton {
	btn := view.NewConnectionPrimaryButton(label, onTapped)
	// If icon is provided, we might need to handle it or modify the exported button
	return btn
}

func newConnectionDialogAccentButton(label string, icon fyne.Resource, onTapped func()) *view.ConnectionPrimaryButton {
	btn := view.NewConnectionPrimaryButton(label, onTapped)
	btn.SetAccent(true)
	return btn
}

func (b *connectionDialogSecondaryButton) iconLabelGap() float32 {
	if b.compact {
		return 0
	}
	return 6
}

func (b *connectionDialogSecondaryButton) CreateRenderer() fyne.WidgetRenderer {
	b.bg = canvas.NewRectangle(color.Transparent)
	b.bg.CornerRadius = design.RadiusMD

	b.border = canvas.NewRectangle(color.Transparent)
	b.border.CornerRadius = design.RadiusMD
	b.border.StrokeColor = b.borderColor
	b.border.StrokeWidth = 1

	b.label = canvas.NewText(b.labelText, b.textColor)
	b.label.TextSize = b.textSize()
	b.label.TextStyle.Bold = true
	b.label.Alignment = fyne.TextAlignCenter

	b.icon = canvas.NewImageFromResource(b.iconRes)
	b.icon.FillMode = canvas.ImageFillContain
	if b.iconRes == nil {
		b.icon.Hide()
	} else {
		iconSz := b.iconSize()
		b.icon.SetMinSize(fyne.NewSize(iconSz, iconSz))
	}

	b.refreshVisuals()
	content := container.NewCenter(container.NewHBox(
		b.icon,
		view.NewInset(b.label, b.iconLabelGap(), 0, 0, 0),
	))
	if b.iconRes == nil {
		content = container.NewCenter(b.label)
	}
	return widget.NewSimpleRenderer(container.NewMax(b.bg, content, b.border))
}

func (b *connectionDialogSecondaryButton) MinSize() fyne.Size {
	// A real intrinsic width, not a bare 0 -- every existing caller places
	// this inside a container.NewGridWithColumns, which stretches each cell
	// regardless of its MinSize.Width, so 0 never actually showed up as a
	// collapsed button there. A right-aligned natural-width group (see
	// showAdaptiveConnectionDialog's footer, and Scan QR/Paste Link's own
	// row) has no such stretch to hide behind, and needs the real number.
	measure := canvas.NewText(b.labelText, color.Black)
	measure.TextSize = b.textSize()
	measure.TextStyle.Bold = true
	width := measure.MinSize().Width + b.padX()*2
	if b.iconRes != nil {
		width += b.iconSize() + b.iconLabelGap()
	}
	return fyne.NewSize(width, b.height())
}

func (b *connectionDialogSecondaryButton) Tapped(*fyne.PointEvent) {
	if b.onTapped != nil {
		b.onTapped()
	}
}

func (b *connectionDialogSecondaryButton) TappedSecondary(*fyne.PointEvent) {}

func (b *connectionDialogSecondaryButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.refreshVisuals()
}

func (b *connectionDialogSecondaryButton) MouseMoved(*desktop.MouseEvent) {}

func (b *connectionDialogSecondaryButton) MouseOut() {
	b.hovered = false
	b.refreshVisuals()
}

func (b *connectionDialogSecondaryButton) refreshVisuals() {
	if b.bg == nil || b.border == nil || b.label == nil || b.icon == nil {
		return
	}

	b.bg.FillColor = b.fillColor
	b.border.StrokeColor = b.borderColor
	b.border.StrokeWidth = 0
	b.label.Color = b.textColor
	b.icon.Resource = b.iconRes
	if b.hovered {
		b.bg.FillColor = b.hoverFillColor
		b.label.Color = b.hoverTextColor
		if b.hoverIconRes != nil {
			b.icon.Resource = b.hoverIconRes
		}
		if b.hoverBorderColor != nil {
			b.border.StrokeColor = b.hoverBorderColor
		}
	}
	if b.border.StrokeColor != nil && b.border.StrokeColor != color.Transparent {
		b.border.StrokeWidth = 1
	}

	b.bg.Refresh()
	b.border.Refresh()
	b.label.Refresh()
	b.icon.Refresh()
}

func newConnectionDialogField(label string, field fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(
		view.NewInset(newConnectionDialogLabel(label), 10, 0, 0, 0),
		view.NewInset(field, 0, 0, 0, 2),
	)
}

func newConnectionDialogFieldWithActions(label string, field fyne.CanvasObject, actions fyne.CanvasObject) fyne.CanvasObject {
	labelRow := container.NewHBox(
		newConnectionDialogLabel(label),
		layout.NewSpacer(),
		actions,
	)
	return container.NewVBox(
		view.NewInset(labelRow, 10, 0, 0, 0),
		view.NewInset(field, 0, 0, 0, 2),
	)
}

// buildInlineField creates a horizontal [label (actions) | field] row for compact mobile layout.
// Actions are placed between the label and the input so they appear before the field.
// A transparent rect enforces a consistent minimum label column width so all inputs align.
func buildInlineField(label string, field fyne.CanvasObject, actions fyne.CanvasObject) fyne.CanvasObject {
	lbl := newConnectionDialogLabel(label)
	minW := canvas.NewRectangle(color.Transparent)
	minW.SetMinSize(fyne.NewSize(84, 1))
	// top=10 vertically centres the ~14 dp label text inside the ~36 dp entry row.
	lblContainer := container.NewStack(minW, view.NewInset(lbl, 0, 6, 10, 0))
	var leftSide fyne.CanvasObject
	if actions != nil {
		// [label][actions] | [field] — actions sit between label and input field
		leftSide = container.NewHBox(lblContainer, actions)
	} else {
		leftSide = lblContainer
	}
	return view.NewInset(container.NewBorder(nil, nil, leftSide, nil, field), 0, 0, 6, 0)
}

// newConnectionDialogEntryAddon creates a small icon button styled to match the
// entry field height, intended as an attached addon button to the right of a field.
func newConnectionDialogEntryAddon(iconRes fyne.Resource, onTapped func()) fyne.CanvasObject {
	btn := &connectionDialogIconButton{
		resource:   iconRes,
		onTapped:   onTapped,
		buttonSize: fyne.NewSize(36, 36),
		iconSize:   fyne.NewSize(16, 16),
	}
	btn.ExtendBaseWidget(btn)
	bg := canvas.NewRectangle(design.ColorSurfaceLight)
	bg.CornerRadius = design.RadiusMD
	bdr := canvas.NewRectangle(color.Transparent)
	bdr.CornerRadius = design.RadiusMD
	bdr.StrokeColor = design.ColorBorder
	bdr.StrokeWidth = 1
	return container.NewStack(bg, bdr, btn)
}

// applyPastedText replaces entry's whole content with text and fires its
// OnChanged the same way a real keystroke/native paste would, so anything
// wired to the field (live validation, draft persistence) still runs.
func applyPastedText(entry *connectionDialogEntry, text string) {
	entry.SetText(text)
	if entry.OnChanged != nil {
		entry.OnChanged(text)
	}
}

// connectionDialogRegisterRow is the "Register in Tailscale" row: a bordered
// card with its own checkbox (not widget.Check -- its look has no room for
// the status dot or the "AUTO-DISCOVER" badge this row also carries), tap-
// to-toggle across the whole row rather than just the checkbox square.
type connectionDialogRegisterRow struct {
	widget.BaseWidget

	Checked  bool
	label    string
	sublabel string
	badge    string
	onChange func(bool)

	bg        *canvas.Rectangle
	checkBg   *canvas.Rectangle
	checkMark *canvas.Image
	labelTxt  *canvas.Text
	dot       *canvas.Circle
	subTxt    *canvas.Text
	badgeBg   *canvas.Rectangle
	badgeTxt  *canvas.Text
}

func newConnectionDialogRegisterRow(checked bool, label, sublabel, badge string, onChange func(bool)) *connectionDialogRegisterRow {
	r := &connectionDialogRegisterRow{Checked: checked, label: label, sublabel: sublabel, badge: badge, onChange: onChange}
	r.ExtendBaseWidget(r)
	return r
}

func (r *connectionDialogRegisterRow) Tapped(*fyne.PointEvent) {
	r.Checked = !r.Checked
	r.refreshVisuals()
	if r.onChange != nil {
		r.onChange(r.Checked)
	}
}

func (r *connectionDialogRegisterRow) TappedSecondary(*fyne.PointEvent) {}
func (r *connectionDialogRegisterRow) Cursor() desktop.Cursor           { return desktop.PointerCursor }

func (r *connectionDialogRegisterRow) MinSize() fyne.Size {
	return fyne.NewSize(0, registerRowHeight)
}

const registerRowHeight = float32(58)

func (r *connectionDialogRegisterRow) CreateRenderer() fyne.WidgetRenderer {
	r.bg = canvas.NewRectangle(design.ColorGray900)
	r.bg.CornerRadius = design.RadiusMD
	r.bg.StrokeColor = design.ColorTailscaleChipBorder
	r.bg.StrokeWidth = 1

	r.checkBg = canvas.NewRectangle(color.Transparent)
	r.checkBg.CornerRadius = 4
	r.checkBg.StrokeColor = design.ColorBorder
	r.checkBg.StrokeWidth = 1

	checkmarkSVG := fyne.NewStaticResource("register-row-check.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="#111111" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="M4 12L10 18L20 6"/></svg>`))
	r.checkMark = canvas.NewImageFromResource(checkmarkSVG)
	r.checkMark.FillMode = canvas.ImageFillContain

	r.labelTxt = canvas.NewText(r.label, design.ColorTextLight)
	r.labelTxt.TextSize = 12
	r.labelTxt.TextStyle.Bold = true

	// Dot removed
	r.dot = canvas.NewCircle(color.Transparent)
	r.dot.Hide()

	r.subTxt = canvas.NewText(r.sublabel, color.NRGBA{R: 0xc3, G: 0xc6, B: 0xb4, A: 0xff})
	r.subTxt.TextSize = 8

	r.badgeBg = canvas.NewRectangle(color.Transparent)
	r.badgeBg.CornerRadius = 4
	r.badgeBg.StrokeColor = design.ColorConnectionBadgeText
	r.badgeBg.StrokeWidth = 1

	r.badgeTxt = canvas.NewText(r.badge, design.ColorConnectionBadgeText)
	r.badgeTxt.TextSize = 8
	r.badgeTxt.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}

	r.refreshVisuals()
	return &connectionDialogRegisterRowRenderer{r: r}
}

func (r *connectionDialogRegisterRow) refreshVisuals() {
	if r.checkBg == nil {
		return
	}
	if r.Checked {
		r.checkBg.FillColor = design.ColorConnectionAddFill
		r.checkBg.StrokeColor = color.Transparent
		r.checkMark.Show()
	} else {
		r.checkBg.FillColor = color.Transparent
		r.checkBg.StrokeColor = design.ColorBorder
		r.checkMark.Hide()
	}
	r.checkBg.Refresh()
	r.checkMark.Refresh()
}

type connectionDialogRegisterRowRenderer struct {
	r *connectionDialogRegisterRow
}

const (
	registerRowCheckSize = float32(18)
	registerRowDotSize   = float32(6)
	registerRowPadding   = float32(14)
)

func (rr *connectionDialogRegisterRowRenderer) Layout(size fyne.Size) {
	r := rr.r
	r.bg.Resize(size)

	checkY := (size.Height - registerRowCheckSize) / 2
	r.checkBg.Move(fyne.NewPos(registerRowPadding, checkY))
	r.checkBg.Resize(fyne.NewSize(registerRowCheckSize, registerRowCheckSize))
	markSize := float32(11)
	r.checkMark.Move(fyne.NewPos(registerRowPadding+(registerRowCheckSize-markSize)/2, checkY+(registerRowCheckSize-markSize)/2))
	r.checkMark.Resize(fyne.NewSize(markSize, markSize))

	textX := registerRowPadding + registerRowCheckSize + 12

	badgeMin := r.badgeTxt.MinSize()
	badgeW := badgeMin.Width + 16
	badgeH := float32(18)
	badgeX := size.Width - registerRowPadding - badgeW
	badgeY := (size.Height - badgeH) / 2
	r.badgeBg.Move(fyne.NewPos(badgeX, badgeY))
	r.badgeBg.Resize(fyne.NewSize(badgeW, badgeH))
	r.badgeTxt.Move(fyne.NewPos(badgeX+8, badgeY+(badgeH-badgeMin.Height)/2))
	r.badgeTxt.Resize(badgeMin)

	textMaxX := badgeX - 12

	labelMin := r.labelTxt.MinSize()
	subMin := r.subTxt.MinSize()
	totalTextH := labelMin.Height + 3 + subMin.Height
	textY := (size.Height - totalTextH) / 2

	r.labelTxt.Move(fyne.NewPos(textX, textY))
	r.labelTxt.Resize(labelMin)

	dotX := textX + labelMin.Width + 6
	r.dot.Move(fyne.NewPos(dotX, textY+(labelMin.Height-registerRowDotSize)/2))
	r.dot.Resize(fyne.NewSize(registerRowDotSize, registerRowDotSize))

	subY := textY + labelMin.Height + 3
	subW := maxFloat32(0, textMaxX-textX)
	r.subTxt.Move(fyne.NewPos(textX, subY))
	r.subTxt.Resize(fyne.NewSize(subW, subMin.Height))
}

func (rr *connectionDialogRegisterRowRenderer) MinSize() fyne.Size {
	return fyne.NewSize(0, registerRowHeight)
}
func (rr *connectionDialogRegisterRowRenderer) Refresh() {
	rr.r.refreshVisuals()
	rr.Layout(rr.r.Size())
	canvas.Refresh(rr.r)
}
func (rr *connectionDialogRegisterRowRenderer) BackgroundColor() color.Color {
	return color.Transparent
}
func (rr *connectionDialogRegisterRowRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{rr.r.bg, rr.r.checkBg, rr.r.checkMark, rr.r.labelTxt, rr.r.dot, rr.r.subTxt, rr.r.badgeBg, rr.r.badgeTxt}
}
func (rr *connectionDialogRegisterRowRenderer) Destroy() {}

// buildConnectionDialogForm assembles the form body: statsBox (Name/LAN/TS/
// Token, all one box now -- view.NewConnectionCardEditableStatsBox(true,
// ...), the exact widget/styling the Grid card's inline edit and List's
// split-edit panel already use for LAN/TS/Token, extended with a Name row
// this one caller opts into) + the optional register-in-Tailscale row.
// Name used to be its own separate newConnectionDialogIconEntry row in a
// different style entirely -- one dark box now, not two different-looking
// ones stacked on top of each other.
//
// This also dropped the old per-platform Tailscale-field handling (a hint +
// download link instead of the field itself on wasm, where there's no
// embedded tsnet to dial it with) -- statsBox always shows all three
// address/key rows, same as the Grid/List surfaces it's shared with, neither
// of which special-cases wasm here either.
func buildConnectionDialogForm(statsBox fyne.CanvasObject, registerCheck fyne.CanvasObject) fyne.CanvasObject {
	items := []fyne.CanvasObject{
		statsBox,
	}
	if registerCheck != nil {
		items = append(items, view.NewInset(registerCheck, 0, 0, 10, 10))
	}
	return container.NewVBox(items...)
}

func newConnectionDialogFeedback(text string, fill color.Color) fyne.CanvasObject {
	label := canvas.NewText(text, fill)
	label.TextSize = 11
	label.TextStyle.Bold = true
	return label
}

func connectionDialogDimColor() color.Color {
	return color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x72}
}

func newConnectionDialogQRButton(label string, onTapped func()) *connectionDialogSecondaryButton {
	btn := &connectionDialogSecondaryButton{
		labelText:      label,
		onTapped:       onTapped,
		fillColor:      color.Transparent,
		borderColor:    design.ColorAccent,
		textColor:      design.ColorAccent,
		hoverFillColor: design.ColorAccent,
		hoverTextColor: design.ColorBackground,
		iconRes:        assets.QRCodeAccent,
		hoverIconRes:   assets.QRCodeBoldBlack,
	}
	btn.ExtendBaseWidget(btn)
	return btn
}

// connectionDialogIconBlock is a large square icon button with a text label below,
// used for QR scan and link paste actions.
type connectionDialogIconBlock struct {
	widget.BaseWidget

	iconRes fyne.Resource
	label   string
	onTap   func()
	hovered bool

	bg  *canvas.Rectangle
	bdr *canvas.Rectangle
	img *canvas.Image
	lbl *canvas.Text
}

func newConnectionDialogIconBlock(iconRes fyne.Resource, label string, onTap func()) *connectionDialogIconBlock {
	b := &connectionDialogIconBlock{iconRes: iconRes, label: label, onTap: onTap}
	b.ExtendBaseWidget(b)
	return b
}

func (b *connectionDialogIconBlock) Tapped(*fyne.PointEvent) {
	if b.onTap != nil {
		b.onTap()
	}
}
func (b *connectionDialogIconBlock) TappedSecondary(*fyne.PointEvent) {}

func (b *connectionDialogIconBlock) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.refreshVisuals()
}
func (b *connectionDialogIconBlock) MouseMoved(*desktop.MouseEvent) {}
func (b *connectionDialogIconBlock) MouseOut() {
	b.hovered = false
	b.refreshVisuals()
}
func (b *connectionDialogIconBlock) Cursor() desktop.Cursor { return desktop.PointerCursor }

const iconBlockSquare = float32(64)
const iconBlockIconSz = float32(28)
const iconBlockLabelH = float32(14)
const iconBlockGap = float32(5)

func (b *connectionDialogIconBlock) MinSize() fyne.Size {
	return fyne.NewSize(iconBlockSquare, iconBlockSquare+iconBlockGap+iconBlockLabelH)
}

func (b *connectionDialogIconBlock) CreateRenderer() fyne.WidgetRenderer {
	b.bg = canvas.NewRectangle(design.ColorSurfaceLight)
	b.bg.CornerRadius = design.RadiusMD

	b.bdr = canvas.NewRectangle(color.Transparent)
	b.bdr.CornerRadius = design.RadiusMD
	b.bdr.StrokeColor = design.ColorBorder
	b.bdr.StrokeWidth = 1

	b.img = canvas.NewImageFromResource(b.iconRes)
	b.img.FillMode = canvas.ImageFillContain

	b.lbl = canvas.NewText(b.label, design.ColorTextMuted)
	b.lbl.TextSize = 11
	b.lbl.Alignment = fyne.TextAlignCenter

	b.refreshVisuals()
	return &connectionDialogIconBlockRenderer{b: b}
}

func (b *connectionDialogIconBlock) refreshVisuals() {
	if b.bg == nil {
		return
	}
	if b.hovered {
		b.bg.FillColor = design.ColorBorder
	} else {
		b.bg.FillColor = design.ColorSurfaceLight
	}
	b.bg.Refresh()
	if b.bdr != nil {
		b.bdr.Refresh()
	}
	if b.img != nil {
		b.img.Refresh()
	}
	if b.lbl != nil {
		b.lbl.Refresh()
	}
}

type connectionDialogIconBlockRenderer struct{ b *connectionDialogIconBlock }

func (r *connectionDialogIconBlockRenderer) Layout(size fyne.Size) {
	sqW := minFloat32(iconBlockSquare, size.Width)
	sqX := (size.Width - sqW) / 2
	if sqX < 0 {
		sqX = 0
	}
	r.b.bg.Move(fyne.NewPos(sqX, 0))
	r.b.bg.Resize(fyne.NewSize(sqW, sqW))
	r.b.bdr.Move(fyne.NewPos(sqX, 0))
	r.b.bdr.Resize(fyne.NewSize(sqW, sqW))

	imgX := sqX + (sqW-iconBlockIconSz)/2
	imgY := (sqW - iconBlockIconSz) / 2
	r.b.img.Move(fyne.NewPos(imgX, imgY))
	r.b.img.Resize(fyne.NewSize(iconBlockIconSz, iconBlockIconSz))

	r.b.lbl.Move(fyne.NewPos(0, sqW+iconBlockGap))
	r.b.lbl.Resize(fyne.NewSize(size.Width, iconBlockLabelH))
}

func (r *connectionDialogIconBlockRenderer) MinSize() fyne.Size { return r.b.MinSize() }
func (r *connectionDialogIconBlockRenderer) Refresh() {
	r.b.refreshVisuals()
	r.Layout(r.b.Size())
	canvas.Refresh(r.b)
}
func (r *connectionDialogIconBlockRenderer) BackgroundColor() color.Color { return color.Transparent }
func (r *connectionDialogIconBlockRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.b.bg, r.b.bdr, r.b.img, r.b.lbl}
}
func (r *connectionDialogIconBlockRenderer) Destroy() {}

// mutedForegroundTheme recolors a themed widget's foreground text --
// widget.Label has no color field of its own (unlike canvas.Text), so the
// header subtitle (needs Wrapping, which only widget.Label supports) goes
// through this to still render muted instead of the theme's default
// full-brightness text color.
type mutedForegroundTheme struct{ fyne.Theme }

func (t *mutedForegroundTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameForeground {
		return color.NRGBA{R: 0xc3, G: 0xc6, B: 0xb4, A: 0xff}
	}
	return t.Theme.Color(name, variant)
}

func (t *mutedForegroundTheme) Size(name fyne.ThemeSizeName) float32 {
	if name == theme.SizeNameText {
		return 8 // Smaller subtitle text
	}
	return t.Theme.Size(name)
}

type subtitleLeftNudgeLayout struct {
	Amount float32
}

func (l *subtitleLeftNudgeLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) == 0 {
		return fyne.NewSize(0, 0)
	}
	min := objects[0].MinSize()
	return fyne.NewSize(min.Width-l.Amount, min.Height)
}

func (l *subtitleLeftNudgeLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	objects[0].Resize(fyne.NewSize(size.Width+l.Amount, size.Height))
	objects[0].Move(fyne.NewPos(-l.Amount, 0))
}

type tightHeaderVBoxLayout struct {
	Gap float32
}

func (l *tightHeaderVBoxLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var width, height float32
	var count int
	for _, obj := range objects {
		if !obj.Visible() {
			continue
		}
		count++
		min := obj.MinSize()
		if min.Width > width {
			width = min.Width
		}
		height += min.Height
	}
	if count > 1 {
		height += float32(count-1) * l.Gap
	}
	return fyne.NewSize(width, height)
}

func (l *tightHeaderVBoxLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	var y float32
	for _, obj := range objects {
		if !obj.Visible() {
			continue
		}
		min := obj.MinSize()
		obj.Resize(fyne.NewSize(size.Width, min.Height))
		obj.Move(fyne.NewPos(0, y))
		y += min.Height + l.Gap
	}
}

func showAdaptiveConnectionDialog(parent fyne.Window, dialogTitle, subtitle string, headerIcon fyne.Resource, feedback fyne.CanvasObject, form fyne.CanvasObject, connectBtn, saveBtn, deleteBtn fyne.CanvasObject, footer ...fyne.CanvasObject) *widget.PopUp {
	title := view.NewBrandText(dialogTitle, 13, design.ColorTextLight, true)

	cancelRes := fyne.NewStaticResource("dialog_cancel.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#8f9381"><path d="M19 6.41 17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z"/></svg>`))
	closeBtn := newConnectionDialogIconButton(cancelRes, nil)

	var titleCol fyne.CanvasObject = title
	if strings.TrimSpace(subtitle) != "" {
		// widget.Label (wrapping), not canvas.Text -- canvas.Text can't wrap
		// at all, so a subtitle this long rendered as one unbroken line
		// wider than the dialog itself. Centered (see below), an
		// overflowing element straddles both edges evenly, which is what
		// actually caused the "stuck to the left edge" look -- it wasn't a
		// missing left inset, the text was simply wider than the box it was
		// supposedly centered in.
		subtitleLbl := widget.NewLabel(subtitle)
		subtitleLbl.Wrapping = fyne.TextWrapWord
		subtitleThemed := container.NewThemeOverride(subtitleLbl, &mutedForegroundTheme{design.NewBrandTheme()})
		nudgedSubtitle := container.New(&subtitleLeftNudgeLayout{Amount: 8}, subtitleThemed)
		titleCol = container.New(&tightHeaderVBoxLayout{Gap: -2}, title, nudgedSubtitle)
	}

	// titleCol renders as-is here, not container.NewCenter(titleCol) --
	// Border already stretches this middle slot to fill the available
	// width/height on its own, and centering on top of that is what broke
	// wrapping (a widget.Label centered at its own natural size never
	// actually gets the full width to wrap against) and is also just wrong
	// for this header: title/subtitle read left-aligned in the reference,
	// not centered.
	var titleBar fyne.CanvasObject
	if headerIcon != nil {
		iconImg := canvas.NewImageFromResource(headerIcon)
		iconImg.FillMode = canvas.ImageFillContain
		iconImg.SetMinSize(fyne.NewSize(22, 22))
		iconTile := canvas.NewRectangle(design.ColorGray950)
		iconTile.CornerRadius = design.RadiusMD
		iconTile.StrokeColor = design.ColorTailscaleChipBorder
		iconTile.StrokeWidth = 1
		iconBox := container.NewStack(iconTile, container.NewCenter(iconImg))
		iconBoxSized := container.NewGridWrap(fyne.NewSize(44, 44), iconBox)
		titleBar = container.NewBorder(nil, nil, iconBoxSized, nil, titleCol)
	} else {
		titleBar = titleCol
	}

	// A thin teal-to-lime gradient line along the panel's very top edge --
	// same two accent colors (design.ColorConnectionBadgeText/
	// ColorConnectionAddFill) the rest of this screen already uses for its
	// "commit" actions (Save/Connect), just as a hairline instead of a fill.
	// Faded to transparent at both ends (3 segments, not 1 flat gradient)
	// rather than run edge-to-edge -- a hard-edged bar butting straight into
	// the panel's rounded top corners read as crooked/misaligned there; a
	// fade reads as intentional regardless of exactly where it meets the
	// curve.
	teal := design.ColorConnectionBadgeText
	lime := design.ColorConnectionAddFill
	tealTransparent := color.NRGBA{R: 0x41, G: 0xe0, B: 0xc3, A: 0}
	limeTransparent := color.NRGBA{R: 0xc4, G: 0xe7, B: 0x7a, A: 0}
	accentLeftFade := canvas.NewHorizontalGradient(tealTransparent, teal)
	accentLeftFade.SetMinSize(fyne.NewSize(70, 2))
	accentRightFade := canvas.NewHorizontalGradient(lime, limeTransparent)
	accentRightFade.SetMinSize(fyne.NewSize(70, 2))
	accentMid := canvas.NewHorizontalGradient(teal, lime)
	topAccent := container.NewBorder(nil, nil, accentLeftFade, accentRightFade, accentMid)

	// Cancel sits at the opposite end of the footer from Connect/Save --
	// same close action as the header's X, just also reachable from where
	// the eye naturally lands after filling in the form. connectBtn/saveBtn/
	// deleteBtn (whichever are non-nil) group together at the right instead
	// of stretching to fill the row -- these are compact pill buttons now,
	// not full-width ones.
	cancelBtn := &connectionDialogSecondaryButton{
		labelText:      i18n.Current.Cancel,
		compact:        true,
		fillColor:      color.Transparent,
		borderColor:    color.Transparent,
		textColor:      color.NRGBA{R: 0x8f, G: 0x93, B: 0x81, A: 0xff},
		hoverFillColor: color.Transparent,
		hoverTextColor: design.ColorTextLight,
	}
	cancelBtn.ExtendBaseWidget(cancelBtn)

	buttonItems := make([]fyne.CanvasObject, 0, 4)
	if deleteBtn != nil && saveBtn != nil && connectBtn == nil {
		buttonItems = append(buttonItems, deleteBtn, saveBtn)
	} else if connectBtn != nil && saveBtn != nil && deleteBtn == nil {
		buttonItems = append(buttonItems, connectBtn, saveBtn)
	} else {
		if connectBtn != nil {
			buttonItems = append(buttonItems, connectBtn)
		}
		if saveBtn != nil {
			buttonItems = append(buttonItems, saveBtn)
		}
		if deleteBtn != nil {
			buttonItems = append(buttonItems, deleteBtn)
		}
	}
	rightGroup := container.New(&view.DeviceRowControlsLayout{Gap: connectionDialogButtonsGap}, buttonItems...)
	buttons := container.NewBorder(nil, nil, container.NewCenter(cancelBtn), rightGroup)

	// Scrollable area contains only the form (and optional feedback).
	// Buttons are placed OUTSIDE the scroll so they stay visible when the
	// keyboard is open and the scroll area is compressed.
	scrollItems := make([]fyne.CanvasObject, 0, 2)
	if feedback != nil {
		scrollItems = append(scrollItems, container.NewCenter(feedback))
	}
	scrollItems = append(scrollItems, form)
	scrollBody := container.NewVBox(scrollItems...)
	scroll := container.NewVScroll(scrollBody)
	// Set scroll min-height to the form content height so the panel reports the correct
	// preferred size (compact, content-sized). The panel is still capped at maxHeight in
	// connectionDialogPanelSize, so when the Android IME keyboard opens and maxHeight
	// shrinks, the panel shrinks too and the scroll becomes scrollable.
	scroll.SetMinSize(fyne.NewSize(0, scrollBody.MinSize().Height))

	bg := canvas.NewRectangle(design.ColorGray900)
	bg.CornerRadius = design.RadiusMD

	border := canvas.NewRectangle(color.Transparent)
	border.CornerRadius = design.RadiusMD
	border.StrokeColor = design.ColorBorder
	border.StrokeWidth = 1

	sep := canvas.NewRectangle(color.NRGBA{R: 0x30, G: 0x34, B: 0x2e, A: 0xff})
	sep.SetMinSize(fyne.NewSize(0, 1))

	sepFooter := canvas.NewRectangle(color.NRGBA{R: 0x30, G: 0x34, B: 0x2e, A: 0xff})
	sepFooter.SetMinSize(fyne.NewSize(0, 1))

	// Panel layout: title fixed at top, buttons fixed at bottom, scroll fills
	// center. titleBar/buttons/scroll each carry their own 18px left/right
	// inset now (rather than one uniform inset around all of `inner`), so
	// topAccent and sep -- both raw/uninset, sharing headerBlock's VBox with
	// titleBar -- stay full-bleed to the panel's left/right edges while
	// titleBar still reads with its own margin: container.NewVBox resizes
	// every child to the same full width regardless of what any *other*
	// child's own inset bakes in, which is what makes mixing full-bleed and
	// inset children in one VBox work here.
	// top=18 (not 10): headerBlock only contributes topAccent's own 2px
	// before this, well short of the ~22px total gap the panel used to have
	// when the outer wrap alone supplied 12px of top padding here -- 18+2
	// gets back to roughly that same breathing room above the title.
	// right=44 (not 18): reserves clearance so title/subtitle text never
	// runs under closeBtn, which -- see cornerBtn below -- sits closer to
	// the panel's actual corner than this 18px content margin reaches.
	headerBlock := container.New(&tightHeaderVBoxLayout{Gap: 0}, topAccent, view.NewInset(titleBar, 21, 44, 9, 4), sep)

	footerBlock := container.NewVBox(
		sepFooter,
		view.NewInset(buttons, 12, 18, 14, 0),
	)

	inner := container.NewBorder(
		headerBlock,
		footerBlock,
		nil, nil,
		view.NewInset(scroll, 18, 18, 0, 0),
	)
	// closeBtn sits on its own layer, pinned close to the panel's actual
	// top-right corner (10px top, 12px right) rather than sharing
	// titleBar's own 18px content margin -- decoupled from the title's own
	// vertical rhythm entirely, so it reads as a corner control instead of
	// as part of the header text block. Not view.NewInset: that pads
	// *around* content sized to fit it, but this layer gets handed the
	// *entire* panel size (it's a sibling of bg/border in the Stack below)
	// -- NewInset's Border-based padding would stretch closeBtn itself to
	// fill that whole box instead of leaving it at its own natural size in
	// the corner.
	cornerBtn := container.New(&dialogCornerButtonLayout{Top: 12, Right: 12}, closeBtn)
	panel := container.NewStack(
		bg,
		view.NewInset(inner, 0, 0, 0, 16),
		cornerBtn,
		border,
	)

	var specFooter fyne.CanvasObject
	if len(footer) > 0 {
		specFooter = footer[0]
	}
	popup := view.ShowOverlayPopup(parent, view.OverlayPopupSpec{
		Panel:    panel,
		Footer:   specFooter,
		DimColor: connectionDialogDimColor(),
		PanelSize: func(canvasSize fyne.Size, panel fyne.CanvasObject) fyne.Size {
			return connectionDialogPanelSize(panel, canvasSize)
		},
		PanelPos: func(canvasSize fyne.Size, panelSize fyne.Size) fyne.Position {
			if view.UseCompactLayout(canvasSize.Width) {
				topMargin := clampFloat32(canvasSize.Height*0.10, 80, 110)
				return fyne.NewPos((canvasSize.Width-panelSize.Width)/2, topMargin)
			}
			centerY := (canvasSize.Height - panelSize.Height) / 2
			return fyne.NewPos((canvasSize.Width-panelSize.Width)/2, centerY)
		},
	})
	closeBtn.onTapped = func() {
		popup.Hide()
	}
	cancelBtn.onTapped = func() {
		popup.Hide()
	}
	return popup
}

type gapTwoColumnsLayout struct {
	Gap float32
}

func (l *gapTwoColumnsLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var maxW, maxH float32
	var count int
	for _, obj := range objects {
		if !obj.Visible() {
			continue
		}
		count++
		min := obj.MinSize()
		if min.Width > maxW {
			maxW = min.Width
		}
		if min.Height > maxH {
			maxH = min.Height
		}
	}
	width := maxW * float32(count)
	if count > 1 {
		width += float32(count-1) * l.Gap
	}
	return fyne.NewSize(width, maxH)
}

func (l *gapTwoColumnsLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	var count int
	for _, obj := range objects {
		if obj.Visible() {
			count++
		}
	}
	if count == 0 {
		return
	}

	colW := (size.Width - float32(count-1)*l.Gap) / float32(count)
	x := float32(0)
	for _, obj := range objects {
		if !obj.Visible() {
			continue
		}
		obj.Resize(fyne.NewSize(colW, size.Height))
		obj.Move(fyne.NewPos(x, 0))
		x += colW + l.Gap
	}
}

type matchHeightLayout struct {
	target fyne.CanvasObject
}

func (l *matchHeightLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, obj := range objects {
		obj.Resize(size)
		obj.Move(fyne.NewPos(0, 0))
	}
}

func (l *matchHeightLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	min := fyne.NewSize(0, 0)
	for _, obj := range objects {
		m := obj.MinSize()
		if m.Width > min.Width {
			min.Width = m.Width
		}
		if m.Height > min.Height {
			min.Height = m.Height
		}
	}
	if l.target != nil {
		tm := l.target.MinSize()
		if tm.Height > min.Height {
			min.Height = tm.Height
		}
	}
	return min
}

func showConnectionEditorDialog(parent fyne.Window, window fyne.Window, spec connectionDialogSpec) *widget.PopUp {
	// The same Name/LAN/TS/Token box (labels, placeholders, copy/paste
	// icons, dark styling) the Grid card's inline edit and List's split-edit
	// panel already use for LAN/TS/Token -- includeName=true is this
	// dialog's own addition (Grid/List keep Name as their own separate row
	// outside this box, so they pass false); entryWidth 0 so each entry
	// fills its row instead of sitting at their fixed 160px (this dialog is
	// much wider than either).
	statsBox, nameEntry, lanEntry, tsEntry, tokenEntry := view.NewConnectionCardEditableStatsBox(
		true, spec.nameValue, spec.internalHostValue, spec.tailscaleHostValue, spec.masterKeyValue, 0,
	)

	registerCheck := newConnectionDialogRegisterRow(
		spec.tailscaleRegisterValue && tailscaleRegisterUISupported(),
		"Tailscale",
		"After connection, the redirect will open on the web.",
		"AUTO-REGISTRATION",
		nil,
	)
	registerCheckContainer := container.NewVBox(registerCheck)
	updateRegisterVisibility := func(tsHost string) {
		if tailscaleRegisterUISupported() && strings.TrimSpace(tsHost) == "" {
			registerCheckContainer.Show()
		} else {
			registerCheckContainer.Hide()
		}
		registerCheckContainer.Refresh()
	}
	updateRegisterVisibility(spec.tailscaleHostValue)
	tsEntry.OnChanged = func(text string) {
		updateRegisterVisibility(text)
	}

	// formSwap holds exactly one child at a time -- statsBox (the Name/LAN/
	// TS/Token fields) normally, swapped out for the inline paste view
	// (below) while "Paste Link" is active, and back again on Cancel/Apply.
	// A single-child Stack sizes to that one child's own MinSize, so the
	// panel reflows to whichever is showing (see d.Refresh() calls at each
	// swap site). registerCheckContainer sits OUTSIDE this swap, in
	// buildConnectionDialogForm below -- whether we know the connection is
	// LAN-only (so Tailscale auto-register applies) isn't something opening
	// the paste view changes; only actually applying a pasted link (which
	// runs tsEntry through the same OnChanged/updateRegisterVisibility path
	// as typing) should hide or show it.
	formSwap := container.NewStack(statsBox)
	normalForm := buildConnectionDialogForm(formSwap, registerCheckContainer)

	var d *widget.PopUp

	var formContent fyne.CanvasObject = normalForm
	var mobileFooter fyne.CanvasObject
	if spec.onQR != nil {
		qrBtn := newConnectionDialogWideActionButton("Scan QR", assets.QRCodeTeal, design.ColorConnectionBadgeText, func() {
			if d != nil {
				d.Hide()
			}
			spec.onQR()
		})

		showNormalFields := func() {
			formSwap.Objects = []fyne.CanvasObject{statsBox}
			formSwap.Refresh()
			if d != nil {
				d.Refresh()
			}
		}
		pasteViewBase := newConnectionDialogInlinePasteView(parent, func(ih, th, mk string) {
			lanEntry.SetText(ih)
			tsEntry.SetText(th)
			tokenEntry.SetText(mk)
			showNormalFields()
		}, showNormalFields)
		pasteView := container.New(&matchHeightLayout{target: statsBox}, pasteViewBase)
		if spec.startWithPasteLink {
			formSwap.Objects = []fyne.CanvasObject{pasteView}
		}

		linkBtn := newConnectionDialogWideActionButton("Paste Link", assets.LinkIconLime, design.ColorConnectionAddFill, func() {
			formSwap.Objects = []fyne.CanvasObject{pasteView}
			formSwap.Refresh()
			if d != nil {
				d.Refresh()
			}
		})
		// Full width (equal split, GridWithColumns), not the buttons' own
		// natural/content width -- they should span the same width as the
		// fields below them; compact keeps their icon/text/height small
		// regardless of how wide the button box around that content ends up.
		iconRow := container.New(&gapTwoColumnsLayout{Gap: 9}, qrBtn, linkBtn)

		if view.UseCompactLayout(parent.Canvas().Size().Width) {
			// On Android/narrow browser viewports: icon row floats OUTSIDE
			// the popup panel, below it. The panel itself stays compact and
			// never moves when keyboard opens.
			mobileFooter = iconRow
		} else {
			// On desktop: QR/Paste sit above the form, not below it -- the
			// fast paths (scan/paste) read first, "OR ENTER MANUALLY" makes
			// the fallback explicit before the fields that fallback fills.
			formContent = container.NewVBox(
				// top=14 -- iconRow sat right under the header divider with
				// nothing between them otherwise.
				view.NewInset(iconRow, 0, 0, 14, 0),
				view.NewInset(newConnectionDialogManualDivider(), 0, 0, 14, 10),
				normalForm,
			)
		}
	}

	var feedback fyne.CanvasObject
	if spec.feedbackText != "" {
		fill := spec.feedbackColor
		if fill == nil {
			fill = design.ColorAccent
		}
		feedback = newConnectionDialogFeedback(spec.feedbackText, fill)
	}

	saveLabel := spec.saveLabel
	if saveLabel == "" {
		saveLabel = i18n.Current.DeepLinkSave
	}

	deleteLabel := spec.deleteLabel
	if deleteLabel == "" {
		deleteLabel = i18n.Current.DeleteButton
	}

	var connectBtn fyne.CanvasObject
	var deleteBtn fyne.CanvasObject
	var saveBtn fyne.CanvasObject

	if spec.onConnect != nil {
		connectLabel := spec.connectLabel
		if connectLabel == "" {
			connectLabel = i18n.Current.DeepLinkConnect
		}
		cIcon := spec.connectIcon
		if cIcon == nil {
			cIcon = assets.ConnectIcon
		}
		cBtn := &connectionDialogSecondaryButton{
			labelText: connectLabel,
			onTapped: func() {
				if spec.onConnect != nil && !spec.onConnect(nameEntry.Text, lanEntry.Text, tsEntry.Text, tokenEntry.Text, registerCheck.Checked) {
					return
				}
				if d != nil {
					d.Hide()
				}
			},
			compact:          true,
			fillColor:        color.NRGBA{R: 0x22, G: 0x26, B: 0x2a, A: 0xff},
			borderColor:      design.ColorTailscaleChipBorder,
			textColor:        design.ColorConnectionAddFill,
			hoverFillColor:   color.NRGBA{R: 0x31, G: 0x35, B: 0x39, A: 0xff},
			hoverTextColor:   design.ColorConnectionAddFill,
			hoverBorderColor: design.ColorConnectionAddFill,
			iconRes:          cIcon,
			hoverIconRes:     cIcon,
		}
		cBtn.ExtendBaseWidget(cBtn)
		connectBtn = cBtn
	}

	sIcon := fyne.NewStaticResource("floppy-disk.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="#111111" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"/><polyline points="17 21 17 13 7 13 7 21"/><polyline points="7 3 7 8 15 8"/></svg>`))
	sBtn := &connectionDialogSecondaryButton{
		labelText: saveLabel,
		onTapped: func() {
			if spec.onSave != nil && !spec.onSave(nameEntry.Text, lanEntry.Text, tsEntry.Text, tokenEntry.Text, registerCheck.Checked) {
				return
			}
			if d != nil {
				d.Hide()
			}
		},
		compact:          true,
		fillColor:        design.ColorConnectionBadgeText,
		borderColor:      color.Transparent,
		textColor:        design.ColorGray950,
		hoverFillColor:   color.NRGBA{R: 0x61, G: 0xf0, B: 0xd3, A: 0xff},
		hoverTextColor:   design.ColorGray950,
		hoverBorderColor: color.Transparent,
		iconRes:          sIcon,
		hoverIconRes:     sIcon,
	}
	sBtn.ExtendBaseWidget(sBtn)
	saveBtn = sBtn

	if spec.onDelete != nil {
		btn := newConnectionDialogDangerSecondaryButton(deleteLabel, theme.DeleteIcon(), func() {
			spec.onDelete(func() {
				if d != nil {
					d.Hide()
				}
			})
		})
		deleteBtn = btn
	}

	d = showAdaptiveConnectionDialog(parent, spec.title, spec.subtitle, spec.icon, feedback, formContent, connectBtn, saveBtn, deleteBtn, mobileFooter)
	return d
}

// newConnectionDialogWideActionButton is the QR/Paste Link row's shared
// look: a small bordered pill (neutral gray border, bold white label, an
// accent-tinted icon), sized to its own content -- not the full-width
// square-icon-over-label block newConnectionDialogIconBlock used to render
// here, which read as oversized next to the rest of this dialog.
// hoverBorder is the border color this button's own hover swaps to (teal
// for Scan QR, lime for Paste Link) -- on top of, not instead of, the
// whole-dialog hover state some other bordered rows in this file have.
func newConnectionDialogWideActionButton(label string, iconRes fyne.Resource, hoverBorder color.Color, onTapped func()) *connectionDialogSecondaryButton {
	btn := &connectionDialogSecondaryButton{
		labelText:        label,
		onTapped:         onTapped,
		compact:          true,
		fillColor:        color.Transparent,
		borderColor:      design.ColorTailscaleChipBorder,
		textColor:        design.ColorTextLight,
		hoverFillColor:   color.NRGBA{R: 0x26, G: 0x2a, B: 0x2e, A: 0xff},
		hoverTextColor:   design.ColorTextLight,
		hoverBorderColor: hoverBorder,
		iconRes:          iconRes,
		hoverIconRes:     iconRes,
	}
	btn.ExtendBaseWidget(btn)
	return btn
}

// newConnectionDialogManualDivider is the "OR ENTER MANUALLY" line
// separating the QR/Paste shortcuts from the fields they'd otherwise fill.
func newConnectionDialogManualDivider() fyne.CanvasObject {
	line := canvas.NewRectangle(color.NRGBA{R: 0x30, G: 0x34, B: 0x2e, A: 0xff})
	line.SetMinSize(fyne.NewSize(1, 1))

	label := canvas.NewText("OR ENTER MANUALLY", color.NRGBA{R: 0x8f, G: 0x93, B: 0x81, A: 0xff})
	label.TextSize = 9
	label.TextStyle = fyne.TextStyle{Monospace: true}

	// Not container.NewBorder(nil,nil,nil,label,line) -- Border's center
	// slot (line, here) always stretches to fill the *whole* row height,
	// not just its own MinSize, so line rendered as thick as the label's
	// text line (~13px) instead of the 1px it actually asked for.
	// thinDividerLayout keeps line's own height (1px) regardless of how
	// tall the label next to it is.
	return container.New(&thinDividerLayout{gap: 10}, line, label)
}

// thinDividerLayout lays out exactly 2 children: a line that fills all the
// row's width except what the label (right-pinned, its own natural size)
// needs -- and, unlike container.NewBorder's center slot, keeps the line at
// its own MinSize height (vertically centered in the row) instead of
// stretching it to match the row's height.
type thinDividerLayout struct {
	gap float32
}

func (l *thinDividerLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) < 2 {
		return fyne.NewSize(0, 0)
	}
	line, label := objects[0], objects[1]
	lineMin := line.MinSize()
	labelMin := label.MinSize()
	height := maxFloat32(lineMin.Height, labelMin.Height)
	return fyne.NewSize(lineMin.Width+l.gap+labelMin.Width, height)
}

func (l *thinDividerLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 2 {
		return
	}
	line, label := objects[0], objects[1]

	labelMin := label.MinSize()
	labelX := maxFloat32(0, size.Width-labelMin.Width)
	label.Move(fyne.NewPos(labelX, (size.Height-labelMin.Height)/2))
	label.Resize(labelMin)

	lineHeight := line.MinSize().Height
	if lineHeight <= 0 {
		lineHeight = 1
	}
	lineWidth := maxFloat32(0, labelX-l.gap)
	line.Move(fyne.NewPos(0, (size.Height-lineHeight)/2))
	line.Resize(fyne.NewSize(lineWidth, lineHeight))
}

func showQuickConnectQRCode(window fyne.Window, internalHost, tailscaleHost, masterKey string) {
	if window == nil {
		return
	}
	qrURL := buildServiceQRFormat(internalHost, tailscaleHost, masterKey)
	pngBytes, err := qrcode.Encode(qrURL, qrcode.Medium, 280)
	if err != nil {
		logrus.Errorf("failed to render quick QR: %v", err)
		return
	}

	resource := fyne.NewStaticResource("quick-connect-qr.png", pngBytes)
	image := canvas.NewImageFromResource(resource)
	image.FillMode = canvas.ImageFillContain
	image.SetMinSize(fyne.NewSize(280, 280))

	linkEntry := widget.NewEntry()
	linkEntry.SetText(qrURL)
	linkEntry.Disable()

	copyBtn := view.NewConnectionPrimaryButton("Copy Link", func() {
		window.Clipboard().SetContent(qrURL)
	})
	copyBtn.SetAccent(true)

	title := view.NewBrandText("Quick Connect", 19, design.ColorTextLight, true)
	title.Alignment = fyne.TextAlignCenter

	var popup *widget.PopUp
	closeBtn := newConnectionDialogIconButton(theme.CancelIcon(), func() {
		if popup != nil {
			popup.Hide()
		}
	})
	titleBar := container.New(&connectionDialogTitleLayout{}, title, closeBtn)

	content := container.NewVBox(
		titleBar,
		view.NewInset(container.NewCenter(image), 0, 0, 10, 10),
		view.NewInset(linkEntry, 0, 0, 0, 14),
		copyBtn,
	)

	bg := canvas.NewRectangle(design.ColorGray900)
	bg.CornerRadius = design.RadiusMD

	border := canvas.NewRectangle(color.Transparent)
	border.CornerRadius = design.RadiusMD
	border.StrokeColor = design.ColorBorder
	border.StrokeWidth = 1

	panel := container.NewStack(
		bg,
		view.NewInset(content, 18, 18, 16, 16),
		border,
	)

	popup = view.ShowOverlayPopup(window, view.OverlayPopupSpec{
		Panel:    panel,
		DimColor: connectionDialogDimColor(),
		PanelSize: func(canvasSize fyne.Size, panel fyne.CanvasObject) fyne.Size {
			panelMin := panel.MinSize()
			width := minFloat32(maxFloat32(panelMin.Width, 360), canvasSize.Width-24)
			height := minFloat32(maxFloat32(panelMin.Height, 460), canvasSize.Height-24)
			return fyne.NewSize(width, height)
		},
	})
}

func buildServiceQRFormat(internalHost, tailscaleHost, masterKey string) string {
	values := url.Values{}
	if strings.TrimSpace(internalHost) != "" {
		values.Set("internal_host", strings.TrimSpace(internalHost))
	}
	if strings.TrimSpace(tailscaleHost) != "" {
		values.Set("tailscale_host", strings.TrimSpace(tailscaleHost))
	}
	values.Set("master_key", strings.TrimSpace(masterKey))
	if strings.TrimSpace(tailscaleHost) != "" {
		values.Set("protocol", models.ConnectionProtocolTailscale)
	} else {
		values.Set("protocol", models.ConnectionProtocolDirect)
	}
	return fmt.Sprintf("usbridge://connect?%s", values.Encode())
}

func resolveHostForDialog(protocol, internalHost, tailscaleHost string) string {
	switch normalizeConnectionProtocol(protocol) {
	case models.ConnectionProtocolTailscale:
		return fallbackText(tailscaleHost, internalHost)
	default:
		return fallbackText(internalHost, tailscaleHost)
	}
}

// showEditDialog was List mode's pencil action -- opening this as a popup
// overlay. List now uses the split-edit layout instead (see
// connection_manager_list_edit.go's buildListEditPanel), so this has no
// callers left. Left in place rather than deleted in the same pass that
// introduced its replacement; showConnectionEditorDialog/
// showAdaptiveConnectionDialog underneath it are still very much alive --
// showPrefilledAddDialog (Add-connection) uses them too.
func (cm *ConnectionManager) showEditDialog(idx int) {
	if idx < 0 || idx >= len(cm.connections) {
		return
	}
	conn := cm.connections[idx]

	showConnectionEditorDialog(cm.window, cm.window, connectionDialogSpec{
		title:                  i18n.Current.EditConnectionTitle,
		saveLabel:              i18n.Current.DeepLinkSave,
		deleteLabel:            i18n.Current.DeleteButton,
		nameValue:              conn.Name,
		internalHostValue:      conn.InternalHost,
		tailscaleHostValue:     conn.TailscaleHost,
		masterKeyValue:         conn.MasterKey,
		tailscaleRegisterValue: conn.TailscaleRegister,
		onSave: func(name, internalHost, tailscaleHost, masterKey string, tailscaleRegister bool) bool {
			internalHost = strings.TrimSpace(internalHost)
			tailscaleHost = strings.TrimSpace(tailscaleHost)
			if name == "" || (internalHost == "" && tailscaleHost == "") {
				logrus.Warn("name and at least one address are required")
				return false
			}

			cm.connections[idx] = SavedConnection{
				Name:              name,
				InternalHost:      internalHost,
				TailscaleHost:     tailscaleHost,
				Host:              fallbackText(internalHost, tailscaleHost),
				MasterKey:         strings.TrimSpace(masterKey),
				Protocol:          conn.Protocol,
				TailscaleRegister: tailscaleRegister,
			}
			cm.selectedIndex = idx
			cm.saveConnections()
			fyne.Do(func() {
				cm.SelectConnection(idx)
				cm.refreshConnectionsList()
			})
			logrus.Infof("Updated connection: %s", name)
			return true
		},
		onDelete: func(close func()) {
			cm.handleDeleteConnection(idx, close)
		},
	})
}

func (cm *ConnectionManager) showAddDialog() {
	internalHost, tailscaleHost := splitHostByType(cm.hostEntry.Text)
	if selected := normalizeConnectionProtocol(cm.protocolSelect.Selected); selected == models.ConnectionProtocolTailscale {
		internalHost, tailscaleHost = "", strings.TrimSpace(cm.hostEntry.Text)
	}
	cm.showPrefilledAddDialog("", internalHost, tailscaleHost, cm.masterKeyEntry.Text, "", false, false)
}

func (cm *ConnectionManager) showPrefilledAddDialog(name, internalHost, tailscaleHost, masterKey, protocol string, scanned, startWithPasteLink bool) {
	feedbackText := ""
	if scanned {
		feedbackText = qrScanSuccessText
	}
	masterKey = strings.TrimSpace(masterKey)

	logrus.Infof("Opening add connection dialog: internal=%s tailscale=%s scanned=%v", internalHost, tailscaleHost, scanned)

	showConnectionEditorDialog(cm.window, cm.window, connectionDialogSpec{
		title:                  i18n.Current.AddConnectionTitle,
		subtitle:               "Pair a hardware or software agent using its IP address and master key.",
		connectLabel:           i18n.Current.DeepLinkConnect,
		connectIcon:            nil,
		saveLabel:              i18n.Current.DeepLinkSave,
		nameValue:              name,
		internalHostValue:      internalHost,
		tailscaleHostValue:     tailscaleHost,
		masterKeyValue:         masterKey, // from QR scan or prefill — treated as master key
		tailscaleRegisterValue: strings.TrimSpace(tailscaleHost) == "" && tailscaleRegisterUISupported(),
		feedbackText:           feedbackText,
		feedbackColor:          design.ColorAccent,
		startWithPasteLink:     startWithPasteLink,
		onConnect: func(name, internalHost, tailscaleHost, masterKey string, tailscaleRegister bool) bool {
			internalHost = strings.TrimSpace(internalHost)
			tailscaleHost = strings.TrimSpace(tailscaleHost)
			if internalHost == "" && tailscaleHost == "" {
				logrus.Warn("at least one address are required")
				return false
			}

			selectedProtocol := protocol
			if selectedProtocol == "" && cm.protocolSelect != nil {
				selectedProtocol = cm.protocolSelect.Selected
			}
			host := resolveHostForDialog(selectedProtocol, internalHost, tailscaleHost)

			fyne.Do(func() {
				cm.ClearSelection()
				cm.applyConnectionToForm(host, masterKey, selectedProtocol)
			})
			if cm.onConnect != nil {
				cm.onConnect(host, masterKey, selectedProtocol, tailscaleRegister)
			}
			return true
		},
		onSave: func(name, internalHost, tailscaleHost, masterKey string, tailscaleRegister bool) bool {
			internalHost = strings.TrimSpace(internalHost)
			tailscaleHost = strings.TrimSpace(tailscaleHost)
			if internalHost == "" && tailscaleHost == "" {
				logrus.Warn("at least one address are required")
				return false
			}

			selectedProtocol := protocol
			if selectedProtocol == "" && cm.protocolSelect != nil {
				selectedProtocol = cm.protocolSelect.Selected
			}
			host := resolveHostForDialog(selectedProtocol, internalHost, tailscaleHost)

			cm.SaveConnection(name, internalHost, tailscaleHost, masterKey, selectedProtocol, tailscaleRegister)
			fyne.Do(func() {
				cm.applyConnectionToForm(host, masterKey, selectedProtocol)
				cm.refreshConnectionsList()
			})
			return true
		},
		onQR: cm.handleQRScan,
	})
}

type connectionDialogIconButton struct {
	widget.BaseWidget

	resource   fyne.Resource
	onTapped   func()
	hovered    bool
	buttonSize fyne.Size
	iconSize   fyne.Size

	bg   *canvas.Rectangle
	bdr  *canvas.Rectangle
	icon *canvas.Image

	customNormalFill   color.Color
	customHoverFill    color.Color
	customNormalBorder color.Color
	customHoverBorder  color.Color
	opaqueIcon         bool
}

func newConnectionDialogIconButton(resource fyne.Resource, onTapped func()) *connectionDialogIconButton {
	btn := &connectionDialogIconButton{
		resource:   resource,
		onTapped:   onTapped,
		buttonSize: fyne.NewSize(28, 28),
		iconSize:   fyne.NewSize(18, 18),
	}
	btn.ExtendBaseWidget(btn)
	return btn
}

func newCompactConnectionDialogIconButton(resource fyne.Resource, onTapped func()) *connectionDialogIconButton {
	btn := newConnectionDialogIconButton(resource, onTapped)
	btn.buttonSize = fyne.NewSize(24, 24)
	btn.iconSize = fyne.NewSize(15, 15)
	return btn
}

func (b *connectionDialogIconButton) SetResource(resource fyne.Resource) {
	b.resource = resource
	b.refreshVisuals()
}

func (b *connectionDialogIconButton) Tapped(*fyne.PointEvent) {
	if b.onTapped != nil {
		b.onTapped()
	}
}

func (b *connectionDialogIconButton) TappedSecondary(*fyne.PointEvent) {}

func (b *connectionDialogIconButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.refreshVisuals()
}

func (b *connectionDialogIconButton) MouseMoved(*desktop.MouseEvent) {}

func (b *connectionDialogIconButton) MouseOut() {
	b.hovered = false
	b.refreshVisuals()
}

func (b *connectionDialogIconButton) Cursor() desktop.Cursor {
	return desktop.PointerCursor
}

func (b *connectionDialogIconButton) MinSize() fyne.Size {
	return b.buttonSize
}

func (b *connectionDialogIconButton) CreateRenderer() fyne.WidgetRenderer {
	b.bg = canvas.NewRectangle(color.Transparent)
	b.bg.CornerRadius = 6

	b.bdr = canvas.NewRectangle(color.Transparent)
	b.bdr.CornerRadius = 6
	b.bdr.StrokeWidth = 1

	b.icon = canvas.NewImageFromResource(b.resource)
	b.icon.FillMode = canvas.ImageFillContain
	b.icon.ScaleMode = canvas.ImageScaleSmooth
	b.icon.SetMinSize(b.iconSize)

	b.refreshVisuals()
	return widget.NewSimpleRenderer(container.NewMax(b.bg, b.bdr, container.NewCenter(b.icon)))
}

func (b *connectionDialogIconButton) refreshVisuals() {
	if b.bg == nil || b.bdr == nil || b.icon == nil {
		return
	}

	b.icon.Resource = b.resource

	if b.opaqueIcon {
		b.icon.Translucency = 0
	} else {
		b.icon.Translucency = 0.32
	}

	normFill := b.customNormalFill
	if normFill == nil {
		normFill = color.Transparent
	}
	normBorder := b.customNormalBorder
	if normBorder == nil {
		normBorder = color.Transparent
	}

	hovFill := b.customHoverFill
	if hovFill == nil {
		hovFill = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x10}
	}
	hovBorder := b.customHoverBorder
	if hovBorder == nil {
		hovBorder = color.NRGBA{R: 0x8f, G: 0x93, B: 0x81, A: 0xff}
	}

	if b.hovered {
		b.bg.FillColor = hovFill
		b.bdr.StrokeColor = hovBorder
		if !b.opaqueIcon {
			b.icon.Translucency = 0.08
		}
	} else {
		b.bg.FillColor = normFill
		b.bdr.StrokeColor = normBorder
	}

	b.bg.Refresh()
	b.icon.Refresh()
}

type connectionDialogButtonsLayout struct {
	gap float32
}

func (l *connectionDialogButtonsLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 2 {
		return
	}

	left := objects[0]
	right := objects[1]
	width := (size.Width - l.gap) / 2
	if width < 0 {
		width = 0
	}

	left.Move(fyne.NewPos(0, 0))
	left.Resize(fyne.NewSize(width, size.Height))

	right.Move(fyne.NewPos(width+l.gap, 0))
	right.Resize(fyne.NewSize(width, size.Height))
}

func (l *connectionDialogButtonsLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) < 2 {
		return fyne.NewSize(0, 0)
	}
	leftMin := objects[0].MinSize()
	rightMin := objects[1].MinSize()
	height := maxFloat32(leftMin.Height, rightMin.Height)
	return fyne.NewSize(leftMin.Width+rightMin.Width+l.gap, height)
}

// dialogCornerButtonLayout pins its one child at its own natural size, Top
// below and Right in from the top-right corner of whatever size it's
// actually handed -- unlike view.NewInset (Border-based padding), which
// stretches the child itself to fill the padded box, this is for a child
// placed in a much larger box (a Stack layer sized to the whole panel) that
// should stay pinned to a fixed corner regardless.
type dialogCornerButtonLayout struct {
	Top, Right float32
}

func (l *dialogCornerButtonLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	child := objects[0]
	childMin := child.MinSize()
	x := maxFloat32(0, size.Width-l.Right-childMin.Width)
	child.Move(fyne.NewPos(x, l.Top))
	child.Resize(childMin)
}

func (l *dialogCornerButtonLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, 0)
}

type connectionDialogTitleLayout struct{}

func (l *connectionDialogTitleLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 2 {
		return
	}

	title := objects[0]
	closeBtn := objects[1]
	titleMin := title.MinSize()
	closeMin := closeBtn.MinSize()

	title.Move(fyne.NewPos(maxFloat32(0, (size.Width-titleMin.Width)/2), maxFloat32(0, (size.Height-titleMin.Height)/2)))
	title.Resize(titleMin)

	closeBtn.Move(fyne.NewPos(maxFloat32(0, size.Width-closeMin.Width), maxFloat32(0, (size.Height-closeMin.Height)/2)))
	closeBtn.Resize(closeMin)
}

func (l *connectionDialogTitleLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) < 2 {
		return fyne.NewSize(0, 0)
	}

	titleMin := objects[0].MinSize()
	closeMin := objects[1].MinSize()
	width := maxFloat32(titleMin.Width, closeMin.Width*2) + closeMin.Width
	height := maxFloat32(titleMin.Height, closeMin.Height)
	return fyne.NewSize(width, height)
}

func connectionDialogPanelSize(panel fyne.CanvasObject, canvasSize fyne.Size) fyne.Size {
	margin := clampFloat32(minFloat32(canvasSize.Width, canvasSize.Height)*0.04, 20, 28)
	maxWidth := canvasSize.Width - margin*2
	maxHeight := canvasSize.Height - margin*2

	if maxWidth <= 0 {
		maxWidth = canvasSize.Width
	}
	if maxHeight <= 0 {
		maxHeight = canvasSize.Height
	}

	panelWidth := minFloat32(408, maxWidth)
	if panelWidth < 0 {
		panelWidth = 0
	}

	panelMin := panel.MinSize()
	panelHeight := panelMin.Height
	if panelHeight > maxHeight {
		// Cap at available space. On mobile, canvasSize is already reduced by the
		// IME keyboard height (via overlayPopupLayout), so this cap shrinks the panel
		// automatically when the keyboard opens.
		panelHeight = maxHeight
	}

	return fyne.NewSize(panelWidth, panelHeight)
}

func (cm *ConnectionManager) handleDeleteConnection(idx int, afterDelete func()) {
	if idx < 0 || idx >= len(cm.connections) {
		return
	}
	deletedName := cm.connections[idx].Name

	view.ShowConfirmYesLeft(
		i18n.Current.DeleteConnectionTitle,
		fmt.Sprintf(i18n.Current.DeleteConnectionConfirm, deletedName),
		func(confirmed bool) {
			if !confirmed {
				return
			}
			fyne.Do(func() {
				cm.window.Canvas().Focus(nil)
			})
			cm.connections = append(cm.connections[:idx], cm.connections[idx+1:]...)
			cm.selectedIndex = -1
			cm.editingGridIndex = -1
			cm.editingListIndex = -1
			cm.saveConnections()
			fyne.Do(func() {
				cm.applyConnectionToForm("", "", "")
				cm.refreshConnectionsList()
			})
			if afterDelete != nil {
				afterDelete()
			}
			logrus.Infof("Deleted connection: %s", deletedName)
		},
		cm.window,
	)
}

func (cm *ConnectionManager) handleQRScan() {
	logrus.Info("Opening QR scanner")
	cm.qrScanner.ShowCameraScanner(cm.window)
}

// newConnectionDialogInlinePasteView is what "Paste Link" swaps the
// Name/LAN/TS/Token box for, in place -- a single usbridge:// link field in
// that same card chrome (dark box, thin border, radius), with Cancel/Paste/
// Apply icon buttons underneath, instead of stacking a second modal on top
// of the Add Connection dialog just to paste one line of text. onApply
// receives the parsed hosts so the caller can refill the real fields and
// swap back to them; the X button (onCancel) discards whatever was typed
// and swaps back untouched.
type pasteEntryTheme struct {
	fyne.Theme
}

func (t *pasteEntryTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameInputBackground:
		return color.NRGBA{R: 255, G: 255, B: 255, A: 8}
	case theme.ColorNameFocus:
		return color.NRGBA{R: 255, G: 255, B: 255, A: 20}
	case theme.ColorNameInputBorder:
		return design.ColorConnectionBadgeText
	case theme.ColorNamePrimary:
		return design.ColorConnectionBadgeText
	case theme.ColorNamePlaceHolder:
		return design.ColorTextMuted
	case theme.ColorNameShadow:
		return color.Transparent
	}
	return t.Theme.Color(name, variant)
}

func (t *pasteEntryTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameInputBorder:
		return 1
	case theme.SizeNameInnerPadding:
		return 5
	case theme.SizeNameInputRadius:
		return 4
	case theme.SizeNameText:
		return 10
	}
	return t.Theme.Size(name)
}

func newConnectionDialogInlinePasteView(parent fyne.Window, onApply func(internalHost, tailscaleHost, masterKey string), onCancel func()) fyne.CanvasObject {
	entry := &connectionDialogEntry{}
	entry.MultiLine = true
	entry.Wrapping = fyne.TextWrapWord
	entry.TextStyle.Monospace = true
	entry.ExtendBaseWidget(entry)
	entry.SetPlaceHolder("usbridge://connect?...")
	entry.SetMinRowsVisible(3)

	errLabel := canvas.NewText("", color.NRGBA{R: 0xff, G: 0x5a, B: 0x52, A: 0xff})
	errLabel.TextSize = 10

	reset := func() {
		entry.SetText("")
		errLabel.Text = ""
		errLabel.Refresh()
	}

	entry.onFocusChanged = nil
	entry.OnChanged = func(_ string) {
		if errLabel.Text != "" {
			errLabel.Text = ""
			errLabel.Refresh()
		}
	}

	cancelRes := fyne.NewStaticResource("dialog_cancel.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#8f9381"><path d="M19 6.41 17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z"/></svg>`))
	closeBtn := newCompactConnectionDialogIconButton(cancelRes, func() {
		reset()
		onCancel()
	})
	closeBtn.opaqueIcon = true

	pasteBtn := newCompactConnectionDialogIconButton(theme.ContentPasteIcon(), func() {
		pasteClipboardInto(entry, parent)
	})
	pasteBtn.customNormalBorder = design.ColorTailscaleChipBorder

	checkRes := fyne.NewStaticResource("check.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#111111"><path d="M9 16.2 4.8 12l-1.4 1.4L9 19 21 7l-1.4-1.4L9 16.2z"/></svg>`))
	applyBtn := newCompactConnectionDialogIconButton(checkRes, func() {
		ih, th, mk, _, err := parseQRContents(entry.Text)
		if err != nil {
			errLabel.Text = "Invalid link format"
			errLabel.Refresh()
			return
		}
		reset()
		onApply(ih, th, mk)
	})
	applyBtn.opaqueIcon = true
	applyBtn.customNormalFill = design.ColorConnectionBadgeText
	applyBtn.customHoverFill = color.NRGBA{R: 0x61, G: 0xf0, B: 0xd3, A: 0xff}
	applyBtn.customNormalBorder = color.Transparent
	applyBtn.customHoverBorder = color.Transparent

	actionsLeft := container.NewHBox(closeBtn, view.NewInset(container.NewCenter(errLabel), 8, 0, 0, 0))
	actionsRight := container.NewHBox(pasteBtn, applyBtn)
	actionsRow := container.NewBorder(nil, nil, actionsLeft, actionsRight)

	bg := canvas.NewRectangle(design.ColorGray950)
	bg.CornerRadius = 6
	bg.StrokeColor = design.ColorTailscaleChipBorder
	bg.StrokeWidth = 1

	entryThemed := container.NewThemeOverride(entry, &pasteEntryTheme{Theme: design.NewBrandTheme()})
	body := container.NewBorder(nil, view.NewInset(actionsRow, 0, 0, 4, 0), nil, nil, entryThemed)
	return container.NewStack(bg, view.NewInset(body, 12, 12, 10, 8))
}

func (cm *ConnectionManager) handlePasteLink() {
	// Straight into the Add Connection dialog with the paste view already
	// showing -- no more separate popup that then reopens this same dialog
	// on apply (see connectionDialogSpec.startWithPasteLink).
	cm.showPrefilledAddDialog("", "", "", "", "", false, true)
}
