//go:build windows

// Platform initialization for Windows:
//   - Per-Monitor-V2 DPI awareness so cursor coordinates and the virtual
//     screen metrics are in real pixels across mixed-DPI displays.
//   - Elevation self-restart: controlling elevated windows (Task Manager,
//     UAC prompts, admin apps) requires SendInput from an equally elevated
//     process due to UIPI; when not elevated the program relaunches itself
//     via ShellExecute "runas" (the standard UAC prompt).
package inject

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	user32DPI            = syscall.NewLazyDLL("user32.dll")
	procSetProcessDPICtx = user32DPI.NewProc("SetProcessDpiAwarenessContext")

	shell32          = syscall.NewLazyDLL("shell32.dll")
	procShellExecute = shell32.NewProc("ShellExecuteW")

	advapi32           = syscall.NewLazyDLL("advapi32.dll")
	procOpenProcessTok = advapi32.NewProc("OpenProcessToken")
	procGetTokenInfo   = advapi32.NewProc("GetTokenInformation")

	kernel32Proc          = syscall.NewLazyDLL("kernel32.dll")
	procGetCurrentProcess = kernel32Proc.NewProc("GetCurrentProcess")
	procCloseHandle       = kernel32Proc.NewProc("CloseHandle")
)

const (
	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4 as an unsigned handle.
	perMonitorAwareV2 = ^uintptr(3)
)

type tokenElevationInfo struct {
	tokenIsElevated uint32
}

// Startup performs one-time Windows process setup: DPI awareness and, unless
// the caller opts out, self-elevation. Returns true when the current process
// should exit because an elevated instance was launched.
//
// requireElevated controls whether a non-elevated process relaunches itself
// through a UAC prompt. Controlling Task Manager / UAC / admin windows needs
// an elevated process (UIPI); ordinary desktop apps do not.
func Startup(requireElevated bool) bool {
	// Preferred on Windows 10 1703+; ignore failure (older OS).
	procSetProcessDPICtx.Call(perMonitorAwareV2)
	if requireElevated {
		return ensureElevated()
	}
	return false
}

// IsElevated reports whether the process is running as administrator.
func IsElevated() bool { return isElevated() }

const (
	tokenQuery     = 0x0008
	tokenElevation = 20 // TokenElevation
	swShownormal   = 1
)

// ensureElevated relaunches the current executable with an elevation prompt
// if it is not already running as administrator. Returns true when the
// current process should exit (a new elevated instance was launched or the
// launch failed fatally). Call as early as possible in main.
func ensureElevated() bool {
	if isElevated() {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	dir, _ := syscall.UTF16PtrFromString("")
	args, _ := syscall.UTF16PtrFromString(commandLineArgs())

	// >32 means success (an HINSTANCE); <=32 is a ShellExecute error code.
	r, _, _ := procShellExecute.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(args)),
		uintptr(unsafe.Pointer(dir)),
		swShownormal,
	)
	if r > 32 {
		return true // elevated instance started; this one exits
	}
	// User declined UAC: continue unelevated (elevated windows stay out of
	// reach, but everything else still works).
	return false
}

// isElevated reports whether the current process token is elevated.
func isElevated() bool {
	var token uintptr
	cur, _, _ := procGetCurrentProcess.Call()
	r, _, _ := procOpenProcessTok.Call(
		cur,
		tokenQuery,
		uintptr(unsafe.Pointer(&token)),
	)
	if r == 0 {
		return false
	}
	defer procCloseHandle.Call(token)

	var info tokenElevationInfo
	var returned uint32
	r, _, _ = procGetTokenInfo.Call(
		token,
		tokenElevation,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
		uintptr(unsafe.Pointer(&returned)),
	)
	return r != 0 && info.tokenIsElevated != 0
}

// commandLineArgs returns the original arguments (excluding the program) as a
// single quoted string for the elevated relaunch.
func commandLineArgs() string {
	args := os.Args[1:]
	out := ""
	for _, a := range args {
		out += " " + syscall.EscapeArg(a)
	}
	if len(out) > 0 {
		return out[1:]
	}
	return ""
}
