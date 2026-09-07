package controller

// connection_manager_list_edit.go -- List mode's split-edit panel (see
// connection_list_table.go's NewConnectionsListSplit): the pencil on a List
// row no longer opens the modal editor (showConnectionEditorDialog/
// showEditDialog, now unused) as a popup overlay. Instead it switches the
// whole List view into a two-pane split -- the table on the left with its
// NETWORK/ACTIONS columns dropped, view.NewConnectionEditPanel on the
// right -- via ConnectionManager.editingListIndex.
//
// view.NewConnectionEditPanel (connection_edit_panel.go) is its own widget,
// not the modal editor's connectionDialogEntry-based fields
// (connection_manager_dialogs.go) -- so it can be restyled without
// affecting showPrefilledAddDialog (Add-connection), which still uses the
// modal.

import (
	"strings"

	"usbridge-client/internal/gui/view"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"github.com/sirupsen/logrus"
)

// buildListEditPanel builds the right-hand pane for List's split-edit
// layout, editing cm.connections[idx]. Returns an empty placeholder if idx
// is out of range (shouldn't happen -- refreshConnectionsList only calls
// this after bounds-checking editingListIndex -- but a stale index between
// that check and this call, e.g. a concurrent delete, shouldn't crash).
func (cm *ConnectionManager) buildListEditPanel(idx int) fyne.CanvasObject {
	if idx < 0 || idx >= len(cm.connections) {
		return container.NewWithoutLayout()
	}
	conn := cm.connections[idx]

	return view.NewConnectionEditPanel(
		view.ConnectionEditPanelData{
			Name:             conn.Name,
			RemoteOS:         conn.RemoteOS,
			LANAddress:       conn.InternalHost,
			TailscaleAddress: conn.TailscaleHost,
			MasterKey:        conn.MasterKey,
		},
		view.ConnectionEditPanelActions{
			OnSave: func(name, lanAddress, tailscaleAddress, masterKey string) {
				cm.saveListEditPanel(idx, name, lanAddress, tailscaleAddress, masterKey)
			},
			OnDelete: func() {
				cm.handleDeleteConnection(idx, nil)
			},
			OnCancel: cm.exitListEdit,
		},
	)
}

// saveListEditPanel commits the split-edit panel's fields -- same
// validation/merge shape as the modal editor's onSave (showEditDialog, now
// unused) and Grid's own saveGridCardEdit. Like Grid's inline edit, there's
// no Tailscale-register toggle here -- the panel has no room for it, so the
// connection's existing value is carried over unchanged; still editable
// from the (still-live) Add-connection modal.
func (cm *ConnectionManager) saveListEditPanel(idx int, name, internalHost, tailscaleHost, masterKey string) {
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
	cm.editingListIndex = -1
	cm.saveConnections()
	fyne.Do(func() {
		cm.SelectConnection(idx)
		cm.refreshConnectionsList()
	})
	logrus.Infof("Updated connection: %s", name)
}

// exitListEdit leaves List's split-edit layout without saving -- the
// panel's "X" button.
func (cm *ConnectionManager) exitListEdit() {
	cm.editingListIndex = -1
	fyne.Do(func() {
		cm.refreshConnectionsList()
	})
}
