package gui

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"usbridge-client/internal/api"
	"usbridge-client/internal/clipboard"
	"usbridge-client/internal/gui/assets"
	"usbridge-client/internal/gui/controller"
	"usbridge-client/internal/gui/i18n"
	"usbridge-client/internal/gui/view"
	"usbridge-client/internal/models"
	"usbridge-client/internal/service"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/sirupsen/logrus"
)

// MainWindow is the main window of the application.
type MainWindow struct {
	app    fyne.App
	window fyne.Window

	// Widgets
	diskWidget          *controller.DiskWidget
	videoWidget         *controller.VideoWidget
	backupWidget        *controller.BackupWidget
	connectionManager   *controller.ConnectionManager
	mainContent         *fyne.Container
	connectionContent   *fyne.Container
	tabs                *container.AppTabs
	deviceButtonsPanel  *fyne.Container
	deviceFooterBar     *fyne.Container
	deviceMountBtn      fyne.CanvasObject
	deviceUnmountBtn    fyne.CanvasObject
	mainExitBtn         *view.HeaderActionButton
	connectionFooterBar *fyne.Container

	// Services
	nbdServer        *service.NBDServer
	videoClient      service.VideoClient
	usbClient        *api.USBClient
	tailscaleService *service.TailscaleService
	clipboardSync    *api.ClipboardSync

	// State
	config                   *models.AppConfig
	appState                 *models.AppState
	isConnected              bool
	isStreaming              bool
	activeAPISecret          []byte
	isConnectionPending      atomic.Bool
	isConnectionLoading      bool
	connectedProtocol        string
	pendingTailscaleRegister bool
	lastTailscaleAuthURL     string
	tailscalePollCancel      context.CancelFunc
	currentVideoFPS          float64
	currentStorageDir        string
	currentStorageTotal      int64
	currentStorageAvailable  int64
	storageStatus            *models.StorageStatusData

	// Connection/Disconnection button
	connectionBtn    *view.HeaderActionButton
	protocolSelect   *widget.Select
	protocolDropdown *view.HeaderDropdown

	// PC Panel (Power/Reset LED buttons)
	pcpanelWidget *controller.PCPanelWidget

	// Scripts tab (MCP Proxy + Automation Scripts)
	scriptsWidget *controller.ScriptsTabWidget

	// Address bar
	hostEntry           *widget.Entry
	tokenEntry          *widget.Entry
	sdStorageProgress   *view.StorageProgressBar
	deepLinkHandler     *DeepLinkHandler
	deepLinkMonitorStop chan struct{}

	lifecycleMu  sync.Mutex
	lifecycleOps chan func()

	// Status icons
	connectionIcon *widget.Button
	nbdIcon        *widget.Button
	videoIcon      *headerStatusBadgeButton
	audioIcon      *headerStatusBadgeButton
	captureIcon    *widget.Button
	keyboardIcon   *widget.Button
	mouseIcon      *widget.Button
	rndisIcon      *widget.Button
	gamepadIcon    *widget.Button
	cdromIcon      *widget.Button
	backupIcon          fyne.CanvasObject
	snapshotIcon        *widget.Button
	scriptIcon          *widget.Button
	runningScriptPath   string
	runningScriptName   string
	statusPanel         *fyne.Container
	protocolPanel       *fyne.Container

	connectionLossInProgress atomic.Bool
	shutdownInProgress       atomic.Bool
	isClosing                atomic.Bool
	// videoOverlayHiddenByNav tracks whether syncVideoOverlayForNav (see
	// its own doc comment) currently holds the native video overlay
	// hidden via view.NotifyOverlayShow/NotifyOverlayHide -- the single
	// authoritative flag for that pairing, read/written only from
	// syncVideoOverlayForNav itself. Only ever touched on the Fyne main
	// goroutine (every call site already runs inside fyne.Do or a tabs/
	// window callback), so no separate lock/atomic needed.
	videoOverlayHiddenByNav bool
	// audioMutedByNav tracks whether syncAudioMuteForNav (see its own doc
	// comment) force-muted audio for being on the connection-manager
	// screen. Only ever unmutes-by-nav if it was the one that muted --
	// never overrides an explicit user mute (toggleAudioMuted) set before
	// or during that screen.
	audioMutedByNav bool
	onReadyCallback          func()

	// lastGoodWindowSize/resizeGuardPending back the windowResizeGuard
	// workaround (main_window_resize_guard.go) for a real Windows-only
	// Fyne/GLFW bug: minimizing then restoring this window can snap it
	// down to its content's bare MinSize instead of its actual prior size.
	lastGoodWindowSize fyne.Size
	resizeGuardPending bool

	// onMainContent tracks which screen is showing (true: mainContent,
	// false: connectionContent) -- syncVideoOverlayForNav/syncAudioMuteForNav
	// used to tell this apart via `mw.window.Content() == mw.mainContent`,
	// which broke once SetContent started wrapping content in
	// wrapWithResizeGuard (window.Content() then returns that wrapper, never
	// mw.mainContent/mw.connectionContent themselves). Zero value (false)
	// matches the real startup order: connectionContent is shown first.
	onMainContent bool
}

func NewMainWindow(cfg *models.AppConfig) *MainWindow {
	if i18n.Current == nil {
		i18n.Init("en")
	}
	a := newFyneApp()
	w := a.NewWindow("USBridge Client")
	w.SetIcon(assets.AppIcon)
	w.SetPadded(false)

	mw := &MainWindow{
		app:    a,
		window: w,
		config: cfg,
		appState: &models.AppState{
			IsConnected: false,
		},
		lifecycleOps: make(chan func(), 32),
	}

	mw.nbdServer = service.NewNBDServer("127.0.0.1")
	mw.tailscaleService = service.NewTailscaleService()
	vc := newPlatformVideoClient(cfg)
	if ts, ok := vc.(interface{ SetTailscaleService(*service.TailscaleService) }); ok {
		ts.SetTailscaleService(mw.tailscaleService)
	}
	mw.videoClient = vc

	// Initialize UI fields
	mw.createInterface()

	// Initialize widgets
	mw.diskWidget = controller.NewDiskWidget(nil, mw.updateStatus, a, cfg)
	mw.diskWidget.SetWindow(w)
	mw.videoWidget = controller.NewVideoWidget(w, nil, mw.videoClient, mw.updateStatus)
	mw.videoWidget.SetShowMouseCursor(a.Preferences().BoolWithFallback("show_mouse_cursor", false))
	mw.videoWidget.SetTailscaleService(mw.tailscaleService)
	// See VideoWidget.HandleAppBackgrounded's doc comment (video_widget_android.go):
	// closes a real race where a stray frame from a connection attempt that
	// went stale while backgrounded (Android Doze/App Standby, especially
	// without the battery-usage exemption, throttles the underlying
	// network/goroutines much harder than the wall-clock timers racing
	// against them) pops the native video overlay up over whatever screen
	// the user is looking at after resuming. No-op on desktop.
	a.Lifecycle().SetOnExitedForeground(mw.videoWidget.HandleAppBackgrounded)
	a.Lifecycle().SetOnEnteredForeground(mw.videoWidget.HandleAppForegrounded)
	mw.diskWidget.SetMoonlightProvider(mw.videoWidget.GetMoonlightInput)
	mw.backupWidget = controller.NewBackupWidget(nil, mw.hostEntry, mw.updateStatus)
	mw.backupWidget.SetWindow(w)
	mw.pcpanelWidget = controller.NewPCPanelWidget(w)
	mw.scriptsWidget = controller.NewScriptsTabWidget(w)

	// Initialize connection manager
	mw.connectionManager = controller.NewConnectionManager(
		a, w, cfg,
		mw.hostEntry, mw.tokenEntry, mw.protocolSelect,
		mw.tailscaleService,
		mw.handleConnectionFromManager, mw.handleSelectionFromManager,
	)

	go mw.runLifecycleLoop()

	return mw
}

func maskSensitiveToken(token string) string {
	if len(token) <= 4 {
		return "****"
	}
	return token[:2] + "..." + token[len(token)-2:]
}

func fallbackText(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (mw *MainWindow) attachUSBClient(client *api.USBClient) *api.USBClient {
	if client == nil {
		return nil
	}

	client.SetOnTransportError(func(err error) {
		logrus.Errorf("📡 [Transport] Network error detected: %v", err)
		if mw.connectionLossInProgress.CompareAndSwap(false, true) {
			go mw.handleConnectionLost(err, client)
		}
	})

	if len(mw.activeAPISecret) > 0 {
		client.SetAPISecretV2(mw.activeAPISecret)
		// Keep Moonlight's PIN relay in sync so it uses the same HMAC key.
		if ms, ok := mw.videoClient.(interface{ SetAPISecret([]byte) }); ok {
			ms.SetAPISecret(mw.activeAPISecret)
		}
	}

	mw.startClipboardSync(client)

	return client
}

// startClipboardSync (re)starts clipboard sync against the newly attached
// client. Called from attachUSBClient, the common chokepoint every
// connection path (direct, tailscale, ...) routes through, so clipboard sync
// tracks whichever client connection is actually active.
func (mw *MainWindow) startClipboardSync(client *api.USBClient) {
	if mw.clipboardSync != nil {
		mw.clipboardSync.Stop()
		mw.clipboardSync = nil
	}
	if mw.config == nil || !mw.config.ClipboardSyncEnabled || client == nil {
		logrus.Infof("[clipboard-sync] not starting: config=%v enabled=%v client=%v",
			mw.config != nil, mw.config != nil && mw.config.ClipboardSyncEnabled, client != nil)
		return
	}
	enabled := mw.app.Preferences().BoolWithFallback("clipboard_sync_enabled", true)
	logrus.Infof("[clipboard-sync] starting (user-toggle enabled=%v, max_bytes=%d)", enabled, mw.config.ClipboardMaxBytes)
	manager := clipboard.NewManager(clipboard.NewBackend(mw.window), mw.config.ClipboardMaxBytes)
	manager.SetEnabled(enabled)
	mw.clipboardSync = api.NewClipboardSync(client, manager, mw.config.ClipboardMaxBytes)
	mw.clipboardSync.Start()
}
