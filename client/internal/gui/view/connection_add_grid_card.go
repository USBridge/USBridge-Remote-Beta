package view

// connection_add_grid_card.go -- the "Add New Connect" placeholder tile Grid
// mode always appends after the real connection cards (see
// ConnectionManager.refreshConnectionsList in the controller package). Same
// footprint as a real card (connectionCardWidth/Height) so it slots into the
// same GridWrap without a special case there, but dashed-bordered and empty
// instead of showing a saved connection -- a standing hint for how to add
// one. List mode has no equivalent: this only ever lands in the cards slice
// SetRows caches for grid mode, never in rows.
//
// Only the "+" circle actually opens anything (the same Add Connection
// dialog the header's own Add button opens) -- the QR/paste-link buttons are
// shortcuts into the two fastest paths through that same flow. The rest of
// the card is inert, unlike a real card, which selects itself on any tap --
// hovering anywhere on it still lights the dashed border up, matching a real
// card's own hover feedback.

import (
	"fmt"
	"image/color"

	"usbridge-client/internal/gui/assets"
	"usbridge-client/internal/gui/design"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
)

// AddConnectionCardActions are the entry points the placeholder card's own
// controls open -- all three lead into the same Add Connection dialog
// (OnQR/OnPasteLink pre-fill it once they've got something to fill in with).
type AddConnectionCardActions struct {
	// OnAdd opens the blank Add Connection dialog -- the card's "+" circle.
	OnAdd func()
	// OnQR opens the QR scanner.
	OnQR func()
	// OnPasteLink opens the paste-a-link popup.
	OnPasteLink func()
}

// addConnectionCardMutedColor is the "+" icon and subtitle's shared color --
// c5c8b5, the same muted tone the rest of the card's chrome (its own
// Delete/Cancel icons, its bordered buttons' text) already uses elsewhere in
// this package.
var addConnectionCardMutedColor = color.NRGBA{R: 0xc5, G: 0xc8, B: 0xb5, A: 0xff}

const addConnectionCardMutedColorHex = "#c5c8b5"

// addConnectionCardHoverColorHex is the "salad" lime the whole card's chrome
// (dashed border, title, and each button's own border/icon/text) switches to
// on hover -- the same lime design.ColorConnectionAddFill already uses for
// the header's own Add button and the KVM type badge (c4e77a).
const addConnectionCardHoverColorHex = "#c4e77a"

var addConnectionCardHoverColor = design.ColorConnectionAddFill

// addConnectionCardRingHoverTint is the "чуть фон лайм" wash behind the "+"
// ring while the card (not necessarily the ring itself) is hovered --
// addConnectionCardButtonHoverTint is the extra, more opaque tint layered on
// top of that specifically when the cursor is directly over the "+" circle
// (see NewAddConnectionGridCard's addBtn) -- "чуть светлее" on top of the
// card-hover state, not instead of it.
var (
	addConnectionCardRingHoverTint   = color.NRGBA{R: 0xc4, G: 0xe7, B: 0x7a, A: 0x22}
	addConnectionCardButtonHoverTint = color.NRGBA{R: 0xc4, G: 0xe7, B: 0x7a, A: 0x40}
)

// NewAddConnectionGridCard builds the dashed-bordered placeholder tile.
func NewAddConnectionGridCard(actions AddConnectionCardActions) fyne.CanvasObject {
	// plus-circle-svgrepo-com.svg's own glyph relies on two overlapping
	// circles drawn via SVG arc commands (opposite winding, so they cancel
	// into a ring under the default nonzero fill rule) -- reasonable in a
	// browser, but oksvg (Fyne's SVG rasterizer) never actually rendered it
	// here, arcs or not. Every icon elsewhere in this package that's
	// confirmed to render (this Add button's own header equivalent
	// included) draws with plain line commands (M/L/H/V/Z), no arcs -- so
	// rather than debug oksvg's arc parsing, the ring here is a real
	// fyne canvas.Circle (native, no SVG involved) and just the "+" itself
	// is that same proven line-only glyph, recolored.
	//
	// The ring/icon react to the whole card's hover (setHovered below), not
	// their own -- so they're plain canvas.Circle/canvas.Image this
	// function keeps a handle on and recolors itself, rather than routed
	// through iconChromeButton's own (button-local-only) hover fields.
	// addBtn on top is just a transparent hit target plus one extra,
	// smaller "even lighter" tint (HoverFill) for when the cursor is
	// directly over the circle -- on top of, not instead of, the
	// card-hover state it also triggers via spec.OnHover below.
	plusIconNormal := fyne.NewStaticResource("add-device-plus.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="`+addConnectionCardMutedColorHex+`"><path d="M19 13h-6v6h-2v-6H5v-2h6V5h2v6h6v2z"/></svg>`))
	plusIconHover := fyne.NewStaticResource("add-device-plus-hover.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="`+addConnectionCardHoverColorHex+`"><path d="M19 13h-6v6h-2v-6H5v-2h6V5h2v6h6v2z"/></svg>`))
	plusImg := canvas.NewImageFromResource(plusIconNormal)
	plusImg.FillMode = canvas.ImageFillContain
	plusImg.SetMinSize(fyne.NewSize(18, 18))

	addRing := canvas.NewCircle(color.Transparent)
	addRing.StrokeColor = addConnectionCardMutedColor
	addRing.StrokeWidth = 1.5
	addRingSized := container.NewGridWrap(fyne.NewSize(48, 48), addRing)

	addBtn := newIconChromeButton(iconChromeButtonSpec{
		NormalFill:   color.Transparent,
		HoverFill:    addConnectionCardButtonHoverTint,
		Stroke:       color.Transparent,
		CornerRadius: 24, // half of ButtonSize's 48 -> full circle
		ButtonSize:   fyne.NewSize(48, 48),
		OnTapped:     actions.OnAdd,
	})
	addControl := container.NewStack(addRingSized, container.NewCenter(plusImg), addBtn)

	title := NewBrandText("Add New Connect", 13, design.ColorTextLight, true)

	subtitleLine1 := canvas.NewText("Scan a QR code or paste a link", addConnectionCardMutedColor)
	subtitleLine2 := canvas.NewText("to add a hardware or software agent", addConnectionCardMutedColor)
	for _, line := range []*canvas.Text{subtitleLine1, subtitleLine2} {
		line.TextSize = 10
		line.Alignment = fyne.TextAlignCenter
	}
	subtitle := container.New(&connectionSubtitleLayout{gap: 2}, subtitleLine1, subtitleLine2)

	// Title and subtitle sit closer to each other (Gap: 4) than either does
	// to the icon above or the buttons below (the outer Gap: 10) -- read as
	// one text block, not three evenly-spaced rows.
	titleGroup := container.New(&tightStatsVBoxLayout{Gap: 4},
		container.NewCenter(title),
		container.New(&capWidthLayout{MaxWidth: 220}, subtitle),
	)

	// Chain-link glyph (assets.LinkIconMuted/LinkIconLime) -- same icon the
	// Add Connection dialog's own Paste Link button now uses, replacing
	// this card's previous clipboard glyph so "paste a link" reads as one
	// consistent affordance across both surfaces.
	pasteIcon := assets.LinkIconMuted
	pasteIconHover := assets.LinkIconLime

	qrBtn := newIconChromeButton(iconChromeButtonSpec{
		NormalFill:      color.Transparent,
		HoverFill:       color.Transparent, // lime border/icon/text alone carry the hover -- no gray wash behind them
		DisabledFill:    connectionActionBlockedFill,
		Stroke:          design.ColorTailscaleChipBorder,
		StrokeWidth:     1,
		CornerRadius:    6,
		LabelColor:      addConnectionCardMutedColor,
		HoverLabelColor: addConnectionCardHoverColor,
		HoverStroke:     addConnectionCardHoverColor,
		LabelSize:       10,
		NormalIcon:      assets.QRCodeLight,
		HoverIcon:       assets.QRCodeAccent,
		IconSize:        fyne.NewSize(12, 12),
		ButtonSize:      fyne.NewSize(0, 26),
		OnTapped:        actions.OnQR,
	})
	qrBtn.SetText("Scan QR")

	pasteBtn := newIconChromeButton(iconChromeButtonSpec{
		NormalFill:      color.Transparent,
		HoverFill:       color.Transparent, // lime border/icon/text alone carry the hover -- no gray wash behind them
		DisabledFill:    connectionActionBlockedFill,
		Stroke:          design.ColorTailscaleChipBorder,
		StrokeWidth:     1,
		CornerRadius:    6,
		LabelColor:      addConnectionCardMutedColor,
		HoverLabelColor: addConnectionCardHoverColor,
		HoverStroke:     addConnectionCardHoverColor,
		LabelSize:       10,
		NormalIcon:      pasteIcon,
		HoverIcon:       pasteIconHover,
		IconSize:        fyne.NewSize(12, 12),
		ButtonSize:      fyne.NewSize(0, 26),
		OnTapped:        actions.OnPasteLink,
	})
	pasteBtn.SetText("Paste Link")

	buttonsRow := container.New(&DeviceRowControlsLayout{Gap: 10}, qrBtn, pasteBtn)

	content := container.New(&tightStatsVBoxLayout{Gap: 10},
		container.NewCenter(addControl),
		titleGroup,
		container.NewCenter(buttonsRow),
	)

	// tightStatsVBoxLayout stacks top-down and only ever takes as much
	// height as its children need -- verticalCenterLayout gives it the
	// card's full height back and centers it in there, the way the
	// reference mock centers its own placeholder content.
	centered := container.New(&verticalCenterLayout{}, content)

	cardBg := canvas.NewRectangle(design.ColorGray900)
	cardBg.CornerRadius = design.RadiusLG
	cardBg.SetMinSize(fyne.NewSize(connectionCardWidth, connectionCardHeight))

	dashedBorder, setBorderHovered := newAddConnectionCardDashedBorder()

	// setHovered lights the dashed border and the title up on hover --
	// wired to the whole-card overlay (for blank areas) and to each of the
	// card's own buttons' OnHover (for when the cursor is actually over one
	// of them, which the overlay -- sitting behind them in the stack --
	// never sees). Same double-wiring NewConnectionGridCard's own
	// setCardHovered uses for the same reason. qrBtn/pasteBtn additionally
	// swap their own border/icon/label to the same lime via HoverStroke/
	// HoverIcon/HoverLabelColor above -- independent of this, since that's
	// scoped to hovering that specific button rather than the whole card.
	setHovered := func(hovered bool) {
		setBorderHovered(hovered)
		if hovered {
			title.Color = addConnectionCardHoverColor
			addRing.StrokeColor = addConnectionCardHoverColor
			addRing.FillColor = addConnectionCardRingHoverTint
			plusImg.Resource = plusIconHover
		} else {
			title.Color = design.ColorTextLight
			addRing.StrokeColor = addConnectionCardMutedColor
			addRing.FillColor = color.Transparent
			plusImg.Resource = plusIconNormal
		}
		title.Refresh()
		addRing.Refresh()
		plusImg.Refresh()
	}
	addBtn.spec.OnHover = setHovered
	qrBtn.spec.OnHover = setHovered
	pasteBtn.spec.OnHover = setHovered
	overlay := newConnectionCardOverlay(nil, setHovered)

	return container.NewStack(overlay, cardBg, centered, dashedBorder)
}

// newAddConnectionCardDashedBorder draws the dashed rounded-rect outline
// that sets this tile apart from a real connection card's solid border.
// canvas.Rectangle has no dash pattern of its own, so this renders one as a
// small static SVG instead -- sized to exactly match connectionCardWidth/
// Height (the size this tile, like every grid card, always actually ends up
// at via GridWrap's fixed cell size), so ImageFillStretch never has to
// distort anything to make it fit.
//
// The rounded corners are drawn as an explicit path with elliptical-arc
// segments rather than a plain <rect rx="..."> -- Fyne's SVG rasterizer
// doesn't honor a rect's rx/ry, which is what left the border's corners
// sharp (and, worse, "чутка pointy" corners on a dashed line read as a much
// squarer card overall than the same rounding on a solid one would).
// Returns the image plus a setter that swaps it between its normal gray and
// hover-lime variants (see NewAddConnectionGridCard's setHovered wiring).
func newAddConnectionCardDashedBorder() (img *canvas.Image, setHovered func(hovered bool)) {
	normal := fyne.NewStaticResource("add-connection-dashed-border.svg", []byte(addConnectionCardDashedBorderSVG("#656565")))
	hover := fyne.NewStaticResource("add-connection-dashed-border-hover.svg", []byte(addConnectionCardDashedBorderSVG(addConnectionCardHoverColorHex)))

	img = canvas.NewImageFromResource(normal)
	img.FillMode = canvas.ImageFillStretch

	setHovered = func(hovered bool) {
		if hovered {
			img.Resource = hover
		} else {
			img.Resource = normal
		}
		img.Refresh()
	}
	return img, setHovered
}

// addConnectionCardDashedBorderSVG builds the dashed rounded-rect path SVG,
// stroked in strokeColor -- see newAddConnectionCardDashedBorder.
func addConnectionCardDashedBorderSVG(strokeColor string) string {
	const inset = float32(0.75) // half the 1.5 stroke-width, so it isn't clipped at the viewBox edge
	r := design.RadiusLG
	x0, y0 := inset, inset
	x1, y1 := connectionCardWidth-inset, connectionCardHeight-inset

	d := fmt.Sprintf(
		"M %g %g L %g %g A %g %g 0 0 1 %g %g L %g %g A %g %g 0 0 1 %g %g L %g %g A %g %g 0 0 1 %g %g L %g %g A %g %g 0 0 1 %g %g Z",
		x0+r, y0,
		x1-r, y0,
		r, r, x1, y0+r,
		x1, y1-r,
		r, r, x1-r, y1,
		x0+r, y1,
		r, r, x0, y1-r,
		x0, y0+r,
		r, r, x0+r, y0,
	)

	return fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %g %g"><path d="%s" fill="none" stroke="%s" stroke-width="1.5" stroke-dasharray="5 4"/></svg>`,
		connectionCardWidth, connectionCardHeight, d, strokeColor,
	)
}

// capWidthLayout passes its one child through to Layout unchanged (full
// available size, so a wrapped/centered element inside still gets the real
// width to lay out against) but reports a MinSize width capped at MaxWidth
// regardless of what the child would otherwise ask for -- used to keep the
// subtitle's own MinSize from ballooning tightStatsVBoxLayout's reported
// width out past the card.
type capWidthLayout struct {
	MaxWidth float32
}

func (l *capWidthLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) == 0 {
		return fyne.NewSize(0, 0)
	}
	m := objects[0].MinSize()
	if m.Width > l.MaxWidth {
		m.Width = l.MaxWidth
	}
	return m
}

func (l *capWidthLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	objects[0].Resize(size)
	objects[0].Move(fyne.NewPos(0, 0))
}

// verticalCenterLayout stretches its one child to the parent's full width
// but vertically centers it at its own natural (MinSize) height instead of
// stretching it to fill the parent's height too -- container.NewCenter
// would shrink the child to its MinSize on both axes, which is what this
// avoids (the subtitle inside needs the full width to wrap at, not just its
// unwrapped MinSize width).
type verticalCenterLayout struct{}

func (l *verticalCenterLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) == 0 {
		return fyne.NewSize(0, 0)
	}
	return objects[0].MinSize()
}

func (l *verticalCenterLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	child := objects[0]
	childHeight := child.MinSize().Height
	y := (size.Height - childHeight) / 2
	if y < 0 {
		y = 0
	}
	child.Move(fyne.NewPos(0, y))
	child.Resize(fyne.NewSize(size.Width, childHeight))
}
