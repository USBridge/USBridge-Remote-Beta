package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

const DefaultMCPProxyPort = 8765

// mcpProxyTimeout is how long a forwarded MCP call is allowed to run,
// independent of AppConfig.APITimeout (15s default) -- that shorter budget
// is tuned to fail fast on a genuinely offline device for the app's own
// routine health/status polling, not for MCP tool calls the device can
// legitimately take longer to answer. Confirmed live: ui.parse tiles the
// screenshot server-side for text detection, and at 1920x1080 that's 6
// tiles (vs 720p's single untiled pass), ~20s end to end -- the shared 15s
// client cut this off every time with "device error: context deadline
// exceeded" even though the device had, in fact, finished and was about to
// answer.
const mcpProxyTimeout = 30 * time.Second

// MCPProxy runs a local HTTP server on 127.0.0.1 that forwards /api/mcp
// requests to the device with HMAC signatures, letting local AI tools reach
// MCP without needing to sign requests themselves.
type MCPProxy struct {
	mu     sync.Mutex
	srv    *http.Server
	client *USBClient
	port   int
}

// Start launches the proxy on 127.0.0.1:port. Calling Start while already
// running is a no-op.
func (p *MCPProxy) Start(port int, client *USBClient) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.srv != nil {
		return nil
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("mcp proxy: listen %s: %w", addr, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/mcp", p.handle)
	// Also accept /mcp for convenience (some AI clients use shorter path)
	mux.HandleFunc("/mcp", p.handle)

	p.client = client
	p.port = port
	p.srv = &http.Server{
		Handler:           mux,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := p.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("mcp proxy: %v", err)
		}
	}()
	log.Printf("✅ MCP proxy started on %s", addr)
	return nil
}

// UpdateClient rewires an already-running proxy to forward through client
// instead of whatever *USBClient it currently holds, without touching the
// listener. Call this whenever the device connection is replaced (a
// reconnect, or a new master key from the connection manager) so the proxy
// doesn't keep signing and forwarding requests with a stale key/host
// indefinitely -- Start itself only sets p.client on the very first launch
// (it no-ops while p.srv != nil, see Start's doc comment), and the app only
// calls Start again from a manual "enable" toggle, never on a reconnect, so
// nothing else pushes the new client into an already-running proxy.
// Confirmed live: after changing the paired device's key in the connection
// manager and reconnecting, the proxy kept HMAC-signing every forwarded MCP
// call with the OLD key/host until the user manually stopped and restarted
// it. client may be nil (e.g. on disconnect) so the proxy correctly reports
// "no device connected" instead of silently continuing to reach the
// previous device.
func (p *MCPProxy) UpdateClient(client *USBClient) {
	p.mu.Lock()
	p.client = client
	p.mu.Unlock()
}

// Stop shuts down the proxy.
func (p *MCPProxy) Stop() {
	p.mu.Lock()
	srv := p.srv
	p.srv = nil
	p.mu.Unlock()
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	log.Println("🛑 MCP proxy stopped")
}

// Running reports whether the proxy is active.
func (p *MCPProxy) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.srv != nil
}

// Port returns the port the proxy is listening on (0 if stopped).
func (p *MCPProxy) Port() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.srv == nil {
		return 0
	}
	return p.port
}

// jsonRPCID pulls the "id" field out of a JSON-RPC request body so an error
// response can echo it back (a client matches responses to in-flight calls
// by id; omitting it, or sending non-JSON, leaves the caller with an
// unparseable/unmatchable reply). Best-effort: a malformed or absent id
// yields nil, which is valid JSON-RPC for a request that never had one.
func jsonRPCID(body []byte) any {
	var envelope struct {
		ID any `json:"id"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil
	}
	return envelope.ID
}

// writeJSONRPCError replies with a spec-shaped JSON-RPC 2.0 error object
// instead of a bare text/plain body. Every other path in this handler used
// to call http.Error directly, which an MCP client (expecting JSON-RPC)
// can't parse -- a device-side or transport failure surfaced to it as an
// opaque "Unexpected identifier ..." rather than a message it could log or
// act on. httpStatus still carries the real outcome for anything reading
// the status code (proxies, curl, tests); the body is now always valid
// JSON regardless of what failed.
func writeJSONRPCError(w http.ResponseWriter, id any, code int, message string, httpStatus int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func (p *MCPProxy) handle(w http.ResponseWriter, r *http.Request) {
	// This proxy exists so a local AI tool (a native process on this same
	// machine) can reach the device's MCP endpoint without signing requests
	// itself — every request it forwards comes back out already HMAC-signed
	// with the paired device's master key (see PostRaw below). That makes an
	// Origin header a hard stop rather than something to just not echo back:
	// native HTTP clients never set it, only browser JS does, and any page
	// open in the user's browser could otherwise POST here and have its
	// request silently signed and relayed to the device — a CSRF-style
	// drive-by that doesn't need to know the master key at all, since this
	// proxy already holds it. Rejecting Origin outright (not just omitting
	// Access-Control-Allow-Origin) also blocks a same-origin fetch a page
	// might make to 127.0.0.1, which CORS alone wouldn't stop.
	if r.Header.Get("Origin") != "" {
		writeJSONRPCError(w, nil, -32600, "forbidden: browser requests are not allowed", http.StatusForbidden)
		return
	}

	p.mu.Lock()
	client := p.client
	p.mu.Unlock()
	if client == nil {
		writeJSONRPCError(w, nil, -32000, "no device connected", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		writeJSONRPCError(w, nil, -32600, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeJSONRPCError(w, nil, -32700, "read error", http.StatusBadRequest)
		return
	}
	body = bytes.TrimSpace(body)
	id := jsonRPCID(body)

	// Local ui.parse offload (see local_ui_intercept.go): when enabled in
	// settings, answer ui.parse ourselves via ONNX Runtime on this
	// machine's CPU or GPU instead of forwarding to the device's NPU. Every
	// other tool call falls through to the normal forward path below
	// unchanged.
	if localResp, handled, localErr := tryLocalUIParse(client, body); handled {
		if localErr != nil {
			writeJSONRPCError(w, id, -32000, fmt.Sprintf("local ui.parse error: %v", localErr), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(localResp)
		return
	}

	// PostRawWithTimeout signs the request with HMAC and forwards to device
	// /api/mcp, using mcpProxyTimeout rather than the app's general
	// (shorter) APITimeout -- see mcpProxyTimeout's doc comment for why.
	// PostRawWithTimeout now surfaces a non-200 device response (e.g. a
	// signature/auth failure, plain-text "Unauthorized: ...") as a Go error
	// instead of returning that text as if it were a successful JSON body
	// (see usb_client.go) -- so every failure here, transport or device-side
	// auth, ends up in this one JSON-RPC error path instead of leaking raw
	// text to a client that assumes JSON-RPC.
	resp, err := client.PostRawWithTimeout("/api/mcp", body, mcpProxyTimeout)
	if err != nil {
		writeJSONRPCError(w, id, -32000, fmt.Sprintf("device error: %v", err), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}
