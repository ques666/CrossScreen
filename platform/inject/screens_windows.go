//go:build windows

package inject

import (
	"fmt"
	"syscall"
	"unsafe"

	"crossscreen/core/layout"
)

var (
	procEnumDisplayMonitors = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
)

type rect struct {
	left, top, right, bottom int32
}

// monitorInfoEx mirrors the Win32 MONITORINFOEXW structure.
type monitorInfoEx struct {
	cbSize    uint32
	rcMonitor rect
	rcWork    rect
	dwFlags   uint32
	szDevice  [32]uint16
}

// enumerateScreens returns the device's displays in the virtual-screen
// coordinate space (the primary display sits at the origin).
func enumerateScreens(device layout.DeviceID) ([]*layout.Screen, error) {
	var screens []*layout.Screen
	cb := syscall.NewCallback(func(hMonitor, hdc, lprc, lParam uintptr) uintptr {
		var mi monitorInfoEx
		mi.cbSize = uint32(unsafe.Sizeof(mi))
		if r, _, _ := procGetMonitorInfoW.Call(hMonitor, uintptr(unsafe.Pointer(&mi))); r != 0 {
			s := &layout.Screen{
				ID:      layout.ScreenID(fmt.Sprintf("%s:%d", device, len(screens))),
				Device:  device,
				Name:    syscall.UTF16ToString(mi.szDevice[:]),
				X:       mi.rcMonitor.left,
				Y:       mi.rcMonitor.top,
				Width:   mi.rcMonitor.right - mi.rcMonitor.left,
				Height:  mi.rcMonitor.bottom - mi.rcMonitor.top,
				Primary: mi.rcMonitor.left == 0 && mi.rcMonitor.top == 0,
			}
			screens = append(screens, s)
		}
		return 1
	})
	if r, _, _ := procEnumDisplayMonitors.Call(0, 0, cb, 0); r == 0 {
		return nil, syscall.GetLastError()
	}
	return screens, nil
}
