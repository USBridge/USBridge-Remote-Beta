package design

import (
	"image/color"

	"fyne.io/fyne/v2"
	fynetheme "fyne.io/fyne/v2/theme"
)

var (
	ColorBackground         = color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xff} // --cs-bg-color
	ColorSurface            = color.NRGBA{R: 0x11, G: 0x11, B: 0x11, A: 0xff} // --cs-surface-color
	ColorInputBackground    = color.NRGBA{R: 0x17, G: 0x17, B: 0x17, A: 0xff} // --cs-input-bg-color
	ColorAccent             = color.NRGBA{R: 0x93, G: 0xc5, B: 0x72, A: 0xff} // --cs-accent
	ColorAccentHover        = color.NRGBA{R: 0xb6, G: 0xea, B: 0x93, A: 0xff} // --cs-accent-hover
	ColorAlert              = color.NRGBA{R: 0xe9, G: 0x8a, B: 0x2b, A: 0xff}
	ColorTextLight          = color.NRGBA{R: 0xf5, G: 0xf5, B: 0xf5, A: 0xff} // --cs-text-light
	ColorTextMuted          = color.NRGBA{R: 0xc9, G: 0xc9, B: 0xc9, A: 0xff} // --cs-text-muted
	ColorBorder             = color.NRGBA{R: 0x65, G: 0x65, B: 0x65, A: 0xff} // --cs-border-color
	ColorSurfaceLight       = color.NRGBA{R: 0x35, G: 0x35, B: 0x35, A: 0xff} // --cs-surface-light
	ColorGray900            = color.NRGBA{R: 0x18, G: 0x1c, B: 0x1f, A: 0xff} // --cs-gray-900 (Header)
	ColorGray950            = color.NRGBA{R: 0x0b, G: 0x0f, B: 0x12, A: 0xff} // --cs-gray-950 (Window)
	ColorGray400            = color.NRGBA{R: 0xc8, G: 0xc8, B: 0xc8, A: 0xff} // --cs-gray-400
	ColorAlphaWhite07       = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x12} // --cs-alpha-white-07
	ColorAlphaWhite12       = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x1f} // --cs-alpha-white-12
	ColorAlphaWhite15       = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x26} // --cs-alpha-white-15
	ColorAlphaWhite24       = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x3d} // --cs-alpha-white-24
	ColorAlphaAccent22      = color.NRGBA{R: 0x93, G: 0xc5, B: 0x72, A: 0x38} // --cs-alpha-accent-22
	ColorAlphaAccent55      = color.NRGBA{R: 0x93, G: 0xc5, B: 0x72, A: 0x8c} // --cs-alpha-accent-55
	ColorAlphaAccentHover55 = color.NRGBA{R: 0xb6, G: 0xea, B: 0x93, A: 0x8c} // --cs-alpha-accent-hover-55
	ColorWhite              = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}

	// Tailscale header chip: distinct from the main ColorAccent/ColorAccentHover
	// used everywhere else, used only by the Tailscale toggle border/label in
	// its default (off, enabled) state.
	ColorTailscaleChipBorder = color.NRGBA{R: 0x42, G: 0x46, B: 0x38, A: 0xff} // was #e5f5b4
	ColorTailscaleChipLabel  = color.NRGBA{R: 0xc3, G: 0xc6, B: 0xb4, A: 0xff}

	// ColorHeaderAccentLine is the brand accent color for the thin line under
	// the connections screen's header bar (previously also the standalone
	// "USBridge" wordmark's text color, before that got folded into the
	// combined logo+wordmark lockup image). Also used for the thin line under
	// the connections section header (ConnectionsSummary badges + actions).
	ColorHeaderAccentLine = color.NRGBA{R: 0x42, G: 0x46, B: 0x38, A: 0xff} // was #e7fbba

	// Connections section header's category-count badges ("2 Agent",
	// "3 KVM"): border, solid fill, and text are three independent colors
	// now (was border+fill both derived from one accent, at #30d4bd). Border
	// happens to match ColorHeaderAccentLine's value -- kept as its own
	// token since it's a different role that could diverge later.
	ColorConnectionBadgeBorder = color.NRGBA{R: 0x42, G: 0x46, B: 0x38, A: 0xff}
	ColorConnectionBadgeFill   = color.NRGBA{R: 0x1c, G: 0x20, B: 0x23, A: 0xff}
	ColorConnectionBadgeText   = color.NRGBA{R: 0x41, G: 0xe0, B: 0xc3, A: 0xff} // was #30d4bd

	// ColorConnectionAddFill/Hover are the connections section header's "+"
	// button -- deliberately light-on-dark inverted from every other button
	// in this app (see iconChromeButtonSpec.LabelColor). Hover shade is a
	// placeholder guess (lightened fill) pending design review.
	ColorConnectionAddFill      = color.NRGBA{R: 0xc4, G: 0xe7, B: 0x7a, A: 0xff} // was #c3f270
	ColorConnectionAddFillHover = color.NRGBA{R: 0xd6, G: 0xf7, B: 0x9c, A: 0xff}

	// ColorConnectionsSectionIcon tints the QR/paste-link icons in the
	// connections section header. Their border reuses ColorHeaderAccentLine
	// (same #424638) rather than a separate token, since it's the same role.
	ColorConnectionsSectionIcon = color.NRGBA{R: 0xe9, G: 0xfd, B: 0xbb, A: 0xff}

	// ColorConnectionsSectionTitle/Subtitle are the "Connections" title and
	// the "Your active..." line under it.
	ColorConnectionsSectionTitle    = color.NRGBA{R: 0xe0, G: 0xe3, B: 0xe7, A: 0xff}
	ColorConnectionsSectionSubtitle = color.NRGBA{R: 0xc5, G: 0xc8, B: 0xb5, A: 0xff} // was #e9fdbb

	// ColorConnectionsSectionMutedText is shared by the subtitle's tone and
	// the Grid/List toggle's inactive side -- same #c5c8b5 as
	// ColorConnectionsSectionSubtitle, kept separate since they're different
	// roles.
	ColorConnectionsSectionMutedText = color.NRGBA{R: 0xc5, G: 0xc8, B: 0xb5, A: 0xff}

	// ColorConnectionsSectionUnderline is the thin line under the
	// connections section header -- its own color, distinct from
	// ColorHeaderAccentLine (the app header's equivalent line).
	ColorConnectionsSectionUnderline = color.NRGBA{R: 0x26, G: 0x29, B: 0x24, A: 0xff}

	// ColorLoginAvatarBg/Text are the placeholder account/login avatar in
	// the app header (connection_header.go's loginAvatarButton). Border
	// reuses ColorHeaderAccentLine.
	ColorLoginAvatarBg   = color.NRGBA{R: 0x2d, G: 0x2f, B: 0x34, A: 0xff}
	ColorLoginAvatarText = color.NRGBA{R: 0xe9, G: 0xfd, B: 0xbb, A: 0xff}

	// ColorDanger marks an error state (e.g. the connecting toast turning
	// into an inline error -- view.ConnectingToastHandle.ShowError). Muted
	// rather than a harsh saturated red, to match this palette's generally
	// desaturated tones (compare ColorAlert's muted orange).
	ColorDanger = color.NRGBA{R: 0xd9, G: 0x5c, B: 0x5c, A: 0xff}
)

const RadiusMD float32 = 8
const RadiusLG float32 = 10

const (
	ColorNameCodeKeyword fyne.ThemeColorName = "code-keyword"
	ColorNameCodeBuiltin fyne.ThemeColorName = "code-builtin"
	ColorNameCodeString  fyne.ThemeColorName = "code-string"
	ColorNameCodeComment fyne.ThemeColorName = "code-comment"
	ColorNameCodeNumber  fyne.ThemeColorName = "code-number"
	ColorNameCodeDefault fyne.ThemeColorName = "code-default"
)

// BrandTheme fixes the application to the current brand dark palette.
// Until a separate light palette is defined, both theme variants use the same colors.
type BrandTheme struct {
	fallback fyne.Theme
}

func NewBrandTheme() fyne.Theme {
	return &BrandTheme{fallback: fynetheme.DefaultTheme()}
}

func (t *BrandTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch name {
	case fynetheme.ColorNameBackground:
		return ColorGray950
	case fynetheme.ColorNameButton:
		return ColorSurfaceLight
	case fynetheme.ColorNameDisabledButton:
		return ColorGray900
	case fynetheme.ColorNameDisabled:
		return ColorBorder
	case fynetheme.ColorNameFocus:
		return ColorAlphaAccent22
	case fynetheme.ColorNameForeground:
		return ColorTextLight
	case fynetheme.ColorNameForegroundOnPrimary:
		return ColorBackground
	case fynetheme.ColorNameHeaderBackground:
		return ColorGray950
	case fynetheme.ColorNameHover:
		return ColorAlphaWhite15
	case fynetheme.ColorNameHyperlink:
		return ColorAccent
	case fynetheme.ColorNameInputBackground:
		return ColorInputBackground
	case fynetheme.ColorNameInputBorder:
		return ColorBorder
	case fynetheme.ColorNameMenuBackground:
		return ColorGray950
	case fynetheme.ColorNameOverlayBackground:
		// Transparent, not ColorGray950 (fully opaque) -- widget.PopUp's own
		// renderer (fyne's popup.go) always paints this color across the
		// *entire* canvas as its background layer, underneath whatever
		// content the popup was given. Every dialog in this app
		// (view.NewOverlayPopup/ShowOverlayPopup) draws its own translucent
		// "dim" rectangle plus an opaque panel background as part of that
		// content -- so with this opaque, the real backdrop was always
		// Fyne's own 100%-opaque layer sitting behind it, making the
		// carefully-tuned translucent dim rect (e.g. DimColor's A:0x72)
		// pointless: the window behind a dialog read as flat black instead
		// of dimmed-but-visible. Transparent here hands full control of the
		// backdrop to each dialog's own dim rectangle, which is the point.
		return color.Transparent
	case fynetheme.ColorNamePlaceHolder:
		return ColorTextMuted
	case fynetheme.ColorNamePressed:
		return ColorAlphaWhite24
	case fynetheme.ColorNamePrimary:
		return ColorAccent
	case fynetheme.ColorNameScrollBar:
		return ColorBorder
	case fynetheme.ColorNameScrollBarBackground:
		return ColorGray950
	case fynetheme.ColorNameSelection:
		return ColorAlphaAccent22
	case fynetheme.ColorNameSeparator:
		return ColorBorder
	case fynetheme.ColorNameShadow:
		return color.Transparent
	case fynetheme.ColorNameSuccess:
		return ColorAccent
	case fynetheme.ColorNameWarning:
		return ColorAccentHover
	case ColorNameCodeKeyword:
		return color.NRGBA{R: 0x56, G: 0x9C, B: 0xD6, A: 0xFF} // blue
	case ColorNameCodeBuiltin:
		return color.NRGBA{R: 0x4E, G: 0xC9, B: 0xB0, A: 0xFF} // teal
	case ColorNameCodeString:
		return color.NRGBA{R: 0xCE, G: 0x91, B: 0x78, A: 0xFF} // orange
	case ColorNameCodeComment:
		return color.NRGBA{R: 0x6A, G: 0x99, B: 0x55, A: 0xFF} // green
	case ColorNameCodeNumber:
		return color.NRGBA{R: 0xB5, G: 0xCE, B: 0xA8, A: 0xFF} // light green
	case ColorNameCodeDefault:
		return color.NRGBA{R: 0xD4, G: 0xD4, B: 0xD4, A: 0xFF} // light gray
	}

	return t.fallback.Color(name, fynetheme.VariantDark)
}

func (t *BrandTheme) Font(style fyne.TextStyle) fyne.Resource {
	return t.fallback.Font(style)
}

func (t *BrandTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return t.fallback.Icon(name)
}

func (t *BrandTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case fynetheme.SizeNameInputRadius, fynetheme.SizeNameSelectionRadius, fynetheme.SizeNameWindowButtonRadius:
		return RadiusMD
	}
	return t.fallback.Size(name)
}
