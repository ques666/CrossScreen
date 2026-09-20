//go:build windows

// Windows service that bridges the interactive app to the secure desktop.
//
// Running as LocalSystem in session 0 it cannot call SendInput into the
// user's session itself; instead it starts one "agent" process inside the
// active console session with a SYSTEM token (derived from winlogon.exe).
// The agent attaches to Winlogon and serves the named pipe (see
// agent_windows.go).
package winsec

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	ServiceName        = "CrossScreenInputService"
	ServiceDisplayName = "CrossScreen Secure Input Service"
	ServiceDescription = "Lets CrossScreen inject keyboard/mouse input on the Windows lock screen (secure desktop)."
	agentCmd           = "agent"
)

var (
	kernel32Svc              = windows.NewLazySystemDLL("kernel32.dll")
	advapi32Svc              = windows.NewLazySystemDLL("advapi32.dll")
	procActiveConsoleSession = kernel32Svc.NewProc("WTSGetActiveConsoleSessionId")
	procProcessIDToSession   = kernel32Svc.NewProc("ProcessIdToSessionId")
	procOpenProcess          = kernel32Svc.NewProc("OpenProcess")
	procOpenProcessToken     = advapi32Svc.NewProc("OpenProcessToken")
	procDuplicateTokenEx     = advapi32Svc.NewProc("DuplicateTokenEx")
	procCreateProcessAsUserW = advapi32Svc.NewProc("CreateProcessAsUserW")
)

const (
	processQueryLimitedInfo = 0x1000
	processQueryInfo        = 0x0400
	tokenDuplicate          = 0x0002
	tokenPrimary            = 1
	securityImpersonation   = 2
)

type inputService struct{}

// RunService is the svc entry point ("CrossScreen.exe svc").
func RunService() error {
	return svc.Run(ServiceName, &inputService{})
}

func (s *inputService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); runSupervisor(ctx) }()

	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop}

loop:
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				break loop
			}
		case <-ctx.Done():
			break loop
		}
	}

	changes <- svc.Status{State: svc.StopPending}
	cancel()
	wg.Wait()
	changes <- svc.Status{State: svc.Stopped}
	return false, 0
}

// supervisor keeps exactly one agent alive in the active console session.
func runSupervisor(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()

	var (
		mu      sync.Mutex
		hProc   windows.Handle
		lastSID uint32
	)
	kill := func() {
		if hProc != 0 {
			_ = windows.TerminateProcess(hProc, 0)
			_ = windows.CloseHandle(hProc)
			hProc = 0
		}
	}

	for {
		select {
		case <-ctx.Done():
			mu.Lock()
			kill()
			mu.Unlock()
			return
		case <-t.C:
		}

		sid := activeConsoleSession()
		if sid == 0xFFFFFFFF {
			continue // nobody logged in at the physical console
		}

		mu.Lock()
		if sid != lastSID {
			kill()
			lastSID = sid
		}
		if hProc != 0 {
			var ec uint32
			if windows.GetExitCodeProcess(hProc, &ec) == nil && ec != 259 {
				// 259 = STILL_ACTIVE; anything else means the agent exited.
				_ = windows.CloseHandle(hProc)
				hProc = 0
			}
		}
		if hProc == 0 {
			h, err := spawnAgentInSession(sid)
			if err != nil {
				log.Printf("winsec svc: spawn agent in session %d: %v", sid, err)
				appendAgentLog("svc: spawn agent in session %d: %v", sid, err)
			} else {
				hProc = h
				log.Printf("winsec svc: agent started in session %d", sid)
				appendAgentLog("svc: agent started in session %d", sid)
			}
		}
		mu.Unlock()
	}
}

func activeConsoleSession() uint32 {
	r, _, _ := procActiveConsoleSession.Call()
	return uint32(r)
}

// spawnAgentInSession starts "CrossScreen.exe agent" in the given user session
// with a SYSTEM token cloned from winlogon.exe, so the child can open the
// Winlogon desktop.
func spawnAgentInSession(sessionID uint32) (windows.Handle, error) {
	exe, err := appExePath()
	if err != nil {
		return 0, err
	}
	winlogonPID, err := findProcessInSession("winlogon.exe", sessionID)
	if err != nil {
		return 0, err
	}

	hProcRaw, _, _ := procOpenProcess.Call(processQueryLimitedInfo|processQueryInfo, 0, uintptr(winlogonPID))
	if hProcRaw == 0 {
		return 0, fmt.Errorf("winsec: OpenProcess(winlogon): %w", windows.GetLastError())
	}
	hProc := windows.Handle(hProcRaw)
	defer windows.CloseHandle(hProc)

	var hTok windows.Handle
	r, _, callErr := procOpenProcessToken.Call(
		uintptr(hProc), tokenDuplicate, uintptr(unsafe.Pointer(&hTok)))
	if r == 0 {
		return 0, fmt.Errorf("winsec: OpenProcessToken: %w", callErr)
	}
	defer windows.CloseHandle(hTok)

	var hUser windows.Handle
	r, _, callErr = procDuplicateTokenEx.Call(
		uintptr(hTok),
		windows.MAXIMUM_ALLOWED,
		0,
		securityImpersonation,
		tokenPrimary,
		uintptr(unsafe.Pointer(&hUser)),
	)
	if r == 0 {
		return 0, fmt.Errorf("winsec: DuplicateTokenEx: %w", callErr)
	}
	defer windows.CloseHandle(hUser)

	cmd, err := windows.UTF16PtrFromString(`"` + exe + `" ` + agentCmd)
	if err != nil {
		return 0, err
	}
	var si windows.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	// Launch the agent directly onto the Winlogon (secure) desktop.
	// SendInput after a runtime SetThreadDesktop onto the secure desktop is
	// silently dropped on modern Windows, but a process CREATED on that
	// desktop injects into the lock screen reliably.
	si.Desktop, _ = windows.UTF16PtrFromString(`winsta0\Winlogon`)
	var pi windows.ProcessInformation
	r, _, callErr = procCreateProcessAsUserW.Call(
		uintptr(hUser),
		0,
		uintptr(unsafe.Pointer(cmd)),
		0, 0,
		0, // inherit handles
		0, // creation flags
		0, 0,
		uintptr(unsafe.Pointer(&si)),
		uintptr(unsafe.Pointer(&pi)),
	)
	if r == 0 {
		return 0, fmt.Errorf("winsec: CreateProcessAsUser: %w", callErr)
	}
	_ = windows.CloseHandle(windows.Handle(pi.Thread))
	return windows.Handle(pi.Process), nil
}

// findProcessInSession locates an executable in a specific RDP/console
// session via a toolhelp snapshot.
func findProcessInSession(name string, sessionID uint32) (uint32, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return 0, err
	}
	for {
		if syscall.UTF16ToString(pe.ExeFile[:]) == name {
			var sid uint32
			r, _, _ := procProcessIDToSession.Call(uintptr(pe.ProcessID), uintptr(unsafe.Pointer(&sid)))
			if r != 0 && sid == sessionID {
				return pe.ProcessID, nil
			}
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return 0, fmt.Errorf("winsec: %s not found in session %d", name, sessionID)
}

// appExePath returns the path of the running executable.
func appExePath() (string, error) {
	n := uint32(4096)
	var buf []uint16
	for {
		buf = make([]uint16, n)
		r, err := windows.GetModuleFileName(0, &buf[0], n)
		if err != nil {
			return "", err
		}
		if r < n-1 {
			break
		}
		n *= 2
	}
	return filepath.Clean(windows.UTF16ToString(buf)), nil
}

// ---------- SCM installation ----------

// InstallService creates and starts the auto-start SYSTEM service. Requires
// elevation (the app already self-elevates on Windows).
func InstallService() error {
	exe, err := appExePath()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("winsec: open SCM (run as administrator): %w", err)
	}
	defer m.Disconnect()

	cfg := mgr.Config{
		DisplayName:      ServiceDisplayName,
		Description:      ServiceDescription,
		BinaryPathName:   `"` + exe + `" svc`,
		StartType:        mgr.StartAutomatic,
		ServiceStartName: "", // "" = LocalSystem
	}
	s, err := m.CreateService(ServiceName, exe, cfg, "svc")
	if err != nil {
		// Already installed: reconcile config and try to start it.
		existing, openErr := m.OpenService(ServiceName)
		if openErr != nil {
			return fmt.Errorf("winsec: create service: %w", err)
		}
		defer existing.Close()
		cur, qErr := existing.Config()
		if qErr == nil {
			cur.BinaryPathName = `"` + exe + `" svc`
			cur.StartType = mgr.StartAutomatic
			cur.DisplayName = ServiceDisplayName
			cur.Description = ServiceDescription
			_ = existing.UpdateConfig(cur)
		}
		s = existing
	}
	defer s.Close()

	if err := s.Start("svc"); err != nil {
		// Already running is fine.
		if !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
			return fmt.Errorf("winsec: start service: %w", err)
		}
	}
	return nil
}

// UninstallService stops and removes the service.
func UninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("winsec: open SCM (run as administrator): %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(ServiceName)
	if err != nil {
		return nil // nothing installed
	}
	defer s.Close()
	_, _ = s.Control(svc.Stop)
	// Give SCM a moment to stop it before deletion.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st, qErr := s.Query()
		if qErr != nil || st.State == svc.Stopped {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	return s.Delete()
}

// ServiceStatus reports installation/run state for the UI.
type ServiceStatusInfo struct {
	Installed bool `json:"installed"`
	Running   bool `json:"running"`
}

func QueryService() (ServiceStatusInfo, error) {
	var info ServiceStatusInfo
	m, err := mgr.Connect()
	if err != nil {
		return info, err
	}
	defer m.Disconnect()
	s, err := m.OpenService(ServiceName)
	if err != nil {
		return info, nil // not installed
	}
	defer s.Close()
	info.Installed = true
	st, err := s.Query()
	if err == nil {
		info.Running = st.State == svc.Running
	}
	return info, nil
}
