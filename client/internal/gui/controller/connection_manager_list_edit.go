package controller

// connection_manager_list_edit.go -- List mode's split-edit panel (see
// connection_list_table.go's NewConnectionsListSplit): the pencil on a List
// row no longer opens the modal editor (showConnectionEditorDialog/
// showEditDialog, now unused) as a popup overlay. Instead it switches the
// whole List view into a two-pane split -- the table on the left with its
// NETWORK/ACTIONS columns dropped, this panel on the right -- via
// ConnectionManager.editingListIndex.
//
// This reuses the modal editor's individual field constructors
// (newConnectionNameEntry et al, buildConnectionDialogForm) rather than its
// popup-showing code (showAdaptiveConnectionDialog), so every other
// showConnectionEditorDialog caller (Add-connection) is completely
// untouched by this. The panel itself keeps the modal's existing look
// as-is (old design, not restyled to match the newer Grid/List cards) --
// that's a deliberate first pass, not an oversight.

import (
	"image/color"
	"strings"

	"usbridge-client/internal/gui/design"
	"usbridge-client/internal/gui/i18n"
	"usbridge-client/internal/gui/view"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
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

	nameEntry := newConnectionNameEntry(conn.Name, nil)
	internalHostEntry := newConnectionHostEntry(conn.InternalHost, nil)
	tailscaleHostEntry := newConnectionTailscaleEntry(conn.TailscaleHost, nil)
	masterKeyEntry := newConnectionMasterKeyEntry(conn.MasterKey, nil)

	registerCheck := widget.NewCheck(i18n.Current.TailscaleRegisterLabel, nil)
	registerCheck.Checked = conn.TailscaleRegister && tailscaleRegisterUISupported()
	registerCheckContainer := container.NewVBox(registerCheck)
	updateRegisterVisibility := func(tsHost string) {
		if tailscaleRegisterUISupported() && strings.TrimSpace(tsHost) == "" {
			registerCheckContainer.Show()
		} else {
			registerCheckContainer.Hide()
		}
		registerCheckContainer.Refresh()
	}
	updateRegisterVisibility(conn.TailscaleHost)
	tailscaleHostEntry.OnChanged = func(text string) {
		updateRegisterVisibility(text)
	}

	form := buildConnectionDialogForm(nameEntry, internalHostEntry, tailscaleHostEntry, masterKeyEntry, registerCheckContainer, cm.window)
	scroll := container.NewVScroll(form)
	scroll.SetMinSize(fyne.NewSize(0, form.MinSize().Height))

	title := view.NewBrandText(i18n.Current.EditConnectionTitle, 19, design.ColorTextLight, true)
	title.Alignment = fyne.TextAlignCenter

	closeBtn := newConnectionDialogIconButton(theme.CancelIcon(), func() {
		cm.exitListEdit()
	})
	titleBar := container.New(&connectionDialogTitleLayout{}, title, closeBtn)

	saveBtn := view.NewConnectionPrimaryButton(i18n.Current.DeepLinkSave, func() {
		cm.saveListEditPanel(idx, nameEntry.Text, internalHostEntry.Text, tailscaleHostEntry.Text, masterKeyEntry.Text, registerCheck.Checked)
	})
	saveBtn.SetAccent(true)

	deleteBtn := newConnectionDialogDangerSecondaryButton(i18n.Current.DeleteButton, theme.DeleteIcon(), func() {
		cm.handleDeleteConnection(idx, nil)
	})

	buttons := container.New(&connectionDialogButtonsLayout{gap: connectionDialogButtonsGap}, deleteBtn, saveBtn)

	inner := container.NewBorder(
		view.NewInset(titleBar, 0, 0, 0, 10),
		view.NewInset(buttons, 0, 0, 10, 0),
		nil, nil,
		scroll,
	)

	bg := canvas.NewRectangle(design.ColorGray900)
	bg.CornerRadius = design.RadiusMD

	border := canvas.NewRectangle(color.Transparent)
	border.CornerRadius = design.RadiusMD
	border.StrokeColor = design.ColorBorder
	border.StrokeWidth = 1

	return container.NewStack(bg, view.NewInset(inner, 18, 18, 16, 16), border)
}

// saveListEditPanel commits the split-edit panel's fields -- same
// validation/merge shape as the modal editor's onSave (showEditDialog,
// now unused) and Grid's own saveGridCardEdit.
func (cm *ConnectionManager) saveListEditPanel(idx int, name, internalHost, tailscaleHost, masterKey string, tailscaleRegister bool) {
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
		TailscaleRegister: tailscaleRegister,
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
