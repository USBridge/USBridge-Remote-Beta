package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"

	"usbridge_agent/assets"
	"usbridge_agent/internal/account"
	"usbridge_agent/internal/adminapi"
	"usbridge_agent/internal/api"
	"usbridge_agent/internal/audio"
	"usbridge_agent/internal/autostart"
	"usbridge_agent/internal/capture"
	"usbridge_agent/internal/clipboard"
	"usbridge_agent/internal/config"
	"usbridge_agent/internal/entitlement"
	"usbridge_agent/internal/hwid"
	"usbridge_agent/internal/input"
	"usbridge_agent/internal/netutil"
	"usbridge_agent/internal/permissions"
	"usbridge_agent/internal/streamhost"
	"usbridge_agent/internal/tailscale"
	"usbridge_agent/internal/ui"
	"usbridge_agent/internal/ui/design"
	"usbridge_agent/internal/update"
)

type deviceState struct {
	mu              sync.Mutex
	startedAt       time.Time
	devices         []api.DeviceInfo
	mountInProgress bool
	lastMountError  string
}

type App struct {
	cfgPath string
	cfg     config.Config

	state     *deviceState
	input     *input.Controller
	screen    *capture.Service
	perms     *permissions.Service
	ts        *tailscale.Service
	stream    streamhost.Backend
	tsProxy   *tailscale.StreamProxy
	server    *http.Server
	tsHTTP    *http.Server
	handler   http.Handler
	apiServer *api.Server
	fyneApp   fyne.App
	clipboard *clipboard.Manager
	adminSrv  *adminapi.Server

	// gpuClockArmed records whether applyGPUClockLock has already launched
	// the elevated lock daemon for this agent process, so repeated calls
	// (the sunshineWatchdog re-invokes startSunshine every 15s, and every
	// stream-host restart -- e.g. SetSunshineOutputName switching the
	// captured monitor -- gets a brand new stream-host PID) don't relaunch
	// the elevated helper, which would pop a fresh UAC prompt each time. The
	// daemon watches this *agent's* PID (see applyGPUClockLock), not the
	// stream host's, specifically so it only ever needs arming once per
	// agent run regardless of how many times the stream host itself
	// restarts -- a UAC prompt mid-switch can't be dismissed from a remote
	// session (it runs on the secure desktop), so the whole point is to get
	// consent once, up front, via the Permissions checkbox.
	gpuClockMu    sync.Mutex
	gpuClockArmed bool

	// exeDir/logPath are the same triple every streamhost.New* constructor
	// takes (alongside cfg.StateDir), stashed here so SetStreamBackend can
	// construct a replacement Backend on demand without New() needing to
	// export them separately.
	exeDir  string
	logPath string
	// streamMu serializes SetStreamBackend calls against each other (a GUI
	// click racing the entitlement watchdog's own downgrade, say) --
	// a.stream/a.streamKind must only ever be read/written while held.
	streamMu   sync.Mutex
	streamKind string // "sunshine" | "rustshine" -- bookkeeping only, mirrors which concrete type a.stream currently is

	// entMu guards the fields below, all touched from both the GUI/adminapi
	// goroutine (user clicks) and entitlementWatchdog's background goroutine.
	entMu         sync.Mutex
	entStatus     entitlement.Status
	entPollCancel context.CancelFunc // cancels an in-flight StartPurchase's post-checkout poll loop, if any

	// accMu guards the account-login fields below -- see StartAccountLogin's
	// doc comment. Separate mutex/status from entMu above: this is a
	// different login entirely (account.Status, not entitlement.Status),
	// touched from the same GUI goroutine but never from
	// entitlementWatchdog's background one.
	accMu         sync.Mutex
	accStatus     account.Status
	accPollCancel context.CancelFunc
}

// Start is the sole entry point from main(). It decides, based on mode and
// whether another instance's admin socket is already reachable, whether
// this process owns the engine (HTTP server, Sunshine, tsnet) or just
// attaches a GUI to one that's already running headless — see
// runThinClientGUI. This is what lets the same binary/AppImage work both as
// a `--headless` systemd/launchd/autostart service and as the normal GUI
// app without ever running two engines (and two Sunshine/tsnet instances)
// at once on the same machine.
func Start(headless bool, version string) error {
	cfgPath := resolveConfigPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	// EnsureState only creates cfg.StateDir if missing — needed up front so
	// the admin socket path is known, but otherwise side-effect-free (no
	// goroutines, no network binds), so probing before committing to owning
	// the engine is safe.
	if err := cfg.EnsureState(); err != nil {
		return err
	}

	socketPath := adminapi.SocketPath(cfg.StateDir)
	if client, dialErr := adminapi.Dial(socketPath); dialErr == nil {
		if headless {
			client.Close()
			return fmt.Errorf("usbridge-agent is already running (admin socket %s)", socketPath)
		}
		return runThinClientGUI(client)
	}

	// Mandatory startup update check — only here, not on a thin-GUI attach
	// above: this is exactly the branch where this process is about to
	// become the one actually owning the engine, so it's the only launch
	// path where replacing the on-disk binary and re-exec'ing is
	// safe/meaningful. A --headless launch has no GUI to ask through, so it
	// applies silently like before (CheckAndApply); a normal GUI launch
	// instead asks via a confirm dialog once the window exists — see
	// internal/ui's ShowAndRun, which runs the same Check/DownloadAndApply
	// pair gated on the user's answer.
	if headless {
		update.CheckAndApply(context.Background(), version)
	}

	instance, err := New()
	if err != nil {
		return err
	}
	return instance.Run(headless)
}

// runThinClientGUI shows the GUI backed by an already-running headless
// instance's admin socket instead of starting a second engine. Closing the
// window here does NOT stop the headless instance — only a process actually
// owning the engine (see App.Run/shutdownEngine) does that.
func runThinClientGUI(client *adminapi.Client) error {
	cfg, err := client.CurrentConfig()
	if err != nil {
		client.Close()
		return fmt.Errorf("fetch config from running instance: %w", err)
	}

	restoreXWayland := forceXWaylandForGUI()
	fyneApp := fyneapp.NewWithID("io.usbridge.agent")
	restoreXWayland()
	fyneApp.Settings().SetTheme(design.NewBrandTheme())
	fyneApp.SetIcon(assets.AppIcon)

	// Permission grants (uinput udev rule, KMS CAP_SYS_ADMIN) run through
	// pkexec, which needs a real polkit authentication agent for the
	// caller's login session. The headless instance behind client is a
	// systemd system service with no session of its own — pkexec run
	// inside it can never reach one, it falls back to a textual agent and
	// fails immediately ("Error opening current controlling terminal...").
	// This process, by contrast, is launched interactively in the user's
	// actual desktop session, where a polkit GUI agent (e.g.
	// polkit-kde-authentication-agent-1) is reachable — so run pkexec here
	// instead, against the same on-disk targets (/dev/uinput, the udev
	// rule, the daemon's own capexec launcher path), then tell the daemon
	// to notice and pick up the change.
	localPerms := permissions.New()
	token := &thinClientToken{Client: client, perms: localPerms}
	ui.NewWindow(fyneApp, cfg, localPerms, client, token).ShowAndRun(func() {
		client.Close()
	})
	return nil
}

// thinClientToken adapts *adminapi.Client for runThinClientGUI, overriding
// only the KMS capability methods to grant locally instead of proxying the
// pkexec call into the headless daemon — see runThinClientGUI for why.
type thinClientToken struct {
	*adminapi.Client
	perms *permissions.Service
}

func (t *thinClientToken) KMSCaptureGranted() bool {
	return t.perms.KMSCaptureGranted(t.Client.SunshineCapExecPath())
}

func (t *thinClientToken) RequestKMSCapture() bool {
	path := t.Client.SunshineCapExecPath()
	if path == "" {
		return false
	}
	if !t.perms.RequestKMSCapture(path) {
		return false
	}
	return t.Client.RecheckKMSCapture()
}

// currentInstance holds the most recently started App, set by Run just
// before it blocks -- the only external reader is NotifySessionChange
// (called from cmd/usbridge_agent/service_windows.go's SessionChange
// handler), which otherwise has no way to reach the *App that Start()
// constructs internally and never returns out to main.
var currentInstance struct {
	mu sync.Mutex
	a  *App
}

// NotifySessionChange restarts the active stream backend (see
// RestartSunshine) so it picks up whichever session Windows now reports as
// the active console session. Called by the USBridgeAgent Windows service's
// SessionChange handler on WTS_SESSION_LOGON/WTS_CONSOLE_CONNECT: a
// LocalSystem service never automatically re-homes an already-running
// gamestream-server child into a session that only becomes interactive
// after the service itself started (see internal/sessionlaunch's package
// doc, and internal/streamhost's rustshineProcess/useSessionBroker) -- this
// is what actually notices "a session just became available/changed" and
// forces the kill+relaunch that picks it up, instead of waiting on
// sunshineWatchdog's blind 15s poll (which would otherwise leave a client
// staring at dead/wrong-resolution video for up to that long after every
// login). No-op if no App is running yet (e.g. the notification races the
// service's own startup) or if RustShine isn't actually the active backend
// (Sunshine's own Windows service tooling, unused by this agent, handles
// this differently and doesn't need the restart).
func NotifySessionChange() {
	currentInstance.mu.Lock()
	a := currentInstance.a
	currentInstance.mu.Unlock()
	if a == nil {
		return
	}
	if a.currentStreamKind() != "rustshine" {
		return
	}
	log.Printf("[app] Windows session change detected, restarting rustshine to pick it up")
	if err := a.RestartSunshine(); err != nil {
		log.Printf("[app] restart after session change failed: %v", err)
	}
}

func New() (*App, error) {
	cfgPath := resolveConfigPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	if err := cfg.EnsureState(); err != nil {
		return nil, err
	}

	// Generate master key on first run.
	if strings.TrimSpace(cfg.MasterKey) == "" {
		key, err := api.GenerateMasterKey()
		if err != nil {
			return nil, fmt.Errorf("generate master key: %w", err)
		}
		cfg.MasterKey = key
		if err := config.Save(cfgPath, cfg); err != nil {
			log.Printf("[app] warning: failed to persist master key: %v", err)
		}
	}

	// The master key is used as an opaque secret string (never hex-decoded) —
	// this must match usbridge_client and the canonical usbridge server, which
	// both derive the HMAC/AES key via SHA256(rawMasterKeyBytes).
	masterKeyBytes := []byte(cfg.MasterKey)

	clipboardMgr := clipboard.NewManager(clipboard.NewBackend(nil), cfg.ClipboardMaxBytes)
	clipboardMgr.SetEnabled(cfg.ClipboardSyncEnabled)

	instance := &App{
		cfgPath:   cfgPath,
		cfg:       cfg,
		state:     &deviceState{startedAt: time.Now()},
		input:     input.New(),
		perms:     permissions.New(),
		ts:        tailscale.New(cfg.StateDir),
		clipboard: clipboardMgr,
	}
	// fyneApp is created lazily in Run(), only for a GUI-owning process — a
	// --headless engine never touches Fyne at all, so it never needs a
	// display connection (see Run).
	instance.exeDir = resolveExeDir()
	instance.logPath = filepath.Join(cfg.StateDir, "logs", "sunshine-stdout.log")
	instance.streamKind = "sunshine"

	// If this install was already switched to RustShine last run, pick it
	// back up from a cold start too — but only via checks that need no
	// network round-trip (Verify is pure Ed25519 signature+expiry, StagePath
	// is a stat call), so this can never make boot depend on the
	// entitlement backend being reachable. A background recheckEntitlement
	// (see entitlementWatchdog, started from Run) re-verifies against the
	// backend shortly after and downgrades to Sunshine if the cached token
	// no longer holds up.
	if cfg.PreferredBackend == "rustshine" {
		if hwID, err := hwid.Get(); err == nil {
			if _, err := entitlement.VerifyForHardware(cfg.EntitlementToken, hwID); err == nil {
				if _, err := os.Stat(entitlement.StagePath(cfg.StateDir)); err == nil {
					instance.streamKind = "rustshine"
				}
			}
		}
	}
	if instance.streamKind == "rustshine" {
		instance.stream = streamhost.NewRustshine(instance.exeDir, cfg.StateDir, instance.logPath)
		applyStreamSharedSecret(instance.stream, masterKeyBytes)
		applyStreamWebRTCEnabled(instance.stream, !cfg.RustShineWebRTCDisabled)
	} else {
		instance.stream = streamhost.NewDefault(instance.exeDir, cfg.StateDir, instance.logPath)
	}
	instance.screen = capture.New(instance.stream)
	instance.syncSunshineCaptureMode()
	instance.syncSunshineCapExec()
	apiServer := api.NewServerWithAuth(instance, masterKeyBytes, cfg.SunshinePort)
	instance.apiServer = apiServer
	handler := apiServer.Routes()
	instance.handler = handler
	instance.server = &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.EffectiveListenHost(), cfg.HTTPPort),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	instance.tsHTTP = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	instance.refreshLocalEntitlementStatus()
	if cfg.AccountToken != "" {
		instance.accStatus.LoggedIn = true
		instance.accStatus.Email = cfg.AccountEmail
		// Licenses populated lazily -- the License dialog's own open
		// triggers a refresh (see window.go), no need to hit the backend
		// on every agent launch before anyone's even looked.
	}
	return instance, nil
}

func resolveExeDir() string {
	if exePath, err := os.Executable(); err == nil {
		return filepath.Dir(exePath)
	}
	return "."
}

func resolveConfigPath() string {
	candidates := make([]string, 0, 8)
	// Under an AppImage, exeDir is the AppImage's read-only squashfs mount
	// (a fresh, ephemeral path each launch) — never usable as a config
	// location, so it's excluded both from the search and from the fallback
	// below (which would otherwise pick it and every later config.Save would
	// fail with "read-only file system").
	skipExeDir := runtime.GOOS == "linux" && os.Getenv("APPIMAGE") != ""
	if !skipExeDir {
		if exePath, err := os.Executable(); err == nil {
			exeDir := filepath.Dir(exePath)
			candidates = append(candidates,
				filepath.Join(exeDir, "config.yaml"),
				filepath.Clean(filepath.Join(exeDir, "..", "..", "..", "config.yaml")),
			)
		}
	}
	candidates = append(candidates, filepath.Join(".", "config.yaml"))
	var homeCandidate string
	if homeDir, err := os.UserHomeDir(); err == nil && homeDir != "" {
		homeCandidate = filepath.Join(homeDir, ".config", "usbridge-agent", "config.yaml")
		candidates = append(candidates, homeCandidate)
		if runtime.GOOS == "darwin" {
			// macOS: the UI saves via StateDir which defaults to ~/Library/Application Support/
			candidates = append(candidates,
				filepath.Join(homeDir, "Library", "Application Support", "usbridge-agent", "config.yaml"),
			)
		}
	}

	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if skipExeDir && homeCandidate != "" {
		return homeCandidate
	}
	return candidates[0]
}

// Run starts the engine (HTTP server, Sunshine, tsnet, admin socket) and
// then either blocks headlessly on ctx.Done() (headless==true — no Fyne
// driver ever touched, so no display connection is required) or shows the
// GUI window backed directly by this same in-process engine (headless==false).
func (a *App) Run(headless bool) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// See NotifySessionChange's doc comment for why this needs to be
	// reachable from outside the normal Start()->New()->Run() call chain.
	currentInstance.mu.Lock()
	currentInstance.a = a
	currentInstance.mu.Unlock()

	keepDisplayAwake(ctx)

	log.Printf("[app] starting http=%s:%d headless=%v", a.cfg.EffectiveListenHost(), a.cfg.HTTPPort, headless)

	a.startSunshine()
	autostart.RefreshX11SessionEnv()
	// See streamhost.ProcessWatcher's doc comment: without this, a crashed
	// stream host only gets noticed (and relaunched) on sunshineWatchdog's
	// next periodic tick, up to sunshineWatchdogInterval (15s) of dead air
	// on a connected client's screen -- confirmed live. startSunshine()
	// itself is always safe to call from here: it no-ops if something else
	// already restarted the process first (e.g. this same tick racing
	// sunshineWatchdog).
	if pw, ok := a.stream.(streamhost.ProcessWatcher); ok {
		pw.SetOnExit(a.startSunshine)
	}
	go a.sunshineWatchdog(ctx)
	go a.x11SessionEnvWatchdog(ctx)
	// Always started, even before ever linking -- recheckEntitlement no-ops
	// immediately (no network call) whenever cfg.EntitlementToken is
	// empty, so this is cheap, and it means a purchase/trial made
	// mid-session (via StartPurchase/StartFreeTrial, no restart) is
	// covered by the same ticker without needing separate "start the
	// watchdog now" bookkeeping.
	go a.entitlementWatchdog(ctx)
	go a.recheckEntitlement(ctx) // one immediate check, don't wait a full entitlementRecheckInterval after a restart
	go func() { _ = a.server.ListenAndServe() }()
	if a.clipboard != nil {
		go a.clipboard.Run(ctx)
	}

	a.initTailscale(ctx)

	if srv, err := adminapi.NewServer(adminapi.SocketPath(a.cfg.StateDir), a, a.perms, a.ts, func() config.Config { return a.cfg }); err != nil {
		// Non-fatal: the engine itself works fine without it, it just means
		// no separate GUI process can attach to this instance later.
		log.Printf("[app] warning: admin socket unavailable: %v", err)
	} else {
		a.adminSrv = srv
		go func() {
			if err := srv.Serve(); err != nil {
				log.Printf("[app] admin socket server error: %v", err)
			}
		}()
	}

	if headless {
		<-ctx.Done()
		a.shutdownEngine()
		return nil
	}

	restoreXWayland := forceXWaylandForGUI()
	a.fyneApp = fyneapp.NewWithID("io.usbridge.agent")
	restoreXWayland()
	a.fyneApp.Settings().SetTheme(design.NewBrandTheme())
	a.fyneApp.SetIcon(assets.AppIcon)

	go a.handleShutdown(ctx, cancel)
	win := ui.NewWindow(a.fyneApp, a.cfg, a.perms, a.ts, a)
	win.SetOwnsEngine(true)
	win.ShowAndRun(cancel)
	return nil
}

// keepDisplayAwake holds a system-wide power assertion for as long as the
// agent runs, on macOS only. Without this, the only thing keeping the
// display awake is Sunshine's own capture-session assertion (see
// startSunshine's "Sunshine display capture" assertion) -- which only exists
// once a capture session is already up, i.e. exactly when it's too late to
// matter: if the display went to sleep (or the whole system slept) during
// the idle stretch between the agent starting and the first incoming
// connection, VTCompressionSessionCreate fails outright with "Cannot create
// compression session: -12903" the moment Sunshine tries to start streaming,
// because macOS revokes hardware H.264/HEVC encoder access while the display
// isn't awake -- confirmed live against a Mac that had been running this
// agent for ~21h idle: every hardware encoder creation attempt on the next
// client connect failed with -12903 and streaming never started, while the
// identical flow against a Windows/Linux host (software encode, no
// "display must be awake" requirement) worked every time. That's also why it
// silently looks like "no video" instead of an obvious error: the agent's
// own HTTP API and Sunshine's process both stay up and keep answering
// requests, so nothing about the connection *looks* down.
//
// Spawning `caffeinate` for the agent's own lifetime (rather than rolling a
// cgo/IOKit power assertion) keeps this a plain subprocess, matching how the
// rest of this package already shells out to tailscale/sunshine/ffmpeg.
func keepDisplayAwake(ctx context.Context) {
	if runtime.GOOS != "darwin" {
		return
	}
	// -d: prevent display sleep -- the one VTCompressionSessionCreate actually
	// needs. -i: prevent idle system sleep too, so the display assertion is
	// never moot because the whole machine suspended around it. -s: also
	// prevent sleep on AC power, the ordinary configuration for a machine
	// left set up as a remote host.
	cmd := exec.CommandContext(ctx, "caffeinate", "-d", "-i", "-s")
	if err := cmd.Start(); err != nil {
		log.Printf("[app] warning: could not start caffeinate — display may sleep and break hardware video encoding: %v", err)
		return
	}
	log.Printf("[app] caffeinate started (pid=%d) — preventing display/system sleep while the agent runs", cmd.Process.Pid)
	go func() { _ = cmd.Wait() }()
}

// startSunshine is the guarded entry point every caller *except* the
// RustShine update flow itself should use: onExit (fired by
// watchProcessExit on literally any process exit, deliberate or not -- see
// its own doc comment) and sunshineWatchdog's periodic tick both go through
// this. Skipping while RustShineUpdateInProgress is set closes a real race
// confirmed live: stopRustShineForUpdate() calls a.stream.Stop() to release
// the .exe's file lock before entitlement.StageRustShine's rename, but that
// same Stop() also makes watchProcessExit fire onExit -- which used to call
// this function directly and relaunch the *old* binary on the spot,
// re-locking the file before the rename (or the periodic watchdog's own
// independent 15s tick, landing in the same window) ever got a chance to
// run. The update flow itself calls startSunshineNow directly (see
// checkRustShineUpdate/CheckRustShineUpdateNow's own failure-recovery
// calls, and RestartSunshine's self-contained stop+start for the success
// path) precisely so its own deliberate restarts aren't the ones this
// guard suppresses.
func (a *App) startSunshine() {
	a.entMu.Lock()
	updateInProgress := a.entStatus.RustShineUpdateInProgress
	a.entMu.Unlock()
	if updateInProgress {
		log.Printf("[app] startSunshine skipped -- rustshine update in progress, the update flow owns restarting it")
		return
	}
	a.startSunshineNow()
}

// startSunshineNow is startSunshine's actual body, callable directly by the
// RustShine update flow (see startSunshine's doc comment for why those
// callers need to bypass the RustShineUpdateInProgress guard rather than
// trip over it).
func (a *App) startSunshineNow() {
	if a.stream == nil {
		return
	}
	// Sync bind_address with external_ip so Sunshine only binds streaming
	// ports to the configured IP — but never to the agent's own tsnet IP:
	// tsnet is a userspace-only netstack with no kernel interface for that
	// address, so a real kernel bind() to it fails (or silently binds
	// nowhere reachable) on any host that isn't also running a full system
	// Tailscale client. Reachability over tsnet is handled instead by
	// restartStreamProxy, which relays tsnet's netstack to Sunshine's real
	// 127.0.0.1 ports.
	if tsIP := a.stream.ExternalIP(); tsIP != "" && tsIP != "0.0.0.0" && !a.isTsnetSelfIP(tsIP) {
		if err := a.stream.SetBindAddress(tsIP); err != nil {
			log.Printf("[app] warning: could not set Sunshine bind address: %v", err)
		}
	}
	if err := a.stream.Start(a.cfg.SunshinePort); err != nil {
		log.Printf("[app] failed to start Sunshine: %v", err)
	} else {
		a.applyGPUClockLock()
	}
	// startSunshine() is invoked unconditionally every sunshineWatchdogInterval
	// (see sunshineWatchdog) and is a no-op whenever Sunshine is already
	// reachable — which is true almost every tick. Only (re)build the tsnet
	// relay when it isn't already up: rebuilding it on every no-op tick tore
	// down and recreated its listeners mid-stream, racing the new listen
	// against the old one's async netstack teardown and intermittently
	// failing with "port is in use" — surfacing as flaky input (ENet control
	// relay) and video (RTP relay) during an active session.
	if a.tsProxy == nil {
		a.restartStreamProxy()
	}
}

// sunshineWatchdogInterval is how often sunshineWatchdog re-checks that
// Sunshine is still alive. Short enough to recover within a bounded time
// after a crash, long enough that a spell of Sunshine being genuinely busy
// (e.g. mid capability-probe on every new session negotiation) never looks
// like a restart storm.
const sunshineWatchdogInterval = 15 * time.Second

// sunshineWatchdog periodically re-invokes startSunshine so a crashed
// Sunshine process gets relaunched automatically instead of leaving
// streaming broken until the agent itself is restarted. This matters
// because the backend's Start()'s "already running" fast path only reflects
// reality once the exited process's Wait() goroutine has cleared its cmd
// (see streamhost.NewSunshine's Start) -- after that, calling startSunshine() again
// here is what actually notices and relaunches it. startSunshine() itself
// is always safe to call repeatedly: it no-ops whenever Sunshine (ours or
// externally managed) is already reachable.
func (a *App) sunshineWatchdog(ctx context.Context) {
	if a.stream == nil {
		return
	}
	ticker := time.NewTicker(sunshineWatchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.startSunshine()
		}
	}
}

// x11SessionEnvRefreshInterval is how often x11SessionEnvWatchdog re-syncs
// the X11 fallback's greeter-env files (see RefreshX11SessionEnv's doc
// comment for why this needs to happen at all). Much shorter than
// sunshineWatchdogInterval deliberately: this used to piggyback on that 15s
// ticker, but every one of those 15 seconds is dead air on a connected
// client's screen during the exact SDDM login/logout window this exists
// for (confirmed live) -- a dedicated, much tighter interval is what
// actually shrinks that window, and capture-kms's own
// BLANK_RECHECK_INTERVAL (250ms, only engaged once the *capture* side has
// independently noticed several seconds of blank content) is the other,
// finer-grained half of that same fix. Still cheap enough (a /proc scan,
// usually no writes at all once a session has settled) to run this often.
const x11SessionEnvRefreshInterval = 2 * time.Second

// x11SessionEnvWatchdog periodically re-syncs RefreshX11SessionEnv on its
// own schedule (see x11SessionEnvRefreshInterval's doc comment for why this
// isn't just folded into sunshineWatchdog), and piggybacks
// EnsureDisplayActive on the same tick -- confirmed live: a fresh SDDM login
// can leave a physical output completely dark (no active mode at all) even
// though the remote KMS capture keeps working fine, because the desktop
// environment's own display auto-configuration on session start doesn't
// reliably reapply whatever mode was working before. No-op on non-Linux
// builds.
func (a *App) x11SessionEnvWatchdog(ctx context.Context) {
	ticker := time.NewTicker(x11SessionEnvRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			autostart.RefreshX11SessionEnv()
			autostart.EnsureDisplayActive()
		}
	}
}

// isTsnetSelfIP reports whether host is the agent's own embedded tsnet
// node's tailnet IP (as opposed to a LAN IP or a different tailnet peer).
func (a *App) isTsnetSelfIP(host string) bool {
	if a.ts == nil {
		return false
	}
	srv, err := a.ts.Server()
	if err != nil {
		return false
	}
	ip4, _ := srv.TailscaleIPs()
	return ip4.IsValid() && ip4.String() == strings.TrimSpace(host)
}

// restartStreamProxy stops any running tsnet↔Sunshine stream relay and, if
// Tailscale is enabled, starts a new one bound to the current Sunshine
// stream port. Safe to call whenever Sunshine's port or Tailscale's
// enablement may have changed.
func (a *App) restartStreamProxy() {
	if a.tsProxy != nil {
		a.tsProxy.Stop()
		a.tsProxy = nil
	}
	if a.ts == nil || !a.cfg.TailscaleEnabled {
		return
	}
	basePort := a.cfg.SunshinePort - 1 // SunshinePort is the admin port; NvHTTP base = admin - 1
	a.tsProxy = a.ts.StartStreamProxy(basePort)
}

func (a *App) initTailscale(ctx context.Context) {
	if a.ts == nil {
		return
	}
	if a.cfg.TailscaleEnabled {
		go a.startTailscaleHTTP(ctx)
		// startSunshineTSNetForwarding is NOT started here: it binds the same
		// fixed ports (Sunshine/rustshine's NvHTTP/RTSP/control/video/audio
		// set) on the same tsnet.Server as StreamProxy (see restartStreamProxy,
		// called from startSunshine/sunshineWatchdog). Running both raced two
		// independent ListenPacket registrations for 47998/47999/48000 --
		// confirmed live: tsnet's netstack kept accepting the client's video/
		// audio ping packets ("[v1] Accept: UDP{...:47998} ok" in the tsnet
		// log) indefinitely, but gamestream-server never saw them ("waiting
		// for client video ping" timing out every ~4.5s, forever) because
		// forwardSunshineUDPPort's reader goroutine had silently died (no
		// error logging on ReadFrom failure) the moment StreamProxy's own
		// later bind attempt on the same port collided with it. StreamProxy
		// alone already covers this port set (TCP http/https/rtsp directly,
		// UDP control directly, UDP video/audio via RTSP SETUP-response
		// snooping) and additionally retries through the ECONNREFUSED
		// startup race -- this generic forwarder is redundant, and the two
		// running together is strictly worse than StreamProxy alone.
	}
}

func (a *App) startTailscaleHTTP(_ context.Context) {
	tsSrv, err := a.ts.Server()
	if err != nil {
		log.Printf("[app] tsnet server unavailable: %v", err)
		return
	}
	ln, err := tsSrv.Listen("tcp", fmt.Sprintf(":%d", a.cfg.HTTPPort))
	if err != nil {
		log.Printf("[app] tsnet listen error: %v", err)
		return
	}
	log.Printf("[app] tsnet http listening on :%d", a.cfg.HTTPPort)
	if err := a.tsHTTP.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Printf("[app] tsnet http server error: %v", err)
	}
}

// handleShutdown is used by the GUI-owning path only: it waits for ctx to be
// cancelled (e.g. the window's close intercept calling cancel), tears down
// the engine, then quits the Fyne app loop so ShowAndRun's blocking call in
// Run returns. The headless path calls shutdownEngine directly instead,
// since there's no Fyne loop to quit.
func (a *App) handleShutdown(ctx context.Context, cancel context.CancelFunc) {
	<-ctx.Done()
	a.shutdownEngine()
	fyne.Do(func() {
		a.fyneApp.Quit()
	})
}

// shutdownEngine tears down everything Run started except the GUI: the HTTP
// server(s), Sunshine, the tsnet stream proxy/service, and the admin socket.
func (a *App) shutdownEngine() {
	_ = a.server.Shutdown(context.Background())
	if a.tsHTTP != nil && a.tsHTTP.Addr != "" {
		_ = a.tsHTTP.Shutdown(context.Background())
	}
	if a.tsProxy != nil {
		a.tsProxy.Stop()
	}
	if a.ts != nil {
		_ = a.ts.Close()
	}
	if a.stream != nil {
		_ = a.stream.Stop()
	}
	if a.adminSrv != nil {
		_ = a.adminSrv.Close()
	}
}

func (a *App) RegenerateMasterKey() (config.Config, error) {
	key, err := api.GenerateMasterKey()
	if err != nil {
		return a.cfg, fmt.Errorf("generate master key: %w", err)
	}
	next := a.cfg
	next.MasterKey = key
	if err := a.SaveConfig(next); err != nil {
		return a.cfg, fmt.Errorf("save config: %w", err)
	}
	// Apply to the already-running HTTP server too — without this, the new
	// key only takes effect after a manual restart (SecurityMiddleware and
	// the Sync/AES-GCM handler both keep verifying against whatever key was
	// baked in at process start), even though config.yaml and a.cfg already
	// hold the new one.
	if a.apiServer != nil {
		a.apiServer.SetMasterKey([]byte(key))
	}
	// rustshine's own native WebRTC signaling endpoint (POST /webrtc/offer)
	// authenticates against a copy of the master key handed to it at launch
	// (see rustshineBackend.SetSharedSecret's doc comment) -- refresh it
	// here too, before RestartSunshine below picks it up on the next
	// Start(), same as apiServer.SetMasterKey does for the agent's own API.
	applyStreamSharedSecret(a.stream, []byte(key))
	// A regenerated master key is meant to revoke access wholesale, but
	// Sunshine's Moonlight pairing is its own cert-based handshake that the
	// master key never governed — a client paired under the old key would
	// otherwise keep streaming right through the rotation. Best-effort and
	// non-fatal: the key has already been rotated above regardless of
	// whether any of this succeeds.
	if clients, err := a.ListSunshineClients(); err == nil {
		for _, c := range clients {
			_ = a.UnpairSunshineClient(c.UniqueID)
		}
	}
	// Unpairing alone only stops *future* pairing/reconnect attempts — a
	// session that was already streaming keeps going, since Sunshine
	// doesn't re-check its authorized-client list against connections it
	// already accepted. Restarting the stream host is what actually drops
	// those live sessions, so the key rotation above can't be quietly
	// undermined by a session that predates it.
	if err := a.RestartSunshine(); err != nil {
		log.Printf("[app] failed to restart stream host after master key regeneration: %v", err)
	}
	return a.cfg, nil
}

// SunshineBinaryPath returns the path to the bundled Sunshine binary, or ""
// if it isn't present (not bundled, or unsupported OS).
func (a *App) SunshineBinaryPath() string {
	if a.stream == nil {
		return ""
	}
	path := a.stream.BinaryPath()
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// SunshineCapExecPath returns the path to the bundled sunshine_capexec
// launcher (Linux KMS capture only), or "" if not present.
func (a *App) SunshineCapExecPath() string {
	if a.stream == nil {
		return ""
	}
	path := a.stream.CapExecPath()
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// syncSunshineCaptureMode writes the capture mode into the active backend's
// own config file. On Linux this is never taken from user input or a stale
// persisted preference — it's unconditionally re-derived from the live
// session via capture.AutoCaptureMode() and (re)written on every start, even
// over an existing on-disk value, so a value written by some earlier version
// of this agent (back when the mode was user-selectable, e.g. a leftover
// "capture = kms" in sunshine.conf from a desktop X11 session) self-heals on
// the next start instead of staying wrong forever. See capture.AutoCaptureMode's
// doc for why Wayland/headless/SDDM/console all keep getting "kms" while a
// live X11 desktop session gets "x11".
//
// Non-Linux platforms have no such concept (AutoCaptureMode returns ""
// there): there, the old behavior is kept exactly — only fill in the
// backend's capture mode from the persisted cfg value when the backend
// doesn't already have its own value set, so switching streamers still
// propagates a preference instead of overwriting it every start.
func (a *App) syncSunshineCaptureMode() {
	if a.stream == nil {
		return
	}
	if mode := capture.AutoCaptureMode(); mode != "" {
		if err := a.stream.SetCaptureMode(mode); err != nil {
			log.Printf("[app] failed to sync capture mode %q to backend: %v", mode, err)
		}
		return
	}
	if a.stream.CaptureMode() != "" || a.cfg.SunshineCaptureMode == "" {
		return
	}
	mode := a.cfg.SunshineCaptureMode
	if err := a.stream.SetCaptureMode(mode); err != nil {
		log.Printf("[app] failed to sync capture mode %q to backend: %v", mode, err)
	}
}

// syncSunshineCapExec sets or clears the backend's sunshine_capexec launcher
// so Start launches Sunshine with CAP_SYS_ADMIN exactly when the capture
// mode is "kms" AND the capability is actually granted on that launcher —
// never based on mode alone, since sunshine_capexec exits with an error if
// asked to raise a capability it doesn't have, which would stop Sunshine
// from starting at all instead of gracefully running without KMS.
func (a *App) syncSunshineCapExec() {
	if a.stream == nil {
		return
	}
	capexecPath := a.SunshineCapExecPath()
	if a.SunshineCaptureMode() == "kms" && a.perms != nil && a.perms.KMSCaptureGranted(capexecPath) {
		a.stream.SetCapExecPath(capexecPath)
	} else {
		a.stream.SetCapExecPath("")
	}
}

// SunshineCaptureMode returns the configured Linux capture backend ("",
// "portal", or "kms"), read from sunshine.conf if present, falling back to
// the persisted agent config.
func (a *App) SunshineCaptureMode() string {
	if a.stream != nil {
		if mode := a.stream.CaptureMode(); mode != "" {
			return mode
		}
	}
	return a.cfg.SunshineCaptureMode
}

// SetSunshineCaptureMode re-applies the capture backend into both the agent
// config and Sunshine's own sunshine.conf, then restarts the bundled
// Sunshine instance so the change actually takes effect — a config edit
// alone is silently ignored by an already-running Sunshine process.
//
// On Linux the requested mode is not a free-form choice: it's overridden by
// capture.AutoCaptureMode() so this can't be pointed at a combination known
// not to work (see its doc). The mode argument only takes effect on other
// platforms, where AutoCaptureMode returns "" and has no opinion.
//
// Switching to "kms" without the capability granted yet is deliberately NOT
// restarted here: Sunshine would immediately fail KMS and fall back to
// portal, popping its portal permission dialog right after the user picked
// KMS — confusing, and pointless since RequestKMSCapture already restarts
// once the capability is actually granted.
func (a *App) SetSunshineCaptureMode(mode string) error {
	if a.stream == nil {
		return nil
	}
	if auto := capture.AutoCaptureMode(); auto != "" {
		mode = auto
	}
	if err := a.stream.SetCaptureMode(mode); err != nil {
		return fmt.Errorf("write sunshine.conf: %w", err)
	}
	next := a.cfg
	next.SunshineCaptureMode = mode
	if err := a.SaveConfig(next); err != nil {
		return err
	}
	a.syncSunshineCapExec()
	if mode == "kms" && !a.KMSCaptureGranted() {
		return nil
	}
	if err := a.RestartSunshine(); err != nil {
		log.Printf("[app] failed to restart Sunshine after capture mode change: %v", err)
	}
	return nil
}

// RestartSunshine stops and relaunches the bundled Sunshine instance (if the
// agent owns its lifecycle) so a config or capability change takes effect.
func (a *App) RestartSunshine() error {
	if a.stream == nil {
		return nil
	}
	_ = a.stream.Stop()
	time.Sleep(time.Second)
	err := a.stream.Start(a.cfg.SunshinePort)
	if err == nil {
		a.applyGPUClockLock()
	}
	// Start() only waits for the OS to fork the Sunshine process, not for its
	// own bootstrap (config parse, KMS/Wayland monitor enumeration, binding
	// its HTTPS/RTSP listeners) to finish. Callers of RestartSunshine (e.g.
	// SetSunshineOutputName, switching the captured monitor) return straight
	// to a client that immediately reconnects — without this wait, that
	// reconnect can race Sunshine's bootstrap and land a session whose
	// control-stream encryption never gets fully wired up, which then fails
	// to decrypt every subsequent input packet for the rest of that session.
	// Waiting here for the same admin port the client's own Launch() call
	// hits closes that window.
	if err == nil {
		a.stream.WaitReady(a.cfg.SunshinePort, 5*time.Second)
		a.waitForMonitorCorrelation()
	}
	a.restartStreamProxy()
	return err
}

// SetStreamBackend switches the active streamhost.Backend at runtime
// between "sunshine" and "rustshine" -- stops whichever is running, builds
// the other, and starts it. No-op if kind is already active. "rustshine"
// requires the binary to already be staged (see entitlement.StageRustShine)
// -- this method never downloads it itself, so callers (the GUI's license
// dialog) must download-then-switch, not switch-then-download.
//
// Persists the choice as cfg.PreferredBackend so a restart picks it back up
// (see New()'s local, offline re-derivation of the initial backend) without
// needing to ask the entitlement backend again just to boot.
func (a *App) SetStreamBackend(kind string) error {
	if kind != "sunshine" && kind != "rustshine" {
		return fmt.Errorf("unknown stream backend %q", kind)
	}

	a.streamMu.Lock()
	defer a.streamMu.Unlock()

	if kind == a.streamKind {
		return nil
	}
	if kind == "rustshine" {
		if _, err := os.Stat(entitlement.StagePath(a.cfg.StateDir)); err != nil {
			return fmt.Errorf("rustshine is not downloaded yet")
		}
	}

	if a.stream != nil {
		_ = a.stream.Stop()
		// Mirrors RestartSunshine's own wait for the same reason: Start()'s
		// "already running" fast path only reflects reality once the
		// exited process's Wait() goroutine has cleared its cmd.
		time.Sleep(time.Second)
	}

	var next streamhost.Backend
	if kind == "rustshine" {
		next = streamhost.NewRustshine(a.exeDir, a.cfg.StateDir, a.logPath)
		applyStreamSharedSecret(next, []byte(a.cfg.MasterKey))
		applyStreamWebRTCEnabled(next, !a.cfg.RustShineWebRTCDisabled)
	} else {
		next = streamhost.NewSunshine(a.exeDir, a.cfg.StateDir, a.logPath)
	}
	a.stream = next
	a.streamKind = kind
	if a.screen != nil {
		a.screen.SetDevices(next)
	}
	a.syncSunshineCaptureMode()
	a.syncSunshineCapExec()
	if pw, ok := next.(streamhost.ProcessWatcher); ok {
		pw.SetOnExit(a.startSunshine)
	}

	a.startSunshine() // generic despite the name -- starts whatever a.stream now is
	a.waitForMonitorCorrelation()
	a.restartStreamProxy()

	saved := a.cfg
	saved.PreferredBackend = kind
	if err := a.SaveConfig(saved); err != nil {
		log.Printf("[app] warning: failed to persist preferred stream backend: %v", err)
	}
	return nil
}

func (a *App) currentStreamKind() string {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	return a.streamKind
}

func (a *App) rustshineStaged() bool {
	_, err := os.Stat(entitlement.StagePath(a.cfg.StateDir))
	return err == nil
}

// refreshLocalEntitlementStatus re-derives entStatus's Linked/Tier/ExpiresAt
// fields purely from cfg.EntitlementToken (local Ed25519 verify, no
// network) -- called after anything that changes that token, and once at
// startup. LinkInProgress/DownloadInProgress/LastError are process-only
// state, untouched here.
//
// Also the single place that mirrors cfg.EntitlementToken out to
// entitlement.TokenFilePath (see that function's doc comment) -- every
// call site that changes the token already calls this afterward, so
// centralizing the file write here means none of them need to remember to
// do it themselves. Only written while the token verifies locally as
// current -- there's no reason to hand a consumer something already
// known-bad.
func (a *App) refreshLocalEntitlementStatus() {
	a.entMu.Lock()
	defer a.entMu.Unlock()

	hwID, hwErr := hwid.Get()
	if hwErr != nil {
		// Can't determine this machine's own identity -- nothing to bind
		// a token to, so there is no way this install can ever be
		// considered entitled. Logged once per call (cheap, this isn't a
		// hot path) rather than cached/silenced, since a persistently
		// failing hwid.Get() is a real configuration problem worth seeing
		// in logs (e.g. a locked-down registry on Windows).
		log.Printf("[app] warning: could not determine hardware id, entitlement disabled: %v", hwErr)
		a.entStatus.Linked = false
		a.entStatus.Tier = ""
		a.entStatus.ExpiresAt = time.Time{}
		return
	}

	if claims, err := entitlement.VerifyForHardware(a.cfg.EntitlementToken, hwID); err == nil {
		a.entStatus.Linked = true
		a.entStatus.Tier = claims.Tier
		a.entStatus.ExpiresAt = time.Unix(claims.ExpireAt, 0)
		if err := entitlement.WriteTokenFile(a.cfg.StateDir, a.cfg.EntitlementToken); err != nil {
			log.Printf("[app] warning: failed to write entitlement token file for rustshine: %v", err)
		}
	} else {
		a.entStatus.Linked = false
		a.entStatus.Tier = ""
		a.entStatus.ExpiresAt = time.Time{}
	}
}

// EntitlementStatus reports the current license state for the GUI (and,
// over adminapi, a thin-client GUI attached to a separate headless engine)
// to render the four-way license switcher and, once entitled, the
// Sunshine/RustShine switch. Cheap enough to poll on the GUI's existing 2s
// refresh ticker (see ui.Window.performRefresh) -- no push/SSE channel
// needed for this.
func (a *App) EntitlementStatus() entitlement.Status {
	a.entMu.Lock()
	st := a.entStatus
	a.entMu.Unlock()
	st.ActiveBackend = a.currentStreamKind()
	st.RustShineStaged = a.rustshineStaged()
	st.RustShineVersion = entitlement.StagedVersion(a.cfg.StateDir)
	st.WebRTCEnabled = !a.cfg.RustShineWebRTCDisabled
	return st
}

// SetRustShineWebRTCEnabled persists cfg.RustShineWebRTCDisabled and, if
// RustShine is the currently active backend, restarts it so the new
// --webrtc-disable flag (or its absence) actually takes effect --
// gamestream-server has no live toggle for this, it's read once at
// startup. A no-op on the running process while Sunshine is active: the
// preference is still saved either way, so switching to RustShine later
// picks it up without needing to touch this checkbox again.
func (a *App) SetRustShineWebRTCEnabled(enabled bool) error {
	saved := a.cfg
	saved.RustShineWebRTCDisabled = !enabled
	if err := a.SaveConfig(saved); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	a.streamMu.Lock()
	kind := a.streamKind
	stream := a.stream
	a.streamMu.Unlock()
	if kind != "rustshine" || stream == nil {
		return nil
	}

	applyStreamWebRTCEnabled(stream, enabled)
	if err := a.RestartSunshine(); err != nil {
		log.Printf("[app] failed to restart rustshine after webrtc toggle: %v", err)
	}
	return nil
}

func (a *App) setEntError(msg string) {
	a.entMu.Lock()
	a.entStatus.LinkInProgress = false
	a.entStatus.LastError = msg
	a.entMu.Unlock()
}

// StartFreeTrial fetches this machine's current (always-available) free
// tier token synchronously -- kept mainly for internal/adminapi's
// thin-client "start-trial" call; there is nothing left to "start", the
// backend's free tier is unconditional and permanent (see
// entitlement.StartTrial's doc comment). Also handy as a manual "refresh
// now" for the license dialog's default/no-purchase state.
func (a *App) StartFreeTrial() error {
	hwID, err := hwid.Get()
	if err != nil {
		a.setEntError(fmt.Sprintf("could not determine this machine's hardware id: %v", err))
		return err
	}

	a.entMu.Lock()
	a.entStatus.LinkInProgress = true
	a.entStatus.LastError = ""
	a.entMu.Unlock()
	defer func() {
		a.entMu.Lock()
		a.entStatus.LinkInProgress = false
		a.entMu.Unlock()
	}()

	res, err := entitlement.StartTrial(context.Background(), hwID)
	if err != nil {
		a.setEntError(fmt.Sprintf("could not reach the entitlement backend: %v", err))
		return err
	}

	a.applyIssuedToken(res.Token, hwID)
	return nil
}

// StartPurchase asks the backend for a Stripe Checkout Session URL bound to
// this machine's hardware id for the given tier ("pro" or "enterprise")
// and begins polling for the purchase to land in the background. Callers
// (the GUI) open the returned URL in the system browser; EntitlementStatus
// reflects progress from here on (the GUI already polls it on its own
// refresh ticker) -- no separate "purchase complete" callback into this
// process, see pollForLicense.
func (a *App) StartPurchase(tier string) (string, error) {
	hwID, err := hwid.Get()
	if err != nil {
		a.setEntError(fmt.Sprintf("could not determine this machine's hardware id: %v", err))
		return "", err
	}

	checkoutURL, err := entitlement.StartCheckoutURL(context.Background(), hwID, tier)
	if err != nil {
		a.setEntError(fmt.Sprintf("could not start checkout: %v", err))
		return "", err
	}

	a.entMu.Lock()
	if a.entPollCancel != nil {
		a.entPollCancel() // a previous attempt's poll loop, if any, is now stale
	}
	pollCtx, cancel := context.WithCancel(context.Background())
	a.entPollCancel = cancel
	a.entStatus.LinkInProgress = true
	a.entStatus.LastError = ""
	a.entMu.Unlock()

	go a.pollForLicense(pollCtx, hwID)
	return checkoutURL, nil
}

// CancelPurchase gives up on an in-flight StartPurchase: stops the
// background pollForLicense loop and clears LinkInProgress so the GUI's
// license dialog falls back to showing the trial/buy buttons again instead
// of being stuck on "waiting for checkout" -- previously there was no way
// out of that screen short of a completed purchase or pollForLicenseTimeout
// (15 minutes), even across closing and reopening the dialog, since
// LinkInProgress lives on entStatus (this App, not the dialog) and nothing
// ever cleared it if the browser tab was simply closed without paying. Safe
// to call even if nothing is in flight (entPollCancel nil, or already
// finished) -- a no-op in that case, not an error, since the GUI can't
// always tell which case it's in before offering a "Cancel" button.
func (a *App) CancelPurchase() {
	a.entMu.Lock()
	if a.entPollCancel != nil {
		a.entPollCancel()
		a.entPollCancel = nil
	}
	a.entStatus.LinkInProgress = false
	a.entStatus.LastError = ""
	a.entMu.Unlock()
}

// pollForLicenseTimeout bounds how long pollForLicense keeps checking after
// a checkout URL was opened -- generous (most Stripe webhooks land within
// seconds, but this also covers someone taking their time entering card
// details) without polling forever if the browser tab was simply closed
// without paying. The GUI's "Refresh" affordance (if the dialog is
// reopened later) calls RefreshLicense directly and picks up a late
// webhook regardless of whether this loop already gave up.
const pollForLicenseTimeout = 15 * time.Minute

// pollForLicense repeatedly asks the backend for hwID's current tier every
// 2s, until it comes back as a paid tier (anything other than "free"), ctx
// is cancelled (a newer StartPurchase call superseded this one), or
// pollForLicenseTimeout elapses. There is no server-side correlation state
// the way the old OAuth poll had (no "state" param) -- hwID itself is the
// only thing being polled for, which is also why this is safe to just call
// again from the GUI's own "Refresh" button without needing to restart a
// checkout.
//
// Checking Status != "free" here (not just "did RefreshLicense succeed")
// matters: register/refresh never fails anymore (see IssueResult's doc
// comment) -- every call, including the very first one right after opening
// a checkout tab, returns a perfectly valid "free" token. Treating any
// successful response as "done" (as this loop used to, back when
// NotLicensed was the only failure signal to check) would apply that free
// token immediately and stop polling before the purchase even completed.
func (a *App) pollForLicense(ctx context.Context, hwID string) {
	deadline := time.Now().Add(pollForLicenseTimeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if time.Now().After(deadline) {
			a.setEntError("Didn't detect a completed payment yet — if you finished checkout, try again in a moment.")
			return
		}
		res, err := entitlement.RefreshLicense(ctx, hwID)
		if err != nil {
			continue // transient network hiccup -- keep polling until the deadline or ctx cancellation
		}
		if res.Status == "free" {
			continue // payment not completed / webhook not landed yet
		}
		a.applyIssuedToken(res.Token, hwID)
		return
	}
}

// applyIssuedToken is StartFreeTrial and pollForLicense's shared tail end:
// verify (hardware-bound, see entitlement.VerifyForHardware), persist, and
// kick off the first RustShine download so there's no separate "Download
// RustShine" click required.
func (a *App) applyIssuedToken(token, hwID string) {
	if _, err := entitlement.VerifyForHardware(token, hwID); err != nil {
		a.setEntError("Received an invalid license token — try again.")
		return
	}

	next := a.cfg
	next.EntitlementToken = token
	if err := a.SaveConfig(next); err != nil {
		log.Printf("[app] warning: failed to persist entitlement token: %v", err)
	}

	a.entMu.Lock()
	a.entStatus.LinkInProgress = false
	a.entStatus.LastError = ""
	a.entMu.Unlock()
	a.refreshLocalEntitlementStatus()

	// Start the first RustShine download immediately -- no "Download
	// RustShine" button click required -- rather than waiting for
	// whatever's left of entitlementWatchdog's 6h cadence. Backgrounded:
	// applyIssuedToken returns to pollForLicense's polling loop (or
	// StartFreeTrial's synchronous caller), either of which a GUI is
	// actively waiting on to stop showing its spinner: it shouldn't also
	// block on a multi-MB download completing first.
	go a.ensureRustShineFresh(context.Background(), token)
}

// bootstrapFreeTier silently links a fresh install to today's unconditional
// free tier (see recheckEntitlement's call site) -- deliberately NOT
// applyIssuedToken: this runs with no user action at all (just the app
// starting up), so it must not also kick off an immediate multi-MB
// RustShine download on every single install regardless of whether that
// install ever wants RustShine. A later explicit tier pick in the license
// dialog (StartFreeTrial/StartPurchase's own applyIssuedToken) is what
// actually triggers that download.
func (a *App) bootstrapFreeTier(ctx context.Context, hwID string) {
	res, err := entitlement.RefreshLicense(ctx, hwID)
	if err != nil {
		log.Printf("[app] entitlement bootstrap failed (will retry next interval): %v", err)
		return
	}
	if _, err := entitlement.VerifyForHardware(res.Token, hwID); err != nil {
		log.Printf("[app] entitlement bootstrap: backend returned a token that doesn't verify locally: %v", err)
		return
	}

	next := a.cfg
	next.EntitlementToken = res.Token
	if err := a.SaveConfig(next); err != nil {
		log.Printf("[app] warning: failed to persist entitlement token: %v", err)
	}
	a.refreshLocalEntitlementStatus()
}

// AccountStatus returns a snapshot of the account-login state (see
// account.Status) for the GUI to render -- same "poll a status struct on a
// refresh ticker" pattern EntitlementStatus already uses, just for the
// separate account login.
func (a *App) AccountStatus() account.Status {
	a.accMu.Lock()
	defer a.accMu.Unlock()
	return a.accStatus
}

func (a *App) setAccError(msg string) {
	a.accMu.Lock()
	a.accStatus.LoginInProgress = false
	a.accStatus.RebindInProgress = false
	a.accStatus.LastError = msg
	a.accMu.Unlock()
}

// StartAccountLogin begins a device-code login (see internal/account's
// package doc comment) and returns the URL to open in the system browser.
// Mirrors StartPurchase's shape: kicks off a background poll goroutine and
// returns immediately, LoginInProgress on accStatus is what the GUI's
// refresh ticker watches.
func (a *App) StartAccountLogin() (string, error) {
	start, err := account.StartLogin(context.Background())
	if err != nil {
		a.setAccError(fmt.Sprintf("could not start login: %v", err))
		return "", err
	}

	a.accMu.Lock()
	if a.accPollCancel != nil {
		a.accPollCancel() // a previous attempt's poll loop, if any, is now stale
	}
	pollCtx, cancel := context.WithCancel(context.Background())
	a.accPollCancel = cancel
	a.accStatus.LoginInProgress = true
	a.accStatus.LastError = ""
	a.accMu.Unlock()

	go a.pollAccountLogin(pollCtx, start.Code)
	return start.VerificationURL, nil
}

// CancelAccountLogin mirrors CancelPurchase's doc comment exactly, for the
// account login's own in-flight poll instead of the entitlement one.
func (a *App) CancelAccountLogin() {
	a.accMu.Lock()
	if a.accPollCancel != nil {
		a.accPollCancel()
		a.accPollCancel = nil
	}
	a.accStatus.LoginInProgress = false
	a.accStatus.LastError = ""
	a.accMu.Unlock()
}

// accountLoginPollTimeout bounds how long pollAccountLogin keeps checking
// after the verification URL was opened -- mirrors
// pollForLicenseTimeout's own reasoning, just shorter: a Google login
// round trip is seconds, not "however long it takes to enter card details".
const accountLoginPollTimeout = 5 * time.Minute

// pollAccountLogin mirrors pollForLicense's shape exactly (see that
// function's doc comment) for the device-code login instead of the Stripe
// checkout poll.
func (a *App) pollAccountLogin(ctx context.Context, code string) {
	deadline := time.Now().Add(accountLoginPollTimeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if time.Now().After(deadline) {
			a.setAccError("Didn't detect a completed login yet — if you finished signing in, try \"Log in\" again.")
			return
		}
		result, err := account.Poll(ctx, code)
		if err != nil {
			continue // transient network hiccup -- keep polling until the deadline or ctx cancellation
		}
		if result.Status == "expired" {
			a.setAccError("Login link expired — click \"Log in\" again.")
			return
		}
		if result.Status != "complete" {
			continue // still pending -- human hasn't finished the browser step yet
		}

		next := a.cfg
		next.AccountEmail = result.Email
		next.AccountToken = result.AccountToken
		if err := a.SaveConfig(next); err != nil {
			log.Printf("[app] warning: failed to persist account token: %v", err)
		}

		a.accMu.Lock()
		a.accStatus.LoggedIn = true
		a.accStatus.Email = result.Email
		a.accStatus.LoginInProgress = false
		a.accStatus.LastError = ""
		a.accMu.Unlock()

		a.refreshAccountLicenses(context.Background())
		return
	}
}

// refreshAccountLicenses re-fetches the logged-in account's desktop
// licenses -- called right after login completes and again after a
// successful Rebind (so the GUI's list reflects the new owner immediately
// rather than waiting for the dialog to be reopened).
func (a *App) refreshAccountLicenses(ctx context.Context) {
	// a.cfg.AccountToken, not accStatus -- same convention every other
	// entitlement/account field on App follows (see DownloadRustShine's
	// own `token := a.cfg.EntitlementToken`): the persisted config is the
	// source of truth, accStatus is only ever a read-optimized mirror of
	// it for the GUI.
	token := a.cfg.AccountToken
	if token == "" {
		return
	}

	licenses, err := account.ListLicenses(ctx, token)
	if err != nil {
		a.setAccError(fmt.Sprintf("could not load your licenses: %v", err))
		return
	}
	a.accMu.Lock()
	a.accStatus.Licenses = licenses
	a.accMu.Unlock()
}

// RebindLicenseToThisDevice moves oldIdentifier (one of the logged-in
// account's own desktop licenses, per accStatus.Licenses) onto this
// machine's hardware id -- the account-login replacement for the manual
// "paste the old machine's token" rebind flow (see internal/entitlement's
// StartCheckoutURL-adjacent flows; that one is untouched and still works).
// Applies the freshly-rebound license locally exactly like applyIssuedToken
// would, except there's no fresh token to verify here: RefreshLicense
// (below) is what actually confirms the backend now considers this
// hardware id licensed and returns a real, hardware-bound token for it.
func (a *App) RebindLicenseToThisDevice(oldIdentifier string) error {
	hwID, err := hwid.Get()
	if err != nil {
		a.setAccError(fmt.Sprintf("could not determine this machine's hardware id: %v", err))
		return err
	}

	token := a.cfg.AccountToken
	a.accMu.Lock()
	a.accStatus.RebindInProgress = true
	a.accStatus.LastError = ""
	a.accMu.Unlock()
	defer func() {
		a.accMu.Lock()
		a.accStatus.RebindInProgress = false
		a.accMu.Unlock()
	}()
	if token == "" {
		err := fmt.Errorf("not logged in")
		a.setAccError("Log in first.")
		return err
	}

	if err := account.Rebind(context.Background(), token, oldIdentifier, hwID); err != nil {
		a.setAccError(fmt.Sprintf("could not rebind license: %v", err))
		return err
	}

	res, err := entitlement.RefreshLicense(context.Background(), hwID)
	if err != nil || res.Status == "free" {
		// The rebind itself succeeded (backend confirmed it above) -- a
		// still-"free" (or failed) refresh here just means this machine
		// hasn't picked up the fresh paid-tier token yet (KV read lag, or a
		// transient network hiccup); entitlementWatchdog's own periodic
		// RefreshLicense call will catch up shortly, and the GUI's
		// "Refresh" affordance re-drives this same path on demand.
		log.Printf("[app] rebind succeeded but refresh-license didn't pick it up yet: err=%v", err)
	} else {
		a.applyIssuedToken(res.Token, hwID)
	}

	a.refreshAccountLicenses(context.Background())
	return nil
}

// RefreshAccountLicenses re-fetches the logged-in account's licenses in the
// background -- exported so the GUI can drive it on demand (opening the
// License dialog, a "Refresh" click) via the TokenProvider interface,
// mirroring refreshAccountLicenses's unexported synchronous version used
// internally right after a login/rebind completes.
func (a *App) RefreshAccountLicenses() {
	go a.refreshAccountLicenses(context.Background())
}

// LogoutAccount forgets the locally-stored account login -- purely local
// (there's no server-side session to invalidate: the Bearer account token
// simply expires on its own after 30 days, see deviceAuth.ts's
// ACCOUNT_TOKEN_TTL_SECONDS). Does not touch EntitlementToken/PreferredBackend
// -- RustShine keeps running under whatever hardware-bound license/trial it
// already had; only the account identity used for rebinding is cleared.
func (a *App) LogoutAccount() error {
	next := a.cfg
	next.AccountEmail = ""
	next.AccountToken = ""
	if err := a.SaveConfig(next); err != nil {
		return err
	}
	a.accMu.Lock()
	a.accStatus = account.Status{}
	a.accMu.Unlock()
	return nil
}

// DownloadRustShine downloads and stages the RustShine build for this
// platform. onProgress mirrors entitlement.ProgressFunc's threading
// contract exactly (internal/update.ProgressFunc's twin) — called from
// this goroutine, GUI callers must hop to their own UI thread themselves.
// Does not switch to it; call SetStreamBackend("rustshine") once this
// returns successfully.
func (a *App) DownloadRustShine(onProgress entitlement.ProgressFunc) error {
	token := a.cfg.EntitlementToken
	hwID, err := hwid.Get()
	if err != nil {
		return fmt.Errorf("could not determine this machine's hardware id: %w", err)
	}
	if _, err := entitlement.VerifyForHardware(token, hwID); err != nil {
		return fmt.Errorf("not currently entitled: %w", err)
	}

	a.entMu.Lock()
	a.entStatus.DownloadInProgress = true
	a.entStatus.Progress = -1
	a.entStatus.LastError = ""
	a.entMu.Unlock()
	defer func() {
		a.entMu.Lock()
		a.entStatus.DownloadInProgress = false
		a.entMu.Unlock()
	}()

	// Always update entStatus.Progress (so a thin-client GUI polling
	// EntitlementStatus over adminapi sees it, per handleDownloadRustShine's
	// doc comment on why that download call is fire-and-forget), and also
	// forward to onProgress when the caller is an engine-owning GUI that
	// wants lower-latency feedback than the 2s poll.
	combined := func(downloaded, total int64) {
		frac := -1.0
		if total > 0 {
			frac = float64(downloaded) / float64(total)
		}
		a.entMu.Lock()
		a.entStatus.Progress = frac
		a.entMu.Unlock()
		if onProgress != nil {
			onProgress(downloaded, total)
		}
	}

	if err := entitlement.StageRustShine(context.Background(), a.cfg.StateDir, token, combined); err != nil {
		a.setEntError(fmt.Sprintf("download failed: %v", err))
		return err
	}
	return nil
}

// ClearLicense clears the saved entitlement token and switches back to
// Sunshine if RustShine was active. Does not delete the already-staged
// RustShine binary -- buying/trialing again later can reuse it without
// re-downloading (see rustshineBackend.BinaryPath's staged-file check).
func (a *App) ClearLicense() error {
	if a.currentStreamKind() == "rustshine" {
		if err := a.SetStreamBackend("sunshine"); err != nil {
			return err
		}
	}
	next := a.cfg
	next.EntitlementToken = ""
	next.PreferredBackend = ""
	if err := a.SaveConfig(next); err != nil {
		return err
	}
	a.refreshLocalEntitlementStatus()
	// Immediately re-bootstrap the free tier rather than leaving the
	// license dialog on its "Setting up…" placeholder until
	// entitlementWatchdog's next interval -- see recheckEntitlement's
	// empty-token branch. Backgrounded: ClearLicense's own caller (the
	// GUI) doesn't need to block on a network round trip just to clear a
	// local file.
	if hwID, err := hwid.Get(); err == nil {
		go a.bootstrapFreeTier(context.Background(), hwID)
	}
	return nil
}

// downgradeToSunshine clears the saved entitlement token and switches back
// to Sunshine if RustShine was active -- the shared tail end of every path
// that decides entitlement no longer holds up (a refunded purchase, a
// locally-expired cached token/trial while offline). Does NOT delete the
// staged RustShine binary or its mirrored token file (see
// entitlement.WriteTokenFile) -- a later purchase/trial can reuse the
// binary without re-downloading, and a stale token file is harmless
// either way, since it just won't get launched again from the Go side
// until a fresh license/trial succeeds anyway.
func (a *App) downgradeToSunshine() {
	next := a.cfg
	next.EntitlementToken = ""
	next.PreferredBackend = ""
	_ = a.SaveConfig(next)
	a.refreshLocalEntitlementStatus()
	if a.currentStreamKind() == "rustshine" {
		_ = a.SetStreamBackend("sunshine")
	}
}

// entitlementRecheckInterval is how often entitlementWatchdog re-verifies a
// licensed customer's purchase against the backend (a trial's validity is
// entirely local -- see recheckEntitlement's own doc comment on why only a
// license, not a trial, needs a network re-check at all). Far longer than
// sunshineWatchdogInterval deliberately: there's no reason to check more
// than a few times a day, and a refund is a rare, human-initiated event,
// not something that needs sub-hour detection latency.
const entitlementRecheckInterval = 6 * time.Hour

// entitlementWatchdog periodically re-verifies entitlement and downgrades
// to Sunshine the moment it no longer holds up (a refund, a trial that's
// now expired) -- mirrors sunshineWatchdog's shape. Run also fires one
// immediate recheckEntitlement shortly after startup (not gated on this
// ticker), so a refund is caught quickly after a restart instead of
// waiting up to a full interval.
func (a *App) entitlementWatchdog(ctx context.Context) {
	ticker := time.NewTicker(entitlementRecheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.recheckEntitlement(ctx)
		}
	}
}

// recheckEntitlement re-verifies whatever's currently cached in
// cfg.EntitlementToken and downgrades to Sunshine if it no longer holds up.
// A no-op if there's no cached token at all (never trialed/purchased, or
// already cleared).
//
// A cached LICENSE token gets a real network re-check (RefreshLicense) --
// this is the only thing a refund can invalidate, so it's the only case
// that needs to ask the backend at all. A cached TRIAL token does NOT get
// a network call: a trial can't be revoked (there's nothing to refund), so
// its validity is entirely captured by its own signed `exp`, which the
// local hardware-bound verify below already checks -- asking the backend
// again would only ever reproduce the same still-active trial or (once
// past exp) a signature check that already failed locally anyway.
//
// A transient failure on the license path (network down, backend
// unreachable) does NOT downgrade immediately by itself — but it also
// doesn't just do nothing: it falls back to checking the *cached* token's
// own embedded expiry locally (entitlement.VerifyForHardware, no network
// needed) and downgrades once *that* has passed. Without this fallback, a
// long-running agent process that's offline for longer than the token's
// TTL would keep trusting an already-locally-expired token forever
// (nothing else re-derives entStatus continuously while already running —
// only at the next restart, via New()'s own local check), silently
// breaking the "offline grace is bounded by the token's TTL" guarantee
// this whole scheme is supposed to provide. This closes that gap: the
// bound is enforced continuously, not just at the next restart.
func (a *App) recheckEntitlement(ctx context.Context) {
	hwID, err := hwid.Get()
	if err != nil {
		log.Printf("[app] entitlement recheck: could not determine hardware id: %v", err)
		return
	}

	if strings.TrimSpace(a.cfg.EntitlementToken) == "" {
		// Fresh install, never linked -- bootstrap today's unconditional
		// free tier right away rather than leaving the license dialog's
		// tier switcher with nothing to show until the user clicks
		// something first (there's no "start trial" step to wait for
		// anymore, see entitlement.StartTrial's doc comment). Deliberately
		// NOT applyIssuedToken (which also kicks off an immediate
		// RustShine download) -- staying on free tier with Sunshine active
		// shouldn't silently pull down a multi-MB RustShine build nobody
		// asked for yet; that still only happens once the user actually
		// picks RustShine in the dialog.
		a.bootstrapFreeTier(ctx, hwID)
		return
	}

	claims, verifyErr := entitlement.VerifyForHardware(a.cfg.EntitlementToken, hwID)
	if verifyErr != nil {
		log.Printf("[app] cached entitlement token is no longer valid locally — switching back to Sunshine: %v", verifyErr)
		a.downgradeToSunshine()
		return
	}
	if claims.Provider == entitlement.ProviderDesktopTrial {
		// This provider string now means "free tier" (see
		// usbridge-entitlement-backend's desktopLicense.ts module doc
		// comment for why it kept the old trial name) -- still locally
		// valid (checked above) and not network-revocable, since free has
		// nothing to refund. Nothing more to do until it expires on its
		// own (and even then, the next refresh just gets a fresh one).
		return
	}

	res, err := entitlement.RefreshLicense(ctx, hwID)
	if err != nil {
		log.Printf("[app] entitlement recheck failed (will retry next interval): %v", err)
		return // cached token already verified locally above -- keep trusting it until it actually expires or a retry succeeds
	}
	if res.Status == "free" {
		log.Printf("[app] license no longer on record for this hardware (refunded/canceled?) — switching back to Sunshine")
		a.downgradeToSunshine()
		return
	}

	next := a.cfg
	next.EntitlementToken = res.Token
	if err := a.SaveConfig(next); err != nil {
		log.Printf("[app] warning: failed to persist refreshed entitlement: %v", err)
	}
	a.refreshLocalEntitlementStatus()
	a.ensureRustShineFresh(ctx, res.Token)
}

// ensureRustShineFresh makes sure a licensed/trialing customer always has
// the latest staged RustShine build, with no manual "Download RustShine"
// click required: called both right after a purchase/trial succeeds
// (applyIssuedToken, so a brand-new customer's download starts within
// seconds, not on whatever the next 6h watchdog tick happens to be) and
// from recheckEntitlement's own cadence afterward (so later releases keep
// reaching them). A no-op with no observable effect whenever
// entitlementToken is empty -- i.e. never entitled, or already downgraded
// to Sunshine -- so an unentitled install never downloads or updates
// RustShine at all, only ever runs Sunshine, exactly like
// DownloadRustShine's own explicit entitlement.VerifyForHardware gate.
func (a *App) ensureRustShineFresh(ctx context.Context, entitlementToken string) {
	if strings.TrimSpace(entitlementToken) == "" {
		return
	}
	if !a.rustshineStaged() {
		log.Printf("[app] entitlement linked — downloading rustshine")
		if err := entitlement.StageRustShine(ctx, a.cfg.StateDir, entitlementToken, nil); err != nil {
			// Non-fatal -- retried at the next watchdog interval (offline,
			// transient backend error, ...). Until it succeeds, this install
			// simply keeps running Sunshine.
			log.Printf("[app] rustshine initial download failed (will retry next interval): %v", err)
			return
		}
		log.Printf("[app] rustshine downloaded")
		a.entMu.Lock()
		a.entStatus.RustShineStaged = a.rustshineStaged()
		a.entMu.Unlock()
		return
	}
	a.checkRustShineUpdate(ctx, entitlementToken)
}

// checkRustShineUpdate re-stages RustShine if the backend has published a
// newer build than whatever's currently staged -- called only via
// ensureRustShineFresh above, once RustShine is already known to be staged
// (a no-op otherwise, see CheckRustShineUpdate's own doc comment).
//
// This is what makes a RustShine-only fix (one that ships in a new
// gamestream-server release but not a new agent build -- e.g. the
// NV_KEYBOARD_PACKET modifiers-byte fix this mechanism was added for)
// actually reach an agent that already downloaded RustShine before that
// fix existed.
func (a *App) checkRustShineUpdate(ctx context.Context, entitlementToken string) {
	if strings.TrimSpace(entitlementToken) == "" {
		return
	}
	needsUpdate, version, err := entitlement.CheckRustShineUpdate(ctx, a.cfg.StateDir, entitlementToken)
	if err != nil {
		log.Printf("[app] rustshine update check failed (will retry next interval): %v", err)
		return
	}
	if !needsUpdate {
		return
	}
	// Same reentrancy guard as CheckRustShineUpdateNow (see its doc comment)
	// -- this silent background tick and a manual "check for updates" click
	// share the same StageRustShine/stopRustShineForUpdate sequence and can
	// otherwise race each other just as easily as two clicks can.
	a.entMu.Lock()
	if a.entStatus.RustShineUpdateInProgress {
		a.entMu.Unlock()
		return
	}
	a.entStatus.RustShineUpdateInProgress = true
	a.entMu.Unlock()
	a.setRustShineUpdatePaused(true)
	defer func() {
		a.entMu.Lock()
		a.entStatus.RustShineUpdateInProgress = false
		a.entMu.Unlock()
		a.setRustShineUpdatePaused(false)
	}()

	log.Printf("[app] rustshine update available (%s) — downloading", version)
	stopped := a.stopRustShineForUpdate()
	if err := entitlement.StageRustShine(ctx, a.cfg.StateDir, entitlementToken, nil); err != nil {
		// On Windows this shouldn't fire anymore now that
		// stopRustShineForUpdate releases the file lock first -- if it
		// still does (AV scan holding the file, some other locker), it's
		// non-fatal: retried at the next watchdog interval. Relaunch
		// immediately (on the still-old binary) if we stopped an actively
		// streaming backend for this attempt, rather than leaving the user
		// without video until the next 15s watchdog tick.
		log.Printf("[app] rustshine auto-update to %s failed (will retry next interval): %v", version, err)
		if stopped {
			a.startSunshineNow()
		}
		return
	}
	log.Printf("[app] rustshine updated to %s", version)
	a.entMu.Lock()
	a.entStatus.RustShineStaged = a.rustshineStaged()
	a.entMu.Unlock()
	a.restartRustShineIfActive()
}

// setRustShineUpdatePaused tells the active backend (if it implements
// streamhost.UpdatePauser -- currently just rustshine) to hold off opening
// any new handle on its own executable for as long as an update is in
// flight. See that interface's doc comment for the exact
// --list-capture-devices race this closes; a no-op for any backend that
// doesn't implement it (Sunshine has no equivalent hot-replace-while-running
// concern).
func (a *App) setRustShineUpdatePaused(paused bool) {
	if up, ok := a.stream.(streamhost.UpdatePauser); ok {
		up.SetUpdateInProgress(paused)
	}
}

// stopRustShineForUpdate stops the active RustShine process before an
// update is staged, so entitlement.StageRustShine's os.Rename over the
// binary doesn't race a running process for the file. Only Windows actually
// needs this -- it locks a running .exe's image against rename/replace,
// while POSIX (Linux/macOS) allows renaming over an open file just fine, so
// staging there already hot-swaps cleanly without interrupting anything.
// Confirmed live as a real, self-perpetuating failure mode on Windows: a
// gamestream-server build that crashes instantly on every launch (e.g. a
// staged binary that predates a CLI flag the agent now always passes) keeps
// the exe open/locked essentially 100% of the time between respawns, so the
// very update that would fix the crash could never land either -- staging
// kept failing every single watchdog interval, forever.
//
// Returns whether it actually stopped anything, so the caller knows whether
// it's now responsible for relaunching (see both call sites).
func (a *App) stopRustShineForUpdate() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	if a.currentStreamKind() != "rustshine" || a.stream == nil {
		return false
	}
	log.Printf("[app] stopping rustshine before staging update (Windows can't replace a running .exe)")
	_ = a.stream.Stop()
	// Stop() only kills the single instance this backend struct is tracking
	// (b.proc) -- confirmed live as insufficient on its own: a manual
	// StageRustShine run against a completely clean process list (zero
	// gamestream-server.exe alive) staged and renamed in ~1.3s every time,
	// while the exact same call from this update flow kept losing to
	// "Access is denied" for the *entire* 20s renameWithRetry budget,
	// despite Stop() reporting its tracked pid gone in milliseconds --
	// meaning some second, untracked gamestream-server.exe instance (this
	// backend struct never learned about it, so Stop() had nothing to kill
	// it with) was the one actually holding the file open. Root cause of
	// that second instance's existence not fully pinned down; this sweeps
	// unconditionally by name as a belt-and-suspenders guarantee that
	// nothing named gamestream-server.exe survives this point, regardless
	// of how it got there or whether this backend ever tracked it.
	killCmd := exec.Command("taskkill", "/F", "/IM", "gamestream-server.exe")
	maybeHideWindow(killCmd)
	_ = killCmd.Run()
	// Stop() only signals termination; give the OS a moment to actually
	// release the exe's image-section file lock before the upcoming rename.
	time.Sleep(500 * time.Millisecond)
	// Confirmed live: the plain taskkill above can still leave a
	// gamestream-server.exe alive with "Access is denied" even from this
	// same agent's own same-user call -- root cause not fully pinned down
	// (not self-spawned: gamestream-server's own source spawns no child
	// processes on Windows), but reproducible: a manual StageRustShine
	// against a genuinely clean process list staged and renamed in ~1.3s
	// every time, while this exact flow, with a survivor still present,
	// lost to "Access is denied" for the entire 20s renameWithRetry budget
	// regardless. Escalating to a UAC-elevated taskkill closes that gap the
	// same-level sweep above can't: an elevated `taskkill /F` carries
	// enough privilege to reach a process a plain same-user one can't, the
	// same reason Task Manager's own "End task" needs "Run as
	// administrator" for some processes. Only fires when something is
	// actually still there (a UAC prompt on every single update, needed or
	// not, would be needlessly disruptive) and is itself non-fatal on
	// failure/decline/no-desktop-to-prompt-on -- StageRustShine's own
	// caller already retries at the next interval and falls back to
	// relaunching the old binary regardless of how this returns.
	if a.perms != nil && processRunning("gamestream-server.exe") {
		log.Printf("[app] gamestream-server.exe survived the plain taskkill -- requesting elevation to force it (a UAC prompt may appear)")
		if err := a.perms.KillGamestreamServerElevated(); err != nil {
			log.Printf("[app] elevated taskkill failed or was declined: %v", err)
		} else {
			time.Sleep(500 * time.Millisecond)
		}
	}
	return true
}

// processRunning reports whether any process named imageName (e.g.
// "gamestream-server.exe") is currently running, via `tasklist`'s own
// image-name filter -- used by stopRustShineForUpdate to decide whether the
// plain taskkill above actually needs the elevated escalation, rather than
// firing a UAC prompt unconditionally on every update.
func processRunning(imageName string) bool {
	cmd := exec.Command("tasklist", "/NH", "/FI", "IMAGENAME eq "+imageName)
	maybeHideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), strings.ToLower(imageName))
}

// restartRustShineIfActive re-execs the running RustShine subprocess (via
// the same generic RestartSunshine plumbing every other capability/config
// change already uses -- see that method's doc comment on why the name is
// legacy but the behavior isn't Sunshine-specific) so a version that was
// just staged onto disk actually takes effect immediately, instead of
// silently sitting there unused until the next unrelated restart. A no-op
// whenever Sunshine is the active backend -- the freshly staged binary
// isn't running yet either way, so there's nothing to hot-swap; it'll be
// picked up the next time something switches to RustShine (SetStreamBackend
// always re-resolves BinaryPath, it doesn't cache the old one).
//
// Called from both the silent background watchdog (checkRustShineUpdate
// above) and the GUI's explicit "Check for updates" button
// (CheckRustShineUpdateNow) -- same contract either way: whichever one
// staged a newer build, the active session should never keep running the
// old bytes just because nobody happened to flip backends or restart the
// whole agent afterward.
func (a *App) restartRustShineIfActive() {
	if a.currentStreamKind() != "rustshine" {
		return
	}
	log.Printf("[app] rustshine is the active backend — restarting it to pick up the update")
	if err := a.RestartSunshine(); err != nil {
		// Non-fatal: the newer binary is already staged and will be used
		// on the next restart regardless of how it's triggered (manual
		// backend toggle, agent restart, ...) -- this only means *this*
		// attempt at an immediate, no-agent-restart hot-swap didn't land.
		log.Printf("[app] restarting rustshine to apply the update failed (will still be picked up on the next restart): %v", err)
	}
}

// CheckRustShineUpdateNow is the GUI's explicit "Check for updates" button
// (as opposed to ensureRustShineFresh/checkRustShineUpdate's own silent
// background cadence) -- same entitlement.CheckRustShineUpdate +
// StageRustShine pair, but synchronous-enough to report a real error back
// to the click that triggered it, and unconditional on whether RustShine is
// currently staged at all (the very first "Download RustShine" click in the
// license dialog covers that case already; this one is a no-op, not an
// error, if nothing's staged yet -- see CheckRustShineUpdate's own doc
// comment). Hot-swaps in place via restartRustShineIfActive if an update
// was actually found and applied.
func (a *App) CheckRustShineUpdateNow() error {
	// Deliberately not a local entitlement.Verify call the way
	// DownloadRustShine's does one -- this button is conceptually "run the
	// same check checkRustShineUpdate's background watchdog already does,
	// right now instead of waiting for the next interval", and that
	// watchdog has never required local verification either (see its own
	// and ensureRustShineFresh's doc comments): ResolveDownload's backend
	// round-trip is the actual authority on whether this token still
	// grants access, same as it is for every other RustShine download
	// path. A locally well-formed-but-since-revoked token still gets a
	// real error here -- just from the backend response, one network
	// round-trip later, not from this local shortcut.
	token := a.cfg.EntitlementToken
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("not currently entitled: no entitlement token")
	}

	// Guard against two update attempts racing each other (a double-click on
	// this button, or overlapping with the silent background watchdog's own
	// checkRustShineUpdate tick). Confirmed live as a real failure mode: two
	// concurrent StageRustShine calls each stop/restart the process out from
	// under the other, so both "Access is denied" rename attempts land in
	// whatever brief window the other one's restart leaves open, and neither
	// ever succeeds. RustShineUpdateInProgress already existed for the UI to
	// show a spinner; it doubles as that lock here.
	a.entMu.Lock()
	if a.entStatus.RustShineUpdateInProgress {
		a.entMu.Unlock()
		return fmt.Errorf("rustshine update already in progress")
	}
	a.entStatus.RustShineUpdateInProgress = true
	a.entStatus.LastError = ""
	a.entMu.Unlock()
	// Same UpdatePauser guard checkRustShineUpdate's own background tick
	// takes (see setRustShineUpdatePaused's doc comment) -- this manual
	// button races the same --list-capture-devices helper the same way a
	// silent watchdog tick does, so it needs the same protection. Set/reset
	// here regardless of whether needsUpdate turns out true below: cheap
	// no-op either way, and keeps a single place responsible for the
	// pause/unpause pairing instead of threading a conditional through the
	// early-return path too.
	a.setRustShineUpdatePaused(true)
	defer func() {
		a.entMu.Lock()
		a.entStatus.RustShineUpdateInProgress = false
		a.entMu.Unlock()
		a.setRustShineUpdatePaused(false)
	}()

	ctx := context.Background()
	needsUpdate, version, err := entitlement.CheckRustShineUpdate(ctx, a.cfg.StateDir, token)
	if err != nil {
		a.setEntError(fmt.Sprintf("update check failed: %v", err))
		return err
	}
	if !needsUpdate {
		log.Printf("[app] rustshine is already up to date")
		return nil
	}

	log.Printf("[app] rustshine update available (%s) — downloading (manual check)", version)
	stopped := a.stopRustShineForUpdate()
	if err := entitlement.StageRustShine(ctx, a.cfg.StateDir, token, nil); err != nil {
		a.setEntError(fmt.Sprintf("update failed: %v", err))
		if stopped {
			a.startSunshineNow()
		}
		return err
	}
	log.Printf("[app] rustshine updated to %s", version)
	a.entMu.Lock()
	a.entStatus.RustShineStaged = a.rustshineStaged()
	a.entMu.Unlock()
	a.restartRustShineIfActive()
	return nil
}

// waitForMonitorCorrelation blocks briefly for Sunshine's KMS/Wayland
// per-monitor CRTC-offset correlation (correlate_to_wayland in kmsgrab.cpp,
// see MonitorIndexByName's doc comment) to finish after a restart.
// WaitReady only confirms the HTTPS/NvHTTP listener is up, which binds well
// before that correlation completes — a client that reconnects (Launch) in
// that gap locks in a session using default/uncorrelated per-connector CRTC
// offsets, so absolute-mouse coordinates land on the wrong monitor or drift
// near its edges for that whole session. A *second*, later reconnect (e.g.
// triggered by an unrelated codec change) picks up the by-then-finished
// correlation and looks like it "fixed" the mouse — this closes that gap by
// making the restart itself wait for correlation instead.
//
// Also required on Windows, for a different reason: Sunshine's Windows
// display_device backend identifies monitors by a device_id GUID that only
// becomes knowable by parsing "Currently available display devices:" back
// out of Sunshine's own log (see sunshine_devices_windows.go) — there is no
// way to predict it in advance. Without this wait, a monitor pick made in
// the tiny window between Sunshine's admin port coming up (WaitReady) and
// that log block actually being appended falls back to capture.Service's
// generic display:N enumeration; videoSetDevice strips its prefix the same
// as a real winid:, so a bare numeric index (e.g. "0") gets written into
// output_name. Sunshine's Windows backend only accepts the GUID form, so it
// can't resolve a plain "0" to any device and silently falls back to
// auto-pick (== the primary/first monitor) from then on, regardless of what
// the client asks for — this is what made monitor switching look like a
// permanent no-op on Windows w/ Sunshine while the same flow worked fine on
// RustShine (whose "monitor_index" key genuinely does take a small numeric
// index).
//
// No-op on macOS: its Sunshine backend has no log-derived device list to
// wait for (ListCaptureDevices always returns nil there — see
// sunshine_devices_other.go), and a bounded poll would just time out doing
// nothing.
func (a *App) waitForMonitorCorrelation() {
	if a.stream == nil {
		return
	}
	switch runtime.GOOS {
	case "linux":
		if a.stream.CaptureMode() != "kms" {
			return
		}
	case "windows":
		// fall through to the poll below
	default:
		return
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(a.stream.ListCaptureDevices()) > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// KMSCaptureGranted reports whether the bundled sunshine_capexec launcher
// has the CAP_SYS_ADMIN capability needed for KMS capture.
func (a *App) KMSCaptureGranted() bool {
	if a.perms == nil {
		return false
	}
	return a.perms.KMSCaptureGranted(a.SunshineCapExecPath())
}

// RequestKMSCapture grants CAP_SYS_ADMIN to the bundled sunshine_capexec
// launcher (prompts for elevation via pkexec) — never to Sunshine itself,
// which would break its bundled-library resolution, see
// internal/permissions.RequestKMSCapture — then restarts Sunshine so the
// newly-granted capability is actually picked up.
func (a *App) RequestKMSCapture() bool {
	if a.perms == nil {
		return false
	}
	granted := a.perms.RequestKMSCapture(a.SunshineCapExecPath())
	if granted {
		a.syncSunshineCapExec()
		if err := a.RestartSunshine(); err != nil {
			log.Printf("[app] failed to restart Sunshine after granting KMS capability: %v", err)
		}
	}
	return granted
}

// RecheckKMSCapture re-syncs the capexec launcher's CAP_SYS_ADMIN capability
// from its current on-disk state and restarts Sunshine if it's now granted.
// Unlike RequestKMSCapture, this never runs pkexec itself — it exists for
// the case where something else already granted the capability on this
// exact path (a GUI thin client's own local pkexec call, see
// internal/adminapi.Server.handleKMSRecheck and cmd/usbridge_agent's
// runThinClientGUI) and this instance just needs to notice and pick it up.
func (a *App) RecheckKMSCapture() bool {
	a.syncSunshineCapExec()
	granted := a.KMSCaptureGranted()
	if granted {
		if err := a.RestartSunshine(); err != nil {
			log.Printf("[app] failed to restart Sunshine after KMS recheck: %v", err)
		}
	}
	return granted
}

// GPUClockLockSupported reports whether this platform's permissions backend
// can attempt an NVML GPU-clock lock at all -- true only on Windows (see
// internal/permissions/service_windows.go); every other platform's Service
// stubs this to false since there's no equivalent NVML-idle-stall problem
// documented there yet.
func (a *App) GPUClockLockSupported() bool {
	if a.perms == nil {
		return false
	}
	return a.perms.GPUClockLockSupported()
}

// LockGPUClocksEnabled returns the persisted "Lock GPU clocks" setting.
func (a *App) LockGPUClocksEnabled() bool {
	return a.cfg.LockGPUClocksEnabled
}

// SetLockGPUClocksEnabled persists the "Lock GPU clocks" setting and, if
// turning it on, immediately arms the lock (see applyGPUClockLock) instead of
// waiting for the next stream-host start. Turning it off does NOT tear down
// an already-armed lock daemon -- it only stops future arming; the daemon
// watches this agent's own PID (not the stream host's -- see
// applyGPUClockLock) and exits on its own once the agent process does, see
// permissions.Service.RequestGPUClockLock.
func (a *App) SetLockGPUClocksEnabled(enabled bool) error {
	next := a.cfg
	next.LockGPUClocksEnabled = enabled
	if err := a.SaveConfig(next); err != nil {
		return err
	}
	if enabled {
		a.applyGPUClockLock()
	}
	return nil
}

// applyGPUClockLock launches the elevated GPU-clock-lock daemon, if the
// setting is enabled and supported on this platform, watching *this agent
// process's own* PID rather than the stream host's. NVML's clock lock is
// GPU-wide, not scoped to whichever process the daemon happens to watch --
// watch-pid only exists so the daemon knows when to exit and release it --
// so tying it to the agent's own (long-lived, restart-free) PID instead of
// the stream host's means arming it once per agent run is enough: it keeps
// holding the lock across every later stream-host restart (sunshineWatchdog
// re-invoking startSunshine every 15s, or SetSunshineOutputName switching
// the captured monitor, both of which hand the stream host a brand new PID)
// without ever relaunching the elevated helper or popping a second UAC
// prompt. That matters because a UAC prompt runs on the secure desktop and
// can't be dismissed from a remote session -- switching monitors mid-stream
// used to hard-lock the session behind an unreachable local prompt. The
// trade-off: clocks now stay locked for as long as the agent runs with the
// setting on, not just while a stream host happens to be alive.
// gpuClockArmed guards against re-launching the helper (and prompting again)
// on every call; errors (including a declined UAC prompt) are logged only,
// never fatal to starting the stream, and deliberately leave gpuClockArmed
// unset so the next call retries instead of giving up silently forever.
func (a *App) applyGPUClockLock() {
	if !a.cfg.LockGPUClocksEnabled || a.perms == nil || a.stream == nil {
		return
	}
	if !a.perms.GPUClockLockSupported() {
		return
	}
	binPath := a.stream.BinaryPath()
	if binPath == "" {
		return
	}
	a.gpuClockMu.Lock()
	alreadyArmed := a.gpuClockArmed
	a.gpuClockMu.Unlock()
	if alreadyArmed {
		return
	}
	if err := a.perms.RequestGPUClockLock(binPath, os.Getpid()); err != nil {
		log.Printf("[app] failed to lock GPU clocks: %v", err)
		return
	}
	a.gpuClockMu.Lock()
	a.gpuClockArmed = true
	a.gpuClockMu.Unlock()
}

// TailscaleStatus returns the current Tailscale status in the format expected by /api/auth/sync.
func (a *App) TailscaleStatus() *api.TailscaleStatusInfo {
	if a.ts == nil {
		return nil
	}
	status, err := a.ts.Status(context.Background())
	if err != nil || status == nil {
		return nil
	}
	return toTailscaleStatusInfo(status)
}

// RegisterTailscale authorizes this node on the tailnet, used by /api/auth/sync
// and /api/auth/tailscale/register. An empty authKey triggers interactive login
// and returns an AuthURL for the caller to open in a browser.
func (a *App) RegisterTailscale(ctx context.Context, authKey, hostname string) (*api.TailscaleStatusInfo, error) {
	if a.ts == nil {
		return nil, fmt.Errorf("tailscale service unavailable")
	}
	status, err := a.ts.Register(ctx, authKey, hostname)
	if err != nil {
		return nil, err
	}
	return toTailscaleStatusInfo(status), nil
}

func toTailscaleStatusInfo(status *tailscale.Status) *api.TailscaleStatusInfo {
	if status == nil {
		return nil
	}
	return &api.TailscaleStatusInfo{
		Running:  status.Running,
		LoggedIn: status.LoggedIn,
		Backend:  status.Backend,
		DNSName:  status.Self.DNSName,
		HostName: status.Self.HostName,
		IP4:      status.Self.IP4,
		AuthURL:  status.AuthURL,
	}
}

// QRLink returns the quick-connect deep link and the master key for the /api/auth/qr/link endpoint.
func (a *App) QRLink() (string, string) {
	masterKey := strings.TrimSpace(a.cfg.MasterKey)
	if masterKey == "" {
		return "", ""
	}
	internalHost := localIPv4()
	tailscaleHost := ""
	if a.ts != nil {
		if status, err := a.ts.Status(context.Background()); err == nil && status != nil && status.LoggedIn {
			switch {
			case strings.TrimSpace(status.Self.IP4) != "":
				tailscaleHost = strings.TrimSpace(status.Self.IP4)
			case strings.TrimSpace(status.Self.DNSName) != "":
				tailscaleHost = strings.TrimSpace(status.Self.DNSName)
			}
		}
	}
	link := buildQRLink(internalHost, tailscaleHost, masterKey)
	return link, masterKey
}

// applyStreamSharedSecret hands secret to stream if it implements the
// optional interface{ SetSharedSecret([]byte) } -- only rustshineBackend
// does today (see its own doc comment). Same optional-interface probe
// pattern the client GUI layer already uses for SetAPISecret/
// SetTailscaleService on service.VideoClient implementations; a no-op for
// sunshineBackend, which has no equivalent (real Sunshine's own protocol
// has nothing this key would authenticate).
func applyStreamSharedSecret(stream streamhost.Backend, secret []byte) {
	if setter, ok := stream.(interface{ SetSharedSecret([]byte) }); ok {
		setter.SetSharedSecret(secret)
	}
}

// applyStreamWebRTCEnabled hands enabled to stream if it implements the
// optional interface{ SetWebRTCEnabled(bool) } -- only rustshineBackend
// does today, same optional-interface probe pattern as
// applyStreamSharedSecret above; a no-op for sunshineBackend.
func applyStreamWebRTCEnabled(stream streamhost.Backend, enabled bool) {
	if setter, ok := stream.(interface{ SetWebRTCEnabled(bool) }); ok {
		setter.SetWebRTCEnabled(enabled)
	}
}

func buildQRLink(internalHost, tailscaleHost, masterKey string) string {
	if masterKey == "" {
		return ""
	}
	if internalHost == "" && tailscaleHost == "" {
		return ""
	}
	values := url.Values{}
	if internalHost != "" {
		values.Set("internal_host", internalHost)
	}
	if tailscaleHost != "" {
		values.Set("tailscale_host", tailscaleHost)
	}
	values.Set("master_key", masterKey)
	return "usbridge://connect?" + values.Encode()
}

func localIPv4() string {
	return netutil.PreferredIPv4()
}

func (a *App) SaveConfig(cfg config.Config) error {
	if err := config.Save(a.cfgPath, cfg); err != nil {
		return err
	}
	a.cfg = cfg
	return nil
}

func (a *App) Status() api.SystemStatus {
	return api.SystemStatus{
		Service: api.ServiceStatus{
			Status:    "running",
			Timestamp: time.Now(),
			Uptime:    time.Since(a.state.startedAt).String(),
		},
		Timestamp: time.Now(),
		OS:        runtime.GOOS,
		Streamer:  a.StreamerName(),
	}
}

func (a *App) DeviceInfo() api.DeviceInfoResponse {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	out := make([]api.DeviceInfo, len(a.state.devices))
	copy(out, a.state.devices)
	return api.DeviceInfoResponse{
		Devices:         out,
		Count:           len(out),
		MountInProgress: a.state.mountInProgress,
		LastMountError:  a.state.lastMountError,
		AgentOS:         capture.GetOSInfo(),
		AgentDisplay:    capture.GetDisplayServer(),
	}
}

func (a *App) ReplaceDevices(reqs []api.DeviceRequest) error {
	now := time.Now()
	devices := make([]api.DeviceInfo, 0, len(reqs))
	for _, req := range reqs {
		if req.Device == "rndis" {
			continue
		}
		deviceType := normalizeDeviceType(req)
		deviceName := strings.TrimSpace(req.ProductName)
		if deviceName == "" {
			deviceName = strings.TrimSpace(req.Server)
		}
		if deviceName == "" {
			deviceName = req.Device
		}

		devices = append(devices, api.DeviceInfo{
			ID:           len(devices) + 1,
			Device:       req.Device,
			Status:       "connected",
			VendorID:     req.VendorID,
			ProductID:    req.ProductID,
			ProductName:  req.ProductName,
			Manufacturer: req.Manufacturer,
			CreatedAt:    now,
			Server:       req.Server,
			Port:         req.Port,
			Type:         deviceType,
			Name:         deviceName,
		})
	}
	log.Printf("[app] devices active=%d", len(devices))
	a.state.mu.Lock()
	a.state.devices = devices
	a.state.mu.Unlock()
	return nil
}

func normalizeDeviceType(req api.DeviceRequest) string {
	switch req.Device {
	case "keyboard":
		return "keyboard"
	case "mouse":
		if strings.TrimSpace(req.Type) != "" {
			return req.Type
		}
		return "mouse"
	case "mtp":
		return "mtp"
	case "drive":
		if req.Port > 0 {
			return "nbd"
		}
		return "local"
	default:
		if strings.TrimSpace(req.Type) != "" {
			return req.Type
		}
		return req.Device
	}
}

func (a *App) ClearDevices() error {
	a.state.mu.Lock()
	a.state.devices = nil
	a.state.mountInProgress = false
	a.state.lastMountError = ""
	a.state.mu.Unlock()
	return nil
}

func (a *App) Input() interface {
	Key(uint8) error
	Combo(uint8, uint8) error
	Text(string) error
	MouseMove(int8, int8) error
	MouseClick(uint8) error
	MouseScroll(int8) error
	MouseAction(uint8, int8, int8, int8) error
	AbsoluteEvent(uint8, uint16, uint16, int8) error
} {
	return a.input
}

func (a *App) Screen() interface {
	Snapshot() (*api.ScreenSnapshot, error)
} {
	return a.screen
}

// Clipboard returns the agent's clipboard sync manager.
func (a *App) Clipboard() *clipboard.Manager {
	return a.clipboard
}

// ClipboardMaxBytes returns the configured per-transfer size cap for
// clipboard image/file payloads.
func (a *App) ClipboardMaxBytes() int64 {
	return a.cfg.ClipboardMaxBytes
}

// VideoDevices reports real display metadata (native resolution, supported
// FPS modes) — descriptive only, no capture process is spawned here.
// Sunshine does the actual capturing/encoding.
func (a *App) VideoDevices() []api.VideoDeviceInfo {
	return a.screen.Devices()
}

// AudioSinks enumerates real system audio output devices the client can
// choose for Sunshine to capture from.
func (a *App) AudioSinks() ([]api.AudioSink, error) {
	return audio.ListSinks()
}

// CurrentAudioSink returns the sink Sunshine is configured to use, falling
// back to the system default sink if Sunshine has no explicit override.
func (a *App) CurrentAudioSink() (string, error) {
	if a.stream != nil {
		if sink := a.stream.AudioSink(); sink != "" {
			return sink, nil
		}
	}
	return audio.DefaultSink()
}

// SunshineStreamHost returns the IP Sunshine advertises to Moonlight clients
// (external_ip from sunshine.conf, or Tailscale IP if not explicitly set).
func (a *App) SunshineStreamHost() string {
	if a.stream == nil {
		return ""
	}
	if ip := a.stream.ExternalIP(); ip != "" && ip != "0.0.0.0" {
		return ip
	}
	return ""
}

// SunshineAdminPort returns the Sunshine web admin / NvHTTP base port.
func (a *App) SunshineAdminPort() int {
	if a.cfg.SunshinePort > 0 {
		return a.cfg.SunshinePort
	}
	return 47990
}

// StreamerName returns a short human-readable label for which streaming
// host backend this build is actually running (e.g. "Sunshine (Open
// Source)" or "RustShine (Proprietary)") — display only, see
// streamhost.Identity.
func (a *App) StreamerName() string {
	if a.stream == nil {
		return "unknown"
	}
	return a.stream.DisplayName()
}

// AdminUser returns the streaming host's admin-API username.
func (a *App) AdminUser() string {
	if a.stream == nil {
		return ""
	}
	return a.stream.AdminUser()
}

// AdminPass returns the streaming host's current session admin password.
func (a *App) AdminPass() string {
	if a.stream == nil {
		return ""
	}
	return a.stream.AdminPass()
}

// ListSunshineClients returns Moonlight clients currently paired with the
// bundled Sunshine instance.
func (a *App) ListSunshineClients() ([]streamhost.Client, error) {
	if a.stream == nil {
		return nil, nil
	}
	port := a.cfg.SunshinePort
	if port == 0 {
		port = 47990
	}
	return a.stream.ListClients(port)
}

// CurrentVideoCodec returns the codec negotiated by the most recent stream,
// defaulting to "h264" if unable to determine.
func (a *App) CurrentVideoCodec() string {
	if a.stream != nil {
		return a.stream.CurrentVideoCodec()
	}
	return "h264"
}

// SupportedVideoCodecs returns which of h264/h265/av1 this host's hardware
// encoder can actually produce right now, per Sunshine's own live capability
// probe (its /serverinfo ServerCodecModeSupport field) — not a static list.
func (a *App) SupportedVideoCodecs() []string {
	if a.stream == nil {
		return []string{"h264"}
	}
	port := a.cfg.SunshinePort
	if port == 0 {
		port = 47990
	}
	return a.stream.SupportedVideoCodecs(port)
}

// UnpairSunshineClient removes the Moonlight client with the given UUID from
// Sunshine's authorized client list.
func (a *App) UnpairSunshineClient(uniqueID string) error {
	if a.stream == nil {
		return nil
	}
	port := a.cfg.SunshinePort
	if port == 0 {
		port = 47990
	}
	return a.stream.UnpairClient(port, uniqueID)
}

// UpdateListenAddr updates the agent's HTTP listen host and port, persists the
// config, and hot-restarts the main HTTP server so the change takes effect immediately.
func (a *App) UpdateListenAddr(host string, port int) (config.Config, error) {
	a.cfg.ListenHost = host
	a.cfg.HTTPPort = port
	if err := config.Save(a.cfgPath, a.cfg); err != nil {
		return a.cfg, err
	}
	go a.restartMainHTTP()
	return a.cfg, nil
}

// restartMainHTTP shuts down the current main HTTP server and starts a new one
// on the address currently in a.cfg. Used for hot-apply of listen address changes.
func (a *App) restartMainHTTP() {
	old := a.server
	if old != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = old.Shutdown(ctx)
	}
	addr := fmt.Sprintf("%s:%d", a.cfg.EffectiveListenHost(), a.cfg.HTTPPort)
	next := &http.Server{
		Addr:              addr,
		Handler:           a.handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	a.server = next
	log.Printf("[app] http restarted on %s", addr)
	if err := next.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("[app] http server error: %v", err)
	}
}

// UpdateSunshinePort updates the Sunshine admin API port in agent config and
// in sunshine.conf, then restarts Sunshine so the change takes effect.
// port is the admin/web port (e.g. 47990); sunshine.conf receives port-1 (NvHTTP base).
func (a *App) UpdateSunshinePort(port int) (config.Config, error) {
	a.cfg.SunshinePort = port
	if err := config.Save(a.cfgPath, a.cfg); err != nil {
		return a.cfg, err
	}
	// Sunshine's `port` key is the NvHTTP base port; admin is at base+1.
	// SunshinePort is the admin port, so write base = SunshinePort - 1.
	if a.stream != nil {
		_ = a.stream.SetConfigKey("port", strconv.Itoa(port-1))
	}
	_ = a.RestartSunshine()
	return a.cfg, nil
}

// UpdateSunshineStreamAddr sets the IP Sunshine advertises to Moonlight clients
// (external_ip in sunshine.conf) and the streaming port, then restarts Sunshine.
func (a *App) UpdateSunshineStreamAddr(host string, streamPort int) (config.Config, error) {
	webPort := streamPort + 1 // admin port = NvHTTP base + 1
	a.cfg.SunshinePort = webPort
	if err := config.Save(a.cfgPath, a.cfg); err != nil {
		return a.cfg, err
	}
	if a.stream != nil {
		_ = a.stream.SetExternalIP(host)
		// Only restrict Sunshine's kernel bind to this IP for real (LAN) hosts —
		// never for the agent's own tsnet IP, which has no kernel interface to
		// bind to. tsnet reachability is handled by restartStreamProxy instead.
		if !a.isTsnetSelfIP(host) {
			_ = a.stream.SetBindAddress(host)
		} else {
			_ = a.stream.SetBindAddress("")
		}
		// Write streamPort (NvHTTP base) to sunshine.conf, not webPort (admin port).
		_ = a.stream.SetConfigKey("port", strconv.Itoa(streamPort))
	}
	_ = a.RestartSunshine()
	return a.cfg, nil
}

// SubmitMoonlightPIN sends the PIN shown by a Moonlight client to Sunshine
// to complete the pairing handshake.
func (a *App) SubmitMoonlightPIN(pin string) error {
	if a.stream == nil {
		return nil
	}
	port := a.cfg.SunshinePort
	if port == 0 {
		port = 47990
	}
	return a.stream.SubmitPIN(port, pin)
}

// SetAudioSink points Sunshine at the given audio device (sunshine.conf's
// audio_sink) and restarts it so the change takes effect. If Sunshine is
// already running with this exact sink, it's left alone — every client
// session start used to unconditionally kill and relaunch Sunshine even
// when nothing changed, causing a needless restart (and brief capture
// interruption) on every connect.
func (a *App) SetAudioSink(sink string) error {
	if a.stream == nil {
		return nil
	}
	unchanged := a.stream.AudioSink() == sink && a.stream.Running()
	if err := a.stream.SetAudioSink(sink); err != nil {
		return fmt.Errorf("write sunshine.conf: %w", err)
	}
	if unchanged {
		return nil
	}
	return a.RestartSunshine()
}

// SunshineOutputName returns the monitor Sunshine is pinned to capture
// (Sunshine's own connected-output index, stringified), read from
// sunshine.conf if present, falling back to the persisted agent config.
func (a *App) SunshineOutputName() string {
	if a.stream != nil {
		if name := a.stream.OutputName(); name != "" {
			return name
		}
	}
	return a.cfg.SunshineOutputName
}

// SetSunshineOutputName pins Sunshine's capture to the given monitor
// (Sunshine's connected-output index, stringified, or "" to auto-pick),
// persists it into both sunshine.conf and the agent config, and restarts
// Sunshine so the change takes effect.
func (a *App) SetSunshineOutputName(name string) error {
	if a.stream == nil {
		return nil
	}
	unchanged := a.stream.OutputName() == name && a.stream.Running()
	if err := a.stream.SetOutputName(name); err != nil {
		return fmt.Errorf("write sunshine.conf: %w", err)
	}
	next := a.cfg
	next.SunshineOutputName = name
	if err := a.SaveConfig(next); err != nil {
		return err
	}
	if unchanged {
		return nil
	}
	return a.RestartSunshine()
}
