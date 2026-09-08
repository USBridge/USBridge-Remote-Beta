package adminapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"usbridge_agent/internal/account"
	"usbridge_agent/internal/config"
	"usbridge_agent/internal/entitlement"
	"usbridge_agent/internal/streamhost"
	"usbridge_agent/internal/tailscale"
)

// TokenBackend mirrors the operations internal/ui.Window drives on the
// engine that owns the agent's config/Sunshine lifecycle (normally *app.App).
type TokenBackend interface {
	RegenerateMasterKey() (config.Config, error)
	SaveConfig(config.Config) error
	SunshineCaptureMode() string
	SetSunshineCaptureMode(mode string) error
	KMSCaptureGranted() bool
	RequestKMSCapture() bool
	SunshineCapExecPath() string
	RecheckKMSCapture() bool
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
	AdminUser() string
	AdminPass() string
	SunshineStreamHost() string
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

	// Account login (see internal/account) -- see internal/ui.TokenProvider's
	// own copy of this same doc comment.
	AccountStatus() account.Status
	StartAccountLogin() (string, error)
	CancelAccountLogin()
	RefreshAccountLicenses()
	RebindLicenseToThisDevice(oldIdentifier string) error
	LogoutAccount() error
}

// PermsBackend mirrors internal/permissions.Service's methods.
type PermsBackend interface {
	AccessibilityGranted() bool
	ScreenRecordingGranted() bool
	RequestAccessibility() bool
	RequestScreenRecording() bool
	OpenPrivacySettings() error
	OpenScreenRecordingSettings() error
}

// TSBackend mirrors internal/tailscale.Service's methods used by the GUI.
type TSBackend interface {
	Status(context.Context) (*tailscale.Status, error)
	StartLogin(context.Context) (string, error)
	Logout(context.Context) error
	SetAuthURLHandler(func(string))
}

// Server exposes TokenBackend/PermsBackend/TSBackend over a local unix
// socket so a GUI process can attach to an already-running headless agent
// instead of starting a second engine. Never listens on any network
// interface — socketPath is a filesystem path under the agent's StateDir,
// permissioned 0600 so only the owning user can connect.
type Server struct {
	socketPath string
	token      TokenBackend
	perms      PermsBackend
	ts         TSBackend
	cfgFn      func() config.Config

	listener net.Listener
	httpSrv  *http.Server

	mu      sync.Mutex
	subs    map[int]chan string
	nextSub int
}

// NewServer builds the socket listener and route table but does not start
// serving — call Serve (typically in its own goroutine) to do that.
func NewServer(socketPath string, token TokenBackend, perms PermsBackend, ts TSBackend, cfgFn func() config.Config) (*Server, error) {
	// A stale socket file left behind by a previous crash makes a fresh
	// Listen fail with "address already in use" even though nothing is
	// listening on it — remove it first. If another instance actually IS
	// live, the caller already probed for that via Dial before ever
	// reaching here (see app.Start), so this is safe.
	_ = os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen admin socket: %w", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		ln.Close()
		return nil, fmt.Errorf("chmod admin socket: %w", err)
	}

	s := &Server{
		socketPath: socketPath,
		token:      token,
		perms:      perms,
		ts:         ts,
		cfgFn:      cfgFn,
		listener:   ln,
		subs:       make(map[int]chan string),
	}

	if ts != nil {
		ts.SetAuthURLHandler(s.broadcastAuthURL)
	}

	s.httpSrv = &http.Server{Handler: s.routes()}
	return s, nil
}

// Serve blocks, accepting connections until Close is called. Run it in its
// own goroutine.
func (s *Server) Serve() error {
	err := s.httpSrv.Serve(s.listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Close shuts down the listener and removes the socket file.
func (s *Server) Close() error {
	err := s.httpSrv.Close()
	_ = os.Remove(s.socketPath)
	return err
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.cfgFn())
	})

	mux.HandleFunc("POST /token/regenerate-master-key", s.handleRegenerateMasterKey)
	mux.HandleFunc("POST /token/save-config", s.handleSaveConfig)
	mux.HandleFunc("GET /token/capture-mode", s.handleGetCaptureMode)
	mux.HandleFunc("POST /token/capture-mode", s.handleSetCaptureMode)
	mux.HandleFunc("GET /token/kms-granted", s.handleKMSGranted)
	mux.HandleFunc("POST /token/request-kms", s.handleRequestKMS)
	mux.HandleFunc("GET /token/kms-capexec-path", s.handleKMSCapExecPath)
	mux.HandleFunc("POST /token/kms-recheck", s.handleKMSRecheck)
	mux.HandleFunc("GET /token/gpu-clock-lock-supported", s.handleGPUClockLockSupported)
	mux.HandleFunc("GET /token/gpu-clock-lock-enabled", s.handleGPUClockLockEnabled)
	mux.HandleFunc("POST /token/gpu-clock-lock-enabled", s.handleSetGPUClockLockEnabled)
	mux.HandleFunc("POST /token/restart-sunshine", s.handleRestartSunshine)
	mux.HandleFunc("GET /token/clients", s.handleListClients)
	mux.HandleFunc("POST /token/unpair", s.handleUnpair)
	mux.HandleFunc("POST /token/pin", s.handlePIN)
	mux.HandleFunc("POST /token/listen-addr", s.handleListenAddr)
	mux.HandleFunc("POST /token/sunshine-port", s.handleSunshinePort)
	mux.HandleFunc("POST /token/sunshine-stream-addr", s.handleSunshineStreamAddr)
	mux.HandleFunc("GET /token/admin-credentials", s.handleAdminCredentials)
	mux.HandleFunc("GET /token/sunshine-stream-host", s.handleSunshineStreamHost)
	mux.HandleFunc("GET /token/streamer-name", s.handleStreamerName)
	mux.HandleFunc("GET /token/entitlement-status", s.handleEntitlementStatus)
	mux.HandleFunc("POST /token/start-trial", s.handleStartTrial)
	mux.HandleFunc("POST /token/start-purchase", s.handleStartPurchase)
	mux.HandleFunc("POST /token/cancel-purchase", s.handleCancelPurchase)
	mux.HandleFunc("POST /token/clear-license", s.handleClearLicense)
	mux.HandleFunc("POST /token/download-rustshine", s.handleDownloadRustShine)
	mux.HandleFunc("POST /token/check-rustshine-update", s.handleCheckRustShineUpdateNow)
	mux.HandleFunc("POST /token/set-stream-backend", s.handleSetStreamBackend)
	mux.HandleFunc("POST /token/set-rustshine-webrtc-enabled", s.handleSetRustShineWebRTCEnabled)
	mux.HandleFunc("GET /token/account-status", s.handleAccountStatus)
	mux.HandleFunc("POST /token/start-account-login", s.handleStartAccountLogin)
	mux.HandleFunc("POST /token/cancel-account-login", s.handleCancelAccountLogin)
	mux.HandleFunc("POST /token/refresh-account-licenses", s.handleRefreshAccountLicenses)
	mux.HandleFunc("POST /token/rebind-license", s.handleRebindLicense)
	mux.HandleFunc("POST /token/logout-account", s.handleLogoutAccount)

	mux.HandleFunc("GET /perms/accessibility", s.handlePermsBool(func() bool { return s.perms.AccessibilityGranted() }))
	mux.HandleFunc("GET /perms/screen-recording", s.handlePermsBool(func() bool { return s.perms.ScreenRecordingGranted() }))
	mux.HandleFunc("POST /perms/request-accessibility", s.handlePermsBool(func() bool { return s.perms.RequestAccessibility() }))
	mux.HandleFunc("POST /perms/request-screen-recording", s.handlePermsBool(func() bool { return s.perms.RequestScreenRecording() }))
	mux.HandleFunc("POST /perms/open-privacy-settings", s.handlePermsErr(func() error { return s.perms.OpenPrivacySettings() }))
	mux.HandleFunc("POST /perms/open-screen-recording-settings", s.handlePermsErr(func() error { return s.perms.OpenScreenRecordingSettings() }))

	mux.HandleFunc("GET /ts/status", s.handleTSStatus)
	mux.HandleFunc("POST /ts/login", s.handleTSLogin)
	mux.HandleFunc("POST /ts/logout", s.handleTSLogout)
	mux.HandleFunc("GET /ts/auth-stream", s.handleTSAuthStream)

	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusInternalServerError, errorBody{Error: err.Error()})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func (s *Server) handleRegenerateMasterKey(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.token.RegenerateMasterKey()
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	var cfg config.Config
	if err := readJSON(r, &cfg); err != nil {
		writeError(w, err)
		return
	}
	if err := s.token.SaveConfig(cfg); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleGetCaptureMode(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, captureModeBody{Mode: s.token.SunshineCaptureMode()})
}

func (s *Server) handleSetCaptureMode(w http.ResponseWriter, r *http.Request) {
	var body captureModeBody
	if err := readJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	if err := s.token.SetSunshineCaptureMode(body.Mode); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleKMSGranted(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, boolBody{Value: s.token.KMSCaptureGranted()})
}

func (s *Server) handleRequestKMS(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, boolBody{Value: s.token.RequestKMSCapture()})
}

// handleKMSCapExecPath exposes the sunshine_capexec launcher path so a GUI
// thin client can run the pkexec setcap grant itself, in its own session,
// instead of asking this (headless, session-less) instance to do it — see
// RecheckKMSCapture and cmd/usbridge_agent's runThinClientGUI.
func (s *Server) handleKMSCapExecPath(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, stringBody{Value: s.token.SunshineCapExecPath()})
}

// handleKMSRecheck re-syncs the capexec launcher's capability from its
// current on-disk state and restarts Sunshine if it's now granted. Used
// after a GUI thin client grants CAP_SYS_ADMIN itself (via its own local
// pkexec) so this instance picks up the change.
func (s *Server) handleKMSRecheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, boolBody{Value: s.token.RecheckKMSCapture()})
}

func (s *Server) handleGPUClockLockSupported(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, boolBody{Value: s.token.GPUClockLockSupported()})
}

func (s *Server) handleGPUClockLockEnabled(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, boolBody{Value: s.token.LockGPUClocksEnabled()})
}

func (s *Server) handleSetGPUClockLockEnabled(w http.ResponseWriter, r *http.Request) {
	var body boolBody
	if err := readJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	if err := s.token.SetLockGPUClocksEnabled(body.Value); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleRestartSunshine(w http.ResponseWriter, r *http.Request) {
	if err := s.token.RestartSunshine(); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleListClients(w http.ResponseWriter, r *http.Request) {
	clients, err := s.token.ListSunshineClients()
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, clients)
}

func (s *Server) handleUnpair(w http.ResponseWriter, r *http.Request) {
	var body unpairBody
	if err := readJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	if err := s.token.UnpairSunshineClient(body.UniqueID); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handlePIN(w http.ResponseWriter, r *http.Request) {
	var body pinBody
	if err := readJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	if err := s.token.SubmitMoonlightPIN(body.PIN); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleListenAddr(w http.ResponseWriter, r *http.Request) {
	var body listenAddrBody
	if err := readJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	cfg, err := s.token.UpdateListenAddr(body.Host, body.Port)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleSunshinePort(w http.ResponseWriter, r *http.Request) {
	var body sunshinePortBody
	if err := readJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	cfg, err := s.token.UpdateSunshinePort(body.Port)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleSunshineStreamAddr(w http.ResponseWriter, r *http.Request) {
	var body sunshineStreamAddrBody
	if err := readJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	cfg, err := s.token.UpdateSunshineStreamAddr(body.Host, body.StreamPort)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleAdminCredentials(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, adminCredentialsBody{User: s.token.AdminUser(), Pass: s.token.AdminPass()})
}

func (s *Server) handleSunshineStreamHost(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, stringBody{Value: s.token.SunshineStreamHost()})
}

func (s *Server) handleStreamerName(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, stringBody{Value: s.token.StreamerName()})
}

func (s *Server) handleEntitlementStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.token.EntitlementStatus())
}

func (s *Server) handleStartTrial(w http.ResponseWriter, r *http.Request) {
	if err := s.token.StartFreeTrial(); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleStartPurchase(w http.ResponseWriter, r *http.Request) {
	var body stringBody // Value = tier ("pro"/"enterprise")
	if err := readJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	url, err := s.token.StartPurchase(body.Value)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stringBody{Value: url})
}

func (s *Server) handleCancelPurchase(w http.ResponseWriter, r *http.Request) {
	s.token.CancelPurchase()
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleClearLicense(w http.ResponseWriter, r *http.Request) {
	if err := s.token.ClearLicense(); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleAccountStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.token.AccountStatus())
}

func (s *Server) handleStartAccountLogin(w http.ResponseWriter, r *http.Request) {
	url, err := s.token.StartAccountLogin()
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stringBody{Value: url})
}

func (s *Server) handleCancelAccountLogin(w http.ResponseWriter, r *http.Request) {
	s.token.CancelAccountLogin()
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleRefreshAccountLicenses(w http.ResponseWriter, r *http.Request) {
	s.token.RefreshAccountLicenses()
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleRebindLicense(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OldIdentifier string `json:"old_identifier"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	if err := s.token.RebindLicenseToThisDevice(body.OldIdentifier); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleLogoutAccount(w http.ResponseWriter, r *http.Request) {
	if err := s.token.LogoutAccount(); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

// handleDownloadRustShine kicks the download off in the engine process and
// returns immediately -- it can take tens of seconds to minutes, far longer
// than this HTTP request should stay open, especially over a thin client's
// connection to a separate engine process. Callers (both an engine-owning
// and a thin-client GUI) poll /token/entitlement-status for
// DownloadInProgress/Progress/RustShineStaged instead of waiting on this
// response. 200 (not 202) so this fits adminapi.Client.do's existing
// strict-200 success check without needing a special case there.
func (s *Server) handleDownloadRustShine(w http.ResponseWriter, r *http.Request) {
	go func() {
		if err := s.token.DownloadRustShine(nil); err != nil {
			logrus.WithError(err).Warn("rustshine download failed")
		}
	}()
	writeJSON(w, http.StatusOK, struct{}{})
}

// handleCheckRustShineUpdateNow mirrors handleDownloadRustShine's own
// fire-and-forget shape and doc comment -- the GUI's "Check for updates"
// button polls /token/entitlement-status for RustShineUpdateInProgress/
// RustShineVersion/LastError instead of waiting on this response.
func (s *Server) handleCheckRustShineUpdateNow(w http.ResponseWriter, r *http.Request) {
	go func() {
		if err := s.token.CheckRustShineUpdateNow(); err != nil {
			logrus.WithError(err).Warn("rustshine update check failed")
		}
	}()
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleSetStreamBackend(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind string `json:"kind"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	if err := s.token.SetStreamBackend(body.Kind); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleSetRustShineWebRTCEnabled(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	if err := s.token.SetRustShineWebRTCEnabled(body.Enabled); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handlePermsBool(fn func() bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, boolBody{Value: fn()})
	}
}

func (s *Server) handlePermsErr(fn func() error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, struct{}{})
	}
}

func (s *Server) handleTSStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.ts.Status(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleTSLogin(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	url, err := s.ts.StartLogin(ctx)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, loginBody{URL: url})
}

func (s *Server) handleTSLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.ts.Logout(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

// handleTSAuthStream holds the connection open and pushes one line per
// AuthURL event (see broadcastAuthURL) for as long as the client stays
// connected — the RPC equivalent of tailscale.Service.SetAuthURLHandler,
// since a callback can't cross the socket.
func (s *Server) handleTSAuthStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch := make(chan string, 4)
	id := s.subscribe(ch)
	defer s.unsubscribe(id)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case url := <-ch:
			if _, err := fmt.Fprintln(w, url); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) subscribe(ch chan string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextSub
	s.nextSub++
	s.subs[id] = ch
	return id
}

func (s *Server) unsubscribe(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subs, id)
}

func (s *Server) broadcastAuthURL(url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ch := range s.subs {
		select {
		case ch <- url:
		default:
			logrus.Warn("[adminapi] auth-stream subscriber too slow, dropping event")
		}
	}
}
