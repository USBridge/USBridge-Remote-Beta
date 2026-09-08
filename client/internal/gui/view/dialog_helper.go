// Package view contains Fyne components and UI helpers.
package view

import (
	"image/color"
	"strings"
	"sync"

	"usbridge-client/internal/gui/assets"
	"usbridge-client/internal/gui/design"
	"usbridge-client/internal/gui/i18n"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// minWidthLayout sets a minimum content width (for mobile in portrait orientation)
type minWidthLayout struct {
	minWidth float32
}

func (m *minWidthLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objects {
		o.Resize(size)
		o.Move(fyne.NewPos(0, 0))
	}
}

func (m *minWidthLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	min := fyne.NewSize(0, 0)
	for _, o := range objects {
		childMin := o.MinSize()
		if childMin.Width > min.Width {
			min.Width = childMin.Width
		}
		if childMin.Height > min.Height {
			min.Height = childMin.Height
		}
	}
	if m.minWidth > 0 && min.Width < m.minWidth {
		min.Width = m.minWidth
	}
	return min
}

type confirmDialogTitleLayout struct{}

type confirmDialogButtonsLayout struct {
	gap float32
}

func (l *confirmDialogTitleLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
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

func (l *confirmDialogTitleLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) < 2 {
		return fyne.NewSize(0, 0)
	}

	titleMin := objects[0].MinSize()
	closeMin := objects[1].MinSize()
	width := maxFloat32(titleMin.Width, closeMin.Width*2) + closeMin.Width
	height := maxFloat32(titleMin.Height, closeMin.Height)
	return fyne.NewSize(width, height)
}

func (l *confirmDialogButtonsLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 2 {
		return
	}

	left := objects[0]
	right := objects[1]
	leftWidth := (size.Width - l.gap) / 2
	if leftWidth < 0 {
		leftWidth = 0
	}
	rightWidth := size.Width - leftWidth - l.gap
	if rightWidth < 0 {
		rightWidth = 0
	}

	left.Move(fyne.NewPos(0, 0))
	left.Resize(fyne.NewSize(leftWidth, size.Height))

	right.Move(fyne.NewPos(leftWidth+l.gap, 0))
	right.Resize(fyne.NewSize(rightWidth, size.Height))
}

func (l *confirmDialogButtonsLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) < 2 {
		return fyne.NewSize(0, 0)
	}

	leftMin := objects[0].MinSize()
	rightMin := objects[1].MinSize()
	height := maxFloat32(leftMin.Height, rightMin.Height)
	return fyne.NewSize(leftMin.Width+rightMin.Width+l.gap, height)
}

func newConfirmDialogCloseButton(onTapped func()) *widget.Button {
	btn := widget.NewButtonWithIcon("", theme.CancelIcon(), onTapped)
	btn.Importance = widget.LowImportance
	return btn
}

func showConfirmDialog(title, message string, callback func(bool), parent fyne.Window, danger bool) {
	label := widget.NewLabel(message)
	label.Wrapping = fyne.TextWrapWord

	var popup *widget.PopUp
	var once sync.Once
	invokeCallback := func(ok bool) {
		once.Do(func() {
			if callback != nil {
				callback(ok)
			}
		})
	}
	closePopup := func(ok bool) {
		invokeCallback(ok)
		if popup != nil {
			popup.Hide()
		}
	}

	titleText := NewBrandText(title, 19, design.ColorTextLight, true)
	titleText.Alignment = fyne.TextAlignCenter

	closeBtn := newConfirmDialogCloseButton(func() {
		closePopup(false)
	})
	titleBar := container.New(&confirmDialogTitleLayout{}, titleText, closeBtn)

	yesBtn := widget.NewButton(i18n.Current.Yes, func() {
		closePopup(true)
	})
	if danger {
		yesBtn.Importance = widget.DangerImportance
	} else {
		yesBtn.Importance = widget.HighImportance
	}

	noBtn := widget.NewButton(i18n.Current.No, func() {
		closePopup(false)
	})

	buttons := container.New(&confirmDialogButtonsLayout{gap: 12}, noBtn, yesBtn)
	body := container.NewVBox(
		titleBar,
		NewInset(label, 0, 0, 16, 14),
		buttons,
	)

	panelContent := body
	if parent != nil {
		var minW float32 = 408
		canvasSize := parent.Canvas().Size()
		if UseCompactLayout(canvasSize.Width) {
			minW = canvasSize.Width * 0.85
			if minW < 280 {
				minW = 280
			}
		}
		panelContent = container.New(&minWidthLayout{minWidth: minW}, body)
	}

	bg := canvas.NewRectangle(design.ColorGray900)
	bg.CornerRadius = design.RadiusMD

	border := canvas.NewRectangle(color.Transparent)
	border.CornerRadius = design.RadiusMD
	border.StrokeColor = design.ColorBorder
	border.StrokeWidth = 1

	panel := container.NewStack(
		bg,
		NewInset(panelContent, 18, 18, 16, 16),
		border,
	)

	popup = ShowOverlayPopup(parent, OverlayPopupSpec{
		Panel:    panel,
		DimColor: color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x72},
		PanelSize: func(canvasSize fyne.Size, panel fyne.CanvasObject) fyne.Size {
			margin := clampFloat32(minFloat32(canvasSize.Width, canvasSize.Height)*0.04, 20, 28)
			maxWidth := canvasSize.Width - margin*2
			maxHeight := canvasSize.Height - margin*2
			if maxWidth <= 0 {
				maxWidth = canvasSize.Width
			}
			if maxHeight <= 0 {
				maxHeight = canvasSize.Height
			}

			panelMin := panel.MinSize()
			panelWidth := minFloat32(maxFloat32(panelMin.Width, 408), maxWidth)
			panelHeight := minFloat32(panelMin.Height, maxHeight)
			return fyne.NewSize(panelWidth, panelHeight)
		},
	})
}

// ShowConfirmYesLeft shows a confirmation dialog with the Yes button on the left, No on the right.
// Uses the localized strings i18n.Current.Yes and i18n.Current.No.
// On mobile in portrait orientation the dialog is wider (like edit connection).
func ShowConfirmYesLeft(title, message string, callback func(bool), parent fyne.Window) {
	showConfirmDialog(title, message, callback, parent, false)
}

// ShowConfirmYesLeftDanger — same as ShowConfirmYesLeft, but the "Yes" button is red (DangerImportance).
// Used for confirming power and reboot actions.
func ShowConfirmYesLeftDanger(title, message string, callback func(bool), parent fyne.Window) {
	showConfirmDialog(title, message, callback, parent, true)
}

// confirmToastButtonsLayout packs two buttons at their own natural content
// width side by side, right-aligned within whatever width it's given --
// unlike confirmDialogButtonsLayout (the full confirm dialogs' half-width
// split), a toast's Нет/Да pair reads better as small pills than as two
// equal-width blocks.
type confirmToastButtonsLayout struct {
	gap float32
}

func (l *confirmToastButtonsLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 2 {
		return
	}

	left := objects[0]
	right := objects[1]
	leftMin := left.MinSize()
	rightMin := right.MinSize()

	x := size.Width - rightMin.Width
	right.Move(fyne.NewPos(x, (size.Height-rightMin.Height)/2))
	right.Resize(rightMin)

	x -= l.gap + leftMin.Width
	left.Move(fyne.NewPos(x, (size.Height-leftMin.Height)/2))
	left.Resize(leftMin)
}

func (l *confirmToastButtonsLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) < 2 {
		return fyne.NewSize(0, 0)
	}

	leftMin := objects[0].MinSize()
	rightMin := objects[1].MinSize()
	return fyne.NewSize(leftMin.Width+rightMin.Width+l.gap, maxFloat32(leftMin.Height, rightMin.Height))
}

// confirmToastButton is a small pill used only by ShowConfirmToast's mini
// bottom toast -- deliberately simpler than the app's other custom buttons
// (no icon, no disabled state): just a filled-or-outlined label that swaps
// color on hover.
type confirmToastButton struct {
	widget.BaseWidget

	labelText      string
	onTapped       func()
	hovered        bool
	fillColor      color.Color
	borderColor    color.Color
	textColor      color.Color
	hoverFillColor color.Color
	hoverTextColor color.Color

	bg     *canvas.Rectangle
	border *canvas.Rectangle
	label  *canvas.Text
}

func newConfirmToastButton(label string, fillColor, borderColor, textColor, hoverFillColor, hoverTextColor color.Color, onTapped func()) *confirmToastButton {
	btn := &confirmToastButton{
		labelText:      label,
		onTapped:       onTapped,
		fillColor:      fillColor,
		borderColor:    borderColor,
		textColor:      textColor,
		hoverFillColor: hoverFillColor,
		hoverTextColor: hoverTextColor,
	}
	btn.ExtendBaseWidget(btn)
	return btn
}

func (b *confirmToastButton) CreateRenderer() fyne.WidgetRenderer {
	b.bg = canvas.NewRectangle(b.fillColor)
	b.bg.CornerRadius = design.RadiusMD

	b.border = canvas.NewRectangle(color.Transparent)
	b.border.CornerRadius = design.RadiusMD
	if b.borderColor != nil && b.borderColor != color.Transparent {
		b.border.StrokeColor = b.borderColor
		b.border.StrokeWidth = 1
	}

	b.label = canvas.NewText(b.labelText, b.textColor)
	b.label.TextSize = 13
	b.label.TextStyle.Bold = true
	b.label.Alignment = fyne.TextAlignCenter

	content := container.NewStack(b.bg, b.border, NewInset(container.NewCenter(b.label), 16, 16, 8, 8))
	return widget.NewSimpleRenderer(content)
}

func (b *confirmToastButton) Tapped(*fyne.PointEvent) {
	if b.onTapped != nil {
		b.onTapped()
	}
}

func (b *confirmToastButton) TappedSecondary(*fyne.PointEvent) {}

func (b *confirmToastButton) Cursor() desktop.Cursor {
	return desktop.PointerCursor
}

func (b *confirmToastButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.refreshVisuals()
}

func (b *confirmToastButton) MouseMoved(*desktop.MouseEvent) {}

func (b *confirmToastButton) MouseOut() {
	b.hovered = false
	b.refreshVisuals()
}

func (b *confirmToastButton) refreshVisuals() {
	if b.bg == nil {
		return
	}

	fill := b.fillColor
	text := b.textColor
	if b.hovered {
		if b.hoverFillColor != nil {
			fill = b.hoverFillColor
		}
		if b.hoverTextColor != nil {
			text = b.hoverTextColor
		}
	}
	b.bg.FillColor = fill
	b.label.Color = text
	b.bg.Refresh()
	b.label.Refresh()
}

// ShowConfirmToast shows a small, non-dimming yes/no prompt anchored near
// the bottom-center of the window -- a single line of text plus a neutral
// "No" pill and a teal "Yes" pill, no title bar and no backdrop dimming.
// Used for lighter-weight confirmations (e.g. deleting a connection) where
// the full modal ShowConfirmYesLeft dialog reads as too heavy.
func ShowConfirmToast(message string, callback func(bool), parent fyne.Window) {
	if parent == nil {
		return
	}

	var popup *widget.PopUp
	var once sync.Once
	invokeCallback := func(ok bool) {
		once.Do(func() {
			if callback != nil {
				callback(ok)
			}
		})
	}
	closePopup := func(ok bool) {
		invokeCallback(ok)
		if popup != nil {
			popup.Hide()
		}
	}

	text := canvas.NewText(message, design.ColorTextLight)
	text.TextSize = 13

	noBtn := newConfirmToastButton(i18n.Current.No,
		color.Transparent, design.ColorBorder, design.ColorTextLight,
		design.ColorSurfaceLight, design.ColorTextLight,
		func() { closePopup(false) })
	yesBtn := newConfirmToastButton(i18n.Current.Yes,
		design.ColorConnectionBadgeText, color.Transparent, design.ColorGray950,
		color.NRGBA{R: 0x61, G: 0xf0, B: 0xd3, A: 0xff}, design.ColorGray950,
		func() { closePopup(true) })

	buttons := container.New(&confirmToastButtonsLayout{gap: 8}, noBtn, yesBtn)
	body := container.NewBorder(nil, nil, nil, buttons, NewInset(text, 0, 14, 0, 0))

	bg := canvas.NewRectangle(design.ColorGray900)
	bg.CornerRadius = design.RadiusMD

	border := canvas.NewRectangle(color.Transparent)
	border.CornerRadius = design.RadiusMD
	border.StrokeColor = design.ColorBorder
	border.StrokeWidth = 1

	panel := container.NewStack(
		bg,
		NewInset(body, 16, 16, 12, 12),
		border,
	)

	popup = ShowOverlayPopup(parent, OverlayPopupSpec{
		Panel:    panel,
		DimColor: color.Transparent,
		PanelSize: func(canvasSize fyne.Size, panel fyne.CanvasObject) fyne.Size {
			margin := clampFloat32(minFloat32(canvasSize.Width, canvasSize.Height)*0.04, 20, 28)
			maxWidth := canvasSize.Width - margin*2
			if maxWidth <= 0 {
				maxWidth = canvasSize.Width
			}

			panelMin := panel.MinSize()
			panelWidth := minFloat32(panelMin.Width, maxWidth)
			return fyne.NewSize(panelWidth, panelMin.Height)
		},
		PanelPos: func(canvasSize fyne.Size, panelSize fyne.Size) fyne.Position {
			bottomMargin := clampFloat32(canvasSize.Height*0.07, 40, 64)
			return fyne.NewPos((canvasSize.Width-panelSize.Width)/2, canvasSize.Height-panelSize.Height-bottomMargin)
		},
	})
}

func ShowErrorDialog(err error, parent fyne.Window) {
	if err == nil {
		return
	}

	message := err.Error()

	var popup *widget.PopUp
	closePopup := func() {
		if popup != nil {
			popup.Hide()
		}
	}

	titleText := NewBrandText(i18n.Current.Error, 19, design.ColorTextLight, true)
	titleText.Alignment = fyne.TextAlignCenter

	closeBtn := newConfirmDialogCloseButton(closePopup)
	titleBar := container.New(&confirmDialogTitleLayout{}, titleText, closeBtn)

	label := widget.NewLabel(message)
	label.Wrapping = fyne.TextWrapWord

	okBtn := widget.NewButton(i18n.Current.OK, closePopup)
	copyBtn := widget.NewButton(i18n.Current.Copy, func() {
		if parent != nil && parent.Clipboard() != nil {
			parent.Clipboard().SetContent(message)
		}
	})

	buttons := container.New(&confirmDialogButtonsLayout{gap: 12}, copyBtn, okBtn)
	body := container.NewVBox(
		titleBar,
		NewInset(label, 0, 0, 16, 14),
		buttons,
	)

	panelContent := fyne.CanvasObject(body)
	if parent != nil {
		var minW float32 = 408
		canvasSize := parent.Canvas().Size()
		if UseCompactLayout(canvasSize.Width) {
			minW = canvasSize.Width * 0.85
			if minW < 280 {
				minW = 280
			}
		}
		panelContent = container.New(&minWidthLayout{minWidth: minW}, body)
	}

	bg := canvas.NewRectangle(design.ColorGray900)
	bg.CornerRadius = design.RadiusMD

	border := canvas.NewRectangle(color.Transparent)
	border.CornerRadius = design.RadiusMD
	border.StrokeColor = design.ColorBorder
	border.StrokeWidth = 1

	panel := container.NewStack(
		bg,
		NewInset(panelContent, 18, 18, 16, 16),
		border,
	)

	popup = ShowOverlayPopup(parent, OverlayPopupSpec{
		Panel:    panel,
		DimColor: color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x72},
		PanelSize: func(canvasSize fyne.Size, panel fyne.CanvasObject) fyne.Size {
			margin := clampFloat32(minFloat32(canvasSize.Width, canvasSize.Height)*0.04, 20, 28)
			maxWidth := canvasSize.Width - margin*2
			maxHeight := canvasSize.Height - margin*2
			if maxWidth <= 0 {
				maxWidth = canvasSize.Width
			}
			if maxHeight <= 0 {
				maxHeight = canvasSize.Height
			}

			panelMin := panel.MinSize()
			panelWidth := minFloat32(maxFloat32(panelMin.Width, 408), maxWidth)
			panelHeight := minFloat32(panelMin.Height, maxHeight)
			return fyne.NewSize(panelWidth, panelHeight)
		},
	})
}

func ShowInfoDialog(title, message string, parent fyne.Window) {
	if strings.TrimSpace(message) == "" {
		return
	}

	var popup *widget.PopUp
	closePopup := func() {
		if popup != nil {
			popup.Hide()
		}
	}

	titleText := NewBrandText(title, 19, design.ColorTextLight, true)
	titleText.Alignment = fyne.TextAlignCenter

	closeBtn := newConfirmDialogCloseButton(closePopup)
	titleBar := container.New(&confirmDialogTitleLayout{}, titleText, closeBtn)

	label := widget.NewLabel(message)
	label.Wrapping = fyne.TextWrapWord

	okBtn := widget.NewButton(i18n.Current.OK, closePopup)
	okBtn.Importance = widget.MediumImportance
	buttons := container.NewCenter(container.NewGridWrap(fyne.NewSize(260, okBtn.MinSize().Height), okBtn))
	body := container.NewVBox(
		titleBar,
		NewInset(label, 0, 0, 16, 14),
		buttons,
	)

	panelContent := fyne.CanvasObject(body)
	if parent != nil {
		var minW float32 = 408
		canvasSize := parent.Canvas().Size()
		if UseCompactLayout(canvasSize.Width) {
			minW = canvasSize.Width * 0.85
			if minW < 280 {
				minW = 280
			}
		}
		panelContent = container.New(&minWidthLayout{minWidth: minW}, body)
	}

	bg := canvas.NewRectangle(design.ColorGray900)
	bg.CornerRadius = design.RadiusMD

	border := canvas.NewRectangle(color.Transparent)
	border.CornerRadius = design.RadiusMD
	border.StrokeColor = design.ColorBorder
	border.StrokeWidth = 1

	panel := container.NewStack(
		bg,
		NewInset(panelContent, 18, 18, 16, 16),
		border,
	)

	popup = ShowOverlayPopup(parent, OverlayPopupSpec{
		Panel:    panel,
		DimColor: color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x72},
		PanelSize: func(canvasSize fyne.Size, panel fyne.CanvasObject) fyne.Size {
			margin := clampFloat32(minFloat32(canvasSize.Width, canvasSize.Height)*0.04, 20, 28)
			maxWidth := canvasSize.Width - margin*2
			maxHeight := canvasSize.Height - margin*2
			if maxWidth <= 0 {
				maxWidth = canvasSize.Width
			}
			if maxHeight <= 0 {
				maxHeight = canvasSize.Height
			}

			panelMin := panel.MinSize()
			panelWidth := minFloat32(maxFloat32(panelMin.Width, 408), maxWidth)
			panelHeight := minFloat32(panelMin.Height, maxHeight)
			return fyne.NewSize(panelWidth, panelHeight)
		},
	})
}

func ShowBusyDialog(title, message string, parent fyne.Window) *widget.PopUp {
	if parent == nil {
		return nil
	}

	titleText := NewBrandText(title, 19, design.ColorTextLight, true)
	titleText.Alignment = fyne.TextAlignCenter

	label := widget.NewLabel(message)
	label.Alignment = fyne.TextAlignCenter
	label.Wrapping = fyne.TextWrapWord

	body := container.NewVBox(
		titleText,
		NewInset(label, 0, 0, 14, 0),
	)

	panelContent := fyne.CanvasObject(body)
	var minW float32 = 408
	canvasSize := parent.Canvas().Size()
	if UseCompactLayout(canvasSize.Width) {
		minW = canvasSize.Width * 0.85
		if minW < 280 {
			minW = 280
		}
	}
	panelContent = container.New(&minWidthLayout{minWidth: minW}, body)

	bg := canvas.NewRectangle(design.ColorGray900)
	bg.CornerRadius = design.RadiusMD

	border := canvas.NewRectangle(color.Transparent)
	border.CornerRadius = design.RadiusMD
	border.StrokeColor = design.ColorBorder
	border.StrokeWidth = 1

	panel := container.NewStack(
		bg,
		NewInset(panelContent, 18, 18, 16, 16),
		border,
	)

	return ShowOverlayPopup(parent, OverlayPopupSpec{
		Panel:    panel,
		DimColor: color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x72},
		PanelSize: func(canvasSize fyne.Size, panel fyne.CanvasObject) fyne.Size {
			margin := clampFloat32(minFloat32(canvasSize.Width, canvasSize.Height)*0.04, 20, 28)
			maxWidth := canvasSize.Width - margin*2
			panelMin := panel.MinSize()
			panelWidth := minFloat32(maxFloat32(panelMin.Width, 408), maxWidth)
			return fyne.NewSize(panelWidth, panelMin.Height)
		},
	})
}

func ShowDeleteImageConfirm(fileName string, safeRemove bool, callback func(bool), parent fyne.Window) {
	var popup *widget.PopUp
	var once sync.Once
	invokeCallback := func(ok bool) {
		once.Do(func() {
			if callback != nil {
				callback(ok)
			}
		})
	}
	closePopup := func(ok bool) {
		invokeCallback(ok)
		if popup != nil {
			popup.Hide()
		}
	}

	titleText := NewBrandText("Remove from list?", 19, design.ColorTextLight, true)
	if !safeRemove {
		titleText = NewBrandText("Delete image?", 19, design.ColorTextLight, true)
	}
	closeBtn := newConfirmDialogCloseButton(func() {
		closePopup(false)
	})
	titleBar := container.New(&confirmDialogTitleLayout{}, titleText, closeBtn)

	leadText := widget.NewLabel("Are you sure you want to remove:")
	if !safeRemove {
		leadText.SetText("Are you sure you want to delete:")
	}
	leadText.Wrapping = fyne.TextWrapWord

	fileLabel := widget.NewLabel(fileName + "?")
	fileLabel.Wrapping = fyne.TextWrapWord
	fileLabel.TextStyle = fyne.TextStyle{Bold: true}

	var noteRow fyne.CanvasObject
	if safeRemove {
		checkIcon := widget.NewIcon(theme.ConfirmIcon())
		noteText := canvas.NewText("Safe: the actual file on disk will not be deleted.", design.ColorTextMuted)
		noteText.TextSize = 13
		noteText.TextStyle = fyne.TextStyle{Italic: true}
		noteRow = container.NewHBox(checkIcon, NewInset(noteText, 6, 0, 0, 0))
	} else {
		warnIcon := canvas.NewImageFromResource(assets.WarningTriangleIcon)
		warnIcon.FillMode = canvas.ImageFillContain
		warnIcon.SetMinSize(fyne.NewSize(18, 18))
		warnText := canvas.NewText("This action cannot be undone.", design.ColorTextLight)
		warnText.TextSize = 13
		warnText.TextStyle = fyne.TextStyle{Italic: true}
		noteRow = container.NewHBox(warnIcon, NewInset(warnText, 8, 0, 0, 0))
	}

	cancelBtn := widget.NewButton(i18n.Current.Cancel, func() {
		closePopup(false)
	})

	confirmLabel := "Delete"
	if safeRemove {
		confirmLabel = "Remove"
	}
	deleteBtn := widget.NewButton(confirmLabel, func() {
		closePopup(true)
	})
	if safeRemove {
		deleteBtn.Importance = widget.MediumImportance
	} else {
		deleteBtn.Importance = widget.DangerImportance
	}

	buttons := container.New(&confirmDialogButtonsLayout{gap: 12}, cancelBtn, deleteBtn)
	body := container.NewVBox(
		titleBar,
		NewInset(leadText, 0, 0, 14, 0),
		NewInset(fileLabel, 0, 0, 0, 16),
		NewInset(noteRow, 4, 0, 0, 18),
		buttons,
	)

	panelContent := body
	if parent != nil {
		var minW float32 = 408
		canvasSize := parent.Canvas().Size()
		if UseCompactLayout(canvasSize.Width) {
			minW = canvasSize.Width * 0.85
			if minW < 280 {
				minW = 280
			}
		}
		panelContent = container.New(&minWidthLayout{minWidth: minW}, body)
	}

	bg := canvas.NewRectangle(design.ColorGray900)
	bg.CornerRadius = design.RadiusMD

	border := canvas.NewRectangle(color.Transparent)
	border.CornerRadius = design.RadiusMD
	border.StrokeColor = design.ColorBorder
	border.StrokeWidth = 1

	panel := container.NewStack(
		bg,
		NewInset(panelContent, 18, 18, 16, 16),
		border,
	)

	popup = ShowOverlayPopup(parent, OverlayPopupSpec{
		Panel:    panel,
		DimColor: color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x72},
		PanelSize: func(canvasSize fyne.Size, panel fyne.CanvasObject) fyne.Size {
			margin := clampFloat32(minFloat32(canvasSize.Width, canvasSize.Height)*0.04, 20, 28)
			maxWidth := canvasSize.Width - margin*2
			maxHeight := canvasSize.Height - margin*2
			if maxWidth <= 0 {
				maxWidth = canvasSize.Width
			}
			if maxHeight <= 0 {
				maxHeight = canvasSize.Height
			}

			panelMin := panel.MinSize()
			panelWidth := minFloat32(maxFloat32(panelMin.Width, 408), maxWidth)
			panelHeight := minFloat32(panelMin.Height, maxHeight)
			return fyne.NewSize(panelWidth, panelHeight)
		},
	})
}

func ShowUploadImageConfirm(fileName string, callback func(bool), parent fyne.Window) {
	var popup *widget.PopUp
	var once sync.Once
	invokeCallback := func(ok bool) {
		once.Do(func() {
			if callback != nil {
				callback(ok)
			}
		})
	}
	closePopup := func(ok bool) {
		invokeCallback(ok)
		if popup != nil {
			popup.Hide()
		}
	}

	titleText := NewBrandText("Upload image?", 19, design.ColorTextLight, true)
	closeBtn := newConfirmDialogCloseButton(func() {
		closePopup(false)
	})
	titleBar := container.New(&confirmDialogTitleLayout{}, titleText, closeBtn)

	leadText := widget.NewLabel("Do you want to upload this file to the device?")
	leadText.Wrapping = fyne.TextWrapWord

	fileLabel := widget.NewLabel(fileName)
	fileLabel.Wrapping = fyne.TextWrapWord
	fileLabel.TextStyle = fyne.TextStyle{Bold: true}

	infoIcon := canvas.NewImageFromResource(assets.WarningInfoIcon)
	infoIcon.FillMode = canvas.ImageFillContain
	infoIcon.SetMinSize(fyne.NewSize(18, 18))

	infoText := canvas.NewText("This process may take some time.", design.ColorTextLight)
	infoText.TextSize = 13
	infoText.TextStyle = fyne.TextStyle{Italic: true}

	infoRow := container.NewHBox(
		infoIcon,
		NewInset(infoText, 8, 0, 0, 0),
	)

	cancelBtn := widget.NewButton(i18n.Current.Cancel, func() {
		closePopup(false)
	})

	uploadBtn := widget.NewButton("Upload", func() {
		closePopup(true)
	})
	uploadBtn.Importance = widget.HighImportance

	buttons := container.New(&confirmDialogButtonsLayout{gap: 12}, cancelBtn, uploadBtn)
	body := container.NewVBox(
		titleBar,
		NewInset(leadText, 0, 0, 14, 0),
		NewInset(fileLabel, 0, 0, 0, 16),
		NewInset(infoRow, 4, 0, 0, 18),
		buttons,
	)

	panelContent := body
	if parent != nil {
		var minW float32 = 408
		canvasSize := parent.Canvas().Size()
		if UseCompactLayout(canvasSize.Width) {
			minW = canvasSize.Width * 0.85
			if minW < 280 {
				minW = 280
			}
		}
		panelContent = container.New(&minWidthLayout{minWidth: minW}, body)
	}

	bg := canvas.NewRectangle(design.ColorGray900)
	bg.CornerRadius = design.RadiusMD

	border := canvas.NewRectangle(color.Transparent)
	border.CornerRadius = design.RadiusMD
	border.StrokeColor = design.ColorBorder
	border.StrokeWidth = 1

	panel := container.NewStack(
		bg,
		NewInset(panelContent, 18, 18, 16, 16),
		border,
	)

	popup = ShowOverlayPopup(parent, OverlayPopupSpec{
		Panel:    panel,
		DimColor: color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x72},
		PanelSize: func(canvasSize fyne.Size, panel fyne.CanvasObject) fyne.Size {
			margin := clampFloat32(minFloat32(canvasSize.Width, canvasSize.Height)*0.04, 20, 28)
			maxWidth := canvasSize.Width - margin*2
			maxHeight := canvasSize.Height - margin*2
			if maxWidth <= 0 {
				maxWidth = canvasSize.Width
			}
			if maxHeight <= 0 {
				maxHeight = canvasSize.Height
			}

			panelMin := panel.MinSize()
			panelWidth := minFloat32(maxFloat32(panelMin.Width, 408), maxWidth)
			panelHeight := minFloat32(panelMin.Height, maxHeight)
			return fyne.NewSize(panelWidth, panelHeight)
		},
	})
}
