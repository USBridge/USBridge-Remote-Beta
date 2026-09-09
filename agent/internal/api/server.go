package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"usbridge_agent/internal/clipboard"
	"usbridge_agent/internal/display"
)

type Application interface {
	Status() SystemStatus
	DeviceInfo() DeviceInfoResponse
	ReplaceDevices([]DeviceRequest) error
	ClearDevices() error
	Input() interface {
		Key(uint8) error
		Combo(uint8, uint8) error
		Text(string) error
		MouseMove(int8, int8) error
		MouseClick(uint8) error
		MouseScroll(int8) error
		MouseAction(uint8, int8, int8, int8) error
		AbsoluteEvent(uint8, uint16, uint16, int8) error
	}
	Screen() interface {
		Snapshot() (*ScreenSnapshot, error)
	}
	VideoDevices() []VideoDeviceInfo
	// SunshineOutputName returns the monitor Sunshine is pinned to capture
	// (its connected-output index, stringified), or "" for auto-pick.
	SunshineOutputName() string
	// SetSunshineOutputName pins Sunshine's capture to the given monitor.
	SetSunshineOutputName(name string) error
	// SunshineStreamHost returns the IP Sunshine advertises to Moonlight clients
	// (external_ip from sunshine.conf, or "" if auto-detect).
	SunshineStreamHost() string
	// SunshineAdminPort returns the Sunshine web admin / NvHTTP port (default 47990).
	SunshineAdminPort() int
	// SubmitMoonlightPIN sends the PIN shown by a Moonlight client to the
	// streaming host to complete the pairing handshake.
	SubmitMoonlightPIN(pin string) error
	CurrentVideoCodec() string
	// SupportedVideoCodecs returns which of h264/h265/av1 the host's hardware
	// encoder can actually produce right now (Sunshine's live capability probe).
	SupportedVideoCodecs() []string
	// Color444Status reports the RustShine Pro color upgrade's state: active
	// is whether the current/most recent session actually negotiated 4:4:4
	// chroma, available is whether this host could offer it right now
	// (hardware AND license tier). Always (false, false) on Sunshine.
	Color444Status() (active bool, available bool)
	AudioSinks() ([]AudioSink, error)
	CurrentAudioSink() (string, error)
	SetAudioSink(sink string) error
	TailscaleStatus() *TailscaleStatusInfo
	RegisterTailscale(ctx context.Context, authKey, hostname string) (*TailscaleStatusInfo, error)
	// Clipboard returns the agent's clipboard sync manager, or nil if
	// clipboard sync isn't available.
	Clipboard() *clipboard.Manager
	// ClipboardMaxBytes returns the configured per-transfer size cap for
	// clipboard image/file payloads (0 means "use the built-in default").
	ClipboardMaxBytes() int64
}

type Server struct {
	app      Application
	upgrader websocket.Upgrader

	// keyMu guards masterKey — see SetMasterKey.
	keyMu        sync.RWMutex
	masterKey    []byte
	sunshinePort int
	sec          *SecurityMiddleware

	clipboardBlobs *clipboardBlobStore
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
}

func NewServer(app Application) *Server {
	return NewServerWithAuth(app, nil, 0)
}

func NewServerWithAuth(app Application, masterKey []byte, sunshinePort int) *Server {
	if sunshinePort == 0 {
		sunshinePort = 47990
	}
	return &Server{
		app:            app,
		upgrader:       websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		masterKey:      masterKey,
		sunshinePort:   sunshinePort,
		sec:            NewSecurityMiddleware(masterKey),
		clipboardBlobs: newClipboardBlobStore(),
	}
}

// SetMasterKey swaps in a freshly rotated master key on an already-running
// server (both the HMAC/AES-GCM secret used directly by handlers like Sync,
// and the copy SecurityMiddleware verifies incoming requests against) — so a
// regenerated key takes effect immediately instead of requiring an agent
// restart to reload it from disk.
func (s *Server) SetMasterKey(key []byte) {
	s.keyMu.Lock()
	s.masterKey = key
	s.keyMu.Unlock()
	s.sec.SetMasterKey(key)
}

func (s *Server) currentMasterKey() []byte {
	s.keyMu.RLock()
	defer s.keyMu.RUnlock()
	return s.masterKey
}

func (s *Server) Routes() http.Handler {
	sec := s.sec
	mux := http.NewServeMux()

	// --- Public endpoints (no HMAC required) ---
	mux.HandleFunc("/api/healthz", sec.Public(s.health))
	mux.HandleFunc("/api/auth/qr/link", sec.Public(s.AuthQRLink))

	// --- HMAC verified when present, allowed unsigned pre-pair ---
	mux.HandleFunc("/api/moonlight/pin", sec.OptionalVerify(s.MoonlightPIN))

	// --- AES-GCM auth only (no HMAC, rate-limited) ---
	mux.HandleFunc("/api/auth/sync", sec.LimitSync(s.Sync))

	// --- HMAC-authenticated endpoints ---
	mux.HandleFunc("/api/status", sec.LimitPolling(s.status))
	mux.HandleFunc("/api/service/status", sec.LimitPolling(s.status))
	mux.HandleFunc("/api/service/stop", sec.LimitPolling(s.serviceStop))
	mux.HandleFunc("/api/service/start", sec.LimitPolling(s.serviceStart))
	mux.HandleFunc("/api/service/restart", sec.LimitPolling(s.serviceRestart))
	mux.HandleFunc("/api/device/start", sec.LimitPolling(s.deviceStart))
	mux.HandleFunc("/api/device/stop", sec.LimitPolling(s.deviceStop))
	mux.HandleFunc("/api/device/info", sec.LimitPolling(s.deviceInfo))
	mux.HandleFunc("/api/device/status", sec.LimitPolling(s.deviceStatus))
	mux.HandleFunc("/api/drives/local", sec.LimitPolling(s.localDrives))
	mux.HandleFunc("/api/iso/space", sec.LimitPolling(s.isoSpace))
	mux.HandleFunc("/api/backup/get_snapshots", sec.LimitPolling(s.backupGetSnapshots))
	// Audio device selection: real sinks (pactl), applied to Sunshine's own
	// audio_sink config — the agent never captures audio itself.
	mux.HandleFunc("/api/audio/info", sec.LimitPolling(s.audioInfo))
	mux.HandleFunc("/api/audio/devices", sec.LimitPolling(s.audioDevices))
	mux.HandleFunc("/api/audio/start", sec.LimitPolling(s.audioStart))
	mux.HandleFunc("/api/audio/stop", sec.LimitPolling(s.audioStop))
	// Hardware-only features (SD/eMMC storage, on-device scripts) that this
	// software agent doesn't implement — stubbed as "nothing here" rather
	// than 404 so client polling doesn't treat them as protocol errors.
	mux.HandleFunc("/api/storage/status", sec.LimitPolling(s.storageStatus))
	mux.HandleFunc("/api/scripts/list", sec.LimitPolling(s.scriptsList))
	mux.HandleFunc("/api/scripts/status", sec.LimitPolling(s.scriptsStatus))
	mux.HandleFunc("/api/auth/tailscale/status", sec.LimitPolling(s.tailscaleStatus))
	mux.HandleFunc("/api/auth/tailscale/register", sec.LimitPolling(s.tailscaleRegister))
	mux.HandleFunc("/api/keyboard", sec.LimitRealtime(s.keyboard))
	mux.HandleFunc("/api/mouse", sec.LimitRealtime(s.mouse))
	mux.HandleFunc("/api/mouse/ws", sec.LimitRealtime(s.mouseWS))
	mux.HandleFunc("/api/clipboard/ws", sec.LimitRealtime(s.clipboardWS))
	mux.HandleFunc("PUT /api/clipboard/blob/{id}", sec.LimitBlobTransfer(s.clipboardBlobPut))
	mux.HandleFunc("GET /api/clipboard/blob/{id}", sec.LimitBlobTransfer(s.clipboardBlobGet))
	mux.HandleFunc("/api/video/info", sec.LimitPolling(s.videoInfo))
	mux.HandleFunc("/api/video/devices", sec.LimitPolling(s.videoDevices))
	mux.HandleFunc("/api/video/set_device", sec.LimitPolling(s.videoSetDevice))
	mux.HandleFunc("/api/screen", sec.LimitPolling(s.screen))
	mux.HandleFunc("/api/devices", sec.LimitPolling(s.devicesLegacy))
	mux.HandleFunc("/api/pcpanel/leds", sec.LimitPolling(s.leds))
	mux.HandleFunc("/api/pcpanel/button", sec.LimitPolling(s.button))
	// MCP (Model Context Protocol): JSON-RPC 2.0 over this one POST endpoint,
	// same shape and auth as the hardware KVM's own /api/mcp (see mcp.go) --
	// the client's MCP proxy (client/internal/api/mcp_proxy.go) forwards to
	// whichever of the two it's paired with without needing to know which.
	mux.HandleFunc("/api/mcp", sec.LimitPolling(s.mcp))

	return s.withCORS(s.withLogging(s.withRecovery(mux)))
}

// withCORS lets the browser/WASM web client (served from its own origin —
// either a separate static host, or the agent's own /app/ route once that
// lands, which would still count as a distinct origin from the page's point
// of view whenever it's opened via a different scheme/port during
// development) call this API with fetch(). Every other USBridge client
// (desktop, Android, iOS) talks over net/http with no browser CORS
// enforcement at all, so this is purely additive — it doesn't change what
// the HMAC/AES-GCM auth layer already guards. Reflecting the request's
// Origin (rather than a fixed "*") keeps this compatible with credentialed
// fetch() calls in case a later stage needs those; the actual auth secret
// still lives in the HMAC signature/AES-GCM payload, never in a cookie, so
// there's nothing CORS itself needs to protect against here.
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Auth-Signature, X-Auth-Timestamp, X-USBridge-Video-Trace")
		w.Header().Set("Access-Control-Max-Age", "600")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("🔥 [api] PANIC in %s %s: %v", r.Method, r.URL.Path, err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (w *loggingResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return h.Hijack()
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		traceID := strings.TrimSpace(r.Header.Get("X-USBridge-Video-Trace"))
		if traceID != "" {
			log.Printf("[http] -> %s %s from=%s trace=%s", r.Method, r.URL.RequestURI(), r.RemoteAddr, traceID)
		} else {
			log.Printf("[http] -> %s %s from=%s", r.Method, r.URL.RequestURI(), r.RemoteAddr)
		}
		lrw := &loggingResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(lrw, r)
		if traceID != "" {
			log.Printf("[http] <- %s %s status=%d dur=%s trace=%s", r.Method, r.URL.RequestURI(), lrw.status, time.Since(start).Round(time.Millisecond), traceID)
		} else {
			log.Printf("[http] <- %s %s status=%d dur=%s", r.Method, r.URL.RequestURI(), lrw.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

func (s *Server) respond(w http.ResponseWriter, status int, payload APIResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) ok(w http.ResponseWriter, message string, data interface{}) {
	s.respond(w, http.StatusOK, APIResponse{Success: true, Message: message, Data: data})
}

func (s *Server) fail(w http.ResponseWriter, status int, message string, err error) {
	details := ""
	if err != nil {
		details = err.Error()
	}
	s.respond(w, status, APIResponse{Success: false, Error: message, Details: details})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	s.ok(w, "OK", map[string]any{"status": "ok", "timestamp": time.Now().Unix()})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	s.ok(w, "status", s.app.Status())
}

func (s *Server) serviceStop(w http.ResponseWriter, r *http.Request) {
	log.Printf("[api] service_stop")
	_ = s.app.ClearDevices()
	s.ok(w, "service_stopped", nil)
}

func (s *Server) serviceStart(w http.ResponseWriter, r *http.Request) {
	log.Printf("[api] service_start")
	s.ok(w, "service_started", nil)
}

func (s *Server) serviceRestart(w http.ResponseWriter, r *http.Request) {
	log.Printf("[api] service_restart")
	_ = s.app.ClearDevices()
	s.ok(w, "service_restarted", nil)
}

func (s *Server) deviceStart(w http.ResponseWriter, r *http.Request) {
	devices, err := decodeDeviceStart(r)
	if err != nil {
		log.Printf("[api] device_start invalid_json: %v", err)
		s.fail(w, http.StatusBadRequest, "invalid_json", err)
		return
	}
	devices = filterDevices(devices)
	log.Printf("[api] device_start count=%d", len(devices))
	if err := s.app.ReplaceDevices(devices); err != nil {
		log.Printf("[api] device_start failed: %v", err)
		s.fail(w, http.StatusInternalServerError, "device_start_failed", err)
		return
	}
	s.ok(w, "devices_started", map[string]any{"count": len(devices)})
}

func (s *Server) deviceStop(w http.ResponseWriter, r *http.Request) {
	log.Printf("[api] device_stop")
	if err := s.app.ClearDevices(); err != nil {
		log.Printf("[api] device_stop failed: %v", err)
		s.fail(w, http.StatusInternalServerError, "device_stop_failed", err)
		return
	}
	s.ok(w, "devices_stopped", nil)
}

func (s *Server) deviceInfo(w http.ResponseWriter, r *http.Request) {
	info := s.app.DeviceInfo()
	types := make([]string, 0, len(info.Devices))
	for _, device := range info.Devices {
		types = append(types, fmt.Sprintf("%s:%s", device.Device, device.Type))
	}
	log.Printf("[api] device_info count=%d devices=%s", len(info.Devices), strings.Join(types, ","))
	s.ok(w, "device_info", info)
}

func (s *Server) deviceStatus(w http.ResponseWriter, r *http.Request) {
	info := s.app.DeviceInfo()
	names := make([]string, 0, len(info.Devices))
	for _, d := range info.Devices {
		names = append(names, d.Device)
	}
	s.ok(w, "device_status", MountDriveStatus{
		Available:         true,
		UnmountAvailable:  len(names) > 0,
		Version:           "software",
		ConnectedDevices:  names,
		KeyboardAvailable: true,
		RNDISAvailable:    false,
	})
}

func (s *Server) localDrives(w http.ResponseWriter, r *http.Request) {
	s.ok(w, "local_drives", map[string]any{"drives": []any{}, "count": 0, "paths": []string{}})
}

func (s *Server) isoSpace(w http.ResponseWriter, r *http.Request) {
	s.ok(w, "iso_space", map[string]any{
		"total_space":     0,
		"used_space":      0,
		"available_space": 0,
		"total_space_gb":  "0",
		"used_space_gb":   "0",
		"available_gb":    "0",
		"used_percent":    0,
		"iso_directory":   "",
	})
}

func (s *Server) backupGetSnapshots(w http.ResponseWriter, r *http.Request) {
	log.Printf("[api] backup_get_snapshots")
	s.ok(w, "snapshots", map[string]any{
		"count":     0,
		"total":     0,
		"snapshots": []any{},
	})
}

func (s *Server) audioInfo(w http.ResponseWriter, r *http.Request) {
	sink, err := s.app.CurrentAudioSink()
	if err != nil {
		s.ok(w, "audio_info", map[string]any{"streaming": false, "device_path": "", "device_name": "", "muted": false})
		return
	}

	deviceName := sink
	if sinks, err := s.app.AudioSinks(); err == nil {
		for _, sk := range sinks {
			if sk.Name == sink {
				deviceName = sk.Description
				break
			}
		}
	}

	s.ok(w, "audio_info", map[string]any{
		"streaming":   false,
		"device_path": sink,
		"device_name": deviceName,
		"muted":       false,
	})
}

func (s *Server) audioDevices(w http.ResponseWriter, r *http.Request) {
	sinks, err := s.app.AudioSinks()
	if err != nil {
		log.Printf("[api] audio_devices: %v", err)
		s.ok(w, "audio_devices", map[string]any{"devices": []any{}, "count": 0})
		return
	}
	devices := make([]map[string]any, 0, len(sinks))
	for _, sk := range sinks {
		devices = append(devices, map[string]any{
			"name":        sk.Description,
			"path":        sk.Name,
			"connected":   true,
			"description": sk.Description,
		})
	}
	s.ok(w, "audio_devices", map[string]any{"devices": devices, "count": len(devices)})
}

func (s *Server) audioStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DevicePath string `json:"device_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[api] audio_start invalid_json: %v", err)
		s.fail(w, http.StatusBadRequest, "invalid_json", err)
		return
	}
	log.Printf("[api] audio_start device=%s", req.DevicePath)
	if err := s.app.SetAudioSink(req.DevicePath); err != nil {
		log.Printf("[api] audio_start failed: %v", err)
		s.fail(w, http.StatusInternalServerError, "audio_start_failed", err)
		return
	}
	s.ok(w, "audio_started", nil)
}

func (s *Server) audioStop(w http.ResponseWriter, r *http.Request) {
	log.Printf("[api] audio_stop (reset to system default sink)")
	if err := s.app.SetAudioSink(""); err != nil {
		log.Printf("[api] audio_stop failed: %v", err)
		s.fail(w, http.StatusInternalServerError, "audio_stop_failed", err)
		return
	}
	s.ok(w, "audio_stopped", nil)
}

func (s *Server) storageStatus(w http.ResponseWriter, r *http.Request) {
	empty := map[string]any{
		"total": 0, "used": 0, "free": 0, "percent": 0,
		"total_human": "0", "used_human": "0", "free_human": "0",
		"mounted": false,
	}
	s.ok(w, "storage_status", map[string]any{"sdcard": empty, "emmc": empty})
}

func (s *Server) scriptsList(w http.ResponseWriter, r *http.Request) {
	s.ok(w, "scripts", []any{})
}

func (s *Server) scriptsStatus(w http.ResponseWriter, r *http.Request) {
	s.ok(w, "script_status", []any{})
}

func (s *Server) tailscaleStatus(w http.ResponseWriter, r *http.Request) {
	status := s.app.TailscaleStatus()
	if status == nil {
		status = &TailscaleStatusInfo{Backend: "unavailable"}
	}
	s.ok(w, "tailscale_status", status)
}

func (s *Server) tailscaleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AuthKey  string `json:"auth_key"`
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[api] tailscale_register invalid_json: %v", err)
		s.fail(w, http.StatusBadRequest, "invalid_json", err)
		return
	}
	log.Printf("[api] tailscale_register hostname=%s auth_key_present=%v", req.Hostname, req.AuthKey != "")
	status, err := s.app.RegisterTailscale(r.Context(), req.AuthKey, req.Hostname)
	if err != nil {
		log.Printf("[api] tailscale_register failed: %v", err)
		s.fail(w, http.StatusInternalServerError, "tailscale_register_failed", err)
		return
	}
	s.ok(w, "tailscale_registered", status)
}

func (s *Server) keyboard(w http.ResponseWriter, r *http.Request) {
	var req KeyboardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[api] keyboard invalid_json: %v", err)
		s.fail(w, http.StatusBadRequest, "invalid_json", err)
		return
	}
	log.Printf("[api] keyboard action=%s", req.Action)

	if badReq, err := s.applyKeyboard(req); badReq != "" {
		s.fail(w, http.StatusBadRequest, badReq, nil)
		return
	} else if err != nil {
		log.Printf("[api] keyboard failed: %v", err)
		s.fail(w, http.StatusInternalServerError, "keyboard_failed", err)
		return
	}
	s.ok(w, "keyboard_ok", nil)
}

func (s *Server) mouse(w http.ResponseWriter, r *http.Request) {
	var req MouseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[api] mouse invalid_json: %v", err)
		s.fail(w, http.StatusBadRequest, "invalid_json", err)
		return
	}
	log.Printf("[api] mouse action=%s", req.Action)

	if err := s.applyMouse(req); err != nil {
		log.Printf("[api] mouse failed: %v", err)
		s.fail(w, http.StatusInternalServerError, "mouse_failed", err)
		return
	}
	s.ok(w, "mouse_ok", MouseResponseData{})
}

const (
	wsReadTimeout  = 90 * time.Second
	wsPingInterval = 25 * time.Second
)

func (s *Server) mouseWS(w http.ResponseWriter, r *http.Request) {
	log.Printf("[api] mouse_ws incoming request from %s", r.RemoteAddr)
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[api] mouse_ws upgrade failed: %v", err)
		return
	}
	log.Printf("[api] mouse_ws upgraded successfully for %s", r.RemoteAddr)
	defer conn.Close()

	// Refresh read deadline on every pong so idle connections survive NAT/Tailscale
	conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		return nil
	})

	var writeMu sync.Mutex
	safePing := func() error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteMessage(websocket.PingMessage, []byte("ping"))
	}
	safeWriteJSON := func(v interface{}) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(v)
	}

	// Keepalive ping pusher
	stopPush := make(chan struct{})
	defer close(stopPush)

	go func() {
		pingTicker := time.NewTicker(wsPingInterval)
		defer pingTicker.Stop()

		for {
			select {
			case <-stopPush:
				return
			case <-pingTicker.C:
				if err := safePing(); err != nil {
					log.Printf("[api] mouse_ws ping failed, closing: %v", err)
					conn.Close()
					return
				}
			}
		}
	}()

	for {
		var req MouseRequest
		if err := conn.ReadJSON(&req); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("[api] mouse_ws read error: %v", err)
			}
			return
		}
		if err := s.applyMouse(req); err != nil {
			log.Printf("[api] mouse_ws failed action=%s: %v", req.Action, err)
			_ = safeWriteJSON(APIResponse{Success: false, Error: "mouse_failed", Details: err.Error()})
			continue
		}
		_ = safeWriteJSON(APIResponse{Success: true, Message: "ok", Data: MouseResponseData{}})
	}
}

// applyKeyboard dispatches a decoded KeyboardRequest against s.app.Input(),
// shared by the /api/keyboard HTTP handler and the MCP keyboard.send tool
// (see mcp.go) so the two don't drift on validation. badReq is a non-empty
// APIResponse.Error code when the request itself was malformed (missing a
// required field for its action, or an unrecognized action) -- distinct
// from err, which is a real Input() failure at the OS level.
func (s *Server) applyKeyboard(req KeyboardRequest) (badReq string, err error) {
	switch req.Action {
	case "key":
		if req.KeyCode == nil {
			return "missing_key_code", nil
		}
		return "", s.app.Input().Key(*req.KeyCode)
	case "combo":
		if req.KeyCode == nil || req.Modifiers == nil {
			return "missing_combo_fields", nil
		}
		return "", s.app.Input().Combo(*req.Modifiers, *req.KeyCode)
	case "text":
		if req.Text == nil {
			return "missing_text", nil
		}
		return "", s.app.Input().Text(*req.Text)
	default:
		return "unsupported_action", nil
	}
}

func (s *Server) applyMouse(req MouseRequest) error {
	switch req.Action {
	case "move":
		return s.app.Input().MouseMove(ptrInt8(req.DX), ptrInt8(req.DY))
	case "click":
		return s.app.Input().MouseClick(ptrUint8(req.Button))
	case "scroll":
		return s.app.Input().MouseScroll(ptrInt8(req.Scroll))
	case "action":
		return s.app.Input().MouseAction(ptrUint8(req.Button), ptrInt8(req.DX), ptrInt8(req.DY), ptrInt8(req.Scroll))
	case "touch", "touch_position", "absolute_event":
		return s.app.Input().AbsoluteEvent(ptrUint8(req.ButtonState), uint16(ptrInt(req.X)), uint16(ptrInt(req.Y)), ptrInt8(req.Scroll))
	default:
		return nil
	}
}

func (s *Server) videoInfo(w http.ResponseWriter, r *http.Request) {
	devices := s.app.VideoDevices()
	requested := strings.TrimSpace(r.URL.Query().Get("device"))

	var selected *VideoDeviceInfo
	for i := range devices {
		if devices[i].Path == requested {
			selected = &devices[i]
			break
		}
	}
	if selected == nil && len(devices) > 0 {
		selected = &devices[0]
	}

	devicePath := "desktop"
	width, height, fps := 1920, 1080, 60
	var modes []VideoCaptureMode
	if selected != nil {
		devicePath = selected.Path
		modes = selected.SupportedModes
		if len(modes) > 0 {
			width, height = modes[0].Width, modes[0].Height
			if n := len(modes[0].FPS); n > 0 {
				fps = modes[0].FPS[n-1]
			}
		}
	} else if dw, dh, ok := display.MaxResolution(); ok {
		// No physical capture device (software agent streaming the whole
		// desktop) — 1920x1080 above is just a last-resort guess. Query the
		// host's actual connected display instead, so a client that doesn't
		// explicitly pick a resolution defaults to the real maximum
		// (e.g. a 4K monitor) rather than always being capped at 1080p.
		width, height = dw, dh
	}

	moonlightHost := s.app.SunshineStreamHost()
	sunshinePort := s.app.SunshineAdminPort()
	color444Active, color444Available := s.app.Color444Status()
	s.ok(w, "video_info", map[string]any{
		"device":            devicePath,
		"width":             width,
		"height":            height,
		"fps":               fps,
		"mode":              "moonlight",
		"transport":         "moonlight",
		"encoding":          s.app.CurrentVideoCodec(),
		"streaming":         false,
		"capture_modes":     modes,
		"supported_modes":   videoCodecModes(s.app.SupportedVideoCodecs()),
		"available_devices": devices,
		"moonlight_host":    moonlightHost,
		"sunshine_port":     sunshinePort,
		// RustShine Pro's $8/mo color upgrade -- see
		// Application.Color444Status's doc comment. "active" reflects the
		// most recently started session (post-fallback truth, same as
		// "encoding"); "available" is whether the popup's 4:4:4 checkbox
		// should even be shown/enabled before the user has started
		// streaming at all.
		"color_444_active":    color444Active,
		"color_444_available": color444Available,
	})
}

// videoCodecModeInfo holds the display metadata for each Moonlight-supported
// codec; videoCodecModes filters this against what the host can actually
// encode (from Application.SupportedVideoCodecs) so the client never offers
// a codec the hardware can't produce — e.g. AV1 on GPUs without an AV1
// encoder, which Sunshine's own capability probe reports as unsupported.
var videoCodecModeInfo = []map[string]string{
	{"id": "h264", "name": "H.264", "description": "H.264 (AVC) Hardware Encoding", "transport": "rtp", "encoding": "h264"},
	{"id": "h265", "name": "H.265", "description": "H.265 (HEVC) Hardware Encoding", "transport": "rtp", "encoding": "h265"},
	{"id": "av1", "name": "AV1", "description": "AV1 Hardware Encoding", "transport": "rtp", "encoding": "av1"},
}

func videoCodecModes(supported []string) []map[string]string {
	supportedSet := make(map[string]bool, len(supported))
	for _, codec := range supported {
		supportedSet[codec] = true
	}
	modes := make([]map[string]string, 0, len(videoCodecModeInfo))
	for _, mode := range videoCodecModeInfo {
		if supportedSet[mode["encoding"]] {
			modes = append(modes, mode)
		}
	}
	if len(modes) == 0 {
		// Never return an empty list — h264 is Sunshine's protocol-mandated
		// baseline, so this only happens if the capability query itself
		// failed in a way that still returned a codec list without h264 in
		// it (shouldn't happen given SupportedVideoCodecs' own fallback, but
		// guard the client's UI from an empty codec picker regardless).
		modes = append(modes, videoCodecModeInfo[0])
	}
	return modes
}

func isLoopbackHost(host string) bool {

	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isLoopbackRequest reports whether r reached this server from the loopback
// interface itself, regardless of what interface the server is actually
// bound to (ListenHost defaults to 0.0.0.0, and the same handler is also
// served over the Tailscale/tsnet listener — see app.go). It additionally
// refuses any request carrying an Origin header, since that always means
// the caller is browser JS (native HTTP clients never set it) — a page
// open in the user's own browser could otherwise read a loopback-bound
// response just as easily as a LAN attacker could read a 0.0.0.0-bound one.
// Used to gate handlers whose response must never leave the machine that
// owns it, such as AuthQRLink (see sync.go), which hands back the master
// key — the sole credential for every other endpoint in this API.
func isLoopbackRequest(r *http.Request) bool {
	if r.Header.Get("Origin") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return isLoopbackHost(host)
}

func (s *Server) screen(w http.ResponseWriter, r *http.Request) {
	snap, err := s.app.Screen().Snapshot()
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "screen_failed", err)
		return
	}
	s.ok(w, "screen", snap)
}

func (s *Server) devicesLegacy(w http.ResponseWriter, r *http.Request) {
	info := s.app.DeviceInfo()
	items := make([]LegacyDeviceInfo, 0, len(info.Devices))
	for _, d := range info.Devices {
		items = append(items, LegacyDeviceInfo{Name: d.Device, Path: d.Server, Connected: true, Description: d.Type})
	}
	s.ok(w, "devices", items)
}

func (s *Server) leds(w http.ResponseWriter, r *http.Request) {
	s.ok(w, "leds", map[string]bool{"power": false, "hdd": false})
}

func (s *Server) button(w http.ResponseWriter, r *http.Request) {
	s.ok(w, "button", nil)
}

func ptrInt8(v *int8) int8 {
	if v == nil {
		return 0
	}
	return *v
}

func ptrUint8(v *uint8) uint8 {
	if v == nil {
		return 0
	}
	return *v
}

func ptrInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func decodeDeviceStart(r *http.Request) ([]DeviceRequest, error) {
	defer r.Body.Close()

	var body bytes.Buffer
	if _, err := body.ReadFrom(r.Body); err != nil {
		return nil, err
	}

	data := bytes.TrimSpace(body.Bytes())
	if len(data) == 0 {
		return nil, nil
	}

	var batch DeviceStartBatchRequest
	if err := json.Unmarshal(data, &batch); err == nil && batch.Devices != nil {
		return batch.Devices, nil
	}

	var devices []DeviceRequest
	if err := json.Unmarshal(data, &devices); err == nil {
		return devices, nil
	}

	return nil, json.Unmarshal(data, &batch)
}

func filterDevices(devices []DeviceRequest) []DeviceRequest {
	out := make([]DeviceRequest, 0, len(devices))
	for _, device := range devices {
		if device.Device == "rndis" {
			continue
		}
		out = append(out, device)
	}
	return out
}

func (s *Server) videoDevices(w http.ResponseWriter, r *http.Request) {
	devices := s.app.VideoDevices()
	s.ok(w, "video devices list", map[string]any{"devices": devices, "count": len(devices)})
}

// videoSetDevice pins Sunshine's capture to the monitor identified by
// "device" (a VideoDeviceInfo.Path, e.g. "drm:1" from /api/video/devices).
// An empty device clears the pin (Sunshine auto-picks).
func (s *Server) videoSetDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Device string `json:"device"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[api] video_set_device invalid_json: %v", err)
		s.fail(w, http.StatusBadRequest, "invalid_json", err)
		return
	}
	// VideoDevices() reports "drm:<index>" on Linux (correlated to Sunshine's
	// own KMS output index — see sunshine.MonitorIndexByName), "winid:<id>"
	// on Windows (Sunshine's own EDID+instance-derived device_id — see
	// sunshine.WindowsDisplayDevices), "display:<index>" as a fallback on
	// Windows/macOS/Linux-X11 before Sunshine has logged its real device list
	// this session (see screen_windows.go, screen_darwin.go,
	// screen_linux_x11.go), or "raw:<value>" for a backend whose OutputName
	// isn't a plain numeric index (e.g. rustshine's "cardPath|connector" —
	// see screen_linux_x11.go's rawOutputName). Sunshine's/rustshine's
	// output_name wants just the bare value in every case, so whichever
	// prefix is present must be stripped — "raw:" specifically must pass
	// its remainder through completely unchanged (no reinterpreting as a
	// numeric index), since it already IS the exact string SetOutputName
	// expects. Previously only "drm:" was handled, so on Windows the
	// literal string "display:0" (or "winid:{...}") was written to
	// output_name verbatim, which Sunshine can't parse and silently falls
	// back to auto-pick — monitor switching had no effect.
	outputName := req.Device
	for _, prefix := range []string{"drm:", "winid:", "display:", "raw:"} {
		if strings.HasPrefix(outputName, prefix) {
			outputName = strings.TrimPrefix(outputName, prefix)
			break
		}
	}
	log.Printf("[api] video_set_device device=%s", req.Device)
	if err := s.app.SetSunshineOutputName(outputName); err != nil {
		log.Printf("[api] video_set_device failed: %v", err)
		s.fail(w, http.StatusInternalServerError, "video_set_device_failed", err)
		return
	}
	s.ok(w, "video_device_set", nil)
}
