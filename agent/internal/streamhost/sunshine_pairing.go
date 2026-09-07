package streamhost

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// sunshineAdminHTTPClient is shared across every ListClients/SubmitPIN/
// UnpairClient call instead of a fresh `&http.Client{...}` per call -- see
// rustshineAdminHTTPClient's doc comment (rustshine_codec.go) for why a
// throwaway Client/Transport per call leaks a persistent connection instead
// of reusing one (confirmed live: exhausted gamestream-server's 1024 fd
// limit via the identical pattern in the rustshine backend's sibling
// functions). Needs its own InsecureSkipVerify TLS config -- unlike
// serverinfoHTTPClient (sunshine_codec.go), which hits the plain-HTTP
// NvHTTP port, these hit Sunshine's self-signed HTTPS admin API.
var sunshineAdminHTTPClient = &http.Client{
	Timeout: 5 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	},
}

// readErrBody reads (and truncates) a failed admin-API response body so
// error messages carry Sunshine's actual explanation (e.g. "Missing CSRF
// token", "PIN must be between 0000 and 9999") instead of a bare status
// code. Without this we were logging "sunshine returned HTTP 400" with no
// way to tell which of savePin's half-dozen 400 paths actually fired --
// see the itsme228/Sunshine fork's confighttp.cpp::savePin/
// validate_csrf_token for the exact set of causes.
func readErrBody(resp *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	msg := string(b)
	if msg == "" {
		return resp.Status
	}
	return msg
}

// fetchCSRFToken requests a short-lived CSRF token from Sunshine's admin API,
// required by POST/DELETE/PATCH admin endpoints (savePin, unpair, ...) since
// the itsme228/Sunshine fork added CSRF protection to confighttp.cpp.
// validate_csrf_token there only *enforces* the token when the request
// carries an Origin or Referer header naming a scheme+host not present in
// Sunshine's csrf_allowed_origins config; a header-less request (which is
// what Go's http.Client sends here) is exempt. Attaching a valid token
// anyway removes that exemption as a load-bearing assumption -- if a proxy,
// VPN client, or a future Go release ever adds either header transparently,
// the PIN relay silently starts failing with HTTP 400 again otherwise.
//
// Returns ("", nil) -- not an error -- when the endpoint doesn't exist
// (older/stock Sunshine predating CSRF protection): callers should proceed
// without the header rather than treat that as fatal.
func fetchCSRFToken(adminPort int, user, pass string) (string, error) {
	url := fmt.Sprintf("https://%s:%d/api/csrf-token", adminHost(), adminPort)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(user, pass)
	resp, err := sunshineAdminHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("Sunshine unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("csrf-token request returned HTTP %d: %s", resp.StatusCode, readErrBody(resp))
	}
	var result struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.CSRFToken, nil
}

// ListClients returns the Moonlight clients currently paired with the
// Sunshine instance running on adminPort. Requires valid admin credentials
// to have been bootstrapped first.
func (b *sunshineBackend) ListClients(adminPort int) ([]Client, error) {
	url := fmt.Sprintf("https://%s:%d/api/clients/list", adminHost(), adminPort)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(b.AdminUser(), b.adminPass())
	resp, err := sunshineAdminHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sunshine returned HTTP %d: %s", resp.StatusCode, readErrBody(resp))
	}
	var result struct {
		NamedCerts []Client `json:"named_certs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.NamedCerts, nil
}

// SubmitPIN sends a Moonlight pairing PIN to Sunshine's admin API on adminPort.
// The PIN is the 4-digit code shown by the Moonlight client during pairing.
func (b *sunshineBackend) SubmitPIN(adminPort int, pin string) error {
	user, pass := b.AdminUser(), b.adminPass()
	token, err := fetchCSRFToken(adminPort, user, pass)
	if err != nil {
		// Non-fatal: fall through and try without the header. Sunshine's own
		// CSRF check only enforces the token for requests carrying an
		// Origin/Referer header, which this client never sends -- see
		// fetchCSRFToken's doc comment.
		log.Printf("[sunshine] csrf-token fetch failed, submitting PIN without one: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"pin": pin})
	url := fmt.Sprintf("https://%s:%d/api/pin", adminHost(), adminPort)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-CSRF-Token", token)
	}
	req.SetBasicAuth(user, pass)
	resp, err := sunshineAdminHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("Sunshine unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sunshine returned HTTP %d: %s", resp.StatusCode, readErrBody(resp))
	}
	return nil
}

// UnpairClient removes the Moonlight client with the given uniqueID from
// Sunshine's authorized client list via the admin API on adminPort.
func (b *sunshineBackend) UnpairClient(adminPort int, uniqueID string) error {
	user, pass := b.AdminUser(), b.adminPass()
	token, err := fetchCSRFToken(adminPort, user, pass)
	if err != nil {
		log.Printf("[sunshine] csrf-token fetch failed, submitting unpair without one: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"uuid": uniqueID})
	url := fmt.Sprintf("https://%s:%d/api/clients/unpair", adminHost(), adminPort)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-CSRF-Token", token)
	}
	req.SetBasicAuth(user, pass)
	resp, err := sunshineAdminHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unpair failed: %s: %s", resp.Status, readErrBody(resp))
	}
	return nil
}
