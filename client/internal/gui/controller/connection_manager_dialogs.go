package controller

import (
	"fmt"
	"image/color"
	"net/url"
	"runtime"
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
	title                  string
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
}

type connectionDialogSecondaryButton struct {
	widget.BaseWidget

	labelText      string
	onTapped       func()
	hovered        bool
	fillColor      color.Color
	borderColor    color.Color
	textColor      color.Color
	hoverFillColor color.Color
	hoverTextColor color.Color
	iconRes        fyne.Resource
	hoverIconRes   fyne.Resource
	bg             *canvas.Rectangle
	border         *canvas.Rectangle
	label          *canvas.Text
	icon           *canvas.Image
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

func (b *connectionDialogSecondaryButton) CreateRenderer() fyne.WidgetRenderer {
	b.bg = canvas.NewRectangle(color.Transparent)
	b.bg.CornerRadius = design.RadiusMD

	b.border = canvas.NewRectangle(color.Transparent)
	b.border.CornerRadius = design.RadiusMD
	b.border.StrokeColor = b.borderColor
	b.border.StrokeWidth = 1

	b.label = canvas.NewText(b.labelText, b.textColor)
	b.label.TextSize = 16
	b.label.TextStyle.Bold = true
	b.label.Alignment = fyne.TextAlignCenter

	b.icon = canvas.NewImageFromResource(b.iconRes)
	b.icon.FillMode = canvas.ImageFillContain
	if b.iconRes == nil {
		b.icon.Hide()
	} else {
		b.icon.SetMinSize(fyne.NewSize(18, 18))
	}

	b.refreshVisuals()
	content := container.NewCenter(container.NewHBox(
		b.icon,
		view.NewInset(b.label, 6, 0, 0, 0),
	))
	if b.iconRes == nil {
		content = container.NewCenter(b.label)
	}
	return widget.NewSimpleRenderer(container.NewMax(b.bg, content, b.border))
}

func (b *connectionDialogSecondaryButton) MinSize() fyne.Size {
	return fyne.NewSize(0, 36)
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
	}
	if b.borderColor != nil && b.borderColor != color.Transparent {
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

// noInputBgTheme makes a widget.Entry render without its own background/border
// so that a custom styled container can provide the visual shell instead.
type noInputBgTheme struct{ fyne.Theme }

func (t *noInputBgTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameInputBackground || name == theme.ColorNameInputBorder || name == theme.ColorNameFocus {
		return color.Transparent
	}
	return t.Theme.Color(name, variant)
}

// newConnectionDialogIconEntry wraps an entry in a Bootstrap-style input-group:
// a styled dark rounded container with a leading icon and optional trailing actions.
func newConnectionDialogIconEntry(iconRes fyne.Resource, entry *connectionDialogEntry, trailingAction fyne.CanvasObject) fyne.CanvasObject {
	iconImg := canvas.NewImageFromResource(iconRes)
	iconImg.FillMode = canvas.ImageFillContain
	iconImg.Translucency = 0.35

	iconWrap := container.NewCenter(container.NewGridWrap(fyne.NewSize(16, 16), iconImg))
	iconPad := view.NewInset(iconWrap, 10, 6, 0, 0)

	bg := canvas.NewRectangle(design.ColorSurfaceLight)
	bg.CornerRadius = design.RadiusMD
	bdr := canvas.NewRectangle(color.Transparent)
	bdr.CornerRadius = design.RadiusMD
	bdr.StrokeColor = design.ColorBorder
	bdr.StrokeWidth = 1

	themedEntry := container.NewThemeOverride(entry, &noInputBgTheme{design.NewBrandTheme()})

	entry.onFocusChanged = func(focused bool) {
		if focused {
			bdr.StrokeColor = design.ColorAccent
		} else {
			bdr.StrokeColor = design.ColorBorder
		}
		bdr.Refresh()
	}

	var inner fyne.CanvasObject
	if trailingAction != nil {
		inner = container.NewBorder(nil, nil, iconPad, view.NewInset(trailingAction, 6, 10, 0, 0), themedEntry)
	} else {
		inner = container.NewBorder(nil, nil, iconPad, nil, themedEntry)
	}
	return container.NewStack(bg, inner, bdr)
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

func newConnectionDialogCopyAction(entry *connectionDialogEntry, window fyne.Window) *connectionDialogIconButton {
	btn := &connectionDialogIconButton{
		resource: theme.ContentCopyIcon(),
		onTapped: func() {
			if window != nil && entry.Text != "" {
				window.Clipboard().SetContent(entry.Text)
			}
		},
		buttonSize: fyne.NewSize(28, 28),
		iconSize:   fyne.NewSize(15, 15),
	}
	btn.ExtendBaseWidget(btn)
	return btn
}

// newConnectionDialogPasteAction is the Copy button's counterpart, sitting
// right next to it in every field's trailing-action group -- see
// connection_dialog_paste_wasm.go/connection_dialog_paste_default.go for
// the platform-split clipboard read behind it.
func newConnectionDialogPasteAction(entry *connectionDialogEntry, window fyne.Window) *connectionDialogIconButton {
	btn := &connectionDialogIconButton{
		resource: theme.ContentPasteIcon(),
		onTapped: func() {
			pasteClipboardInto(entry, window)
		},
		buttonSize: fyne.NewSize(28, 28),
		iconSize:   fyne.NewSize(15, 15),
	}
	btn.ExtendBaseWidget(btn)
	return btn
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

// fieldActions bundles the Copy and Paste buttons for one field into a
// single trailing-action group.
func fieldActions(entry *connectionDialogEntry, window fyne.Window) fyne.CanvasObject {
	return container.NewHBox(
		newConnectionDialogCopyAction(entry, window),
		newConnectionDialogPasteAction(entry, window),
	)
}

func buildConnectionDialogForm(nameEntry, internalHostEntry, tailscaleHostEntry, masterKeyEntry *connectionDialogEntry, registerCheck fyne.CanvasObject, window fyne.Window) fyne.CanvasObject {
	masterKeyEntry.ActionItem = nil
	masterKeyEntry.Refresh()

	items := []fyne.CanvasObject{
		newConnectionDialogIconEntry(theme.AccountIcon(), nameEntry, nil),
		newConnectionDialogIconEntry(theme.ComputerIcon(), internalHostEntry, fieldActions(internalHostEntry, window)),
	}
	// No embedded tsnet in a browser tab (tailscale_service_wasm.go is a
	// stub, same reasoning as normalizeConnectionProtocol's wasm override
	// in connection_manager.go) -- every web connection is LAN-only, so
	// this field has nothing to actually do there and just invites users
	// to fill in an address that will never be dialed. Native builds
	// (desktop/Android/iOS) keep it.
	if runtime.GOOS != "js" {
		items = append(items, newConnectionDialogIconEntry(assets.NetworkIcon, tailscaleHostEntry, fieldActions(tailscaleHostEntry, window)))
	} else {
		items = append(items, newWebTailscaleHint(window))
	}
	items = append(items, newConnectionDialogIconEntry(theme.VisibilityOffIcon(), masterKeyEntry, fieldActions(masterKeyEntry, window)))
	if registerCheck != nil {
		items = append(items, view.NewInset(registerCheck, 10, 0, 0, 0))
	}
	return container.NewVBox(items...)
}

// newWebTailscaleHint replaces the Tailscale-address field in the browser
// build (see buildConnectionDialogForm's own doc comment on why that field
// is omitted there): a Tailscale/100.x.x.x address typed into "internal ip
// address" above still works from a browser tab -- dialTailscaleTarget
// (main_window_connection_tailscale_wasm.go) just dials it as a plain host,
// same as any LAN address -- but only if a real Tailscale client is
// already running on this device, since the wasm sandbox can't embed tsnet
// itself. This note exists so that requirement isn't silently discovered
// as a connection failure.
func newWebTailscaleHint(window fyne.Window) fyne.CanvasObject {
	text := widget.NewLabel("Tailscale requires a local client to connect from an external network:")
	text.Wrapping = fyne.TextWrapWord

	u, _ := url.Parse("https://tailscale.com/download")
	link := widget.NewHyperlink("tailscale.com/download", u)

	return view.NewInset(container.NewVBox(text, link), 10, 0, 0, 0)
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

func showAdaptiveConnectionDialog(parent fyne.Window, dialogTitle string, feedback fyne.CanvasObject, form fyne.CanvasObject, connectBtn, saveBtn, deleteBtn fyne.CanvasObject, footer ...fyne.CanvasObject) *widget.PopUp {
	title := view.NewBrandText(dialogTitle, 19, design.ColorTextLight, true)
	title.Alignment = fyne.TextAlignCenter

	closeBtn := newConnectionDialogIconButton(theme.CancelIcon(), nil)
	titleBar := container.New(&connectionDialogTitleLayout{}, title, closeBtn)

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

	columns := len(buttonItems)
	if columns > 3 {
		columns = 2
	}
	if columns == 0 {
		columns = 1
	}
	var buttons fyne.CanvasObject
	if columns == 2 && len(buttonItems) == 2 {
		buttons = container.New(&connectionDialogButtonsLayout{gap: connectionDialogButtonsGap}, buttonItems...)
	} else {
		buttons = container.NewGridWithColumns(columns, buttonItems...)
	}

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

	// Panel layout: title fixed at top, buttons fixed at bottom, scroll fills center.
	inner := container.NewBorder(
		view.NewInset(titleBar, 0, 0, 0, 10),
		view.NewInset(buttons, 0, 0, 10, 0),
		nil, nil,
		scroll,
	)
	panel := container.NewStack(
		bg,
		view.NewInset(inner, 18, 18, 16, 16),
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
	return popup
}

func showConnectionEditorDialog(parent fyne.Window, window fyne.Window, spec connectionDialogSpec) *widget.PopUp {
	nameEntry := newConnectionNameEntry(spec.nameValue, nil)
	internalHostEntry := newConnectionHostEntry(spec.internalHostValue, nil)
	tailscaleHostEntry := newConnectionTailscaleEntry(spec.tailscaleHostValue, nil)
	masterKeyEntry := newConnectionMasterKeyEntry(spec.masterKeyValue, nil)

	registerCheck := widget.NewCheck(i18n.Current.TailscaleRegisterLabel, nil)
	registerCheck.Checked = spec.tailscaleRegisterValue && tailscaleRegisterUISupported()
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
	tailscaleHostEntry.OnChanged = func(text string) {
		updateRegisterVisibility(text)
	}

	form := buildConnectionDialogForm(nameEntry, internalHostEntry, tailscaleHostEntry, masterKeyEntry, registerCheckContainer, window)

	var d *widget.PopUp

	var formContent fyne.CanvasObject = form
	var mobileFooter fyne.CanvasObject
	if spec.onQR != nil {
		qrBlock := newConnectionDialogIconBlock(assets.QRCodeAccent, "Scan QR", func() {
			if d != nil {
				d.Hide()
			}
			spec.onQR()
		})
		linkBlock := newConnectionDialogIconBlock(assets.ConnectionStatusAccent, "Paste Link", func() {
			showPasteLinkDialog(parent, func(ih, th, mk string) {
				internalHostEntry.SetText(ih)
				tailscaleHostEntry.SetText(th)
				masterKeyEntry.SetText(mk)
			})
		})

		// Two fixed-size icon blocks centered with a gap between them.
		gap := canvas.NewRectangle(color.Transparent)
		gap.SetMinSize(fyne.NewSize(20, 1))
		iconRow := container.NewCenter(container.NewHBox(qrBlock, gap, linkBlock))

		if view.UseCompactLayout(parent.Canvas().Size().Width) {
			// On Android/narrow browser viewports: icon row floats OUTSIDE
			// the popup panel, below it. The panel itself stays compact and
			// never moves when keyboard opens.
			mobileFooter = iconRow
		} else {
			// On desktop: add inside the scroll area, below the form fields.
			sep := canvas.NewRectangle(design.ColorBorder)
			sep.SetMinSize(fyne.NewSize(0, 1))
			formContent = container.NewVBox(
				form,
				view.NewInset(sep, 0, 0, 10, 10),
				iconRow,
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
		btn := newConnectionDialogPrimaryButton(connectLabel, spec.connectIcon, func() {
			if spec.onConnect != nil && !spec.onConnect(nameEntry.Text, internalHostEntry.Text, tailscaleHostEntry.Text, masterKeyEntry.Text, registerCheck.Checked) {
				return
			}
			if d != nil {
				d.Hide()
			}
		})
		connectBtn = btn
	}

	btn := view.NewConnectionPrimaryButton(saveLabel, func() {
		if spec.onSave != nil && !spec.onSave(nameEntry.Text, internalHostEntry.Text, tailscaleHostEntry.Text, masterKeyEntry.Text, registerCheck.Checked) {
			return
		}
		if d != nil {
			d.Hide()
		}
	})
	btn.SetAccent(true)
	saveBtn = btn

	if spec.onConnect != nil && spec.onDelete == nil {
		btn := view.NewConnectionPrimaryButton(saveLabel, func() {
			if spec.onSave != nil && !spec.onSave(nameEntry.Text, internalHostEntry.Text, tailscaleHostEntry.Text, masterKeyEntry.Text, registerCheck.Checked) {
				return
			}
			if d != nil {
				d.Hide()
			}
		})
		btn.SetAccent(true)
		saveBtn = btn
	}

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

	d = showAdaptiveConnectionDialog(parent, spec.title, feedback, formContent, connectBtn, saveBtn, deleteBtn, mobileFooter)
	return d
}

func newConnectionNameEntry(value string, onFocusChanged func(bool)) *connectionDialogEntry {
	entry := &connectionDialogEntry{onFocusChanged: onFocusChanged}
	entry.ExtendBaseWidget(entry)
	entry.Text = value
	entry.SetPlaceHolder("Name")
	return entry
}

func newConnectionHostEntry(value string, onFocusChanged func(bool)) *connectionDialogEntry {
	entry := &connectionDialogEntry{onFocusChanged: onFocusChanged}
	entry.ExtendBaseWidget(entry)
	entry.Text = value
	entry.SetPlaceHolder("Internal IP")
	return entry
}

func newConnectionTailscaleEntry(value string, onFocusChanged func(bool)) *connectionDialogEntry {
	entry := &connectionDialogEntry{onFocusChanged: onFocusChanged}
	entry.ExtendBaseWidget(entry)
	entry.Text = value
	entry.SetPlaceHolder("Tailscale Address")
	return entry
}

func newConnectionMasterKeyEntry(value string, onFocusChanged func(bool)) *connectionDialogEntry {
	entry := &connectionDialogEntry{onFocusChanged: onFocusChanged}
	entry.ExtendBaseWidget(entry)
	entry.Text = value
	entry.SetPlaceHolder("Master Key")
	entry.Password = false
	return entry
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
	cm.showPrefilledAddDialog("", internalHost, tailscaleHost, cm.masterKeyEntry.Text, "", false)
}

func (cm *ConnectionManager) showPrefilledAddDialog(name, internalHost, tailscaleHost, masterKey, protocol string, scanned bool) {
	feedbackText := ""
	if scanned {
		feedbackText = qrScanSuccessText
	}
	masterKey = strings.TrimSpace(masterKey)

	logrus.Infof("Opening add connection dialog: internal=%s tailscale=%s scanned=%v", internalHost, tailscaleHost, scanned)

	showConnectionEditorDialog(cm.window, cm.window, connectionDialogSpec{
		title:                  i18n.Current.AddConnectionTitle,
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
	icon *canvas.Image
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

	b.icon = canvas.NewImageFromResource(b.resource)
	b.icon.FillMode = canvas.ImageFillContain
	b.icon.ScaleMode = canvas.ImageScaleSmooth
	b.icon.SetMinSize(b.iconSize)

	b.refreshVisuals()
	return widget.NewSimpleRenderer(container.NewMax(b.bg, container.NewCenter(b.icon)))
}

func (b *connectionDialogIconButton) refreshVisuals() {
	if b.bg == nil || b.icon == nil {
		return
	}

	b.bg.FillColor = color.Transparent
	b.icon.Resource = b.resource
	b.icon.Translucency = 0.32
	if b.hovered {
		b.bg.FillColor = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x10}
		b.icon.Translucency = 0.08
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

// showPasteLinkDialog shows a small popup with a text entry for pasting a
// usbridge:// deep link. On apply, it calls onApply with the parsed hosts.
func showPasteLinkDialog(parent fyne.Window, onApply func(internalHost, tailscaleHost, masterKey string)) {
	title := view.NewBrandText("Paste Link", 17, design.ColorTextLight, true)
	title.Alignment = fyne.TextAlignCenter

	entry := &connectionDialogEntry{}
	entry.MultiLine = true
	entry.Wrapping = fyne.TextWrapWord
	entry.ExtendBaseWidget(entry)
	entry.SetPlaceHolder("usbridge://connect?...")
	entry.SetMinRowsVisible(4)

	errLabel := canvas.NewText("", color.NRGBA{R: 0xff, G: 0x5a, B: 0x52, A: 0xff})
	errLabel.TextSize = 11

	var popup *widget.PopUp

	applyFn := func() {
		ih, th, mk, _, err := parseQRContents(entry.Text)
		if err != nil {
			errLabel.Text = "Invalid link format"
			errLabel.Refresh()
			return
		}
		if popup != nil {
			popup.Hide()
		}
		onApply(ih, th, mk)
	}
	entry.onFocusChanged = nil
	entry.OnChanged = func(_ string) {
		if errLabel.Text != "" {
			errLabel.Text = ""
			errLabel.Refresh()
		}
	}

	var closeBtn *connectionDialogIconButton
	closeBtn = newConnectionDialogIconButton(theme.CancelIcon(), func() {
		if popup != nil {
			popup.Hide()
		}
	})
	titleBar := container.New(&connectionDialogTitleLayout{}, title, closeBtn)

	// This entry has no icon/copy/paste input-group chrome the way the
	// address/key fields in the main editor do (see fieldActions) -- it's a
	// bare multi-line textarea meant to be filled by pasting a whole
	// usbridge:// link, so a Paste button matters here even more than on
	// those. Same platform-split pasteClipboardInto (Fyne's own Clipboard()
	// on desktop/mobile, navigator.clipboard.readText() on wasm) that
	// powers the Copy/Paste pair everywhere else.
	pasteBtn := newConnectionDialogIconButton(theme.ContentPasteIcon(), func() {
		pasteClipboardInto(entry, parent)
	})
	pasteRow := container.NewHBox(layout.NewSpacer(), pasteBtn)

	applyBtn := view.NewConnectionPrimaryButton("Apply", applyFn)
	applyBtn.SetAccent(true)

	body := container.NewVBox(
		view.NewInset(titleBar, 0, 0, 0, 10),
		widget.NewSeparator(),
		view.NewInset(container.NewVBox(pasteRow, entry, errLabel), 0, 0, 8, 8),
		widget.NewSeparator(),
		view.NewInset(applyBtn, 0, 0, 8, 0),
	)

	bg := canvas.NewRectangle(design.ColorGray900)
	bg.CornerRadius = design.RadiusMD
	border := canvas.NewRectangle(color.Transparent)
	border.CornerRadius = design.RadiusMD
	border.StrokeColor = design.ColorBorder
	border.StrokeWidth = 1
	panel := container.NewStack(
		bg,
		view.NewInset(body, 18, 18, 16, 16),
		border,
	)

	popup = view.ShowOverlayPopup(parent, view.OverlayPopupSpec{
		Panel:    panel,
		DimColor: connectionDialogDimColor(),
		PanelSize: func(canvasSize fyne.Size, panel fyne.CanvasObject) fyne.Size {
			panelMin := panel.MinSize()
			w := minFloat32(maxFloat32(panelMin.Width, 340), canvasSize.Width-48)
			h := minFloat32(maxFloat32(panelMin.Height, 0), canvasSize.Height-48)
			return fyne.NewSize(w, h)
		},
	})
}

func (cm *ConnectionManager) handlePasteLink() {
	showPasteLinkDialog(cm.window, func(ih, th, mk string) {
		cm.showPrefilledAddDialog("", ih, th, mk, "", false)
	})
}
