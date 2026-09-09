//go:build windows

package main

import (
	"fmt"
	"log"
	"os"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"usbridge_agent/internal/app"
	"usbridge_agent/internal/sasinput"
)

const serviceName = "USBridgeAgent"

func runMain(headless bool) {
	isSvc, err := svc.IsWindowsService()
	if err != nil {
		log.Printf("failed to determine if we are running in an interactive session: %v", err)
		isSvc = false
	}
	if isSvc {
		// Best-effort: SendSAS (app.SendSAS -> sasinput) needs this policy
		// bit to do anything at all -- see EnsureServicesCanGenerateSAS's
		// doc comment. A failure here (e.g. registry access somehow
		// denied) shouldn't block the service from starting; it just means
		// a later SendSAS call silently does nothing, same as it already
		// does when the policy is unset.
		if err := sasinput.EnsureServicesCanGenerateSAS(); err != nil {
			log.Printf("could not enable SoftwareSASGeneration policy (Ctrl+Alt+Del injection on the lock screen may not work): %v", err)
		}
		err = svc.Run(serviceName, &agentService{})
		if err != nil {
			log.Fatalf("service execution failed: %v", err)
		}
		return
	}
	doStart(headless)
}

type agentService struct{}

func (m *agentService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	// AcceptSessionChange is what lets Windows notify this service of
	// WTS_SESSION_LOGON/WTS_CONSOLE_CONNECT below -- see the SessionChange
	// case and app.NotifySessionChange's doc comment for why a LocalSystem
	// service needs this at all: it never automatically re-homes an
	// already-running gamestream-server child into a session that only
	// becomes interactive after this service itself started.
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptSessionChange
	changes <- svc.Status{State: svc.StartPending}

	// Start headless mode in background
	go doStart(true)

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
loop:
	for {
		c := <-r
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus
		case svc.SessionChange:
			// EventType is one of the WTS_* constants (windows package);
			// only react to "a session just became the active console
			// session" -- WTS_SESSION_LOGOFF/WTS_CONSOLE_DISCONNECT need no
			// handling here since the process that was running in that
			// session dies on its own, which rustshineBackend's own
			// watchProcessExit -> onExit -> startSunshine chain already
			// notices and reacts to; WTS_SESSION_UNLOCK is deliberately
			// excluded too -- a locked (not logged-off) session is still a
			// real interactive session DXGI capture already works fine
			// against, so restarting on unlock would just interrupt an
			// otherwise-fine stream for no reason.
			if c.EventType == windows.WTS_SESSION_LOGON || c.EventType == windows.WTS_CONSOLE_CONNECT {
				go app.NotifySessionChange()
			}
			changes <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			break loop
		}
	}
	changes <- svc.Status{State: svc.StopPending}
	return
}

func manageService(action string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %v", err)
	}
	defer m.Disconnect()

	if action == "install" {
		exePath, err := os.Executable()
		if err != nil {
			return err
		}

		s, err := m.OpenService(serviceName)
		if err == nil {
			// already exists, maybe update it?
			s.Close()
			return nil
		}

		s, err = m.CreateService(serviceName, exePath, mgr.Config{
			StartType:        mgr.StartAutomatic,
			DisplayName:      "USBridge Agent",
			Description:      "USBridge Remote Access Service",
			DelayedAutoStart: false,
		}, "--headless")
		if err != nil {
			return fmt.Errorf("create service: %v", err)
		}
		defer s.Close()

		// also start it right away
		_ = s.Start()
		return nil
	} else if action == "uninstall" {
		s, err := m.OpenService(serviceName)
		if err != nil {
			return nil // already doesn't exist
		}
		defer s.Close()
		_, _ = s.Control(svc.Stop)
		if err := s.Delete(); err != nil {
			return fmt.Errorf("delete service: %v", err)
		}
		return nil
	}
	return fmt.Errorf("unknown service action: %s", action)
}
