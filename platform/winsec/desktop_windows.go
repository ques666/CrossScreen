//go:build windows

package winsec

import (
	"syscall"
	"unsafe"
)

var (
	user32                 = syscall.NewLazyDLL("user32.dll")
	procOpenInputDesktop   = user32.NewProc("OpenInputDesktop")
	procCloseDesktop       = user32.NewProc("CloseDesktop")
	procSetThreadDesktop   = user32.NewProc("SetThreadDesktop")
	procGetUserObjectInfoW = user32.NewProc("GetUserObjectInformationW")
	procGetThreadDesktop   = user32.NewProc("GetThreadDesktop")

	kernel32Desk         = syscall.NewLazyDLL("kernel32.dll")
	procGetCurrentThread = kernel32Desk.NewProc("GetCurrentThreadId")
)

const (
	desktopReadObjects   = 0x00000001
	desktopWriteObjects  = 0x00000080
	desktopSwitchDesktop = 0x00000100
	uoiName              = 2
)

// desktopAttachAccess is the smallest set of desktop rights needed to attach
// a thread to the input desktop via SetThreadDesktop. It deliberately avoids
// the GENERIC_ALL-style mask (which includes WRITE_DAC/WRITE_OWNER/DELETE):
// those privileged bits are routinely denied to non-SYSTEM processes on the
// desktop object, making OpenInputDesktop fail or return a handle whose name
// query misbehaves.
const desktopAttachAccess = desktopReadObjects | desktopWriteObjects | desktopSwitchDesktop

// defaultDesktopName is the ordinary interactive desktop. Any other input
// desktop ("Winlogon" lock/credential UI, "Screen-saver") is treated as a
// secure surface where direct SendInput from the user session is blocked.
const defaultDesktopName = "Default"

// desktopObjectName returns the name of an already-open desktop handle.
// A fixed first-shot buffer is used instead of the "query size with a NULL
// buffer" dance: some Windows builds return TRUE from the size query without
// writing the size (leaving n==0), which used to produce an empty name and
// broke secure-desktop detection. Growth handles any pathologically long name.
func desktopObjectName(h uintptr) (string, error) {
	buf := make([]uint16, 256)
	for {
		n := uint32(len(buf) * 2)
		r, _, callErr := procGetUserObjectInfoW.Call(
			h, uoiName,
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(n),
			uintptr(unsafe.Pointer(&n)),
		)
		if r != 0 {
			return syscall.UTF16ToString(buf), nil
		}
		if errno, ok := callErr.(syscall.Errno); ok && errno == 122 && n > uint32(len(buf)*2) {
			// ERROR_INSUFFICIENT_BUFFER: grow to the reported size and retry.
			buf = make([]uint16, n/2+1)
			continue
		}
		return "", callErr
	}
}

// currentDesktopName returns the name of the desktop the calling thread is
// running on (used by the agent to confirm it was created on Winlogon).
func currentDesktopName() string {
	tid, _, _ := procGetCurrentThread.Call()
	h, _, _ := procGetThreadDesktop.Call(tid)
	if h == 0 {
		return ""
	}
	name, err := desktopObjectName(h)
	if err != nil {
		return ""
	}
	return name
}

// inputDesktopName returns the name of the desktop currently receiving
// input ("Default" when unlocked, "Winlogon" at the lock/credential screen).
// Querying the name needs only READ permission and works from a normal
// (non-SYSTEM) user process.
func inputDesktopName(access uint32) (string, error) {
	h, _, callErr := procOpenInputDesktop.Call(0, 0, uintptr(access))
	if h == 0 {
		return "", callErr
	}
	defer procCloseDesktop.Call(h)
	return desktopObjectName(h)
}

// IsSecureDesktop reports whether the input desktop is anything other than
// the normal "Default" desktop — i.e. the user is at the lock screen /
// credential UI / secure screensaver, where user-session SendInput is
// blocked by Windows.
func IsSecureDesktop() bool {
	name, err := inputDesktopName(desktopReadObjects)
	if err != nil {
		// Fail open: assume a normal desktop so direct injection keeps
		// working if the query is transiently unavailable.
		return false
	}
	return name != defaultDesktopName
}

// attachInputDesktop switches the calling OS thread onto the current input
// desktop. Must be called on a runtime.LockOSThread'd goroutine that has
// created no windows/hooks (a bare SendInput worker), which is the supported
// way to inject into Winlogon from a SYSTEM process.
//
// It returns the opened desktop handle (caller closes it when done with
// that attachment) and the desktop name.
func attachInputDesktop() (syscall.Handle, string, error) {
	// The desktop name is read through a read-only handle, which is the only
	// open that reliably returns the real name ("Default"/"Winlogon"). The
	// separate attach handle uses just the rights SetThreadDesktop needs.
	name, err := inputDesktopName(desktopReadObjects)
	if err != nil {
		return 0, "", err
	}
	h, _, callErr := procOpenInputDesktop.Call(0, 0, desktopAttachAccess)
	if h == 0 {
		return 0, "", callErr
	}
	r, _, sErr := procSetThreadDesktop.Call(h)
	if r == 0 {
		procCloseDesktop.Call(h)
		return 0, "", sErr
	}
	return syscall.Handle(h), name, nil
}
