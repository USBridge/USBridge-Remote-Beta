package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"usbridge-client/internal/localui"
)

// Local ui.parse interception: when enabled (see SetLocalUIParser), the MCP
// proxy answers the ui.parse tool call itself instead of forwarding it to
// the device -- running the same three-model pipeline (YOLOv8 icon
// detector + DBNet text detector + SVTR text recognizer) locally via ONNX
// Runtime on this machine's CPU or GPU (CoreML/DirectML/OpenVINO, see
// internal/localui/onnx.go's acceleratorEP) instead of the device's RK3566
// NPU. It still fetches the raw screenshot from the device (screen.get_image
// is cheap -- no OCR/YOLO runs on the device for that call, see that tool's
// description), only the expensive detection/recognition work moves local.
//
// This exists because ui.parse at 1920x1080 tiles DBNet into 6 native-
// resolution passes on the device's single-core NPU (~20s end to end, see
// mcpProxyTimeout's doc comment); the same models on this machine's CPU or
// an accelerator EP finish in well under 5s (see internal/localui's package
// doc comment and its benchmarked numbers).
//
// Every other MCP tool (including ui.parse's own tools/list entry) is
// untouched -- an MCP client sees identical behavior and JSON shape
// regardless of which backend answered, aside from the added informational
// "_backend" field localui.Result carries.

// minimal local mirrors of the device's MCP JSON-RPC envelope -- just
// enough fields to parse a tools/call request and re-serialize a
// tools/call response; the client repo doesn't share a Go module with the
// device repo, so these aren't imported.
type mcpEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type mcpContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

type mcpToolResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

// localUIState holds the optional local ui.parse offload, guarded by a
// mutex since Start/SetLocalUIParser can race with in-flight requests.
type localUIState struct {
	mu      sync.RWMutex
	parser  *localui.Parser
	enabled bool
}

var globalLocalUI localUIState

// SetLocalUIParser installs (or, with p==nil, removes) the local ui.parse
// backend. Called once at startup if the user's settings enable it (see
// models.AppConfig's LocalUIParse* fields) -- building the Parser is
// relatively expensive (loads 3 ONNX models), so it happens once, not
// per-request.
func SetLocalUIParser(p *localui.Parser) {
	globalLocalUI.mu.Lock()
	defer globalLocalUI.mu.Unlock()
	if globalLocalUI.parser != nil && globalLocalUI.parser != p {
		globalLocalUI.parser.Close()
	}
	globalLocalUI.parser = p
	globalLocalUI.enabled = p != nil
}

// LocalUIParserActive reports whether local ui.parse offload is currently
// wired up (for GUI status display).
func LocalUIParserActive() bool {
	globalLocalUI.mu.RLock()
	defer globalLocalUI.mu.RUnlock()
	return globalLocalUI.enabled
}

// GetLocalUIParser returns the currently installed local ui.parse backend,
// or nil if none is active (see SetLocalUIParser). Exposed so other
// features can reuse the already-loaded ONNX models instead of duplicating
// the "optional accelerator, load once at startup" lifecycle handled here
// -- e.g. the AI Vision live video overlay (internal/service/ai_vision.go),
// which runs the exact same detector against live frames instead of a
// screenshot fetched on demand.
func GetLocalUIParser() *localui.Parser {
	globalLocalUI.mu.RLock()
	defer globalLocalUI.mu.RUnlock()
	return globalLocalUI.parser
}

// tryLocalUIParse intercepts a tools/call request for "ui.parse" and
// answers it locally if enabled. Returns (responseBody, true, nil) on a
// successful local answer, (nil, false, nil) if this request isn't a
// ui.parse call or local offload isn't enabled (caller should forward to
// the device as usual), or (nil, true, err) if it WAS a ui.parse call
// meant to be answered locally but local processing failed (caller should
// report the error rather than silently falling back, so a broken local
// setup doesn't disguise itself as an empty ui.parse result).
func tryLocalUIParse(client *USBClient, reqBody []byte) (respBody []byte, handled bool, err error) {
	globalLocalUI.mu.RLock()
	parser := globalLocalUI.parser
	enabled := globalLocalUI.enabled
	globalLocalUI.mu.RUnlock()
	if !enabled || parser == nil {
		return nil, false, nil
	}

	var env mcpEnvelope
	if err := json.Unmarshal(reqBody, &env); err != nil {
		return nil, false, nil // not our concern, let the normal path report the parse error
	}
	if env.Method != "tools/call" {
		return nil, false, nil
	}
	var call mcpToolCallParams
	if err := json.Unmarshal(env.Params, &call); err != nil {
		return nil, false, nil
	}
	if call.Name != "ui.parse" {
		return nil, false, nil
	}

	imgBytes, err := screenImageForLocalParse(client)
	if err != nil {
		return nil, true, fmt.Errorf("local ui.parse: fetch screen.get_image from device: %w", err)
	}

	markedPNG, result, err := parser.Parse(imgBytes)
	if err != nil {
		return nil, true, fmt.Errorf("local ui.parse: %w", err)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return nil, true, fmt.Errorf("local ui.parse: marshal result: %w", err)
	}

	toolResult := mcpToolResult{Content: []mcpContent{
		{Type: "image", MimeType: "image/png", Data: base64.StdEncoding.EncodeToString(markedPNG)},
		{Type: "text", MimeType: "application/json", Text: string(payload)},
	}}
	resultJSON, err := json.Marshal(toolResult)
	if err != nil {
		return nil, true, fmt.Errorf("local ui.parse: marshal tool result: %w", err)
	}

	out := mcpEnvelope{JSONRPC: "2.0", ID: env.ID, Result: resultJSON}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, true, fmt.Errorf("local ui.parse: marshal response: %w", err)
	}
	return body, true, nil
}

// liveFrameWaitTimeout bounds how long screenImageForLocalParse waits for
// the video decode path to hand over a frame (see live_frame.go) before
// giving up and falling back to a device round-trip. Long enough that a
// live session (frames arriving every 8-16ms at 60-120fps) always wins the
// race; short enough that calling ui.parse with no video session open
// (a perfectly normal, headless MCP-agent use case) doesn't add a
// noticeable stall before it falls back to fetchScreenImage.
const liveFrameWaitTimeout = 300 * time.Millisecond

// screenImageForLocalParse gets the screenshot local ui.parse decodes,
// preferring a frame the video decode path is already producing (no extra
// network round-trip or device-side capture) over fetchScreenImage's full
// device round-trip. Only ever skips the device entirely when a video
// session is actively streaming AND the operator is looking at it in the
// GUI right now -- with no video session open (the common case for a
// headless/background MCP agent), RequestLiveFrame just times out after
// liveFrameWaitTimeout and this falls back to fetchScreenImage exactly as
// before this existed.
func screenImageForLocalParse(client *USBClient) ([]byte, error) {
	if png, ok := RequestLiveFrame(liveFrameWaitTimeout); ok {
		return png, nil
	}
	return fetchScreenImage(client)
}

// fetchScreenImage calls the device's screen.get_image MCP tool and
// extracts the raw PNG bytes -- this is the one call the local path still
// makes to the device (cheap: no OCR/YOLO runs there for it).
func fetchScreenImage(client *USBClient) ([]byte, error) {
	req := mcpEnvelope{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"local-ui-parse"`),
		Method:  "tools/call",
	}
	params := mcpToolCallParams{Name: "screen.get_image"}
	paramsJSON, _ := json.Marshal(params)
	req.Params = paramsJSON
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	respBody, err := client.PostRawWithTimeout("/api/mcp", reqBody, mcpProxyTimeout)
	if err != nil {
		return nil, err
	}
	var resp mcpEnvelope
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if len(resp.Error) > 0 {
		return nil, fmt.Errorf("device error: %s", string(resp.Error))
	}
	var result mcpToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("decode tool result: %w", err)
	}
	for _, c := range result.Content {
		if c.Type == "image" && c.Data != "" {
			return base64.StdEncoding.DecodeString(c.Data)
		}
	}
	return nil, fmt.Errorf("screen.get_image returned no image content")
}
