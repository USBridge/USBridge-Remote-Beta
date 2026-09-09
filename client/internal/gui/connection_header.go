package gui

import (
	"image/color"
	"runtime"
	"strings"

	"usbridge-client/internal/gui/assets"
	"usbridge-client/internal/gui/design"
	"usbridge-client/internal/gui/view"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// connectionHeaderActions are the events the connection screen's header can
// report -- what the user tapped, never what should happen as a result. The
// composition root (createConnectionAddressBar) wires these to the actual
// controller calls.
type connectionHeaderActions struct {
	// OnSelectLanguage fires with "en"/"es"/"uk" when the language dropdown's
	// selection changes.
	OnSelectLanguage  func(code string)
	OnOpenCommunity   func()
	OnOpenInfo        func()
	OnToggleTailscale func()
	// OnOpenAccount opens the account login/sync dialog (see
	// MainWindow.showAccountDialog) -- fired by the login avatar button.
	OnOpenAccount func()
}

// ConnectionHeaderHandle lets the controller push live Tailscale status and
// account/login state into an already-built connection header, without
// owning (or even seeing the type of) any of its widgets. nil-safe: a nil
// accessory (wasm builds, see newConnectionHeader) yields a handle whose
// SetTailscaleState is a no-op.
type ConnectionHeaderHandle struct {
	toggle *tailscaleHeaderToggle
	avatar *loginAvatarButton
}

// SetTailscaleState updates the header's Tailscale toggle from the same raw
// status/auth-label strings the tsnet polling loop already produces.
func (h *ConnectionHeaderHandle) SetTailscaleState(status, authLabel string) {
	if h == nil || h.toggle == nil {
		return
	}
	active, loading := summarizeTailscaleState(status, authLabel)
	h.toggle.SetOn(active)
	h.toggle.SetLoading(loading)
	h.toggle.SetDisabled(loading) // Block button during transition
}

// SetAccountState updates the login avatar button from the account manager's
// login state -- teal background with the account email's first letter while
// logged in, the plain gray "U" placeholder otherwise (see
// loginAvatarButton.SetState).
func (h *ConnectionHeaderHandle) SetAccountState(loggedIn bool, email string) {
	if h == nil || h.avatar == nil {
		return
	}
	h.avatar.SetState(loggedIn, email)
}

// headerCompactButtonSize is how big the info/community/language buttons
// render in this header -- smaller and closer together than
// headerStatusBadgeButton's own 36x36 default (used as-is by the connected
// screen's video/audio status buttons), which is why each one here is
// wrapped in a GridWrap rather than changing that shared default.
var headerCompactButtonSize = fyne.NewSize(28, 28)

// languageDropdownLabel maps a stored language code ("en"/"es"/"uk", plus
// the legacy "ua") to the short display label the header's language
// dropdown shows/collapses to -- mirrors the per-connection protocol
// dropdown's own AUTO/TS/LAN short-code convention.
func languageDropdownLabel(code string) string {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "es":
		return "ES"
	case "uk", "ua":
		return "UA"
	default:
		return "EN"
	}
}

// languageDropdownCode is languageDropdownLabel's inverse -- the dropdown's
// OnSelected callback receives one of its own option labels back, which
// this resolves to the actual code i18n.SetLanguage/app.Preferences expect.
func languageDropdownCode(label string) string {
	switch label {
	case "ES":
		return "es"
	case "UA":
		return "uk"
	default:
		return "en"
	}
}

// newConnectionHeader builds the top bar shown on the connections screen
// (before a device is connected): logo+wordmark lockup on the left, and on
// the right the Tailscale toggle, info, community and language buttons. The
// returned handle is how the controller later pushes Tailscale status into
// the toggle it just built. currentLanguage seeds the language dropdown's
// initial selection (see languageDropdownLabel) -- the caller re-builds this
// whole header on every language change (MainWindow.reloadUI ->
// recreateContainers -> createConnectionAddressBar), so this never needs to
// change in place after construction.
//
// This is the desktop-only design for now -- there is no mobile variant of
// this component yet. When one exists, the choice between them belongs in
// the caller (createConnectionAddressBar), not inside this component.
func newConnectionHeader(actions connectionHeaderActions, currentLanguage string) (*fyne.Container, *ConnectionHeaderHandle) {
	logoLockup := canvas.NewImageFromResource(assets.LogoUSBridgeLockup)
	logoLockup.FillMode = canvas.ImageFillContain
	const logoAspectRatio = 951.0 / 236.0
	const logoHeight = 28
	logoLockup.SetMinSize(fyne.NewSize(logoHeight*logoAspectRatio, logoHeight))

	// Styled to match the per-connection protocol dropdown's own AUTO/TS/LAN
	// pill exactly (same border/text/hover colors, size, font) -- same
	// component (view.HeaderDropdown), just a different option set.
	langDropdown := view.NewHeaderDropdown([]string{"EN", "ES", "UA"}, languageDropdownLabel(currentLanguage), func(label string) {
		if actions.OnSelectLanguage != nil {
			actions.OnSelectLanguage(languageDropdownCode(label))
		}
	})
	langDropdown.UltraCompact = true
	langDropdown.CornerRadius = 6
	langDropdown.BorderColor = design.ColorTailscaleChipBorder
	langDropdown.TextColor = design.ColorConnectionBadgeText
	langDropdown.IconColor = color.NRGBA{R: 0xc5, G: 0xc8, B: 0xb5, A: 0xff}
	langDropdown.TextSize = 10
	langDropdown.HoverBorderColor = design.ColorConnectionBadgeText
	langDropdown.HoverFillColor = design.ColorGray900

	communityBtn := newHeaderStatusBadgeButton(assets.DiscordIconHeader, func() {
		if actions.OnOpenCommunity != nil {
			actions.OnOpenCommunity()
		}
	})
	communityBtn.SetBadgeText("")
	communityBtn.SetIconSize(fyne.NewSize(15, 15))

	infoBtn := newHeaderStatusBadgeButton(assets.QuestionIconHeader, func() {
		if actions.OnOpenInfo != nil {
			actions.OnOpenInfo()
		}
	})
	infoBtn.SetBadgeText("")
	infoBtn.SetIconSize(fyne.NewSize(15, 15))

	// Account/login avatar -- opens the account login/sync dialog (see
	// connectionHeaderActions.OnOpenAccount, wired by createConnectionAddressBar
	// to MainWindow.showAccountDialog).
	loginBtn := newLoginAvatarButton("U", func() {
		if actions.OnOpenAccount != nil {
			actions.OnOpenAccount()
		}
	})

	var tailscaleAccessory fyne.CanvasObject
	handle := &ConnectionHeaderHandle{avatar: loginBtn}
	if runtime.GOOS == "js" {
		// No embedded tsnet in a browser tab (tailscale_service_wasm.go is a
		// stub) -- the "Sign In With Google" toggle has nothing to do here,
		// so don't show it at all rather than show a button that can't
		// function. handle.toggle stays nil, so SetTailscaleState is a no-op.
		tailscaleAccessory = canvas.NewRectangle(color.Transparent)
	} else {
		toggle := newTailscaleHeaderToggle(actions.OnToggleTailscale)
		handle.toggle = toggle
		tailscaleAccessory = toggle
	}

	rightRow := container.New(&centeredInlineLayout{gap: 4, minGap: 2},
		tailscaleAccessory,
		container.NewGridWrap(headerCompactButtonSize, infoBtn),
		container.NewGridWrap(headerCompactButtonSize, communityBtn),
		// Not GridWrap'd like the icon-only buttons above -- langDropdown
		// sizes itself to its own short label text (see HeaderDropdown.
		// MinSize), not a fixed square.
		langDropdown,
		container.NewGridWrap(headerCompactButtonSize, loginBtn),
	)

	row := container.NewHBox(logoLockup, layout.NewSpacer(), rightRow)

	bg := canvas.NewRectangle(design.ColorGray900)
	paddedRow := view.NewInset(row, 16, 16, 2, 2)

	accentLine := canvas.NewRectangle(design.ColorHeaderAccentLine)
	accentLine.SetMinSize(fyne.NewSize(1, 0.5))

	content := container.NewBorder(nil, accentLine, nil, nil, paddedRow)

	return container.NewStack(bg, content), handle
}

// summarizeTailscaleState turns the tsnet polling loop's free-form status
// text into the toggle's two boolean visual states (on, loading). authLabel
// is currently unused (kept for signature symmetry with the raw status
// strings the polling loop already has on hand).
func summarizeTailscaleState(status, _ string) (bool, bool) {
	raw := strings.ToLower(strings.TrimSpace(status))

	switch {
	case strings.Contains(raw, "signed out"), strings.Contains(raw, "not connected"), strings.Contains(raw, "needslogin"), strings.Contains(raw, "loggedout"):
		return false, false
	case strings.Contains(raw, "starting"), strings.Contains(raw, "signing"), strings.Contains(raw, "browser opened"), strings.Contains(raw, "auth url"), strings.Contains(raw, "checking"):
		return false, true
	case strings.Contains(raw, "stopped"), strings.Contains(raw, "no state"), strings.Contains(raw, "login failed"):
		return false, false
	case strings.Contains(raw, "running"), strings.Contains(raw, "connected"), strings.Contains(raw, "active"):
		return true, false
	case strings.Contains(raw, "tailscale:"):
		return false, false
	default:
		return false, false
	}
}

// tailscaleHeaderToggle is the small pill switch in the connection header
// that shows/toggles Tailscale sign-in state (see ConnectionHeaderHandle for
// how the controller drives it).
type tailscaleHeaderToggle struct {
	widget.BaseWidget

	onTapped func()
	on       bool
	loading  bool
	disabled bool
	hovered  bool

	bg     *canvas.Rectangle
	border *canvas.Rectangle
	label  *canvas.Text
	track  *canvas.Rectangle
	thumb  *canvas.Circle
}

func newTailscaleHeaderToggle(onTapped func()) *tailscaleHeaderToggle {
	toggle := &tailscaleHeaderToggle{onTapped: onTapped}
	toggle.ExtendBaseWidget(toggle)
	return toggle
}

func (t *tailscaleHeaderToggle) SetOn(on bool) {
	t.on = on
	t.refreshVisuals()
	t.Refresh()
}

func (t *tailscaleHeaderToggle) SetLoading(loading bool) {
	t.loading = loading
	if loading {
		t.hovered = false
	}
	t.refreshVisuals()
	t.Refresh()
}

func (t *tailscaleHeaderToggle) SetDisabled(disabled bool) {
	t.disabled = disabled
	if disabled {
		t.hovered = false
	}
	t.refreshVisuals()
	t.Refresh()
}

func (t *tailscaleHeaderToggle) Tapped(e *fyne.PointEvent) {
	if t.disabled || t.loading || t.onTapped == nil {
		return
	}
	if e.Position.X < t.Size().Width-36 {
		return
	}
	t.onTapped()
}

func (t *tailscaleHeaderToggle) TappedSecondary(*fyne.PointEvent) {}

func (t *tailscaleHeaderToggle) MouseIn(e *desktop.MouseEvent) {
	if t.disabled || t.loading {
		return
	}
	t.hovered = true
	t.refreshVisuals()
}

func (t *tailscaleHeaderToggle) MouseMoved(e *desktop.MouseEvent) {
	hover := false
	if !t.disabled && !t.loading && e.Position.X >= t.Size().Width-36 {
		hover = true
	}
	if t.hovered != hover {
		t.hovered = hover
		t.refreshVisuals()
	}
}

func (t *tailscaleHeaderToggle) MouseOut() {
	if !t.hovered {
		return
	}
	t.hovered = false
	t.refreshVisuals()
}

func (t *tailscaleHeaderToggle) MinSize() fyne.Size {
	return fyne.NewSize(92, 24)
}

func (t *tailscaleHeaderToggle) CreateRenderer() fyne.WidgetRenderer {
	t.bg = canvas.NewRectangle(design.ColorSurfaceLight)
	t.bg.CornerRadius = 12

	t.border = canvas.NewRectangle(color.Transparent)
	t.border.CornerRadius = 12
	t.border.StrokeColor = design.ColorAccent
	t.border.StrokeWidth = 1

	t.label = canvas.NewText("Tailscale", design.ColorTextMuted)
	t.label.TextSize = 10
	t.label.TextStyle = fyne.TextStyle{Bold: true}
	t.label.Alignment = fyne.TextAlignLeading

	t.track = canvas.NewRectangle(design.ColorSurfaceLight)
	t.track.CornerRadius = 7

	t.thumb = canvas.NewCircle(design.ColorGray400)

	t.refreshVisuals()
	return &tailscaleHeaderToggleRenderer{toggle: t}
}

func (t *tailscaleHeaderToggle) refreshVisuals() {
	if t.bg == nil || t.border == nil || t.label == nil || t.track == nil || t.thumb == nil {
		return
	}

	bgColor := design.ColorGray950
	borderColor := design.ColorTailscaleChipBorder
	labelColor := design.ColorTailscaleChipLabel

	trackColor := design.ColorGray900
	thumbColor := design.ColorGray400

	if t.on {
		trackColor = design.ColorAccent
		thumbColor = design.ColorWhite
	}
	if t.disabled {
		// Loading always sets disabled too (see ConnectionHeaderHandle.
		// SetTailscaleState) -- this is the toggle's only "thinking" cue: no
		// spinner, just the track/thumb dimming and going unclickable.
		labelColor = design.ColorGray400
		trackColor = design.ColorGray950
		borderColor = design.ColorGray900
	}

	t.bg.FillColor = bgColor
	t.border.StrokeColor = borderColor
	t.border.StrokeWidth = 1
	t.label.Color = labelColor

	t.track.FillColor = trackColor
	t.thumb.FillColor = thumbColor

	if t.disabled {
		t.thumb.FillColor = design.ColorGray900
	}

	t.bg.Refresh()
	t.border.Refresh()
	t.label.Refresh()
	t.track.Refresh()
	t.thumb.Refresh()
}

type tailscaleHeaderToggleRenderer struct {
	toggle *tailscaleHeaderToggle
}

func (r *tailscaleHeaderToggleRenderer) Layout(size fyne.Size) {
	if r.toggle.bg == nil || r.toggle.border == nil || r.toggle.label == nil || r.toggle.track == nil || r.toggle.thumb == nil {
		return
	}

	r.toggle.bg.Move(fyne.NewPos(0, 0))
	r.toggle.bg.Resize(size)
	r.toggle.border.Move(fyne.NewPos(0, 0))
	r.toggle.border.Resize(size)

	r.toggle.label.Move(fyne.NewPos(10, (size.Height-14)/2))
	r.toggle.label.Resize(fyne.NewSize(55, 14))

	trackSize := fyne.NewSize(24, 14)
	trackX := size.Width - trackSize.Width - 6
	trackY := (size.Height - trackSize.Height) / 2
	r.toggle.track.Move(fyne.NewPos(trackX, trackY))
	r.toggle.track.Resize(trackSize)

	thumbSize := float32(10)
	thumbY := trackY + 2
	thumbX := trackX + 2
	if r.toggle.on {
		thumbX = trackX + trackSize.Width - thumbSize - 2
	}
	r.toggle.thumb.Move(fyne.NewPos(thumbX, thumbY))
	r.toggle.thumb.Resize(fyne.NewSize(thumbSize, thumbSize))
}

func (r *tailscaleHeaderToggleRenderer) MinSize() fyne.Size {
	return r.toggle.MinSize()
}

func (r *tailscaleHeaderToggleRenderer) Refresh() {
	r.toggle.refreshVisuals()
	r.Layout(r.toggle.Size())
}

func (r *tailscaleHeaderToggleRenderer) Destroy() {}

func (r *tailscaleHeaderToggleRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.toggle.bg, r.toggle.label, r.toggle.track, r.toggle.thumb, r.toggle.border}
}

func (r *tailscaleHeaderToggleRenderer) BackgroundColor() color.Color {
	return color.Transparent
}

var (
	_ fyne.Tappable     = (*tailscaleHeaderToggle)(nil)
	_ desktop.Hoverable = (*tailscaleHeaderToggle)(nil)
	_ fyne.Widget       = (*tailscaleHeaderToggle)(nil)
	_ fyne.Tappable     = (*loginAvatarButton)(nil)
	_ desktop.Hoverable = (*loginAvatarButton)(nil)
	_ fyne.Widget       = (*loginAvatarButton)(nil)
)

// loginAvatarButton is the connections screen's placeholder account/login
// avatar (circle + initial letter) shown next to the language icon -- no
// functionality yet. A dedicated widget rather than headerStatusBadgeButton
// for two reasons: that widget's hover fill is a rounded *square*, wrong
// behind a circular avatar, and Fyne's SVG renderer doesn't support <text>
// elements at all -- an SVG-baked letter silently never draws, so it has to
// be a real canvas.Text instead.
type loginAvatarButton struct {
	widget.BaseWidget

	letterText string
	onTapped   func()
	hovered    bool
	// loggedIn switches refreshVisuals from the plain gray placeholder look
	// to a teal-filled avatar -- set via SetState, driven by
	// ConnectionHeaderHandle.SetAccountState (ultimately AccountManager's
	// own login state).
	loggedIn bool

	circle *canvas.Circle
	letter *canvas.Text
}

func newLoginAvatarButton(letterText string, onTapped func()) *loginAvatarButton {
	b := &loginAvatarButton{letterText: letterText, onTapped: onTapped}
	b.ExtendBaseWidget(b)
	return b
}

func (b *loginAvatarButton) MinSize() fyne.Size {
	return fyne.NewSize(24, 24)
}

func (b *loginAvatarButton) CreateRenderer() fyne.WidgetRenderer {
	b.circle = canvas.NewCircle(design.ColorLoginAvatarBg)
	b.circle.StrokeColor = design.ColorHeaderAccentLine
	b.circle.StrokeWidth = 1.5

	b.letter = canvas.NewText(b.letterText, design.ColorLoginAvatarText)
	b.letter.TextSize = 11
	b.letter.TextStyle = fyne.TextStyle{Bold: true}
	b.letter.Alignment = fyne.TextAlignCenter

	b.refreshVisuals()
	return widget.NewSimpleRenderer(container.NewStack(b.circle, container.NewCenter(b.letter)))
}

func (b *loginAvatarButton) Tapped(*fyne.PointEvent) {
	if b.onTapped != nil {
		b.onTapped()
	}
}

func (b *loginAvatarButton) TappedSecondary(*fyne.PointEvent) {}

func (b *loginAvatarButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.refreshVisuals()
}

func (b *loginAvatarButton) MouseMoved(*desktop.MouseEvent) {}

func (b *loginAvatarButton) MouseOut() {
	b.hovered = false
	b.refreshVisuals()
}

// SetState reflects the account manager's login state onto the avatar:
// teal background with the email's first letter (uppercased) while logged
// in, or back to the plain gray placeholder letter ("U") when logged out.
func (b *loginAvatarButton) SetState(loggedIn bool, email string) {
	b.loggedIn = loggedIn
	letter := "U"
	if loggedIn {
		if trimmed := strings.TrimSpace(email); trimmed != "" {
			letter = strings.ToUpper(string([]rune(trimmed)[0]))
		}
	}
	b.letterText = letter
	if b.letter != nil {
		b.letter.Text = letter
		b.letter.Refresh()
	}
	b.refreshVisuals()
}

// refreshVisuals only ever changes the circle's own fill (plus the letter's
// color, since a teal-filled circle needs a dark letter to stay readable) --
// never adds a separate hover shape -- so the hover state stays circular no
// matter what.
func (b *loginAvatarButton) refreshVisuals() {
	if b.circle == nil {
		return
	}
	fill := design.ColorLoginAvatarBg
	letterColor := design.ColorLoginAvatarText
	if b.loggedIn {
		fill = design.ColorConnectionBadgeText
		letterColor = design.ColorGray950
		if b.hovered {
			fill = color.NRGBA{R: 0x61, G: 0xf0, B: 0xd3, A: 0xff} // same hover teal Save/Apply use
		}
	} else if b.hovered {
		fill = design.ColorSurfaceLight
	}
	b.circle.FillColor = fill
	b.circle.Refresh()
	if b.letter != nil {
		b.letter.Color = letterColor
		b.letter.Refresh()
	}
}
