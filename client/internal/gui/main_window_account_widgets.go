package gui

// main_window_account_widgets.go -- the small purely-visual pieces the
// Account dialog's logged-in view (see main_window_account.go's render())
// is built out of: the avatar square, the bordered card wrapper both the
// identity and sync sections sit in, the thin divider inside a card, the
// small on/off status pill, and one license row. Split out from
// main_window_account.go so that file stays about *state*, not layout.

import (
	"image/color"
	"strings"

	"usbridge-client/internal/gui/design"
	"usbridge-client/internal/gui/view"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// accountCardBg is a touch lighter than the dialog's own ColorGray900
// background -- just enough for the identity/sync cards to read as
// distinct panels sitting on it, without introducing a whole new surface
// color for one dialog.
var accountCardBg = color.NRGBA{R: 0x18, G: 0x1c, B: 0x1f, A: 0xff}

// newAccountCard wraps content in the rounded, bordered panel both the
// "authenticated user" and "connections sync" sections use -- same
// RadiusMD/border treatment as the dialog's own outer panel, one size down.
func newAccountCard(content fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(accountCardBg)
	bg.CornerRadius = design.RadiusMD
	border := canvas.NewRectangle(color.Transparent)
	border.CornerRadius = design.RadiusMD
	border.StrokeColor = design.ColorConnectionBadgeBorder
	border.StrokeWidth = 1
	return container.NewStack(bg, view.NewInset(content, 14, 14, 12, 12), border)
}

// newAccountDivider is the thin low-contrast rule between a card's header
// row and its body -- same shade the dialog's own header/footer dividers
// use (see main_window_account.go's sep).
func newAccountDivider() fyne.CanvasObject {
	sep := canvas.NewRectangle(color.NRGBA{R: 0x30, G: 0x34, B: 0x2e, A: 0xff})
	sep.SetMinSize(fyne.NewSize(0, 1))
	return sep
}

// newAccountEyebrowLabel is the small caps section label ("AUTHENTICATED
// USER") above a card -- plain canvas.Text, no letter-spacing (Fyne's text
// renderer has no such knob), close enough at this size to read as a label
// rather than a sentence.
func newAccountEyebrowLabel(text string) fyne.CanvasObject {
	t := canvas.NewText(strings.ToUpper(text), design.ColorConnectionsSectionMutedText)
	t.TextSize = 10
	t.TextStyle = fyne.TextStyle{Bold: true}
	return t
}

// newAccountAvatarBadge is the bigger, rounded-square sibling of the
// header's own circular loginAvatarButton (connection_header.go) -- same
// teal-fill/dark-letter look, just static (no hover/tap state) since this
// one sits inside an already-open dialog.
func newAccountAvatarBadge(letter string) fyne.CanvasObject {
	bg := canvas.NewRectangle(design.ColorConnectionBadgeText)
	bg.CornerRadius = design.RadiusLG
	bg.SetMinSize(fyne.NewSize(36, 36))

	label := canvas.NewText(letter, design.ColorGray950)
	label.TextSize = 16
	label.TextStyle = fyne.TextStyle{Bold: true}
	label.Alignment = fyne.TextAlignCenter

	return container.NewStack(bg, container.NewCenter(label))
}

// newAccountStatusPill is the small "on"/"off" badge next to "Connections
// sync" -- a status readout, not a control (see accountSyncPassphraseSection's
// own doc comment for why: there is no separate enable/disable toggle here,
// sync is simply "on" once a passphrase has been set).
func newAccountStatusPill(label string, on bool) fyne.CanvasObject {
	var textColor color.Color
	var borderColor color.Color

	if on {
		textColor = design.ColorConnectionAddFill
		borderColor = design.ColorConnectionAddFill
	} else {
		textColor = design.ColorTextMuted
		borderColor = design.ColorBorder
	}

	text := canvas.NewText(strings.ToUpper(label), textColor)
	text.TextSize = 9
	text.TextStyle = fyne.TextStyle{Bold: true}

	bg := canvas.NewRectangle(color.Transparent)
	bg.CornerRadius = 4
	bg.StrokeColor = borderColor
	bg.StrokeWidth = 1

	return container.NewStack(bg, view.NewInset(text, 6, 6, 1, 1))
}

// licenseStatusColor maps a license's raw Status string (see
// internal/account.License) to the color its status dot/text render in.
func licenseStatusColor(status string) color.Color {
	switch status {
	case "licensed":
		return design.ColorConnectionAddFill
	case "trial":
		return design.ColorAlert
	case "revoked":
		return color.NRGBA{R: 0xe2, G: 0x6a, B: 0x6a, A: 0xff}
	default: // "trial_used" and anything unrecognized
		return design.ColorTextMuted
	}
}

// newAccountLicenseRow renders one account.License as "[kind] identifier —
// ● status", with a copy-to-clipboard button pinned to the row's right edge
// -- the styled equivalent of the old plain
// "[%s] %s — %s" label (see renderLicenses).
var accountDialogCopyIconRes = fyne.NewStaticResource("copy_colored.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#ebffbc"><path d="M16 1H4c-1.1 0-2 .9-2 2v14h2V3h12V1zm3 4H8c-1.1 0-2 .9-2 2v14c0 1.1.9 2 2 2h11c1.1 0 2-.9 2-2V7c0-1.1-.9-2-2-2zm0 16H8V7h11v14z"/></svg>`))

func newAccountLicenseRow(kind, identifier, status string, window fyne.Window) fyne.CanvasObject {
	kindText := canvas.NewText("["+kind+"]", design.ColorConnectionBadgeText)
	kindText.TextSize = 11
	kindText.TextStyle = fyne.TextStyle{Bold: true}

	idText := canvas.NewText(identifier, design.ColorTextLight)
	idText.TextSize = 11

	statusColor := licenseStatusColor(status)
	statusText := canvas.NewText("— "+status, statusColor)
	statusText.TextSize = 11

	left := container.NewHBox(kindText, idText, statusText)

	copyBtn := newAccountDialogIconButton(accountDialogCopyIconRes, func() {
		if window != nil && window.Clipboard() != nil {
			window.Clipboard().SetContent(identifier)
		}
	})
	copyBtn.opaqueIcon = true
	copyBtn.iconSize = 14
	copyBtn.buttonSize = 24

	return container.NewBorder(nil, nil, nil, copyBtn, left)
}

// newAccountLicenseSkeletonRow is shown in place of the license list while
// its very first fetch of this login session is still in flight (see
// AccountManager.CachedLicenses -- every OTHER dialog open renders the real
// rows immediately, skipping this entirely). Built from the exact same
// container.NewBorder(left-text, right-24x24) shape newAccountLicenseRow
// uses, so its MinSize height matches a real row's (dominated by the
// 24x24 copy button on the right) instead of a bare text line's -- that
// height mismatch was what made the dialog visibly resize the moment the
// fetch resolved and swapped the placeholder for real rows.
func newAccountLicenseSkeletonRow() fyne.CanvasObject {
	text := canvas.NewText("Loading your licenses…", design.ColorTextMuted)
	text.TextSize = 11

	rightSpacer := canvas.NewRectangle(color.Transparent)
	rightSpacer.SetMinSize(fyne.NewSize(24, 24))

	return container.NewBorder(nil, nil, nil, rightSpacer, text)
}

type accountDialogLinkButton struct {
	widget.BaseWidget
	prefix   string
	linkText string
	onTapped func()
	hovered  bool

	lbl1 *canvas.Text
	lbl2 *canvas.Text
	line *canvas.Rectangle
}

func newAccountDialogLinkButton(prefix, linkText string, onTapped func()) *accountDialogLinkButton {
	b := &accountDialogLinkButton{prefix: prefix, linkText: linkText, onTapped: onTapped}
	b.ExtendBaseWidget(b)
	return b
}

type accountLinkHBoxLayout struct{}

func (l *accountLinkHBoxLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) < 3 {
		return fyne.NewSize(0, 0)
	}
	m1 := objects[0].MinSize()
	m2 := objects[1].MinSize()
	h := m1.Height
	if m2.Height > h {
		h = m2.Height
	}
	return fyne.NewSize(m1.Width+m2.Width, h)
}

func (l *accountLinkHBoxLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 3 {
		return
	}
	m1 := objects[0].MinSize()
	m2 := objects[1].MinSize()

	// Both texts at Y=0 so baselines exactly match (same font and size)
	objects[0].Resize(m1)
	objects[0].Move(fyne.NewPos(0, 0))

	objects[1].Resize(m2)
	objects[1].Move(fyne.NewPos(m1.Width, 0))

	objects[2].Resize(fyne.NewSize(m2.Width, 1))
	objects[2].Move(fyne.NewPos(m1.Width, m2.Height-3))
}

func (b *accountDialogLinkButton) CreateRenderer() fyne.WidgetRenderer {
	b.lbl1 = canvas.NewText(b.prefix, color.NRGBA{R: 0x8f, G: 0x93, B: 0x81, A: 0xff})
	b.lbl1.TextSize = 11
	b.lbl1.TextStyle = fyne.TextStyle{Bold: true}

	b.lbl2 = canvas.NewText(b.linkText, design.ColorConnectionAddFill)
	b.lbl2.TextSize = 11
	b.lbl2.TextStyle = fyne.TextStyle{Bold: true}

	b.line = canvas.NewRectangle(design.ColorConnectionAddFill)
	b.line.SetMinSize(fyne.NewSize(0, 1))

	content := container.New(&accountLinkHBoxLayout{}, b.lbl1, b.lbl2, b.line)
	return widget.NewSimpleRenderer(content)
}

func (b *accountDialogLinkButton) MinSize() fyne.Size {
	b.ExtendBaseWidget(b)
	return b.BaseWidget.MinSize()
}

func (b *accountDialogLinkButton) Tapped(*fyne.PointEvent) {
	if b.onTapped != nil {
		b.onTapped()
	}
}

func (b *accountDialogLinkButton) TappedSecondary(*fyne.PointEvent) {}

func (b *accountDialogLinkButton) Cursor() desktop.Cursor {
	return desktop.PointerCursor
}

func (b *accountDialogLinkButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.refreshVisuals()
}

func (b *accountDialogLinkButton) MouseMoved(*desktop.MouseEvent) {}

func (b *accountDialogLinkButton) MouseOut() {
	b.hovered = false
	b.refreshVisuals()
}

func (b *accountDialogLinkButton) refreshVisuals() {
	if b.lbl1 == nil {
		return
	}
	hoverColor := color.NRGBA{R: 0xd6, G: 0xf7, B: 0x9c, A: 0xff} // Lighter green
	if b.hovered {
		b.lbl2.Color = hoverColor
		b.line.FillColor = hoverColor
	} else {
		b.lbl2.Color = design.ColorConnectionAddFill
		b.line.FillColor = design.ColorConnectionAddFill
	}
	b.lbl2.Refresh()
	b.line.Refresh()
}

// accountDialogDarkButton is a small bordered chip (like "Log out" or "Reset").
type accountDialogDarkButton struct {
	widget.BaseWidget
	text     string
	onTapped func()
	hovered  bool

	iconNormal fyne.Resource
	iconHover  fyne.Resource

	bg   *canvas.Rectangle
	bdr  *canvas.Rectangle
	icon *canvas.Image
	lbl  *canvas.Text
}

func newAccountDialogDarkButton(text string, iconNormal, iconHover fyne.Resource, onTapped func()) *accountDialogDarkButton {
	b := &accountDialogDarkButton{
		text:       text,
		onTapped:   onTapped,
		iconNormal: iconNormal,
		iconHover:  iconHover,
	}
	b.ExtendBaseWidget(b)
	return b
}

func (b *accountDialogDarkButton) CreateRenderer() fyne.WidgetRenderer {
	b.bg = canvas.NewRectangle(color.NRGBA{R: 0x23, G: 0x27, B: 0x2a, A: 0xff})
	b.bg.CornerRadius = 6

	b.bdr = canvas.NewRectangle(color.Transparent)
	b.bdr.StrokeWidth = 1
	b.bdr.StrokeColor = color.NRGBA{R: 0x44, G: 0x48, B: 0x39, A: 0xff}
	b.bdr.CornerRadius = 6

	b.lbl = canvas.NewText(b.text, color.NRGBA{R: 0xe0, G: 0xe3, B: 0xe7, A: 0xff})
	b.lbl.TextSize = 11
	b.lbl.TextStyle = fyne.TextStyle{Bold: true}

	var content *fyne.Container
	if b.iconNormal != nil {
		b.icon = canvas.NewImageFromResource(b.iconNormal)
		b.icon.SetMinSize(fyne.NewSize(14, 14))
		b.icon.FillMode = canvas.ImageFillContain
		content = container.NewHBox(b.icon, b.lbl)
	} else {
		content = container.NewHBox(b.lbl)
	}

	return widget.NewSimpleRenderer(container.NewStack(b.bg, b.bdr, view.NewInset(content, 12, 12, 3, 3)))
}

func (b *accountDialogDarkButton) MinSize() fyne.Size {
	b.ExtendBaseWidget(b)
	return b.BaseWidget.MinSize()
}

func (b *accountDialogDarkButton) Tapped(*fyne.PointEvent) {
	if b.onTapped != nil {
		b.onTapped()
	}
}

func (b *accountDialogDarkButton) TappedSecondary(*fyne.PointEvent) {}

func (b *accountDialogDarkButton) Cursor() desktop.Cursor {
	return desktop.PointerCursor
}

func (b *accountDialogDarkButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.refreshVisuals()
}

func (b *accountDialogDarkButton) MouseMoved(*desktop.MouseEvent) {}

func (b *accountDialogDarkButton) MouseOut() {
	b.hovered = false
	b.refreshVisuals()
}

func (b *accountDialogDarkButton) refreshVisuals() {
	if b.bg == nil {
		return
	}
	if b.hovered {
		b.bg.FillColor = color.NRGBA{R: 0x1d, G: 0x13, B: 0x1b, A: 0xff}
		b.bdr.StrokeColor = color.NRGBA{R: 0x4e, G: 0x13, B: 0x28, A: 0xff}
		b.lbl.Color = color.NRGBA{R: 0xed, G: 0x6b, B: 0x7f, A: 0xff}
		if b.icon != nil && b.iconHover != nil {
			b.icon.Resource = b.iconHover
		}
	} else {
		b.bg.FillColor = color.NRGBA{R: 0x23, G: 0x27, B: 0x2a, A: 0xff}
		b.bdr.StrokeColor = color.NRGBA{R: 0x44, G: 0x48, B: 0x39, A: 0xff}
		b.lbl.Color = color.NRGBA{R: 0xe0, G: 0xe3, B: 0xe7, A: 0xff}
		if b.icon != nil && b.iconNormal != nil {
			b.icon.Resource = b.iconNormal
		}
	}
	b.bg.Refresh()
	b.bdr.Refresh()
	b.lbl.Refresh()
	if b.icon != nil {
		b.icon.Refresh()
	}
}

// accountDialogTextButton is a plain text button for "Cancel".
type accountDialogTextButton struct {
	widget.BaseWidget
	text     string
	onTapped func()
	hovered  bool
	lbl      *canvas.Text
}

func newAccountDialogTextButton(text string, onTapped func()) *accountDialogTextButton {
	b := &accountDialogTextButton{text: text, onTapped: onTapped}
	b.ExtendBaseWidget(b)
	return b
}

func (b *accountDialogTextButton) CreateRenderer() fyne.WidgetRenderer {
	b.lbl = canvas.NewText(b.text, color.NRGBA{R: 0x8f, G: 0x93, B: 0x81, A: 0xff})
	b.lbl.TextSize = 10
	b.lbl.TextStyle = fyne.TextStyle{Bold: true}
	// Add some padding to match heights visually
	return widget.NewSimpleRenderer(view.NewInset(b.lbl, 8, 8, 3, 3))
}

func (b *accountDialogTextButton) MinSize() fyne.Size {
	b.ExtendBaseWidget(b)
	return b.BaseWidget.MinSize()
}

func (b *accountDialogTextButton) Tapped(*fyne.PointEvent) {
	if b.onTapped != nil {
		b.onTapped()
	}
}

func (b *accountDialogTextButton) TappedSecondary(*fyne.PointEvent) {}

func (b *accountDialogTextButton) Cursor() desktop.Cursor {
	return desktop.PointerCursor
}

func (b *accountDialogTextButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.refreshVisuals()
}

func (b *accountDialogTextButton) MouseMoved(*desktop.MouseEvent) {}

func (b *accountDialogTextButton) MouseOut() {
	b.hovered = false
	b.refreshVisuals()
}

func (b *accountDialogTextButton) refreshVisuals() {
	if b.lbl == nil {
		return
	}
	if b.hovered {
		b.lbl.Color = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	} else {
		b.lbl.Color = color.NRGBA{R: 0x8f, G: 0x93, B: 0x81, A: 0xff}
	}
	b.lbl.Refresh()
}

type accountFieldTheme struct {
	fyne.Theme
	textSize  float32
	textColor color.Color
}

func (t *accountFieldTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameInputBackground:
		return color.NRGBA{R: 255, G: 255, B: 255, A: 8}
	case theme.ColorNameFocus:
		return color.NRGBA{R: 255, G: 255, B: 255, A: 20}
	case theme.ColorNameInputBorder:
		return color.NRGBA{R: 0x41, G: 0xe0, B: 0xc3, A: 0x40}
	case theme.ColorNamePrimary:
		return design.ColorConnectionBadgeText
	case theme.ColorNameShadow:
		return color.Transparent
	case theme.ColorNameForeground:
		if t.textColor != nil {
			return t.textColor
		}
		return design.ColorTextLight
	case theme.ColorNamePlaceHolder:
		return design.ColorTextMuted
	}
	return t.Theme.Color(name, variant)
}

func (t *accountFieldTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameText:
		return t.textSize
	case theme.SizeNameInputBorder:
		return 1
	case theme.SizeNamePadding:
		return 2
	case theme.SizeNameInnerPadding:
		return 8
	case theme.SizeNameInputRadius:
		return 4
	case theme.SizeNameInlineIcon:
		return t.textSize * 1.6 // Larger eye icon
	}
	return t.Theme.Size(name)
}

func wrapAccountField(obj fyne.CanvasObject, textSize float32, textColor color.Color) fyne.CanvasObject {
	return container.NewThemeOverride(obj, &accountFieldTheme{Theme: design.NewBrandTheme(), textSize: textSize, textColor: textColor})
}
