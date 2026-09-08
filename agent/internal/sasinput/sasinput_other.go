//go:build !windows

package sasinput

import "fmt"

// SendSecureAttentionSequence and EnsureServicesCanGenerateSAS are
// Windows-only concepts (Secure Attention Sequence exists only on the
// Windows Winlogon lock screen) -- see sasinput_windows.go. Non-Windows
// callers get a stable "unsupported" error rather than a missing symbol.
func SendSecureAttentionSequence() error {
	return fmt.Errorf("sasinput: Secure Attention Sequence is Windows-only")
}

func EnsureServicesCanGenerateSAS() error {
	return fmt.Errorf("sasinput: Secure Attention Sequence is Windows-only")
}
