package controller

import (
	"runtime"
	"strings"

	"usbridge-client/internal/gui/view"
	"usbridge-client/internal/models"

	"fyne.io/fyne/v2"
	"github.com/sirupsen/logrus"
)

func (cm *ConnectionManager) createInterface() {
	cm.ui = view.NewConnectionManagerUI(
		cm.handleQRScan,
		cm.showAddDialog,
		cm.openInfoPage,
		cm.openHardwarePromo,
		cm.handlePasteLink,
	)
	cm.refreshConnectionsList()
	cm.initTailscaleMode()
}

func (cm *ConnectionManager) initTailscaleMode() {
	// Tailscale always runs in userspace (tsnet) mode on every platform — no
	// system VPN daemon involved.

	// Do NOT eagerly start tsnet here just because TailscaleEnabled defaults to
	// true. Starting tsnet while unauthenticated triggers StartLoginInteractive,
	// which pops an auth browser window on top of the app on every launch/connect
	// — even for a plain LAN/direct connection the user never asked to route via
	// Tailscale. tsnet is started lazily instead: by the explicit "Sign In With
	// Google" button (startTailscaleLogin) or by a connection attempt that
	// actually targets a tailnet host. Status() will not auto-start tsnet if the
	// server hasn't been explicitly started yet.
	//
	// Run off the main goroutine: this runs during startup before the Fyne
	// event loop is pumping, and refreshTailscaleStatus ends up calling
	// fyne.Do, which errors if invoked directly from the main goroutine.
	go cm.refreshTailscaleStatus()
}

func (cm *ConnectionManager) showLanguageMenu(anchor fyne.CanvasObject) {
	currentLanguage := cm.app.Preferences().StringWithFallback("language", "en")
	view.ShowStyledMenu(anchor, []view.StyledMenuItem{
		{
			Label:    "English",
			Selected: currentLanguage == "en",
			OnTap: func() {
				cm.setLanguage("en")
			},
		},
		{
			Label:    "Español",
			Selected: currentLanguage == "es",
			OnTap: func() {
				cm.setLanguage("es")
			},
		},
		{
			Label:    "Українська",
			Selected: currentLanguage == "uk" || currentLanguage == "ua",
			OnTap: func() {
				cm.setLanguage("uk")
			},
		},
	})
}

func (cm *ConnectionManager) openQuickStartDocs() {
	const docsURL = "https://www.usbridge.io/docs/getting-started/quick-start-guide/"

	cm.openExternalLink(docsURL, "docs URL")
}

// openInfoPage opens the USBridge Remote product page -- the destination for
// the connections screen's own "?" footer icon (distinct from the header's
// helpBtn in main_window_layout.go, which links here too, and from
// openQuickStartDocs' getting-started guide).
func (cm *ConnectionManager) openInfoPage() {
	const infoURL = "https://www.usbridge.io/usbridge-remote"

	cm.openExternalLink(infoURL, "info URL")
}

func (cm *ConnectionManager) openDiscordInvite() {
	const discordURL = "https://discord.gg/XwNpCrGfsB"

	cm.openExternalLink(discordURL, "Discord invite URL")
}

func (cm *ConnectionManager) openHardwarePromo() {
	const promoURL = "https://www.crowdsupply.com/usbridge-technologies/usbridge-kvm-2-0"

	cm.openExternalLink(promoURL, "hardware promo URL")
}

// RefreshList forces an immediate re-render of the connections list.
// Call this whenever the connection manager becomes visible so stale
// "TS: none" entries are replaced with the latest saved Tailscale addresses.
func (cm *ConnectionManager) RefreshList() {
	if cm == nil {
		return
	}
	cm.refreshConnectionsList()
}

func (cm *ConnectionManager) refreshConnectionsList() {
	if cm.ui == nil {
		// Guards a ConnectionManager built without going through
		// NewConnectionManager's createInterface() step -- every real
		// caller does, but connection_manager_sync.go's background
		// goroutines (trySyncPullAndMerge, reconcileConflict) call this
		// too, and their unit tests construct a bare ConnectionManager
		// struct literal with no UI at all.
		return
	}
	if len(cm.connections) == 0 {
		cm.ui.SetEmptyState()
		cm.notifyConnectionsState()
		return
	}

	rows := make([]view.ConnectionListItem, 0, len(cm.connections))
	cards := make([]fyne.CanvasObject, 0, len(cm.connections))
	remoteOSValues := make([]string, 0, len(cm.connections))
	for i, conn := range cm.connections {
		rows = append(rows, cm.createConnectionRow(conn, i))
		cards = append(cards, cm.createConnectionGridCard(conn, i))
		remoteOSValues = append(remoteOSValues, conn.RemoteOS)
	}
	cm.ui.SetRows(rows, cards, view.SummarizeConnections(remoteOSValues))
	cm.notifyConnectionsState()
}

func (cm *ConnectionManager) createConnectionRow(conn SavedConnection, idx int) view.ConnectionListItem {
	conn.Protocol = normalizeConnectionProtocol(conn.Protocol)
	internalHost, tailscaleHost := classifyConnectionHosts(conn)
	rowState := view.ConnectionRowState{
		Disabled: cm.connectionPending,
		Loading:  cm.connectionPending && cm.activeConnectionIndex == idx,
	}

	fillForm := func() {
		if cm.connectionPending {
			return
		}

		fyne.Do(func() {
			cm.SelectConnection(idx)
		})
	}

	return view.ConnectionListItem{
		Data: view.ConnectionRowData{
			Name:             conn.Name,
			AddressSummary:   formatConnectionAddressSummary(internalHost, tailscaleHost),
			LANAddress:       internalHost,
			TailscaleAddress: tailscaleHost,
			ProtocolBadge:    connectionProtocolBadge(conn.Protocol),
			ProtocolOptions: []string{
				connectionProtocolBadge(models.ConnectionProtocolAuto),
				connectionProtocolBadge(models.ConnectionProtocolTailscale),
				connectionProtocolBadge(models.ConnectionProtocolDirect),
			},
			// No embedded tsnet in a browser tab (tailscale_service_wasm.go
			// is a stub) -- every web connection is LAN-only regardless
			// (see normalizeConnectionProtocol's own wasm override), so
			// this selector has nothing to actually select on this
			// platform. Native builds (desktop/Android/iOS) keep it.
			HideProtocolSelector: runtime.GOOS == "js",
			RegisterChecked:      conn.TailscaleRegister && tailscaleRegisterUISupported(),
			RegisterVisible:      tailscaleRegisterUISupported() && internalHost != "" && tailscaleHost == "",
			RemoteOS:             conn.RemoteOS,
		},
		State: rowState,
		Actions: view.ConnectionRowActions{
			OnSelect: fillForm,
			OnUse: func() {
				if !cm.beginConnectionFromRow(idx) {
					return
				}

				fyne.Do(func() {
					cm.SelectConnection(idx)
					if cm.onConnect != nil {
						conn := cm.connections[idx]
						protocol := normalizeConnectionProtocol(conn.Protocol)
						host := cm.resolveHostForProtocol(conn, protocol)
						cm.onConnect(host, conn.MasterKey, protocol, conn.TailscaleRegister)
						return
					}
					cm.SetConnectionPending(false)
				})
			},
			OnEdit: func() {
				if cm.connectionPending {
					return
				}
				cm.showEditDialog(idx)
			},
			OnProtocolChange: func(label string) {
				if cm.connectionPending {
					return
				}
				cm.updateConnectionProtocol(idx, connectionProtocolFromBadge(label))
			},
			OnRegisterChange: func(checked bool) {
				if cm.connectionPending {
					return
				}
				cm.connections[idx].TailscaleRegister = checked
				cm.saveConnections()
				// If this is the currently selected connection, update the main check too
				if cm.selectedIndex == idx && cm.onSelect != nil {
					cm.onSelect(checked)
				}
			},
		},
	}
}

// createConnectionGridCard builds the same connection's Grid-mode card
// (NewConnectionGridCard), mirroring createConnectionRow's data/actions so
// the Grid/List toggle shows equivalent content either way.
func (cm *ConnectionManager) createConnectionGridCard(conn SavedConnection, idx int) fyne.CanvasObject {
	conn.Protocol = normalizeConnectionProtocol(conn.Protocol)
	internalHost, tailscaleHost := classifyConnectionHosts(conn)
	rowState := view.ConnectionRowState{
		Disabled: cm.connectionPending,
		Loading:  cm.connectionPending && cm.activeConnectionIndex == idx,
		Editing:  cm.editingGridIndex == idx,
	}

	fillForm := func() {
		if cm.connectionPending {
			return
		}

		fyne.Do(func() {
			cm.SelectConnection(idx)
		})
	}

	return view.NewConnectionGridCard(
		view.ConnectionCardData{
			Name:             conn.Name,
			RemoteOS:         conn.RemoteOS,
			LANAddress:       internalHost,
			TailscaleAddress: tailscaleHost,
			MasterKey:        conn.MasterKey,
			ProtocolBadge:    connectionProtocolBadge(conn.Protocol),
			ProtocolOptions: []string{
				connectionProtocolBadge(models.ConnectionProtocolAuto),
				connectionProtocolBadge(models.ConnectionProtocolTailscale),
				connectionProtocolBadge(models.ConnectionProtocolDirect),
			},
		},
		rowState,
		view.ConnectionCardActions{
			OnSelect: fillForm,
			OnEdit: func() {
				if cm.connectionPending {
					return
				}
				// Grid's pencil edits the card in place instead of opening
				// the modal (List's showEditDialog stays modal -- see
				// createConnectionRow's own OnEdit above).
				cm.editingGridIndex = idx
				fyne.Do(func() {
					cm.refreshConnectionsList()
				})
			},
			OnSave: func(name, lanAddress, tailscaleAddress, masterKey string) {
				cm.saveGridCardEdit(idx, name, lanAddress, tailscaleAddress, masterKey)
			},
			OnDelete: func() {
				cm.handleDeleteConnection(idx, nil)
			},
			OnCancel: func() {
				// Discard the in-progress edit and go back to the normal
				// card layout -- no validation, no save.
				cm.editingGridIndex = -1
				fyne.Do(func() {
					cm.refreshConnectionsList()
				})
			},
			OnUse: func() {
				if !cm.beginConnectionFromRow(idx) {
					return
				}

				fyne.Do(func() {
					cm.SelectConnection(idx)
					if cm.onConnect != nil {
						conn := cm.connections[idx]
						protocol := normalizeConnectionProtocol(conn.Protocol)
						host := cm.resolveHostForProtocol(conn, protocol)
						cm.onConnect(host, conn.MasterKey, protocol, conn.TailscaleRegister)
						return
					}
					cm.SetConnectionPending(false)
				})
			},
			OnProtocolChange: func(label string) {
				if cm.connectionPending {
					return
				}
				cm.updateConnectionProtocol(idx, connectionProtocolFromBadge(label))
			},
		},
	)
}

// saveGridCardEdit commits a Grid card's inline edit (ConnectionCardActions.
// OnSave) -- same validation/merge shape as the modal editor's onSave
// (showEditDialog in connection_manager_dialogs.go), just without the
// Tailscale-register toggle the compact card has no room for, and without a
// bool return since the card has nowhere to surface a "rejected" state --
// an invalid save just leaves the card in edit mode instead.
func (cm *ConnectionManager) saveGridCardEdit(idx int, name, internalHost, tailscaleHost, masterKey string) {
	if idx < 0 || idx >= len(cm.connections) {
		return
	}
	name = strings.TrimSpace(name)
	internalHost = strings.TrimSpace(internalHost)
	tailscaleHost = strings.TrimSpace(tailscaleHost)
	if name == "" || (internalHost == "" && tailscaleHost == "") {
		logrus.Warn("name and at least one address are required")
		return
	}

	conn := cm.connections[idx]
	cm.connections[idx] = SavedConnection{
		Name:              name,
		InternalHost:      internalHost,
		TailscaleHost:     tailscaleHost,
		Host:              fallbackText(internalHost, tailscaleHost),
		MasterKey:         strings.TrimSpace(masterKey),
		Protocol:          conn.Protocol,
		TailscaleRegister: conn.TailscaleRegister,
		RemoteOS:          conn.RemoteOS,
	}
	cm.selectedIndex = idx
	cm.editingGridIndex = -1
	cm.saveConnections()
	fyne.Do(func() {
		cm.SelectConnection(idx)
		cm.refreshConnectionsList()
	})
	logrus.Infof("Updated connection: %s", name)
}

func (cm *ConnectionManager) updateConnectionProtocol(idx int, protocol string) {
	cm.connections[idx].Protocol = protocol
	cm.saveConnections()
	cm.refreshConnectionsList()
}

func formatConnectionAddressSummary(internalHost, tailscaleHost string) string {
	internalHost = strings.TrimSpace(internalHost)
	tailscaleHost = strings.TrimSpace(tailscaleHost)
	if internalHost == "" {
		internalHost = "none"
	}
	if tailscaleHost == "" {
		tailscaleHost = "none"
	}
	return "LAN: " + internalHost + "\nTS: " + tailscaleHost
}

func (cm *ConnectionManager) ShowLanguageMenu(anchor fyne.CanvasObject) {
	cm.showLanguageMenu(anchor)
}
