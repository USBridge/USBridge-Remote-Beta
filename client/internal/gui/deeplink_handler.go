package gui

import (
	"fmt"
	"net/netip"
	"net/url"
	"strings"

	"usbridge-client/internal/gui/i18n"
	"usbridge-client/internal/gui/view"
	"usbridge-client/internal/platform"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/sirupsen/logrus"
)

// DeepLinkHandler handles deep links
type DeepLinkHandler struct {
	onConnect func(host, masterKey, protocol string, tailscaleRegister bool)                              // Connect
	onSave    func(name, internalHost, tailscaleHost, masterKey, protocol string, tailscaleRegister bool) // Save only, without connecting
	lastURI   string                                                                                      // Last processed URI (to avoid processing twice)
}

// NewDeepLinkHandler creates a new handler
func NewDeepLinkHandler(onConnect func(host, masterKey, protocol string, tailscaleRegister bool), onSave func(name, internalHost, tailscaleHost, masterKey, protocol string, tailscaleRegister bool)) *DeepLinkHandler {
	return &DeepLinkHandler{
		onConnect: onConnect,
		onSave:    onSave,
		lastURI:   "",
	}
}

// CheckAndHandleDeepLink checks for a deep link and handles it
func (h *DeepLinkHandler) CheckAndHandleDeepLink(parent fyne.Window) {
	// Get the URI from the Intent
	uri, err := platform.GetIntentDataURI()
	if err != nil {
		logrus.Errorf("❌ Failed to read deep link: %v", err)
		return
	}

	if uri == "" {
		// No deep link
		return
	}

	// Check whether we've already processed this URI
	if h.lastURI == uri {
		// Already processed, skip
		return
	}

	logrus.Infof("🔗 New deep link detected: %s", uri)

	// Remember the current URI
	h.lastURI = uri

	// Parse the URI
	internalHost, tailscaleHost, masterKey, protocol, immediate, err := h.parseDeepLink(uri)
	if err != nil {
		logrus.Errorf("❌ Failed to parse deep link: %v", err)
		view.ShowConnectionErrorDialog(fmt.Errorf(i18n.Current.DeepLinkError, err), parent)
		return
	}

	if immediate {
		logrus.Info("🚀 Immediate connection requested via deep link")
		host := resolveDeepLinkHost(protocol, internalHost, tailscaleHost)
		if h.onConnect != nil {
			h.onConnect(host, masterKey, protocol, false)
		}
		return
	}

	// Show the confirmation dialog
	h.showConfirmDialog(internalHost, tailscaleHost, masterKey, protocol, parent)
}

// parseDeepLink parses the deep link URI
func (h *DeepLinkHandler) parseDeepLink(uri string) (internalHost, tailscaleHost, masterKey, protocol string, immediate bool, err error) {
	// Parse the URL
	u, err := url.Parse(uri)
	if err != nil {
		return "", "", "", "", false, fmt.Errorf("invalid link format: %v", err)
	}

	// Check the scheme (only usbridge://)
	if u.Scheme != "usbridge" {
		return "", "", "", "", false, fmt.Errorf("unsupported scheme: %s (use usbridge://)", u.Scheme)
	}

	// Format: usbridge://connect?host=192.168.1.1&master_key=secret
	if u.Host != "connect" {
		return "", "", "", "", false, fmt.Errorf("unsupported path: %s (use usbridge://connect)", u.Host)
	}

	// Get the parameters
	query := u.Query()
	internalHost = query.Get("internal_host")
	tailscaleHost = query.Get("tailscale_host")
	host := query.Get("host")
	if internalHost == "" && tailscaleHost == "" {
		if isLikelyTailnetHost(host) {
			tailscaleHost = host
		} else {
			internalHost = host
		}
	}
	masterKey = query.Get("master_key")
	if masterKey == "" {
		masterKey = query.Get("token")
	}
	protocol = query.Get("protocol")
	immediate = query.Get("immediate") == "true"

	// Check the required parameters
	if internalHost == "" && tailscaleHost == "" {
		return "", "", "", "", false, fmt.Errorf("missing host parameter")
	}

	if masterKey == "" {
		return "", "", "", "", false, fmt.Errorf("missing master_key parameter")
	}

	logrus.Infof("✅ Deep link parsed: internal=%s tailscale=%s masterKey=%s protocol=%s immediate=%v", internalHost, tailscaleHost, maskSensitiveToken(masterKey), protocol, immediate)
	return internalHost, tailscaleHost, masterKey, protocol, immediate, nil
}

// showConfirmDialog shows the connection confirmation dialog with an option to save
// IMPORTANT: must be called from the UI thread (inside fyne.Do)
func (h *DeepLinkHandler) showConfirmDialog(internalHost, tailscaleHost, masterKey, protocol string, parent fyne.Window) {
	host := resolveDeepLinkHost(protocol, internalHost, tailscaleHost)
	// Create a preview with the data
	titleLabel := widget.NewLabelWithStyle(
		"🔗 "+i18n.Current.ConnectViaLink,
		fyne.TextAlignCenter,
		fyne.TextStyle{Bold: true},
	)

	masterKeyLabel := widget.NewLabel(i18n.Current.DeepLinkToken)
	masterKeyEntry := widget.NewEntry()
	masterKeyEntry.SetText(masterKey)
	masterKeyEntry.Disable() // Read-only

	infoLabel := widget.NewLabel(i18n.Current.DeepLinkConnectPrompt)
	infoLabel.Wrapping = fyne.TextWrapWord

	// Create the buttons (create first, to use in the callbacks)
	var d dialog.Dialog

	connectBtn := widget.NewButton(i18n.Current.DeepLinkConnect, func() {
		logrus.Infof("✅ User chose to connect via deep link")
		if d != nil {
			d.Hide()
		}
		if h.onConnect != nil {
			h.onConnect(host, masterKey, protocol, false)
		}
	})
	connectBtn.Importance = widget.HighImportance

	saveBtn := widget.NewButton(i18n.Current.DeepLinkSave, func() {
		logrus.Infof("💾 User chose to save the connection from deep link")
		if d != nil {
			d.Hide()
		}
		// Call the save callback with an empty name - it will be generated automatically
		if h.onSave != nil {
			h.onSave("", internalHost, tailscaleHost, masterKey, protocol, false)
		}
	})
	saveBtn.Importance = widget.MediumImportance

	cancelBtn := widget.NewButton(i18n.Current.Cancel, func() {
		logrus.Info("❌ User cancelled deep link connection")
		if d != nil {
			d.Hide()
		}
	})
	cancelBtn.Importance = widget.LowImportance

	// Buttons: Connect (Yes/primary) on the left, Save, Cancel
	buttons := container.NewGridWithColumns(3,
		connectBtn,
		saveBtn,
		cancelBtn,
	)

	// Full content with buttons
	fullContent := container.NewBorder(
		container.NewVBox(
			titleLabel,
			widget.NewSeparator(),
			infoLabel,
			widget.NewSeparator(),
			widget.NewLabel("Internal Address"),
			disabledDeepLinkEntry(internalHost),
			widget.NewLabel("Tailscale Address"),
			disabledDeepLinkEntry(tailscaleHost),
			masterKeyLabel,
			masterKeyEntry,
		), // Top
		buttons,  // Bottom
		nil, nil, // Left, Right
		nil, // Center
	)

	// Create a custom dialog with the full content
	d = dialog.NewCustomWithoutButtons(i18n.Current.ConnectionTitle, fullContent, parent)
	d.Resize(fyne.NewSize(500, 380))
	d.Show()
}

// GenerateDeepLink generates a deep link for connecting.
// Format: usbridge://connect?internal_host=<HOST>&tailscale_host=<HOST>&master_key=<TOKEN>&protocol=<PROTOCOL>
func GenerateDeepLink(internalHost, tailscaleHost, masterKey, protocol string) string {
	// Encode the parameters
	params := url.Values{}
	if internalHost != "" {
		params.Set("internal_host", internalHost)
	}
	if tailscaleHost != "" {
		params.Set("tailscale_host", tailscaleHost)
	}
	if masterKey != "" {
		params.Set("master_key", masterKey)
	}
	if protocol != "" {
		params.Set("protocol", protocol)
	}

	return fmt.Sprintf("usbridge://connect?%s", params.Encode())
}

func disabledDeepLinkEntry(value string) *widget.Entry {
	entry := widget.NewEntry()
	entry.SetText(value)
	entry.Disable()
	return entry
}

func resolveDeepLinkHost(protocol, internalHost, tailscaleHost string) string {
	if protocol == "tailscale" && tailscaleHost != "" {
		return tailscaleHost
	}
	if protocol == "direct" && internalHost != "" {
		return internalHost
	}
	if tailscaleHost != "" {
		return tailscaleHost
	}
	return internalHost
}

func isLikelyTailnetHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.HasSuffix(strings.ToLower(host), ".ts.net") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return netip.MustParsePrefix("100.64.0.0/10").Contains(addr)
}
