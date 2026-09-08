//go:build windows

package streamhost

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"

	"usbridge_agent/internal/sessionlaunch"
)

// init wires Start()'s session-broker hooks the same way
// rustshine_process_windows.go's own init does for gamestream-server: when
// this process is itself running as the LocalSystem USBridgeAgent service,
// launch Sunshine into the active console session instead of directly
// under Session 0 -- see useSunshineSessionBroker's doc comment
// (sunshine_backend.go) for why Sunshine specifically needs this.
// svc.IsWindowsService() reflects how this process was actually started
// and never changes for the life of the process, so it's safe to cache
// once instead of re-checking on every Start() call.
func init() {
	isSvc, err := svc.IsWindowsService()
	if err != nil {
		log.Printf("[sunshine] could not determine if running as a Windows service (assuming not): %v", err)
		isSvc = false
	}
	useSunshineSessionBroker = func() bool { return isSvc }
	sunshineSessionBrokerLaunch = sunshineSessionBrokerLaunchImpl
}

// sunshineSessionBrokerLaunchImpl launches exe inside the active console
// session via internal/sessionlaunch, then assigns it to the same
// kill-on-job-close Job Object a plain exec.Cmd launch would get via
// afterStart -- CreateProcessAsUser (unlike exec.Cmd) never gives Go's
// os/exec any notion of "child" to hook Pdeathsig-equivalent cleanup into,
// so it has to be done explicitly by PID here instead. No __COMPAT_LAYER
// override is needed the way rustshine's gamestream-server (a fork of
// upstream, no DPI manifest of its own) needs one -- Sunshine ships a
// proper DPI-awareness manifest.
func sunshineSessionBrokerLaunchImpl(exe string, args []string, workDir string, stdout, stderr *os.File) (sunshineProcess, error) {
	h, err := sessionlaunch.LaunchInActiveSession(exe, args, workDir, stdout, stderr, nil)
	if err != nil {
		if err == sessionlaunch.ErrNoActiveSession {
			return nil, fmt.Errorf("%w: %v", errSunshineNoActiveSessionMarker, err)
		}
		return nil, err
	}
	assignToKillOnCloseJob(h.Pid())
	return sessionProcAdapter{h}, nil
}

// configureProcess hides the console window Windows would otherwise pop up
// for Sunshine (a console-subsystem exe) when spawned from the agent, which
// has no console of its own.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

var (
	jobOnce   sync.Once
	jobHandle windows.Handle
)

// killOnCloseJob lazily creates a single Windows Job Object configured with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE. The handle is intentionally never
// closed by us: Windows closes every handle owned by a process when that
// process terminates for any reason (normal exit, crash, or being killed),
// and closing the last handle to a KILL_ON_JOB_CLOSE job terminates every
// process still assigned to it. That makes this an OS-enforced guarantee
// instead of relying on our own Go code getting a chance to run at exit
// time — which previously let Sunshine survive the agent on Windows.
func killOnCloseJob() windows.Handle {
	jobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			log.Printf("[sunshine] CreateJobObject failed, Sunshine may survive an agent crash: %v", err)
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
			BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
				LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
			},
		}
		if _, err := windows.SetInformationJobObject(
			h,
			windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)),
			uint32(unsafe.Sizeof(info)),
		); err != nil {
			log.Printf("[sunshine] SetInformationJobObject failed, Sunshine may survive an agent crash: %v", err)
			windows.CloseHandle(h)
			return
		}
		jobHandle = h
	})
	return jobHandle
}

// afterStart assigns the freshly-started Sunshine process to the
// kill-on-job-close Job Object, so Windows itself terminates Sunshine the
// instant the agent process ends for any reason — crash, task-kill, normal
// exit — instead of leaving it orphaned.
func afterStart(b *sunshineBackend, cmd *exec.Cmd) {
	job := killOnCloseJob()
	if job == 0 {
		return
	}
	procHandle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		log.Printf("[sunshine] OpenProcess failed, Sunshine may survive an agent crash: %v", err)
		return
	}
	defer windows.CloseHandle(procHandle)
	if err := windows.AssignProcessToJobObject(job, procHandle); err != nil {
		log.Printf("[sunshine] AssignProcessToJobObject failed, Sunshine may survive an agent crash: %v", err)
	}
}
