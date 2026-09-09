package controller

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"usbridge-client/internal/api"
	"usbridge-client/internal/gui/i18n"
	"usbridge-client/internal/gui/view"
	"usbridge-client/internal/media"
	"usbridge-client/internal/models"
	"usbridge-client/internal/service"

	"fyne.io/fyne/v2"
	"github.com/sirupsen/logrus"
)

// createInterface creates the widget's interface.
func (vw *VideoWidget) createInterface() {
	vw.touchpadWrapper = NewTouchpadWrapper(vw)
	vw.platformRegisterGestureTarget()
	vw.ui = view.NewVideoWidgetUI(vw.touchpadWrapper, nil, vw.handleStartVideo, vw.handleStopVideo, vw.handleFullscreen)
	vw.container = vw.ui.Container
	vw.videoCanvas = vw.ui.VideoCanvas
	vw.touchpadWrapper.SetImage(vw.videoCanvas)
	vw.statusLabel = vw.ui.StatusLabel
	vw.infoLabel = vw.ui.InfoLabel
	vw.statsLabel = vw.ui.StatsLabel
	vw.contentContainer = vw.ui.ContentContainer

	vw.startStatsLoop()
	vw.startRenderTicker()
	vw.updateButtons()
	vw.resetViewport()
}

// handleStartVideo handles video start.
func (vw *VideoWidget) handleStartVideo() {
	vw.userStoppedVideo.Store(false)
	vw.setDesiredStreaming(true)
	if !vw.beginVideoOperation() {
		// Another op (likely stop/reconcile) is running. Mark restart so the
		// coalesced reconcile will start video once the current op finishes.
		vw.videoOpMu.Lock()
		vw.videoRestartPending = true
		vw.videoOpMu.Unlock()
		return
	}
	go func() {
		defer vw.endVideoOperation()
		vw.debugLogSpinner("handleStartVideo-begin")

		if vw.usbClient == nil {
			logrus.Warn("⚠️ USB client is not initialized")
			fyne.Do(func() {
				vw.statusLabel.SetText(i18n.Current.ErrorNoConnection)
			})
			return
		}

		fyne.Do(func() {
			if vw.statusLabel != nil {
				vw.statusLabel.SetText(i18n.Current.VideoWaitingConnection)
			}
		})

		if vw.startDialog == nil {
			if vw.parentWindow == nil {
				logrus.Warn("⚠️ Parent window not set")
				fyne.Do(func() {
					vw.statusLabel.SetText(i18n.Current.ErrorWindowNotInit)
				})
				return
			}
			vw.startDialog = view.NewVideoStartDialog(vw.parentWindow)
			vw.startDialog.SetLiveCodecProvider(func() (string, bool) {
				if vw.videoClient == nil {
					return "", false
				}
				return vw.videoClient.NegotiatedVideoCodecName()
			})
		}

		preferredConfig, preferredErr := vw.resolvePreferredVideoConfig()
		preferredDevicePath := ""
		if preferredErr == nil {
			preferredDevicePath = preferredConfig.DevicePath
		}

		videoInfo := vw.fetchVideoInfoForStartDialog(preferredDevicePath)

		// Check whether the widget was closed while the HTTP requests were in flight
		if vw.isClosing.Load() {
			return
		}

		defaultWidth := 800
		defaultHeight := 600
		defaultFPS := 30
		defaultBitrate := "20M"
		// Prefer saved per-device config over generic Moonlight AppConfig defaults.
		if preferredErr == nil && preferredConfig.VideoWidth > 0 {
			defaultWidth = preferredConfig.VideoWidth
			defaultHeight = preferredConfig.VideoHeight
			defaultFPS = preferredConfig.VideoFPS
			if preferredConfig.VideoBitrate != "" {
				defaultBitrate = preferredConfig.VideoBitrate
			}
		} else if cfg := vw.videoClient.GetConfig(); cfg != nil {
			if cfg.VideoWidth > 0 {
				defaultWidth = cfg.VideoWidth
			}
			if cfg.VideoHeight > 0 {
				defaultHeight = cfg.VideoHeight
			}
			if cfg.VideoFPS > 0 {
				defaultFPS = cfg.VideoFPS
			}
			if cfg.VideoBitrate > 0 {
				defaultBitrate = fmt.Sprintf("%dK", cfg.VideoBitrate)
			}
		}
		// Override with live server params only when the server is actively streaming.
		// Without this guard the server's hard-coded config defaults (1280x720 @ 30fps)
		// would silently overwrite the saved per-device preferences when not streaming.
		if videoInfo != nil && videoInfo.Streaming {
			if videoInfo.Width > 0 {
				defaultWidth = videoInfo.Width
			}
			if videoInfo.Height > 0 {
				defaultHeight = videoInfo.Height
			}
			if videoInfo.FPS > 0 {
				defaultFPS = videoInfo.FPS
			}
			if videoInfo.Bitrate != "" {
				defaultBitrate = videoInfo.Bitrate
			}
		}

		fyne.Do(func() {
			vw.startDialog.Configure(videoInfo, defaultWidth, defaultHeight, defaultFPS, defaultBitrate)
			vw.startDialog.SetDeviceLabel("")
			vw.startDialog.SetPrimaryAction(i18n.Current.StartVideo)
			vw.startDialog.SetExtraAction("", nil)
			vw.debugLogSpinner("startDialog-shown")
			vw.startDialog.Show(func(request *models.VideoStartRequest) {
				vw.debugLogSpinner("startDialog-submitted")
				if vw.usbClient == nil {
					logrus.Warn("⚠️ video start dialog submitted but USB client is gone (disconnected)")
					return
				}
				if preferredDevicePath != "" {
					request.VideoDevice = preferredDevicePath
				}
				vw.handleVideoStartWithParams(request)
			})
			if vw.statusLabel != nil && !vw.isStreaming {
				vw.statusLabel.SetText("")
			}
		})
	}()
}

func (vw *VideoWidget) fetchVideoInfoForStartDialog(devicePath string) *models.VideoInfoData {
	const maxAttempts = 5

	var lastInfo *models.VideoInfoData
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		resp, err := vw.usbClient.GetVideoInfoForDevice(devicePath)
		if err != nil {
			logrus.Warnf("⚠️ Failed to get video info before opening start dialog (attempt %d/%d, device=%s): %v", attempt, maxAttempts, devicePath, err)
		} else if resp != nil && resp.Success && resp.Data != nil {
			parsed, parseErr := models.ParseVideoInfoData(resp.Data)
			if parseErr != nil {
				logrus.Warnf("⚠️ Failed to parse video info for start dialog (attempt %d/%d, device=%s): %v", attempt, maxAttempts, devicePath, parseErr)
			} else {
				lastInfo = parsed
				if len(parsed.CaptureModes) > 0 {
					logrus.Infof("✅ Video info for start dialog ready on attempt %d/%d: %d capture modes (device=%s)", attempt, maxAttempts, len(parsed.CaptureModes), devicePath)
					return parsed
				}
				logrus.Infof("ℹ️ Video info for start dialog attempt %d/%d returned no capture modes yet, retrying... (device=%s)", attempt, maxAttempts, devicePath)
			}
		}

		if attempt < maxAttempts {
			time.Sleep(200 * time.Millisecond)
		}
	}

	// For virtual/display devices (e.g. "display:N" used in Sunshine/Moonlight mode)
	// the server cannot run v4l2-ctl on the path and returns no capture_modes.
	// Fall back to the default device query to get the actual V4L2 capabilities.
	if devicePath != "" && (lastInfo == nil || len(lastInfo.CaptureModes) == 0) {
		logrus.Infof("ℹ️ No capture modes for device=%s, falling back to default device query", devicePath)
		fallback := vw.fetchVideoInfoForStartDialog("")
		if fallback != nil && len(fallback.CaptureModes) > 0 {
			if lastInfo != nil {
				// Preserve current status (width/height/fps/streaming) but inject capture modes
				lastInfo.CaptureModes = fallback.CaptureModes
				lastInfo.SupportedModes = fallback.SupportedModes
			} else {
				lastInfo = fallback
			}
		}
	}

	// Ensure the server's current streaming resolution is present in CaptureModes so
	// the dialog can pre-select it. For Sunshine the V4L2 fallback modes often only
	// go up to 720p while Sunshine itself streams 1080p or higher.
	if lastInfo != nil && lastInfo.Width > 0 && lastInfo.Height > 0 {
		found := false
		for _, cm := range lastInfo.CaptureModes {
			if cm.Width == lastInfo.Width && cm.Height == lastInfo.Height {
				found = true
				break
			}
		}
		if !found {
			fps := lastInfo.FPS
			if fps <= 0 {
				fps = 30
			}
			lastInfo.CaptureModes = append(lastInfo.CaptureModes, models.VideoCaptureMode{
				Width:       lastInfo.Width,
				Height:      lastInfo.Height,
				FPS:         []int{fps},
				PixelFormat: "YUYV",
			})
			logrus.Infof("ℹ️ Injected current resolution %dx%d@%dfps into capture modes for dialog pre-selection", lastInfo.Width, lastInfo.Height, fps)
		}
	}

	return lastInfo
}

// handleVideoStartWithParams handles video start with parameters from the dialog.
func (vw *VideoWidget) handleVideoStartWithParams(request *models.VideoStartRequest) {
	cfg := models.VideoDeviceConfig{
		DevicePath:         request.VideoDevice,
		VideoWidth:         request.VideoWidth,
		VideoHeight:        request.VideoHeight,
		VideoFPS:           request.VideoFPS,
		VideoQuality:       request.VideoQuality,
		VideoBitrate:       request.VideoBitrate,
		VideoMode:          request.VideoMode,
		CapturePixelFormat: request.CapturePixelFormat,
	}
	if err := vw.applyVideoDeviceConfig(cfg, true); err != nil {
		logrus.Warnf("⚠️ cannot start video from request: %v", err)
		fyne.Do(func() {
			vw.statusLabel.SetText(fmt.Sprintf("❌ %v", err))
		})
	}
}

func (vw *VideoWidget) startVideoWithParamsInternal(request *models.VideoStartRequest) {
	if vw.videoClient == nil {
		logrus.Warn("⚠️ Video client (Moonlight) is not initialized")
		fyne.Do(func() {
			vw.statusLabel.SetText(i18n.Current.VideoLaunchFailed)
		})
		return
	}

	if request != nil {
		if request.VideoWidth > 0 && request.VideoHeight > 0 {
			vw.videoClient.SetExpectedVideoSize(request.VideoWidth, request.VideoHeight)
			// Pre-set viewport dims from the requested resolution so absolute mouse mapping
			// is correct immediately — without waiting for the first decoded frame.
			// Needed when Vulkan/Metal is already active and frames arrive as nil, preventing
			// updateFrameContentRect from ever running (and triggering the fyne.Do update).
			newFW, newFH := float32(request.VideoWidth), float32(request.VideoHeight)
			fyne.Do(func() {
				if vw.lastVideoImgW != newFW || vw.lastVideoImgH != newFH {
					vw.lastVideoImgW = newFW
					vw.lastVideoImgH = newFH
					if tw := vw.activeViewportWrapper(); tw != nil {
						sz := tw.Size()
						if sz.Width > 0 && sz.Height > 0 {
							vw.UpdateTouchpadAndContentRect(sz.Width, sz.Height, nil)
						}
					}
				}
			})
		}
		if request.VideoMode != "" {
			vw.videoClient.SetVideoMode(request.VideoMode)
		}
		vw.videoClient.SetColor444(request.Color444)
		if request.VideoFPS > 0 {
			vw.videoClient.SetFPS(request.VideoFPS)
		}
		if kbps, ok := parseBitrateKbps(request.VideoBitrate); ok && kbps > 0 {
			vw.videoClient.SetBitrate(kbps)
		}
	}

	// Run HID auto-connect in parallel with Moonlight stream setup — independent subsystems.
	hidDone := make(chan error, 1)
	go func() {
		logrus.Debug("⌨️🖱️ [VIDEO] HID auto-connect running in parallel with Moonlight start...")
		hidDone <- vw.ensureControlHIDDevices()
	}()

	// The stream will be started via Moonlight's /launch API inside ConnectToMoonlight.
	if vw.usbClient != nil {
		fyne.Do(func() {
			if vw.statusLabel != nil {
				vw.statusLabel.SetText("⏳ Starting Moonlight...")
			}
		})
	}

	logrus.Info("🌕 startVideoWithParamsInternal: calling ConnectToMoonlight (Moonlight)")
	vw.debugLogSpinner("before-ConnectToMoonlight")
	// ConnectToMoonlight itself blocks for the entire negotiation --
	// classic Moonlight's LiStartConnection (RTSP/handshake) or, on web,
	// WebRTCVideoClient's ICE-gathering + /webrtc/offer POST/answer round
	// trip, plus up to 20 retries at 500ms apart on failure below -- and
	// only returns once that's done. beginVideoTrace (which normally
	// shows this spinner) doesn't run until *after* this returns
	// successfully, triggered by a later, asynchronous "connected"
	// state-change callback -- so without this, the entire blocking
	// window here had no visible feedback at all. Confirmed live via the
	// spinner debug HUD (?debug=1): a real web/WebRTC connection attempt
	// sat at exactly this checkpoint, spinner-less, for the whole
	// negotiation.
	vw.showConnectingSpinner()
	var connectErr error
	for attempt := 1; attempt <= 20; attempt++ {
		connectErr = vw.videoClient.ConnectToMoonlight()
		if connectErr == nil {
			break
		}
		logrus.Warnf("⚠️ Moonlight ConnectToMoonlight failed (attempt %d/20): %v", attempt, connectErr)
		// errors.Is(connectErr, service.ErrStreamerUnsupportedWebRTC): the
		// agent answered and is simply running Sunshine, which has no
		// WebRTC support at all -- retrying another 19 times 500ms apart
		// (originally here to give Sunshine time to bind its RTSP port on
		// a fresh launch) can't ever change that outcome, so stop
		// immediately instead of leaving the user staring at a blank
		// Control tab for ~10s before the same message finally appears.
		if errors.Is(connectErr, service.ErrStreamerUnsupportedWebRTC) {
			break
		}
		if attempt < 20 {
			time.Sleep(500 * time.Millisecond) // Give Sunshine time to bind port 47989
		}
	}

	if connectErr != nil {
		logrus.Errorf("❌ Moonlight ConnectToMoonlight ultimately failed: %v", connectErr)
		vw.hideConnectingSpinner()

		// Stop trying to reconnect, otherwise the reconcile loop will spam the server
		vw.videoOpMu.Lock()
		vw.desiredStreaming = false
		vw.videoOpMu.Unlock()

		go func() { <-hidDone }() // drain so the goroutine can exit
		fyne.Do(func() {
			if vw.statusLabel != nil {
				if errors.Is(connectErr, service.ErrStreamerUnsupportedWebRTC) {
					vw.statusLabel.SetText("❌ " + i18n.Current.ErrorSunshineNoWebRTC)
					return
				}
				vw.statusLabel.SetText(fmt.Sprintf("❌ %v", connectErr))
			}
		})
		return
	}
	// ConnectToMoonlight may have completed AFTER a disconnect was requested
	// (e.g. StopVideoSync timed out and called Disconnect concurrently).
	// If we're no longer supposed to be streaming, abort without marking
	// the session active — this prevents the Vulkan/Metal overlay from
	// appearing on the connection-manager screen.
	if !vw.desiredStreamingState() {
		logrus.Info("🛑 ConnectToMoonlight succeeded but streaming no longer desired — aborting session")
		vw.hideConnectingSpinner()
		go func() { <-hidDone }() // drain so the goroutine can exit
		go func() { _ = vw.videoClient.Disconnect() }()
		return
	}
	vw.isStreaming = true
	vw.isVideoConnected = true
	vw.debugLogSpinner("ConnectToMoonlight-succeeded")
	logrus.Info("✅ Moonlight stream started")

	// Re-check right after marking the session live: ConnectToMoonlight()
	// returns once LiStartConnection is merely *submitted* (see its own
	// "submitted... waiting for first frame" log), not once the underlying
	// C-level session is actually up — so the desiredStreamingState() check
	// above can pass and then the user presses stop in the gap before this
	// point. Before this second check, nothing ever re-evaluated
	// desiredStreaming against the now-true isStreaming: the reconcile that
	// coalesces after a mismatch could run while isStreaming was still
	// false (the race itself), see streaming==desired==false, and exit
	// without calling stopVideoInternal() — leaving this session's video
	// and (audibly) its audio running indefinitely with nothing left to
	// notice or clean it up.
	if !vw.desiredStreamingState() {
		logrus.Info("🛑 stream went live but streaming no longer desired (stop arrived mid-connect) — stopping immediately")
		go func() { <-hidDone }() // drain so the goroutine can exit
		vw.stopVideoInternal()
		return
	}

	// Wait for HID — non-fatal if it fails. checkMouseConnected() (periodic
	// Refresh()/touch-triggered) only ever *observes* connection state, it
	// never re-sends the registration request — so a failure here (e.g. the
	// agent still busy restarting Sunshine right after a capture-device
	// switch, see RestartSunshine) previously left the mouse dead until some
	// unrelated action (like a codec switch) happened to call
	// ensureControlHIDDevices() again. Retry it ourselves a few times instead
	// of requiring the user to stumble into that.
	if err := <-hidDone; err != nil {
		logrus.Errorf("❌ [VIDEO] HID auto-connect failed: %v", err)
		go vw.retryControlHIDDevices()
	}

	vw.updateStatus() // update header icon to show video-active state
	vw.ensureInputFocusAsync("stream-started", 300*time.Millisecond)
}

// handleStopVideo handles video stop.
func (vw *VideoWidget) handleStopVideo() {
	if vw.usbClient == nil {
		logrus.Warn("⚠️ USB client not initialized")
		fyne.Do(func() {
			vw.statusLabel.SetText(i18n.Current.ErrorNoConnection)
		})
		return
	}
	vw.userStoppedVideo.Store(true)
	vw.setDesiredStreaming(false)
	vw.scheduleVideoReconcile("handle-stop-video")
}

func (vw *VideoWidget) StopVideoSync() error {
	vw.userStoppedVideo.Store(true)
	vw.setDesiredStreaming(false)

	// Use a timeout for the synchronous operation so we don't hang the calling thread (lifecycle loop)
	done := make(chan struct{})
	go func() {
		vw.runVideoOpSync("stop-video-sync", func() {
			if vw.usbClient != nil {
				vw.stopVideoInternal()
			} else {
				// Client is already gone — clean up only the local state
				vw.isStreaming = false
				vw.isVideoConnected = false
				vw.isMouseConnected = false
				vw.clearVideo()
				fyne.Do(func() {
					if vw.statusLabel != nil {
						vw.statusLabel.SetText(i18n.Current.VideoStopped)
					}
					vw.updateButtons()
				})
				vw.updateStatus()
			}
		})
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-time.After(4 * time.Second):
		logrus.Warn("⚠️ StopVideoSync timed out, forcing local cleanup")
		vw.isStreaming = false
		vw.isVideoConnected = false
		if vw.videoClient != nil {
			_ = vw.videoClient.Disconnect()
		}
		// Destroy the native GPU overlay so it does not linger on the
		// connection-manager screen or conflict with the next session.
		vw.clearVideo()
		return fmt.Errorf("stop video timeout")
	}
}

func (vw *VideoWidget) stopVideoInternal() {
	logrus.Info("🛑 [VideoWidget] Internal stop starting...")

	fyne.Do(func() {
		if vw.statusLabel != nil {
			vw.statusLabel.SetText(i18n.Current.StoppingVideoCapture)
		}
	})
	resetVideoInfoCache()
	vw.hideConnectingSpinner()

	vw.isStreaming = false
	vw.isVideoConnected = false
	vw.isMouseConnected = false

	// Tear down the local overlay/canvas first, before touching the network at
	// all. clearVideo() destroys the native GPU overlay (Android's Vulkan
	// SurfaceView, macOS/Windows' Metal/D3D window) — a real OS-level surface
	// sitting outside Fyne's canvas, independent of whatever screen the app
	// navigates to next. It used to run only after waiting up to 1.5s for
	// Moonlight's network Disconnect() (LiStopConnection + Sunshine /cancel)
	// to finish, so pressing stop and immediately switching screens left that
	// overlay rendering on top of the new screen for up to that long. Nothing
	// here depends on the network op completing first.
	vw.resetViewport()
	vw.clearVideo()

	fyne.Do(func() {
		vw.updateButtons()
		if vw.statusLabel != nil {
			vw.statusLabel.SetText(i18n.Current.VideoStopped)
		}
	})

	vw.updateStatus()

	// Sunshine stops video streaming when the Moonlight session ends via /cancel
	// (Quit). The UI (canvas/overlay/status) is already cleared above, so this
	// doesn't block perceived responsiveness — but the actual wait DOES matter:
	// Disconnect() does real, load-bearing work synchronously (LiStopConnection,
	// then closing our local tsnet UDP proxy listener on Sunshine's control/video/
	// audio ports — see MoonlightService.Disconnect). A restart (reconcileVideoState
	// with restartPending) calls startVideoWithParamsInternal right after this
	// returns, which opens a *new* proxy listener on those same ports and issues a
	// fresh /launch. If Disconnect() hasn't actually finished — e.g. it's still
	// inside the up-to-2s ENet graceful-disconnect linger
	// (CONTROL_STREAM_LINGER_TIMEOUT_SEC in ControlStream.c) — the new listener's
	// bind() collides with the old one ("Only one usage of each socket address"),
	// Sunshine still thinks a session is running ("An app is already running on
	// this host"), and the two overlapping native sessions can race each other in
	// moonlight-common-c, corrupting stream state. A fixed 800ms sleep used to
	// stand in for this wait, which was fast but frequently too short. Bounded at
	// 3s (comfortably above the 2s ENet linger) as a safety net so a stuck
	// Disconnect() can't hang a restart forever.
	if vw.videoClient != nil {
		done := make(chan struct{})
		go func(client interface{ Disconnect() error }) {
			defer close(done)
			if err := client.Disconnect(); err != nil {
				logrus.Errorf("Failed to disconnect Moonlight: %v", err)
			}
		}(vw.videoClient)

		logrus.Info("⏳ Waiting for Moonlight disconnect (LiStopConnection + local proxy teardown) to complete...")
		select {
		case <-done:
			logrus.Info("✅ [VideoWidget] Moonlight disconnect completed")
		case <-time.After(3 * time.Second):
			logrus.Warn("⚠️ [VideoWidget] Moonlight disconnect did not complete within 3s, proceeding anyway")
		}
	} else {
		// No client to disconnect — still give Sunshine a moment to settle
		// (Python test "standard": wait 0.8s, like test_api_full.py).
		time.Sleep(800 * time.Millisecond)
	}

	logrus.Info("✅ [VideoWidget] Internal stop complete")
}

func isConnectedStorageDevice(device models.DeviceInfo) bool {
	if device.Status != "connected" {
		return false
	}

	switch {
	case device.Type == "local":
		return true
	case device.Type == "mtp":
		return true
	case device.Type == "nbd":
		return true
	case strings.HasPrefix(device.Device, "disk:"):
		return true
	case strings.HasPrefix(device.Device, "drive"):
		return true
	case strings.HasPrefix(device.Device, "mtp"):
		return true
	}

	return false
}

// retryControlHIDDevices re-attempts ensureControlHIDDevices a few times
// after the initial parallel attempt in startVideoWithParamsInternal failed
// (most commonly a timeout because the agent was still mid-restart from a
// capture-device switch, see RestartSunshine's WaitReady). Bails out early
// if the session moved on (stopped, or a newer stream superseded this one)
// so it can't fight a later, legitimate device-connect attempt.
func (vw *VideoWidget) retryControlHIDDevices() {
	delays := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	for _, d := range delays {
		time.Sleep(d)
		if vw.isClosing.Load() || !vw.desiredStreamingState() {
			return
		}
		if err := vw.ensureControlHIDDevices(); err != nil {
			logrus.Warnf("⌨️🖱️ [VIDEO] HID auto-connect retry failed: %v", err)
			continue
		}
		logrus.Info("⌨️🖱️ [VIDEO] HID auto-connect retry succeeded")
		return
	}
	logrus.Error("⌨️🖱️ [VIDEO] HID auto-connect: all retries exhausted, mouse/keyboard may be unresponsive until next reconnect")
}

func (vw *VideoWidget) ensureControlHIDDevices() error {
	// Capture usbClient once. The field can be set to nil concurrently by
	// UpdateClient(nil) during disconnect, so all subsequent uses must go
	// through this local variable to avoid nil-pointer panics in the loop.
	client := vw.usbClient
	if client == nil {
		return nil
	}

	deviceInfo, err := client.GetDeviceInfo()
	if err != nil {
		return fmt.Errorf("failed to get device info before HID auto-connect: %w", err)
	}
	vw.SetAgentEnvironment(deviceInfo.AgentOS, deviceInfo.AgentDisplay)
	if deviceInfo.MountInProgress {
		logrus.Infof("⌨️🖱️ Control HID auto-connect skipped: gadget reconfiguration already in progress (desired=%s)", vw.GetMouseInputMode())
		return nil
	}

	keyboardConnected := false
	mouseConnected := false
	mouseModeMatches := false
	storageConnected := false
	xinputGamepadConnected := false
	desiredMouseType := vw.GetMouseInputMode()
	// The client-facing mode ("cursor", "gyro", ...) and the wire/device type
	// the agent actually reports back are different vocabularies — virtual
	// cursor and gyro modes are client-only concepts that always register as
	// "absolute" on the gadget/bridge (see mouseTransportType). Comparing
	// observedMode straight against desiredMouseType here always failed for
	// those modes (Android's default), so the device never looked "ready"
	// and this function retried/timed out forever.
	desiredTransportType := mouseTransportType(desiredMouseType)

	for _, device := range deviceInfo.Devices {
		if device.Status != "connected" {
			continue
		}

		switch {
		case device.Type == "keyboard" || strings.HasPrefix(device.Type, "keyboard:"):
			keyboardConnected = true
		case isMouseDeviceType(device.Type):
			mouseConnected = true
			observedMode := mouseModeFromDeviceType(device.Type)
			vw.setObservedMouseMode(observedMode)
			mouseModeMatches = observedMode == desiredTransportType
		case IsGamepadXInputDeviceType(device.Type):
			xinputGamepadConnected = true
		}

		if isConnectedStorageDevice(device) {
			storageConnected = true
		}
	}

	if storageConnected {
		logrus.Info("💿 Control HID auto-connect skipped: storage devices are connected, avoiding gadget reconfiguration")
		return nil
	}

	if xinputGamepadConnected {
		logrus.Info("🎮 Control HID auto-connect skipped: XInput gamepad connected — keyboard/mouse share incompatible with XInput composite")
		return nil
	}

	var requests models.DeviceStartBatchRequest
	needKeyboard := !keyboardConnected
	needMouse := !mouseConnected || !mouseModeMatches
	if needKeyboard || needMouse {
		requests = append(requests, newKeyboardStartRequest())
		requests = append(requests, newMouseStartRequest(desiredMouseType))
		logrus.Infof("⌨️🖱️ Control HID auto-connect: desired=%q keyboard_connected=%v mouse_connected=%v mode_matches=%v", desiredMouseType, keyboardConnected, mouseConnected, mouseModeMatches)
	}

	if len(requests) == 0 {
		logrus.Info("⌨️🖱️ Control HID auto-connect: keyboard and mouse already connected")
		return nil
	}

	logrus.Infof("⌨️🖱️ Control HID auto-connect: starting %d missing HID device(s)", len(requests))
	if _, err := executeDeviceBatch(client, client.StartDevicesBatchWithMerge, requests, true); err != nil {
		return fmt.Errorf("failed to auto-connect HID devices: %w", err)
	}

	hidTimer := time.NewTimer(5 * time.Second)
	hidTicker := time.NewTicker(250 * time.Millisecond)
	defer hidTimer.Stop()
	defer hidTicker.Stop()

hidWaitLoop:
	for {
		select {
		case <-hidTimer.C:
			return fmt.Errorf("timed out waiting for HID devices after auto-connect")
		case <-hidTicker.C:
			if vw.isClosing.Load() || vw.usbClient == nil {
				return fmt.Errorf("disconnected during HID wait")
			}
			info, err := client.GetDeviceInfo()
			if err != nil {
				continue hidWaitLoop
			}

			keyboardReady := false
			mouseReady := false
			for _, device := range info.Devices {
				if device.Status != "connected" {
					continue
				}
				if device.Type == "keyboard" || strings.HasPrefix(device.Type, "keyboard:") {
					keyboardReady = true
				}
				if isMouseDeviceType(device.Type) {
					observedMode := mouseModeFromDeviceType(device.Type)
					vw.setObservedMouseMode(observedMode)
					mouseReady = observedMode == desiredTransportType
				}
			}

			if keyboardReady && mouseReady {
				logrus.Info("✅ Control HID auto-connect completed and devices are visible in device/info")
				return nil
			}
		}
	}
}

func (vw *VideoWidget) controlHIDReady() (bool, error) {
	client := vw.usbClient
	if client == nil {
		return false, nil
	}

	deviceInfo, err := client.GetDeviceInfo()
	if err != nil {
		return false, err
	}
	if deviceInfo.MountInProgress {
		return false, nil
	}

	keyboardConnected := false
	mouseConnected := false
	mouseModeMatches := false
	desiredMouseType := vw.GetMouseInputMode()
	// See the identical comment in ensureControlHIDDevices: compare against
	// the wire transport type, not the raw client-facing mode name.
	desiredTransportType := mouseTransportType(desiredMouseType)

	for _, device := range deviceInfo.Devices {
		if device.Status != "connected" {
			continue
		}
		switch {
		case device.Type == "keyboard" || strings.HasPrefix(device.Type, "keyboard:"):
			keyboardConnected = true
		case isMouseDeviceType(device.Type):
			mouseConnected = true
			observedMode := mouseModeFromDeviceType(device.Type)
			vw.setObservedMouseMode(observedMode)
			mouseModeMatches = observedMode == desiredTransportType
		}
	}

	return keyboardConnected && mouseConnected && mouseModeMatches, nil
}

func (vw *VideoWidget) BootstrapControlSessionAsync() {
	if vw.userStoppedVideo.Load() {
		// The user explicitly pressed stop; this call is one of
		// scheduleControlBootstrap's timers (main_window_lifecycle.go), which
		// fire on a schedule tied to which tab is visible, not to user intent
		// — a stop can land in the window between them, and an already-queued
		// timer would otherwise silently reconnect video/audio moments later.
		logrus.Info("🎬 control-bootstrap skipped — user explicitly stopped video")
		return
	}
	vw.setDesiredStreaming(true)
	vw.scheduleVideoReconcile("control-bootstrap")
}

// updateButtons updates the button state.
func (vw *VideoWidget) updateButtons() {
	if vw.onFPSChanged != nil && !vw.isStreaming {
		vw.onFPSChanged(0)
	}
}

// Refresh updates the widget.
func (vw *VideoWidget) Refresh() {
	if vw.isClosing.Load() {
		return
	}
	if vw.usbClient == nil {
		logrus.Debug("USB client is not initialized, skipping video refresh")
		fyne.Do(func() {
			vw.infoLabel.SetText(i18n.Current.VideoWaitingConnection)
		})
		return
	}

	vw.checkMouseConnected()

	videoInfo, err := vw.usbClient.GetVideoInfo()
	if err != nil {
		logrus.Errorf("Failed to get video information: %v", err)
		fyne.Do(func() {
			vw.infoLabel.SetText(i18n.Current.ErrorVideoInfo)
		})
		return
	}

	fyne.Do(func() {
		if videoInfo.Success && videoInfo.Data != nil {
			vw.infoLabel.SetText(i18n.Current.VideoInfoReceived)
		} else {
			vw.infoLabel.SetText(i18n.Current.VideoInfoUnavailable)
		}
	})

}

// checkMouseConnected checks whether the mouse is connected.
func (vw *VideoWidget) checkMouseConnected() {
	if vw.isClosing.Load() || vw.usbClient == nil {
		if vw.usbClient == nil {
			logrus.Debug("🖱️ checkMouseConnected: USB client is not initialized")
		}
		vw.isMouseConnected = false
		return
	}

	deviceInfo, err := vw.usbClient.GetDeviceInfo()
	if err != nil {
		logrus.Infof("🖱️ Failed to get device information: %v", err)
		vw.isMouseConnected = false
		vw.refreshCursorOverlay()
		return
	}
	vw.SetAgentEnvironment(deviceInfo.AgentOS, deviceInfo.AgentDisplay)

	logrus.Debugf("🖱️ checkMouseConnected: received %d devices", len(deviceInfo.Devices))

	mouseConnected := false
	for _, device := range deviceInfo.Devices {
		logrus.Debugf("🖱️ Inspecting device: type=%s, status=%s, name=%s", device.Type, device.Status, device.Name)
		if device.Status == "connected" && isMouseDeviceType(device.Type) {
			mouseConnected = true
			vw.setObservedMouseMode(mouseModeFromDeviceType(device.Type))
			logrus.Infof("🖱️ ✅ Pointer device connected: %s (type: %s)", device.Name, device.Type)
			break
		}
	}
	if !mouseConnected {
		vw.setObservedMouseMode("")
	}

	logrus.Debugf("🖱️ checkMouseConnected: mouseConnected=%v (previously %v)", mouseConnected, vw.isMouseConnected)

	if vw.isMouseConnected != mouseConnected {
		vw.isMouseConnected = mouseConnected
		if mouseConnected {
			logrus.Info("🖱️ Touchpad activated: pointer device connected (Moonlight)")
			vw.startDesktopMousePolling()
		} else {
			logrus.Info("🖱️ Touchpad deactivated: pointer device disconnected")
			vw.stopDesktopMousePolling()
			fyne.Do(func() {
				if vw.statusLabel != nil {
					vw.statusLabel.SetText("")
				}
			})
		}
	}
}

// handleVideoFrame handles a received video frame.
func (vw *VideoWidget) handleVideoFrame(frame image.Image) {
	defer func() {
		if r := recover(); r != nil {
			logrus.Errorf("🔥 PANIC in handleVideoFrame: %v", r)
		}
	}()

	// frame is nil when the native GPU overlay (Metal/GL) is active and has
	// already received the frame at the C level. We still update counters so
	// the Go-level FPS display and trace logging stay accurate.
	if frame == nil {
		vw.frameMutex.Lock()
		vw.frameCount++
		frameNum := vw.frameCount
		vw.lastFrameTime = time.Now()
		vw.frameMutex.Unlock()
		vw.frameDecoder.IncrementFrameCount()
		vw.noteVideoTraceFirstFrame(frameNum)
		// Log FPS for Metal path (frame=nil means VT→Metal bypasses Go image).
		if frameNum%60 == 0 {
			now := time.Now().UnixNano()
			prev := vw.fpsWindowStart.Swap(now)
			if prev > 0 {
				elapsed := time.Duration(now - prev)
				measuredFPS := float64(60) / elapsed.Seconds()
				configuredFPS := 0
				if vw.videoClient != nil {
					if cfg := vw.videoClient.GetConfig(); cfg != nil {
						configuredFPS = cfg.VideoFPS
					}
				}
				if configuredFPS > 0 && measuredFPS < float64(configuredFPS)*0.75 {
					logrus.Warnf("⚠️ [FPS] delivery=%.1f fps configured=%d fps (Metal path) — Sunshine sending less than requested.", measuredFPS, configuredFPS)
				} else {
					logrus.Infof("📊 [VIDEO FPS] Metal callback: %.1f fps (frame=%d configured=%d)", measuredFPS, frameNum, configuredFPS)
				}
			}
		}
		return
	}

	// Normalise to *image.RGBA (all HW decoders already produce this type).
	var rgba *image.RGBA
	if r, ok := frame.(*image.RGBA); ok {
		rgba = r
	} else {
		b := frame.Bounds()
		rgba = image.NewRGBA(b)
		draw.Draw(rgba, b, frame, b.Min, draw.Src)
	}

	vw.frameMutex.Lock()
	vw.frameCount++
	frameNum := vw.frameCount
	vw.lastFrameTime = time.Now()
	vw.frameMutex.Unlock()

	// When the native GPU overlay is already active, skip Fyne canvas update.
	if !vw.isNativeVideoActive() {
		// Delay Fyne canvas rendering by 30 frames (~0.5s) to allow the native
		// overlay (Vulkan/Metal) to initialize. This prevents a "double start" flash
		// where Fyne draws the first few frames before the native window maps on top.
		if frameNum > 30 {
			// Atomic store — render ticker picks this up at next 60Hz tick.
			vw.pendingFrame.Store(rgba)
		}
	}

	if frameNum <= 10 || frameNum%120 == 0 {
		vw.updateFrameContentRect(rgba)
	}

	vw.frameDecoder.IncrementFrameCount()
	vw.noteVideoTraceFirstFrame(frameNum)

	// Log Go-level frame arrival FPS every 60 frames (≈2s at 30fps, ≈1s at 60fps).
	if frameNum%60 == 0 {
		now := time.Now().UnixNano()
		prev := vw.fpsWindowStart.Swap(now)
		if prev > 0 {
			elapsed := time.Duration(now - prev)
			measuredFPS := float64(60) / elapsed.Seconds()
			configuredFPS := 0
			if vw.videoClient != nil {
				if cfg := vw.videoClient.GetConfig(); cfg != nil {
					configuredFPS = cfg.VideoFPS
				}
			}
			if configuredFPS > 0 && measuredFPS < float64(configuredFPS)*0.75 {
				logrus.Warnf("⚠️ [FPS] delivery=%.1f fps configured=%d fps — Sunshine sending less than requested. Causes: V4L2 source limited to 30fps, or /resume ignores fps param. Set matching fps in UI.", measuredFPS, configuredFPS)
			} else {
				logrus.Infof("📊 [VIDEO FPS] Go callback: %.1f fps (frame=%d configured=%d metal=%v)", measuredFPS, frameNum, configuredFPS, vw.isNativeVideoActive())
			}
		}
	}

	if frameNum == 1 {
		bounds := rgba.Bounds()
		logrus.Infof("✅ [VIDEO] first frame reached client trace=%s frame=%d size=%dx%d", vw.currentVideoTraceLabel(), frameNum, bounds.Dx(), bounds.Dy())
		vw.dumpFrameSnapshot(rgba, frameNum)
		// Start native GPU overlay only when we still want to be streaming.
		// Frames can arrive from C callbacks even after LiStopConnection is called,
		// so guard against starting the overlay on the connection-manager screen.
		if !vw.isClosing.Load() && vw.isStreaming {
			go vw.startMetalVideoOnWindow(vw.parentWindow, false)
		}
	}
	if frameNum <= 5 || frameNum%300 == 0 {
		bounds := rgba.Bounds()
		logrus.Infof("🖼️ [VIDEO] client frame trace=%s frame=%d size=%dx%d stats=%s", vw.currentVideoTraceLabel(), frameNum, bounds.Dx(), bounds.Dy(), summarizeImage(rgba))
	}
	// Render is driven by the 60 Hz ticker — no scheduleFrameRender() call needed here.
}

// handleFullscreen handles switching to fullscreen mode.
func (vw *VideoWidget) handleFullscreen() {
	vw.ShowFullscreen()
}

func (vw *VideoWidget) ShowFullscreen() {
	if vw.fullscreenDialog == nil {
		if vw.parentWindow == nil {
			logrus.Warn("⚠️ Parent window is not set")
			return
		}
		vw.fullscreenDialog = NewFullscreenDialog(vw.parentWindow)
		vw.fullscreenDialog.SetVideoWidget(vw)
		vw.fullscreenDialog.SetVideoClient(vw.videoClient)
	}

	vw.fullscreenDialog.Show()
}

// HandleVirtualKeyboard handles opening/closing the virtual keyboard.
func (vw *VideoWidget) HandleVirtualKeyboard() {
	vw.platformHandleVirtualKeyboard()
}

// updateStats updates statistics.
func (vw *VideoWidget) updateStats() {
	vw.frameMutex.RLock()
	lastFrameTime := vw.lastFrameTime
	vw.frameMutex.RUnlock()

	decoderStats := vw.frameDecoder.GetFrameStats()
	fps := decoderStats["fps"].(float64)

	// When Metal overlay is active, VT frames bypass the Go decoder entirely,
	// so decoderStats.fps is always 0. Read the actual render FPS from Metal instead.
	if vw.isNativeVideoActive() {
		if metalFPS := vw.getNativeFPS(); metalFPS > 0 {
			fps = metalFPS
		}
	}

	stats := fmt.Sprintf("FPS: %.1f | %s", fps, lastFrameTime.Format("15:04:05"))
	vw.statsLabel.SetText(stats)
	if vw.onFPSChanged != nil {
		vw.onFPSChanged(math.Round(fps*10) / 10)
	}
	// Keep Metal overlay aligned with the video widget (handles window resizes).
	vw.updateMetalVideoFrame()
}

// SetParentWindow sets the parent window for dialogs.
func (vw *VideoWidget) SetParentWindow(window fyne.Window) {
	vw.parentWindow = window

	vw.touchpadWrapper.SetKeyHandlers(vw.handlePhysicalKeyDown, vw.handlePhysicalKeyUp, vw.handlePhysicalKeyPress, vw.handlePhysicalRunePress)
	vw.touchpadWrapper.SetWindowForFocus(window)
	vw.touchpadWrapper.SetWindowFocusTarget(nil)
	vw.ensureInputFocusAsync("set-parent-window", 150*time.Millisecond)
}

func (vw *VideoWidget) ensureInputFocusAsync(reason string, delay time.Duration) {
	if vw.parentWindow == nil || vw.touchpadWrapper == nil || fyne.CurrentDevice().IsMobile() {
		return
	}
	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		fyne.Do(func() {
			if vw.parentWindow == nil || vw.touchpadWrapper == nil {
				return
			}
			logrus.Infof("⌨️ [WINDOW][FOCUS] requesting focus reason=%s current=%T", reason, vw.parentWindow.Canvas().Focused())
			vw.parentWindow.RequestFocus()
			vw.parentWindow.Canvas().Focus(vw.touchpadWrapper)
			logrus.Infof("⌨️ [WINDOW][FOCUS] focused=%T reason=%s", vw.parentWindow.Canvas().Focused(), reason)
		})
	}()
}

func (vw *VideoWidget) SetOnFPSChanged(fn func(float64)) {
	vw.onFPSChanged = fn
}

// UpdateClient updates the USB client.
func (vw *VideoWidget) UpdateClient(usbClient *api.USBClient) {
	vw.usbClient = usbClient
	if usbClient != nil {
		vw.isClosing.Store(false)
		vw.userStoppedVideo.Store(false)
		usbClient.SetCursorUpdateHandler(vw.handleRemoteCursorUpdate)
	}
	vw.updateButtons()
}

func (vw *VideoWidget) SetTailscaleService(ts *service.TailscaleService) {
	vw.tailscaleService = ts
	vw.tailscaleVideoEnabled = ts != nil
}

func (vw *VideoWidget) SetTailscaleVideoEnabled(enabled bool) {
	vw.tailscaleVideoEnabled = enabled
}

// SetBridgeInternalHost stores the bridge's LAN/internal IP so the video
// transport can prefer a direct LAN path over the Tailscale DERP relay.
func (vw *VideoWidget) SetBridgeInternalHost(host string) {
	vw.bridgeInternalHost = strings.TrimSpace(host)
}

// GetContainer returns the widget's container.
func (vw *VideoWidget) GetContainer() *fyne.Container {
	return vw.container
}

// IsStreaming returns the capture state.
func (vw *VideoWidget) IsStreaming() bool {
	return vw.isStreaming
}

// SetStreaming sets the capture state.
func (vw *VideoWidget) SetStreaming(streaming bool) {
	vw.isStreaming = streaming
	vw.updateButtons()
	if streaming {
		vw.ensureInputFocusAsync("set-streaming", 300*time.Millisecond)
	}
}

// StopVideo stops the video stream via the widget's public API.
func (vw *VideoWidget) StopVideo() {
	if vw.usbClient == nil {
		return
	}
	vw.runVideoOpSync("stop-video-sync", func() {
		vw.stopVideoInternal()
	})
}

// HandleConnectionLost stops local video/input resources without contacting the server.
func (vw *VideoWidget) HandleConnectionLost() {
	resetVideoInfoCache()

	if vw.videoClient != nil {
		if err := vw.videoClient.Disconnect(); err != nil {
			logrus.Warnf("⚠️ Failed to disconnect video client after transport loss: %v", err)
		}
	}

	vw.isStreaming = false
	vw.isVideoConnected = false
	vw.isMouseConnected = false
	vw.hideConnectingSpinner()
	vw.clearVideo()

	fyne.Do(func() {
		vw.updateButtons()
		if vw.statusLabel != nil {
			vw.statusLabel.SetText(i18n.Current.ErrorNoConnection)
		}
	})

	vw.updateStatus()
}

func (vw *VideoWidget) handleDeviceRebuildLocally() {
	resetVideoInfoCache()
	logrus.Infof("🔄 [VideoTrace #%d] local control rebuild reset", vw.videoTraceID.Load())

	vw.stopDesktopMousePolling()
	if vw.videoClient != nil {
		if err := vw.videoClient.Disconnect(); err != nil {
			logrus.Warnf("⚠️ Failed to disconnect Moonlight after device rebuild: %v", err)
		}
	}

	vw.isStreaming = false
	vw.isVideoConnected = false
	vw.isMouseConnected = false
	vw.clearVideo()

	fyne.Do(func() {
		vw.updateButtons()
		if vw.statusLabel != nil {
			vw.statusLabel.SetText(i18n.Current.VideoWaitingConnection)
		}
	})
}

// ExitFullscreenIfNeeded closes fullscreen mode if it is active.
func (vw *VideoWidget) ExitFullscreenIfNeeded() bool {
	if vw.fullscreenDialog == nil || !vw.fullscreenDialog.IsFullscreen() {
		return false
	}
	vw.fullscreenDialog.exitFullscreen()
	return true
}

// clearVideo clears the video.
func (vw *VideoWidget) clearVideo() {
	vw.clearVideoMu.Lock()
	defer vw.clearVideoMu.Unlock()

	vw.frameMutex.Lock()
	lastFrame := vw.currentFrame // saved for darkened pause display (Fyne canvas path)
	vw.currentFrame = nil
	vw.frameCount = 0
	vw.lastFrameTime = time.Time{}
	vw.frameContentX = 0
	vw.frameContentY = 0
	vw.frameContentW = 0
	vw.frameContentH = 0
	vw.frameMutex.Unlock()
	// Keep lastVideoImgW/H — aspect ratio is still valid for the same stream config.

	// When Metal was active, currentFrame is nil (VT frames bypass Go entirely).
	// Capture the last rendered frame from Metal before destroying the overlay.
	// Must use a typed check to avoid the Go typed-nil-interface pitfall:
	// getMetalLastFrame returns (*image.RGBA)(nil) on non-darwin/no-frame,
	// which would make lastFrame non-nil as an image.Image and crash Fyne.
	if lastFrame == nil {
		if f := vw.getMetalLastFrame(); f != nil {
			lastFrame = f
		}
	}
	vw.stopMetalVideo()
	vw.frameDecoder.Reset()
	vw.pendingFrame.Store(nil)
	vw.frameRenderScheduled.Store(false)

	fyne.Do(func() {
		vw.UpdateCursorOverlayPointer(0, 0, false)
		if vw.videoCanvas != nil {
			if lastFrame != nil {
				// Show last frame darkened to indicate stream stopped.
				vw.videoCanvas.Image = lastFrame
				vw.videoCanvas.Translucency = 0.55
			} else {
				vw.videoCanvas.Resource = nil
				vw.videoCanvas.Image = nil
				vw.videoCanvas.Translucency = 0
			}
			vw.videoCanvas.Refresh()
		}
		if vw.touchpadWrapper != nil {
			vw.touchpadWrapper.Refresh()
		}
		if vw.container != nil {
			vw.container.Refresh()
		}
	})
	vw.lastUIFrameRenderAt.Store(0)
	vw.forceCanvasRefresh.Store(true)
	logrus.Infof("🧹 [VideoTrace #%d] video canvas cleared", vw.videoTraceID.Load())
}

// GetCurrentFrame returns the current frame for fullscreen mode.
func (vw *VideoWidget) GetCurrentFrame() image.Image {
	vw.frameMutex.RLock()
	defer vw.frameMutex.RUnlock()
	return vw.currentFrame
}

// GetFrameDecoder returns the frame decoder for fullscreen mode.
func (vw *VideoWidget) GetFrameDecoder() *media.FrameDecoder {
	return vw.frameDecoder
}

func (vw *VideoWidget) startStatsLoop() {
	if vw.statsTickerStop != nil {
		close(vw.statsTickerStop)
	}
	vw.statsTickerStop = make(chan struct{})

	go func(stop <-chan struct{}) {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if vw.IsStreaming() {
					vw.checkVideoSilence()
				}
				fyne.Do(func() {
					if vw.IsStreaming() {
						vw.updateStats()
					}
				})
			case <-stop:
				return
			}
		}
	}(vw.statsTickerStop)
}

func (vw *VideoWidget) beginVideoOperation() bool {
	vw.videoOpMu.Lock()
	defer vw.videoOpMu.Unlock()
	if vw.videoOpRunning {
		return false
	}
	vw.videoOpRunning = true
	return true
}

func (vw *VideoWidget) endVideoOperation() {
	vw.videoOpMu.Lock()
	vw.videoOpRunning = false
	desiredStreaming := vw.desiredStreaming
	streaming := vw.isStreaming
	restartPending := vw.videoRestartPending
	vw.videoOpMu.Unlock()

	if restartPending || desiredStreaming != streaming {
		vw.scheduleVideoReconcile("end-video-operation")
	}
}

func (vw *VideoWidget) finishVideoOperation() {
	vw.videoOpMu.Lock()
	vw.videoOpRunning = false
	vw.videoOpMu.Unlock()
}

func (vw *VideoWidget) scheduleFrameRender() {
	if !vw.frameRenderScheduled.CompareAndSwap(false, true) {
		return
	}
	fyne.Do(func() {
		defer func() {
			vw.frameRenderScheduled.Store(false)
			if r := recover(); r != nil {
				logrus.Errorf("🔥 PANIC in scheduleFrameRender/fyne.Do: %v", r)
			}
		}()
		vw.renderLatestFrame()
	})
}

// startRenderTicker starts a render goroutine at the given FPS (default 60).
// Decoding and display are fully decoupled: the decoder stores frames via
// pendingFrame.Store; the ticker picks up the latest one each display cycle.
// Call with the stream's configured FPS so the tick interval matches the source.
func (vw *VideoWidget) startRenderTicker(fps ...int) {
	targetFPS := 60
	if len(fps) > 0 && fps[0] > 0 {
		targetFPS = fps[0]
	}
	if vw.renderTickerStop != nil {
		close(vw.renderTickerStop)
	}
	stop := make(chan struct{})
	vw.renderTickerStop = stop

	go func() {
		ticker := time.NewTicker(time.Second / time.Duration(targetFPS))
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if vw.isClosing.Load() {
					return
				}
				// When the native GPU overlay is active the decoder does not store
				// frames in pendingFrame (C handles them directly). Always fire so
				// that viewport and cursor updates reach the Vulkan layer once per
				// display frame — preventing the cursor from appearing at two
				// different positions in different swapchain images.
				if !vw.isNativeVideoActive() &&
					vw.pendingFrame.Load() == nil &&
					!vw.forceCanvasRefresh.Load() {
					continue
				}
				vw.scheduleFrameRender()
			case <-stop:
				return
			}
		}
	}()
}

func (vw *VideoWidget) renderLatestFrame() {
	defer func() {
		if r := recover(); r != nil {
			logrus.Errorf("🔥 PANIC in renderLatestFrame: %v", r)
		}
	}()

	// Atomically take the latest decoded frame (no copy — just pointer swap).
	pending := vw.pendingFrame.Swap(nil)

	vw.frameMutex.Lock()
	if pending != nil {
		vw.currentFrame = pending // transfer ownership to currentFrame
	}
	frame := vw.currentFrame
	frameNum := vw.frameCount
	vw.frameMutex.Unlock()

	// If native GPU overlay is active, frame will be nil, but we still need to proceed
	// to hide the Fyne canvas and update viewport/cursor.
	if frame == nil && !vw.isNativeVideoActive() {
		return
	}

	mainWindowVisible := vw.fullscreenDialog == nil || !vw.fullscreenDialog.IsFullscreen()
	needsFullRefresh := vw.forceCanvasRefresh.Swap(false)

	// Native GPU overlay path — update viewport and cursor every render tick
	// regardless of whether the main window is visible. On Android the Vulkan
	// SurfaceView persists through fullscreen transitions, so cursor and viewport
	// state must reach the C layer even while the fullscreen dialog is showing.
	// Without this, cursor positions set from touch events via updateNativeViewportAndCursor
	// would never be applied in fullscreen (mainWindowVisible == false), causing the
	// Vulkan cursor to appear frozen and then teleport on finger-lift.
	if vw.isNativeVideoActive() {
		if mainWindowVisible && vw.videoCanvas != nil && vw.videoCanvas.Translucency < 1.0 {
			logrus.Infof("🕶️ [VIDEO] hardware overlay active, hiding Fyne canvas")
			vw.videoCanvas.Translucency = 1.0
			vw.videoCanvas.Refresh()
		}
		vw.updateMetalVideoFrame()
		vw.frameRenderScheduled.Store(false)
		return
	}

	// Non-native (Fyne canvas) path.
	if mainWindowVisible && vw.videoCanvas != nil {
		if frameNum <= 5 || frameNum%300 == 0 {
			logrus.Infof("🪟 [VIDEO] canvas render trace=%s frame=%d stats=%s", vw.currentVideoTraceLabel(), frameNum, summarizeImage(frame))
		}
		if vw.videoCanvas.Image != frame || vw.videoCanvas.Translucency != 0 {
			vw.videoCanvas.Image = frame
			vw.videoCanvas.Translucency = 0 // clear darkening from paused state
		}
		vw.videoCanvas.Refresh()
	}
	// touchpadWrapper.Refresh() removed from per-frame hot path;
	// cursor updates call Refresh() directly via UpdateCursorOverlayPointer.
	if mainWindowVisible && (frameNum == 1 || needsFullRefresh) {
		vw.noteVideoTraceFirstPaint(frameNum)
		if vw.contentContainer != nil {
			vw.contentContainer.Refresh()
		}
		if vw.container != nil {
			vw.container.Refresh()
		}
		if vw.parentWindow != nil {
			if content := vw.parentWindow.Content(); content != nil {
				content.Refresh()
			}
		}
	}
	vw.lastUIFrameRenderAt.Store(time.Now().UnixNano())
	vw.frameRenderScheduled.Store(false)
	// No hasNewerFrame re-schedule — the 60 Hz ticker picks up the next frame automatically.
}

func summarizeImage(img image.Image) string {
	if img == nil {
		return "none"
	}
	bounds := img.Bounds()
	if bounds.Dx() == 0 || bounds.Dy() == 0 {
		return "empty"
	}

	points := []image.Point{
		{X: bounds.Min.X, Y: bounds.Min.Y},
		{X: bounds.Min.X + bounds.Dx()/2, Y: bounds.Min.Y + bounds.Dy()/2},
		{X: bounds.Max.X - 1, Y: bounds.Min.Y},
		{X: bounds.Min.X, Y: bounds.Max.Y - 1},
		{X: bounds.Max.X - 1, Y: bounds.Max.Y - 1},
	}

	samples := make([]string, 0, len(points))
	minR, minG, minB, minA := 255, 255, 255, 255
	maxR, maxG, maxB, maxA := 0, 0, 0, 0
	nonGrayCount := 0
	opaqueCount := 0
	pixelCount := 0
	stepX := maxInt(bounds.Dx()/6, 1)
	stepY := maxInt(bounds.Dy()/6, 1)

	for y := bounds.Min.Y; y < bounds.Max.Y; y += stepY {
		for x := bounds.Min.X; x < bounds.Max.X; x += stepX {
			c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
			if int(c.R) < minR {
				minR = int(c.R)
			}
			if int(c.G) < minG {
				minG = int(c.G)
			}
			if int(c.B) < minB {
				minB = int(c.B)
			}
			if int(c.A) < minA {
				minA = int(c.A)
			}
			if int(c.R) > maxR {
				maxR = int(c.R)
			}
			if int(c.G) > maxG {
				maxG = int(c.G)
			}
			if int(c.B) > maxB {
				maxB = int(c.B)
			}
			if int(c.A) > maxA {
				maxA = int(c.A)
			}
			if c.R != c.G || c.G != c.B {
				nonGrayCount++
			}
			if c.A == 0xff {
				opaqueCount++
			}
			pixelCount++
		}
	}

	for _, pt := range points {
		c := color.RGBAModel.Convert(img.At(pt.X, pt.Y)).(color.RGBA)
		samples = append(samples, fmt.Sprintf("(%d,%d)=%d,%d,%d,%d", pt.X, pt.Y, c.R, c.G, c.B, c.A))
	}

	return fmt.Sprintf(
		"samples=[%s] min=%d,%d,%d,%d max=%d,%d,%d,%d non_gray=%d/%d opaque=%d/%d type=%T",
		strings.Join(samples, " "),
		minR, minG, minB, minA,
		maxR, maxG, maxB, maxA,
		nonGrayCount, pixelCount,
		opaqueCount, pixelCount,
		img,
	)
}

func (vw *VideoWidget) dumpFrameSnapshot(img image.Image, frameNum int64) {
	if img == nil {
		return
	}

	trace := vw.currentVideoTraceLabel()
	if trace == "" {
		trace = "unknown"
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("usbridge-%s-frame-%d.png", trace, frameNum))
	file, err := os.Create(path)
	if err != nil {
		logrus.Warnf("⚠️ [VIDEO] cannot create frame snapshot %s: %v", path, err)
		return
	}
	defer file.Close()

	if err := png.Encode(file, img); err != nil {
		logrus.Warnf("⚠️ [VIDEO] cannot encode frame snapshot %s: %v", path, err)
		return
	}

	logrus.Infof("📸 [VIDEO] frame snapshot saved trace=%s frame=%d path=%s stats=%s", trace, frameNum, path, summarizeImage(img))
}

// ShowVirtualKeyboardIfMobile shows the virtual keyboard if we're on a mobile OS
func (vw *VideoWidget) ShowVirtualKeyboardIfMobile() {
	vw.platformShowVirtualKeyboardIfMobile()
}

// parseBitrateKbps parses a bitrate string in the "<n>K"/"<n>M"/"<n>" format
// (as produced by the video-start dialog's bitrate slider) into kbps.
func parseBitrateKbps(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	last := value[len(value)-1]
	switch last {
	case 'M':
		v, err := strconv.ParseFloat(value[:len(value)-1], 64)
		if err != nil {
			return 0, false
		}
		return int(v * 1000), true
	case 'K':
		v, err := strconv.ParseFloat(value[:len(value)-1], 64)
		if err != nil {
			return 0, false
		}
		return int(v), true
	default:
		v, err := strconv.Atoi(value)
		if err != nil {
			return 0, false
		}
		return v, true
	}
}
