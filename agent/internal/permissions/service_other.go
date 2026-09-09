//go:build !darwin && !linux && !windows

package permissions

type Service struct{}

func New() *Service                           { return &Service{} }
func (s *Service) AccessibilityGranted() bool { return true }
func (s *Service) ScreenRecordingGranted() bool {
	return true
}
func (s *Service) RequestAccessibility() bool { return true }
func (s *Service) RequestScreenRecording() bool {
	return true
}
func (s *Service) RequestMissing()                       {}
func (s *Service) OpenPrivacySettings() error            { return nil }
func (s *Service) OpenScreenRecordingSettings() error    { return nil }
func (s *Service) KMSCaptureGranted(binPath string) bool { return false }
func (s *Service) RequestKMSCapture(binPath string) bool { return false }

// ClipboardToolAvailable/RequestClipboardTool are Linux-only -- see
// service_linux.go.
func (s *Service) ClipboardToolAvailable() bool    { return true }
func (s *Service) RequestClipboardTool() bool      { return true }
func (s *Service) ClipboardInstallPreview() string { return "" }

// GPUClockLockSupported reports whether this platform can even attempt an
// NVML GPU clock lock at all -- see service_windows.go's own docs; every
// other platform's gamestream-server build has no --gpu-clock-lock-daemon
// mode to begin with.
func (s *Service) GPUClockLockSupported() bool { return false }

// GPUClockLockElevated always false off Windows -- see
// service_windows.go's IsProcessElevated docs for what this actually checks
// there.
func (s *Service) GPUClockLockElevated() bool { return false }

// RequestGPUClockLock is a no-op off Windows -- see service_windows.go's
// real implementation.
func (s *Service) RequestGPUClockLock(binPath string, watchPID int) error { return nil }

// KillGamestreamServerElevated is a no-op off Windows -- see
// service_windows.go's real implementation.
func (s *Service) KillGamestreamServerElevated() error { return nil }
