//go:build windows

package winsec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"crossscreen/platform/inject"

	"golang.org/x/sys/windows"
)

// Diagnose collects the state of the whole secure-input chain and writes it
// to %TEMP%\crossscreen-diag.txt for support. Invoked as "CrossScreen.exe
// diag". It never modifies anything.
func Diagnose() {
	var sb strings.Builder
	line := func(format string, args ...any) {
		fmt.Fprintf(&sb, format+"\n", args...)
	}

	line("CrossScreen secure-input diagnostics %s", time.Now().Format(time.RFC3339))
	line("elevated: %v", inject.IsElevated())

	if name, err := inputDesktopName(desktopReadObjects); err == nil {
		line("input desktop: %q (secure=%v)", name, name != defaultDesktopName)
	} else {
		line("input desktop query failed: %v", err)
	}

	if info, err := QueryService(); err == nil {
		line("service: installed=%v running=%v", info.Installed, info.Running)
	} else {
		line("service query failed: %v", err)
	}

	line("CrossScreen.exe processes running: %d", countProcesses("CrossScreen.exe"))

	line("pipe probe: %s", ProbePipeState())
	conn, err := DialPipe(1200 * time.Millisecond)
	if err == nil {
		conn.Close()
		line("pipe: CONNECT OK")
	} else if strings.Contains(ProbePipeState(), "busy") {
		line("pipe: connect FAILED (expected: the app holds the single pipe connection): %v", err)
	} else {
		line("pipe: connect FAILED: %v", err)
	}

	// Attachment requires SYSTEM and a locked screen to be meaningful, but
	// the attempt still reports the raw error for diagnosis.
	if h, dn, aerr := attachInputDesktop(); aerr == nil {
		line("attach: OK (desktop %q handle 0x%x)", dn, uintptr(h))
		procCloseDesktop.Call(uintptr(h))
	} else {
		line("attach: FAILED: %v", aerr)
	}

	// The agent and service have no console; their diagnostics live in the
	// agent log file. Include the tail so the chain is readable in one go.
	line("")
	line("agent log (last 40 lines) [%s]:", AgentLogPath())
	tail, terr := readAgentLogTail(40)
	if terr != nil {
		line("  (unreadable: %v)", terr)
	} else if len(tail) == 0 {
		line("  (empty)")
	} else {
		for _, l := range tail {
			line("  %s", l)
		}
	}

	out := filepath.Join(os.TempDir(), "crossscreen-diag.txt")
	if werr := os.WriteFile(out, []byte(sb.String()), 0o644); werr == nil {
		fmt.Println("diagnostics written to", out)
	}
	fmt.Print(sb.String())
}

// countProcesses returns how many processes with the given executable name
// are running (used to detect the agent alongside the main panel process).
func countProcesses(name string) int {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return -1
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return 0
	}
	n := 0
	for {
		if strings.EqualFold(syscall.UTF16ToString(pe.ExeFile[:]), name) {
			n++
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return n
}

// readAgentLogTail returns the last n lines of the agent log file.
func readAgentLogTail(n int) ([]string, error) {
	data, err := os.ReadFile(AgentLogPath())
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}
