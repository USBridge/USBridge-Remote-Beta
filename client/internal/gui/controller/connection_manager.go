package controller

import (
	"context"
	"fmt"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"usbridge-client/internal/gui/i18n"
	"usbridge-client/internal/gui/view"
	"usbridge-client/internal/models"
	"usbridge-client/internal/service"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"github.com/sirupsen/logrus"
)

func ternary(condition bool, a, b string) string {
	if condition {
		return a
	}
	return b
}

type SavedConnection struct {
	Name          string `json:"name"`
	InternalHost  string `json:"internal_host,omitempty"`
	TailscaleHost string `json:"tailscale_host,omitempty"`
	Host          string `json:"host,omitempty"`
	// MasterKey holds the API master secret (obtained by scanning the device QR code).
	// It is used to sign requests and perform the initial sync.
	MasterKey         string `json:"master_key"`
	Protocol          string `json:"protocol,omitempty"`
	TailscaleRegister bool   `json:"tailscale_register,omitempty"`
	RemoteOS          string `json:"remote_os,omitempty"`
}

type ConnectionManager struct {
	app    fyne.App
	window fyne.Window
	config *models.AppConfig
	ui     *view.ConnectionManagerUI

	connections           []SavedConnection
	selectedIndex         int
	connectionPending     bool
	activeConnectionIndex int
	syncingForm           bool

	// editingGridIndex is the connections slice index of the Grid-mode card
	// currently showing its inline edit layout (see
	// connection_grid_card.go's ConnectionRowState.Editing), or -1 when no
	// card is being edited. Grid and List each track their own edit target
	// independently (editingListIndex is List's) since the two view modes
	// can't both be showing at once anyway.
	editingGridIndex int
	// editingListIndex is the connections slice index of the List row
	// currently shown in the split-edit layout (see
	// connection_list_table.go's NewConnectionsListSplit and
	// connection_manager_list_edit.go's buildListEditPanel), or -1 when
	// List is showing its normal full table. The old modal editor
	// (showEditDialog) is unused now that List's pencil drives this instead.
	editingListIndex int

	// connectionSortMode drives the connections header's KVM/Agent badge
	// toggle (see connectionsDisplayOrder/handleConnectionSortToggle):
	// "" (default) leaves the list in creation-date order, "kvm"/"agent"
	// stably moves that category to the front without hiding anything else.
	connectionSortMode string

	hostEntry      *widget.Entry
	masterKeyEntry *widget.Entry
	protocolSelect *widget.Select

	qrScanner               *QRScanner
	ts                      *service.TailscaleService
	tsStatus                *service.TailscaleStatus
	tailscaleAuthInProgress atomic.Bool // guards against concurrent auth goroutines

	onConnect                func(host, masterKey, protocol string, tailscaleRegister bool)
	onSelect                 func(tailscaleRegister bool)
	onLanguageChange         func()
	onConnectionsStateChange func(bool)
	tsPollStop               chan struct{}

	// tsStatusSink pushes raw Tailscale status text into the connection
	// header's toggle (see gui.ConnectionHeaderHandle.SetTailscaleState).
	// Set once by MainWindow after it builds that header -- this package
	// never references the header's own type, only this callback shape, so
	// there's no import cycle back to package gui.
	tsStatusSink func(status, authLabel string)

	// accountStateSink pushes the account login state into the connection
	// header's avatar button (see gui.ConnectionHeaderHandle.SetAccountState)
	// -- same wiring shape as tsStatusSink above, fired both once up front
	// (SetAccountStateSink) and again on every login/logout (see the
	// AccountManager onChange callback in NewConnectionManager).
	accountStateSink func(loggedIn bool, email string)

	// Account owns the account login + sync passphrase this connections
	// list is end-to-end synced under -- see account_manager.go and
	// connection_manager_sync.go. nil is a valid state (no account
	// features wired up, e.g. some future embedded/CI build): every sync
	// call site checks for it.
	Account *AccountManager

	// syncMu guards the fields below -- see connection_manager_sync.go.
	syncMu        sync.Mutex
	syncVersion   int // last version this device knows the backend to be at; 0 = never successfully synced
	syncPushTimer *time.Timer
	syncLastError string
}

func (cm *ConnectionManager) ResolveMasterKey(host, currentMasterKey string) string {
	masterKey := strings.TrimSpace(currentMasterKey)
	if masterKey != "" {
		return masterKey
	}

	normalizedHost := strings.TrimSpace(host)
	if normalizedHost == "" {
		return ""
	}

	if cm.selectedIndex >= 0 && cm.selectedIndex < len(cm.connections) {
		conn := cm.connections[cm.selectedIndex]
		internalHost, tailscaleHost := classifyConnectionHosts(conn)
		if strings.TrimSpace(conn.Host) == normalizedHost ||
			internalHost == normalizedHost ||
			tailscaleHost == normalizedHost {
			return strings.TrimSpace(conn.MasterKey)
		}
	}

	return ""
}

func (cm *ConnectionManager) ResolveInternalHost(host string) string {
	normalizedHost := strings.TrimSpace(host)
	if normalizedHost == "" {
		return ""
	}

	for _, conn := range cm.connections {
		internalHost, tailscaleHost := classifyConnectionHosts(conn)
		if internalHost == "" {
			continue
		}
		if strings.TrimSpace(conn.Host) == normalizedHost || tailscaleHost == normalizedHost || internalHost == normalizedHost {
			return internalHost
		}
	}
	return ""
}

// ResolveTailscaleHost returns the stored Tailscale IP/hostname for a given host key.
// Used by connectTailscale when the current host is an internal LAN address.
func (cm *ConnectionManager) ResolveTailscaleHost(host string) string {
	normalizedHost := strings.TrimSpace(host)
	if normalizedHost == "" {
		return ""
	}
	for _, conn := range cm.connections {
		internalHost, tailscaleHost := classifyConnectionHosts(conn)
		if tailscaleHost == "" {
			continue
		}
		if strings.TrimSpace(conn.Host) == normalizedHost || internalHost == normalizedHost || tailscaleHost == normalizedHost {
			return tailscaleHost
		}
	}
	return ""
}

func NewConnectionManager(app fyne.App, window fyne.Window, config *models.AppConfig, hostEntry, masterKeyEntry *widget.Entry, protocolSelect *widget.Select, ts *service.TailscaleService, onConnect func(host, masterKey, protocol string, tailscaleRegister bool), onSelect func(tailscaleRegister bool)) *ConnectionManager {
	cm := &ConnectionManager{
		app:                   app,
		window:                window,
		config:                config,
		hostEntry:             hostEntry,
		masterKeyEntry:        masterKeyEntry,
		protocolSelect:        protocolSelect,
		onConnect:             onConnect,
		onSelect:              onSelect,
		selectedIndex:         -1,
		connections:           make([]SavedConnection, 0),
		activeConnectionIndex: -1,
		editingGridIndex:      -1,
		editingListIndex:      -1,
		ts:                    ts,
	}
	if cm.ts == nil {
		cm.ts = service.NewTailscaleService()
	}
	// Centralizes "open the login link in a browser": tsnet can produce an
	// AuthURL from any first touch of the server (WarmUpPeer, WaitUntilReady,
	// HTTPClient, TailnetIPv4 — not just the explicit Sign-In button), and
	// only tsnet's own internal auto-login attempts it once per server
	// lifetime. Keying the open off "a genuinely new URL appeared" — rather
	// than each caller racing its own poll loop against Status() — is what
	// makes the login reliably surface instead of sometimes silently timing
	// out with nothing ever opened.
	cm.ts.SetAuthURLHandler(func(authURL string) {
		if runtime.GOOS == "android" {
			// Android already opens it via the JNI opener inside setLatestAuthURL —
			// calling openExternalLink too would pop a second browser/intent.
			return
		}
		cm.setTailscaleStateAsync(
			"Tailscale: auth URL received",
			"Google: opening browser",
			authURL,
			"Sign In With Google",
		)
		cm.openExternalLink(authURL, "Tailscale login URL")
	})

	cm.qrScanner = NewQRScanner(
		app,
		func(host, masterKey, protocol string, tailscaleRegister bool) {
			fyne.Do(func() {
				cm.ClearSelection()
				cm.applyConnectionToForm(host, masterKey, protocol)
			})
			if cm.onConnect != nil {
				cm.onConnect(host, masterKey, protocol, tailscaleRegister)
			}
			logrus.Infof("QR connect: host=%s", host)
		},
		func(name, internalHost, tailscaleHost, masterKey, protocol string, tailscaleRegister bool) {
			cm.SaveConnection(name, internalHost, tailscaleHost, masterKey, protocol, tailscaleRegister)
			fyne.Do(func() {
				cm.applyConnectionToForm(resolveScannedHost(protocol, internalHost, tailscaleHost), masterKey, protocol)
			})
			logrus.Infof("QR saved directly: internal=%s tailscale=%s", internalHost, tailscaleHost)
		},
		func(internalHost, tailscaleHost, masterKey, protocol string, scanned bool) {
			cm.showPrefilledAddDialog("", internalHost, tailscaleHost, masterKey, protocol, scanned, false)
		},
	)

	cm.loadConnections()
	cm.createInterface()
	cm.startTailscaleStatusPolling()

	cm.Account = NewAccountManager(app, func() {
		// Fires on every login/passphrase/logout change -- cheap to call
		// unconditionally (trySyncPullAndMerge no-ops the instant sync
		// credentials aren't both set yet) and is exactly the moment a
		// fresh set of credentials becomes available worth reconciling
		// against, e.g. right after SetSyncPassphrase on a second device.
		go cm.trySyncPullAndMerge()
		// Also keep the header avatar's teal/letter state in sync with
		// every login/logout -- not just passphrase changes, which don't
		// affect it, but cheap enough not to bother filtering.
		cm.notifyAccountState()
	})
	go cm.trySyncPullAndMerge()
	return cm
}

// startTailscaleLogin handles a tap on the toggle while it's off. The intent
// is fixed at "get me connected" — it must never fall through to a logout,
// even if resuming tsnet's persisted session (below) happens to land it in a
// LoggedIn state by the time the check runs.
func (cm *ConnectionManager) startTailscaleLogin() {
	if cm.ts == nil {
		return
	}
	if !cm.tailscaleAuthInProgress.CompareAndSwap(false, true) {
		logrus.Info("tailscale client ui: auth action already in progress, ignoring duplicate click")
		return
	}
	go func() {
		defer cm.tailscaleAuthInProgress.Store(false)

		// Show the spinner immediately on tap. The resume attempt below
		// (Start + WaitUntilReady, up to 8s) previously ran silently before
		// any state update reached the UI, so the toggle looked dead/unresponsive
		// for up to 8 seconds — as if the tap had done nothing — until this
		// same "starting login" state finally got set afterwards.
		cm.setTailscaleStateAsync(
			"Tailscale: checking saved session",
			"Google: connecting",
			"Address: unavailable",
			"Sign In With Google",
		)

		// Status() reports a default "not logged in, not running" result
		// whenever the tsnet server hasn't been explicitly started yet — and
		// this button, unlike Connect (which starts tsnet via
		// WaitUntilReady/HTTPClient before ever checking status), could
		// previously be the very first thing to touch tsnet. That made it
		// look like there was never a saved session, so it always fell
		// through to StartLogin/StartLoginInteractive and forced a brand new
		// browser sign-in — even when a valid Tailscale session was already
		// persisted on disk from a previous run. Start tsnet and give it a
		// moment to resume that persisted session first, exactly like
		// Connect does, so this button only prompts for a fresh login when
		// one is actually needed.
		if err := cm.ts.Start(context.Background()); err != nil {
			logrus.WithError(err).Warn("tailscale client ui: Start failed")
		}
		warmCtx, warmCancel := context.WithTimeout(context.Background(), 8*time.Second)
		_ = cm.ts.WaitUntilReady(warmCtx)
		warmCancel()

		status, err := cm.ts.Status(context.Background())
		if err == nil && status != nil && status.LoggedIn {
			// The persisted session was resumed successfully — already
			// connected, nothing more to do. This must NOT trigger a
			// logout: that was a real regression where resuming a valid
			// session right here made it look, one line down, like the
			// user had asked to sign out.
			logrus.Info("tailscale client ui: login button pressed — session already resumed, nothing to do")
			cm.refreshTailscaleStatus()
			return
		}

		cm.setTailscaleStateAsync(
			"Tailscale: starting login",
			"Google: waiting for browser sign-in",
			"Address: unavailable until login completes",
			"Sign In With Google",
		)
		logrus.Info("tailscale client ui: login button pressed")
		// The actual "open the login link in a browser" happens in the
		// AuthURLHandler registered in NewConnectionManager, once tsnet
		// actually produces a URL — not off this call's return value, since
		// tsnet may have already silently started (and even completed) the
		// interactive login via some earlier, unrelated call (WarmUpPeer,
		// WaitUntilReady, ...) before this button was ever clicked.
		if _, err := cm.ts.StartLogin(context.Background()); err != nil {
			logrus.WithError(err).Error("tailscale client ui: StartLogin failed")
			cm.setTailscaleStateAsync(
				"Tailscale: login failed",
				fmt.Sprintf("Google: %v", err),
				"Address: unavailable",
				"Sign In With Google",
			)
		}
		cm.refreshTailscaleStatus()
	}()
}

// startTailscaleLogout handles a tap on the toggle while it's on, after the
// user has confirmed the sign-out dialog. Intent is fixed at "disconnect" —
// unlike startTailscaleLogin, it never re-derives what to do from a status
// check.
func (cm *ConnectionManager) startTailscaleLogout() {
	if cm.ts == nil {
		return
	}
	if !cm.tailscaleAuthInProgress.CompareAndSwap(false, true) {
		logrus.Info("tailscale client ui: auth action already in progress, ignoring duplicate click")
		return
	}
	go func() {
		defer cm.tailscaleAuthInProgress.Store(false)

		logrus.Info("tailscale client ui: logout button pressed")
		cm.setTailscaleStateAsync(
			"Tailscale: signing out",
			"Google: disconnecting account",
			"Address: unavailable",
			"Sign Out",
		)
		if logoutErr := cm.ts.Logout(context.Background()); logoutErr != nil {
			logrus.WithError(logoutErr).Error("tailscale client ui: Logout failed")
		}
		cm.refreshTailscaleStatus()
	}()
}

func (cm *ConnectionManager) handleTailscaleToggleAction() {
	if cm.tsStatus != nil && cm.tsStatus.LoggedIn {
		view.ShowConfirmYesLeft(
			i18n.Current.Confirmation,
			i18n.Current.TailscaleLogoutConfirm,
			func(confirmed bool) {
				if confirmed {
					cm.startTailscaleLogout()
				}
			},
			cm.window,
		)
	} else {
		cm.startTailscaleLogin()
	}
}

func (cm *ConnectionManager) SelectConnection(idx int) {
	if idx < 0 || idx >= len(cm.connections) {
		cm.selectedIndex = -1
		return
	}
	cm.selectedIndex = idx
	conn := cm.connections[idx]
	cm.applyConnectionToForm(cm.resolveHostForProtocol(conn, conn.Protocol), conn.MasterKey, conn.Protocol)

	if cm.onSelect != nil {
		cm.onSelect(conn.TailscaleRegister)
	}
}

func (cm *ConnectionManager) applyConnectionToForm(host, masterKey, protocol string) {
	cm.syncingForm = true
	defer func() {
		cm.syncingForm = false
	}()

	if cm.hostEntry != nil {
		cm.hostEntry.SetText(strings.TrimSpace(host))
	}
	if cm.masterKeyEntry != nil {
		cm.masterKeyEntry.SetText(strings.TrimSpace(masterKey))
	}
	if cm.protocolSelect != nil {
		cm.protocolSelect.SetSelected(normalizeConnectionProtocol(protocol))
	}
}

func (cm *ConnectionManager) HandleFormEdited(host, masterKey, protocol string) bool {
	if cm == nil || cm.syncingForm {
		return false
	}
	if cm.selectedIndex < 0 {
		return true // Selection already cleared
	}

	host = strings.TrimSpace(host)
	masterKey = strings.TrimSpace(masterKey)
	protocol = normalizeConnectionProtocol(protocol)

	current := cm.connections[cm.selectedIndex]
	if strings.TrimSpace(current.Host) == host &&
		strings.TrimSpace(current.MasterKey) == masterKey &&
		normalizeConnectionProtocol(current.Protocol) == protocol {
		return false
	}

	cm.selectedIndex = -1
	return true
}

func (cm *ConnectionManager) ClearSelection() {
	if cm == nil {
		return
	}
	cm.selectedIndex = -1
}

func (cm *ConnectionManager) SetConnectionsStateCallback(cb func(bool)) {
	cm.onConnectionsStateChange = cb
}

func (cm *ConnectionManager) SetLanguageChangeCallback(cb func()) {
	cm.onLanguageChange = cb
}

func (cm *ConnectionManager) SetConnectionPending(pending bool) {
	if cm == nil {
		return
	}
	cm.connectionPending = pending
	activeIndex := cm.selectedIndex
	if cm.selectedIndex < 0 || cm.selectedIndex >= len(cm.connections) {
		activeIndex = -1
	}
	cm.setConnectionPendingState(pending, activeIndex)
}

func (cm *ConnectionManager) setConnectionPendingState(pending bool, activeIndex int) {
	cm.connectionPending = pending
	cm.activeConnectionIndex = activeIndex

	if cm.ui != nil {
		fyne.Do(func() {
			cm.ui.SetActionButtonsDisabled(pending)
			cm.refreshConnectionsList()
		})
	}
}

func (cm *ConnectionManager) resolveHostForProtocol(conn SavedConnection, protocol string) string {
	switch normalizeConnectionProtocol(protocol) {
	case models.ConnectionProtocolTailscale:
		if strings.TrimSpace(conn.TailscaleHost) != "" {
			return strings.TrimSpace(conn.TailscaleHost)
		}
		return strings.TrimSpace(conn.InternalHost)
	case models.ConnectionProtocolDirect:
		if strings.TrimSpace(conn.InternalHost) != "" {
			return strings.TrimSpace(conn.InternalHost)
		}
		return strings.TrimSpace(conn.TailscaleHost)
	default:
		if strings.TrimSpace(conn.InternalHost) != "" {
			return strings.TrimSpace(conn.InternalHost)
		}
		return strings.TrimSpace(conn.TailscaleHost)
	}
}

func normalizeConnectionProtocol(protocol string) string {
	if runtime.GOOS == "js" {
		// No embedded tsnet in a browser tab (tailscale_service_wasm.go is
		// a stub, same reasoning as newConnectionHeader's own Tailscale-toggle
		// omission on wasm, in package gui) -- always dial over plain LAN, regardless of
		// what a connection saved on a native client set this to, or what
		// a stale saved value in localStorage says. Single choke point:
		// every caller (resolveHostForProtocol, connectionProtocolBadge,
		// ...) goes through this, so the badge always reads "LAN" too.
		return models.ConnectionProtocolDirect
	}
	switch strings.TrimSpace(protocol) {
	case models.ConnectionProtocolTailscale:
		return models.ConnectionProtocolTailscale
	case models.ConnectionProtocolDirect:
		return models.ConnectionProtocolDirect
	default:
		return models.ConnectionProtocolAuto
	}
}

func classifyConnectionHosts(conn SavedConnection) (internalHost, tailscaleHost string) {
	internalHost = strings.TrimSpace(conn.InternalHost)
	tailscaleHost = strings.TrimSpace(conn.TailscaleHost)
	return internalHost, tailscaleHost
}

func splitHostByType(raw string) (internalHost, tailscaleHost string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}

	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '|' || r == ',' || r == ';' || r == '\n'
	})
	if len(parts) == 0 {
		return "", ""
	}

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if isLikelyTailnetHost(p) {
			if tailscaleHost == "" {
				tailscaleHost = p
			}
		} else {
			if internalHost == "" {
				internalHost = p
			}
		}
	}
	return internalHost, tailscaleHost
}

func (cm *ConnectionManager) OpenDiscordInvite() {
	cm.openDiscordInvite()
}

func (cm *ConnectionManager) OpenInfoPage() {
	cm.openInfoPage()
}

// SetTailscaleStatusSink registers where live Tailscale status text goes --
// normally the connection header's toggle, wired up once by MainWindow right
// after it builds that header (see connection_header.go's
// ConnectionHeaderHandle.SetTailscaleState).
func (cm *ConnectionManager) SetTailscaleStatusSink(sink func(status, authLabel string)) {
	cm.tsStatusSink = sink
}

// SetAccountStateSink registers where live account login state goes --
// normally the connection header's avatar button, wired up once by
// MainWindow right after it builds that header (see connection_header.go's
// ConnectionHeaderHandle.SetAccountState). Pushes the current state right
// away too, so an already-logged-in account (persisted from a previous
// session) shows correctly from the first frame instead of waiting for the
// next login/logout event.
func (cm *ConnectionManager) SetAccountStateSink(sink func(loggedIn bool, email string)) {
	cm.accountStateSink = sink
	// Off the main goroutine: this runs during MainWindow construction,
	// before the Fyne event loop is pumping, and notifyAccountState calls
	// fyne.Do -- which errors if invoked directly from the main goroutine
	// at that point (same reasoning as initTailscaleMode's own comment).
	go cm.notifyAccountState()
}

// notifyAccountState pushes the account manager's current login state into
// accountStateSink, if one is registered.
func (cm *ConnectionManager) notifyAccountState() {
	if cm.accountStateSink == nil || cm.Account == nil {
		return
	}
	loggedIn := cm.Account.LoggedIn()
	email := cm.Account.Email()
	fyne.Do(func() {
		cm.accountStateSink(loggedIn, email)
	})
}

// ToggleTailscale runs the same sign-in/sign-out flow the connection
// header's Tailscale toggle triggers on tap.
func (cm *ConnectionManager) ToggleTailscale() {
	cm.handleTailscaleToggleAction()
}

func (cm *ConnectionManager) startTailscaleStatusPolling() {
	if cm.tsPollStop != nil {
		close(cm.tsPollStop)
	}
	cm.tsPollStop = make(chan struct{})

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-cm.tsPollStop:
				return
			case <-ticker.C:
				cm.refreshTailscaleStatus()
			}
		}
	}()
}

func (cm *ConnectionManager) refreshTailscaleStatus() {
	if cm.ts == nil {
		return
	}
	if cm.tailscaleAuthInProgress.Load() {
		// A login/logout action is actively driving the toggle's state right
		// now (spinner, "checking saved session", "starting login", ...).
		// This function also runs off a 5s background ticker, and used to
		// stomp over that in-flight state with whatever tsnet's status
		// happened to be mid-transition (e.g. still NeedsLogin a moment
		// before StartLogin's AuthURL arrives) — flipping the spinner back
		// to a plain toggle for a second or two before the browser opened.
		// The auth goroutine itself calls refreshTailscaleStatus once it's
		// actually done, so skipping here just avoids the race.
		return
	}
	status, err := cm.ts.Status(context.Background())
	if err != nil {
		cm.setTailscaleStateAsync(
			"Tailscale: status unavailable",
			fmt.Sprintf("Error: %v", err),
			"Address: unavailable",
			"Sign In With Google",
		)
		return
	}
	cm.tsStatus = status

	address := "unavailable"
	if status.Self.IP4 != "" {
		address = status.Self.IP4
	} else if status.Self.DNSName != "" {
		address = status.Self.DNSName
	}
	loginText := "disconnected"
	if status.Self.UserLogin != "" {
		loginText = status.Self.UserLogin
	} else if status.LoggedIn {
		loginText = "connected"
	}

	// Use raw state string from backend (Running, NeedsLogin, etc.) for easier recognition in View
	header := fmt.Sprintf("Tailscale: %s", status.Backend)
	if status.Backend == "" {
		header = "Tailscale: status available"
	}

	cm.setTailscaleStateAsync(
		header,
		fmt.Sprintf("Google: %s", loginText),
		fmt.Sprintf("Address: %s (%s)", address, ternary(status.Userspace, "embedded", "system")),
		ternary(status.LoggedIn, "Sign Out", "Sign In With Google"),
	)
}

func (cm *ConnectionManager) setTailscaleStateAsync(header, subHeader, addr, button string) {
	if cm.tsStatusSink == nil {
		return
	}
	fyne.Do(func() {
		cm.tsStatusSink(header, button)
	})
}

func (cm *ConnectionManager) notifyConnectionsState() {
	if cm.onConnectionsStateChange != nil {
		cm.onConnectionsStateChange(len(cm.connections) > 0)
	}
}

func (cm *ConnectionManager) beginConnectionFromRow(idx int) bool {
	if cm.connectionPending {
		return false
	}
	cm.connectionPending = true
	cm.activeConnectionIndex = idx
	if cm.ui != nil {
		fyne.Do(func() {
			cm.ui.SetActionButtonsDisabled(true)
			cm.refreshConnectionsList()
		})
	}
	return true
}

func connectionProtocolBadge(protocol string) string {
	switch normalizeConnectionProtocol(protocol) {
	case models.ConnectionProtocolTailscale:
		return "TS"
	case models.ConnectionProtocolDirect:
		return "LAN"
	default:
		return "AUTO"
	}
}

func connectionProtocolFromBadge(badge string) string {
	switch strings.ToUpper(strings.TrimSpace(badge)) {
	case "TS":
		return models.ConnectionProtocolTailscale
	case "LAN":
		return models.ConnectionProtocolDirect
	default:
		return models.ConnectionProtocolAuto
	}
}

func isLikelyTailnetHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return strings.HasSuffix(host, ".ts.net") || strings.HasPrefix(host, "100.")
}

func (cm *ConnectionManager) GetContainer() fyne.CanvasObject {
	if cm == nil || cm.ui == nil {
		return nil
	}
	return cm.ui.Container
}

func (cm *ConnectionManager) OpenQuickStartDocs() {
	cm.openQuickStartDocs()
}

func (cm *ConnectionManager) openExternalLink(rawURL string, label string) {
	uri, err := url.Parse(rawURL)
	if err != nil {
		logrus.Errorf("failed to parse %s %q: %v", label, rawURL, err)
		return
	}

	fyneApp := cm.app
	if fyneApp == nil {
		fyneApp = fyne.CurrentApp()
	}
	if fyneApp == nil {
		logrus.Errorf("failed to open %s: fyne app is nil", label)
		return
	}

	go func() {
		if err := fyneApp.OpenURL(uri); err != nil {
			logrus.Errorf("failed to open %s %q: %v", label, rawURL, err)
		}
	}()
}
