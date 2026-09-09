package gui

import (
	"fmt"
	"image/color"
	"math"
	"path/filepath"
	"strings"
	"time"

	"usbridge-client/internal/gui/assets"
	"usbridge-client/internal/gui/controller"
	"usbridge-client/internal/gui/design"
	"usbridge-client/internal/gui/i18n"
	"usbridge-client/internal/gui/view"
	"usbridge-client/internal/models"
	"usbridge-client/internal/service"

	"github.com/sirupsen/logrus"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	addressBarGap        float32 = 10
	addressBarControlH   float32 = 36
	addressBarActionBtn  float32 = 36
	addressBarHostHideAt float32 = 180
	statusIconSize       float32 = 18
)

func protocolDropdownLabel(protocol string) string {
	switch strings.TrimSpace(protocol) {
	case models.ConnectionProtocolTailscale:
		return "ts"
	case models.ConnectionProtocolDirect:
		return "lan"
	default:
		return models.ConnectionProtocolAuto
	}
}

func protocolDropdownValue(label string) string {
	switch strings.TrimSpace(strings.ToLower(label)) {
	case "ts":
		return models.ConnectionProtocolTailscale
	case "lan":
		return models.ConnectionProtocolDirect
	default:
		return models.ConnectionProtocolAuto
	}
}

type tabsTheme struct {
	base fyne.Theme
}

func (t *tabsTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case fynetheme.ColorNameHover, fynetheme.ColorNamePressed, fynetheme.ColorNameFocus:
		return color.Transparent
	case fynetheme.ColorNameShadow, fynetheme.ColorNameSeparator:
		return color.Transparent
	default:
		return t.base.Color(name, variant)
	}
}

func (t *tabsTheme) Font(style fyne.TextStyle) fyne.Resource {
	return t.base.Font(style)
}

func (t *tabsTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return t.base.Icon(name)
}

func (t *tabsTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case fynetheme.SizeNamePadding:
		return 2
	}
	return t.base.Size(name)
}

// createInterface initializes the address bar fields.
func (mw *MainWindow) createInterface() {
	mw.hostEntry = widget.NewEntry()
	mw.hostEntry.SetPlaceHolder(i18n.Current.ServerAddress)

	mw.hostEntry.OnChanged = func(string) {
		mw.persistConnectionDraft()
		if mw.connectionManager != nil {
			mw.connectionManager.HandleFormEdited(
				mw.hostEntry.Text,
				mw.tokenEntry.Text,
				mw.protocolSelect.Selected,
			)
		}
		mw.refreshConnectionControls()
	}

	mw.tokenEntry = widget.NewEntry()
	mw.tokenEntry.SetPlaceHolder("Master Key")
	mw.tokenEntry.Password = true
	mw.tokenEntry.OnChanged = func(string) {
		mw.persistConnectionDraft()
		if mw.connectionManager != nil {
			mw.connectionManager.HandleFormEdited(
				mw.hostEntry.Text,
				mw.tokenEntry.Text,
				mw.protocolSelect.Selected,
			)
		}
	}

	mw.protocolSelect = widget.NewSelect([]string{
		models.ConnectionProtocolAuto,
		models.ConnectionProtocolTailscale,
		models.ConnectionProtocolDirect,
	}, nil)
	mw.protocolSelect.OnChanged = func(value string) {
		mw.persistConnectionDraft()
		if mw.connectionManager != nil {
			mw.connectionManager.HandleFormEdited(
				mw.hostEntry.Text,
				mw.tokenEntry.Text,
				value,
			)
		}
		if mw.protocolDropdown != nil {
			mw.protocolDropdown.SetSelected(protocolDropdownLabel(value))
		}
	}

	mw.protocolDropdown = view.NewHeaderDropdown([]string{
		models.ConnectionProtocolAuto,
		"ts",
		"lan",
	}, protocolDropdownLabel(mw.config.ConnectionProtocol), func(value string) {
		mw.protocolSelect.SetSelected(protocolDropdownValue(value))
	})
	mw.protocolDropdown.Compact = true
	mw.protocolSelect.SetSelected(mw.config.ConnectionProtocol)

	mw.connectionBtn = view.NewHeaderActionButton(mw.handleConnectionToggle)

	mw.sdStorageProgress = view.NewStorageProgressBar()
	mw.sdStorageProgress.SetOnTapped(mw.showStorageInfoDialog)
	mw.sdStorageProgress.Hide()

	if mw.backupWidget != nil {
		mw.backupWidget.UpdateHostEntry(mw.hostEntry)
	}

	mw.refreshConnectionControls()
}

func (mw *MainWindow) refreshConnectionControls() {
	if mw.connectionBtn == nil || mw.protocolSelect == nil || mw.protocolDropdown == nil {
		return
	}

	if mw.isConnected {
		text, fill, foreground := protocolButtonState(mw.connectedProtocol)
		mw.connectionBtn.ApplySpec(view.HeaderActionButtonSpec{
			Disabled:   mw.isConnectionPending.Load(),
			Fill:       fill,
			Foreground: foreground,
			Stroke:     color.Transparent,
			Text:       text,
		})
		mw.protocolDropdown.Hide()
	} else {
		hasHost := strings.TrimSpace(mw.hostEntry.Text) != ""
		spec := view.HeaderActionButtonSpec{
			Disabled:   mw.isConnectionPending.Load() || !hasHost,
			Fill:       design.ColorAccent,
			Foreground: design.ColorBackground,
			Icon:       assets.ConnectIconBoldBlack,
			Stroke:     color.Transparent,
		}

		if !hasHost {
			spec.Fill = color.Transparent
			spec.Foreground = design.ColorBorder
			spec.Icon = assets.ConnectIconMuted
			spec.Stroke = design.ColorBorder
			spec.StrokeWidth = 1
		}
		if mw.isConnectionLoading {
			spec.Disabled = true
			spec.Fill = design.ColorAccent
			spec.Foreground = design.ColorBackground
			spec.Icon = nil
			spec.Stroke = color.Transparent
			spec.SpinnerFrames = assets.LoadingGrayFrames
		}

		mw.connectionBtn.ApplySpec(spec)
		mw.protocolDropdown.SetDisabled(mw.isConnectionPending.Load())
		mw.protocolDropdown.Show()
	}

	mw.protocolDropdown.Refresh()
	if mw.connectionManager != nil && mw.isConnectionPending.Load() && !mw.isConnected {
		mw.connectionManager.SetConnectionPending(true)
	}
}

// setDefaultValues sets initial values for fields.
func (mw *MainWindow) setDefaultValues() {
	mw.restoreConnectionDraft()
}

// recreateContainers recreates containers with the connection manager.
func (mw *MainWindow) recreateContainers() {
	storageUpdate := func(usedPct float64, available, total int64) {
		fyne.Do(func() {
			if mw.sdStorageProgress == nil {
				return
			}
			if total <= 0 {
				mw.sdStorageProgress.Hide()
				return
			}
			mw.currentStorageTotal = total
			mw.currentStorageAvailable = available
			mw.currentStorageDir = ""
			if mw.backupWidget != nil && mw.backupWidget.GetISODirectory() != "" {
				mw.currentStorageDir = mw.backupWidget.GetISODirectory()
			}
			if mw.currentStorageDir == "" && mw.diskWidget != nil {
				mw.currentStorageDir = mw.diskWidget.GetISODirectory()
			}
			icon := assets.MemoryChipIcon
			if mw.currentStorageDir == "/mnt/sdcard/iso" {
				icon = assets.SDCardIcon
			}
			mw.sdStorageProgress.SetIcon(icon)
			mw.sdStorageProgress.SetValue(usedPct)
			used := total - available
			if used < 0 {
				used = 0
			}
			mw.sdStorageProgress.SetSizeText(models.FormatStorageSizeOnly(used, total))
			mw.sdStorageProgress.Show()
			mw.refreshMainHeaderLayout()
		})
	}
	if mw.diskWidget != nil {
		mw.diskWidget.SetOnStorageInfoUpdate(storageUpdate)
		mw.diskWidget.SetOnButtonsChanged(mw.refreshDeviceFooterButtons)
		if mw.videoWidget != nil {
			mw.diskWidget.SetOnMouseTypeChanged(mw.videoWidget.SetMouseInputMode)
			mw.diskWidget.SetOnMouseModeReconfigured(func() {
				mw.videoWidget.RecoverAfterControlDeviceRebuildAsync()
			})
			mw.diskWidget.SetOnVideoConfigRequested(func(devicePath string) {
				mw.videoWidget.ShowVideoDeviceSettings(devicePath, mw.tabs != nil && mw.tabs.SelectedIndex() == mw.controlTabIndex(), false)
			})
			mw.diskWidget.SetOnVideoConnect(func(devicePath string) {
				mw.videoWidget.StartVideoDeviceAsync(devicePath)
			})
			mw.diskWidget.SetOnVideoDisconnect(func() {
				mw.videoWidget.StopVideoAsync()
			})
			mw.diskWidget.SetOnAudioConnect(func(devicePath string) {
				if mw.usbClient != nil {
					if err := mw.usbClient.StartAudio(devicePath); err != nil {
						logrus.Errorf("Failed to start audio: %v", err)
					} else {
						mw.updateStatusBar()
					}
				}
			})
			mw.diskWidget.SetOnAudioDisconnect(func() {
				if mw.usbClient != nil {
					if err := mw.usbClient.StopAudio(); err != nil {
						logrus.Errorf("Failed to stop audio: %v", err)
					} else {
						mw.updateStatusBar()
					}
				}
			})
			mw.diskWidget.SetOnUSBAudioConnect(func(mode string) {
				if mw.usbClient != nil {
					// Synchronous: selectUSBAudio goroutine waits for USB gadget teardown+rebuild
					// to complete before calling onAudioConnect("uac"). If this were async, the UAC
					// ALSA card would be destroyed mid-loopback, killing the PulseAudio loopback.
					req := models.DeviceStartBatchRequest{models.DeviceStartRequest{
						Device: "usbaudio",
						Type:   mode,
					}}
					if _, err := mw.usbClient.StartDevicesBatchWithMerge(req, true); err != nil {
						logrus.Errorf("Failed to connect USB audio codec: %v", err)
					}
				}
			})
			mw.videoWidget.SetOnFPSChanged(func(fps float64) {
				mw.currentVideoFPS = fps
				mw.updateVideoIconLabel()
			})
		}
	}
	if mw.backupWidget != nil {
		mw.backupWidget.SetOnStorageInfoUpdate(storageUpdate)
	}

	mw.createStatusBar()
	mainAddressBar := mw.createMainAddressBar()
	connectionAddressBar := mw.createConnectionAddressBar()
	mainFooter := mw.createDeviceFooterBar()
	connectionFooter := mw.createConnectionFooterBar()
	devicesTabTitle := "Devices"
	controlTabTitle := "Control"
	snapshotsTabTitle := "Snapshots"
	scriptsTabTitle := "Scripts"

	mw.tabs = container.NewAppTabs(
		container.NewTabItem(controlTabTitle, container.NewThemeOverride(mw.videoWidget.GetContainer(), design.NewBrandTheme())),
		container.NewTabItem(devicesTabTitle, container.NewThemeOverride(mw.diskWidget.GetContainer(), design.NewBrandTheme())),
		container.NewTabItem(snapshotsTabTitle, container.NewThemeOverride(mw.createBackupFlashTab(), design.NewBrandTheme())),
		container.NewTabItem(scriptsTabTitle, container.NewThemeOverride(mw.scriptsWidget.GetContainer(), design.NewBrandTheme())),
	)
	mw.applyTabVisualState(0)
	mw.tabs.OnSelected = func(tab *container.TabItem) {
		tabSwitchStart := time.Now()
		tabName := "?"
		if tab != nil {
			tabName = tab.Text
		}
		mw.applyTabVisualState(mw.tabs.SelectedIndex())
		mw.updateDeviceButtonsVisibility()
		// syncVideoOverlayForNav is the single authoritative place that
		// calls NotifyOverlayShow/Hide for tab/screen navigation — see its
		// own doc comment for why this must not do so directly.
		mw.syncVideoOverlayForNav()
		if tab != nil && tab.Text == controlTabTitle {
			if mw.videoWidget != nil {
				mw.videoWidget.BootstrapControlSessionAsync()
				mw.videoWidget.RefreshViewportGeometry()
				// The tab's content may not have been laid out to its final
				// size yet at the point this callback runs, so retry shortly
				// after — mirrors scheduleControlBootstrap's settle delays.
				time.AfterFunc(150*time.Millisecond, mw.videoWidget.RefreshViewportGeometry)
			}
		}
		if tab != nil && tab.Text == snapshotsTabTitle {
			// Snapshots otherwise only update via BackupWidget's 30s background
			// ticker -- opening this tab right after a new snapshot lands on
			// the device (e.g. via the on-device auto-snapshot daemon) could
			// show stale data for up to 30s, easy to mistake for "doesn't
			// update at all" since only a full reconnect forced an immediate
			// fetch. Force one here too, same as Control's bootstrap above.
			if mw.backupWidget != nil {
				mw.backupWidget.Refresh()
			}
		}
		logrus.Infof("📑 [Tabs] switched to %q in %v", tabName, time.Since(tabSwitchStart))
	}

	deviceFooterOverlay := container.NewBorder(nil, mainFooter, nil, nil, nil)
	tabsWithTheme := container.NewThemeOverride(mw.tabs, &tabsTheme{base: design.NewBrandTheme()})

	mainBg := canvas.NewRectangle(design.ColorGray950)
	mw.mainContent = container.NewStack(
		mainBg,
		container.NewBorder(
			mainAddressBar,
			nil,
			nil,
			nil,
			container.NewStack(tabsWithTheme, deviceFooterOverlay),
		),
	)

	connBg := canvas.NewRectangle(design.ColorGray950)
	mw.connectionContent = container.NewStack(
		connBg,
		container.NewBorder(
			connectionAddressBar,
			connectionFooter,
			nil,
			nil,
			mw.connectionManager.GetContainer(),
		),
	)

	mw.window.SetContent(mw.wrapWithResizeGuard(mw.connectionContent))
	mw.onMainContent = false
}

func (mw *MainWindow) applyTabVisualState(activeIndex int) {
	if mw == nil || mw.tabs == nil || len(mw.tabs.Items) < 3 {
		return
	}
	_ = activeIndex
}

// createConnectionAddressBar creates the connection screen's header bar (see
// connection_header.go for the component itself). This is only the
// composition point: it owns nothing visual, just wires the header's
// reported actions to the real controller calls.
func (mw *MainWindow) createConnectionAddressBar() *fyne.Container {
	band, handle := newConnectionHeader(connectionHeaderActions{
		OnSelectLanguage: func(code string) {
			if mw.connectionManager != nil {
				mw.connectionManager.SetLanguage(code)
			}
		},
		OnOpenCommunity: func() {
			if mw.connectionManager != nil {
				mw.connectionManager.OpenDiscordInvite()
			}
		},
		OnOpenInfo: func() {
			if mw.connectionManager != nil {
				mw.connectionManager.OpenInfoPage()
			}
		},
		OnToggleTailscale: func() {
			if mw.connectionManager != nil {
				mw.connectionManager.ToggleTailscale()
			}
		},
		OnOpenAccount: func() {
			mw.showAccountDialog()
		},
	}, mw.app.Preferences().StringWithFallback("language", "en"))

	// The header owns the Tailscale toggle widget now; hand the controller a
	// way to push live status into it without either side knowing the
	// other's types (see ConnectionHeaderHandle).
	if mw.connectionManager != nil {
		mw.connectionManager.SetTailscaleStatusSink(handle.SetTailscaleState)
		mw.connectionManager.SetAccountStateSink(handle.SetAccountState)
		mw.connectionManager.SetConnectingStateSink(mw.handleConnectingStateChange)
	}

	return band
}

// createMainAddressBar creates the main address bar.
func (mw *MainWindow) createMainAddressBar() *fyne.Container {
	if mw.pcpanelWidget == nil {
		mw.pcpanelWidget = controller.NewPCPanelWidget(mw.window)
	}
	if mw.mainExitBtn == nil {
		mw.mainExitBtn = view.NewHeaderActionButton(mw.handleConnectionToggle)
		mw.mainExitBtn.ApplySpec(view.HeaderActionButtonSpec{
			Fill:        design.ColorSurfaceLight,
			Foreground:  design.ColorTextLight,
			Stroke:      color.NRGBA{R: 0xd6, G: 0x6d, B: 0x6d, A: 0xff},
			StrokeWidth: 1.2,
			Icon:        assets.ExitIcon,
			IconSize:    fyne.NewSize(24, 24),
		})
	}
	exitPanel := container.NewGridWrap(fyne.NewSize(addressBarActionBtn, addressBarControlH), mw.mainExitBtn)
	rightGroup := container.New(&exitStatusOverlayLayout{badgeInsetX: -7, badgeInsetY: -3}, exitPanel, mw.protocolPanel)
	middleGroup := container.New(&centeredInlineLayout{gap: 8, minGap: 4}, mw.sdStorageProgress, mw.statusPanel)
	// Clip, not Scroll: on a narrow/mobile window this row can genuinely
	// run out of horizontal space for the SD-progress + status readout,
	// but they're passive indicators, not something worth navigating to --
	// an HScroll here used to leave a persistent thin scrollbar sitting
	// right above the video/Control-tab content whenever that happened
	// (Fyne's scroll-bar-area renders any time content overflows its
	// viewport, not just on hover/drag -- see internal/widget/scroller.go's
	// handleAreaVisibility), which read as a stray UI glitch since nobody
	// was ever meant to actually scroll this row. Clip keeps
	// mainHeaderBarLayout.Layout's existing width-capping math (below)
	// working exactly the same way, it just quietly clips whatever
	// overflows instead of exposing a scrollbar for it.
	middleClip := container.NewClip(middleGroup)
	row := container.New(
		&mainHeaderBarLayout{edgeInset: 0, sideGap: 10},
		mw.pcpanelWidget.GetContainer(),
		middleClip,
		rightGroup,
	)
	return view.NewHeaderBand("", row)
}

func headerGapSpacer(width float32) fyne.CanvasObject {
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(width, 1))
	return spacer
}

func newHeaderPassiveIndicator(icon fyne.Resource) fyne.CanvasObject {
	image := canvas.NewImageFromResource(icon)
	image.FillMode = canvas.ImageFillContain
	image.SetMinSize(fyne.NewSize(18, 18))
	return container.NewGridWrap(
		fyne.NewSize(28, 28),
		container.NewCenter(image),
	)
}

func (mw *MainWindow) createConnectionFooterBar() *fyne.Container {
	bar := container.NewWithoutLayout()
	bar.Hide()
	mw.connectionFooterBar = bar
	return bar
}

func (mw *MainWindow) createDeviceFooterBar() *fyne.Container {
	bottomInset := float32(6)
	if fyne.CurrentDevice().IsMobile() {
		bottomInset = 36
	}
	bar := view.NewInset(container.NewCenter(mw.deviceButtonsPanel), 6, 8, 2, bottomInset)
	mw.deviceFooterBar = bar
	mw.deviceFooterBar.Hide()
	return bar
}

func (mw *MainWindow) updateConnectionFooterVisibility(hasConnections bool) {
	if mw.connectionFooterBar == nil {
		return
	}

	fyne.Do(func() {
		_ = hasConnections
		mw.connectionFooterBar.Hide()

		if content := mw.window.Content(); content != nil {
			content.Refresh()
			mw.window.Canvas().Refresh(content)
		}
	})
}

type collapsingBoxLayout struct{}
type optionalLeadingGapLayout struct {
	gap float32
}
type connectionAddressBarLayout struct {
	gap           float32
	hideHostAt    float32
	keepHostShown func() bool
}
type addressBarResponsiveLayout struct {
	hideHostAt    float32
	keepHostShown func() bool
}
type mainHeaderBarLayout struct {
	edgeInset float32
	sideGap   float32
}
type exitStatusOverlayLayout struct {
	badgeInsetX float32
	badgeInsetY float32
}
type centeredInlineLayout struct {
	gap    float32
	minGap float32
}
type distributedVisibleLayout struct {
	minGap float32
}

func newCollapsingBox(objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(&collapsingBoxLayout{}, objects...)
}

func newOptionalLeadingGap(width float32, content fyne.CanvasObject) *fyne.Container {
	return container.New(&optionalLeadingGapLayout{gap: width}, headerGapSpacer(width), content)
}

func newHeaderStatusIcon(resource fyne.Resource) fyne.CanvasObject {
	icon := canvas.NewImageFromResource(resource)
	icon.FillMode = canvas.ImageFillContain
	return container.NewGridWrap(fyne.NewSize(statusIconSize, statusIconSize), icon)
}

func newProtocolIndicator(protocol string) fyne.CanvasObject {
	textColor := design.ColorTextMuted
	if strings.TrimSpace(protocol) != "" {
		textColor = design.ColorAccent
	}

	label := canvas.NewText(strings.TrimSpace(protocol), textColor)
	label.TextSize = 14
	label.TextStyle.Bold = true

	return container.NewCenter(label)
}

func newProtocolBadge(protocol string) fyne.CanvasObject {
	text, fill, foreground := protocolButtonState(protocol)
	if strings.TrimSpace(protocol) == "" {
		fill = design.ColorBorder
		foreground = design.ColorTextLight
		text = ""
	}

	bg := canvas.NewRectangle(fill)
	bg.CornerRadius = 7
	bg.StrokeColor = design.ColorBackground
	bg.StrokeWidth = 1.2

	label := canvas.NewText(strings.TrimSpace(text), foreground)
	label.TextSize = 8
	label.TextStyle.Bold = true

	badgeWidth := fyne.MeasureText(strings.TrimSpace(text), 8, fyne.TextStyle{Bold: true}).Width + 8
	if badgeWidth < 20 {
		badgeWidth = 20
	}

	return container.NewGridWrap(
		fyne.NewSize(badgeWidth, 14),
		container.NewMax(bg, container.NewCenter(label)),
	)
}

func minFloat32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func maxFloat32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func (l *distributedVisibleLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	visible := make([]fyne.CanvasObject, 0, len(objects))
	totalWidth := float32(0)
	maxHeight := float32(0)
	for _, obj := range objects {
		if !hasVisibleContent(obj) {
			obj.Move(fyne.NewPos(0, 0))
			obj.Resize(fyne.NewSize(0, 0))
			continue
		}
		min := obj.MinSize()
		totalWidth += min.Width
		if min.Height > maxHeight {
			maxHeight = min.Height
		}
		visible = append(visible, obj)
	}
	if len(visible) == 0 {
		return
	}
	if len(visible) == 1 {
		min := visible[0].MinSize()
		visible[0].Move(fyne.NewPos(0, maxFloat32(0, (size.Height-min.Height)/2)))
		visible[0].Resize(min)
		return
	}

	gap := l.minGap
	if extra := size.Width - totalWidth; extra > 0 {
		distributed := extra / float32(len(visible)-1)
		if distributed > gap {
			gap = distributed
		}
	}

	x := float32(0)
	for _, obj := range visible {
		min := obj.MinSize()
		obj.Move(fyne.NewPos(x, maxFloat32(0, (size.Height-min.Height)/2)))
		obj.Resize(min)
		x += min.Width + gap
	}
}

func (l *distributedVisibleLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	width := float32(0)
	height := float32(0)
	count := 0
	for _, obj := range objects {
		if !hasVisibleContent(obj) {
			continue
		}
		min := obj.MinSize()
		width += min.Width
		if min.Height > height {
			height = min.Height
		}
		count++
	}
	if count > 1 {
		width += float32(count-1) * l.minGap
	}
	return fyne.NewSize(width, height)
}

func (l *mainHeaderBarLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 3 {
		return
	}

	left := objects[0]
	center := objects[1]
	right := objects[2]

	leftMin := left.MinSize()
	rightMin := right.MinSize()
	// centerMin comes from the wrapped content's own MinSize, not center's
	// (the Clip wrapper) -- Clip.MinSize() always reports a degenerate
	// {1,1} since it has no natural size of its own (see container/clip.go),
	// so using it directly here would collapse both this row's height and
	// centerY's vertical centering down to that same 1px.
	centerMin := headerCenterContentMinSize(center)

	leftY := maxFloat32(0, (size.Height-leftMin.Height)/2)
	left.Move(fyne.NewPos(l.edgeInset, leftY))
	left.Resize(leftMin)

	rightX := maxFloat32(0, size.Width-l.edgeInset-rightMin.Width)
	rightY := maxFloat32(0, (size.Height-rightMin.Height)/2)
	right.Move(fyne.NewPos(rightX, rightY))
	right.Resize(rightMin)

	centerMaxWidth := rightX - (leftMin.Width + l.edgeInset*2 + l.sideGap*2)
	if centerMaxWidth < 0 {
		centerMaxWidth = 0
	}
	centerWidth := minFloat32(centerMin.Width, centerMaxWidth)
	centerMinX := leftMin.Width + l.edgeInset + l.sideGap
	centerMaxX := rightX - l.sideGap - centerWidth
	centerX := maxFloat32(centerMinX, (size.Width-centerWidth)/2)
	if centerX > centerMaxX {
		centerX = maxFloat32(centerMinX, centerMaxX)
	}
	centerY := maxFloat32(0, (size.Height-centerMin.Height)/2)
	center.Move(fyne.NewPos(centerX, centerY))
	center.Resize(fyne.NewSize(centerWidth, centerMin.Height))
}

func (l *mainHeaderBarLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) < 3 {
		return fyne.NewSize(0, 0)
	}

	leftMin := objects[0].MinSize()
	centerMin := headerCenterContentMinSize(objects[1])
	rightMin := objects[2].MinSize()
	height := maxFloat32(leftMin.Height, maxFloat32(centerMin.Height, rightMin.Height))
	return fyne.NewSize(leftMin.Width+centerMin.Width+rightMin.Width+l.edgeInset*2+l.sideGap*2, height)
}

// headerCenterContentMinSize reports the natural size of the header bar's
// center slot -- unwrapping container.Clip if that's what's actually
// there (see createMainAddressBar's middleClip) rather than trusting
// Clip.MinSize() itself, which always degenerates to {1,1} since a Clip
// has no natural size of its own.
func headerCenterContentMinSize(center fyne.CanvasObject) fyne.Size {
	if cl, ok := center.(*container.Clip); ok && cl.Content != nil {
		return cl.Content.MinSize()
	}
	return center.MinSize()
}

func (l *exitStatusOverlayLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}

	base := objects[0]
	base.Resize(size)
	base.Move(fyne.NewPos(0, 0))

	if len(objects) < 2 || !hasVisibleContent(objects[1]) {
		return
	}

	badge := objects[1]
	badgeMin := badge.MinSize()
	baseWidth := base.MinSize().Width
	if baseWidth <= 0 || baseWidth > size.Width {
		baseWidth = size.Width
	}
	badgeX := (baseWidth - badgeMin.Width) / 2
	badge.Move(fyne.NewPos(badgeX, l.badgeInsetY))
	badge.Resize(badgeMin)
}

func (l *exitStatusOverlayLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) == 0 {
		return fyne.NewSize(0, 0)
	}
	return objects[0].MinSize()
}

func (l *centeredInlineLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	visible := make([]fyne.CanvasObject, 0, len(objects))
	totalChildWidth := float32(0)
	maxHeight := float32(0)

	for _, obj := range objects {
		if !hasVisibleContent(obj) {
			obj.Move(fyne.NewPos(0, 0))
			obj.Resize(fyne.NewSize(0, 0))
			continue
		}
		min := obj.MinSize()
		totalChildWidth += min.Width
		if min.Height > maxHeight {
			maxHeight = min.Height
		}
		visible = append(visible, obj)
	}

	if len(visible) == 0 {
		return
	}
	gap := l.gap
	if len(visible) > 1 {
		minGap := l.minGap
		if minGap < 0 {
			minGap = 0
		}
		maxGap := (size.Width - totalChildWidth) / float32(len(visible)-1)
		if maxGap < minGap {
			gap = minGap
		} else if maxGap < gap {
			gap = maxGap
		}
	}
	totalWidth := totalChildWidth
	if len(visible) > 1 {
		totalWidth += float32(len(visible)-1) * gap
	}

	x := maxFloat32(0, (size.Width-totalWidth)/2)
	for _, obj := range visible {
		min := obj.MinSize()
		obj.Move(fyne.NewPos(x, maxFloat32(0, (size.Height-min.Height)/2)))
		obj.Resize(min)
		x += min.Width + gap
	}
}

func (l *centeredInlineLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	width := float32(0)
	height := float32(0)
	count := 0
	for _, obj := range objects {
		if !hasVisibleContent(obj) {
			continue
		}
		min := obj.MinSize()
		width += min.Width
		if min.Height > height {
			height = min.Height
		}
		count++
	}
	if count > 1 {
		gap := l.gap
		if gap < l.minGap {
			gap = l.minGap
		}
		width += float32(count-1) * gap
	}
	return fyne.NewSize(width, height)
}

func (l *connectionAddressBarLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 3 {
		return
	}

	host := objects[0]
	protocol := objects[1]
	connect := objects[2]

	connectMin := connect.MinSize()
	connectWidth := minFloat32(connectMin.Width, size.Width)
	connectX := maxFloat32(0, size.Width-connectWidth)
	connect.Move(fyne.NewPos(connectX, maxFloat32(0, (size.Height-connectMin.Height)/2)))
	connect.Resize(fyne.NewSize(connectWidth, minFloat32(size.Height, connectMin.Height)))

	availableForProtocol := connectX - l.gap
	if availableForProtocol < 0 {
		availableForProtocol = 0
	}
	protocolMin := protocol.MinSize()
	protocolWidth := minFloat32(protocolMin.Width, availableForProtocol)
	protocolX := maxFloat32(0, availableForProtocol-protocolWidth)
	protocol.Move(fyne.NewPos(protocolX, maxFloat32(0, (size.Height-protocolMin.Height)/2)))
	protocol.Resize(fyne.NewSize(protocolWidth, minFloat32(size.Height, protocolMin.Height)))

	hostWidth := protocolX - l.gap
	keepShown := l.keepHostShown != nil && l.keepHostShown()
	if !keepShown && hostWidth < l.hideHostAt {
		host.Hide()
		host.Move(fyne.NewPos(0, 0))
		host.Resize(fyne.NewSize(0, 0))
		return
	}

	if hostWidth < 0 {
		hostWidth = 0
	}
	host.Show()
	hostMin := host.MinSize()
	host.Move(fyne.NewPos(0, maxFloat32(0, (size.Height-hostMin.Height)/2)))
	host.Resize(fyne.NewSize(hostWidth, minFloat32(size.Height, hostMin.Height)))
}

func (l *connectionAddressBarLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) < 3 {
		return fyne.NewSize(0, 0)
	}

	hostMin := objects[0].MinSize()
	protocolMin := objects[1].MinSize()
	connectMin := objects[2].MinSize()
	height := maxFloat32(hostMin.Height, maxFloat32(protocolMin.Height, connectMin.Height))
	return fyne.NewSize(protocolMin.Width+connectMin.Width+2*l.gap, height)
}

func (l *addressBarResponsiveLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 2 {
		return
	}

	host := objects[0]
	right := objects[1]
	rightMin := right.MinSize()
	rightWidth := minFloat32(size.Width, rightMin.Width)
	rightX := size.Width - rightWidth
	if rightX < 0 {
		rightX = 0
	}

	right.Move(fyne.NewPos(rightX, maxFloat32(0, (size.Height-rightMin.Height)/2)))
	right.Resize(fyne.NewSize(rightWidth, minFloat32(size.Height, rightMin.Height)))

	hostWidth := size.Width - rightWidth
	keepShown := l.keepHostShown != nil && l.keepHostShown()
	if !keepShown && hostWidth < l.hideHostAt {
		host.Hide()
		host.Move(fyne.NewPos(0, 0))
		host.Resize(fyne.NewSize(0, 0))
		return
	}

	host.Show()
	hostMin := host.MinSize()
	host.Move(fyne.NewPos(0, maxFloat32(0, (size.Height-hostMin.Height)/2)))
	host.Resize(fyne.NewSize(hostWidth, minFloat32(size.Height, hostMin.Height)))
}

func (l *addressBarResponsiveLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) < 2 {
		return fyne.NewSize(0, 0)
	}

	hostMin := objects[0].MinSize()
	rightMin := objects[1].MinSize()
	height := maxFloat32(hostMin.Height, rightMin.Height)
	return fyne.NewSize(rightMin.Width, height)
}

func (l *collapsingBoxLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	x := float32(0)
	for _, obj := range objects {
		if !hasVisibleContent(obj) {
			obj.Resize(fyne.NewSize(0, 0))
			continue
		}
		min := obj.MinSize()
		obj.Move(fyne.NewPos(x, (size.Height-min.Height)/2))
		obj.Resize(min)
		x += min.Width
	}
}

func (l *collapsingBoxLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	width := float32(0)
	height := float32(0)
	for _, obj := range objects {
		if !hasVisibleContent(obj) {
			continue
		}
		min := obj.MinSize()
		width += min.Width
		if min.Height > height {
			height = min.Height
		}
	}
	return fyne.NewSize(width, height)
}

func (l *optionalLeadingGapLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) != 2 {
		return
	}

	gapObj := objects[0]
	content := objects[1]
	if !hasVisibleContent(content) {
		gapObj.Resize(fyne.NewSize(0, 0))
		content.Resize(fyne.NewSize(0, 0))
		return
	}

	contentMin := content.MinSize()
	gapObj.Move(fyne.NewPos(0, 0))
	gapObj.Resize(fyne.NewSize(l.gap, size.Height))
	content.Move(fyne.NewPos(l.gap, (size.Height-contentMin.Height)/2))
	content.Resize(contentMin)
}

func (l *optionalLeadingGapLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) != 2 || !hasVisibleContent(objects[1]) {
		return fyne.NewSize(0, 0)
	}

	contentMin := objects[1].MinSize()
	return fyne.NewSize(l.gap+contentMin.Width, contentMin.Height)
}

func hasVisibleContent(obj fyne.CanvasObject) bool {
	if obj == nil || !obj.Visible() {
		return false
	}
	if c, ok := obj.(*fyne.Container); ok {
		for _, child := range c.Objects {
			if hasVisibleContent(child) {
				return true
			}
		}
		return false
	}
	return true
}

// createStatusBar creates the status bar.
func (mw *MainWindow) createStatusBar() *fyne.Container {
	mw.connectionIcon = widget.NewButton("🔌", func() {})
	mw.connectionIcon.Importance = widget.LowImportance
	mw.nbdIcon = widget.NewButton("💿", func() {})
	mw.nbdIcon.Importance = widget.LowImportance
	mw.videoIcon = newHeaderStatusBadgeButton(assets.CameraIcon, func() {
		mw.showVideoMenu()
	})
	mw.videoIcon.Hide()
	mw.audioIcon = newHeaderStatusBadgeButton(assets.AudioIcon, func() {
		mw.showAudioMenu()
	})
	mw.audioIcon.SetBadgeText("")
	mw.audioIcon.Hide()
	mw.captureIcon = widget.NewButtonWithIcon("", assets.CameraIcon, func() {
		if mw.videoWidget != nil {
			mw.videoWidget.ShowCurrentVideoSettings(false)
		}
	})
	mw.captureIcon.Importance = widget.LowImportance
	mw.keyboardIcon = widget.NewButtonWithIcon("", assets.KeyboardIcon, func() {
		if mw.tabs != nil && len(mw.tabs.Items) > mw.controlTabIndex() {
			mw.tabs.Select(mw.tabs.Items[mw.controlTabIndex()])
		}
		if mw.videoWidget != nil {
			mw.videoWidget.HandleVirtualKeyboard()
		}
	})
	mw.keyboardIcon.Importance = widget.LowImportance
	mw.keyboardIcon.Hide()
	mw.mouseIcon = widget.NewButtonWithIcon("", assets.MouseIcon, func() {
		mw.showMouseModeMenu()
	})
	mw.mouseIcon.Importance = widget.LowImportance
	mw.mouseIcon.Hide()
	mw.rndisIcon = widget.NewButtonWithIcon("", assets.NetworkIcon, func() {
		mw.showRNDISModeMenu()
	})
	mw.rndisIcon.Importance = widget.LowImportance
	mw.rndisIcon.Hide()
	mw.gamepadIcon = widget.NewButtonWithIcon("", assets.GamepadIconActive, func() {
		if mw.tabs != nil && len(mw.tabs.Items) > mw.devicesTabIndex() {
			mw.tabs.Select(mw.tabs.Items[mw.devicesTabIndex()])
		}
	})
	mw.gamepadIcon.Importance = widget.LowImportance
	mw.gamepadIcon.Hide()
	mw.cdromIcon = widget.NewButtonWithIcon("", assets.DiscIcon, func() {
		if mw.tabs != nil && len(mw.tabs.Items) > mw.devicesTabIndex() {
			mw.tabs.Select(mw.tabs.Items[mw.devicesTabIndex()])
		}
	})
	mw.cdromIcon.Importance = widget.LowImportance
	mw.cdromIcon.Hide()
	mw.backupIcon = newHeaderPassiveIndicator(assets.SDCardIconActive)
	mw.backupIcon.Hide()
	mw.snapshotIcon = widget.NewButtonWithIcon("", assets.SnapshotsTabIconActive, func() {
		if mw.tabs != nil && len(mw.tabs.Items) > mw.snapshotsTabIndex() {
			mw.tabs.Select(mw.tabs.Items[mw.snapshotsTabIndex()])
		}
	})
	mw.snapshotIcon.Importance = widget.LowImportance
	mw.snapshotIcon.Hide()
	mw.scriptIcon = widget.NewButtonWithIcon("", fynetheme.MediaPlayIcon(), func() {
		mw.showScriptRunningMenu()
	})
	mw.scriptIcon.Importance = widget.LowImportance
	mw.scriptIcon.Hide()

	mw.statusPanel = container.New(&centeredInlineLayout{gap: 4, minGap: 2})
	mw.statusPanel.Objects = buildHeaderStatusIndicators(
		mw.backupIcon,
		mw.videoIcon,
		mw.audioIcon,
		mw.cdromIcon,
		mw.keyboardIcon,
		mw.mouseIcon,
		mw.rndisIcon,
		mw.gamepadIcon,
		mw.snapshotIcon,
		mw.scriptIcon,
	)
	mw.protocolPanel = container.NewHBox(newProtocolBadge(strings.TrimSpace(mw.connectedProtocol)))

	mountBtn, unmountBtn, _ := mw.diskWidget.GetButtons()
	mw.deviceMountBtn = mountBtn
	mw.deviceUnmountBtn = unmountBtn
	mw.deviceButtonsPanel = container.NewHBox()
	mw.refreshDeviceFooterButtons()
	mw.deviceButtonsPanel.Hide()
	mw.updateVideoIconLabel()

	return container.NewBorder(nil, nil, mw.deviceButtonsPanel, nil, nil)
}

func (mw *MainWindow) refreshDeviceFooterButtons() {
	if mw.deviceButtonsPanel == nil {
		return
	}

	buildGap := func() fyne.CanvasObject {
		gap := canvas.NewRectangle(color.Transparent)
		gap.SetMinSize(fyne.NewSize(16, 1))
		return gap
	}

	objects := make([]fyne.CanvasObject, 0, 5)

	if mw.deviceMountBtn != nil {
		objects = append(objects, mw.deviceMountBtn)
	}
	if mw.deviceUnmountBtn != nil {
		if len(objects) > 0 {
			objects = append(objects, buildGap())
		}
		objects = append(objects, mw.deviceUnmountBtn)
	}

	mw.deviceButtonsPanel.Objects = objects
	mw.deviceButtonsPanel.Refresh()
	if mw.deviceFooterBar != nil {
		mw.deviceFooterBar.Refresh()
	}
}

func buildHeaderStatusIndicators(backupIndicator, captureButton, audioButton, cdromButton, keyboardButton, mouseButton, rndisButton, gamepadButton, snapshotButton, scriptButton fyne.CanvasObject) []fyne.CanvasObject {
	return []fyne.CanvasObject{
		backupIndicator,
		captureButton,
		audioButton,
		cdromButton,
		keyboardButton,
		mouseButton,
		rndisButton,
		gamepadButton,
		snapshotButton,
		scriptButton,
	}
}

// updateDeviceButtonsVisibility updates the visibility of device buttons.
func (mw *MainWindow) updateDeviceButtonsVisibility() {
	if mw.tabs == nil || mw.deviceButtonsPanel == nil || mw.deviceFooterBar == nil {
		return
	}

	fyne.Do(func() {
		mw.refreshDeviceFooterButtons()
		if mw.tabs.SelectedIndex() == mw.devicesTabIndex() {
			mw.deviceButtonsPanel.Show()
			mw.deviceFooterBar.Show()
		} else {
			mw.deviceButtonsPanel.Hide()
			mw.deviceFooterBar.Hide()
		}
		mw.deviceButtonsPanel.Refresh()
		mw.deviceFooterBar.Refresh()
	})
}

// updateStatusBar updates the status panel.
func (mw *MainWindow) updateStatusBar() {
	if mw.usbClient == nil {
		mw.updateStatusBarUI(false, false, false, false, false, false, mw.videoWidget != nil && mw.videoWidget.IsStreaming(), false, false)
		return
	}

	go func() {
		client := mw.usbClient
		if client == nil {
			return
		}

		keyboardConnected := false
		mouseConnected := false
		rndisConnected := false
		cdromConnected := false
		backupConnected := false
		snapshotConnected := false
		gamepadConnected := false

		deviceInfo, err := client.GetDeviceInfo()
		if err == nil {
			for _, device := range deviceInfo.Devices {
				if device.Status == "connected" {
					if controller.IsKeyboardDeviceType(device.Type) {
						keyboardConnected = true
					}
					if controller.IsMouseDeviceType(device.Type) {
						mouseConnected = true
					}
					if controller.IsRNDISDeviceType(device.Type) {
						rndisConnected = true
					}
					if controller.IsGamepadDeviceType(device.Type) {
						gamepadConnected = true
					}
					if controller.IsStorageDeviceType(device.Type, device.Name) {
						cdromConnected = true
					}
					if controller.IsBackupDeviceType(device.Type, device.Name, device.ProductName) {
						backupConnected = true
					}
					if controller.IsSnapshotDeviceType(device.Type, device.Name, device.ProductName) {
						snapshotConnected = true
					}
				}
			}
		}

		if mw.backupWidget != nil && !snapshotConnected {
			snapshotsResp, err := client.GetSnapshots()
			if err == nil {
				for _, snapshot := range snapshotsResp.Snapshots {
					if snapshot.Connected {
						snapshotConnected = true
						break
					}
				}
			}
		}

		videoStreaming := mw.videoWidget != nil && mw.videoWidget.IsStreaming()
		audioStreaming := false
		if info, err := client.GetAudioInfo(); err == nil && info != nil {
			audioStreaming = info.Streaming
		}
		mw.updateStatusBarUI(keyboardConnected, mouseConnected, rndisConnected, cdromConnected, backupConnected, snapshotConnected, videoStreaming, gamepadConnected, audioStreaming)

		if scriptStatuses, err := client.GetScriptStatus(); err == nil {
			var runningPath, runningName string
			for _, st := range scriptStatuses {
				if st.Running {
					runningPath = st.Path
					runningName = filepath.Base(st.Path)
					break
				}
			}
			fyne.Do(func() {
				mw.runningScriptPath = runningPath
				mw.runningScriptName = runningName
				if mw.scriptIcon != nil {
					if runningPath != "" {
						mw.scriptIcon.Show()
					} else {
						mw.scriptIcon.Hide()
					}
					mw.scriptIcon.Refresh()
				}
			})
		}

		// Fetch detailed storage status
		storageStatus, err := client.GetStorageStatus()
		if err == nil {
			fyne.Do(func() {
				mw.storageStatus = storageStatus
				// Update the header progress bar with whichever storage
				// actually represents "the SD card" right now -- normally
				// the literal /mnt/sdcard figures, but when booted from the
				// SD slot itself (no separate card slot free), that's the
				// /mnt/emmc figures instead (this same card's own storage,
				// see StorageDisplayInfo's doc comment). Previously this
				// only ever checked SDCard.Total, so the header bar simply
				// never appeared at all while running from SD.
				display, _ := storageStatus.StorageDisplayInfo()
				if display.Total > 0 {
					mw.currentStorageTotal = display.Total
					mw.currentStorageAvailable = display.Free

					used := display.Used
					usedPct := display.Percent / 100 // view.ProgressBar expects 0..1

					if mw.sdStorageProgress != nil {
						mw.sdStorageProgress.SetIcon(assets.SDCardIcon)
						mw.sdStorageProgress.SetValue(usedPct)
						mw.sdStorageProgress.SetSizeText(models.FormatStorageSizeOnly(used, display.Total))
						mw.sdStorageProgress.Show()
						mw.refreshMainHeaderLayout()
					}
				}
			})
		}
	}()
}

func (mw *MainWindow) updateStatusBarUI(keyboardConnected, mouseConnected, rndisConnected, cdromConnected, backupConnected, snapshotConnected, videoStreaming, gamepadConnected, audioStreaming bool) {
	fyne.Do(func() {
		if mw.keyboardIcon != nil {
			if keyboardConnected {
				mw.keyboardIcon.SetIcon(assets.KeyboardIconActive)
				mw.keyboardIcon.Show()
			} else {
				mw.keyboardIcon.SetIcon(assets.KeyboardIcon)
				mw.keyboardIcon.Hide()
			}
			mw.keyboardIcon.Refresh()
		}
		if mw.mouseIcon != nil {
			if mouseConnected {
				mw.mouseIcon.SetIcon(assets.MouseIconActive)
				mw.mouseIcon.Show()
			} else {
				mw.mouseIcon.SetIcon(assets.MouseIcon)
				mw.mouseIcon.Hide()
			}
			mw.mouseIcon.Refresh()
		}
		if mw.videoIcon != nil {
			if videoStreaming {
				mw.videoIcon.SetIcon(assets.CameraIconActive)
				mw.videoIcon.Show()
			} else {
				mw.videoIcon.SetIcon(assets.CameraIcon)
				mw.videoIcon.Hide()
			}
			mw.videoIcon.Refresh()
		}
		if mw.audioIcon != nil {
			if audioStreaming {
				mw.audioIcon.SetIcon(assets.AudioIconActive)
				mw.audioIcon.Show()
			} else {
				mw.audioIcon.SetIcon(assets.AudioIcon)
				mw.audioIcon.Hide()
			}
			mw.audioIcon.Refresh()
		}
		if mw.cdromIcon != nil {
			if cdromConnected {
				mw.cdromIcon.SetIcon(assets.DiscIconActive)
				mw.cdromIcon.Show()
			} else {
				mw.cdromIcon.SetIcon(assets.DiscIcon)
				mw.cdromIcon.Hide()
			}
			mw.cdromIcon.Refresh()
		}
		if mw.rndisIcon != nil {
			if rndisConnected {
				mw.rndisIcon.SetIcon(assets.NetworkIconActive)
				mw.rndisIcon.Show()
			} else {
				mw.rndisIcon.SetIcon(assets.NetworkIcon)
				mw.rndisIcon.Hide()
			}
			mw.rndisIcon.Refresh()
		}
		if mw.gamepadIcon != nil {
			if gamepadConnected {
				mw.gamepadIcon.Show()
			} else {
				mw.gamepadIcon.Hide()
			}
			mw.gamepadIcon.Refresh()
		}
		if mw.backupIcon != nil {
			if backupConnected {
				mw.backupIcon.Show()
			} else {
				mw.backupIcon.Hide()
			}
			mw.backupIcon.Refresh()
		}
		if mw.snapshotIcon != nil {
			if snapshotConnected {
				mw.snapshotIcon.Show()
			} else {
				mw.snapshotIcon.Hide()
			}
			mw.snapshotIcon.Refresh()
		}

		if mw.statusPanel != nil {
			mw.statusPanel.Refresh()
		}
		if mw.protocolPanel != nil {
			mw.protocolPanel.Objects = []fyne.CanvasObject{
				newProtocolBadge(strings.TrimSpace(mw.connectedProtocol)),
			}
			mw.protocolPanel.Refresh()
		}
		mw.refreshMainHeaderLayout()
	})
}

func (mw *MainWindow) refreshMainHeaderLayout() {
	if mw == nil || mw.window == nil {
		return
	}
	if mw.sdStorageProgress != nil {
		mw.sdStorageProgress.Refresh()
	}
	if mw.statusPanel != nil {
		mw.statusPanel.Refresh()
	}
	if mw.protocolPanel != nil {
		mw.protocolPanel.Refresh()
	}
	if mw.mainContent != nil {
		mw.mainContent.Refresh()
	}
	if content := mw.window.Content(); content != nil {
		mw.window.Canvas().Refresh(content)
	}
}

// updateVideoIconLabel refreshes both badges on the video icon: the
// top-left video fps (unchanged) and the bottom-right detection fps --
// service.DetectionFPS's measured rate for the local ui.parse/AI Vision
// pipeline (see ai_vision.go's DetectionFPS doc comment), 0/hidden
// whenever AI Vision's live overlay is off or hasn't completed a pass yet.
// Both are driven from the same 1Hz callback (videoWidget.SetOnFPSChanged,
// see updateStats), so the two numbers refresh in lockstep.
func (mw *MainWindow) updateVideoIconLabel() {
	if mw.videoIcon == nil {
		return
	}

	label := ""
	if mw.currentVideoFPS > 0 {
		label = fmt.Sprintf("%.0f", math.Round(mw.currentVideoFPS))
	}

	detectionLabel := ""
	if detFPS := service.DetectionFPS(); detFPS > 0 {
		detectionLabel = fmt.Sprintf("%.1f", detFPS)
	}

	fyne.Do(func() {
		mw.videoIcon.SetBadgeText(label)
		mw.videoIcon.SetSecondaryBadgeText(detectionLabel)
	})
}

func (mw *MainWindow) showVideoMenu() {
	if mw.videoIcon == nil || mw.videoWidget == nil {
		return
	}

	items := []view.StyledMenuItem{
		{
			Label: i18n.Current.SettingsAction,
			OnTap: func() {
				mw.videoWidget.ShowCurrentVideoSettings(false)
			},
		},
	}

	if mw.videoWidget.IsStreaming() {
		items = append(items, view.StyledMenuItem{
			Label: i18n.Current.FullscreenAction,
			OnTap: func() {
				mw.videoWidget.ShowFullscreen()
			},
		})
	}

	view.ShowStyledMenu(mw.videoIcon, items)
}

func (mw *MainWindow) showAudioMenu() {
	if mw.audioIcon == nil {
		return
	}

	audioMuted := mw.isAudioMuted()
	audioLabel := i18n.Current.MuteAudio
	if audioMuted {
		audioLabel = i18n.Current.UnmuteAudio
	}

	items := []view.StyledMenuItem{
		{
			Label:    audioLabel,
			Selected: audioMuted,
			OnTap: func() {
				mw.toggleAudioMuted()
			},
		},
	}

	view.ShowStyledMenu(mw.audioIcon, items)
}

func (mw *MainWindow) isAudioMuted() bool {
	if ms, ok := mw.videoClient.(interface{ GetAudioMuted() bool }); ok {
		return ms.GetAudioMuted()
	}
	return false
}

func (mw *MainWindow) toggleAudioMuted() {
	ms, ok := mw.videoClient.(interface {
		SetAudioMuted(bool)
		GetAudioMuted() bool
	})
	if !ok {
		return
	}
	next := !ms.GetAudioMuted()
	ms.SetAudioMuted(next)
	mw.app.Preferences().SetBool("audio_muted", next)
	logrus.Infof("🔊 Audio muted=%v", next)
}

func (mw *MainWindow) controlTabIndex() int {
	return 0
}

func (mw *MainWindow) devicesTabIndex() int {
	return 1
}

func (mw *MainWindow) snapshotsTabIndex() int {
	return 2
}

func (mw *MainWindow) scriptsTabIndex() int {
	return 3
}

func (mw *MainWindow) showMouseModeMenu() {
	if mw.mouseIcon == nil || mw.diskWidget == nil {
		return
	}

	currentMode := mw.diskWidget.GetMouseMode()
	showMouse := mw.app.Preferences().BoolWithFallback("show_mouse_cursor", false)
	items := []view.StyledMenuItem{
		{
			Label:    i18n.Current.DeviceTouchPad,
			Selected: currentMode == controller.MouseModeTouchPad,
			OnTap: func() {
				mw.diskWidget.SetMouseMode(controller.MouseModeTouchPad)
			},
		},
		{
			Label:    i18n.Current.DeviceAbsolute,
			Selected: currentMode == controller.MouseModeAbsolute,
			OnTap: func() {
				mw.diskWidget.SetMouseMode(controller.MouseModeAbsolute)
			},
		},
	}
	if fyne.CurrentDevice().IsMobile() {
		items = append(items, view.StyledMenuItem{
			Label:    i18n.Current.DeviceVirtualCursor,
			Selected: currentMode == controller.MouseModeVirtualCursor,
			OnTap: func() {
				mw.diskWidget.SetMouseMode(controller.MouseModeVirtualCursor)
			},
		})
		items = append(items, view.StyledMenuItem{
			Label:    i18n.Current.DeviceGyroMouse,
			Selected: currentMode == controller.MouseModeGyroMouse,
			OnTap: func() {
				mw.diskWidget.SetMouseMode(controller.MouseModeGyroMouse)
			},
		})
	}
	items = append(items, view.StyledMenuItem{
		Label:    i18n.Current.ShowMouseCursor,
		Selected: showMouse,
		OnTap: func() {
			next := !mw.app.Preferences().BoolWithFallback("show_mouse_cursor", false)
			mw.app.Preferences().SetBool("show_mouse_cursor", next)
			if mw.videoWidget != nil {
				mw.videoWidget.SetShowMouseCursor(next)
			}
		},
	})
	items = append(items, view.StyledMenuItem{
		Label:    i18n.Current.ClipboardSyncEnabled,
		Selected: mw.app.Preferences().BoolWithFallback("clipboard_sync_enabled", true),
		OnTap: func() {
			next := !mw.app.Preferences().BoolWithFallback("clipboard_sync_enabled", true)
			mw.app.Preferences().SetBool("clipboard_sync_enabled", next)
			if mw.clipboardSync != nil {
				mw.clipboardSync.SetEnabled(next)
			}
		},
	})

	view.ShowStyledMenu(mw.mouseIcon, items)
}

func (mw *MainWindow) showRNDISModeMenu() {
	if mw.rndisIcon == nil || mw.diskWidget == nil {
		return
	}

	currentMode := mw.diskWidget.GetRNDISMode()
	items := []view.StyledMenuItem{
		{
			Label:    "auto",
			Selected: currentMode == "auto",
			OnTap: func() {
				mw.diskWidget.SetRNDISMode("auto")
			},
		},
		{
			Label:    "wifirouter",
			Selected: currentMode == "wifirouter",
			OnTap: func() {
				mw.diskWidget.SetRNDISMode("wifirouter")
			},
		},
		{
			Label:    "etherouter",
			Selected: currentMode == "etherouter",
			OnTap: func() {
				mw.diskWidget.SetRNDISMode("etherouter")
			},
		},
		{
			Label:    "etherbridge",
			Selected: currentMode == "etherbridge",
			OnTap: func() {
				mw.diskWidget.SetRNDISMode("etherbridge")
			},
		},
	}

	view.ShowStyledMenu(mw.rndisIcon, items)
}

func (mw *MainWindow) showScriptRunningMenu() {
	if mw.scriptIcon == nil || mw.runningScriptPath == "" {
		return
	}

	scriptPath := mw.runningScriptPath
	scriptName := mw.runningScriptName

	items := []view.StyledMenuItem{
		{
			Label: "Stop Script",
			OnTap: func() {
				if mw.usbClient != nil {
					go func() {
						if err := mw.usbClient.StopScript(scriptPath); err != nil {
							view.ShowErrorDialog(err, mw.window)
						}
					}()
				}
			},
		},
		{
			Label: "Log",
			OnTap: func() {
				view.ShowScriptLogDialog(mw.window, mw.usbClient, scriptPath, scriptName)
			},
		},
	}

	view.ShowStyledMenu(mw.scriptIcon, items)
}

func protocolButtonState(protocol string) (string, color.Color, color.Color) {
	switch protocol {
	case models.ConnectionProtocolTailscale:
		return "tailscale", design.ColorAccent, design.ColorBackground
	case "direct":
		return "online", design.ColorSurfaceLight, design.ColorTextLight
	case models.ConnectionProtocolAuto:
		return "online", design.ColorSurfaceLight, design.ColorTextLight
	default:
		return "online", design.ColorSurfaceLight, design.ColorTextLight
	}
}
