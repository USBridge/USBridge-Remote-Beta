package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"usbridge_agent/internal/clipboard"
)

// fakeBackend is an in-memory clipboard.Backend so this test never touches
// the real OS clipboard.
type fakeBackend struct {
	mu      sync.Mutex
	content clipboard.Content
	stamp   int
}

func (f *fakeBackend) ChangeStamp() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strconv.Itoa(f.stamp), nil
}

func (f *fakeBackend) Read() (clipboard.Content, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.content, !f.content.Empty(), nil
}

func (f *fakeBackend) Write(c clipboard.Content) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.content = c
	f.stamp++
	return nil
}

func (f *fakeBackend) simulateLocalChange(c clipboard.Content) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.content = c
	f.stamp++
}

func (f *fakeBackend) snapshot() clipboard.Content {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.content
}

// fakeFileEnumeratorBackend adds a canned FileEnumerator response on top of
// fakeBackend, so tests can drive Manager's fast pre-announce path (Run only
// calls EnumerateFiles when the concrete backend implements it — a plain
// *fakeBackend doesn't).
type fakeFileEnumeratorBackend struct {
	fakeBackend
	files []clipboard.FileSummary
}

func (f *fakeFileEnumeratorBackend) EnumerateFiles() ([]clipboard.FileSummary, bool) {
	if len(f.files) == 0 {
		return nil, false
	}
	return f.files, true
}

type stubInput struct{}

func (stubInput) Key(uint8) error                                 { return nil }
func (stubInput) Combo(uint8, uint8) error                        { return nil }
func (stubInput) Text(string) error                               { return nil }
func (stubInput) MouseMove(int8, int8) error                      { return nil }
func (stubInput) MouseClick(uint8) error                          { return nil }
func (stubInput) MouseScroll(int8) error                          { return nil }
func (stubInput) MouseAction(uint8, int8, int8, int8) error       { return nil }
func (stubInput) AbsoluteEvent(uint8, uint16, uint16, int8) error { return nil }

type stubScreen struct{}

func (stubScreen) Snapshot() (*ScreenSnapshot, error) { return nil, nil }

// stubApp implements Application with just enough behavior to exercise the
// clipboard transport in isolation.
type stubApp struct {
	clip *clipboard.Manager
}

func (s *stubApp) Status() SystemStatus                 { return SystemStatus{} }
func (s *stubApp) DeviceInfo() DeviceInfoResponse       { return DeviceInfoResponse{} }
func (s *stubApp) ReplaceDevices([]DeviceRequest) error { return nil }
func (s *stubApp) ClearDevices() error                  { return nil }
func (s *stubApp) Input() interface {
	Key(uint8) error
	Combo(uint8, uint8) error
	Text(string) error
	MouseMove(int8, int8) error
	MouseClick(uint8) error
	MouseScroll(int8) error
	MouseAction(uint8, int8, int8, int8) error
	AbsoluteEvent(uint8, uint16, uint16, int8) error
} {
	return stubInput{}
}
func (s *stubApp) Screen() interface {
	Snapshot() (*ScreenSnapshot, error)
} {
	return stubScreen{}
}
func (s *stubApp) VideoDevices() []VideoDeviceInfo       { return nil }
func (s *stubApp) SunshineOutputName() string            { return "" }
func (s *stubApp) SetSunshineOutputName(string) error    { return nil }
func (s *stubApp) SunshineStreamHost() string            { return "" }
func (s *stubApp) SunshineAdminPort() int                { return 0 }
func (s *stubApp) SubmitMoonlightPIN(string) error       { return nil }
func (s *stubApp) CurrentVideoCodec() string             { return "" }
func (s *stubApp) SupportedVideoCodecs() []string        { return []string{"h264"} }
func (s *stubApp) Color444Status() (bool, bool)          { return false, false }
func (s *stubApp) AudioSinks() ([]AudioSink, error)      { return nil, nil }
func (s *stubApp) CurrentAudioSink() (string, error)     { return "", nil }
func (s *stubApp) SetAudioSink(string) error             { return nil }
func (s *stubApp) TailscaleStatus() *TailscaleStatusInfo { return nil }
func (s *stubApp) RegisterTailscale(context.Context, string, string) (*TailscaleStatusInfo, error) {
	return nil, nil
}
func (s *stubApp) Clipboard() *clipboard.Manager { return s.clip }
func (s *stubApp) ClipboardMaxBytes() int64      { return 0 }
func (s *stubApp) QRLink() (string, string) {
	return "usbridge://connect?master_key=stub-master-key", "stub-master-key"
}

// testClipboardClient plays the "other side" of the protocol using the same
// signing/framing rules as the real client's ClipboardSync, without
// depending on the client module (separate Go module, can't import it here).
type testClipboardClient struct {
	t       *testing.T
	baseURL string
	secret  []byte
	conn    *websocket.Conn
}

func dialTestClipboardClient(t *testing.T, baseURL string, secret []byte) *testClipboardClient {
	t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := CalculateHMAC("GET", "/api/clipboard/ws", ts, "", secret)
	header := http.Header{}
	header.Set("X-Auth-Signature", sig)
	header.Set("X-Auth-Timestamp", ts)

	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/api/clipboard/ws"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial failed: %v (status %d)", err, resp.StatusCode)
		}
		t.Fatalf("dial failed: %v", err)
	}
	return &testClipboardClient{t: t, baseURL: baseURL, secret: secret, conn: conn}
}

func (c *testClipboardClient) close() { c.conn.Close() }

func (c *testClipboardClient) send(event ClipboardEvent) {
	c.t.Helper()
	if err := c.conn.WriteJSON(event); err != nil {
		c.t.Fatalf("write event failed: %v", err)
	}
}

func (c *testClipboardClient) recv(timeout time.Duration) ClipboardEvent {
	c.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(timeout))
	var event ClipboardEvent
	if err := c.conn.ReadJSON(&event); err != nil {
		c.t.Fatalf("read event failed: %v", err)
	}
	return event
}

func (c *testClipboardClient) uploadBlob(id string, data []byte) {
	c.t.Helper()
	path := "/api/clipboard/blob/" + id
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := CalculateHMAC("PUT", path, ts, "", c.secret)
	req, err := http.NewRequest(http.MethodPut, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		c.t.Fatalf("build PUT request failed: %v", err)
	}
	req.Header.Set("X-Auth-Signature", sig)
	req.Header.Set("X-Auth-Timestamp", ts)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("upload blob failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		c.t.Fatalf("upload blob: unexpected status %d", resp.StatusCode)
	}
}

func (c *testClipboardClient) downloadBlob(id string) []byte {
	c.t.Helper()
	path := "/api/clipboard/blob/" + id
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := CalculateHMAC("GET", path, ts, "", c.secret)
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		c.t.Fatalf("build GET request failed: %v", err)
	}
	req.Header.Set("X-Auth-Signature", sig)
	req.Header.Set("X-Auth-Timestamp", ts)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("download blob failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("download blob: unexpected status %d", resp.StatusCode)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	return buf.Bytes()
}

func newTestClipboardServer(t *testing.T) (*httptest.Server, *fakeBackend, []byte) {
	t.Helper()
	secret := []byte("test-master-key-clipboard")
	backend := &fakeBackend{}
	mgr := clipboard.NewManager(backend, 10*1024*1024)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go mgr.Run(ctx)

	srv := NewServerWithAuth(&stubApp{clip: mgr}, secret, 0)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return ts, backend, secret
}

func waitFor(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func TestClipboardWS_AgentToClient_Text(t *testing.T) {
	ts, backend, secret := newTestClipboardServer(t)
	client := dialTestClipboardClient(t, ts.URL, secret)
	defer client.close()

	backend.simulateLocalChange(clipboard.Content{Kind: clipboard.KindText, Text: "from-agent"})

	event := client.recv(3 * time.Second)
	if event.Kind != string(clipboard.KindText) || event.Text != "from-agent" {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestClipboardWS_AgentToClient_FilePendingBeforeReal(t *testing.T) {
	secret := []byte("test-master-key-clipboard")
	backend := &fakeFileEnumeratorBackend{}
	mgr := clipboard.NewManager(backend, 10*1024*1024)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go mgr.Run(ctx)

	srv := NewServerWithAuth(&stubApp{clip: mgr}, secret, 0)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	client := dialTestClipboardClient(t, ts.URL, secret)
	defer client.close()

	backend.files = []clipboard.FileSummary{
		{Name: "a.jpg", Size: 1_000_000},
		{Name: "b.jpg", Size: 2_000_000},
	}
	backend.simulateLocalChange(clipboard.Content{
		Kind:  clipboard.KindFile,
		Files: []clipboard.FileItem{{Name: "a.jpg", Data: []byte("aaa")}, {Name: "b.jpg", Data: []byte("bbb")}},
	})

	pending := client.recv(3 * time.Second)
	if !pending.Pending || pending.Kind != string(clipboard.KindFile) {
		t.Fatalf("expected a pending pre-announce first, got: %+v", pending)
	}
	if pending.FileCount != 2 || pending.Size != 3_000_000 {
		t.Fatalf("unexpected pending summary: count=%d size=%d", pending.FileCount, pending.Size)
	}
	if pending.BlobID != "" || pending.Hash != "" {
		t.Fatalf("pending event must not carry a fetchable blob/hash: %+v", pending)
	}

	real := client.recv(3 * time.Second)
	if real.Pending || real.Kind != string(clipboard.KindFile) || real.BlobID == "" {
		t.Fatalf("expected the real event to follow with a usable BlobID, got: %+v", real)
	}
}

func TestClipboardWS_ClientToAgent_Text(t *testing.T) {
	ts, backend, secret := newTestClipboardServer(t)
	client := dialTestClipboardClient(t, ts.URL, secret)
	defer client.close()

	client.send(ClipboardEvent{Kind: string(clipboard.KindText), Text: "from-client", Hash: "irrelevant"})

	waitFor(t, 3*time.Second, func() bool {
		c := backend.snapshot()
		return c.Kind == clipboard.KindText && c.Text == "from-client"
	})
}

func TestClipboardWS_ClientToAgent_Image(t *testing.T) {
	ts, backend, secret := newTestClipboardServer(t)
	client := dialTestClipboardClient(t, ts.URL, secret)
	defer client.close()

	imgData := []byte{0x89, 0x50, 0x4e, 0x47, 0x01, 0x02, 0x03}
	client.uploadBlob("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", imgData)
	client.send(ClipboardEvent{Kind: string(clipboard.KindImage), BlobID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", MimeType: "image/png"})

	waitFor(t, 3*time.Second, func() bool {
		c := backend.snapshot()
		return c.Kind == clipboard.KindImage && bytes.Equal(c.Image, imgData)
	})
}

func TestClipboardWS_AgentToClient_Image(t *testing.T) {
	ts, backend, secret := newTestClipboardServer(t)
	client := dialTestClipboardClient(t, ts.URL, secret)
	defer client.close()

	imgData := []byte{0x11, 0x22, 0x33, 0x44, 0x55}
	backend.simulateLocalChange(clipboard.Content{Kind: clipboard.KindImage, Image: imgData})

	event := client.recv(3 * time.Second)
	if event.Kind != string(clipboard.KindImage) || event.BlobID == "" {
		t.Fatalf("unexpected event: %+v", event)
	}
	got := client.downloadBlob(event.BlobID)
	if !bytes.Equal(got, imgData) {
		t.Fatalf("blob mismatch: got %v want %v", got, imgData)
	}
}

func TestClipboardWS_ClientToAgent_MultiFile(t *testing.T) {
	ts, backend, secret := newTestClipboardServer(t)
	client := dialTestClipboardClient(t, ts.URL, secret)
	defer client.close()

	files := []clipboard.FileItem{
		{Name: "a.txt", Data: []byte("aaa")},
		{Name: "b.txt", Data: []byte("bbbbb")},
	}
	encoded := clipboard.EncodeFiles(files)
	client.uploadBlob("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", encoded)
	client.send(ClipboardEvent{Kind: string(clipboard.KindFile), BlobID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", FileName: "a.txt"})

	waitFor(t, 3*time.Second, func() bool {
		c := backend.snapshot()
		return c.Kind == clipboard.KindFile && len(c.Files) == 2
	})
	c := backend.snapshot()
	if c.Files[0].Name != "a.txt" || string(c.Files[0].Data) != "aaa" {
		t.Fatalf("unexpected file[0]: %+v", c.Files[0])
	}
	if c.Files[1].Name != "b.txt" || string(c.Files[1].Data) != "bbbbb" {
		t.Fatalf("unexpected file[1]: %+v", c.Files[1])
	}
}

func TestClipboardBlob_InvalidID_Rejected(t *testing.T) {
	ts, _, secret := newTestClipboardServer(t)
	path := "/api/clipboard/blob/gggggggggggggggggggggggggggggggg" // right length, not hex
	tsStr := strconv.FormatInt(time.Now().Unix(), 10)
	sig := CalculateHMAC("GET", path, tsStr, "", secret)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	req.Header.Set("X-Auth-Signature", sig)
	req.Header.Set("X-Auth-Timestamp", tsStr)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected rejection for invalid blob id, got status %d", resp.StatusCode)
	}
}
