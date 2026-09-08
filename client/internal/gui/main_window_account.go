package gui

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"time"

	"usbridge-client/internal/account"
	"usbridge-client/internal/gui/controller"
	"usbridge-client/internal/gui/design"
	"usbridge-client/internal/gui/view"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// accountDialogSnapshot is the subset of AccountManager state that actually
// changes what showAccountDialog's body needs to look like -- compared
// tick-to-tick by the background poller (see showAccountDialog) so a
// render only happens on a REAL transition (login started/finished/failed,
// logged out), never unconditionally on every 2s tick. Rebuilding the
// whole body on every tick regardless of whether anything changed is what
// caused the dialog to visibly flicker ("No licenses" flashing in and out)
// and, worse, wiped out the sync-passphrase Entry's in-progress text on
// every tick -- widget.NewPasswordEntry() started over from empty each
// time body.RemoveAll() ran, so a passphrase could never actually be typed
// in before the next tick erased it.
type accountDialogSnapshot struct {
	loginInProgress bool
	loggedIn        bool
	lastError       string
}

func newAccountDialogSnapshot(am *controller.AccountManager) accountDialogSnapshot {
	return accountDialogSnapshot{
		loginInProgress: am.LoginInProgress(),
		loggedIn:        am.LoggedIn(),
		lastError:       am.LastError(),
	}
}

// showAccountDialog is the client's account button's single entry point --
// mirrors the Go agent's showLicenseDialog in spirit (a small
// self-re-rendering dialog driven by a status snapshot) but much simpler:
// the client has no billing of its own (see internal/account's package doc
// comment), this is purely "who am I signed in as, and what does that
// account own" -- plus setting up the sync passphrase that end-to-end
// encrypts the synced connections list (see internal/syncconn,
// connection_manager_sync.go).
func (mw *MainWindow) showAccountDialog() {
	if mw.connectionManager == nil || mw.connectionManager.Account == nil {
		return
	}
	cm := mw.connectionManager
	am := cm.Account

	body := container.NewVBox()
	// licensesLoaded/licensesCache/licensesErr: fetched exactly ONCE per
	// dialog open (the first time render() reaches the LoggedIn case), not
	// re-fetched on every render -- see accountLicensesList below. Seeded
	// from AccountManager's own cross-dialog-open cache (am.CachedLicenses)
	// so re-opening the dialog within the same login session renders the
	// real license list on its very FIRST render -- no "Loading…"
	// placeholder, and so no resize once a fetch would otherwise resolve
	// moments later (see accountLicensesList's own doc comment).
	licensesCache, licensesErr, licensesLoaded := am.CachedLicenses()
	// resettingSyncPassphrase: true while the "Forgot passphrase? Reset
	// it" flow (see accountSyncPassphraseSection) is showing its
	// new-passphrase entry -- a UI-only flag, not part of AccountManager's
	// own state, so it has to be threaded through the same way
	// licensesLoaded above is (render() rebuilds the whole body on every
	// call, so anything that must survive across renders lives out here).
	var resettingSyncPassphrase bool

	footerContainer := container.NewStack()

	var render func()
	render = func() {
		body.RemoveAll()
		footerContainer.Objects = nil

		switch {
		case am.LoginInProgress():
			body.Add(widget.NewLabel("Waiting for Google login to complete in your browser…"))
			body.Add(widget.NewProgressBarInfinite())
			cancelBtn := widget.NewButton("Cancel", func() {
				am.CancelLogin()
				render()
			})
			cancelBtn.Importance = widget.LowImportance
			body.Add(container.NewCenter(cancelBtn))

		case am.LoggedIn():
			letter := "U"
			if trimmed := strings.TrimSpace(am.Email()); trimmed != "" {
				letter = strings.ToUpper(string([]rune(trimmed)[0]))
			}
			signedInLabel := canvas.NewText("Signed in as", color.NRGBA{R: 0x8f, G: 0x93, B: 0x81, A: 0xff})
			signedInLabel.TextSize = 10
			emailText := canvas.NewText(am.Email(), design.ColorTextLight)
			emailText.TextSize = 13
			emailText.TextStyle = fyne.TextStyle{Bold: true}
			identityHeader := container.NewHBox(
				newAccountAvatarBadge(letter),
				view.NewInset(container.NewVBox(signedInLabel, emailText), 10, 0, 0, 0),
			)

			identityBody := container.NewVBox(identityHeader)
			if errMsg := am.LastError(); errMsg != "" {
				errText := canvas.NewText(errMsg, design.ColorAlert)
				errText.TextSize = 11
				identityBody.Add(errText)
			}
			identityBody.Add(view.NewInset(newAccountDivider(), 0, 0, 2, 0))
			identityBody.Add(accountLicensesList(am, &licensesLoaded, &licensesCache, &licensesErr, mw.window, render))

			identityBody.Add(newAccountDivider())
			identityBody.Add(accountSyncPassphraseSection(cm, am, &resettingSyncPassphrase, render))

			body.Add(newAccountCard(identityBody))

			var footerLeft fyne.CanvasObject
			if am.HasSyncKey() && !resettingSyncPassphrase {
				footerLeft = newAccountDialogLinkButton("Forgot passphrase? ", "Reset it", func() {
					resettingSyncPassphrase = true
					render()
				})
			}

			logoutBtn := newAccountDialogLogoutButton(func() {
				am.Logout()
				licensesLoaded = false
				licensesCache = nil
				licensesErr = nil
				resettingSyncPassphrase = false
				render()
			})

			// footerLeft is nil whenever there's no "Forgot passphrase?" link
			// to show (no sync key yet, or a reset is in progress) --
			// container.NewCenter does NOT filter nil objects out of its own
			// Objects slice, so wrapping a nil footerLeft in NewCenter here
			// would panic the first time CenterLayout.Layout/MinSize calls a
			// method on that nil entry. Only wrap when there's something to
			// center; container.NewBorder's own "left" param already treats
			// nil as "no left edge" safely.
			var footerLeftCentered fyne.CanvasObject
			if footerLeft != nil {
				footerLeftCentered = container.NewCenter(footerLeft)
			}
			footerBar := container.NewBorder(nil, nil, footerLeftCentered, logoutBtn)
			footerArea := container.NewVBox(
				newAccountDivider(),
				view.NewInset(footerBar, 21, 21, 14, 18),
			)
			footerContainer.Objects = []fyne.CanvasObject{footerArea}

		default:
			intro := widget.NewLabel("Log in to see your USBridge licenses and sync your saved connections across devices.")
			intro.Wrapping = fyne.TextWrapWord
			body.Add(intro)
			if errMsg := am.LastError(); errMsg != "" {
				body.Add(widget.NewLabel(errMsg))
			}
			loginBtn := widget.NewButton("Log in with Google", func() {
				if err := am.StartLogin(); err == nil {
					render()
				}
			})
			loginBtn.Importance = widget.HighImportance
			body.Add(container.NewCenter(loginBtn))
		}

		body.Refresh()
		footerContainer.Refresh()
	}
	render()

	// Chrome restyled after the Add Connection dialog's own panel: title +
	// X header with the teal-to-lime top accent hairline, dark
	// design.ColorGray900 background, thin design.ColorBorder outline --
	// instead of this dialog's old plain dialog.NewCustom frame.
	var popup *widget.PopUp
	closeDialog := func() {
		if popup != nil {
			popup.Hide()
		}
	}

	title := view.NewBrandText("Account", 13, design.ColorTextLight, true)
	closeBtn := newAccountDialogIconButton(accountDialogCloseIcon, closeDialog)
	topAccent := newAccountDialogTopAccentBar()

	sep := canvas.NewRectangle(color.NRGBA{R: 0x30, G: 0x34, B: 0x2e, A: 0xff})
	sep.SetMinSize(fyne.NewSize(0, 1))

	// right=44 on the title's own inset (not closeBtn sharing this row)
	// reserves clearance so the title never runs under closeBtn, which sits
	// on its own layer closer to the panel's actual corner (see cornerBtn
	// below) -- same reasoning as the Add Connection dialog's own header.
	header := container.NewVBox(topAccent, view.NewInset(title, 21, 44, 9, 4), sep)

	scroll := container.NewVScroll(view.NewInset(body, 21, 21, 14, 18))
	// Explicit min size -- ScrollVerticalOnly's own MinSize otherwise stays
	// frozen at whatever was last set (0 here, never set), which would
	// collapse the dialog to the header's height alone on first layout.
	scroll.SetMinSize(fyne.NewSize(0, 195))

	bg := canvas.NewRectangle(design.ColorGray900)
	bg.CornerRadius = design.RadiusMD
	border := canvas.NewRectangle(color.Transparent)
	border.CornerRadius = design.RadiusMD
	border.StrokeColor = design.ColorBorder
	border.StrokeWidth = 1

	// closeBtn sits on its own layer, pinned close to the panel's actual
	// top-right corner rather than sharing header's own content margin --
	// same accountDialogCornerButtonLayout treatment (mirroring
	// controller.dialogCornerButtonLayout) the Add Connection dialog and QR
	// scanner popup's own header X use.
	cornerBtn := container.New(&accountDialogCornerButtonLayout{Top: 12, Right: 12}, closeBtn)

	panel := container.NewStack(
		bg,
		container.NewBorder(header, footerContainer, nil, nil, scroll),
		cornerBtn,
		border,
	)

	popup = view.ShowOverlayPopup(mw.window, view.OverlayPopupSpec{
		Panel:    panel,
		DimColor: color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x72},
		PanelSize: func(canvasSize fyne.Size, panel fyne.CanvasObject) fyne.Size {
			margin := clampFloat32(minFloat32(canvasSize.Width, canvasSize.Height)*0.04, 20, 32)
			maxWidth := canvasSize.Width - margin*2
			maxHeight := canvasSize.Height - margin*2
			if maxWidth <= 0 {
				maxWidth = canvasSize.Width
			}
			if maxHeight <= 0 {
				maxHeight = canvasSize.Height
			}

			panelMin := panel.MinSize()
			panelWidth := minFloat32(maxFloat32(panelMin.Width, 420), maxWidth)
			panelHeight := minFloat32(panelMin.Height, maxHeight)
			return fyne.NewSize(panelWidth, panelHeight)
		},
	})

	// Polls while the dialog is open (same 2s cadence the agent's own
	// license dialog uses) so a login completing in the browser is
	// reflected without needing to close and reopen this dialog -- but
	// only actually re-renders (rebuilding every widget, including
	// whatever Entry the human might be mid-typing into) when the
	// snapshot genuinely changed since the last tick. See
	// accountDialogSnapshot's own doc comment for why this matters.
	// Stops itself once popup is no longer visible (X button, tap-outside,
	// or however else it closed) rather than needing an explicit
	// close-hook -- widget.PopUp has no SetOnClosed equivalent.
	go func() {
		last := newAccountDialogSnapshot(am)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if popup == nil || !popup.Visible() {
				return
			}
			next := newAccountDialogSnapshot(am)
			if next == last {
				continue
			}
			last = next
			fyne.Do(render)
		}
	}()
}

// accountDialogCloseIcon is the same muted-olive X glyph the Add Connection
// dialog's own header close button uses (controller package's
// connectionDialogCancelIconRes) -- duplicated here (one line of SVG)
// rather than exported across the package boundary just for this.
var accountDialogCloseIcon = fyne.NewStaticResource("account_dialog_cancel.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#8f9381"><path d="M19 6.41 17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z"/></svg>`))

// accountDialogIconButton is a minimal transparent-until-hovered icon
// button -- originally a trimmed-down copy of the Add Connection dialog's
// own close button (controller.connectionDialogIconButton), generalized to
// take any icon resource/size (see the close button and the license row's
// copy button, both built on this).
type accountDialogIconButton struct {
	widget.BaseWidget

	resource fyne.Resource
	onTapped func()
	hovered  bool

	opaqueIcon bool
	iconSize   float32
	buttonSize float32

	bg   *canvas.Rectangle
	bdr  *canvas.Rectangle
	icon *canvas.Image
}

func newAccountDialogIconButton(resource fyne.Resource, onTapped func()) *accountDialogIconButton {
	b := &accountDialogIconButton{
		resource:   resource,
		onTapped:   onTapped,
		iconSize:   18,
		buttonSize: 28,
	}
	b.ExtendBaseWidget(b)
	return b
}

func (b *accountDialogIconButton) MinSize() fyne.Size {
	return fyne.NewSize(b.buttonSize, b.buttonSize)
}

func (b *accountDialogIconButton) CreateRenderer() fyne.WidgetRenderer {
	b.bg = canvas.NewRectangle(color.Transparent)
	b.bg.CornerRadius = 6

	b.bdr = canvas.NewRectangle(color.Transparent)
	b.bdr.CornerRadius = 6
	b.bdr.StrokeWidth = 1

	b.icon = canvas.NewImageFromResource(b.resource)
	b.icon.FillMode = canvas.ImageFillContain
	b.icon.ScaleMode = canvas.ImageScaleSmooth
	b.icon.SetMinSize(fyne.NewSize(b.iconSize, b.iconSize))
	if !b.opaqueIcon {
		b.icon.Translucency = 0.32
	}

	return widget.NewSimpleRenderer(container.NewStack(b.bg, b.bdr, container.NewCenter(b.icon)))
}

func (b *accountDialogIconButton) Tapped(*fyne.PointEvent) {
	if b.onTapped != nil {
		b.onTapped()
	}
}

func (b *accountDialogIconButton) TappedSecondary(*fyne.PointEvent) {}

func (b *accountDialogIconButton) Cursor() desktop.Cursor {
	return desktop.PointerCursor
}

func (b *accountDialogIconButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.refreshVisuals()
}

func (b *accountDialogIconButton) MouseMoved(*desktop.MouseEvent) {}

func (b *accountDialogIconButton) MouseOut() {
	b.hovered = false
	b.refreshVisuals()
}

func (b *accountDialogIconButton) refreshVisuals() {
	if b.bg == nil {
		return
	}
	if b.hovered {
		b.bg.FillColor = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x10}
		b.bdr.StrokeColor = color.NRGBA{R: 0x8f, G: 0x93, B: 0x81, A: 0xff}
		if !b.opaqueIcon {
			b.icon.Translucency = 0.08
		}
	} else {
		b.bg.FillColor = color.Transparent
		b.bdr.StrokeColor = color.Transparent
		if !b.opaqueIcon {
			b.icon.Translucency = 0.32
		}
	}
	b.bg.Refresh()
	b.bdr.Refresh()
	b.icon.Refresh()
}

// newAccountDialogTopAccentBar is the same thin teal-to-lime fade hairline
// the Add Connection dialog and QR scanner popups carry along their top
// edge -- duplicated here rather than exported across the package boundary
// (see controller.newConnectionDialogTopAccentBar, the original).
func newAccountDialogTopAccentBar() fyne.CanvasObject {
	teal := design.ColorConnectionBadgeText
	lime := design.ColorConnectionAddFill
	tealTransparent := color.NRGBA{R: 0x41, G: 0xe0, B: 0xc3, A: 0}
	limeTransparent := color.NRGBA{R: 0xc4, G: 0xe7, B: 0x7a, A: 0}
	leftFade := canvas.NewHorizontalGradient(tealTransparent, teal)
	leftFade.SetMinSize(fyne.NewSize(70, 2))
	rightFade := canvas.NewHorizontalGradient(lime, limeTransparent)
	rightFade.SetMinSize(fyne.NewSize(70, 2))
	mid := canvas.NewHorizontalGradient(teal, lime)
	return container.NewBorder(nil, nil, leftFade, rightFade, mid)
}

// accountDialogCornerButtonLayout pins its single child at a fixed offset
// from the panel's top-right corner, at the child's own natural size --
// mirrors controller.dialogCornerButtonLayout (used by the Add Connection
// dialog and QR scanner popups for the same "X sits in the corner, decoupled
// from the title's own margin" placement), duplicated here rather than
// exported across the package boundary for one small layout type.
type accountDialogCornerButtonLayout struct {
	Top   float32
	Right float32
}

func (l *accountDialogCornerButtonLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	btn := objects[0]
	btnSize := btn.MinSize()
	btn.Resize(btnSize)
	btn.Move(fyne.NewPos(size.Width-l.Right-btnSize.Width, l.Top))
}

func (l *accountDialogCornerButtonLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, 0)
}

func clampFloat32(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// accountLicensesList fetches the logged-in account's licenses exactly
// once per dialog open (guarded by *loaded) and renders from the cached
// result on every subsequent render() call -- render() itself only runs on
// a real state transition now (see accountDialogSnapshot), but this cache
// also means a manual re-render (e.g. after setting a sync passphrase)
// doesn't refire an unnecessary network call. *loaded/*cache/*cacheErr
// start out already seeded from AccountManager.CachedLicenses() (see
// showAccountDialog) whenever this isn't the first dialog open of the
// login session, so the network round-trip below -- and the placeholder
// row it shows while in flight -- only happens once per login, not once
// per dialog open.
func accountLicensesList(am *controller.AccountManager, loaded *bool, cache *[]account.License, cacheErr *error, window fyne.Window, render func()) fyne.CanvasObject {
	if *loaded {
		return renderLicenses(*cache, *cacheErr, window)
	}

	// Same shape (container.NewBorder + 24x24 right slot) a real license
	// row renders as, so its MinSize height already matches what's about to
	// replace it -- the license card doesn't change height once the fetch
	// below resolves and render() swaps this out.
	box := container.NewVBox(newAccountLicenseSkeletonRow())
	go func() {
		licenses, err := am.Licenses(context.Background())
		*cache = licenses
		*cacheErr = err
		*loaded = true
		fyne.Do(render)
	}()
	return box
}

func renderLicenses(licenses []account.License, err error, window fyne.Window) fyne.CanvasObject {
	box := container.NewVBox()
	switch {
	case err != nil:
		errText := canvas.NewText(fmt.Sprintf("Could not load licenses: %v", err), design.ColorAlert)
		errText.TextSize = 11
		box.Add(errText)
	case len(licenses) == 0:
		mutedNone := canvas.NewText("No licenses on this account yet.", design.ColorTextMuted)
		mutedNone.TextSize = 11
		box.Add(mutedNone)
	default:
		for i, lic := range licenses {
			if i > 0 {
				box.Add(newAccountDivider())
			}
			box.Add(newAccountLicenseRow(lic.Kind, lic.Identifier, lic.Status, window))
		}
	}
	return box
}

// accountSyncPassphraseSection lets the human set (or, on a second device,
// re-enter the same one they used on the first) the passphrase that
// derives this device's connections-sync encryption key -- see
// internal/syncconn's doc comment for why this is a SEPARATE secret from
// the Google login above, never sent to any server. Also covers the
// "I forgot my passphrase" recovery path, gated by *resetting -- see
// ResetSyncPassphrase's own doc comment for why that's a genuinely
// different operation from the normal set-passphrase one below (it
// deliberately overwrites the account's synced data instead of merging
// with it, since nothing can decrypt the old blob anymore once its
// passphrase is forgotten).
type tightVBoxLayout struct{}

func (t *tightVBoxLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var size fyne.Size
	for _, o := range objects {
		m := o.MinSize()
		if m.Width > size.Width {
			size.Width = m.Width
		}
		size.Height += m.Height
	}
	return size
}

func (t *tightVBoxLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for _, o := range objects {
		m := o.MinSize()
		o.Move(fyne.NewPos(0, y))
		o.Resize(fyne.NewSize(size.Width, m.Height))
		y += m.Height
	}
}

func accountSyncPassphraseSection(cm *controller.ConnectionManager, am *controller.AccountManager, resetting *bool, render func()) fyne.CanvasObject {
	titleText := canvas.NewText("Connections sync", design.ColorTextLight)
	titleText.TextSize = 12
	titleText.TextStyle = fyne.TextStyle{Bold: true}

	on := am.HasSyncKey() && !*resetting
	pill := newAccountStatusPill(map[bool]string{true: "on", false: "off"}[on], on)

	if on {
		desc := canvas.NewText("End-to-end encrypted sync of your saved connections across devices.", color.NRGBA{R: 0x8f, G: 0x93, B: 0x81, A: 0xff})
		desc.TextSize = 8
		textBlock := container.New(&tightVBoxLayout{}, titleText, desc)
		shiftedPill := view.NewInset(pill, 0, 0, 2, 0)
		return view.NewInset(container.NewBorder(nil, nil, nil, container.NewCenter(shiftedPill), textBlock), 0, 0, 4, 0)
	}

	titleRow := container.NewBorder(nil, nil, titleText, view.NewInset(pill, 0, 0, 2, 0))

	if *resetting {
		warn := widget.NewLabel(
			"Resetting starts fresh: this device's own saved connections will overwrite whatever is " +
				"currently synced on this account under the old passphrase -- that old synced data becomes " +
				"permanently unreadable the moment you do this. Enter a new passphrase:",
		)
		warn.Wrapping = fyne.TextWrapWord
		entry := widget.NewPasswordEntry()
		entry.SetPlaceHolder("New sync passphrase")
		statusLabel := widget.NewLabel("")

		resetBtn := widget.NewButton("Reset & overwrite", func() {
			if entry.Text == "" {
				return
			}
			statusLabel.SetText("Resetting…")
			go func() {
				err := cm.ResetSyncPassphrase(context.Background(), entry.Text)
				*resetting = false
				if err != nil {
					// The new key is already set locally either way (see
					// ResetSyncPassphrase) -- a failed overwrite here just
					// means try "Forgot passphrase?" again to retry the
					// push, not start over from scratch.
					fyne.Do(func() {
						statusLabel.SetText(fmt.Sprintf("Reset failed: %v", err))
						render()
					})
					return
				}
				fyne.Do(render)
			}()
		})
		resetBtn.Importance = widget.DangerImportance

		cancelBtn := widget.NewButton("Cancel", func() {
			*resetting = false
			render()
		})
		cancelBtn.Importance = widget.LowImportance

		return container.NewVBox(titleRow, warn, entry, statusLabel, container.NewHBox(resetBtn, cancelBtn))
	}

	label := widget.NewLabel("Set a sync passphrase to sync your saved connections across devices (never sent to our servers):")
	label.Wrapping = fyne.TextWrapWord
	entry := widget.NewPasswordEntry()
	entry.SetPlaceHolder("Sync passphrase")
	saveBtn := widget.NewButton("Set passphrase", func() {
		if entry.Text == "" {
			return
		}
		am.SetSyncPassphrase(entry.Text)
		render()
	})
	return container.NewVBox(titleRow, label, entry, container.NewCenter(saveBtn))
}
