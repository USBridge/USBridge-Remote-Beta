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
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// accountCardBg is a touch lighter than the dialog's own ColorGray900
// background -- just enough for the identity/sync cards to read as
// distinct panels sitting on it, without introducing a whole new surface
// color for one dialog.
var accountCardBg = color.NRGBA{R: 0x1e, G: 0x23, B: 0x1f, A: 0xff}

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
	bg.SetMinSize(fyne.NewSize(44, 44))

	label := canvas.NewText(letter, design.ColorGray950)
	label.TextSize = 18
	label.TextStyle = fyne.TextStyle{Bold: true}
	label.Alignment = fyne.TextAlignCenter

	return container.NewStack(bg, container.NewCenter(label))
}

// newAccountStatusPill is the small "on"/"off" badge next to "Connections
// sync" -- a status readout, not a control (see accountSyncPassphraseSection's
// own doc comment for why: there is no separate enable/disable toggle here,
// sync is simply "on" once a passphrase has been set).
func newAccountStatusPill(label string, on bool) fyne.CanvasObject {
	text := canvas.NewText(strings.ToUpper(label), design.ColorGray950)
	text.TextSize = 9
	text.TextStyle = fyne.TextStyle{Bold: true}

	bg := canvas.NewRectangle(design.ColorConnectionAddFill)
	bg.CornerRadius = 8
	if !on {
		bg.FillColor = design.ColorConnectionBadgeFill
		bg.StrokeColor = design.ColorBorder
		bg.StrokeWidth = 1
		text.Color = design.ColorTextMuted
	}

	return container.NewStack(bg, view.NewInset(text, 6, 6, 3, 3))
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
func newAccountLicenseRow(kind, identifier, status string, window fyne.Window) fyne.CanvasObject {
	kindText := canvas.NewText("["+kind+"]", design.ColorConnectionBadgeText)
	kindText.TextSize = 11
	kindText.TextStyle = fyne.TextStyle{Bold: true}

	idText := canvas.NewText(identifier, design.ColorTextLight)
	idText.TextSize = 11

	statusColor := licenseStatusColor(status)
	statusText := canvas.NewText("— ● "+status, statusColor)
	statusText.TextSize = 11

	left := container.New(&centeredInlineLayout{gap: 6, minGap: 4}, kindText, idText, statusText)

	copyBtn := widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() {
		if window != nil && window.Clipboard() != nil {
			window.Clipboard().SetContent(identifier)
		}
	})
	copyBtn.Importance = widget.LowImportance

	return container.NewBorder(nil, nil, nil, copyBtn, left)
}
