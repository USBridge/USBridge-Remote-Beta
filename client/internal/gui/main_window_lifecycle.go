package gui

import (
	"time"

	"usbridge-client/internal/api"
	"usbridge-client/internal/gui/controller"
	"usbridge-client/internal/gui/i18n"
	"usbridge-client/internal/gui/view"

	"fyne.io/fyne/v2"
	"github.com/sirupsen/logrus"
)

// createBackupFlashTab creates the Backup Flash tab.
func (mw *MainWindow) createBackupFlashTab() fyne.CanvasObject {
	return mw.backupWidget.GetContainer()
}

// setupEventHandlers configures window event handlers.
func (mw *MainWindow) setupEventHandlers() {
	mw.window.SetCloseIntercept(func() {
		mw.handleClose()
	})
}

// handleHostChanged updates the current host.
func (mw *MainWindow) handleHostChanged(host string) {
	if host == "" {
		return
	}

	tempClient := api.NewUSBClient(host, mw.config.USBPort, mw.config.APITimeout)
	mw.videoClient.UpdateHost(host)
	mw.diskWidget.UpdateClient(tempClient)
	mw.videoWidget.UpdateClient(tempClient)
	if mw.backupWidget != nil {
		mw.backupWidget.UpdateClient(tempClient)
	}

	logrus.Infof("Host updated: %s", host)
}

// showConnectionManager displays the connection manager.
func (mw *MainWindow) showConnectionManager() {
	fyne.Do(func() {
		if mw.connectionContent == nil {
			logrus.Warn("showConnectionManager: connectionContent is nil")
			return
		}
		if mw.deviceButtonsPanel != nil {
			mw.deviceButtonsPanel.Hide()
		}
		// Refresh the list before making it visible so newly-resolved
		// Tailscale addresses (set by RememberResolvedTailscaleHost while
		// the main content was shown) are not stuck showing "TS: none".
		if mw.connectionManager != nil {
			mw.connectionManager.RefreshList()
		}
		mw.window.SetContent(mw.wrapWithResizeGuard(mw.connectionContent))
		mw.onMainContent = false
		mw.connectionContent.Refresh()
		mw.window.Canvas().Refresh(mw.connectionContent)
		mw.syncVideoOverlayForNav()
		mw.syncAudioMuteForNav()
	})
}

// showMainContent displays the main interface.
func (mw *MainWindow) showMainContent() {
	fyne.Do(func() {
		if mw.mainContent == nil {
			logrus.Warn("showMainContent: mainContent is nil")
			return
		}
		mw.window.SetContent(mw.wrapWithResizeGuard(mw.mainContent))
		mw.onMainContent = true
		mw.mainContent.Refresh()
		mw.window.Canvas().Refresh(mw.mainContent)
		mw.updateDeviceButtonsVisibility()
		mw.syncVideoOverlayForNav()
		mw.syncAudioMuteForNav()
	})
	mw.scheduleControlBootstrap()
}

// syncAudioMuteForNav force-mutes audio while the connection-manager screen
// is showing -- unlike the video overlay (Control tab only, see
// syncVideoOverlayForNav), audio is allowed on every tab of the main
// content, just not here. Never overrides an explicit user mute
// (toggleAudioMuted/the "audio_muted" preference): audioMutedByNav only
// unmutes on return if navigation itself was what muted it.
func (mw *MainWindow) syncAudioMuteForNav() {
	ms, ok := mw.videoClient.(interface {
		SetAudioMuted(bool)
		GetAudioMuted() bool
	})
	if !ok {
		return
	}
	onConnectionManager := !mw.onMainContent

	if onConnectionManager && !ms.GetAudioMuted() {
		ms.SetAudioMuted(true)
		mw.audioMutedByNav = true
	} else if !onConnectionManager && mw.audioMutedByNav {
		ms.SetAudioMuted(false)
		mw.audioMutedByNav = false
	}
}

// syncVideoOverlayForNav is the single authoritative place deciding whether
// the native video overlay (Vulkan/Metal — attached directly to the
// Activity/UIWindow, not to any Fyne window, see
// VideoWidget.HandleAppBackgrounded's doc comment) is allowed to be
// visible: only when the main content is showing AND the Control tab is
// the one selected -- never on the connection-manager screen, never on
// any other tab (Devices/Snapshots/Scripts).
//
// view.NotifyOverlayShow/NotifyOverlayHide are a depth counter (see
// overlay_hooks.go), not an idempotent boolean -- calling Show/Hide from
// more than one independently-tracked call site risks over- or
// under-counting it, which can leave the counter stuck above zero and the
// video permanently hidden even once every caller thinks it's back to
// "visible". mw.videoOverlayHiddenByNav is the one flag this whole file
// touches for that pairing, so every navigation transition (tab switch,
// connection-manager show/hide) MUST route through this function rather
// than calling NotifyOverlayShow/Hide directly.
func (mw *MainWindow) syncVideoOverlayForNav() {
	if mw.videoWidget == nil {
		return
	}
	shouldBeVisible := mw.tabs != nil &&
		mw.onMainContent &&
		mw.tabs.SelectedIndex() == mw.controlTabIndex()

	// Drift-proof nav signal for wasm (see view.NavVideoHidden's doc
	// comment): a plain last-write, set unconditionally on every call
	// regardless of the depth-counter dance below, so it can never get
	// stuck by a lost Show/Hide pairing the way overlayDepth can.
	view.SetNavVideoHidden(!shouldBeVisible)

	if !shouldBeVisible && !mw.videoOverlayHiddenByNav {
		view.NotifyOverlayShow()
		mw.videoOverlayHiddenByNav = true
	} else if shouldBeVisible && mw.videoOverlayHiddenByNav {
		view.NotifyOverlayHide()
		mw.videoOverlayHiddenByNav = false
	}
}

func (mw *MainWindow) scheduleControlBootstrap() {
	if mw.videoWidget == nil || mw.tabs == nil {
		return
	}

	runBootstrap := func(reason string) {
		if mw.videoWidget == nil || mw.tabs == nil {
			return
		}
		if mw.tabs.SelectedIndex() != mw.controlTabIndex() {
			return
		}
		logrus.Infof("🎬 Control bootstrap trigger: %s", reason)
		mw.videoWidget.BootstrapControlSessionAsync()
	}

	runBootstrap("main-content-immediate")
	time.AfterFunc(350*time.Millisecond, func() { runBootstrap("main-content-delayed-350ms") })
	time.AfterFunc(1200*time.Millisecond, func() { runBootstrap("main-content-delayed-1200ms") })
}

// handleClose handles app shutdown.
func (mw *MainWindow) handleClose() {
	if !mw.shutdownInProgress.CompareAndSwap(false, true) {
		logrus.Info("[shutdown] handleClose: shutdown already in progress")
		return
	}
	if !mw.isClosing.CompareAndSwap(false, true) {
		mw.shutdownInProgress.Store(false)
		return
	}

	mw.stopDeepLinkMonitoring()
	mw.enqueueLifecycleOp("app-close", func() {
		logrus.Infof("[shutdown] handleClose: entered connected=%v", mw.isConnected)

		if mw.videoWidget != nil && mw.videoWidget.ExitFullscreenIfNeeded() {
			logrus.Info("handleClose: fullscreen active, exit it first")
			mw.isClosing.Store(false)
			mw.shutdownInProgress.Store(false)
			return
		}

		needsDisconnect := mw.isConnected ||
			mw.usbClient != nil ||
			(mw.videoWidget != nil && mw.videoWidget.IsStreaming()) ||
			(mw.nbdServer != nil && mw.nbdServer.IsRunning())

		if needsDisconnect {
			logrus.Info("[shutdown] handleClose: calling handleDisconnect")
			mw.handleDisconnect()
		}

		fyne.Do(func() {
			if mw.app != nil {
				logrus.Info("[shutdown] handleClose: quitting app")
				mw.app.Quit()
				return
			}

			logrus.Info("[shutdown] handleClose: closing window")
			mw.window.SetCloseIntercept(nil)
			mw.window.Close()
		})
	})
}

// SetOnReadyCallback sets a callback invoked after the UI is fully initialized.
func (mw *MainWindow) SetOnReadyCallback(cb func()) {
	mw.onReadyCallback = cb
}

// Show displays the window.
func (mw *MainWindow) Show() {
	go func() {
		time.Sleep(200 * time.Millisecond)

		fyne.Do(func() {
			// createInterface and connectionManager creation moved to NewMainWindow
			mw.recreateContainers()
			mw.connectionManager.SetConnectionsStateCallback(mw.updateConnectionFooterVisibility)
			mw.setupEventHandlers()
			mw.setDefaultValues()
			mw.showConnectionManager()
			mw.applyInitialWindowSize()
			mw.updateStatusBar()
			mw.deepLinkHandler = NewDeepLinkHandler(mw.handleConnectionFromDeepLink, mw.handleSaveFromDeepLink)
			mw.checkDeepLink()
			mw.startDeepLinkMonitoring()
			mw.connectionManager.SetLanguageChangeCallback(mw.reloadUI)
			if mw.onReadyCallback != nil {
				go mw.onReadyCallback()
			}
		})
	}()

	mw.window.ShowAndRun()
}

// reloadUI reloads all UI with the current language.
func (mw *MainWindow) reloadUI() {
	logrus.Info("Reloading UI with new language...")

	currentHost := mw.hostEntry.Text
	currentToken := mw.tokenEntry.Text
	wasConnected := mw.isConnected

	// Re-initialize UI fields with new language
	mw.createInterface()
	mw.hostEntry.SetText(currentHost)
	mw.tokenEntry.SetText(currentToken)

	// Re-initialize widgets to refresh their localized strings
	mw.diskWidget = controller.NewDiskWidget(mw.usbClient, mw.updateStatus, mw.app, mw.config)
	mw.videoWidget = controller.NewVideoWidget(mw.window, mw.usbClient, mw.videoClient, mw.updateStatus)
	mw.videoWidget.SetShowMouseCursor(mw.app.Preferences().BoolWithFallback("show_mouse_cursor", false))
	mw.backupWidget = controller.NewBackupWidget(mw.usbClient, mw.hostEntry, mw.updateStatus)
	mw.pcpanelWidget = controller.NewPCPanelWidget(mw.window)
	mw.scriptsWidget = controller.NewScriptsTabWidget(mw.window)

	// Re-initialize connection manager
	mw.connectionManager = controller.NewConnectionManager(
		mw.app, mw.window, mw.config,
		mw.hostEntry, mw.tokenEntry, mw.protocolSelect,
		mw.tailscaleService,
		mw.handleConnectionFromManager, mw.handleSelectionFromManager,
	)
	mw.connectionManager.SetLanguageChangeCallback(mw.reloadUI)

	mw.recreateContainers()
	mw.connectionManager.SetConnectionsStateCallback(mw.updateConnectionFooterVisibility)
	mw.window.SetTitle(i18n.Current.AppTitle)

	if wasConnected {
		if mw.pcpanelWidget != nil && mw.usbClient != nil {
			mw.pcpanelWidget.SetClient(mw.usbClient)
		}
		if mw.scriptsWidget != nil && mw.usbClient != nil {
			mw.scriptsWidget.SetClient(mw.usbClient)
		}
		mw.showMainContent()
	} else {
		mw.showConnectionManager()
	}

	mw.applyInitialWindowSize()
	mw.updateStatusBar()
	logrus.Info("UI reloaded successfully")
}

// checkDeepLink checks for a deep link on app start.
func (mw *MainWindow) checkDeepLink() {
	if mw.deepLinkHandler != nil {
		mw.deepLinkHandler.CheckAndHandleDeepLink(mw.window)
	}
}

// startDeepLinkMonitoring starts background deep-link monitoring.
func (mw *MainWindow) startDeepLinkMonitoring() {
	mw.stopDeepLinkMonitoring()
	stopCh := make(chan struct{})
	mw.deepLinkMonitorStop = stopCh

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
			}
			if mw.deepLinkHandler != nil && !mw.isClosing.Load() {
				mw.deepLinkHandler.CheckAndHandleDeepLink(mw.window)
			}
		}
	}()
}

func (mw *MainWindow) stopDeepLinkMonitoring() {
	if mw.deepLinkMonitorStop == nil {
		return
	}

	close(mw.deepLinkMonitorStop)
	mw.deepLinkMonitorStop = nil
}
