package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"usbridge-client/internal/account"
	"usbridge-client/internal/syncconn"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/storage"
	"github.com/sirupsen/logrus"
)

// accountLoginPollTimeout bounds how long the background poll loop keeps
// checking after the verification URL was opened -- a Google login round
// trip is seconds, not minutes; this just stops an abandoned attempt (tab
// closed, never finished) from polling forever.
const accountLoginPollTimeout = 5 * time.Minute

// AccountManager owns the USBridge account login (see internal/account --
// Google login, same identity as billing.usbridge.io/manage) for the
// client app's top-bar account button, plus the locally-derived key that
// makes the connections-list sync (see internal/syncconn and
// connection_manager_sync.go) end-to-end encrypted. These are
// deliberately two separate secrets: AccountToken (proves who this is to
// the backend) and SyncKey (derived from a passphrase the backend never
// sees at all, see syncconn's own doc comment) -- a leaked AccountToken
// lets someone see/rebind licenses under this backend's existing
// self-service rules, but decrypts nothing.
//
// Persisted via fyne.App.Storage() as its own "account.json", the same
// mechanism ConnectionManager's own connections.json already uses --
// client/internal/models.AppConfig is a mostly-static, deployment-level
// config file (USB ports, scan paths, ...) loaded once at startup via
// viper, not a runtime settings store anything here writes back to.
type AccountManager struct {
	app fyne.App

	mu           sync.Mutex
	email        string
	accountToken string
	syncKey      []byte // derived, see syncconn.DeriveKey -- nil until a passphrase has been set

	loginInProgress bool
	pollCancel      context.CancelFunc
	lastError       string

	// licensesCache/licensesCached/licensesErr: the last successful (or
	// failed) Licenses() fetch for the CURRENT login, kept here rather than
	// only inside the Account dialog's own closure -- so a re-open of the
	// dialog within the same login session renders the real license list
	// immediately, with no "Loading your licenses…" placeholder and no
	// resulting resize once the fetch resolves (see showAccountDialog,
	// which used to always start from an empty cache on every open).
	// Cleared on Logout since it belongs to that login, not the device.
	licensesCached bool
	licensesCache  []account.License
	licensesErr    error

	// onChange notifies the GUI (the top-bar button's icon/badge, an open
	// account dialog) that something worth re-rendering changed --
	// deliberately fire-and-forget, called with the lock released.
	onChange func()
}

type accountFileState struct {
	Email        string `json:"email,omitempty"`
	AccountToken string `json:"account_token,omitempty"`
	SyncKeyB64   string `json:"sync_key_b64,omitempty"`
}

func NewAccountManager(app fyne.App, onChange func()) *AccountManager {
	am := &AccountManager{app: app, onChange: onChange}
	am.load()
	return am
}

func (am *AccountManager) notify() {
	if am.onChange != nil {
		am.onChange()
	}
}

func (am *AccountManager) getStorageURI() fyne.URI {
	uri, err := storage.Child(am.app.Storage().RootURI(), "account.json")
	if err != nil {
		u, _ := url.Parse("file://account.json")
		return storage.NewFileURI(u.String())
	}
	return uri
}

// load mirrors connection_manager_storage.go's loadConnections -- same
// fyne storage.Reader pattern, just a different (much smaller) file.
func (am *AccountManager) load() {
	reader, err := storage.Reader(am.getStorageURI())
	if err != nil {
		return // no account.json yet -- fine, logged out is the default state
	}
	defer reader.Close()

	var data []byte
	buf := make([]byte, 1024)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			data = append(data, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	var state accountFileState
	if err := json.Unmarshal(data, &state); err != nil {
		return
	}
	am.email = state.Email
	am.accountToken = state.AccountToken
	if state.SyncKeyB64 != "" {
		if key, err := base64.StdEncoding.DecodeString(state.SyncKeyB64); err == nil {
			am.syncKey = key
		}
	}
}

// save mirrors connection_manager_storage.go's saveConnections. Must be
// called with am.mu held.
func (am *AccountManager) save() {
	state := accountFileState{Email: am.email, AccountToken: am.accountToken}
	if am.syncKey != nil {
		state.SyncKeyB64 = base64.StdEncoding.EncodeToString(am.syncKey)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		logrus.Errorf("account: serialize error: %v", err)
		return
	}
	writer, err := storage.Writer(am.getStorageURI())
	if err != nil {
		logrus.Errorf("account: write error: %v", err)
		return
	}
	defer writer.Close()
	if _, err := writer.Write(data); err != nil {
		logrus.Errorf("account: save error: %v", err)
	}
}

func (am *AccountManager) Email() string {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.email
}

func (am *AccountManager) LoggedIn() bool {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.accountToken != ""
}

func (am *AccountManager) HasSyncKey() bool {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.syncKey != nil
}

func (am *AccountManager) LoginInProgress() bool {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.loginInProgress
}

func (am *AccountManager) LastError() string {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.lastError
}

// SyncCredentials returns what connection_manager_sync.go needs to talk to
// the backend -- ok is false until both a login AND a sync passphrase have
// been set (see SetSyncPassphrase).
func (am *AccountManager) SyncCredentials() (accountToken string, key []byte, ok bool) {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.accountToken == "" || am.syncKey == nil {
		return "", nil, false
	}
	return am.accountToken, am.syncKey, true
}

// AccountToken returns just the Bearer login token (no sync key needed) --
// used by ResetSyncPassphrase (connection_manager_sync.go), which has to
// talk to the backend BEFORE a new key exists yet, unlike SyncCredentials
// above which requires both.
func (am *AccountManager) AccountToken() (token string, ok bool) {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.accountToken == "" {
		return "", false
	}
	return am.accountToken, true
}

func (am *AccountManager) setError(msg string) {
	am.mu.Lock()
	am.loginInProgress = false
	am.lastError = msg
	am.mu.Unlock()
	am.notify()
}

// StartLogin begins a device-code login (see internal/account's package
// doc comment): mints a code, opens the verification URL in the system
// browser, and polls in the background until it's claimed. Mirrors the Go
// agent's App.StartAccountLogin almost exactly (see that function's doc
// comment for the shared reasoning) -- this package just has no App
// singleton to hang the poll goroutine off of, so it owns one directly.
func (am *AccountManager) StartLogin() error {
	start, err := account.StartLogin(context.Background())
	if err != nil {
		am.setError(fmt.Sprintf("could not start login: %v", err))
		return err
	}

	am.mu.Lock()
	if am.pollCancel != nil {
		am.pollCancel()
	}
	pollCtx, cancel := context.WithCancel(context.Background())
	am.pollCancel = cancel
	am.loginInProgress = true
	am.lastError = ""
	am.mu.Unlock()
	am.notify()

	if uri, parseErr := url.Parse(start.VerificationURL); parseErr == nil {
		go func() {
			if err := am.app.OpenURL(uri); err != nil {
				logrus.Errorf("account: failed to open login URL %q: %v", start.VerificationURL, err)
			}
		}()
	}

	go am.pollLogin(pollCtx, start.Code)
	return nil
}

func (am *AccountManager) CancelLogin() {
	am.mu.Lock()
	if am.pollCancel != nil {
		am.pollCancel()
		am.pollCancel = nil
	}
	am.loginInProgress = false
	am.lastError = ""
	am.mu.Unlock()
	am.notify()
}

func (am *AccountManager) pollLogin(ctx context.Context, code string) {
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
			am.setError("Didn't detect a completed login yet — try \"Log in\" again.")
			return
		}
		result, err := account.Poll(ctx, code)
		if err != nil {
			continue // transient network hiccup -- keep polling until the deadline or ctx cancellation
		}
		if result.Status == "expired" {
			am.setError("Login link expired — click \"Log in\" again.")
			return
		}
		if result.Status != "complete" {
			continue
		}

		am.mu.Lock()
		am.email = result.Email
		am.accountToken = result.AccountToken
		am.loginInProgress = false
		am.lastError = ""
		am.save()
		am.mu.Unlock()
		am.notify()
		return
	}
}

// SetSyncPassphrase derives and persists this device's sync key from the
// given passphrase (see syncconn.DeriveKey) -- the same passphrase entered
// on every device that should share this account's synced connections
// list. There is nothing to verify it against locally (this is the first
// time this device has ever seen this passphrase, by definition); a wrong
// passphrase is only discovered the first time a Pull's Decrypt fails (see
// connection_manager_sync.go), at which point the caller should prompt to
// re-enter it rather than silently treating garbage as an empty list.
func (am *AccountManager) SetSyncPassphrase(passphrase string) {
	am.mu.Lock()
	email := am.email
	am.syncKey = syncconn.DeriveKey(email, passphrase)
	am.save()
	am.mu.Unlock()
	am.notify()
}

// ClearSyncKey forgets the locally-derived key (e.g. after a Decrypt
// failure the human confirms means "wrong passphrase") without logging the
// account out entirely.
func (am *AccountManager) ClearSyncKey() {
	am.mu.Lock()
	am.syncKey = nil
	am.save()
	am.mu.Unlock()
	am.notify()
}

// Licenses fetches every license (SBC + desktop) belonging to the
// logged-in account -- for the account dialog's read-only profile view
// (see account_dialog.go). Returns an error if not logged in.
func (am *AccountManager) Licenses(ctx context.Context) ([]account.License, error) {
	am.mu.Lock()
	token := am.accountToken
	am.mu.Unlock()
	if token == "" {
		return nil, fmt.Errorf("not logged in")
	}
	licenses, err := account.ListLicenses(ctx, token)
	am.mu.Lock()
	am.licensesCached = true
	am.licensesCache = licenses
	am.licensesErr = err
	am.mu.Unlock()
	return licenses, err
}

// CachedLicenses returns the last successful-or-failed Licenses() result
// for the current login, if there's been one yet -- lets the Account
// dialog skip its "Loading…" placeholder (and the resize that follows once
// a fresh fetch actually resolves) on every open after the first.
func (am *AccountManager) CachedLicenses() (licenses []account.License, err error, ok bool) {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.licensesCache, am.licensesErr, am.licensesCached
}

// Logout forgets the locally-stored login AND sync key -- purely local
// (the Bearer account token simply expires on its own after 30 days, see
// usbridge-entitlement-backend's deviceAuth.ts ACCOUNT_TOKEN_TTL_SECONDS;
// there is no server-side session to invalidate).
func (am *AccountManager) Logout() {
	am.mu.Lock()
	am.email = ""
	am.accountToken = ""
	am.syncKey = nil
	am.lastError = ""
	am.licensesCached = false
	am.licensesCache = nil
	am.licensesErr = nil
	am.save()
	am.mu.Unlock()
	am.notify()
}
