package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

// mcp implements the Agent's half of the Model Context Protocol endpoint:
// JSON-RPC 2.0 over a single POST /api/mcp, same wire shape and auth
// (HMAC-signed, via sec.LimitPolling in Routes) as the hardware KVM's own
// MCP server. The client's MCP proxy (client/internal/api/mcp_proxy.go)
// just forwards whatever it receives to this device's /api/mcp -- it has no
// idea which kind of device is on the other end, so this handler exists to
// give a software Agent connection something to forward *to*.
//
// The tool catalog here is deliberately smaller than the hardware KVM's
// (see agent/docs/README.md's capability table): no mountdrive/media/rndis
// (there's no USB-HID gadget to arm -- keyboard/mouse injection works
// directly via SendInput/CGEvent/uinput, no "enable" step needed first), no
// scripts.* (no on-device Starlark engine or SD/eMMC storage), no
// pcpanel.* (no physical front panel). What's left -- screen capture,
// keyboard/mouse injection, device info -- covers what an Agent actually
// can do: drive the OS UI and see the result, the same as a human at the
// keyboard.
func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMCPError(w, nil, -32600, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req mcpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMCPError(w, nil, -32700, "parse error: "+err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("[api] mcp method=%s id=%v", req.Method, req.ID)

	switch req.Method {
	case "initialize":
		s.mcpOK(w, req.ID, mcpInitializeResult{
			ProtocolVersion: "2024-11-05",
			Capabilities: mcpCapabilities{
				Tools:     map[string]any{},
				Resources: map[string]any{},
			},
			ServerInfo: mcpServerInfo{Name: "usbridge-agent", Version: agentMCPVersion},
		})
	case "tools/list":
		s.mcpOK(w, req.ID, map[string]any{"tools": mcpToolCatalog})
	case "tools/call":
		s.mcpToolsCall(w, req)
	case "resources/list":
		s.mcpOK(w, req.ID, map[string]any{"resources": mcpResourceCatalog})
	case "resources/read":
		s.mcpResourcesRead(w, req)
	default:
		writeMCPError(w, req.ID, -32601, "method not found: "+req.Method, http.StatusNotFound)
	}
}

const agentMCPVersion = "1.0"

// ─── JSON-RPC envelope ──────────────────────────────────────────────────────

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (s *Server) mcpOK(w http.ResponseWriter, id any, result any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func writeMCPError(w http.ResponseWriter, id any, code int, message string, httpStatus int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
}

type mcpInitializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    mcpCapabilities `json:"capabilities"`
	ServerInfo      mcpServerInfo   `json:"serverInfo"`
}

type mcpCapabilities struct {
	Tools     map[string]any `json:"tools"`
	Resources map[string]any `json:"resources"`
}

type mcpServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ─── tools/list ─────────────────────────────────────────────────────────────

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

var mcpToolCatalog = []mcpTool{
	{
		Name:        "screen.get_image",
		Description: "Current screen as a lossless PNG screenshot (base64-encoded MCP image content), plus a {\"width\":...,\"height\":...} text block.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	},
	{
		Name:        "keyboard.send",
		Description: "Send a keyboard event. action: \"key\" (key_code, a USB HID code), \"combo\" (modifiers + key_code), or \"text\" (text, typed directly). No mountdrive step needed -- the Agent injects input directly via the host OS.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":    map[string]any{"type": "string", "enum": []string{"key", "combo", "text"}},
				"key_code":  map[string]any{"type": "integer"},
				"modifiers": map[string]any{"type": "integer"},
				"text":      map[string]any{"type": "string"},
			},
			"required": []string{"action"},
		},
	},
	{
		Name:        "mouse.action",
		Description: "Send a mouse event. action: \"move\" (dx,dy relative), \"click\" (button), \"scroll\" (scroll), \"action\" (button+dx+dy+scroll combined), or \"absolute\" (x,y absolute position + button_state).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":       map[string]any{"type": "string", "enum": []string{"move", "click", "scroll", "action", "absolute"}},
				"dx":           map[string]any{"type": "integer"},
				"dy":           map[string]any{"type": "integer"},
				"button":       map[string]any{"type": "integer"},
				"scroll":       map[string]any{"type": "integer"},
				"x":            map[string]any{"type": "integer"},
				"y":            map[string]any{"type": "integer"},
				"button_state": map[string]any{"type": "integer"},
			},
			"required": []string{"action"},
		},
	},
	{
		Name:        "device.info",
		Description: "Current device/gadget info (always empty on a software Agent -- there's no USB gadget) plus the Agent's reported OS.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	},
}

// ─── tools/call ─────────────────────────────────────────────────────────────

type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) mcpToolsCall(w http.ResponseWriter, req mcpRequest) {
	var params mcpToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeMCPError(w, req.ID, -32602, "invalid params: "+err.Error(), http.StatusBadRequest)
		return
	}

	var (
		content []map[string]any
		err     error
	)
	switch params.Name {
	case "screen.get_image":
		content, err = s.mcpScreenGetImage()
	case "keyboard.send":
		content, err = s.mcpKeyboardSend(params.Arguments)
	case "mouse.action":
		content, err = s.mcpMouseAction(params.Arguments)
	case "device.info":
		content, err = s.mcpDeviceInfo()
	default:
		writeMCPError(w, req.ID, -32602, "unknown tool: "+params.Name, http.StatusBadRequest)
		return
	}

	if err != nil {
		// A tool failure (bad args, OS-level input/capture error) is a
		// normal JSON-RPC *result* with isError:true, not a transport-level
		// error -- that's the MCP spec's own convention, and it's how the
		// hardware KVM's own tools/call responds too, so an agent talking
		// to either kind of device sees the same shape either way.
		s.mcpOK(w, req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
			"isError": true,
		})
		return
	}
	s.mcpOK(w, req.ID, map[string]any{"content": content})
}

func (s *Server) mcpScreenGetImage() ([]map[string]any, error) {
	snap, err := s.app.Screen().Snapshot()
	if err != nil {
		return nil, fmt.Errorf("screen capture failed: %w", err)
	}
	return []map[string]any{
		{"type": "image", "data": snap.ImageBase64, "mimeType": "image/png"},
		{"type": "text", "text": fmt.Sprintf(`{"width":%d,"height":%d}`, snap.Width, snap.Height)},
	}, nil
}

func (s *Server) mcpKeyboardSend(args json.RawMessage) ([]map[string]any, error) {
	var req KeyboardRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if badReq, err := s.applyKeyboard(req); badReq != "" {
		return nil, fmt.Errorf("%s", badReq)
	} else if err != nil {
		return nil, err
	}
	return []map[string]any{{"type": "text", "text": "ok"}}, nil
}

func (s *Server) mcpMouseAction(args json.RawMessage) ([]map[string]any, error) {
	var req MouseRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if err := s.applyMouse(req); err != nil {
		return nil, err
	}
	return []map[string]any{{"type": "text", "text": "ok"}}, nil
}

func (s *Server) mcpDeviceInfo() ([]map[string]any, error) {
	info := s.app.DeviceInfo()
	b, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}
	return []map[string]any{{"type": "text", "text": string(b)}}, nil
}

// ─── resources ──────────────────────────────────────────────────────────────

type mcpResource struct {
	URI      string `json:"uri"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
}

var mcpResourceCatalog = []mcpResource{
	{URI: "usbridge://instructions", Name: "Agent MCP usage notes", MimeType: "text/markdown"},
}

const agentMCPInstructions = `# USBridge Agent — MCP quick reference

This is a **software Agent**, not the hardware USBridge-KVM — it has no USB-HID
gadget, SD/eMMC storage, or Starlark scripting engine, so ` + "`mountdrive.*`" + `,
` + "`media.*`" + `, ` + "`rndis.*`" + `, ` + "`scripts.*`" + ` and ` + "`pcpanel.*`" + ` aren't available here.

Available tools:
- ` + "`screen.get_image`" + ` — a screenshot of the Agent's desktop (lossless PNG).
- ` + "`keyboard.send`" + ` / ` + "`mouse.action`" + ` — inject keyboard/mouse input directly into the
  host OS. No enable/arm step needed first (unlike the hardware KVM's
  ` + "`mountdrive.start`" + `) — the Agent has direct OS-level input access as soon as
  it's running.
- ` + "`device.info`" + ` — the Agent's reported OS/host info.

There is no OCR/text-mode screen reader here (that's specific to the
hardware KVM's pre-OS BIOS-in-Terminal pipeline) — use ` + "`screen.get_image`" + ` and
read the pixels directly.

Recommended loop: ` + "`screen.get_image`" + ` → decide the next click/keystroke →
` + "`mouse.action`" + `/` + "`keyboard.send`" + ` → wait ~150-300ms for the UI to react →
` + "`screen.get_image`" + ` again.
`

func (s *Server) mcpResourcesRead(w http.ResponseWriter, req mcpRequest) {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeMCPError(w, req.ID, -32602, "invalid params: "+err.Error(), http.StatusBadRequest)
		return
	}

	switch params.URI {
	case "usbridge://instructions":
		s.mcpOK(w, req.ID, map[string]any{
			"contents": []map[string]any{
				{"uri": params.URI, "mimeType": "text/markdown", "text": agentMCPInstructions},
			},
		})
	default:
		writeMCPError(w, req.ID, -32602, "unknown resource: "+params.URI, http.StatusNotFound)
	}
}
