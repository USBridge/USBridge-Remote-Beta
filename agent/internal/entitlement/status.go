package entitlement

import "time"

// Status is what the GUI (and, over adminapi, a thin-client GUI attached to
// a separate headless engine process) needs to render the four-way
// license switcher (see internal/ui's showLicenseDialog) and, once
// entitled, the Sunshine/RustShine switch.
type Status struct {
	// Linked is true once a verified, unexpired, hardware-bound desktop
	// token (any tier -- free/pro/enterprise) is cached locally. Only
	// false in the brief window before app.go's bootstrapFreeTier first
	// runs, or if this machine's hardware id can't be determined at all --
	// every hardware id gets an unconditional free tier with no purchase,
	// so in practice this is true almost all the time. The name predates
	// the Patreon->Stripe/hardware-bound switch (see this package's doc
	// comment) but the JSON field name is kept stable -- nothing
	// downstream needs to change just because what "linked" means
	// underneath did.
	Linked    bool      `json:"linked"`
	Tier      string    `json:"tier,omitempty"` // "free", "pro", or "enterprise" -- see entitlement.Claims.Tier
	ExpiresAt time.Time `json:"expires_at,omitempty"`

	// ActiveBackend is "sunshine" or "rustshine" -- which one is actually
	// running right now, independent of Linked (a linked supporter can
	// still choose Sunshine).
	ActiveBackend string `json:"active_backend"`
	// RustShineStaged is true once the binary has actually been
	// downloaded and verified onto disk -- switching to RustShine before
	// this is true requires a download first.
	RustShineStaged bool `json:"rustshine_staged"`
	// RustShineVersion is whatever StagedVersion(stateDir) currently
	// reports -- "" if never staged, or staged by a build of this agent
	// that predated version tracking. Shown next to the RustShine row so a
	// customer can see what's actually installed without opening the
	// license dialog; the GUI's "Check for updates" button
	// (CheckRustShineUpdateNow) is what refreshes this to a newer value.
	RustShineVersion string `json:"rustshine_version,omitempty"`
	// RustShineUpdateInProgress mirrors DownloadInProgress but specifically
	// for a check triggered by the "Check for updates" button (as opposed
	// to the very first "Download RustShine" click, or the silent
	// background watchdog) -- kept separate so the GUI can show "Checking
	// for updates…" instead of the first-download copy without the two
	// call sites racing each other's spinner text.
	RustShineUpdateInProgress bool `json:"rustshine_update_in_progress"`
	// WebRTCEnabled mirrors cfg.RustShineWebRTCDisabled (inverted) -- the
	// GUI's RustShine web-client checkbox reflects and toggles this.
	// Meaningful only when ActiveBackend == "rustshine"; Sunshine has no
	// WebRTC endpoint of its own.
	WebRTCEnabled bool `json:"webrtc_enabled"`

	// LinkInProgress/DownloadInProgress + Progress (0..1, -1 if
	// indeterminate/unknown total) describe an in-flight operation the GUI
	// should show a spinner/progress bar for.
	LinkInProgress     bool    `json:"link_in_progress"`
	DownloadInProgress bool    `json:"download_in_progress"`
	Progress           float64 `json:"progress"`

	// LastError is a short, user-presentable message for the most recent
	// failed operation (checkout failed, download failed, ...) — cleared
	// on the next successful step. Empty string means "nothing to report."
	LastError string `json:"last_error,omitempty"`
}
