// Package sasinput lets the agent trigger a Secure Attention Sequence
// (Ctrl+Alt+Del) on Windows -- the one input a client can't reach through
// ordinary SendInput injection, on purpose: Windows deliberately never lets
// synthesized input reach the SAS gesture itself (that guarantee is what
// makes SAS "secure" -- it can't be spoofed by a compromised or remote-
// controlled desktop app). A client connected while the target session is
// on the Winlogon lock screen (see sessionlaunch's package doc and
// capture-dxgi's attach_input_desktop for the rest of that story) can see
// and type into the password field via normal keyboard injection just
// fine, but on a machine where "Interactive logon: require CTRL+ALT+DEL"
// is enabled, nothing reaches that field at all until something raises SAS
// first -- RustDesk hits the same wall and solves it the same way this
// package does: the undocumented-but-stable sas.dll!SendSAS export,
// gated behind the SoftwareSASGeneration policy (see
// EnsureServicesCanGenerateSAS's doc comment).
package sasinput
