package entitlement

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
)

// httpTimeout bounds every call to the backend -- none of these are on the
// agent's startup path (see app.go's applyPreferredBackend, which only
// ever uses an already-cached, already-verified local token), so a slow or
// unreachable backend just makes a background check take a few seconds
// longer, never blocks the engine coming up.
const httpTimeout = 10 * time.Second

func httpClient() *http.Client { return &http.Client{Timeout: httpTimeout} }

// Platform reports this build's platform key exactly as
// usbridge-entitlement-backend's PLATFORM_ASSET map and rust-shine's
// release-gamestream-server.yml asset names use it -- "" for anything not
// covered (there's no RustShine build for that platform, e.g. Linux arm64
// or any non-Sunshine-supported OS).
func Platform() string {
	switch {
	case runtime.GOOS == "linux" && runtime.GOARCH == "amd64":
		return "linux-x86_64"
	case runtime.GOOS == "windows" && runtime.GOARCH == "amd64":
		return "windows-x86_64"
	case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
		return "macos-arm64"
	default:
		return ""
	}
}

// IssueResult is the outcome of asking the backend for a desktop token
// bound to this machine's hwid.Get() value. Deliberately the same shape
// for both StartTrial and RefreshLicense (mirrors
// usbridge-entitlement-backend's own register/refresh being the same
// handler).
type IssueResult struct {
	// Status is "free", "pro", or "enterprise" -- see
	// usbridge-entitlement-backend's desktopLicense.ts tier doc comment.
	// register/refresh NEVER fail: every hardware id always gets a token,
	// "free" by default, so there is no "not_licensed"/"trial_used"
	// outcome to check for anymore -- Status alone tells the whole story.
	Status    string
	Token     string
	ExpiresIn int // seconds
}

// StartTrial is a thin back-compat wrapper around RefreshLicense, kept only
// because internal/adminapi's thin-client protocol still exposes a
// separate "start-trial" call. There is nothing left to "start" -- the
// backend's free tier is unconditional and permanent (see
// usbridge-entitlement-backend's desktopLicense.ts module doc comment) --
// this just fetches today's free-tier token the same way RefreshLicense
// would.
func StartTrial(ctx context.Context, hwID string) (*IssueResult, error) {
	return RefreshLicense(ctx, hwID)
}

// RefreshLicense re-derives a fresh desktop token for this machine's
// hardware id, reflecting whatever tier (free/pro/enterprise) the backend
// currently has it on record as -- called both right after
// StartCheckoutURL's flow completes and on the periodic watchdog. No
// browser interaction, no stored credential to refresh unlike the old
// Patreon flow's provider refresh token -- hwID itself is the only
// correlating value, re-derived locally (hwid.Get()) on every call rather
// than persisted. Never returns a "not licensed" outcome -- see
// IssueResult's doc comment.
func RefreshLicense(ctx context.Context, hwID string) (*IssueResult, error) {
	reqBody, _ := json.Marshal(map[string]string{"hw_id": hwID})
	var raw struct {
		Status    string `json:"status"`
		License   string `json:"license"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := doJSON(ctx, http.MethodPost, "/v1/desktop-license/refresh", reqBody, "", &raw); err != nil {
		return nil, err
	}
	return &IssueResult{Status: raw.Status, Token: raw.License, ExpiresIn: raw.ExpiresIn}, nil
}

// StartCheckoutURL asks the backend for a Stripe Checkout Session URL tied
// to this machine's hardware id, for the given tier ("pro" or
// "enterprise") -- open it in the system browser (or an embedded webview;
// it's just an https:// URL either way). The backend's webhook marks hwID
// licensed at that tier the moment payment completes; RefreshLicense is
// what actually picks that up afterward -- there is no separate "purchase
// complete" callback into this process the way the old OAuth flow's poll
// loop had one; see app.go's pollForLicense for how the caller bridges
// that gap.
func StartCheckoutURL(ctx context.Context, hwID, tier string) (string, error) {
	reqBody, _ := json.Marshal(map[string]string{"hw_id": hwID, "tier": tier})
	var out struct {
		URL string `json:"url"`
	}
	if err := doJSON(ctx, http.MethodPost, "/v1/desktop-billing/checkout", reqBody, "", &out); err != nil {
		return "", err
	}
	if out.URL == "" {
		return "", fmt.Errorf("entitlement: checkout response had no url")
	}
	return out.URL, nil
}

type DownloadInfo struct {
	URL       string `json:"download_url"`
	SHA256    string `json:"sha256"`
	Version   string `json:"version"`
	SizeBytes int64  `json:"size_bytes"`
}

// ResolveDownload asks the backend for a short-lived signed URL to the
// RustShine build for this platform -- only succeeds if entitlementToken
// still verifies as current and unexpired on the backend's own side too
// (this package's local Verify is necessary but not sufficient: the
// backend independently re-checks, since a token could be locally valid
// but for a platform/version combination it no longer wants to serve).
func ResolveDownload(ctx context.Context, entitlementToken, platform string) (*DownloadInfo, error) {
	var out DownloadInfo
	path := "/v1/download/rustshine?platform=" + url.QueryEscape(platform)
	if err := doJSON(ctx, http.MethodGet, path, nil, entitlementToken, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func newRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, backendBaseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "usbridge-agent-entitlement")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func doJSON(ctx context.Context, method, path string, body []byte, bearer string, out any) error {
	req, err := newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("entitlement: request %s: %w", path, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("entitlement: %s: HTTP %d: %s", path, resp.StatusCode, truncate(respBody))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("entitlement: parse response from %s: %w", path, err)
	}
	return nil
}

func truncate(b []byte) string {
	const max = 300
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}
