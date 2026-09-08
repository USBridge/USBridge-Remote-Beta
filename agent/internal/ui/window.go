package ui

import (
	"context"
	"fmt"
	"image/color"
	"log"
	"net"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/sirupsen/logrus"
	qrcode "github.com/skip2/go-qrcode"

	"usbridge_agent/internal/account"
	"usbridge_agent/internal/autostart"
	"usbridge_agent/internal/capture"
	"usbridge_agent/internal/config"
	"usbridge_agent/internal/entitlement"
	"usbridge_agent/internal/netutil"
	"usbridge_agent/internal/streamhost"
	"usbridge_agent/internal/tailscale"
	"usbridge_agent/internal/ui/design"
	"usbridge_agent/internal/update"
)

// TokenProvider is whatever owns the agent's config/Sunshine lifecycle —
// normally *app.App when the GUI starts its own engine, or *adminapi.Client
// when it's attaching to an already-running headless instance instead.
type TokenProvider interface {
	RegenerateMasterKey() (config.Config, error)
	SaveConfig(config.Config) error
	SunshineCaptureMode() string
	SetSunshineCaptureMode(mode string) error
	KMSCaptureGranted() bool
	RequestKMSCapture() bool
	GPUClockLockSupported() bool
	LockGPUClocksEnabled() bool
	SetLockGPUClocksEnabled(enabled bool) error
	RestartSunshine() error
	ListSunshineClients() ([]streamhost.Client, error)
	UnpairSunshineClient(uniqueID string) error
	SubmitMoonlightPIN(pin string) error
	UpdateListenAddr(host string, port int) (config.Config, error)
	UpdateSunshinePort(port int) (config.Config, error)
	UpdateSunshineStreamAddr(host string, streamPort int) (config.Config, error)
	SunshineStreamHost() string
	AdminUser() string
	AdminPass() string
	StreamerName() string

	// Hardware-bound RustShine entitlement (see internal/entitlement,
	// internal/hwid).
	EntitlementStatus() entitlement.Status
	StartFreeTrial() error
	StartPurchase(tier string) (string, error)
	CancelPurchase()
	ClearLicense() error
	DownloadRustShine(onProgress entitlement.ProgressFunc) error
	CheckRustShineUpdateNow() error
	SetStreamBackend(kind string) error
	SetRustShineWebRTCEnabled(enabled bool) error

	// Account login (see internal/account) -- a separate identity from the
	// hardware-bound entitlement above, used only to pick which of the
	// logged-in account's own desktop licenses to rebind onto this
	// machine. See account.Status's own doc comment.
	AccountStatus() account.Status
	StartAccountLogin() (string, error)
	CancelAccountLogin()
	RefreshAccountLicenses()
	RebindLicenseToThisDevice(oldIdentifier string) error
	LogoutAccount() error
}

// PermsProvider is satisfied by *permissions.Service (embedded engine) or
// *adminapi.Client (thin client attaching to a headless instance).
type PermsProvider interface {
	AccessibilityGranted() bool
	ScreenRecordingGranted() bool
	RequestAccessibility() bool
	RequestScreenRecording() bool
	OpenPrivacySettings() error
	OpenScreenRecordingSettings() error

	// ClipboardToolAvailable/RequestClipboardTool: Linux only (a CLI
	// clipboard helper -- xclip/wl-clipboard/xsel -- is what clipboard sync
	// actually shells out to there, see internal/clipboard/backend_linux.go);
	// every other platform's implementation just returns true/true.
	ClipboardToolAvailable() bool
	RequestClipboardTool() bool

	// ClipboardInstallPreview returns the exact command the "Install"
	// button's click would run (via pkexec), for its "?" tooltip -- "" if
	// nothing would actually run (no pkexec / no supported package manager
	// found), in which case the tooltip is skipped and the button's own
	// click surfaces that reason instead.
	ClipboardInstallPreview() string
}

// TailscaleProvider is satisfied by *tailscale.Service (embedded engine) or
// *adminapi.Client (thin client attaching to a headless instance).
type TailscaleProvider interface {
	Status(context.Context) (*tailscale.Status, error)
	StartLogin(context.Context) (string, error)
	Logout(context.Context) error
	SetAuthURLHandler(func(string))
}

// appVersion is set once at startup via SetAppVersion (ldflags -X in each
// platform's build script) and shown as a small "vX.Y.Z" tag in the
// bottom-right corner of the main window.
var appVersion string

// SetAppVersion records the running build's version string.
func SetAppVersion(v string) {
	appVersion = strings.TrimSpace(v)
}

type Window struct {
	app   fyne.App
	cfg   config.Config
	token TokenProvider
	perms PermsProvider
	ts    TailscaleProvider

	// awaitingLocalLogin is true only while the local "Sign In With Google"
	// button has an interactive login in flight. It gates auto-opening a
	// browser from the AuthURL handler: tsnet can also produce an AuthURL from
	// a remote client's sync/register request or from its own first-boot
	// auto-login, and those must NOT pop a browser on this (possibly headless,
	// possibly actively streaming) machine — only the local button's own
	// request should.
	awaitingLocalLogin atomic.Bool

	// UI components
	accessLabel *widget.Label
	accessBtn   *widget.Button
	permInfo    *widget.Label

	// Screen Capture: a single unified control for how video gets captured.
	// On Linux this is Sunshine's capture backend, picked automatically from
	// the live session (capture.AutoCaptureMode) — no user choice, since a
	// manual "KMS (root)" pick on an X11 desktop session (esp. NVIDIA) is a
	// known-broken combination; see capture.AutoCaptureMode's doc. On other
	// platforms it's just the OS screen-recording permission. The status
	// label and request button always reflect whichever method is currently
	// active — there is only ever one active method at a time.
	screenCaptureLabel *widget.Label
	screenCaptureBtn   *widget.Button

	// clipboardToolRow: Linux only, shown only while no CLI clipboard helper
	// (xclip/wl-clipboard/xsel) is installed -- see
	// permissions.RequestClipboardTool. Hidden entirely (not just the
	// button) once one is found, since there's nothing actionable left to
	// show once clipboard sync just works.
	clipboardToolRow *fyne.Container
	clipboardToolBtn *widget.Button

	// rustshineWebRTCRow: shown only while RustShine is the active backend
	// -- lets a supporter turn USBridge's browser/WASM web client on or off
	// without needing to reopen the license dialog. Sunshine has no
	// equivalent surface (no WebRTC endpoint of its own).
	rustshineWebRTCRow   *fyne.Container
	rustshineWebRTCCheck *widget.Check

	// sunWebSunshineRow/sunWebRustshineRow are mutually exclusive: the
	// Status panel's "web UI" row shows Sunshine's local admin UI address
	// while Sunshine is active, or a link to the RustShine web client
	// (rustshineWebURL) while RustShine is active and its WebRTC endpoint
	// is enabled -- and neither (an empty gap) when RustShine is active
	// with WebRTC turned off, since there's nothing reachable to show.
	sunWebSunshineRow  *fyne.Container
	sunWebRustshineRow *fyne.Container

	// moonlightBtn shows the paired-device count; clicking opens the clients dialog.
	moonlightBtn *widget.Button

	tsInfo    *widget.Label
	tsPeers   *widget.RichText
	tsAuthBtn *widget.Button

	autostartCheck *widget.Check

	// Lock GPU Clocks: Windows+NVIDIA only, see app.applyGPUClockLock.
	gpuClockCheck *widget.Check

	// supportBtn opens showLicenseDialog -- a single, low-emphasis entry
	// point for the whole license/RustShine flow, deliberately never
	// popped up on its own (unlike promptForUpdate's confirm dialog, which
	// is functionally necessary on every launch) -- this is a monetization
	// affordance, not something the agent should ever interrupt a session
	// to push. Its text/importance reflect entitlement.Status live (see
	// performRefresh), so a supporter sees at a glance that they're one
	// without needing to open the dialog.
	supportBtn *supportButton

	// streamerNameLabel shows StreamerName() ("Sunshine (Open Source)" /
	// "RustShine (Proprietary)") -- kept as a field (not a one-off local in
	// the constructor) so performRefresh can keep it in sync with the
	// active backend after a runtime SetStreamBackend switch; otherwise it
	// would freeze at whatever was active when the window was built.
	streamerNameLabel *widget.Label

	// streamerVersionLabel shows whichever version applies to the active
	// backend -- appVersion (this agent build's own version) while
	// Sunshine is active, since Sunshine ships bundled with the agent and
	// has no independent version/update of its own, or
	// entitlement.Status.RustShineVersion while RustShine is active. Kept
	// in sync by refreshRustShineUI, same as streamerNameLabel above.
	streamerVersionLabel *widget.Label
	// rustshineUpdateBtn is a small "check for updates" affordance shown
	// only while RustShine is both active and already staged (see
	// refreshRustShineUI) -- there's nothing to "update" before the first
	// "Download RustShine" click in the license dialog, and Sunshine has
	// no manual update path at all (see streamerVersionLabel's doc
	// comment on why), so this button never applies to it.
	rustshineUpdateBtn *widget.Button

	// ownsEngine is true only when this window's process itself started the
	// engine (App.Run(headless=false)) — as opposed to a thin client
	// attached to an already-running headless instance (runThinClientGUI).
	// Gates the startup update prompt: applying an update from a thin
	// client would replace the on-disk binary and relaunch this attach-only
	// process without ever touching the separate headless instance that's
	// actually running, so it's not offered there. Set via SetOwnsEngine.
	ownsEngine bool
}

// SetOwnsEngine marks this window as backed by an engine this same process
// started, rather than one it merely attached a GUI to. See the ownsEngine
// field doc for why this matters.
func (w *Window) SetOwnsEngine(owns bool) {
	w.ownsEngine = owns
}

type uiStatus struct {
	tsStatus       *tailscale.Status
	accessGranted  bool
	moonlightCount int
}

// accountSnapshot is the comparable (== usable) subset of account.Status --
// that type itself carries a []account.License slice, which Go won't let
// you compare with ==, so showLicenseDialog's poll loop builds one of
// these each tick to detect an actual change cheaply instead of
// unconditionally re-rendering. licensesKey folds the license list into
// one string precisely so a licenses-only change (a fresh
// RefreshAccountLicenses result) still counts as "changed" even though
// none of the scalar fields above it did.
type accountSnapshot struct {
	loggedIn         bool
	email            string
	loginInProgress  bool
	rebindInProgress bool
	lastError        string
	licensesKey      string
}

func newAccountSnapshot(acc account.Status) accountSnapshot {
	var licensesKey strings.Builder
	for _, lic := range acc.Licenses {
		licensesKey.WriteString(lic.Identifier)
		licensesKey.WriteByte(':')
		licensesKey.WriteString(lic.Status)
		licensesKey.WriteByte(':')
		licensesKey.WriteString(lic.Tier)
		licensesKey.WriteByte('|')
	}
	return accountSnapshot{
		loggedIn:         acc.LoggedIn,
		email:            acc.Email,
		loginInProgress:  acc.LoginInProgress,
		rebindInProgress: acc.RebindInProgress,
		lastError:        acc.LastError,
		licensesKey:      licensesKey.String(),
	}
}

func NewWindow(app fyne.App, cfg config.Config, perms PermsProvider, ts TailscaleProvider, tokenManager TokenProvider) *Window {
	return &Window{app: app, cfg: cfg, perms: perms, ts: ts, token: tokenManager}
}

// linuxCaptureUIEnabled reports whether the Screen Capture row should use
// the Linux capture-mode status (Sunshine's auto-detected backend) rather
// than the simple OS screen-recording permission toggle used on other
// platforms.
func (w *Window) linuxCaptureUIEnabled() bool {
	return runtime.GOOS == "linux" && w.token != nil
}

// refreshScreenCaptureUI updates the status label and shows/hides the
// request button based on whichever capture method is currently selected —
// never both at once, so there's no ambiguity about which grant a click
// affects.
func (w *Window) refreshScreenCaptureUI() {
	if w.screenCaptureLabel == nil {
		return
	}

	if w.linuxCaptureUIEnabled() {
		mode := w.token.SunshineCaptureMode()
		if mode == "kms" {
			if w.token.KMSCaptureGranted() {
				w.screenCaptureLabel.SetText("Screen Capture: ✅")
				if w.screenCaptureBtn != nil {
					w.screenCaptureBtn.Hide()
				}
			} else {
				w.screenCaptureLabel.SetText("Screen Capture: ❌")
				if w.screenCaptureBtn != nil {
					w.screenCaptureBtn.Show()
				}
			}
			return
		}

		// Portal capture needs no root, but on Wayland the portal permission
		// can (and should) be pre-approved ahead of time — same
		// InitPortalSession() flow used before Sunshine existed — so the
		// system dialog doesn't surprise the user on first capture.
		granted := w.perms != nil && w.perms.ScreenRecordingGranted()
		if granted {
			w.screenCaptureLabel.SetText("Screen Capture: ✅")
		} else {
			w.screenCaptureLabel.SetText("Screen Capture: ❌")
		}
		if w.screenCaptureBtn != nil {
			if !granted && capture.GetLinuxEnv() == "Wayland" {
				w.screenCaptureBtn.Show()
			} else {
				w.screenCaptureBtn.Hide()
			}
		}
		return
	}

	if w.perms == nil {
		return
	}
	if w.perms.ScreenRecordingGranted() {
		w.screenCaptureLabel.SetText("Screen Capture: ✅")
		if w.screenCaptureBtn != nil {
			w.screenCaptureBtn.Hide()
		}
	} else {
		w.screenCaptureLabel.SetText("Screen Capture: ❌")
		if (runtime.GOOS == "darwin" || (runtime.GOOS == "linux" && capture.GetLinuxEnv() == "Wayland")) && w.screenCaptureBtn != nil {
			w.screenCaptureBtn.Show()
		}
	}
}

// refreshClipboardToolUI hides the whole clipboard-tool row (not just its
// button) once a CLI clipboard helper is found -- there's nothing left to
// act on at that point, unlike the Accessibility/Screen Capture rows above
// which keep showing a ✅/❌ status permanently.
func (w *Window) refreshClipboardToolUI() {
	if w.clipboardToolRow == nil || w.perms == nil {
		return
	}
	if w.perms.ClipboardToolAvailable() {
		w.clipboardToolRow.Hide()
	} else {
		w.clipboardToolRow.Show()
	}
}

// rustshineWebURL is USBridge's browser/WASM web client -- shown in the
// Status panel's web-UI row in place of Sunshine's local admin address
// whenever RustShine is active and its WebRTC endpoint is enabled (see
// refreshRustShineUI).
const rustshineWebURL = "https://web.usbridge.io"

// refreshRustShineUI keeps the standalone WebRTC checkbox (moved out of
// showLicenseDialog so it's visible without opening that popup) and the
// Status panel's web-UI row in sync with entitlement.Status on every
// refresh tick.
func (w *Window) refreshRustShineUI(st entitlement.Status) {
	active := st.ActiveBackend == "rustshine"

	if w.streamerVersionLabel != nil {
		version := "v" + appVersion
		if active {
			// st.RustShineVersion is the raw release tag
			// entitlement.StagedVersion recorded (e.g.
			// "gamestream-server-v0.3.16", see that function's own doc
			// comment) -- showing that whole tag next to "RustShine
			// (Proprietary)" visibly stretched/wrapped this row (confirmed
			// live: "RustShine(Proprietary)   vgamestream-server-v0.3.16").
			// Strip the known release-tag prefix down to the bare version
			// for display; if some future tag ever doesn't have it, this
			// just falls back to showing the raw tag untouched rather than
			// hiding real version info.
			version = strings.TrimPrefix(st.RustShineVersion, "gamestream-server-v")
		}
		if w.streamerVersionLabel.Text != version {
			w.streamerVersionLabel.SetText(version)
		}
	}
	if w.rustshineUpdateBtn != nil {
		switch {
		case !active || !st.RustShineStaged:
			// Sunshine has no manual update path (see
			// streamerVersionLabel's own doc comment), and there's
			// nothing to check an update *against* before RustShine has
			// even been downloaded once.
			w.rustshineUpdateBtn.Hide()
		case st.RustShineUpdateInProgress:
			w.rustshineUpdateBtn.Show()
			w.rustshineUpdateBtn.Disable()
		default:
			w.rustshineUpdateBtn.Show()
			w.rustshineUpdateBtn.Enable()
		}
	}

	if w.rustshineWebRTCRow != nil {
		if active {
			w.rustshineWebRTCRow.Show()
			if w.rustshineWebRTCCheck != nil && w.rustshineWebRTCCheck.Checked != st.WebRTCEnabled {
				w.rustshineWebRTCCheck.Checked = st.WebRTCEnabled
				w.rustshineWebRTCCheck.Refresh()
			}
		} else {
			w.rustshineWebRTCRow.Hide()
		}
	}

	if w.sunWebSunshineRow != nil && w.sunWebRustshineRow != nil {
		switch {
		case active && st.WebRTCEnabled:
			w.sunWebSunshineRow.Hide()
			w.sunWebRustshineRow.Show()
		case active:
			// RustShine active but its WebRTC endpoint is off -- nothing
			// reachable to show here at all.
			w.sunWebSunshineRow.Hide()
			w.sunWebRustshineRow.Hide()
		default:
			w.sunWebRustshineRow.Hide()
			w.sunWebSunshineRow.Show()
		}
	}
}

func (w *Window) ShowAndRun(onClose func()) {
	win := w.app.NewWindow("USBridge Agent")
	win.SetPadded(false)
	win.Resize(fyne.NewSize(640, 400))
	win.CenterOnScreen()

	tokenBtn := newIconActionButton("TOKEN", theme.SettingsIcon(), func() {
		w.showTokenDialog(win)
	})
	header := newHeaderBar(nil, tokenBtn)

	// Column 1: Permissions
	accessLabelBase := "Accessibility"
	if runtime.GOOS == "linux" {
		accessLabelBase = "Input Control"
	}
	w.accessLabel = widget.NewLabel(accessLabelBase)
	w.accessBtn = widget.NewButton("Request", func() {
		if w.perms == nil {
			return
		}
		w.accessBtn.Disable()
		go func() {
			defer fyne.Do(func() {
				if w.accessBtn != nil {
					w.accessBtn.Enable()
				}
			})
			granted := w.perms.RequestAccessibility()
			if !granted {
				if e, ok := w.perms.(interface{ LastAccessibilityError() string }); ok {
					if msg := e.LastAccessibilityError(); msg != "" {
						fyne.Do(func() { dialog.ShowError(fmt.Errorf("%s", msg), win) })
					}
				}
			}
			w.performRefresh()
		}()
	})
	w.accessBtn.Importance = widget.HighImportance

	// Screen Capture: single row covering however the video actually gets
	// captured. On Linux that's Sunshine's backend (Portal, no root vs. KMS,
	// root); elsewhere it's just the OS screen-recording permission.
	w.screenCaptureLabel = widget.NewLabel("Screen Capture")
	w.screenCaptureBtn = widget.NewButton("Request", func() {
		w.screenCaptureBtn.Disable()
		go func() {
			defer fyne.Do(func() {
				if w.screenCaptureBtn != nil {
					w.screenCaptureBtn.Enable()
				}
			})
			if w.linuxCaptureUIEnabled() && w.token.SunshineCaptureMode() == "kms" {
				w.token.RequestKMSCapture()
			} else if runtime.GOOS == "darwin" && w.perms != nil {
				// On macOS, screen recording must be granted to Sunshine
				// (a separate process) via System Preferences — we can't
				// request it on Sunshine's behalf. Open the Settings pane
				// so the user can enable it, then restart Sunshine to pick
				// up the new permission.
				_ = w.perms.OpenScreenRecordingSettings()
				if w.token != nil {
					_ = w.token.RestartSunshine()
				}
			} else if w.perms != nil {
				_ = w.perms.RequestScreenRecording()
			}
			fyne.Do(w.refreshScreenCaptureUI)
		}()
	})
	w.screenCaptureBtn.Importance = widget.HighImportance
	w.permInfo = widget.NewLabel("")
	w.permInfo.Wrapping = fyne.TextWrapWord

	// Adjust for OS. Input Control (uinput access) is an OS-level grant with
	// no dependency on which display server is running, so it's requestable
	// on Linux unconditionally (X11 or Wayland), same as macOS's
	// Accessibility permission — unlike Screen Capture below, whose own
	// interactive request button is only meaningful for macOS's flow or
	// Wayland's portal flow (X11/KMS capture needs no such request: the
	// dropdown alone is enough, see linuxCapture below).
	showAccessButton := runtime.GOOS == "darwin" || runtime.GOOS == "linux"
	showScreenCaptureButton := runtime.GOOS == "darwin" || (runtime.GOOS == "linux" && capture.GetLinuxEnv() == "Wayland")
	linuxCapture := w.linuxCaptureUIEnabled()

	if !showAccessButton {
		w.accessBtn.Hide()
	} else {
		w.accessBtn.Resize(fyne.NewSize(80, 24))
		w.accessBtn.Show()
	}

	if !showScreenCaptureButton && !linuxCapture {
		w.screenCaptureBtn.Hide()
	} else {
		w.screenCaptureBtn.Resize(fyne.NewSize(80, 24))
		w.screenCaptureBtn.Show()
	}

	// No manual method picker here: on Linux the capture backend is always
	// capture.AutoCaptureMode()'s pick for the live session (kept in sync by
	// app.syncSunshineCaptureMode/SetSunshineCaptureMode) — a user-selectable
	// "KMS (root)" on an X11 desktop session (esp. NVIDIA) was a known-broken
	// combination in practice (confirmed live: 12/12 DRM planes and the
	// connector's own encoder_id read back empty/zero on an otherwise fully
	// working RTX 2080 Ti + driver 550 X11 session — KMS needs DRM master,
	// which Xorg already holds). The row below just reports which method
	// ended up active and, on Wayland, offers the portal permission request.
	screenCaptureRow := container.NewHBox(w.screenCaptureLabel, layout.NewSpacer())
	screenCaptureRow.Add(w.screenCaptureBtn)

	// Autostart at Boot: installs the OS-native autostart mechanism (a
	// system-wide systemd unit on Linux — so it starts at boot before any
	// graphical session, which is what KMS capture needs; a LaunchAgent
	// plist on macOS; a Run registry value on Windows — see
	// internal/autostart). The registered command always launches with
	// --headless, so a later normal launch of this same binary/AppImage
	// attaches a GUI to that instance instead of starting a second engine —
	// see app.Start. On Linux this shells out via pkexec, same as the KMS
	// capability grant, so expect a polkit prompt on toggle.
	w.autostartCheck = widget.NewCheck("", nil)
	w.autostartCheck.Checked = autostart.IsEnabled()
	w.autostartCheck.Refresh()
	w.autostartCheck.OnChanged = func(checked bool) {
		w.autostartCheck.Disable()
		go func() {
			var err error
			if checked {
				err = autostart.Enable()
			} else {
				err = autostart.Disable()
			}
			fyne.Do(func() {
				if w.autostartCheck == nil {
					return
				}
				w.autostartCheck.Enable()
				if err != nil {
					logrus.Errorf("[ui] autostart toggle failed: %v", err)
					w.autostartCheck.Checked = !checked
					w.autostartCheck.Refresh()
					dialog.ShowError(err, win)
				}
			})
		}()
	}

	// Autostart at Boot is always shown, regardless of platform — unlike the
	// access/screen-capture rows below, it's never collapsed into permInfo's
	// plain-text summary. Windows (and any other platform with neither
	// interactive request buttons nor the Linux capture dropdown) used to
	// fall into the permInfo-only branch below, which replaced the entire
	// permRows slice and silently dropped the Autostart checkbox with it.
	autostartRow := container.NewHBox(widget.NewLabel("Autostart at Boot"), layout.NewSpacer(), w.autostartCheck)

	// Lock GPU Clocks: holds an NVML max-clock lock for the life of this
	// agent process (once enabled) so the GPU doesn't idle into a low-power
	// state between frames and stall NVENC on the next one (see
	// app.applyGPUClockLock). Windows+NVIDIA only -- entirely absent from the
	// Permissions block on other platforms, where GPUClockLockSupported()
	// returns false, rather than shown-but-disabled. No separate "Request"
	// button: the checkbox itself triggers the one (UAC-prompting) request
	// for this agent run -- deliberately upfront and one-time, not
	// re-triggered on every stream-host restart, since a UAC prompt can't be
	// dismissed from a remote session (it runs on the secure desktop) and
	// would otherwise strand a remote client switching monitors mid-stream.
	gpuClockSupported := w.token != nil && w.token.GPUClockLockSupported()
	w.gpuClockCheck = widget.NewCheck("", nil)
	if gpuClockSupported {
		w.gpuClockCheck.Checked = w.token.LockGPUClocksEnabled()
		w.gpuClockCheck.Refresh()
	}
	w.gpuClockCheck.OnChanged = func(checked bool) {
		w.gpuClockCheck.Disable()
		go func() {
			var err error
			if w.token != nil {
				err = w.token.SetLockGPUClocksEnabled(checked)
			}
			fyne.Do(func() {
				if w.gpuClockCheck == nil {
					return
				}
				w.gpuClockCheck.Enable()
				if err != nil {
					logrus.Errorf("[ui] lock GPU clocks toggle failed: %v", err)
					w.gpuClockCheck.Checked = !checked
					w.gpuClockCheck.Refresh()
					dialog.ShowError(err, win)
				}
			})
		}()
	}
	gpuClockRow := container.NewHBox(widget.NewLabel("Lock GPU Clocks"), layout.NewSpacer(), w.gpuClockCheck)

	// Clipboard sync (Linux only): internal/clipboard's Linux backend shells
	// out to xclip/wl-clipboard/xsel, none of which every distro ships by
	// default (confirmed live: a Debian machine with wl-clipboard installed
	// but not xclip still failed every clipboard apply until one was
	// present). Offer a one-click pkexec install instead of a silent,
	// permanent "no clipboard tool available" failure the user has no way
	// to self-diagnose from this UI.
	w.clipboardToolBtn = widget.NewButton("Install", func() {
		if w.perms == nil {
			return
		}
		w.clipboardToolBtn.Disable()
		go func() {
			defer fyne.Do(func() {
				if w.clipboardToolBtn != nil {
					w.clipboardToolBtn.Enable()
				}
			})
			granted := w.perms.RequestClipboardTool()
			if !granted {
				if e, ok := w.perms.(interface{ LastAccessibilityError() string }); ok {
					if msg := e.LastAccessibilityError(); msg != "" {
						fyne.Do(func() { dialog.ShowError(fmt.Errorf("%s", msg), win) })
					}
				}
			}
			fyne.Do(w.refreshClipboardToolUI)
		}()
	})
	w.clipboardToolBtn.Importance = widget.HighImportance

	// "?" info button: shows the literal pkexec command Install would run,
	// before it runs it -- clicking Install itself pops a polkit password
	// prompt, which isn't the moment to first learn what's about to execute
	// as root.
	clipboardInfoBtn := widget.NewButtonWithIcon("", theme.InfoIcon(), func() {
		preview := ""
		if w.perms != nil {
			preview = w.perms.ClipboardInstallPreview()
		}
		if preview == "" {
			preview = "No supported package manager (or pkexec) was found on this system -- " +
				"clicking Install will show why, instead of a command preview."
		}
		dialog.ShowInformation("Clipboard Tool Install", preview, win)
	})
	w.clipboardToolRow = container.NewHBox(widget.NewLabel("Clipboard Tool"), layout.NewSpacer(), clipboardInfoBtn, w.clipboardToolBtn)

	// RustShine web client (WebRTC) toggle -- shown only while RustShine is
	// the active backend (see refreshRustShineUI). Built unconditionally
	// here (not gated by OS or entitlement) since it starts hidden and
	// refreshRustShineUI is what actually decides visibility on every tick,
	// same shape as clipboardToolRow above.
	w.rustshineWebRTCCheck = widget.NewCheck("", func(checked bool) {
		if w.token == nil {
			return
		}
		go func() {
			if err := w.token.SetRustShineWebRTCEnabled(checked); err != nil {
				fyne.Do(func() {
					if w.rustshineWebRTCCheck != nil {
						w.rustshineWebRTCCheck.Checked = !checked
						w.rustshineWebRTCCheck.Refresh()
					}
				})
			}
		}()
	})
	w.rustshineWebRTCRow = container.NewHBox(widget.NewLabel("RustShine Web (WebRTC)"), layout.NewSpacer(), w.rustshineWebRTCCheck)
	w.rustshineWebRTCRow.Hide()

	var permRows []fyne.CanvasObject
	if !showAccessButton && !showScreenCaptureButton && !linuxCapture {
		permRows = []fyne.CanvasObject{autostartRow, w.permInfo}
	} else {
		permRows = []fyne.CanvasObject{
			autostartRow,
			container.NewHBox(w.accessLabel, layout.NewSpacer(), w.accessBtn),
			screenCaptureRow,
		}
	}
	if gpuClockSupported {
		permRows = append(permRows, gpuClockRow)
	}
	if runtime.GOOS == "linux" {
		permRows = append(permRows, w.clipboardToolRow)
		w.refreshClipboardToolUI()
	}
	permRows = append(permRows, w.rustshineWebRTCRow)

	// Moonlight Clients — add (+) opens PIN dialog; icon+count opens list; ✕ removes all.
	moonlightAddBtn := widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		w.showMoonlightPINDialog(win)
	})
	w.moonlightBtn = widget.NewButtonWithIcon("0", theme.AccountIcon(), func() {
		w.showMoonlightClientsDialog(win)
	})
	moonlightDeleteAllBtn := newDangerGlyphButton(func() {
		dialog.ShowConfirm("Remove All Clients",
			"Remove all paired Moonlight devices?",
			func(yes bool) {
				if !yes || w.token == nil {
					return
				}
				go func() {
					clients, err := w.token.ListSunshineClients()
					if err != nil {
						return
					}
					for _, c := range clients {
						_ = w.token.UnpairSunshineClient(c.UniqueID)
					}
					// Unpairing only blocks future reconnects — a client
					// already mid-stream keeps going until the stream host
					// itself is restarted (same reasoning as
					// RegenerateMasterKey's own client wipe).
					_ = w.token.RestartSunshine()
					fyne.Do(func() {
						if w.moonlightBtn != nil {
							w.moonlightBtn.SetText("0")
						}
					})
				}()
			}, win)
	})
	permRows = append(permRows, container.NewHBox(
		widget.NewLabel("Moonlight Clients"),
		layout.NewSpacer(),
		moonlightAddBtn,
		w.moonlightBtn,
		moonlightDeleteAllBtn,
	))

	permContent := newTightVBox(permRows...)
	permBlock := newPanel("Permissions", permContent)

	// Column 2: Stats & Tailscale
	sunshinePort := w.cfg.SunshinePort
	if sunshinePort == 0 {
		sunshinePort = 47990
	}

	// supportBtn sits on the OS row (not the Streamer row below it) so it's
	// the first thing visible in the Status panel, one level up from the
	// per-streamer details -- an entitlement/license affordance, not
	// specific to whichever streamer happens to be active.
	w.supportBtn = newSupportButton("Support us", func() {
		w.showLicenseDialog(win)
	})
	osLabel := container.NewHBox(makeStatusLabel("OS:"), widget.NewLabel(capture.GetOSInfo()), layout.NewSpacer(), w.supportBtn)

	w.streamerNameLabel = widget.NewLabel(w.token.StreamerName())
	w.streamerVersionLabel = widget.NewLabel("")
	w.streamerVersionLabel.TextStyle.Italic = true
	// Fires the check in the engine process and returns immediately --
	// refreshRustShineUI's own 2s poll of EntitlementStatus is what shows
	// the "checking…"/new-version/error result, the same fire-and-forget
	// shape the "Download RustShine" button in showLicenseDialog already
	// uses (see CheckRustShineUpdateNow's doc comment for why this is safe
	// to not wait on here).
	w.rustshineUpdateBtn = widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() {
		go func() {
			if err := w.token.CheckRustShineUpdateNow(); err != nil {
				logrus.WithError(err).Warn("rustshine update check failed")
			}
		}()
	})
	w.rustshineUpdateBtn.Importance = widget.LowImportance
	streamerLabel := container.NewHBox(makeStatusLabel("Streamer:"), w.streamerNameLabel, w.streamerVersionLabel, w.rustshineUpdateBtn)

	// HTTP listen row
	httpVal := widget.NewLabel(fmt.Sprintf("%s:%d", w.cfg.EffectiveListenHost(), w.cfg.HTTPPort))
	httpWarn := makeWarningBadge()
	if !needsWarnBadge(w.cfg.EffectiveListenHost()) {
		httpWarn.Hide()
	}
	httpEditBtn := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() {
		w.showEditHTTPAddrDialog(win, httpVal, httpWarn)
	})
	httpRow := container.NewBorder(nil, nil,
		container.NewHBox(makeStatusLabel("HTTP:"), httpVal, httpWarn),
		httpEditBtn, nil)

	// Declare sunWebVal first so sunStreamEditBtn closure can capture it
	sunWebVal := widget.NewLabel(fmt.Sprintf("127.0.0.1:%d", sunshinePort))

	// Sunshine GameStream row — Moonlight clients connect here (port = web-1)
	sunStreamPort := sunshinePort - 1
	sunStreamIP := w.token.SunshineStreamHost()
	if sunStreamIP == "" {
		sunStreamIP = "0.0.0.0"
	}
	sunStreamVal := widget.NewLabel(fmt.Sprintf("%s:%d", sunStreamIP, sunStreamPort))
	sunStreamWarn := makeWarningBadge()
	if !needsWarnBadge(sunStreamIP) {
		sunStreamWarn.Hide()
	}
	sunStreamEditBtn := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() {
		w.showEditSunStreamDialog(win, sunStreamVal, sunStreamWarn, sunWebVal)
	})
	sunStreamRow := container.NewBorder(nil, nil,
		container.NewHBox(makeStatusLabel("Sunshine:"), sunStreamVal, sunStreamWarn),
		sunStreamEditBtn, nil)

	// Sun web / admin API row — always localhost; port editable (remove initial decl moved above)
	sunWebEyeBtn := widget.NewButtonWithIcon("", theme.VisibilityIcon(), func() {
		port := w.cfg.SunshinePort
		if port == 0 {
			port = 47990
		}
		w.showSunshineWebDialog(win, port)
	})
	sunWebEditBtn := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() {
		w.showEditSunPortDialog(win, sunWebVal, sunStreamVal)
	})
	w.sunWebSunshineRow = container.NewBorder(nil, nil,
		container.NewHBox(makeStatusLabel("Sun web:"), sunWebVal),
		container.NewHBox(sunWebEyeBtn, sunWebEditBtn), nil)

	// RustShine web-client row -- replaces sunWebSunshineRow above whenever
	// RustShine is active and its WebRTC endpoint is enabled (see
	// refreshRustShineUI); hidden by default until the first refresh tick
	// decides which of the two applies.
	// No Truncation here (unlike e.g. showSunshineWebDialog's copyRow
	// labels): those labels sit in a Border's *stretched* center slot, so
	// truncation shrinks them gracefully. This one sits in the Border's
	// *left* slot alongside "Web:" (see sunWebSunshineRow's own layout
	// above, which it mirrors) -- a left/right slot only ever gets its
	// MinSize, and a truncated Label's MinSize collapses to just the
	// ellipsis glyph, so this rendered as a bare "…" instead of the URL.
	var webURL *url.URL
	if parsed, err := url.Parse(rustshineWebURL); err == nil {
		webURL = parsed
	}
	sunWebLinkVal := widget.NewHyperlink(rustshineWebURL, webURL)
	sunWebLinkCopyBtn := widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() {
		win.Clipboard().SetContent(rustshineWebURL)
	})
	sunWebLinkInfoBtn := widget.NewButtonWithIcon("", theme.InfoIcon(), func() {
		dialog.ShowInformation("RustShine Web Client",
			"Open this link in a browser on any device to stream via RustShine's "+
				"built-in WebRTC client -- no Moonlight app needed. Uses the same "+
				"pairing/master key as everything else in this agent.",
			win)
	})
	w.sunWebRustshineRow = container.NewBorder(nil, nil,
		container.NewHBox(makeStatusLabel("Web:"), sunWebLinkVal),
		container.NewHBox(sunWebLinkInfoBtn, sunWebLinkCopyBtn), nil)
	w.sunWebRustshineRow.Hide()

	statsBlock := newPanel("Status", newTightVBox(osLabel, streamerLabel, httpRow, sunStreamRow, w.sunWebSunshineRow, w.sunWebRustshineRow))

	w.tsInfo = widget.NewLabel("Status: checking...\nAccount: not connected\nAddress: unavailable")
	w.tsInfo.Wrapping = fyne.TextWrapWord
	w.tsPeers = widget.NewRichTextFromMarkdown("")
	w.tsPeers.Wrapping = fyne.TextWrapWord

	// The actual "open a browser" action is centralized in the AuthURL handler
	// registered below (via SetAuthURLHandler) — it fires whenever tsnet
	// produces a fresh AuthURL, no matter what triggered it. This button only
	// sets awaitingLocalLogin so the handler knows THIS particular AuthURL was
	// asked for locally, and nudges the login so one actually gets generated.
	w.tsAuthBtn = widget.NewButton("Sign In With Google", func() {
		if w.ts == nil {
			w.setTailscaleInfo("service unavailable", "", "")
			return
		}

		w.tsAuthBtn.Disable()
		go func() {
			defer fyne.Do(w.tsAuthBtn.Enable)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			status, statusErr := w.ts.Status(ctx)
			if statusErr == nil && status != nil && status.LoggedIn {
				if err := w.ts.Logout(ctx); err != nil {
					fyne.Do(func() {
						w.setTailscaleInfo(fmt.Sprintf("logout error: %v", err), "", "")
					})
				}
				w.performRefresh()
				return
			}
			if !w.awaitingLocalLogin.CompareAndSwap(false, true) {
				return
			}
			fyne.Do(func() { w.setTailscaleInfo("starting login flow...", "", "") })
			authURL, err := w.ts.StartLogin(ctx)
			if err != nil {
				w.awaitingLocalLogin.Store(false)
				fyne.Do(func() { w.setTailscaleInfo(fmt.Sprintf("error: %v", err), "", "") })
				return
			}
			// Open directly from StartLogin's own return value instead of waiting
			// on SetAuthURLHandler alone: that handler is fed by tsnet's
			// printAuthURLLoop, a goroutine tsnet starts exactly once per Server
			// lifetime and permanently exits the moment login first succeeds (see
			// tsnet.Server.printAuthURLLoop — "state is Running; done"). After any
			// Sign Out + Sign In cycle within the same agent run, that loop is
			// already dead, so the handler never fires again and the button looked
			// broken no matter how many times it was clicked. StartLogin's own
			// 500ms status poll doesn't depend on that loop at all, so it keeps
			// working across repeated login cycles. The CompareAndSwap still guards
			// against double-opening if the (still-registered, occasionally still
			// alive) handler also fires for the same click.
			if authURL != "" && w.awaitingLocalLogin.CompareAndSwap(true, false) {
				parsed, parseErr := url.Parse(strings.TrimSpace(authURL))
				if parseErr != nil {
					logrus.Errorf("tailscale ui: failed to parse auth URL %q: %v", authURL, parseErr)
					fyne.Do(func() { w.setTailscaleInfo("invalid login URL received", "", "") })
					return
				}
				fyne.Do(func() {
					if w.app != nil {
						_ = w.app.OpenURL(parsed)
					}
					w.setTailscaleInfo("login link opened in browser", "", "")
				})
			}
		}()
	})

	if w.ts != nil {
		w.ts.SetAuthURLHandler(func(authURL string) {
			if !w.awaitingLocalLogin.CompareAndSwap(true, false) {
				// Triggered by something other than this window's own button (a
				// remote client's sync/register request, or tsnet's first-boot
				// auto-login) — don't pop a browser on this machine unasked.
				logrus.Infof("tailscale ui: AuthURL produced by a non-local trigger, not opening: %s", authURL)
				return
			}
			parsed, parseErr := url.Parse(strings.TrimSpace(authURL))
			if parseErr != nil {
				logrus.Errorf("tailscale ui: failed to parse auth URL %q: %v", authURL, parseErr)
				fyne.Do(func() { w.setTailscaleInfo("invalid login URL received", "", "") })
				return
			}
			logrus.Infof("tailscale ui: captured auth URL: %s", parsed.String())
			fyne.Do(func() {
				if w.app != nil {
					_ = w.app.OpenURL(parsed)
				}
				w.setTailscaleInfo("login link opened in browser", "", "")
			})
		})
	}

	tsPanel := newPanel("Tailscale", newTightVBox(
		container.NewBorder(nil, nil, nil, container.NewVBox(w.tsAuthBtn),
			container.NewVBox(w.tsInfo),
		),
		w.tsPeers,
	))

	// Layout construction
	col1 := container.NewVBox(permBlock)
	col2 := container.NewVBox(statsBlock)

	mainGrid := container.NewGridWithColumns(2, col1, col2)

	content := container.NewVBox(
		tsPanel,
		mainGrid,
		layout.NewSpacer(),
	)

	// Initial refresh
	w.performRefresh()
	w.refreshScreenCaptureUI()

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if win.Canvas() == nil {
				return
			}
			w.performRefresh()
		}
	}()

	bg := canvas.NewRectangle(design.ColorPanel)
	bodyContent := container.NewBorder(nil, nil, layout.NewSpacer(), layout.NewSpacer(), content)
	body := container.NewBorder(header, nil, nil, nil, container.NewPadded(bodyContent))
	stackLayers := []fyne.CanvasObject{bg, body}
	if v := strings.TrimSpace(appVersion); v != "" {
		versionLabel := canvas.NewText("v"+v, design.ColorTextMuted)
		versionLabel.TextSize = 10
		versionLabel.Alignment = fyne.TextAlignTrailing
		versionCorner := container.NewBorder(nil,
			container.NewPadded(container.NewHBox(layout.NewSpacer(), versionLabel)),
			nil, nil, nil)
		stackLayers = append(stackLayers, versionCorner)
	}
	win.SetContent(container.NewStack(stackLayers...))
	win.SetCloseIntercept(func() {
		if onClose != nil {
			onClose()
		}
		win.Close()
	})
	if w.ownsEngine {
		go w.promptForUpdate(win)
	}

	win.Show()
	w.app.Run()
}

// promptForUpdate runs the mandatory startup update check and, if a newer
// signed version is available, asks before installing it — replacing a
// forced silent update, which is jarring for a window the user is actively
// looking at. Only called for an engine-owning window (see ownsEngine); a
// --headless launch has no window to ask through and applies silently
// instead (see app.Start).
func (w *Window) promptForUpdate(parent fyne.Window) {
	manifest := update.Check(context.Background(), appVersion)
	if manifest == nil {
		return
	}

	fyne.Do(func() {
		d := dialog.NewConfirm(
			"Update Available",
			fmt.Sprintf("USBridge Agent %s is available (you have %s). Update now?", manifest.Version, appVersion),
			func(confirmed bool) {
				if !confirmed {
					logrus.WithField("component", "update").Info("update declined by user")
					return
				}
				// A progress dialog while this downloads — a silent
				// update that just sits there with no visible activity
				// is indistinguishable from having frozen.
				progress := showUpdateProgressDialog(parent, manifest.Version)
				go func() {
					err := update.DownloadAndApply(context.Background(), manifest, progress.Update)
					// A successful apply never returns here at all (it
					// hands off to a helper/relaunch and exits) —
					// reaching this point means it didn't.
					progress.Close()
					if err != nil {
						logrus.WithField("component", "update").WithError(err).Error("failed to apply update")
						fyne.Do(func() { dialog.ShowError(err, parent) })
					}
				}()
			},
			parent,
		)
		d.SetConfirmText("Update")
		d.SetDismissText("Not Now")
		d.Show()
	})
}

// updateProgress is a live handle to an in-flight update's progress
// dialog. Every method is safe to call from any goroutine (they hop to the
// UI thread via fyne.Do) and safe to call on a nil receiver.
type updateProgress struct {
	dialog *dialog.CustomDialog
	bar    *widget.ProgressBar
}

func showUpdateProgressDialog(parent fyne.Window, version string) *updateProgress {
	up := &updateProgress{bar: widget.NewProgressBar()}
	fyne.Do(func() {
		content := container.NewVBox(
			widget.NewLabel(fmt.Sprintf("Downloading version %s…", version)),
			up.bar,
		)
		up.dialog = dialog.NewCustomWithoutButtons("Updating…", content, parent)
		up.dialog.Resize(fyne.NewSize(360, 120))
		up.dialog.Show()
	})
	return up
}

// Update sets the progress bar's fraction from downloaded/total bytes —
// intended to be passed directly as internal/update's ProgressFunc.
func (up *updateProgress) Update(downloaded, total int64) {
	if up == nil || up.bar == nil || total <= 0 {
		return
	}
	fraction := float64(downloaded) / float64(total)
	fyne.Do(func() { up.bar.SetValue(fraction) })
}

// Close dismisses the progress dialog. A successful apply relaunches the
// whole agent before this would ever run, but the error path needs it to
// avoid leaving a stuck-looking dialog on screen.
func (up *updateProgress) Close() {
	if up == nil || up.dialog == nil {
		return
	}
	fyne.Do(func() { up.dialog.Hide() })
}

// refreshSupportButton keeps supportBtn's text/emphasis in sync with
// entitlement.Status on every refresh tick -- a linked supporter sees at a
// glance which backend they're on without opening the dialog; everyone
// else sees the same quiet, low-importance invitation every time (this
// button is the ONLY place this feature ever surfaces on its own — it
// never pops a dialog, badge, or notification unprompted).
func (w *Window) refreshSupportButton(st entitlement.Status) {
	if w.supportBtn == nil {
		return
	}
	switch {
	case st.ActiveBackend == "rustshine":
		w.supportBtn.SetText("RustShine active")
	case st.Linked:
		w.supportBtn.SetText("RustShine ready")
	default:
		w.supportBtn.SetText("Support us")
	}
}

// The four global licenses (see usbridge-entitlement-backend's
// desktopLicense.ts tier doc comment) as shown in showLicenseDialog's
// radio group -- exact row labels, used both to build the group and to
// tell rows apart in its OnChanged switch.
const (
	licenseRowSunshine            = "Sunshine (Open Source) — free"
	licenseRowRustShineFree       = "RustShine — Free"
	licenseRowRustShinePro        = "RustShine — Pro · $8/mo (4:4:4 color)"
	licenseRowRustShineEnterprise = "RustShine — Enterprise · $25/mo (session logs, team access)"
)

// tierDisplayName renders a bare tier string ("pro"/"enterprise") as the
// capitalized name used in confirm dialogs and status headlines.
func tierDisplayName(tier string) string {
	switch tier {
	case "pro":
		return "RustShine Pro"
	case "enterprise":
		return "RustShine Enterprise"
	default:
		return tier
	}
}

// showLicenseDialog is the single entry point for the whole hardware-bound
// RustShine license flow: pick one of the four global licenses, wait for a
// subscription checkout to land, download, switch, clear -- all as one
// dialog that re-renders itself as entitlement.Status changes, rather than
// a sequence of separate popups. Opened only by an explicit click on
// supportBtn — never shown automatically.
func (w *Window) showLicenseDialog(parent fyne.Window) {
	if parent == nil || w.token == nil {
		return
	}

	var dlg *widget.PopUp
	stopPoll := make(chan struct{})
	closeDialog := func() {
		select {
		case <-stopPoll:
		default:
			close(stopPoll)
		}
		if dlg != nil {
			dlg.Hide()
		}
	}

	titleLabel := canvas.NewText("RUSTSHINE — FASTER STREAMING", design.ColorTextMuted)
	titleLabel.TextSize = 11
	titleLabel.TextStyle.Bold = true
	xBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), func() { closeDialog() })
	titleRow := container.NewBorder(nil, nil, titleLabel, xBtn, nil)

	minWidth := canvas.NewRectangle(color.Transparent)
	minWidth.SetMinSize(fyne.NewSize(420, 1))

	body := container.NewVBox()

	// checkoutURLFallback: see its use in requestTier's click handler
	// below. Declared out here (not local to render) so it survives from
	// the goroutine's fyne.Do callback into the next render() call.
	var checkoutURLFallback string

	// pendingTierSwitch: set by requestTier right before opening a Pro/
	// Enterprise checkout, cleared once that tier actually lands AND
	// RustShine finishes staging -- at which point render auto-switches
	// the active backend to RustShine with no separate button click
	// needed. See render's own use of it below.
	var pendingTierSwitch string

	var render func(st entitlement.Status)
	render = func(st entitlement.Status) {
		body.RemoveAll()

		if pendingTierSwitch != "" && st.Tier == pendingTierSwitch && st.RustShineStaged && st.ActiveBackend != "rustshine" {
			pendingTierSwitch = ""
			go func() {
				_ = w.token.SetStreamBackend("rustshine")
				fyne.Do(func() { render(w.token.EntitlementStatus()) })
			}()
			return // this render call is stale -- the goroutine's own re-render shows the final state
		}

		switch {
		case st.LinkInProgress:
			body.Add(widget.NewLabel("Waiting for checkout to complete in your browser…"))
			body.Add(widget.NewProgressBarInfinite())
			// Previously there was no way out of this screen short of an
			// actual completed purchase or pollForLicenseTimeout (15
			// minutes) -- closing the dialog and reopening it (even via a
			// fresh "Support us" click) landed right back here, since
			// LinkInProgress lives on the App's entStatus, not this dialog.
			// A closed checkout tab with nothing bought had no way back to
			// the trial/buy buttons at all. CancelPurchase abandons the
			// background poll and clears LinkInProgress.
			cancelBtn := widget.NewButton("Cancel", func() {
				go func() {
					w.token.CancelPurchase()
					fyne.Do(func() { render(w.token.EntitlementStatus()) })
				}()
			})
			cancelBtn.Importance = widget.LowImportance
			body.Add(container.NewCenter(cancelBtn))

		case st.DownloadInProgress:
			body.Add(widget.NewLabel("Downloading RustShine…"))
			pb := widget.NewProgressBar()
			if st.Progress >= 0 {
				pb.SetValue(st.Progress)
			}
			body.Add(pb)

		case !st.Linked:
			// Only reached in the brief window before the very first
			// background bootstrapFreeTier lands (see app.go's
			// recheckEntitlement), or if this machine's hardware id can't
			// be determined at all -- there is no more manual "start
			// trial"/"buy" step to wait on before showing something real.
			body.Add(widget.NewLabel("Setting up…"))
			body.Add(widget.NewProgressBarInfinite())
			if st.LastError != "" {
				errText := canvas.NewText(st.LastError, design.ColorTextMuted)
				errText.TextStyle.Italic = true
				body.Add(errText)
			}

		case pendingTierSwitch != "" && st.Tier == pendingTierSwitch && !st.RustShineStaged:
			// Payment landed (Tier already flipped) but RustShine hasn't
			// finished downloading yet -- app.go's applyIssuedToken already
			// kicked that off in the background the moment the tier
			// landed; the check at the top of render switches to it
			// automatically the instant RustShineStaged flips true, no
			// button needed.
			body.Add(widget.NewRichTextFromMarkdown(fmt.Sprintf("**%s active** 🎉\n\nDownloading RustShine…", tierDisplayName(pendingTierSwitch))))
			body.Add(widget.NewProgressBarInfinite())

		default:
			var headline string
			switch st.Tier {
			case "pro":
				headline = "**RustShine Pro active** — 4:4:4 color unlocked 🎉"
			case "enterprise":
				headline = "**RustShine Enterprise active** 🎉"
			default:
				headline = "Pick a license below."
			}
			body.Add(widget.NewRichTextFromMarkdown(headline))

			if st.LastError != "" {
				errText := canvas.NewText(st.LastError, design.ColorTextMuted)
				errText.TextStyle.Italic = true
				body.Add(errText)
			}
			// checkoutURLFallback carries a URL that requestTier's click
			// handler obtained fine but couldn't hand to the OS browser
			// itself (e.g. no default browser registered, a sandboxed/
			// headless environment without xdg-open) -- previously that
			// failure was silently swallowed, so the click visibly did
			// nothing at all once the checkout link had already been
			// fetched. Shown as a copyable link so the purchase can still
			// be completed manually.
			if checkoutURLFallback != "" {
				linkURI, _ := url.Parse(checkoutURLFallback)
				fallback := widget.NewLabel("Couldn't open your browser automatically. Checkout link:")
				fallback.Wrapping = fyne.TextWrapWord
				body.Add(fallback)
				if linkURI != nil {
					link := widget.NewHyperlink(checkoutURLFallback, linkURI)
					link.Wrapping = fyne.TextWrapBreak
					copyBtn := newIconActionButton("Copy", theme.ContentCopyIcon(), func() {
						parent.Clipboard().SetContent(checkoutURLFallback)
					})
					body.Add(container.NewBorder(nil, nil, nil, copyBtn, link))
				}
			}

			// The single switch for all four global licenses (see
			// usbridge-entitlement-backend's desktopLicense.ts tier doc
			// comment): which row is selected reflects what's ACTUALLY
			// active right now (backend + tier), not just a purchase
			// intent -- picking Sunshine or free RustShine always takes
			// effect immediately (nothing to buy, nothing to confirm);
			// picking Pro/Enterprise while not already entitled to it
			// opens a subscription checkout instead of switching anything
			// yet (see requestTier).
			options := []string{
				licenseRowSunshine,
				licenseRowRustShineFree,
				licenseRowRustShinePro,
				licenseRowRustShineEnterprise,
			}
			current := licenseRowSunshine
			if st.ActiveBackend == "rustshine" {
				switch st.Tier {
				case "pro":
					current = licenseRowRustShinePro
				case "enterprise":
					current = licenseRowRustShineEnterprise
				default:
					current = licenseRowRustShineFree
				}
			}

			// switchToRustShine ensures RustShine is staged (downloading
			// it first if this is the very first time, reusing the
			// existing st.DownloadInProgress spinner case above -- no
			// separate button needed) and then makes it the active
			// backend. Used both for the plain Free row and for a
			// Pro/Enterprise row that's already paid for.
			switchToRustShine := func() {
				go func() {
					if !st.RustShineStaged {
						if err := w.token.DownloadRustShine(nil); err != nil {
							fyne.Do(func() { render(w.token.EntitlementStatus()) })
							return
						}
					}
					_ = w.token.SetStreamBackend("rustshine")
					fyne.Do(func() { render(w.token.EntitlementStatus()) })
				}()
			}

			// requestTier handles a Pro/Enterprise row click: switches
			// straight to RustShine if this hardware id already carries
			// that tier (or better) -- no need to pay twice -- otherwise
			// confirms, then opens a subscription checkout for it.
			// pollForLicense (app.go) picks up the completed purchase in
			// the background same as before; render's own top-of-function
			// check auto-switches the backend once it lands and RustShine
			// finishes staging.
			requestTier := func(tier string) {
				if st.Tier == tier || (tier == "pro" && st.Tier == "enterprise") {
					switchToRustShine()
					return
				}
				dialog.NewConfirm(
					fmt.Sprintf("Subscribe to %s?", tierDisplayName(tier)),
					fmt.Sprintf(
						"Opens Stripe checkout in your browser for the %s subscription. "+
							"Once payment completes, RustShine downloads and switches on automatically.",
						tierDisplayName(tier),
					),
					func(confirmed bool) {
						if !confirmed {
							render(w.token.EntitlementStatus()) // reset the radio's visual selection back to what's actually active
							return
						}
						checkoutURLFallback = ""
						pendingTierSwitch = tier
						go func() {
							checkoutURL, err := w.token.StartPurchase(tier)
							if err != nil {
								// StartPurchase already recorded st.LastError -- the
								// re-render below picks it up and shows it.
								fyne.Do(func() { render(w.token.EntitlementStatus()) })
								return
							}
							parsed, parseErr := url.Parse(checkoutURL)
							openErr := parseErr
							if parseErr == nil {
								openErr = w.app.OpenURL(parsed)
							}
							fyne.Do(func() {
								if openErr != nil {
									checkoutURLFallback = checkoutURL
								}
								render(w.token.EntitlementStatus())
							})
						}()
					},
					parent,
				).Show()
			}

			radio := widget.NewRadioGroup(options, nil)
			radio.Horizontal = false
			radio.SetSelected(current)
			radio.OnChanged = func(selected string) {
				if selected == current {
					return
				}
				switch selected {
				case licenseRowSunshine:
					go func() {
						_ = w.token.SetStreamBackend("sunshine")
						fyne.Do(func() { render(w.token.EntitlementStatus()) })
					}()
				case licenseRowRustShineFree:
					switchToRustShine()
				case licenseRowRustShinePro:
					requestTier("pro")
				case licenseRowRustShineEnterprise:
					requestTier("enterprise")
				}
			}
			body.Add(radio)
			// The "Web client (WebRTC)" toggle for RustShine lives in the
			// main window's Permissions column now (w.rustshineWebRTCRow),
			// not here -- it's useful to reach without opening this dialog.

			if st.Tier == "pro" || st.Tier == "enterprise" {
				note := widget.NewLabel("Picking a lower tier above only switches the active encoder locally -- it doesn't cancel your subscription. Contact support to cancel or change plans.")
				note.Wrapping = fyne.TextWrapWord
				note.TextStyle.Italic = true
				body.Add(note)
			}

			clearBtn := widget.NewButton("Forget this machine's license locally", func() {
				dialog.NewConfirm(
					"Forget license?",
					"Switches back to Sunshine and forgets the cached license token on this machine only -- it does NOT cancel a paid subscription. Re-opening this dialog immediately re-links to your account's real tier (free, or paid if still active).",
					func(confirmed bool) {
						if !confirmed {
							return
						}
						go func() {
							_ = w.token.ClearLicense()
							fyne.Do(func() { render(w.token.EntitlementStatus()) })
						}()
					},
					parent,
				).Show()
			})
			clearBtn.Importance = widget.LowImportance
			body.Add(container.NewCenter(clearBtn))
		}

		body.Refresh()
	}

	render(w.token.EntitlementStatus())

	// accountBody: the account-login/rebind section (see internal/account),
	// deliberately its own block below the entitlement one above rather
	// than folded into render's switch -- account login is orthogonal to
	// which entitlement state (trial/licensed/unlicensed) this machine is
	// currently in, and stays visible/interactable regardless of it (e.g.
	// logging in to pick up a purchased license while this machine is
	// still shown as "unlicensed" above, right up until the rebind lands).
	accountBody := container.NewVBox()
	var accountLoginURLFallback string
	var renderAccount func(acc account.Status)
	renderAccount = func(acc account.Status) {
		accountBody.RemoveAll()
		accountBody.Add(widget.NewSeparator())

		switch {
		case acc.LoginInProgress:
			accountBody.Add(widget.NewLabel("Waiting for Google login to complete in your browser…"))
			accountBody.Add(widget.NewProgressBarInfinite())
			cancelBtn := widget.NewButton("Cancel", func() {
				w.token.CancelAccountLogin()
				fyne.Do(func() { renderAccount(w.token.AccountStatus()) })
			})
			cancelBtn.Importance = widget.LowImportance
			accountBody.Add(container.NewCenter(cancelBtn))
			if accountLoginURLFallback != "" {
				linkURI, _ := url.Parse(accountLoginURLFallback)
				fallback := widget.NewLabel("Couldn't open your browser automatically. Login link:")
				fallback.Wrapping = fyne.TextWrapWord
				accountBody.Add(fallback)
				if linkURI != nil {
					link := widget.NewHyperlink(accountLoginURLFallback, linkURI)
					link.Wrapping = fyne.TextWrapBreak
					accountBody.Add(link)
				}
			}

		case acc.LoggedIn:
			accountBody.Add(widget.NewLabel(fmt.Sprintf("Signed in as %s", acc.Email)))
			if acc.LastError != "" {
				errText := canvas.NewText(acc.LastError, design.ColorTextMuted)
				errText.TextStyle.Italic = true
				accountBody.Add(errText)
			}
			if len(acc.Licenses) == 0 {
				accountBody.Add(widget.NewLabel("No desktop licenses on this account yet."))
			}
			for _, lic := range acc.Licenses {
				lic := lic
				row := widget.NewLabel(fmt.Sprintf("%s — %s", lic.Identifier, lic.Status))
				useBtn := widget.NewButton("Use this license on this device", func() {
					go func() {
						_ = w.token.RebindLicenseToThisDevice(lic.Identifier)
						fyne.Do(func() {
							renderAccount(w.token.AccountStatus())
							render(w.token.EntitlementStatus())
						})
					}()
				})
				useBtn.Importance = widget.LowImportance
				if lic.Status != "licensed" || acc.RebindInProgress {
					useBtn.Disable()
				}
				accountBody.Add(container.NewBorder(nil, nil, nil, useBtn, row))
			}
			logoutBtn := widget.NewButton("Log out", func() {
				go func() {
					_ = w.token.LogoutAccount()
					fyne.Do(func() { renderAccount(w.token.AccountStatus()) })
				}()
			})
			logoutBtn.Importance = widget.LowImportance
			accountBody.Add(container.NewCenter(logoutBtn))

		default:
			intro := widget.NewLabel("Already bought a license on another machine? Log in to move it here.")
			intro.Wrapping = fyne.TextWrapWord
			accountBody.Add(intro)
			if acc.LastError != "" {
				errText := canvas.NewText(acc.LastError, design.ColorTextMuted)
				errText.TextStyle.Italic = true
				accountBody.Add(errText)
			}
			loginBtn := widget.NewButton("Log in with Google account", func() {
				accountLoginURLFallback = ""
				go func() {
					loginURL, err := w.token.StartAccountLogin()
					if err != nil {
						fyne.Do(func() { renderAccount(w.token.AccountStatus()) })
						return
					}
					parsed, parseErr := url.Parse(loginURL)
					openErr := parseErr
					if parseErr == nil {
						openErr = w.app.OpenURL(parsed)
					}
					fyne.Do(func() {
						if openErr != nil {
							accountLoginURLFallback = loginURL
						}
						renderAccount(w.token.AccountStatus())
					})
				}()
			})
			loginBtn.Importance = widget.LowImportance
			accountBody.Add(container.NewCenter(loginBtn))
		}

		accountBody.Refresh()
	}
	initialAccStatus := w.token.AccountStatus()
	renderAccount(initialAccStatus)
	if initialAccStatus.LoggedIn {
		w.token.RefreshAccountLicenses()
	}

	content := container.NewVBox(titleRow, minWidth, widget.NewSeparator(), body, accountBody)
	cardBG := canvas.NewRectangle(design.ColorPanel)
	card := container.NewStack(cardBG, container.NewPadded(content))
	dlg = widget.NewModalPopUp(container.NewCenter(card), parent.Canvas())
	dlg.Show()

	// Polls while the dialog is open so LinkInProgress/DownloadInProgress
	// (and the account login/rebind's own in-progress flags) advance to
	// their next state on their own (a link completing in the browser, a
	// download finishing) without the user needing to close and reopen
	// this dialog to see it. Only actually re-renders a section when its
	// own snapshot changed since the last tick -- rebuilding every widget
	// unconditionally every 2s (the original version of this loop) is
	// wasteful and visibly flickers; the client's equivalent dialog had
	// the same pattern and it was actively destructive there (wiped an
	// in-progress sync-passphrase Entry on every tick, see
	// client/internal/gui/main_window_account.go's accountDialogSnapshot)
	// -- this dialog has no text Entry to lose, but the same fix still
	// removes the pointless flicker.
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		lastSt := w.token.EntitlementStatus()
		lastAcc := newAccountSnapshot(w.token.AccountStatus())
		for {
			select {
			case <-stopPoll:
				return
			case <-ticker.C:
			}
			st := w.token.EntitlementStatus()
			acc := w.token.AccountStatus()
			accSnap := newAccountSnapshot(acc)
			stChanged := st != lastSt
			accChanged := accSnap != lastAcc
			if !stChanged && !accChanged {
				continue
			}
			lastSt, lastAcc = st, accSnap
			fyne.Do(func() {
				if stChanged {
					render(st)
				}
				if accChanged {
					renderAccount(acc)
				}
			})
		}
	}()
}

func (w *Window) performRefresh() {
	go func() {
		status := uiStatus{}
		if w.ts != nil {
			status.tsStatus, _ = w.ts.Status(context.Background())
		}
		if w.perms != nil {
			status.accessGranted = w.perms.AccessibilityGranted()
		}
		var entStatus entitlement.Status
		if w.token != nil {
			if clients, err := w.token.ListSunshineClients(); err == nil {
				status.moonlightCount = len(clients)
			}
			entStatus = w.token.EntitlementStatus()
		}
		fyne.Do(func() {
			w.refreshSupportButton(entStatus)
			w.refreshRustShineUI(entStatus)
			if w.streamerNameLabel != nil && w.token != nil {
				w.streamerNameLabel.SetText(w.token.StreamerName())
			}
			if w.accessLabel != nil {
				accessName := "Accessibility"
				if runtime.GOOS == "linux" {
					accessName = "Input Control"
				}
				if status.accessGranted {
					w.accessLabel.SetText(accessName + ": ✅")
					if w.accessBtn != nil {
						w.accessBtn.Hide()
					}
				} else {
					w.accessLabel.SetText(accessName + ": ❌")
					if (runtime.GOOS == "darwin" || (runtime.GOOS == "linux" && capture.GetLinuxEnv() == "Wayland")) && w.accessBtn != nil {
						w.accessBtn.Show()
					}
				}
			}
			if w.moonlightBtn != nil {
				w.moonlightBtn.SetText(fmt.Sprintf("%d", status.moonlightCount))
			}
			w.refreshScreenCaptureUI()
			w.refreshClipboardToolUI()
			if runtime.GOOS != "darwin" && w.permInfo != nil && w.accessLabel != nil && w.screenCaptureLabel != nil {
				w.permInfo.SetText(fmt.Sprintf("%s\n%s", w.accessLabel.Text, w.screenCaptureLabel.Text))
			}
			w.refreshTailscaleWithStatus(status.tsStatus)
		})
	}()
}

func (w *Window) refreshTailscaleWithStatus(status *tailscale.Status) {
	if w.tsPeers == nil || w.tsInfo == nil {
		return
	}
	if w.ts == nil || status == nil {
		w.setTailscaleInfo("unavailable", "unavailable", "unavailable")
		w.tsPeers.ParseMarkdown("")
		if w.tsAuthBtn != nil {
			w.tsAuthBtn.SetText("Sign In With Google")
		}
		return
	}

	if !status.LoggedIn {
		w.setTailscaleInfo("signed out", "sign in required", "sign in to publish this agent")
		w.tsPeers.ParseMarkdown("")
		if w.tsAuthBtn != nil {
			w.tsAuthBtn.SetText("Sign In With Google")
		}
		return
	}

	endpoint := status.Self.IP4
	if endpoint == "127.0.0.1" || strings.TrimSpace(endpoint) == "" {
		endpoint = status.Self.DNSName
	}
	if strings.TrimSpace(endpoint) == "" {
		endpoint = status.Self.HostName
	}

	w.setTailscaleInfo(
		strings.ToLower(status.Backend),
		fallbackValue(status.Self.UserLogin, "connected"),
		fmt.Sprintf("%s (embedded)", endpoint),
	)

	if w.tsAuthBtn != nil {
		w.tsAuthBtn.SetText("Sign Out")
	}

	// Update active sessions
	var activePeers []string
	for _, p := range status.Peers {
		if !isActiveTailscalePeer(p) {
			continue
		}
		connType := "Relay (DERP)"
		if p.CurAddr != "" {
			connType = fmt.Sprintf("P2P DIRECT (%s)", p.CurAddr)
		} else if p.Relay != "" {
			connType = fmt.Sprintf("Relay (DERP %s)", p.Relay)
		}
		activePeers = append(activePeers, fmt.Sprintf("* **%s** (%s) - %s", fallbackValue(p.UserLogin, p.HostName), p.IP4, connType))
	}

	if len(activePeers) > 0 {
		w.tsPeers.ParseMarkdown(fmt.Sprintf("### Active Sessions:\n%s", strings.Join(activePeers, "\n")))
		w.tsPeers.Show()
	} else {
		w.tsPeers.ParseMarkdown("*No active remote controllers*")
		// Keep it visible but simple
		w.tsPeers.Show()
	}
}

func (w *Window) setTailscaleInfo(status, account, address string) {
	if w.tsInfo != nil {
		w.tsInfo.SetText(fmt.Sprintf("Status: %s\nAccount: %s\nAddress: %s", status, account, address))
	}
}

func fallbackValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func isActiveTailscalePeer(p tailscale.Peer) bool {
	if p.Active {
		return true
	}
	if strings.TrimSpace(p.CurAddr) != "" {
		return true
	}
	return false
}

func (w *Window) showTokenDialog(parent fyne.Window) {
	if parent == nil {
		return
	}

	linkLabel := widget.NewLabel("")
	linkLabel.Alignment = fyne.TextAlignCenter
	linkLabel.Wrapping = fyne.TextWrapBreak

	qrImage := canvas.NewImageFromResource(nil)
	qrImage.FillMode = canvas.ImageFillContain
	qrImage.SetMinSize(fyne.NewSize(200, 200))
	qrMessage := widget.NewLabel("")
	qrMessage.Alignment = fyne.TextAlignCenter
	qrMessage.Wrapping = fyne.TextWrapWord
	qrContent := container.NewCenter(qrImage)
	qrMessage.Hide()
	qrPanelBody := container.NewVBox(qrContent, qrMessage)

	copyLinkBtn := newIconActionButton("Copy Link", theme.ContentCopyIcon(), func() {
		masterKey := strings.TrimSpace(w.cfg.MasterKey)
		internalHost, tailscaleHost, protocol := w.quickConnectTargets()
		link := buildQuickConnectLink(internalHost, tailscaleHost, masterKey, protocol)
		if link != "" {
			parent.Clipboard().SetContent(link)
		}
	})
	regenerateBtn := newIconActionButton("Regenerate Key", theme.ViewRefreshIcon(), nil)

	topGap := spacerSize(1, 8)
	linkGap := spacerSize(1, 2)
	buttonGap := spacerSize(1, 6)
	closeTopGap := spacerSize(1, 8)
	closeBottomGap := spacerSize(1, 0)

	copyLinkSlot := container.NewCenter(container.NewGridWrap(fyne.NewSize(260, copyLinkBtn.MinSize().Height), copyLinkBtn))
	regenerateSlot := container.NewCenter(container.NewGridWrap(fyne.NewSize(260, regenerateBtn.MinSize().Height), regenerateBtn))
	linkActions := container.NewCenter(container.NewGridWithColumns(2,
		copyLinkSlot,
		regenerateSlot,
	))

	var tokenDialog *widget.PopUp
	closeDialogBtn := widget.NewButton("Close", func() {
		if tokenDialog != nil {
			tokenDialog.Hide()
		}
	})

	contentWidth := canvas.NewRectangle(color.Transparent)
	contentWidth.SetMinSize(fyne.NewSize(620, 1))
	contentBody := container.NewVBox(
		contentWidth,
		topGap,
		qrPanelBody,
		linkGap,
		container.NewPadded(linkLabel),
		buttonGap,
		linkActions,
		closeTopGap,
		container.NewCenter(closeDialogBtn),
		closeBottomGap,
	)
	// Remove the top margin completely to lift the QR code
	pL := canvas.NewRectangle(color.Transparent)
	pL.SetMinSize(fyne.NewSize(8, 1))
	pR := canvas.NewRectangle(color.Transparent)
	pR.SetMinSize(fyne.NewSize(8, 1))
	pB := canvas.NewRectangle(color.Transparent)
	pB.SetMinSize(fyne.NewSize(1, 0))
	dialogContent := container.NewBorder(nil, pB, pL, pR, contentBody)

	cardBG := canvas.NewRectangle(design.ColorPanel)
	dialogCard := container.NewStack(cardBG, dialogContent)
	dialogBody := container.NewCenter(dialogCard)

	// Create a local theme with zero padding only for this dialog
	compactTheme := &compactTheme{Theme: design.NewBrandTheme()}
	tokenDialog = widget.NewModalPopUp(container.NewThemeOverride(dialogBody, compactTheme), parent.Canvas())

	tokenDialog.Resize(parent.Canvas().Size())

	refreshDialogContent := func() {
		masterKey := strings.TrimSpace(w.cfg.MasterKey)
		if masterKey == "" {
			masterKey = "unavailable"
		}
		internalHost, tailscaleHost, protocol := w.quickConnectTargets()
		link := buildQuickConnectLink(internalHost, tailscaleHost, masterKey, protocol)

		linkLabel.SetText(link)
		if link == "" {
			copyLinkBtn.Disable()
		} else {
			copyLinkBtn.Enable()
		}

		if link == "" {
			qrImage.Resource = nil
			qrImage.Hide()
			qrMessage.Show()
			qrMessage.SetText("QR link unavailable until the agent has a reachable address.")
			return
		}

		pngBytes, err := qrcode.Encode(link, qrcode.Medium, 320)
		if err != nil {
			qrImage.Resource = nil
			qrImage.Hide()
			qrMessage.Show()
			qrMessage.SetText(fmt.Sprintf("QR unavailable: %v", err))
			return
		}

		qrImage.Resource = fyne.NewStaticResource("agent-token-qr.png", pngBytes)
		qrImage.Show()
		qrImage.Refresh()
		qrMessage.Hide()
		qrMessage.SetText("")
	}

	regenerateBtn.OnTapped = func() {
		if w.token == nil {
			return
		}
		cfg, err := w.token.RegenerateMasterKey()
		if err != nil {
			dialog.ShowError(err, parent)
			return
		}
		w.cfg = cfg
		refreshDialogContent()
	}

	refreshDialogContent()
	tokenDialog.Show()
}

func buildQuickConnectLink(internalHost, tailscaleHost, masterKey, protocol string) string {
	masterKey = strings.TrimSpace(masterKey)
	if masterKey == "" || masterKey == "unavailable" {
		return ""
	}
	if strings.TrimSpace(internalHost) == "" && strings.TrimSpace(tailscaleHost) == "" {
		return ""
	}

	values := url.Values{}
	if strings.TrimSpace(internalHost) != "" {
		values.Set("internal_host", strings.TrimSpace(internalHost))
	}
	if strings.TrimSpace(tailscaleHost) != "" {
		values.Set("tailscale_host", strings.TrimSpace(tailscaleHost))
	}
	values.Set("master_key", masterKey)
	if strings.TrimSpace(protocol) != "" {
		values.Set("protocol", strings.TrimSpace(protocol))
	}
	return fmt.Sprintf("usbridge://connect?%s", values.Encode())
}

func (w *Window) quickConnectTargets() (internalHost string, tailscaleHost string, protocol string) {
	internalHost = localQuickConnectIPv4()
	if w.ts != nil {
		if status, err := w.ts.Status(context.Background()); err == nil && status != nil && status.LoggedIn {
			switch {
			case strings.TrimSpace(status.Self.IP4) != "":
				tailscaleHost = strings.TrimSpace(status.Self.IP4)
			case strings.TrimSpace(status.Self.DNSName) != "":
				tailscaleHost = strings.TrimSpace(status.Self.DNSName)
			}
		}
	}

	if tailscaleHost != "" {
		return internalHost, tailscaleHost, "tailscale"
	}
	if internalHost != "" {
		return internalHost, "", "direct"
	}
	return "", "", ""
}

func localQuickConnectIPv4() string {
	return netutil.PreferredIPv4()
}

func (w *Window) showMoonlightClientsDialog(parent fyne.Window) {
	if w.token == nil || parent == nil {
		return
	}

	listBox := container.NewVBox()
	var dlg *widget.PopUp
	var refreshList func()

	refreshList = func() {
		go func() {
			clients, err := w.token.ListSunshineClients()
			fyne.Do(func() {
				listBox.RemoveAll()
				if err != nil {
					listBox.Add(widget.NewLabel("Error: " + err.Error()))
					listBox.Refresh()
					return
				}
				if len(clients) == 0 {
					emptyLabel := widget.NewLabel("No paired clients")
					emptyLabel.Alignment = fyne.TextAlignCenter
					listBox.Add(emptyLabel)
				} else {
					for _, c := range clients {
						c := c
						displayName := strings.TrimSpace(c.Name)
						if displayName == "" {
							// Sunshine often leaves the name blank — show the UUID instead
							displayName = c.UniqueID
						}
						nameLabel := widget.NewLabel(displayName)
						nameLabel.Truncation = fyne.TextTruncateEllipsis
						removeBtn := newDangerGlyphButton(func() {
							go func() {
								if err := w.token.UnpairSunshineClient(c.UniqueID); err != nil {
									log.Printf("[ui] unpair moonlight client: %v", err)
								} else {
									// Unpairing only blocks future reconnects
									// — a client already mid-stream keeps
									// going until the stream host itself is
									// restarted.
									_ = w.token.RestartSunshine()
								}
								refreshList()
							}()
						})
						row := container.NewBorder(nil, nil, nil, removeBtn, nameLabel)
						listBox.Add(row)
					}
				}
				listBox.Refresh()
			})
		}()
	}

	closeBtn := widget.NewButton("Close", func() {
		if dlg != nil {
			dlg.Hide()
		}
	})

	minWidth := canvas.NewRectangle(color.Transparent)
	minWidth.SetMinSize(fyne.NewSize(340, 1))

	titleLabel := canvas.NewText("MOONLIGHT CLIENTS", design.ColorTextMuted)
	titleLabel.TextSize = 11
	titleLabel.TextStyle.Bold = true

	content := container.NewVBox(
		titleLabel,
		minWidth,
		widget.NewSeparator(),
		listBox,
		widget.NewSeparator(),
		container.NewCenter(closeBtn),
	)

	cardBG := canvas.NewRectangle(design.ColorPanel)
	card := container.NewStack(cardBG, container.NewPadded(content))

	dlg = widget.NewModalPopUp(container.NewCenter(card), parent.Canvas())
	refreshList()
	dlg.Show()
}

func (w *Window) showMoonlightPINDialog(parent fyne.Window) {
	if w.token == nil || parent == nil {
		return
	}

	entry := widget.NewEntry()
	entry.SetPlaceHolder("4-digit PIN from Moonlight")

	statusLabel := widget.NewLabel("")
	statusLabel.Alignment = fyne.TextAlignCenter
	statusLabel.Hide()

	var dlg *widget.PopUp

	var submitBtn *widget.Button
	submitBtn = widget.NewButton("Submit", func() {
		pin := strings.TrimSpace(entry.Text)
		if len(pin) == 0 {
			statusLabel.SetText("Enter the PIN shown in Moonlight")
			statusLabel.Show()
			return
		}
		submitBtn.Disable()
		go func() {
			err := w.token.SubmitMoonlightPIN(pin)
			fyne.Do(func() {
				if err != nil {
					statusLabel.SetText("Error: " + err.Error())
					statusLabel.Show()
					submitBtn.Enable()
				} else {
					if dlg != nil {
						dlg.Hide()
					}
				}
			})
		}()
	})
	submitBtn.Importance = widget.HighImportance

	cancelBtn := widget.NewButton("Cancel", func() {
		if dlg != nil {
			dlg.Hide()
		}
	})

	titleLabel := canvas.NewText("PAIR MOONLIGHT CLIENT", design.ColorTextMuted)
	titleLabel.TextSize = 11
	titleLabel.TextStyle.Bold = true

	minWidth := canvas.NewRectangle(color.Transparent)
	minWidth.SetMinSize(fyne.NewSize(320, 1))

	content := container.NewVBox(
		titleLabel,
		minWidth,
		widget.NewSeparator(),
		widget.NewLabel("Open Moonlight → Add PC → enter PIN shown here:"),
		entry,
		statusLabel,
		widget.NewSeparator(),
		container.NewCenter(container.NewHBox(submitBtn, cancelBtn)),
	)

	cardBG := canvas.NewRectangle(design.ColorPanel)
	card := container.NewStack(cardBG, container.NewPadded(content))
	dlg = widget.NewModalPopUp(container.NewCenter(card), parent.Canvas())
	dlg.Show()
}

func (w *Window) showSunshineWebDialog(parent fyne.Window, port int) {
	if parent == nil {
		return
	}

	sunshineURL := fmt.Sprintf("https://127.0.0.1:%d", port)

	var dlg *widget.PopUp

	copyRow := func(labelText, valueText string) fyne.CanvasObject {
		lbl := canvas.NewText(labelText, design.ColorTextMuted)
		lbl.TextSize = 11
		lbl.TextStyle.Bold = true
		lblBox := container.NewGridWrap(fyne.NewSize(52, 16), lbl)
		val := widget.NewLabel(valueText)
		val.Truncation = fyne.TextTruncateEllipsis
		copyBtn := widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() {
			parent.Clipboard().SetContent(valueText)
		})
		return container.NewBorder(nil, nil, lblBox, copyBtn, val)
	}

	openBtn := widget.NewButtonWithIcon("Open in Browser", theme.ComputerIcon(), func() {
		if parsed, err := url.Parse(sunshineURL); err == nil {
			_ = w.app.OpenURL(parsed)
		}
	})

	titleLabel := canvas.NewText("SUNSHINE WEB UI", design.ColorTextMuted)
	titleLabel.TextSize = 11
	titleLabel.TextStyle.Bold = true

	xBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		if dlg != nil {
			dlg.Hide()
		}
	})
	titleRow := container.NewBorder(nil, nil, titleLabel, xBtn, nil)

	minWidth := canvas.NewRectangle(color.Transparent)
	minWidth.SetMinSize(fyne.NewSize(360, 1))

	// Password row: built dynamically so it reflects the value set by the
	// async bootstrap goroutine even if the dialog opens before it completes.
	passLabel := widget.NewLabel(w.token.AdminPass())
	passLabel.Truncation = fyne.TextTruncateEllipsis
	passLbl := canvas.NewText("Pass:", design.ColorTextMuted)
	passLbl.TextSize = 11
	passLbl.TextStyle.Bold = true
	passLblBox := container.NewGridWrap(fyne.NewSize(52, 16), passLbl)
	passCopyBtn := widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() {
		parent.Clipboard().SetContent(w.token.AdminPass())
	})
	passRow := container.NewBorder(nil, nil, passLblBox, passCopyBtn, passLabel)
	// If password is not yet available (bootstrap still running), poll until ready.
	if passLabel.Text == "" {
		go func() {
			for i := 0; i < 40; i++ {
				time.Sleep(500 * time.Millisecond)
				if p := w.token.AdminPass(); p != "" {
					fyne.Do(func() { passLabel.SetText(p) })
					return
				}
			}
		}()
	}

	content := container.NewVBox(
		titleRow,
		minWidth,
		widget.NewSeparator(),
		copyRow("URL:", sunshineURL),
		copyRow("Login:", w.token.AdminUser()),
		passRow,
		widget.NewSeparator(),
		container.NewCenter(openBtn),
	)

	cardBG := canvas.NewRectangle(design.ColorPanel)
	card := container.NewStack(cardBG, container.NewPadded(content))
	dlg = widget.NewModalPopUp(container.NewCenter(card), parent.Canvas())
	dlg.Show()
}

// showEditSunStreamDialog lets the user change the IP Sunshine advertises to
// Moonlight clients (external_ip in sunshine.conf) and the streaming port.
// The web/admin port is always streamPort+1. Changes are applied by restarting
// Sunshine immediately.
func (w *Window) showEditSunStreamDialog(parent fyne.Window, streamLabel *widget.Label, streamWarn *canvas.Text, webLabel *widget.Label) {
	if parent == nil {
		return
	}

	var dlg *widget.PopUp

	currentStreamPort := w.cfg.SunshinePort - 1
	if currentStreamPort <= 0 {
		currentStreamPort = 47989
	}
	currentIP := w.token.SunshineStreamHost()
	if currentIP == "" {
		currentIP = "0.0.0.0"
	}

	displays, valueFor, currentDisplay := ipSelectOptions(currentIP)
	hostSelect := widget.NewSelect(displays, nil)
	hostSelect.SetSelected(currentDisplay)

	portEntry := widget.NewEntry()
	portEntry.SetText(strconv.Itoa(currentStreamPort))

	errLabel := widget.NewLabel("")
	errLabel.Alignment = fyne.TextAlignCenter
	errLabel.Hide()

	var saveBtn *widget.Button
	saveBtn = widget.NewButton("Save", func() {
		host := valueFor[hostSelect.Selected]
		if host == "" {
			host = "0.0.0.0"
		}
		streamPort, err := strconv.Atoi(strings.TrimSpace(portEntry.Text))
		if err != nil || streamPort < 1 || streamPort > 65534 {
			errLabel.SetText("Invalid port (1–65534)")
			errLabel.Show()
			return
		}
		saveBtn.Disable()
		go func() {
			if w.token == nil {
				fyne.Do(func() { saveBtn.Enable() })
				return
			}
			cfg, err := w.token.UpdateSunshineStreamAddr(host, streamPort)
			fyne.Do(func() {
				if err != nil {
					errLabel.SetText("Error: " + err.Error())
					errLabel.Show()
					saveBtn.Enable()
					return
				}
				w.cfg = cfg
				streamLabel.SetText(fmt.Sprintf("%s:%d", host, streamPort))
				if needsWarnBadge(host) {
					streamWarn.Show()
					streamWarn.Refresh()
				} else {
					streamWarn.Hide()
					streamWarn.Refresh()
				}
				webLabel.SetText(fmt.Sprintf("127.0.0.1:%d", streamPort+1))
				if dlg != nil {
					dlg.Hide()
				}
			})
		}()
	})
	saveBtn.Importance = widget.HighImportance

	xBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		if dlg != nil {
			dlg.Hide()
		}
	})
	titleLabel := canvas.NewText("SUNSHINE STREAMING", design.ColorTextMuted)
	titleLabel.TextSize = 11
	titleLabel.TextStyle.Bold = true
	titleRow := container.NewBorder(nil, nil, titleLabel, xBtn, nil)

	noteLabel := canvas.NewText("Sets external_ip + port in sunshine.conf · restarts Sunshine", design.ColorTextMuted)
	noteLabel.TextSize = 10

	minWidth := canvas.NewRectangle(color.Transparent)
	minWidth.SetMinSize(fyne.NewSize(340, 1))

	content := container.NewVBox(
		titleRow, minWidth,
		widget.NewSeparator(),
		widget.NewLabel("IP (advertised to Moonlight clients):"), hostSelect,
		widget.NewLabel("Streaming port:"), portEntry,
		noteLabel, errLabel,
		widget.NewSeparator(),
		container.NewCenter(saveBtn),
	)

	cardBG := canvas.NewRectangle(design.ColorPanel)
	card := container.NewStack(cardBG, container.NewPadded(content))
	dlg = widget.NewModalPopUp(container.NewCenter(card), parent.Canvas())
	dlg.Show()
}

// needsWarnBadge reports whether a host binding warrants a ⚠ warning.
// Warns for all-interfaces (0.0.0.0/"") and LAN IPs — not for Tailscale or loopback.
func needsWarnBadge(host string) bool {
	if host == "" || host == "0.0.0.0" {
		return true
	}
	p := net.ParseIP(host)
	if p == nil {
		return false
	}
	if p.IsLoopback() || isTailscaleIP(p) {
		return false
	}
	return true // LAN, public, or unknown — show warning
}

// makeWarningBadge returns a yellow ⚠ badge for rows that listen on 0.0.0.0.
func makeWarningBadge() *canvas.Text {
	t := canvas.NewText("⚠", color.RGBA{R: 255, G: 185, B: 0, A: 255})
	t.TextSize = 13
	return t
}

// makeStatusLabel returns a small bold muted text node for status row labels.
func makeStatusLabel(text string) fyne.CanvasObject {
	t := canvas.NewText(text, design.ColorTextMuted)
	t.TextSize = 12
	t.TextStyle.Bold = true
	return t
}

// ipOption pairs a display string (annotated) with the actual IP value.
type ipOption struct{ display, value string }

func isTailscaleIP(ip net.IP) bool {
	_, cidr, _ := net.ParseCIDR("100.64.0.0/10")
	return cidr != nil && cidr.Contains(ip)
}

func isPrivateLANIP(ip net.IP) bool {
	for _, s := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		_, lan, _ := net.ParseCIDR(s)
		if lan != nil && lan.Contains(ip) {
			return true
		}
	}
	return false
}

func annotateIP(ipStr string) string {
	if ipStr == "0.0.0.0" {
		return "0.0.0.0  (all interfaces)"
	}
	p := net.ParseIP(ipStr)
	if p == nil {
		return ipStr
	}
	switch {
	case p.IsLoopback():
		return ipStr + "  (loopback)"
	case isTailscaleIP(p):
		return ipStr + "  (Tailscale)"
	case isPrivateLANIP(p):
		return ipStr + "  (LAN)"
	default:
		return ipStr
	}
}

// localIPOptions returns annotated IP options for host-binding dropdowns.
// Always includes 0.0.0.0 and 127.0.0.1 first, then active interface IPs.
func localIPOptions() []ipOption {
	raw := []string{"0.0.0.0", "127.0.0.1"}
	seen := map[string]bool{"0.0.0.0": true, "127.0.0.1": true}
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				ip, _, err := net.ParseCIDR(addr.String())
				if err != nil || ip.To4() == nil {
					continue
				}
				s := ip.String()
				if !seen[s] {
					seen[s] = true
					raw = append(raw, s)
				}
			}
		}
	}
	opts := make([]ipOption, 0, len(raw))
	for _, ip := range raw {
		opts = append(opts, ipOption{display: annotateIP(ip), value: ip})
	}
	return opts
}

// ipSelectOptions builds display list + value lookup for a widget.Select,
// and finds the display string matching currentVal.
func ipSelectOptions(currentVal string) (displays []string, valueFor map[string]string, currentDisplay string) {
	opts := localIPOptions()
	displays = make([]string, len(opts))
	valueFor = make(map[string]string, len(opts))
	for i, o := range opts {
		displays[i] = o.display
		valueFor[o.display] = o.value
		if o.value == currentVal {
			currentDisplay = o.display
		}
	}
	if currentDisplay == "" {
		currentDisplay = currentVal
	}
	return
}

// showEditHTTPAddrDialog opens a modal to change the agent's HTTP listen
// host and port. Updates valLabel and warnBadge immediately after save.
// The HTTP server itself is NOT hot-reloaded — the user must restart the app.
func (w *Window) showEditHTTPAddrDialog(parent fyne.Window, valLabel *widget.Label, warnBadge *canvas.Text) {
	if parent == nil {
		return
	}

	var dlg *widget.PopUp

	displays, valueFor, currentDisplay := ipSelectOptions(w.cfg.EffectiveListenHost())
	hostSelect := widget.NewSelect(displays, nil)
	hostSelect.SetSelected(currentDisplay)

	portEntry := widget.NewEntry()
	portEntry.SetText(strconv.Itoa(w.cfg.HTTPPort))

	errLabel := widget.NewLabel("")
	errLabel.Alignment = fyne.TextAlignCenter
	errLabel.Hide()

	var saveBtn *widget.Button
	saveBtn = widget.NewButton("Save", func() {
		host := valueFor[hostSelect.Selected]
		if host == "" {
			host = "0.0.0.0"
		}
		port, err := strconv.Atoi(strings.TrimSpace(portEntry.Text))
		if err != nil || port < 1 || port > 65535 {
			errLabel.SetText("Invalid port (1–65535)")
			errLabel.Show()
			return
		}
		saveBtn.Disable()
		go func() {
			if w.token == nil {
				fyne.Do(func() { saveBtn.Enable() })
				return
			}
			cfg, err := w.token.UpdateListenAddr(host, port)
			fyne.Do(func() {
				if err != nil {
					errLabel.SetText("Error: " + err.Error())
					errLabel.Show()
					saveBtn.Enable()
					return
				}
				w.cfg = cfg
				valLabel.SetText(fmt.Sprintf("%s:%d", cfg.EffectiveListenHost(), cfg.HTTPPort))
				if needsWarnBadge(cfg.EffectiveListenHost()) {
					warnBadge.Show()
					warnBadge.Refresh()
				} else {
					warnBadge.Hide()
					warnBadge.Refresh()
				}
				if dlg != nil {
					dlg.Hide()
				}
			})
		}()
	})
	saveBtn.Importance = widget.HighImportance

	xBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		if dlg != nil {
			dlg.Hide()
		}
	})
	titleLabel := canvas.NewText("HTTP LISTEN ADDRESS", design.ColorTextMuted)
	titleLabel.TextSize = 11
	titleLabel.TextStyle.Bold = true
	titleRow := container.NewBorder(nil, nil, titleLabel, xBtn, nil)

	minWidth := canvas.NewRectangle(color.Transparent)
	minWidth.SetMinSize(fyne.NewSize(300, 1))

	content := container.NewVBox(
		titleRow, minWidth,
		widget.NewSeparator(),
		widget.NewLabel("Host:"), hostSelect,
		widget.NewLabel("Port:"), portEntry,
		errLabel,
		widget.NewSeparator(),
		container.NewCenter(saveBtn),
	)

	cardBG := canvas.NewRectangle(design.ColorPanel)
	card := container.NewStack(cardBG, container.NewPadded(content))
	dlg = widget.NewModalPopUp(container.NewCenter(card), parent.Canvas())
	dlg.Show()
}

// showEditSunPortDialog opens a modal to change the Sunshine admin API port.
// Updates valLabel (Sun web) and streamLabel (Sunshine GameStream) immediately;
// also writes sunshine.conf and restarts Sunshine.
func (w *Window) showEditSunPortDialog(parent fyne.Window, valLabel *widget.Label, streamLabel *widget.Label) {
	if parent == nil {
		return
	}

	var dlg *widget.PopUp

	currentPort := w.cfg.SunshinePort
	if currentPort == 0 {
		currentPort = 47990
	}

	portEntry := widget.NewEntry()
	portEntry.SetText(strconv.Itoa(currentPort))

	errLabel := widget.NewLabel("")
	errLabel.Alignment = fyne.TextAlignCenter
	errLabel.Hide()

	var saveBtn *widget.Button
	saveBtn = widget.NewButton("Save", func() {
		port, err := strconv.Atoi(strings.TrimSpace(portEntry.Text))
		if err != nil || port < 1 || port > 65535 {
			errLabel.SetText("Invalid port (1–65535)")
			errLabel.Show()
			return
		}
		saveBtn.Disable()
		go func() {
			if w.token == nil {
				fyne.Do(func() { saveBtn.Enable() })
				return
			}
			cfg, err := w.token.UpdateSunshinePort(port)
			fyne.Do(func() {
				if err != nil {
					errLabel.SetText("Error: " + err.Error())
					errLabel.Show()
					saveBtn.Enable()
					return
				}
				w.cfg = cfg
				valLabel.SetText(fmt.Sprintf("127.0.0.1:%d", port))
				if streamLabel != nil {
					streamLabel.SetText(fmt.Sprintf("0.0.0.0:%d", port-1))
				}
				if dlg != nil {
					dlg.Hide()
				}
			})
		}()
	})
	saveBtn.Importance = widget.HighImportance

	xBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		if dlg != nil {
			dlg.Hide()
		}
	})
	titleLabel := canvas.NewText("SUNSHINE ADMIN PORT", design.ColorTextMuted)
	titleLabel.TextSize = 11
	titleLabel.TextStyle.Bold = true
	titleRow := container.NewBorder(nil, nil, titleLabel, xBtn, nil)

	noteLabel := canvas.NewText("Restarts Sunshine to apply", design.ColorTextMuted)
	noteLabel.TextSize = 11

	minWidth := canvas.NewRectangle(color.Transparent)
	minWidth.SetMinSize(fyne.NewSize(280, 1))

	content := container.NewVBox(
		titleRow, minWidth,
		widget.NewSeparator(),
		widget.NewLabel("Port:"), portEntry,
		noteLabel, errLabel,
		widget.NewSeparator(),
		container.NewCenter(saveBtn),
	)

	cardBG := canvas.NewRectangle(design.ColorPanel)
	card := container.NewStack(cardBG, container.NewPadded(content))
	dlg = widget.NewModalPopUp(container.NewCenter(card), parent.Canvas())
	dlg.Show()
}

func newPanel(title string, content fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(design.ColorHeader)
	bg.CornerRadius = design.RadiusMD
	bg.SetMinSize(fyne.NewSize(0, 1))

	shadow := canvas.NewRectangle(design.ColorShadow)
	shadow.CornerRadius = design.RadiusMD
	shadowTopGap := canvas.NewRectangle(color.Transparent)
	shadowTopGap.SetMinSize(fyne.NewSize(0, 4))
	shadowLeftGap := canvas.NewRectangle(color.Transparent)
	shadowLeftGap.SetMinSize(fyne.NewSize(1, 0))

	// Apply a dense theme (zero padding) only to the content inside the panel
	denseContent := container.NewThemeOverride(content, &headerButtonTheme{Theme: design.NewBrandTheme(), padding: 0})

	card := container.NewStack(
		container.NewBorder(shadowTopGap, nil, shadowLeftGap, nil, shadow),
		container.NewStack(
			bg,
			container.NewBorder(
				spacerSize(6, 6),
				spacerSize(6, 6),
				spacerSize(6, 6),
				spacerSize(6, 6),
				denseContent,
			),
		),
	)

	if strings.TrimSpace(title) == "" {
		return card
	}

	titleText := canvas.NewText(strings.ToUpper(title), design.ColorTextMuted)
	titleText.TextSize = 11
	titleText.TextStyle.Bold = true

	titleIndent := canvas.NewRectangle(color.Transparent)
	titleIndent.SetMinSize(fyne.NewSize(8, 0))
	titleRow := container.NewBorder(nil, nil, titleIndent, nil, titleText)

	return container.NewVBox(titleRow, card)
}

func newTightVBox(items ...fyne.CanvasObject) fyne.CanvasObject {
	rows := make([]fyne.CanvasObject, 0, len(items)*2)
	for i, item := range items {
		if item == nil {
			continue
		}
		if len(rows) > 0 && i > 0 {
			gap := canvas.NewRectangle(color.Transparent)
			gap.SetMinSize(fyne.NewSize(0, 2))
			rows = append(rows, gap)
		}
		rows = append(rows, item)
	}
	return container.NewVBox(rows...)
}

func spacerSize(width, height float32) fyne.CanvasObject {
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(width, height))
	return spacer
}

func newKeyValueRow(label string, value *widget.Label) fyne.CanvasObject {
	title := canvas.NewText(label+":", design.ColorTextLight)
	title.TextStyle.Bold = true
	title.TextSize = 14

	if value == nil {
		value = widget.NewLabel("")
	}
	value.Wrapping = fyne.TextWrapWord

	titleSlot := container.NewGridWrap(fyne.NewSize(72, title.MinSize().Height), title)
	return container.NewBorder(nil, nil, titleSlot, nil, value)
}

func newHeaderBar(left fyne.CanvasObject, right fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(design.ColorHeader)
	bg.SetMinSize(fyne.NewSize(0, 48))

	leftBox := container.NewHBox()
	if left != nil {
		leftBox.Add(left)
	}

	rightBox := container.NewHBox()
	if right != nil {
		rightBox.Add(right)
	}

	bar := container.NewPadded(container.NewBorder(nil, nil, leftBox, rightBox, nil))
	return container.NewStack(bg, bar)
}

func newBadge(text string, size fyne.Size) fyne.CanvasObject {
	bg := canvas.NewRectangle(design.ColorAccent)
	bg.CornerRadius = design.RadiusMD

	label := canvas.NewText(text, design.ColorBackground)
	label.TextStyle.Bold = true
	label.TextSize = 14

	return container.NewGridWrap(size, container.NewStack(bg, container.NewCenter(label)))
}

func wrapDialogButton(btn *widget.Button) fyne.CanvasObject {
	if btn == nil {
		return layout.NewSpacer()
	}
	slot := canvas.NewRectangle(color.Transparent)
	slot.SetMinSize(fyne.NewSize(0, 48))
	return container.NewStack(slot, btn)
}

func minFloat32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

type compactTheme struct {
	fyne.Theme
}

func (t *compactTheme) Size(name fyne.ThemeSizeName) float32 {
	if name == theme.SizeNamePadding {
		return 4
	}
	return t.Theme.Size(name)
}

func newLabelValue(labelText string, valueText *canvas.Text) fyne.CanvasObject {
	title := canvas.NewText(strings.ToUpper(labelText)+":", design.ColorTextMuted)
	title.TextSize = 10
	title.TextStyle.Bold = true

	// Container for the title with a fixed width
	titleBox := container.NewGridWrap(fyne.NewSize(55, 16), container.NewCenter(title))

	return container.NewHBox(titleBox, valueText)
}

type labelTheme struct {
	fyne.Theme
	textColor color.Color
	textSize  float32
}

func (t *labelTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameForeground {
		return t.textColor
	}
	return t.Theme.Color(name, v)
}

func (t *labelTheme) Size(name fyne.ThemeSizeName) float32 {
	if name == theme.SizeNameText {
		return t.textSize
	}
	if name == theme.SizeNamePadding {
		return 0
	}
	return t.Theme.Size(name)
}

type headerButtonTheme struct {
	fyne.Theme
	padding float32
}

func (t *headerButtonTheme) Size(name fyne.ThemeSizeName) float32 {
	if name == theme.SizeNamePadding {
		return t.padding
	}
	return t.Theme.Size(name)
}

// redGlyphTheme overrides the foreground colour to the error/red colour so that
// icons and text inside the themed container appear red while the background
// remains the standard button colour (no DangerImportance red fill).
type redGlyphTheme struct{ fyne.Theme }

func (t *redGlyphTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameForeground {
		return design.ColorError
	}
	return t.Theme.Color(name, v)
}

// newDangerGlyphButton returns a cancel-icon (✕) button with a red glyph on a
// normal (non-red) background — use instead of DangerImportance when only the
// icon should be red, not the whole button background.
func newDangerGlyphButton(tapped func()) fyne.CanvasObject {
	btn := widget.NewButtonWithIcon("", theme.CancelIcon(), tapped)
	return container.NewThemeOverride(btn, &redGlyphTheme{design.NewBrandTheme()})
}

type iconActionButton struct {
	widget.DisableableWidget
	Text     string
	Icon     fyne.Resource
	OnTapped func()
	hovered  bool
}

func newIconActionButton(label string, icon fyne.Resource, tapped func()) *iconActionButton {
	b := &iconActionButton{Text: label, Icon: icon, OnTapped: tapped}
	b.ExtendBaseWidget(b)
	return b
}

func (b *iconActionButton) SetText(text string) {
	b.Text = text
	b.Refresh()
}

func (b *iconActionButton) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(design.ColorSurfaceLight)
	bg.CornerRadius = design.RadiusMD

	icon := canvas.NewImageFromResource(b.Icon)
	icon.FillMode = canvas.ImageFillContain

	text := canvas.NewText(b.Text, design.ColorTextLight)
	text.TextStyle.Bold = true
	text.TextSize = 13

	objects := []fyne.CanvasObject{bg, icon, text}
	return &iconActionButtonRenderer{
		bg:      bg,
		icon:    icon,
		text:    text,
		button:  b,
		objects: objects,
	}
}

func (b *iconActionButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.Refresh()
}

func (b *iconActionButton) MouseOut() {
	b.hovered = false
	b.Refresh()
}

func (b *iconActionButton) MouseMoved(*desktop.MouseEvent) {}

func (b *iconActionButton) Tapped(*fyne.PointEvent) {
	if b.Disabled() {
		return
	}
	if b.OnTapped != nil {
		b.OnTapped()
	}
}

type iconActionButtonRenderer struct {
	bg      *canvas.Rectangle
	icon    *canvas.Image
	text    *canvas.Text
	button  *iconActionButton
	objects []fyne.CanvasObject
}

func (r *iconActionButtonRenderer) Destroy() {}

func (r *iconActionButtonRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)

	iconSize := float32(20)
	gap := float32(8)
	paddingX := float32(14)
	textSize := r.text.MinSize()
	contentWidth := iconSize + gap + textSize.Width

	startX := paddingX
	if size.Width > contentWidth+paddingX*2 {
		startX = (size.Width - contentWidth) / 2
	}

	r.icon.Resize(fyne.NewSize(iconSize, iconSize))
	r.icon.Move(fyne.NewPos(startX, (size.Height-iconSize)/2))
	r.text.Move(fyne.NewPos(startX+iconSize+gap, (size.Height-textSize.Height)/2))
	r.text.Resize(textSize)
}

func (r *iconActionButtonRenderer) MinSize() fyne.Size {
	iconSize := float32(20)
	gap := float32(8)
	paddingX := float32(14)
	paddingY := float32(10)
	textSize := r.text.MinSize()
	return fyne.NewSize(iconSize+gap+textSize.Width+paddingX*2, fyne.Max(iconSize, textSize.Height)+paddingY*2)
}

func (r *iconActionButtonRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *iconActionButtonRenderer) Refresh() {
	r.text.Text = r.button.Text
	if r.button.Disabled() {
		r.bg.FillColor = design.ColorSurface
		r.text.Color = design.ColorBorder
	} else if r.button.hovered {
		r.bg.FillColor = design.ColorHover
		r.text.Color = design.ColorTextLight
	} else {
		r.bg.FillColor = design.ColorSurfaceLight
		r.text.Color = design.ColorTextLight
	}
	r.bg.Refresh()
	r.text.Refresh()
	r.icon.Refresh()
}

type closeButton struct {
	widget.BaseWidget
	Text     string
	OnTapped func()
	hovered  bool
}

func newDangerButton(label string, tapped func()) fyne.CanvasObject {
	b := &closeButton{Text: label, OnTapped: tapped}
	b.ExtendBaseWidget(b)
	return b
}

func (b *closeButton) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(color.Transparent)
	bg.CornerRadius = design.RadiusMD
	bg.StrokeWidth = 1
	bg.StrokeColor = design.ColorError

	text := canvas.NewText(b.Text, design.ColorError)
	text.Alignment = fyne.TextAlignCenter
	text.TextStyle.Bold = true
	text.TextSize = 13

	content := container.NewStack(bg, container.NewPadded(text))
	return &closeButtonRenderer{
		bg:      bg,
		text:    text,
		button:  b,
		objects: []fyne.CanvasObject{content},
	}
}

type closeButtonRenderer struct {
	bg      *canvas.Rectangle
	text    *canvas.Text
	button  *closeButton
	objects []fyne.CanvasObject
}

func (r *closeButtonRenderer) Destroy() {}
func (r *closeButtonRenderer) Layout(size fyne.Size) {
	r.objects[0].Resize(size)
}
func (r *closeButtonRenderer) MinSize() fyne.Size {
	return fyne.NewSize(80, 32)
}
func (r *closeButtonRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}
func (r *closeButtonRenderer) Refresh() {
	if r.button.hovered {
		r.bg.FillColor = design.ColorError
		r.bg.StrokeColor = color.Transparent
		r.text.Color = color.Black
	} else {
		r.bg.FillColor = color.Transparent
		r.bg.StrokeColor = design.ColorError
		r.text.Color = design.ColorError
	}
	r.bg.Refresh()
	r.text.Refresh()
}

func (b *closeButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.Refresh()
}
func (b *closeButton) MouseOut() {
	b.hovered = false
	b.Refresh()
}
func (b *closeButton) MouseMoved(*desktop.MouseEvent) {}
func (b *closeButton) Tapped(*fyne.PointEvent) {
	if b.OnTapped != nil {
		b.OnTapped()
	}
}

// supportButton is a filled brand-green (#bafc81) button with black text,
// lightening slightly on hover — used for supportBtn. A stock widget.Button
// can't do this: Importance only ever picks from the theme's fixed palette,
// and its hover state blends theme.ColorNameHover over whatever background
// it has (see design.ColorHoverOverlay's doc comment), so a plain
// widget.Button here would still flip to that blended color on hover rather
// than a lightened version of its own green.
type supportButton struct {
	widget.BaseWidget
	Text     string
	OnTapped func()
	hovered  bool
}

func newSupportButton(label string, tapped func()) *supportButton {
	b := &supportButton{Text: label, OnTapped: tapped}
	b.ExtendBaseWidget(b)
	return b
}

func (b *supportButton) SetText(text string) {
	b.Text = text
	b.Refresh()
}

// boltIconResource is a plain lightning-bolt glyph drawn as an SVG, not the
// "⚡" emoji character — the emoji depends on the OS/font having a color-emoji
// glyph for it, which Fyne's bundled Windows font doesn't, so it rendered as
// a blank tofu box (looking like a stray leading space pushing the button's
// text off-center). An SVG resource is rasterized by Fyne itself, so it
// always renders identically on every platform.
var boltIconResource = fyne.NewStaticResource("bolt.svg",
	[]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000000" d="M7 2v11h3v9l7-12h-4l4-8z"/></svg>`))

func (b *supportButton) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(design.ColorBrandAccent)
	bg.CornerRadius = design.RadiusMD

	icon := canvas.NewImageFromResource(boltIconResource)
	icon.FillMode = canvas.ImageFillContain

	text := canvas.NewText(b.Text, color.Black)
	text.TextStyle.Bold = true
	text.TextSize = 13

	return &supportButtonRenderer{
		bg:      bg,
		icon:    icon,
		text:    text,
		button:  b,
		objects: []fyne.CanvasObject{bg, icon, text},
	}
}

func (b *supportButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.Refresh()
}
func (b *supportButton) MouseOut() {
	b.hovered = false
	b.Refresh()
}
func (b *supportButton) MouseMoved(*desktop.MouseEvent) {}
func (b *supportButton) Tapped(*fyne.PointEvent) {
	if b.OnTapped != nil {
		b.OnTapped()
	}
}

type supportButtonRenderer struct {
	bg      *canvas.Rectangle
	icon    *canvas.Image
	text    *canvas.Text
	button  *supportButton
	objects []fyne.CanvasObject
}

func (r *supportButtonRenderer) Destroy() {}

func (r *supportButtonRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)

	iconSize := float32(14)
	gap := float32(6)
	textSize := r.text.MinSize()
	contentWidth := iconSize + gap + textSize.Width
	startX := (size.Width - contentWidth) / 2

	r.icon.Resize(fyne.NewSize(iconSize, iconSize))
	r.icon.Move(fyne.NewPos(startX, (size.Height-iconSize)/2))
	r.text.Resize(textSize)
	r.text.Move(fyne.NewPos(startX+iconSize+gap, (size.Height-textSize.Height)/2))
}

func (r *supportButtonRenderer) MinSize() fyne.Size {
	paddingX := float32(16)
	paddingY := float32(8)
	iconSize := float32(14)
	gap := float32(6)
	textSize := r.text.MinSize()
	return fyne.NewSize(iconSize+gap+textSize.Width+paddingX*2, fyne.Max(iconSize, textSize.Height)+paddingY*2)
}

func (r *supportButtonRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *supportButtonRenderer) Refresh() {
	r.text.Text = r.button.Text
	if r.button.hovered {
		r.bg.FillColor = design.ColorBrandAccentHover
	} else {
		r.bg.FillColor = design.ColorBrandAccent
	}
	r.bg.Refresh()
	r.text.Refresh()
	r.icon.Refresh()
	r.Layout(r.button.Size())
}
