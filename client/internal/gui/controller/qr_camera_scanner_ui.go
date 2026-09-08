//go:build !android && !ios
// +build !android,!ios

package controller

import (
	"image"
	"image/color"

	"usbridge-client/internal/gui/design"
	"usbridge-client/internal/gui/view"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/makiuchi-d/gozxing"
)

// Shared popup UI for the desktop QR camera scanner. Used by the V4L2-based
// capture (Linux), AVFoundation-based capture (macOS), and the Media
// Foundation-based capture (Windows) — the layout has no platform-specific
// behavior.
//
// Restyled to match the Add Connection dialog's own panel chrome (header
// layout, X button, top accent hairline, background) instead of its own
// separate look -- same header/close-button/accent-bar building blocks
// (newConnectionDialogTopAccentBar, connectionDialogCancelIconRes,
// newConnectionDialogIconButton, dialogCornerButtonLayout,
// tightHeaderVBoxLayout) connection_manager_dialogs.go already built for
// that dialog, reused here rather than re-invented. Panel sized ~20%
// smaller than before (see qrScannerCardLayout.MinSize/qrScannerPanelSize).

func showEmbeddedQRScannerPopup(parent fyne.Window, videoImg *canvas.Image, onClose func()) *widget.PopUp {
	title := view.NewBrandText("Scan device qr code", 13, design.ColorTextLight, true)

	closeBtn := newConnectionDialogIconButton(connectionDialogCancelIconRes, onClose)

	topAccent := newConnectionDialogTopAccentBar()

	sep := canvas.NewRectangle(color.NRGBA{R: 0x30, G: 0x34, B: 0x2e, A: 0xff})
	sep.SetMinSize(fyne.NewSize(0, 1))

	// top=21,44,10,9 -- 44px right clearance keeps the title from running
	// under closeBtn, which (see cornerBtn below) sits closer to the
	// panel's actual corner than this content margin reaches, same as the
	// Add Connection dialog's own header.
	header := container.New(&tightHeaderVBoxLayout{Gap: 0}, topAccent, view.NewInset(title, 21, 44, 10, 9), sep)

	videoBg := canvas.NewRectangle(color.NRGBA{R: 0x08, G: 0x08, B: 0x08, A: 0xf2})
	videoBg.CornerRadius = 14

	videoBorder := canvas.NewRectangle(color.Transparent)
	videoBorder.CornerRadius = 14
	videoBorder.StrokeColor = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x18}
	videoBorder.StrokeWidth = 1

	videoCard := container.NewStack(
		videoBg,
		view.NewInset(container.NewMax(videoImg), 14, 14, 14, 14),
		videoBorder,
	)

	// Same panel background/border as the Add Connection dialog
	// (design.ColorGray900/ColorBorder/RadiusMD) instead of this popup's
	// previous translucent dark-gray/white-alpha look.
	cardBg := canvas.NewRectangle(design.ColorGray900)
	cardBg.CornerRadius = design.RadiusMD
	cardBorder := canvas.NewRectangle(color.Transparent)
	cardBorder.CornerRadius = design.RadiusMD
	cardBorder.StrokeColor = design.ColorBorder
	cardBorder.StrokeWidth = 1

	// closeBtn sits on its own layer, pinned close to the panel's actual
	// top-right corner rather than sharing header's own content margin --
	// same dialogCornerButtonLayout the Add Connection dialog's header X
	// uses, for the same reason (decoupled from the title's own vertical
	// rhythm, reads as a corner control).
	cornerBtn := container.New(&dialogCornerButtonLayout{Top: 12, Right: 12}, closeBtn)

	card := container.NewStack(
		cardBg,
		container.New(&qrScannerCardLayout{}, header, videoCard),
		cornerBtn,
		cardBorder,
	)

	return view.NewOverlayPopup(parent, view.OverlayPopupSpec{
		Panel:    card,
		DimColor: color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x72},
		PanelSize: func(canvasSize fyne.Size, _ fyne.CanvasObject) fyne.Size {
			return qrScannerPanelSize(canvasSize)
		},
	})
}

type qrScannerCardLayout struct{}

func (l *qrScannerCardLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 2 {
		return
	}

	header := objects[0]
	video := objects[1]

	padding := clampFloat32(minFloat32(size.Width, size.Height)*0.04, 14, 21)
	// header's own real height, not a hardcoded guess -- its content
	// (accent bar + title + divider) is this restyled header's own doing,
	// not qrScannerCardLayout's business to know the exact pixel sum of.
	headerHeight := header.MinSize().Height
	headerWidth := size.Width - padding*2
	if headerWidth < 0 {
		headerWidth = 0
	}

	header.Move(fyne.NewPos(padding, 0))
	header.Resize(fyne.NewSize(headerWidth, headerHeight))

	videoTop := headerHeight + 12
	availableWidth := size.Width - padding*2
	availableHeight := size.Height - videoTop - padding
	if availableWidth < 0 {
		availableWidth = 0
	}
	if availableHeight < 0 {
		availableHeight = 0
	}

	videoWidth := availableWidth
	videoHeight := videoWidth * 3 / 4
	if videoHeight > availableHeight {
		videoHeight = availableHeight
		videoWidth = videoHeight * 4 / 3
	}
	if videoWidth > availableWidth {
		videoWidth = availableWidth
		videoHeight = videoWidth * 3 / 4
	}
	if videoWidth < 0 {
		videoWidth = 0
	}
	if videoHeight < 0 {
		videoHeight = 0
	}

	video.Move(fyne.NewPos((size.Width-videoWidth)/2, videoTop+(availableHeight-videoHeight)/2))
	video.Resize(fyne.NewSize(videoWidth, videoHeight))
}

func (l *qrScannerCardLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	// ~20% smaller than this popup's previous 320x280 floor.
	return fyne.NewSize(256, 224)
}

func qrScannerPanelSize(canvasSize fyne.Size) fyne.Size {
	margin := clampFloat32(minFloat32(canvasSize.Width, canvasSize.Height)*0.04, 20, 32)
	maxWidth := canvasSize.Width - margin*2
	maxHeight := canvasSize.Height - margin*2
	if maxWidth <= 0 {
		maxWidth = canvasSize.Width
	}
	if maxHeight <= 0 {
		maxHeight = canvasSize.Height
	}

	padding := clampFloat32(minFloat32(maxWidth, maxHeight)*0.04, 14, 21)
	headerHeight := float32(34)
	gap := float32(12)
	videoMaxWidth := maxFloat32(0, maxWidth-padding*2)
	videoMaxHeight := maxFloat32(0, maxHeight-padding*2-headerHeight-gap)

	// ~20% smaller than this popup's previous 680px video cap.
	videoWidth := minFloat32(544, videoMaxWidth)
	videoHeight := videoWidth * 3 / 4
	if videoHeight > videoMaxHeight {
		videoHeight = videoMaxHeight
		videoWidth = videoHeight * 4 / 3
	}
	if videoWidth > videoMaxWidth {
		videoWidth = videoMaxWidth
		videoHeight = videoWidth * 3 / 4
	}

	// ~20% smaller than this popup's previous 320/260 floor.
	panelMinWidth := minFloat32(256, maxWidth)
	panelMinHeight := minFloat32(208, maxHeight)
	panelWidth := clampFloat32(videoWidth+padding*2, panelMinWidth, maxWidth)
	panelHeight := clampFloat32(padding*2+headerHeight+gap+videoHeight, panelMinHeight, maxHeight)
	return fyne.NewSize(panelWidth, panelHeight)
}

// decodeQRImage tries to find and decode a QR code in img, returning its text on success.
func decodeQRImage(reader gozxing.Reader, img image.Image) (string, bool) {
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", false
	}
	result, err := reader.Decode(bmp, nil)
	if err != nil {
		return "", false
	}
	return result.GetText(), true
}
