package gui

import (
	"image/color"

	"usbridge-client/internal/gui/design"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// colorViolet distinguishes the secondary (detection-fps) badge from the
// primary badge's design.ColorAccent green -- no existing design.Color*
// token fit, so this is local to this one small UI element rather than
// added to the shared palette.
var colorViolet = color.NRGBA{R: 0xa7, G: 0x8b, B: 0xfa, A: 0xff}

type headerStatusBadgeButton struct {
	widget.BaseWidget

	iconRes   fyne.Resource
	badgeText string
	// secondaryBadgeText renders as a second, bottom-anchored badge below
	// the icon (the primary badgeText sits top-left, see Layout) -- used by
	// the video icon to show detection fps under the video fps badge
	// (main_window_layout.go's updateVideoIconLabel), distinctly colored so
	// the two numbers aren't mistaken for one another at a glance.
	secondaryBadgeText string
	onTapped           func()
	hovered            bool
	iconSize           fyne.Size

	bg        *canvas.Rectangle
	icon      *canvas.Image
	badgeBg   *canvas.Rectangle
	badgeTxt  *canvas.Text
	badge2Bg  *canvas.Rectangle
	badge2Txt *canvas.Text
}

func newHeaderStatusBadgeButton(icon fyne.Resource, onTapped func()) *headerStatusBadgeButton {
	b := &headerStatusBadgeButton{
		iconRes:   icon,
		badgeText: "0",
		onTapped:  onTapped,
		iconSize:  fyne.NewSize(22, 22),
	}
	b.ExtendBaseWidget(b)
	return b
}

func (b *headerStatusBadgeButton) SetIcon(icon fyne.Resource) {
	b.iconRes = icon
	b.Refresh()
}

func (b *headerStatusBadgeButton) SetBadgeText(text string) {
	b.badgeText = text
	b.Refresh()
}

// SetSecondaryBadgeText sets the bottom badge (see the struct's doc
// comment); an empty string hides it, same convention as SetBadgeText.
func (b *headerStatusBadgeButton) SetSecondaryBadgeText(text string) {
	b.secondaryBadgeText = text
	b.Refresh()
}

func (b *headerStatusBadgeButton) SetIconSize(size fyne.Size) {
	if size.Width <= 0 || size.Height <= 0 {
		size = fyne.NewSize(22, 22)
	}
	b.iconSize = size
	b.Refresh()
}

func (b *headerStatusBadgeButton) Tapped(*fyne.PointEvent) {
	if b.onTapped != nil {
		b.onTapped()
	}
}

func (b *headerStatusBadgeButton) TappedSecondary(*fyne.PointEvent) {}

func (b *headerStatusBadgeButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.Refresh()
}

func (b *headerStatusBadgeButton) MouseMoved(*desktop.MouseEvent) {}

func (b *headerStatusBadgeButton) MouseOut() {
	b.hovered = false
	b.Refresh()
}

func (b *headerStatusBadgeButton) MinSize() fyne.Size {
	return fyne.NewSize(36, 36)
}

func (b *headerStatusBadgeButton) CreateRenderer() fyne.WidgetRenderer {
	b.bg = canvas.NewRectangle(color.Transparent)
	b.bg.CornerRadius = design.RadiusMD

	b.icon = canvas.NewImageFromResource(b.iconRes)
	b.icon.FillMode = canvas.ImageFillContain
	b.icon.SetMinSize(b.iconSize)

	b.badgeBg = canvas.NewRectangle(design.ColorAccent)
	b.badgeBg.CornerRadius = 7

	b.badgeTxt = canvas.NewText(b.badgeText, design.ColorBackground)
	b.badgeTxt.TextSize = 8
	b.badgeTxt.TextStyle.Bold = true
	b.badgeTxt.Alignment = fyne.TextAlignCenter

	// Secondary (bottom) badge: a distinct color (violet, vs. the primary's
	// accent) so the two numbers read as "two different measurements", not
	// a typo/duplicate.
	b.badge2Bg = canvas.NewRectangle(colorViolet)
	b.badge2Bg.CornerRadius = 7

	b.badge2Txt = canvas.NewText(b.secondaryBadgeText, design.ColorBackground)
	b.badge2Txt.TextSize = 8
	b.badge2Txt.TextStyle.Bold = true
	b.badge2Txt.Alignment = fyne.TextAlignCenter

	renderer := &headerStatusBadgeButtonRenderer{
		button:  b,
		objects: []fyne.CanvasObject{b.bg, b.icon, b.badgeBg, b.badgeTxt, b.badge2Bg, b.badge2Txt},
	}
	renderer.Refresh()
	return renderer
}

type headerStatusBadgeButtonRenderer struct {
	button  *headerStatusBadgeButton
	objects []fyne.CanvasObject
}

func (r *headerStatusBadgeButtonRenderer) Layout(size fyne.Size) {
	r.button.bg.Move(fyne.NewPos(0, 0))
	r.button.bg.Resize(size)

	iconSize := r.button.iconSize
	if iconSize.Width <= 0 || iconSize.Height <= 0 {
		iconSize = fyne.NewSize(22, 22)
	}
	r.button.icon.Resize(iconSize)
	r.button.icon.Move(fyne.NewPos((size.Width-iconSize.Width)/2, (size.Height-iconSize.Height)/2))

	if r.button.badgeText != "" {
		badgeMin := fyne.MeasureText(r.button.badgeText, 8, fyne.TextStyle{Bold: true})
		badgeW := badgeMin.Width + 8
		if badgeW < 14 {
			badgeW = 14
		}
		badgeSize := fyne.NewSize(badgeW, 14)
		r.button.badgeBg.Resize(badgeSize)
		r.button.badgeBg.Move(fyne.NewPos(0, 0))
		textSize := fyne.MeasureText(r.button.badgeText, 8, fyne.TextStyle{Bold: true})
		r.button.badgeTxt.Resize(textSize)
		r.button.badgeTxt.Move(fyne.NewPos(
			(badgeSize.Width-textSize.Width)/2,
			(badgeSize.Height-textSize.Height)/2-1,
		))
	}

	if r.button.secondaryBadgeText != "" {
		badge2Min := fyne.MeasureText(r.button.secondaryBadgeText, 8, fyne.TextStyle{Bold: true})
		badge2W := badge2Min.Width + 8
		if badge2W < 14 {
			badge2W = 14
		}
		badge2Size := fyne.NewSize(badge2W, 14)
		// Bottom-right, mirroring the primary badge's top-left placement.
		badge2Pos := fyne.NewPos(size.Width-badge2Size.Width, size.Height-badge2Size.Height)
		r.button.badge2Bg.Resize(badge2Size)
		r.button.badge2Bg.Move(badge2Pos)
		text2Size := fyne.MeasureText(r.button.secondaryBadgeText, 8, fyne.TextStyle{Bold: true})
		r.button.badge2Txt.Resize(text2Size)
		r.button.badge2Txt.Move(fyne.NewPos(
			badge2Pos.X+(badge2Size.Width-text2Size.Width)/2,
			badge2Pos.Y+(badge2Size.Height-text2Size.Height)/2-1,
		))
	}
}

func (r *headerStatusBadgeButtonRenderer) MinSize() fyne.Size {
	return r.button.MinSize()
}

func (r *headerStatusBadgeButtonRenderer) Refresh() {
	r.button.bg.FillColor = color.Transparent
	if r.button.hovered {
		r.button.bg.FillColor = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x10}
	}
	r.button.bg.Refresh()

	r.button.icon.Resource = r.button.iconRes
	r.button.icon.Refresh()

	r.button.badgeTxt.Text = r.button.badgeText
	if r.button.badgeText == "" {
		r.button.badgeBg.Hide()
		r.button.badgeTxt.Hide()
	} else {
		r.button.badgeBg.Show()
		r.button.badgeTxt.Show()
		r.button.badgeBg.FillColor = design.ColorAccent
		r.button.badgeBg.Refresh()
		r.button.badgeTxt.Color = design.ColorBackground
		r.button.badgeTxt.Refresh()
	}

	r.button.badge2Txt.Text = r.button.secondaryBadgeText
	if r.button.secondaryBadgeText == "" {
		r.button.badge2Bg.Hide()
		r.button.badge2Txt.Hide()
	} else {
		r.button.badge2Bg.Show()
		r.button.badge2Txt.Show()
		r.button.badge2Bg.FillColor = colorViolet
		r.button.badge2Bg.Refresh()
		r.button.badge2Txt.Color = design.ColorBackground
		r.button.badge2Txt.Refresh()
	}
	r.Layout(r.button.Size())
}

func (r *headerStatusBadgeButtonRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *headerStatusBadgeButtonRenderer) Destroy() {}

func (r *headerStatusBadgeButtonRenderer) BackgroundColor() color.Color {
	return color.Transparent
}
