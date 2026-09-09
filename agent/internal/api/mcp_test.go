package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"usbridge_agent/internal/clipboard"
)

// mcpTestInput records every call so tests can assert dispatch, and lets a
// specific action be made to fail to exercise the isError:true path.
type mcpTestInput struct {
	failAction string
	lastKey    uint8
	lastCombo  [2]uint8
	lastText   string
	lastMouse  string
}

func (i *mcpTestInput) Key(k uint8) error {
	i.lastKey = k
	if i.failAction == "key" {
		return errTestInput
	}
	return nil
}
func (i *mcpTestInput) Combo(mod, k uint8) error {
	i.lastCombo = [2]uint8{mod, k}
	return nil
}
func (i *mcpTestInput) Text(t string) error {
	i.lastText = t
	return nil
}
func (i *mcpTestInput) MouseMove(int8, int8) error { i.lastMouse = "move"; return nil }
func (i *mcpTestInput) MouseClick(uint8) error     { i.lastMouse = "click"; return nil }
func (i *mcpTestInput) MouseScroll(int8) error     { i.lastMouse = "scroll"; return nil }
func (i *mcpTestInput) MouseAction(uint8, int8, int8, int8) error {
	i.lastMouse = "action"
	return nil
}
func (i *mcpTestInput) AbsoluteEvent(uint8, uint16, uint16, int8) error {
	i.lastMouse = "absolute"
	return nil
}

var errTestInput = &testError{"simulated input failure"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

type mcpTestScreen struct{}

func (mcpTestScreen) Snapshot() (*ScreenSnapshot, error) {
	return &ScreenSnapshot{Format: "png-base64", Width: 100, Height: 50, ImageBase64: "Zm9v"}, nil
}

// mcpTestApp implements Application with just enough behavior to exercise
// the MCP handler in isolation, independent of clipboard_test.go's stubApp
// (whose Screen() intentionally returns a nil snapshot, which would panic
// mcpScreenGetImage's field access).
type mcpTestApp struct {
	input *mcpTestInput
}

func (a *mcpTestApp) Status() SystemStatus { return SystemStatus{} }
func (a *mcpTestApp) DeviceInfo() DeviceInfoResponse {
	return DeviceInfoResponse{AgentOS: "darwin", Count: 0}
}
func (a *mcpTestApp) ReplaceDevices([]DeviceRequest) error { return nil }
func (a *mcpTestApp) ClearDevices() error                  { return nil }
func (a *mcpTestApp) Input() interface {
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
func (a *mcpTestApp) Screen() interface {
	Snapshot() (*ScreenSnapshot, error)
} {
	return mcpTestScreen{}
}
func (a *mcpTestApp) VideoDevices() []VideoDeviceInfo       { return nil }
func (a *mcpTestApp) SunshineOutputName() string            { return "" }
func (a *mcpTestApp) SetSunshineOutputName(string) error    { return nil }
func (a *mcpTestApp) SunshineStreamHost() string            { return "" }
func (a *mcpTestApp) SunshineAdminPort() int                { return 0 }
func (a *mcpTestApp) SubmitMoonlightPIN(string) error       { return nil }
func (a *mcpTestApp) CurrentVideoCodec() string             { return "" }
func (a *mcpTestApp) SupportedVideoCodecs() []string        { return []string{"h264"} }
func (a *mcpTestApp) Color444Status() (bool, bool)          { return false, false }
func (a *mcpTestApp) AudioSinks() ([]AudioSink, error)      { return nil, nil }
func (a *mcpTestApp) CurrentAudioSink() (string, error)     { return "", nil }
func (a *mcpTestApp) SetAudioSink(string) error             { return nil }
func (a *mcpTestApp) TailscaleStatus() *TailscaleStatusInfo { return nil }
func (a *mcpTestApp) RegisterTailscale(context.Context, string, string) (*TailscaleStatusInfo, error) {
	return nil, nil
}
func (a *mcpTestApp) Clipboard() *clipboard.Manager { return nil }
func (a *mcpTestApp) ClipboardMaxBytes() int64      { return 0 }

const mcpTestSecret = "test-mcp-master-key"

func mcpTestServer() (*Server, *mcpTestInput) {
	input := &mcpTestInput{}
	srv := NewServerWithAuth(&mcpTestApp{input: input}, []byte(mcpTestSecret), 0)
	return srv, input
}

func mcpTestRequest(t *testing.T, srv *Server, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := CalculateHMAC(http.MethodPost, "/api/mcp", ts, string(raw), []byte(mcpTestSecret))

	req := httptest.NewRequest(http.MethodPost, "/api/mcp", bytes.NewReader(raw))
	req.Header.Set("X-Auth-Signature", sig)
	req.Header.Set("X-Auth-Timestamp", ts)
	req.RemoteAddr = "127.0.0.1:54321"

	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec
}

func decodeRPC(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
	}
	return out
}

func TestMCP_ToolsList_ExcludesHardwareOnlyTools(t *testing.T) {
	srv, _ := mcpTestServer()
	rec := mcpTestRequest(t, srv, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	out := decodeRPC(t, rec)
	result := out["result"].(map[string]any)
	tools := result["tools"].([]any)

	names := map[string]bool{}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		names[tool["name"].(string)] = true
	}
	for _, want := range []string{"screen.get_image", "keyboard.send", "mouse.action", "device.info"} {
		if !names[want] {
			t.Errorf("tools/list missing %q", want)
		}
	}
	for _, mustNotHave := range []string{"mountdrive.start", "scripts.run", "pcpanel.button", "media.insert"} {
		if names[mustNotHave] {
			t.Errorf("tools/list should not advertise hardware-only tool %q", mustNotHave)
		}
	}
}

func TestMCP_ToolsCall_KeyboardSend(t *testing.T) {
	srv, input := mcpTestServer()
	rec := mcpTestRequest(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "keyboard.send", "arguments": map[string]any{"action": "text", "text": "hello"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if input.lastText != "hello" {
		t.Fatalf("Input().Text not called with expected text, got %q", input.lastText)
	}
	out := decodeRPC(t, rec)
	result := out["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("unexpected isError:true, body: %s", rec.Body.String())
	}
}

func TestMCP_ToolsCall_KeyboardSend_MissingField(t *testing.T) {
	srv, _ := mcpTestServer()
	rec := mcpTestRequest(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "keyboard.send", "arguments": map[string]any{"action": "key"}},
	})
	out := decodeRPC(t, rec)
	result := out["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("expected isError:true for missing key_code, body: %s", rec.Body.String())
	}
}

func TestMCP_ToolsCall_InputFailureSurfacesAsToolError(t *testing.T) {
	input := &mcpTestInput{failAction: "key"}
	srv := NewServerWithAuth(&mcpTestApp{input: input}, []byte(mcpTestSecret), 0)
	kc := 40
	rec := mcpTestRequest(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "keyboard.send", "arguments": map[string]any{"action": "key", "key_code": kc}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("OS-level tool failure must still be HTTP 200 (JSON-RPC result, not transport error): got %d", rec.Code)
	}
	out := decodeRPC(t, rec)
	result := out["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("expected isError:true, body: %s", rec.Body.String())
	}
}

func TestMCP_ToolsCall_ScreenGetImage(t *testing.T) {
	srv, _ := mcpTestServer()
	rec := mcpTestRequest(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 5, "method": "tools/call",
		"params": map[string]any{"name": "screen.get_image", "arguments": map[string]any{}},
	})
	out := decodeRPC(t, rec)
	result := out["result"].(map[string]any)
	content := result["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("expected 2 content blocks (image + dims), got %d: %v", len(content), content)
	}
	imgBlock := content[0].(map[string]any)
	if imgBlock["type"] != "image" || imgBlock["data"] != "Zm9v" || imgBlock["mimeType"] != "image/png" {
		t.Fatalf("unexpected image block: %v", imgBlock)
	}
}

func TestMCP_ResourcesRead_Instructions(t *testing.T) {
	srv, _ := mcpTestServer()
	rec := mcpTestRequest(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 6, "method": "resources/read",
		"params": map[string]any{"uri": "usbridge://instructions"},
	})
	out := decodeRPC(t, rec)
	result := out["result"].(map[string]any)
	contents := result["contents"].([]any)
	first := contents[0].(map[string]any)
	if first["mimeType"] != "text/markdown" {
		t.Fatalf("unexpected mimeType: %v", first["mimeType"])
	}
}

func TestMCP_UnknownMethod(t *testing.T) {
	srv, _ := mcpTestServer()
	rec := mcpTestRequest(t, srv, map[string]any{"jsonrpc": "2.0", "id": 7, "method": "bogus/method"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body: %s", rec.Code, rec.Body.String())
	}
}

func TestMCP_RejectsUnsigned(t *testing.T) {
	srv, _ := mcpTestServer()
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", bytes.NewReader(raw))
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("expected an unsigned request to be rejected, got 200: %s", rec.Body.String())
	}
}
