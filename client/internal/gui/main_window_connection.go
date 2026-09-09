package gui

import (
	"context"
	"fmt"
	"image/color"
	"net"
	"net/url"
	"strings"
	"time"

	"usbridge-client/internal/api"
	"usbridge-client/internal/gui/assets"
	"usbridge-client/internal/gui/design"
	"usbridge-client/internal/gui/i18n"
	"usbridge-client/internal/gui/view"
	"usbridge-client/internal/models"

	"fyne.io/fyne/v2"
	"github.com/sirupsen/logrus"
)

func (mw *MainWindow) handleSelectionFromManager(tailscaleRegister bool) {
	mw.pendingTailscaleRegister = tailscaleRegister
}

// handleConnectionFromDeepLink handles the deep-link connect callback.
func (mw *MainWindow) handleConnectionFromDeepLink(host, masterKey, protocol string, tailscaleRegister bool) {
	mw.handleConnectionFromManager(host, masterKey, protocol, tailscaleRegister)
}

// handleConnectionFromManager handles connection from the manager (arrow on the card).
// masterKey is the API secret (from QR sync).
func (mw *MainWindow) handleConnectionFromManager(host, masterKey, protocol string, tailscaleRegister bool) {
	setForm := func() {
		mw.hostEntry.SetText(host)
		mw.tokenEntry.SetText(masterKey)
		if protocol != "" {
			mw.protocolSelect.SetSelected(protocol)
		}
	}
	// Silently, via connectionManager -- for the OnUse (Grid/List card)
	// caller, cm.SelectConnection(idx) already just populated these same
	// entries under its own syncingForm guard; setting them again here
	// unguarded fires OnChanged -> HandleFormEdited, which (before this fix)
	// compared against a value the form was never actually populated with
	// and wrongly cleared cm.selectedIndex mid-connect -- see
	// HandleFormEdited's and SetFormTextSilently's doc comments for the
	// full chain (it also desyncs SetConnectionPending's redundant-call
	// activeIndex, which is what made the toast/button flicker).
	if mw.connectionManager != nil {
		mw.connectionManager.SetFormTextSilently(setForm)
	} else {
		setForm()
	}
	mw.pendingTailscaleRegister = tailscaleRegister
	mw.handleConnectionToggle()
}

// handleSaveFromDeepLink saves data from a deep link WITHOUT connecting.
func (mw *MainWindow) handleSaveFromDeepLink(name, internalHost, tailscaleHost, masterKey, protocol string, tailscaleRegister bool) {
	host := strings.TrimSpace(tailscaleHost)
	if host == "" {
		host = strings.TrimSpace(internalHost)
	}
	logrus.Infof("💾 handleSaveFromDeepLink: name='%s' internal='%s' tailscale='%s' masterKey='%s' protocol='%s' register=%v", name, internalHost, tailscaleHost, maskSensitiveToken(masterKey), protocol, tailscaleRegister)

	fyne.Do(func() {
		mw.hostEntry.SetText(host)
		mw.tokenEntry.SetText(masterKey)
		if protocol != "" {
			mw.protocolSelect.SetSelected(protocol)
		}
	})

	if mw.connectionManager != nil {
		generatedName := mw.connectionManager.SaveConnection(name, internalHost, tailscaleHost, masterKey, protocol, tailscaleRegister)
		logrus.Infof("✅ Connection '%s' saved", generatedName)
		fyne.Do(func() {
			logrus.Infof("💾 Saved as: %s", generatedName)
		})
	} else {
		logrus.Warn("⚠️ ConnectionManager is not initialized")
	}
}

func (mw *MainWindow) canAttemptConnection() bool {
	return strings.TrimSpace(mw.hostEntry.Text) != ""
}

func (mw *MainWindow) setConnectionLoading(loading bool) {
	mw.isConnectionLoading = loading
	mw.refreshConnectionControls()
}

// connectingToastBarDuration paces the connecting toast's progress bar --
// deliberately NOT mw.config.APITimeout: that bounds only the first network
// call inside doConnect, while several earlier steps (tsnet's own 25s
// WaitUntilReady waits, sync, Tailscale registration polling) run on their
// own separate timeouts and can make the real wall-clock attempt take
// noticeably longer than APITimeout before anything actually resolves. The
// bar reaching 100% doesn't close the toast (handleConnectingStateChange
// only closes it once the real attempt resolves) -- it just gives a sense
// of pace for a typical attempt without pretending to know the real one.
const connectingToastBarDuration = 15 * time.Second

// handleConnectingStateChange is ConnectionManager's connectingStateSink --
// wired up once in createConnectionAddressBar (main_window_layout.go),
// alongside the header's other cross-package status sinks. Shows/hides the
// bottom "Connecting to X…" toast (view.ShowConnectingToast) in lockstep
// with connectionPending's own start/stop, so it tracks a Connect press
// regardless of which button started it (Grid card, List row, or a saved
// deep link) without any of those call sites needing to know about the
// toast themselves.
func (mw *MainWindow) handleConnectingStateChange(connecting bool, name string) {
	fyne.Do(func() {
		if !connecting && mw.suppressConnectingToastClose {
			// A connect failure just called ShowError on this same toast
			// (see handleConnectFailure) -- leave it open instead of
			// closing it out from under that transform.
			mw.suppressConnectingToastClose = false
			return
		}

		if mw.connectingToast != nil {
			logrus.Infof("🔌 [CONNECT-TOAST] closing (connecting=%v name=%q)", connecting, name)
			mw.connectingToast.Close()
			mw.connectingToast = nil
		}
		if !connecting {
			return
		}

		logrus.Infof("🔌 [CONNECT-TOAST] showing (name=%q)", name)
		message := fmt.Sprintf(i18n.Current.ConnectingToConnection, name)
		mw.connectingToast = view.ShowConnectingToast(message, connectingToastBarDuration, mw.window)
	})
}

func (mw *MainWindow) clearConnectionPending() {
	mw.isConnectionPending.Store(false)
	mw.isConnectionLoading = false
	mw.pendingTailscaleRegister = false
	if mw.connectionManager != nil {
		mw.connectionManager.SetConnectionPending(false)
	}
}

func (mw *MainWindow) resolveConnectionToken(host, masterKey string) string {
	resolved := strings.TrimSpace(masterKey)
	if resolved != "" {
		return resolved
	}

	if mw.connectionManager != nil {
		resolved = mw.connectionManager.ResolveMasterKey(host, masterKey)
		if resolved != "" {
			logrus.Infof("🔍 [DEBUG] Resolved master key from saved connection for host='%s'", host)
			return resolved
		}
	}

	return ""
}

func (mw *MainWindow) resolveBootstrapHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if mw.connectionManager != nil {
		if resolved := strings.TrimSpace(mw.connectionManager.ResolveInternalHost(host)); resolved != "" {
			return resolved
		}
	}
	if !strings.HasSuffix(strings.ToLower(host), ".ts.net") {
		return host
	}
	return ""
}

// connectionRecoveryRetryDelays is the backoff schedule
// tryRecoverConnectionAfterLoss waits through after a transport loss before
// giving up and tearing down the connection.
//
// This must comfortably outlast a real Tailscale/tsnet re-establishment
// after a client-side network path change (Wi-Fi<->cellular handoff, AP
// roam, DHCP renewal): observed field logs on Android show the netcheck
// (probing every DERP region) plus WireGuard re-handshake taking 15-30s
// after such a blip. The previous {1,2,5}s schedule (~8s budget) gave up
// while tsnet was still mid-reconnect, surfacing every transient network
// hiccup as a full "connection lost" dialog requiring the user to manually
// reconnect -- see the investigation this schedule was widened for.
var connectionRecoveryRetryDelays = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	15 * time.Second,
	20 * time.Second,
}

func (mw *MainWindow) tryRecoverConnectionAfterLoss(client *api.USBClient, lastErr error) bool {
	if client == nil || client != mw.usbClient || !mw.isConnected {
		return false
	}

	protocol := mw.connectedProtocol
	if protocol == "" {
		protocol = models.ConnectionProtocolAuto
	}

	logrus.Infof("🔄 Attempting automatic connection recovery for host=%s protocol=%s", mw.hostEntry.Text, protocol)

	retryDelays := connectionRecoveryRetryDelays
	for attempt, delay := range retryDelays {
		if !mw.isConnected || client != mw.usbClient || mw.isClosing.Load() {
			return false
		}

		select {
		case <-time.After(delay):
		}

		fyne.Do(func() {
			mw.isConnectionPending.Store(true)
			mw.isConnectionLoading = true
			mw.refreshConnectionControls()
			mw.hostEntry.Disable()
			mw.tokenEntry.Disable()
			mw.protocolSelect.Disable()
		})

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(mw.config.APITimeout)*time.Second)
		err := mw.doConnectWithProtocol(ctx, mw.hostEntry.Text, protocol)
		cancel()
		if err == nil {
			return true
		}
		logrus.Warnf("⚠️ Recovery attempt %d/%d failed: %v", attempt+1, len(retryDelays), err)
	}

	return false
}

func (mw *MainWindow) handleConnectionLost(err error, client *api.USBClient) {
	if mw.isClosing.Load() {
		mw.connectionLossInProgress.Store(false)
		return
	}
	if client != nil && client != mw.usbClient {
		mw.connectionLossInProgress.Store(false)
		return
	}

	if mw.tryRecoverConnectionAfterLoss(client, err) {
		logrus.Infof("✅ Connection recovered automatically after transport loss")
		mw.connectionLossInProgress.Store(false)
		return
	}

	logrus.Errorf("❌ Connection lost, tearing down local state: %v", err)
	mw.cleanupDeadConnectionState()

	fyne.Do(func() {
		mw.clearConnectionPending()
		mw.refreshConnectionControls()
		mw.hostEntry.Enable()
		mw.tokenEntry.Enable()
		mw.protocolSelect.Enable()
		mw.updateStatus()
		mw.showConnectionManager()
		view.ShowConnectionErrorDialog(fmt.Errorf(i18n.Current.ConnectionLost, err), mw.window)
	})

	mw.connectionLossInProgress.Store(false)
}

func (mw *MainWindow) cleanupDeadConnectionState() {
	mw.isConnected = false
	mw.isStreaming = false

	if mw.videoWidget != nil {
		mw.videoWidget.HandleConnectionLost()
	}

	mw.usbClient = nil
}

func isLikelyTailscaleAuthKey(token string) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	return strings.HasPrefix(token, "tskey-")
}

func isLikelyTailscaleHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	return host != "" && (strings.HasSuffix(host, ".ts.net") || strings.HasPrefix(host, "100."))
}

func splitBridgeAuthInputs(raw string) (deviceToken, tailscaleAuthKey string) {
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

	deviceToken = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		tailscaleAuthKey = strings.TrimSpace(parts[1])
	}
	return deviceToken, tailscaleAuthKey
}

func (mw *MainWindow) resolveBridgeAuthInputs(host, masterKey string) (deviceToken, tailscaleAuthKey string) {
	deviceToken, tailscaleAuthKey = splitBridgeAuthInputs(masterKey)
	if deviceToken == "" {
		deviceToken = mw.resolveConnectionToken(host, "")
	}
	return deviceToken, tailscaleAuthKey
}

func (mw *MainWindow) handleConnectionToggle() {
	if mw.isConnectionPending.Load() {
		logrus.Warn("⚠️ A connect/disconnect operation is already in progress, ignoring repeated press")
		return
	}

	if mw.isConnected {
		// Immediately provide visual feedback
		if mw.mainExitBtn != nil {
			mw.mainExitBtn.ApplySpec(view.HeaderActionButtonSpec{
				Disabled:      true,
				Fill:          design.ColorSurfaceLight,
				Foreground:    design.ColorTextLight,
				Stroke:        color.NRGBA{R: 0xd6, G: 0x6d, B: 0x6d, A: 0xff},
				StrokeWidth:   1.2,
				SpinnerFrames: assets.LoadingGrayFrames,
			})
		}
		mw.isConnectionPending.Store(true)
		mw.refreshConnectionControls()
		mw.enqueueLifecycleOp("disconnect", func() {
			mw.handleDisconnect()
		})
		return
	}

	if !mw.canAttemptConnection() {
		return
	}

	mw.isConnectionPending.Store(true)
	mw.setConnectionLoading(true)
	mw.hostEntry.Disable()
	mw.tokenEntry.Disable()
	mw.protocolSelect.Disable()

	go mw.handleConnect()
}

// handleConnect handles connecting
func (mw *MainWindow) handleConnect() {
	logrus.Infof("🔍 [DEBUG] handleConnect() called")

	host := mw.hostEntry.Text
	masterKey := mw.tokenEntry.Text

	if host == "" {
		logrus.Warn("Enter a server address")
		mw.clearConnectionPending()
		mw.refreshConnectionControls()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(mw.config.APITimeout)*time.Second)
	defer cancel()

	if err := mw.doConnect(ctx, host, masterKey); err != nil {
		mw.handleConnectFailure("Connection failed", err)
	}
}

// testConnectionWithRetry wraps TestConnectionWithContext with retry logic for
// transient "no route to host" errors. On macOS these can occur briefly when
// the system network stack is reconfiguring (e.g. WiFi handoff, DNS change).
func testConnectionWithRetry(ctx context.Context, client *api.USBClient, host string) error {
	const maxAttempts = 4
	const retryDelay = 700 * time.Millisecond
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastErr = client.TestConnectionWithContext(ctx)
		if lastErr == nil {
			return nil
		}
		if !api.IsConnectionLostError(lastErr) {
			return lastErr
		}
		if attempt < maxAttempts {
			logrus.Warnf("⚠️ [CONNECT] Direct connection to %s failed (attempt %d/%d): %v — retrying in %.0fs",
				host, attempt, maxAttempts, lastErr, retryDelay.Seconds())
			select {
			case <-time.After(retryDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return lastErr
}

// getFreeVideoUDPPort finds an available UDP port dynamically
func getFreeVideoUDPPort() int {
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		return models.DefaultVideoUDPPort
	}
	l, err := net.ListenUDP("udp", addr)
	if err != nil {
		return models.DefaultVideoUDPPort
	}
	port := l.LocalAddr().(*net.UDPAddr).Port
	l.Close()
	return port
}

// doConnect performs the blocking connection logic (called from a goroutine).
// masterKey — API master secret (from the QR code): used for sync and to sign API requests.
func (mw *MainWindow) doConnect(ctx context.Context, host, masterKey string) error {
	mw.lastTailscaleAuthURL = ""

	selectedProtocol := mw.protocolSelect.Selected

	// The tsnet (userspace Tailscale) WireGuard stack initialization briefly
	// disrupts the OS network routing table on some platforms, causing even LAN
	// connections to fail with EHOSTUNREACH. Wait for tsnet to reach Running
	// state before making any network calls. WaitUntilReady returns immediately
	// when already Running.
	//
	// Only do this when the target actually needs Tailscale (a tailnet-looking
	// host, or the user explicitly picked the "tailscale" protocol) — an
	// unqualified call here would call tsnet's Up() on every direct/LAN connect
	// attempt, including before the user has ever pressed the Tailscale button,
	// which triggers an unauthenticated tsnet login and pops an auth browser
	// window on top of the app.
	if usesTsnetTransport() && mw.tailscaleService != nil &&
		(isLikelyTailscaleHost(host) || selectedProtocol == models.ConnectionProtocolTailscale) {
		// Deliberately not derived from ctx (which is bounded by the short
		// APITimeout meant for the actual API calls below): tsnet coming up
		// cold — especially first-ever interactive login — can take well
		// longer than that. Carving this wait out of ctx's budget left
		// little or nothing for the connect attempt that follows, so the
		// first press after enabling Tailscale would fail with a
		// "context deadline exceeded"-style error even though tsnet itself
		// was still fine — a second press then worked because tsnet was
		// already Running by then.
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 25*time.Second)
		if waitErr := mw.tailscaleService.WaitUntilReady(waitCtx); waitErr != nil {
			logrus.Warnf("⚠️ [CONNECT] tsnet not yet ready: %v (proceeding anyway)", waitErr)
		}
		waitCancel()
	}

	if mw.videoWidget != nil {
		_ = mw.videoWidget.StopVideoSync()
	}
	if mw.videoClient != nil {
		_ = mw.videoClient.Disconnect()
	}

	mw.config.VideoUDPPort = getFreeVideoUDPPort()
	if mw.videoClient != nil {
		mw.videoClient.UpdateVideoPort(mw.config.VideoUDPPort)
		mw.videoClient.UpdateVideoUDPPort(mw.config.VideoUDPPort)
	}

	protocol := mw.protocolSelect.Selected
	if protocol == "" {
		protocol = models.ConnectionProtocolAuto
	}

	if strings.TrimSpace(masterKey) != "" {
		// Master key — perform QR sync.
		key := strings.TrimSpace(masterKey)
		if strings.HasPrefix(key, "usbridge://sync") {
			// Full deep-link URI passed via bar (e.g., manual paste).
			u, _ := url.Parse(key)
			if u != nil {
				if s := u.Query().Get("secret"); s != "" {
					key = s
				}
			}
		}
		// On Android userspace Tailscale (tsnet), wait for the node to be
		// online before sending the sync request to a Tailscale host.
		// tsnet.Up() blocks until Running state (~4s on first launch).
		if usesTsnetTransport() && isLikelyTailscaleHost(host) && mw.tailscaleService != nil {
			logrus.Info("🛰️ [SYNC] Waiting for Tailscale to be ready...")
			// Own budget, not ctx (see the identical rationale above) — otherwise
			// this wait alone can exhaust the API timeout, leaving the sync
			// request below to fail immediately with a deadline-exceeded error.
			waitCtx, waitCancel := context.WithTimeout(context.Background(), 25*time.Second)
			waitErr := mw.tailscaleService.WaitUntilReady(waitCtx)
			waitCancel()
			if waitErr != nil {
				logrus.Warnf("🛰️ [SYNC] Tailscale not ready: %v (proceeding anyway)", waitErr)
			} else {
				logrus.Info("🛰️ [SYNC] Tailscale ready")
			}
		}

		if tsReady, err := mw.syncWithBridgeV2(ctx, host, key); err == nil {
			// When the user wants Tailscale registration but the bridge is not yet
			// in the tailnet (no auth key was sent), fall back to Auto or Direct
			// so that registration can proceed over the current connection.
			if mw.pendingTailscaleRegister && !tsReady {
				if !isLikelyTailscaleHost(host) {
					logrus.Infof("🛰️ [CONNECT] Bridge not in Tailscale yet; staying on direct LAN for this session to finish registration")
					protocol = "direct"
				} else if protocol == models.ConnectionProtocolTailscale {
					logrus.Infof("🛰️ [CONNECT] Bridge not in Tailscale yet; switching protocol tailscale→auto for this attempt")
					protocol = models.ConnectionProtocolAuto
				}
			}
		} else {
			logrus.Warnf("⚠️ [SYNC] Sync failed: %v", err)
			// For direct and auto protocols, sync failure is not fatal —
			// mw.activeAPISecret was already set at the start of
			// syncWithBridgeV2 (before the network call), so HMAC auth still
			// works via attachUSBClient. Tailscale protocol still requires sync
			// to resolve the Tailscale IP.
			if protocol == models.ConnectionProtocolTailscale {
				return fmt.Errorf("sync failed: %w", err)
			}
			logrus.Infof("🔗 [CONNECT] Sync failed but protocol=%s — proceeding without sync", protocol)
		}
	} else {
		logrus.Warn("⚠️ [CONNECT] No master key provided")
	}

	logrus.Infof("🔗 [CONNECT] start host=%s protocol=%s timeout=%ds",
		strings.TrimSpace(host), protocol, mw.config.APITimeout)

	if mw.pendingTailscaleRegister && tailscaleRegisterSupported() {
		mw.pollTailscaleRegistration(host, masterKey, protocol)
	}

	return mw.doConnectWithProtocol(ctx, host, protocol)
}

func (mw *MainWindow) pollTailscaleRegistration(host, masterKey, protocol string) {
	// Cancel any previous poll goroutine before starting a new one.
	if mw.tailscalePollCancel != nil {
		mw.tailscalePollCancel()
		mw.tailscalePollCancel = nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	mw.tailscalePollCancel = cancel

	go func() {
		defer func() {
			cancel()
		}()

		logrus.Infof("🛰️ [TS] Starting Tailscale registration polling for host=%s", host)
		time.Sleep(3 * time.Second)

		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			// Pass tailscaleRegister=true so the bridge runs 'tailscale up --json'
			// and returns an AuthURL when not yet logged in.
			syncCtx, syncCancel := context.WithTimeout(ctx, 25*time.Second)
			tsReady, err := mw.syncWithBridgeV2(syncCtx, host, masterKey, true)
			syncCancel()

			if err == nil && tsReady {
				logrus.Infof("🛰️ [TS] Bridge registered in Tailscale for host=%s", host)
				if protocol != models.ConnectionProtocolDirect {
					logrus.Infof("🛰️ [TS] Reconnecting via Tailscale")
					mw.reconnectViaTailscaleAfterRegistration(host, masterKey)
				} else {
					logrus.Infof("🛰️ [TS] LAN protocol selected, skipping Tailscale reconnect")
				}
				return
			}

			if err != nil {
				logrus.Debugf("🛰️ [TS] Poll sync failed for host=%s: %v", host, err)
			}

			select {
			case <-ctx.Done():
				logrus.Warnf("🛰️ [TS] Tailscale registration polling timed out for host=%s", host)
				return
			case <-ticker.C:
			}
		}
	}()
}

func (mw *MainWindow) reconnectViaTailscaleAfterRegistration(host, masterKey string) {
	if !mw.isConnected {
		// Not connected right now — next manual connect will pick up the saved Tailscale IP.
		return
	}
	if mw.connectedProtocol == models.ConnectionProtocolTailscale {
		// Already on Tailscale — no need to switch.
		logrus.Infof("🛰️ [TS] Bridge in Tailscale but already connected via Tailscale — skipping reconnect")
		return
	}

	capturedHost := host
	capturedKey := masterKey

	// Disconnect from LAN bootstrap session then reconnect via Tailscale.
	fyne.Do(func() {
		mw.enqueueLifecycleOp("tailscale-switch", func() {
			logrus.Infof("🛰️ [TS] Disconnecting LAN bootstrap session to reconnect via Tailscale")
			mw.handleDisconnect()

			// Give background cleanup time before initiating the reconnect.
			time.Sleep(2 * time.Second)

			fyne.Do(func() {
				mw.handleConnectionFromManager(capturedHost, capturedKey, models.ConnectionProtocolTailscale, false)
			})
		})
	})
}

func (mw *MainWindow) doConnectWithProtocol(ctx context.Context, host, protocol string) error {
	connectTailscale := func(ctx context.Context) error {
		resolvedHost := strings.TrimSpace(host)

		// If the current host is not a Tailscale address (e.g. it's a LAN IP from QR scan),
		// look up the Tailscale IP stored by a previous sync for this connection.
		if !isLikelyTailscaleHost(resolvedHost) && mw.connectionManager != nil {
			if tsHost := mw.connectionManager.ResolveTailscaleHost(resolvedHost); tsHost != "" {
				logrus.Infof("🔍 [TS] Resolved tailscale host %s for internal %s", tsHost, resolvedHost)
				resolvedHost = tsHost
			}
		}

		if !isLikelyTailscaleHost(resolvedHost) {
			return fmt.Errorf("no tailscale address available for bridge (do a fresh sync to register)")
		}

		if resolvedHost == "" || !isLikelyTailscaleHost(resolvedHost) {
			return fmt.Errorf("no tailscale address available for bridge (do a fresh sync to register)")
		}

		// dialTailscaleTarget is platform-split: tsnet on desktop/Android
		// (main_window_connection_tailscale_default.go), a plain direct HTTP
		// dial in the browser (main_window_connection_tailscale_wasm.go —
		// wasm has no tsnet to speak of, a Tailscale address is just an
		// ordinary reachable hostname there).
		client, err := mw.dialTailscaleTarget(ctx, resolvedHost)
		if err != nil {
			return fmt.Errorf("tailscale connect failed: %w", err)
		}

		mw.usbClient = mw.attachUSBClient(client)
		mw.videoClient.UpdateHost(resolvedHost)
		mw.connectedProtocol = models.ConnectionProtocolTailscale
		mw.videoWidget.SetTailscaleService(mw.tailscaleService)
		mw.videoWidget.SetTailscaleVideoEnabled(true)
		return nil
	}

	logrus.Infof("🔗 [CONNECT] protocol=%s host=%s", protocol, host)

	switch protocol {
	case models.ConnectionProtocolTailscale:
		if err := connectTailscale(ctx); err != nil {
			return err
		}
	case models.ConnectionProtocolAuto:
		if err := connectTailscale(ctx); err != nil {
			logrus.Warnf("⚠️ Tailscale auto-connect failed, falling back to direct: %v", err)
			tempClient := api.NewDirectUSBClient(host, mw.config.USBPort, mw.config.APITimeout)
			if err2 := testConnectionWithRetry(ctx, tempClient, host); err2 != nil {
				return fmt.Errorf("failed to establish connection in auto mode: %w", err2)
			}
			mw.usbClient = mw.attachUSBClient(tempClient)
			mw.videoClient.UpdateHost(host)
			mw.connectedProtocol = models.ConnectionProtocolDirect
			mw.videoWidget.SetTailscaleVideoEnabled(false)
		}
	case models.ConnectionProtocolDirect:
		tempClient := api.NewDirectUSBClient(host, mw.config.USBPort, mw.config.APITimeout)
		if err := testConnectionWithRetry(ctx, tempClient, host); err != nil {
			return err
		}
		mw.usbClient = mw.attachUSBClient(tempClient)
		mw.videoClient.UpdateHost(host)
		mw.connectedProtocol = models.ConnectionProtocolDirect
		mw.videoWidget.SetTailscaleVideoEnabled(false)
	default:
		tempClient := api.NewUSBClient(host, mw.config.USBPort, mw.config.APITimeout)
		if err := tempClient.TestConnectionWithContext(ctx); err != nil {
			return err
		}
		mw.usbClient = mw.attachUSBClient(tempClient)
		mw.videoClient.UpdateHost(host)
		if isLikelyTailscaleHost(host) {
			mw.connectedProtocol = models.ConnectionProtocolTailscale
		} else {
			mw.connectedProtocol = models.ConnectionProtocolDirect
			mw.videoWidget.SetTailscaleVideoEnabled(false)
		}
	}

	if err := mw.verifyActiveConnectionWithContext(ctx); err != nil {
		logrus.Errorf("❌ Connection verification failed: %v", err)
		mw.usbClient = nil
		fyne.Do(func() {
			mw.clearConnectionPending()
			mw.isConnected = false
			mw.connectedProtocol = ""
			mw.refreshConnectionControls()
			mw.hostEntry.Enable()
			mw.tokenEntry.Enable()
			mw.protocolSelect.Enable()
		})
		return fmt.Errorf("connection verification failed: %w", err)
	}

	mw.diskWidget.UpdateClient(mw.usbClient)
	mw.videoWidget.UpdateClient(mw.usbClient)
	if mw.backupWidget != nil {
		mw.backupWidget.UpdateClient(mw.usbClient)
	}

	mw.isConnected = true
	mw.appState.IsConnected = true
	mw.appState.LastConnected = time.Now()
	mw.connectionLossInProgress.Store(false)

	fyne.Do(func() {
		mw.clearConnectionPending()
		mw.refreshConnectionControls()
		if mw.pcpanelWidget != nil {
			mw.pcpanelWidget.SetClient(mw.usbClient)
		}
		if mw.scriptsWidget != nil {
			mw.scriptsWidget.SetClient(mw.usbClient)
		}
		mw.updateStatus()
		mw.showMainContent()
		if mw.videoWidget != nil {
			mw.videoWidget.ShowVirtualKeyboardIfMobile()
			// showMainContent()'s SetContent() doesn't necessarily finish
			// cascading a real layout pass down to the touchpad wrapper
			// synchronously within this same callback -- confirmed live
			// (via device screenshot) that opening the keyboard panel
			// right here, immediately after SetContent, can leave the
			// video widget laid out against a stale/default size (a tiny
			// video with a large unclaimed gap below it, instead of
			// filling the Control tab's actual available height). Same
			// root cause and same fix already established for the
			// tab-switch case in main_window_layout.go's own
			// time.AfterFunc(150ms, RefreshViewportGeometry) — give the
			// layout a moment to settle, then force the geometry to be
			// recomputed against whatever the touchpad wrapper's real,
			// final size turned out to be.
			time.AfterFunc(200*time.Millisecond, mw.videoWidget.RefreshViewportGeometry)
			time.AfterFunc(800*time.Millisecond, mw.videoWidget.RefreshViewportGeometry)
		}
	})

	logrus.Infof("✅ Connected to USBridge via %s", mw.connectedProtocol)

	if mw.usbClient != nil && mw.connectionManager != nil {
		client := mw.usbClient
		connMgr := mw.connectionManager
		connHost := strings.TrimSpace(host)
		go func() {
			deviceInfo, err := client.GetDeviceInfo()
			if err == nil && deviceInfo != nil {
				osName := strings.TrimSpace(deviceInfo.AgentOS)
				if osName != "" {
					connMgr.UpdateConnectionOS(connHost, osName)
					return
				}
			}
			status, err := client.GetStatus()
			if err != nil || status == nil || status.Data == nil {
				return
			}
			osName := strings.TrimSpace(status.Data.OS)
			if osName != "" {
				connMgr.UpdateConnectionOS(connHost, osName)
			}
		}()
	}

	return nil
}

func (mw *MainWindow) verifyActiveConnectionWithContext(ctx context.Context) error {
	if mw.usbClient == nil {
		return fmt.Errorf("usb client is not initialized")
	}

	// Deliberately NOT TestConnectionWithContext: that hits /api/healthz,
	// which the agent registers as sec.Public (see
	// agent/internal/api/security.go's isPublicPath) -- it proves the host
	// is reachable, not that mw.activeAPISecret is the right master key.
	// A wrong key's master-sync call earlier already gets a real 401, but
	// doConnect treats that as non-fatal for direct/auto protocols (the
	// secret is still wired into mw.usbClient via attachUSBClient, in case
	// sync merely had a transient hiccup on an otherwise-correct key) -- so
	// this was the only remaining gate before the client declared itself
	// connected, and reusing the public endpoint here meant a bad password
	// sailed straight through it: the app would show "connected" while
	// every actually-authenticated call (screen, PC panel, disk, scripts)
	// kept silently failing with 401. GetDeviceInfo requires a valid HMAC
	// signature, so a wrong key fails right here instead.
	_, err := mw.usbClient.GetDeviceInfoWithContext(ctx)
	return err
}

func (mw *MainWindow) verifyActiveConnection() error {
	return mw.verifyActiveConnectionWithContext(context.Background())
}

func (mw *MainWindow) handleConnectFailure(message string, err error) {
	logrus.Errorf("%s: %v", message, err)
	fyne.Do(func() {
		// If the "Connecting to X…" toast is up for this attempt, keep it
		// open through clearConnectionPending (which would otherwise close
		// it via handleConnectingStateChange) so the error below can
		// transform that same toast in place instead of closing it and
		// popping a separate dialog on top. Not when the app is closing --
		// there's no error to show then, so let the toast close normally
		// instead of leaving it open with nothing left to transform it.
		closing := mw.isClosing.Load()
		toast := mw.connectingToast
		mw.suppressConnectingToastClose = !closing && toast != nil

		mw.clearConnectionPending()
		mw.isConnected = false
		mw.connectedProtocol = ""
		mw.refreshConnectionControls()
		mw.hostEntry.Enable()
		mw.tokenEntry.Enable()
		mw.protocolSelect.Enable()
		if closing {
			return
		}

		fullErr := fmt.Errorf("%s: %w", message, err)
		if toast != nil {
			toast.ShowError(fullErr.Error())
		} else {
			view.ShowConnectionErrorDialog(fullErr, mw.window)
		}
	})
}

// handleDisconnect handles disconnecting
func (mw *MainWindow) handleDisconnect() {
	logrus.Infof("[shutdown] handleDisconnect: start connected=%v", mw.isConnected)

	// Copy references for background cleanup
	client := mw.usbClient
	video := mw.videoWidget
	backup := mw.backupWidget
	nbd := mw.nbdServer
	diskWidget := mw.diskWidget

	// Must happen synchronously, before anything else: a pending
	// scheduleControlBootstrap timer (main_window_lifecycle.go, fires on a
	// schedule tied to which tab is visible, not to user intent) can land in
	// the gap between now and when the background cleanup goroutine below
	// gets around to stopping video, silently reconnecting it right after
	// this disconnect. Setting the flag from inside that goroutine was too
	// late to reliably win that race.
	if video != nil {
		video.MarkUserStopped()
	}

	// Stop any running Tailscale registration poll goroutine.
	if mw.tailscalePollCancel != nil {
		mw.tailscalePollCancel()
		mw.tailscalePollCancel = nil
	}

	if mw.clipboardSync != nil {
		mw.clipboardSync.Stop()
		mw.clipboardSync = nil
	}

	// 1. Immediately reset the state
	mw.isConnected = false
	mw.isStreaming = false
	mw.connectedProtocol = ""
	mw.appState.IsConnected = false
	mw.appState.IsStreaming = false
	mw.appState.IsNBDRunning = false
	mw.connectionLossInProgress.Store(false)
	mw.appState.LastDisconnected = time.Now()

	// 2. Immediately update the UI (go back to the login screen)
	fyne.Do(func() {
		mw.showConnectionManager()
		if mw.mainExitBtn != nil {
			mw.mainExitBtn.ApplySpec(view.HeaderActionButtonSpec{
				Fill:        design.ColorSurfaceLight,
				Foreground:  design.ColorTextLight,
				Stroke:      color.NRGBA{R: 0xd6, G: 0x6d, B: 0x6d, A: 0xff},
				StrokeWidth: 1.2,
				Icon:        assets.ExitIcon,
				IconSize:    fyne.NewSize(24, 24),
			})
		}

		if mw.diskWidget != nil {
			mw.diskWidget.UpdateClient(nil)
		}
		if video != nil {
			video.UpdateClient(nil)
		}
		if backup != nil {
			backup.UpdateClient(nil)
		}

		mw.usbClient = nil

		mw.clearConnectionPending()
		mw.refreshConnectionControls()

		if mw.pcpanelWidget != nil {
			mw.pcpanelWidget.SetClient(nil)
		}
		if mw.scriptsWidget != nil {
			mw.scriptsWidget.SetClient(nil)
		}

		mw.updateStatus()
		mw.config.VideoBindHost = "127.0.0.1"

		if !mw.isClosing.Load() {
			mw.hostEntry.Enable()
			mw.tokenEntry.Enable()
			mw.protocolSelect.Enable()
		}

		mw.updateStatusBar()
	})

	// 3. Do the heavy lifting in the BACKGROUND
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logrus.Errorf("🔥 PANIC in background disconnect cleanup: %v", r)
			}
			logrus.Info("✅ [shutdown] Background disconnect cleanup complete")
		}()

		logrus.Info("⏳ [shutdown] Background cleanup starting...")

		if video != nil {
			logrus.Info("🛑 [shutdown] Stopping video...")
			_ = video.StopVideoSync()
			video.Close()
		}

		if backup != nil {
			backup.Close()
		}

		if client != nil {
			// Never call StopAllDevicesWithContext here, on any disconnect
			// path (plain Disconnect, reconnect cycle, or the app actually
			// closing) -- it tears down the *device's* whole USB gadget:
			// every mounted drive, keyboard, mouse, RNDIS, not just this
			// client's own local resources (video/NBD/ports, cleaned up
			// below). This client disconnecting must never drop the
			// remote's disk or HID control -- those stay mounted exactly as
			// the user left them, for this client's own next reconnect or
			// for any other consumer (e.g. the device's own SSH-KVM
			// session) using the same gadget in the meantime. Previously
			// unconditional here, so every disconnect (and every app close)
			// silently unmounted whatever was mounted -- confirmed live.
			logrus.Info("ℹ️ [shutdown] Disconnecting without touching the remote gadget")
			client.Disconnect()
		}

		if nbd != nil && nbd.IsRunning() {
			logrus.Info("🛑 [shutdown] Stopping NBD server...")
			_ = nbd.Stop()
		}

		// Stop per-disk NBD servers created by the disk widget (these are separate from mw.nbdServer).
		// Without this, ports remain occupied after a main-window disconnect and reconnection fails.
		if diskWidget != nil {
			logrus.Info("🛑 [shutdown] Stopping disk widget NBD servers...")
			diskWidget.StopAllNBDServers()
		}
	}()
}

// handleRefresh handles a refresh
func (mw *MainWindow) handleRefresh() {
	if !mw.isConnected || mw.usbClient == nil {
		logrus.Warn("Cannot refresh: no active connection")
		return
	}

	mw.diskWidget.Refresh()
	mw.videoWidget.Refresh()
	if mw.backupWidget != nil {
		mw.backupWidget.Refresh()
	}
}

// updateStatus updates the status in the UI
func (mw *MainWindow) updateStatus() {
	nbdConnected := false
	if mw.nbdServer.IsRunning() {
		clients := mw.nbdServer.GetClients()
		nbdConnected = len(clients) > 0
	}
	mw.appState.IsNBDRunning = nbdConnected

	if mw.videoWidget != nil && mw.videoWidget.IsStreaming() {
		mw.appState.IsStreaming = true
		mw.isStreaming = true
	} else {
		mw.appState.IsStreaming = false
		mw.isStreaming = false
	}

	fyne.Do(func() {
		mw.updateStatusBar()
	})
}

// resolveVideoBindHost returns the address on which the video client should listen.
// Tailscale: Tailscale interface (100.x.x.x), otherwise 127.0.0.1.
func (mw *MainWindow) resolveVideoBindHost() string {
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		name := strings.ToLower(iface.Name)
		if !strings.Contains(name, "tailscale") && !strings.Contains(name, "wg") && !strings.Contains(name, "tun") {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				if ip := ipnet.IP.To4(); ip != nil && ip[0] == 100 {
					return ip.String()
				}
			}
		}
	}
	return "127.0.0.1"
}
