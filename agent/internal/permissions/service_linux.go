//go:build linux

package permissions

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"usbridge_agent/internal/capture"
)

const uinputRulePath = "/etc/udev/rules.d/99-usbridge-input.rules"

// UinputGroupName is the dedicated system group that owns /dev/uinput (see
// uinputRuleContent below). Exported so autostart_linux.go's Enable() can
// add its systemd unit's fixed User=%s into it -- the identical pattern
// that file already uses for the "render" GPU group, applied here to close
// the same class of gap for uinput.
const UinputGroupName = "usbridge-input"

// shellQuoteUsername double-quotes s for safe interpolation into the
// /bin/sh -c script below (same escaping rule as autostart_linux.go's
// systemdQuote, which happens to satisfy POSIX double-quote rules too).
func shellQuoteUsername(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`)
	return `"` + replacer.Replace(s) + `"`
}

// buildUinputGrantScript builds the /bin/sh -c script RequestAccessibility
// runs under pkexec: creates UinputGroupName if needed, adds currentUser to
// it (the static, reboot-proof grant), installs the persistent udev rule +
// modules-load file, and chgrp/chmod's the live device node to 0660 for an
// immediate effect this session (see uinputRuleContent's doc comment for why
// both the static group and the live chmod matter). Pulled out of
// RequestAccessibility as a pure function so its exact shape — in
// particular, that it never falls back to a world-writable chmod — has a
// test that doesn't need root or a real polkit agent to run.
func buildUinputGrantScript(rulePath, modulesPath, currentUser string) string {
	return fmt.Sprintf(
		"(getent group %[5]s >/dev/null || groupadd %[5]s) && usermod -aG %[5]s %[6]s && "+
			"install -m 0644 %[1]s %[2]s && install -m 0644 %[3]s %[4]s && modprobe uinput && "+
			"chgrp %[5]s /dev/uinput && chmod 0660 /dev/uinput && "+
			"udevadm control --reload-rules && udevadm trigger --subsystem-match=misc; "+
			"STATUS=$?; command -v setfacl >/dev/null 2>&1 && setfacl -m u:%[6]s:rw- /dev/uinput; exit $STATUS",
		rulePath, uinputRulePath, modulesPath, uinputModulesLoadPath, UinputGroupName, currentUser,
	)
}

// GROUP="usbridge-input", MODE="0660" (not MODE="0666") is what makes this
// survive a reboot *without* making /dev/uinput world-writable -- letting
// literally any local user, any process, synthesize keyboard/mouse input
// system-wide is a real local-privilege-escalation primitive (it can drive
// a pkexec prompt, an unlocked terminal, anything with focus) and Wayland's
// stricter security model does nothing to stop it: uinput operates at the
// kernel evdev level, below any display protocol, so both X11 and Wayland
// compositors read a uinput-created device exactly like real hardware.
//
// Relying only on TAG+="uaccess" (systemd-logind's *dynamic* per-session
// ACL, granted only to whichever user's session is currently active on the
// seat) is what used to make uinput access disappear after every restart:
// usbridge-agent.service is a system unit that starts at boot
// unconditionally, independent of any login (see autostart_linux.go's
// comment on why -- KMS capture needs to come up before a display manager
// does), so it doesn't reliably have, or keep, a session logind considers
// "active" the way an interactive desktop process does -- and while SDDM's
// greeter (a different user) owns the active seat, the uaccess ACL belongs
// to the greeter, not this agent's user. A static GROUP grant applies
// unconditionally every time the kernel (re)creates the device node, with
// no session/login dependency at all -- but unlike MODE=0666, it only
// grants access to whichever specific user(s) this agent actually adds to
// the group (see RequestAccessibility and autostart_linux.go's Enable),
// not every local account. uaccess is kept alongside as an immediate-effect
// path: a foreground, interactively-run agent in an active graphical
// session gets access the instant the rule is (re)applied, without needing
// to wait for its own group membership to take effect on next login/spawn
// -- which is exactly the same combination already used for the "render"
// GPU group in autostart_linux.go for the identical class of problem.
const uinputRuleContent = "KERNEL==\"uinput\", SUBSYSTEM==\"misc\", GROUP=\"" + UinputGroupName + "\", MODE=\"0660\", TAG+=\"uaccess\"\n"

// Even with the GROUP/MODE rule above, /dev/uinput's permissions after a
// reboot depend on *how* the node comes into existence. Without this file,
// nothing forces the real "uinput" kernel module to load at boot: the node
// that shows up early is typically just udev's "static_node" stand-in
// (created by systemd-tmpfiles-setup-dev.service straight from the
// distro's own default rule, e.g. /usr/lib/udev/rules.d/50-udev-default.rules
// -- 0660, root:root there, not ours) — a full re-application of our own
// rule only happens once the module actually registers for real and emits
// a genuine uevent, which otherwise only happens lazily (whenever the
// kernel first autoloads it, racing against whatever tries to open the
// device first). /etc/modules-load.d makes systemd-modules-load.service
// modprobe uinput unconditionally, early in sysinit, so that real
// registration -- and with it, full udev rule processing -- always
// happens well before anything (including this agent's own systemd unit)
// tries to touch the device. See autostart_linux.go's dev-uinput.device
// ordering for the other half of this: the agent unit not even starting
// until udev has finished applying the rule to that real device.
const uinputModulesLoadPath = "/etc/modules-load.d/usbridge-uinput.conf"
const uinputModulesLoadContent = "uinput\n"

type Service struct {
	lastAccessErr string
}

func New() *Service { return &Service{} }

// LastAccessibilityError returns a human-readable reason the last
// RequestAccessibility call failed, or "" if it succeeded (or hasn't run
// yet). Debian's default install neither adds the user to the sudo group
// nor pulls in pkexec (it was split into its own package from policykit-1
// around trixie), unlike Ubuntu where both are present out of the box --
// so the pkexec-based flow below silently does nothing there unless we
// surface why.
func (s *Service) LastAccessibilityError() string { return s.lastAccessErr }

func (s *Service) AccessibilityGranted() bool {
	f, err := os.OpenFile("/dev/uinput", os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	f.Close()
	return uinputRuleUpToDate()
}

// uinputRuleUpToDate reports whether the persistent udev rule on disk
// already grants the current (GROUP=usbridge-input, MODE=0660) content, not
// an older version this project shipped before -- either the bare
// uaccess-only rule, or the briefly-shipped world-writable MODE=0666 one.
// The /dev/uinput open() probe above can succeed right now purely because
// the *caller's own* interactive session happens to hold a live uaccess ACL
// (or the one-time setfacl grant from a previous RequestAccessibility call),
// even on a machine whose on-disk rule -- the one usbridge-agent.service
// actually depends on after the next reboot -- is still an old, weaker one.
// Checking the file too makes RequestAccessibility keep firing (and
// upgrading the rule on disk) for anyone who granted access before this
// fix, instead of only for machines where access is visibly broken right
// this second.
func uinputRuleUpToDate() bool {
	data, err := os.ReadFile(uinputRulePath)
	if err != nil {
		return false
	}
	if string(data) != uinputRuleContent {
		return false
	}
	modules, err := os.ReadFile(uinputModulesLoadPath)
	if err != nil {
		return false
	}
	return string(modules) == uinputModulesLoadContent
}

func (s *Service) ScreenRecordingGranted() bool {
	if capture.GetLinuxEnv() == "Wayland" {
		return capture.GetPortalSession() != ""
	}
	return true
}

func (s *Service) RequestAccessibility() bool {
	s.lastAccessErr = ""
	log.Printf("[permissions] RequestAccessibility called, granted=%v", s.AccessibilityGranted())
	if s.AccessibilityGranted() {
		return true
	}

	if _, err := exec.LookPath("pkexec"); err != nil {
		s.lastAccessErr = "pkexec is not installed. Install it and try again:\n" +
			"  su -c 'apt install pkexec'\n" +
			"(on Debian, pkexec ships in its own package and the default user\n" +
			"isn't in the sudo group, so plain \"sudo apt install\" may also fail)"
		log.Printf("[permissions] %s", s.lastAccessErr)
		return false
	}

	tmp, err := os.CreateTemp("", "usbridge-udev-*.rules")
	if err != nil {
		s.lastAccessErr = fmt.Sprintf("could not create temp udev rule: %v", err)
		log.Printf("[permissions] create temp udev rule: %v", err)
		return false
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.WriteString(uinputRuleContent); err != nil {
		tmp.Close()
		s.lastAccessErr = fmt.Sprintf("could not write temp udev rule: %v", err)
		log.Printf("[permissions] write temp udev rule: %v", err)
		return false
	}
	tmp.Close()

	modulesTmp, err := os.CreateTemp("", "usbridge-modules-load-*.conf")
	if err != nil {
		s.lastAccessErr = fmt.Sprintf("could not create temp modules-load file: %v", err)
		log.Printf("[permissions] create temp modules-load file: %v", err)
		return false
	}
	defer os.Remove(modulesTmp.Name())

	if _, err := modulesTmp.WriteString(uinputModulesLoadContent); err != nil {
		modulesTmp.Close()
		s.lastAccessErr = fmt.Sprintf("could not write temp modules-load file: %v", err)
		log.Printf("[permissions] write temp modules-load file: %v", err)
		return false
	}
	modulesTmp.Close()
	log.Printf("[permissions] temp rule at %s, temp modules-load at %s, running pkexec...", tmp.Name(), modulesTmp.Name())

	// The user this (unprivileged) process is actually running as -- the one
	// that needs adding to UinputGroupName. Falls back to "$(whoami)" inside
	// the privileged script itself (still the same real user, just resolved
	// as root sees it) if the lookup fails for some reason, rather than
	// silently skipping usermod. Double-quoted (shellQuoteUsername) the same
	// way autostart_linux.go's systemdQuote guards its own usermod line for
	// the render group -- usernames are practically always plain, but this
	// costs nothing and avoids trusting that assumption in a root-run script.
	currentUser := "$(whoami)"
	if u, err := user.Current(); err == nil && u.Username != "" {
		currentUser = shellQuoteUsername(u.Username)
	}

	// Install persistent udev rule AND immediately apply chgrp/chmod for
	// current session.
	// install -m 0644 (not cp): cp preserves the source file's mode, and the
	// tmp file above was created by os.CreateTemp as 0600 owned by this
	// (non-root) agent user, so a plain cp left the installed rule file
	// unreadable by anyone but root. udevd itself runs as root so the rule
	// still applied fine either way, but uinputRuleUpToDate() below reads
	// this same path as the agent's own unprivileged user -- with a 0600
	// file it always got EACCES and returned false, so AccessibilityGranted
	// reported "broken" forever even on machines where /dev/uinput was
	// already working (this predates the MODE=0660/GROUP=usbridge-input
	// grant above -- back when this comment was written, that meant
	// world-writable 0666, but the observation about the rule *file*'s own
	// readability holds regardless of what mode the device node itself
	// ends up at). Rule files under /etc/udev/rules.d are world-readable
	// everywhere else on the system; match that.
	//
	// Also install /etc/modules-load.d/usbridge-uinput.conf and modprobe
	// uinput right now: this is the piece that actually makes the whole
	// thing survive a reboot (see uinputModulesLoadPath's comment) -- without
	// forcing a real module load, the udev rule above only gets applied to
	// whatever bare-bones stand-in device node happens to exist at the
	// moment the module finally, lazily, loads on its own.
	//
	// getent/groupadd + usermod -aG: creates UinputGroupName if it doesn't
	// exist yet and adds the current user to it -- the static, reboot-proof
	// half of the grant (see uinputRuleContent's comment): a *future*
	// process for this user (next login, or this agent's own systemd unit
	// after a restart) picks up that group membership automatically and can
	// just open the device via the plain 0660 permission bits.
	//
	// setfacl -m u:<user>:rw is the *other* half, and turned out to be
	// necessary, not optional: confirmed live that neither the group
	// membership just added nor `udevadm trigger` re-tagging the device
	// with uaccess makes the process that's *already running right now*
	// (this one, mid-RequestAccessibility-call) able to open() the device --
	// a process's supplementary group list is fixed at exec/login time in
	// Unix, unaffected by a usermod that happens after it's already
	// running, and re-triggering the uevent didn't cause logind to apply a
	// fresh uaccess ACL either (getfacl showed none). A direct, explicit
	// per-user ACL entry is checked at open() time against the file's
	// current ACL list, independent of the calling process's cached
	// credentials, so it's what actually delivers the "works immediately,
	// this session, without restarting" guarantee the old chmod 0666 had --
	// but scoped to this one user instead of everyone. chgrp/chmod 0660
	// (rather than leaving the node's mode as whatever modprobe/udev just
	// created) still matters on its own for the *reboot* case: it's what
	// the persistent udev rule reproduces every time the module reloads,
	// so a fresh boot or a restarted systemd unit under the also-now-group-
	// member user gets in via the plain permission bits, no ACL required.
	script := buildUinputGrantScript(tmp.Name(), modulesTmp.Name(), currentUser)
	cmd := exec.Command("pkexec", "/bin/sh", "-c", script)
	out, err := cmd.CombinedOutput()
	log.Printf("[permissions] pkexec exit=%v output=%q", err, string(out))
	if err != nil {
		switch {
		case strings.Contains(string(out), "No authentication agent found"):
			s.lastAccessErr = "no polkit authentication agent is running for this session " +
				"(pkexec needs one to prompt for the password). Log into a full desktop " +
				"session and make sure its polkit agent is running, then retry."
		case strings.Contains(err.Error(), "exit status 126"):
			s.lastAccessErr = "authentication was cancelled or dismissed. Click Request again and approve the prompt."
		default:
			s.lastAccessErr = fmt.Sprintf("pkexec failed: %v (%s)", err, strings.TrimSpace(string(out)))
		}
		return false
	}

	time.Sleep(300 * time.Millisecond)
	granted := s.AccessibilityGranted()
	log.Printf("[permissions] after pkexec granted=%v", granted)
	if !granted {
		s.lastAccessErr = "udev rule was installed but /dev/uinput is still inaccessible; " +
			"try unplugging/replugging, or log out and back in."
	}
	return granted
}

func (s *Service) RequestScreenRecording() bool {
	if capture.GetLinuxEnv() == "Wayland" {
		err := capture.InitPortalSession()
		if err != nil {
			logrus.Errorf("Failed to initiate Wayland portal: %v", err)
		}
		return true
	}
	return true
}

func (s *Service) RequestMissing()                    {}
func (s *Service) OpenPrivacySettings() error         { return nil }
func (s *Service) OpenScreenRecordingSettings() error { return nil }

// clipboardToolFound reports whether a CLI clipboard helper
// internal/clipboard's Linux backend knows how to drive is installed --
// mirrors detect()'s preference order there (wl-clipboard needs both
// halves present; xclip and xsel are single binaries).
func clipboardToolFound() bool {
	if _, err := exec.LookPath("wl-copy"); err == nil {
		if _, err := exec.LookPath("wl-paste"); err == nil {
			return true
		}
	}
	if _, err := exec.LookPath("xclip"); err == nil {
		return true
	}
	if _, err := exec.LookPath("xsel"); err == nil {
		return true
	}
	return false
}

// ClipboardToolAvailable reports whether clipboard sync has a working CLI
// helper to shell out to. Wayland sessions typically ship wl-clipboard
// preinstalled, but plenty of X11/XWayland desktops -- this project's own
// Debian test machine included -- ship neither xclip nor xsel by default,
// which silently and permanently disables clipboard sync (both directions)
// until one is installed: confirmed live via
// "clipboard: no clipboard tool available" on every apply.
func (s *Service) ClipboardToolAvailable() bool {
	return clipboardToolFound()
}

// pkgManager describes one Linux package manager. Package names are
// identical across every one of these for both packages RequestClipboardTool
// installs -- "xclip" and "wl-clipboard" -- so a plain space-joined package
// list works for all of them; no per-distro name mapping needed.
type pkgManager struct {
	name string
	// probe is the binary whose presence on PATH identifies this manager.
	probe string
	// install builds the script run as root (via pkexec /bin/sh -c) to
	// install pkgs non-interactively. pkgs is always a compile-time
	// constant slice from RequestClipboardTool, never user input.
	install func(pkgs []string) string
}

// pkgManagers covers every package manager on the popular Linux desktop
// distro families: apt (Debian/Ubuntu/Mint/Pop!_OS), dnf (Fedora/RHEL 8+/
// Rocky/Alma), yum (older RHEL/CentOS 7), pacman (Arch/Manjaro/EndeavourOS),
// zypper (openSUSE), apk (Alpine). Checked/ordered so a system with more
// than one manager installed (e.g. a distro-hopper's leftover binaries)
// still picks its *actual* one first.
var pkgManagers = []pkgManager{
	{
		name:  "apt",
		probe: "apt-get",
		install: func(pkgs []string) string {
			list := strings.Join(pkgs, " ")
			// Try the install straight away first -- on any system that's
			// ever run "apt update" before (i.e. virtually every real
			// desktop, as opposed to a brand new container image), the
			// local package index already has these in it, so this alone
			// succeeds without touching the network's repo signing keys at
			// all.
			//
			// Only fall back to "apt-get update" (with its own failure
			// tolerated via ";" not "&&") if that plain install fails --
			// confirmed live: a machine with an expired/misconfigured repo
			// signing key makes "apt-get update" hard-fail with exit 100
			// ("... couldn't be verified because the public key is not
			// available"), which used to abort the whole install even when
			// the existing cached index already had a perfectly installable
			// xclip in it. --no-install-recommends keeps this to just the
			// requested packages and their real deps (for xclip: libc6,
			// libx11-6 -- already present on this project's own
			// XWayland-forced GUI, see forceXWaylandForGUI) -- no extra
			// desktop/display-server packages get pulled in either way.
			return "DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends " + list + " || " +
				"(DEBIAN_FRONTEND=noninteractive apt-get update -qq; " +
				"DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends " + list + ")"
		},
	},
	// install_weak_deps=False is dnf's --no-install-recommends equivalent;
	// yum has no such knob (it predates weak deps as a concept) so it's
	// omitted there -- neither package pulls in anything extra either way.
	{name: "dnf", probe: "dnf", install: func(pkgs []string) string {
		return "dnf install -y --setopt=install_weak_deps=False " + strings.Join(pkgs, " ")
	}},
	{name: "yum", probe: "yum", install: func(pkgs []string) string {
		return "yum install -y " + strings.Join(pkgs, " ")
	}},
	// -Sy (not plain -S): like apt above, pacman needs its local sync
	// database refreshed first or a perfectly valid package name can come
	// back "target not found" on a system that hasn't run pacman -Sy
	// recently (containers, minimal installs). Pacman never auto-installs
	// optdepends on its own, so there's no recommends-equivalent flag needed.
	{name: "pacman", probe: "pacman", install: func(pkgs []string) string {
		return "pacman -Sy --noconfirm " + strings.Join(pkgs, " ")
	}},
	{name: "zypper", probe: "zypper", install: func(pkgs []string) string {
		return "zypper --non-interactive install --no-recommends " + strings.Join(pkgs, " ")
	}},
	// Alpine's apk has no recommends concept either -- add only installs
	// what's actually required.
	{name: "apk", probe: "apk", install: func(pkgs []string) string {
		return "apk add --no-cache " + strings.Join(pkgs, " ")
	}},
}

// detectPkgManager returns the first package manager from pkgManagers whose
// probe binary is on PATH, or nil if none of them are (an unsupported/
// exotic distro, or a minimal container image with no package manager at
// all).
func detectPkgManager() *pkgManager {
	return detectPkgManagerWith(exec.LookPath)
}

// detectPkgManagerWith is detectPkgManager with its PATH lookup injected, so
// the distro-selection logic (order, first-match) is testable without
// depending on which package managers happen to be installed on whatever
// machine runs `go test`.
func detectPkgManagerWith(lookPath func(string) (string, error)) *pkgManager {
	for i := range pkgManagers {
		if _, err := lookPath(pkgManagers[i].probe); err == nil {
			return &pkgManagers[i]
		}
	}
	return nil
}

// clipboardPkgs is the package list RequestClipboardTool installs: xclip
// always, plus wl-clipboard on a Wayland session -- see RequestClipboardTool
// for why both. Shared with ClipboardInstallPreview so the UI's "what will
// this do?" tooltip can never drift from what actually gets installed.
func clipboardPkgs() []string {
	pkgs := []string{"xclip"}
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		pkgs = append(pkgs, "wl-clipboard")
	}
	return pkgs
}

// ClipboardInstallPreview returns the exact command RequestClipboardTool
// would run right now (as pkexec would run it), for the UI's "Install"
// button to show in a tooltip/info dialog before the user clicks it and
// hits a polkit password prompt sight-unseen. Returns "" if nothing would
// actually run (no pkexec, or no supported package manager) -- the caller
// falls back to RequestClipboardTool's own LastAccessibilityError message
// in that case, surfaced only once the button is actually clicked.
func (s *Service) ClipboardInstallPreview() string {
	if _, err := exec.LookPath("pkexec"); err != nil {
		return ""
	}
	pm := detectPkgManager()
	if pm == nil {
		return ""
	}
	return "pkexec /bin/sh -c \"" + pm.install(clipboardPkgs()) + "\""
}

// RequestClipboardTool installs xclip (and, on a Wayland session, also
// wl-clipboard) via pkexec, using whichever package manager this distro
// actually has (see pkgManagers) -- the same pkexec pattern as
// RequestAccessibility.
//
// Both, not just xclip: whether a given app's paste actually observes
// XWayland's bridged X11 CLIPBOARD selection depends on the app's toolkit
// and how well the compositor bridges X11<->native-Wayland clipboards --
// not reliable enough to assume. Confirmed live: text and file clipboard
// sync stayed broken in both directions on a real KDE/kwin_wayland session
// with only xclip installed. clipboard.detect() dual-writes through both
// once both are present (see backend_linux.go's dualTool), so installing
// both here is what actually makes the "Install" button fix clipboard sync
// end to end on Wayland, not just get xclip onto the disk.
func (s *Service) RequestClipboardTool() bool {
	s.lastAccessErr = ""
	if clipboardToolFound() {
		return true
	}
	if _, err := exec.LookPath("pkexec"); err != nil {
		s.lastAccessErr = "pkexec is not installed. Install a clipboard tool manually instead, e.g.:\n" +
			"  su -c 'apt install xclip wl-clipboard -y'   (Debian/Ubuntu)\n" +
			"  su -c 'dnf install -y xclip wl-clipboard'   (Fedora/RHEL)\n" +
			"  su -c 'pacman -S xclip wl-clipboard'        (Arch)"
		log.Printf("[permissions] %s", s.lastAccessErr)
		return false
	}

	pm := detectPkgManager()
	if pm == nil {
		s.lastAccessErr = "no supported package manager found (looked for apt, dnf, yum, pacman, " +
			"zypper, apk). Install xclip (and wl-clipboard, on Wayland) manually for this distro."
		log.Printf("[permissions] %s", s.lastAccessErr)
		return false
	}

	pkgs := clipboardPkgs()

	cmd := exec.Command("pkexec", "/bin/sh", "-c", pm.install(pkgs))
	out, err := cmd.CombinedOutput()
	log.Printf("[permissions] install %v via %s pkexec exit=%v output=%q", pkgs, pm.name, err, string(out))
	if err != nil {
		switch {
		case strings.Contains(string(out), "No authentication agent found"):
			s.lastAccessErr = "no polkit authentication agent is running for this session " +
				"(pkexec needs one to prompt for the password). Log into a full desktop " +
				"session and make sure its polkit agent is running, then retry."
		case strings.Contains(err.Error(), "exit status 126"):
			s.lastAccessErr = "authentication was cancelled or dismissed. Click Install again and approve the prompt."
		default:
			s.lastAccessErr = fmt.Sprintf("clipboard tool install via %s failed: %v (%s)", pm.name, err, strings.TrimSpace(string(out)))
		}
		return false
	}

	granted := clipboardToolFound()
	if !granted {
		s.lastAccessErr = fmt.Sprintf("%s reported success but no clipboard tool is on PATH yet; try again or install manually.", pm.name)
	}
	return granted
}

// findCapTool resolves getcap/setcap to an absolute path. Both live in
// /usr/sbin (libcap2-bin), which many non-login shells -- and pkexec's own
// sanitized environment -- don't include in PATH, so a bare exec.LookPath
// (or handing the bare name to pkexec) can fail with "not found" even
// though the binary is installed.
func findCapTool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, dir := range []string{"/usr/sbin", "/sbin"} {
		p := dir + "/" + name
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return name
}

// KMSCaptureGranted reports whether the bundled sunshine_capexec launcher
// has the CAP_SYS_ADMIN capability needed for Sunshine's direct KMS screen
// capture (root-level, no compositor/portal involved).
//
// capexecPath is the path to sunshine_capexec, NOT to sunshine itself — see
// RequestKMSCapture for why the capability lives on a separate launcher.
func (s *Service) KMSCaptureGranted(capexecPath string) bool {
	if strings.TrimSpace(capexecPath) == "" {
		return false
	}
	out, err := exec.Command(findCapTool("getcap"), capexecPath).CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "cap_sys_admin")
}

// RequestKMSCapture grants CAP_SYS_ADMIN to the bundled sunshine_capexec
// launcher via pkexec setcap, so Sunshine can use its KMS capture backend
// without running as root outright.
//
// This deliberately targets sunshine_capexec, a tiny statically-linked
// (zero dynamic deps) launcher — never the sunshine binary itself. Setting a
// file capability puts the dynamic linker into "secure execution" mode for
// that binary (same as setuid): glibc ignores RPATH/RUNPATH and
// LD_LIBRARY_PATH entirely, the same protection that stops a setuid binary
// from being tricked into loading an attacker-controlled library. Since
// Sunshine resolves its bundled dependencies (e.g. libminiupnpc.so.17) via
// RPATH=$ORIGIN/../lib, setting the capability directly on it would break
// that resolution the moment it's granted. sunshine_capexec instead raises
// CAP_SYS_ADMIN into its own ambient capability set and execs the real,
// perfectly ordinary (no file capability of its own) sunshine binary —
// ambient capabilities are preserved across exec of a non-privileged binary
// without ever placing it into secure-execution mode, so its RPATH keeps
// resolving normally. See cmd/sunshine_capexec.
func (s *Service) RequestKMSCapture(capexecPath string) bool {
	if strings.TrimSpace(capexecPath) == "" {
		return false
	}
	if s.KMSCaptureGranted(capexecPath) {
		return true
	}
	cmd := exec.Command("pkexec", findCapTool("setcap"), "cap_sys_admin=eip", capexecPath)
	out, err := cmd.CombinedOutput()
	log.Printf("[permissions] setcap pkexec exit=%v output=%q", err, string(out))
	if err != nil {
		return false
	}
	return s.KMSCaptureGranted(capexecPath)
}

// GPU clock locking is Windows-only (NVML clock lock via an elevated
// gamestream-server --gpu-clock-lock-daemon helper -- see
// service_windows.go's own docs); not applicable on Linux.
func (s *Service) GPUClockLockSupported() bool                            { return false }
func (s *Service) GPUClockLockElevated() bool                             { return false }
func (s *Service) RequestGPUClockLock(binPath string, watchPID int) error { return nil }

// KillGamestreamServerElevated is Windows-only -- see service_windows.go.
// Linux has no UAC-style per-process elevation prompt, and a stray process
// this agent's own SIGKILL couldn't already reach wouldn't be reachable via
// any escalation this package could request here either.
func (s *Service) KillGamestreamServerElevated() error { return nil }
