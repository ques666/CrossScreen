//go:build windows

package inject

import (
	"syscall"
	"unsafe"

	"crossscreen/core/event"
	"crossscreen/core/keymap"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procSendInput        = user32.NewProc("SendInput")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
)

// GetSystemMetrics indices.
const (
	smXVirtualScreen  = 76 // SM_XVIRTUALSCREEN: virtual-screen origin X
	smYVirtualScreen  = 77 // SM_YVIRTUALSCREEN: virtual-screen origin Y
	smCxVirtualScreen = 78 // width
	smCyVirtualScreen = 79 // height
	wheelDelta        = 120
)

// Input event flags (WinUser.h).
const (
	mouseEventFMove            = 0x0001
	mouseEventFLeftDown        = 0x0002
	mouseEventFLeftUp          = 0x0004
	mouseEventFRightDown       = 0x0008
	mouseEventFRightUp         = 0x0010
	mouseEventFMiddleDown      = 0x0020
	mouseEventFMiddleUp        = 0x0040
	mouseEventFWheel           = 0x0800
	mouseEventFHorizontalWheel = 0x1000
	mouseEventFVirtualDesk     = 0x4000 // map absolute coords to whole virtual desktop
	mouseEventFAbsolute        = 0x8000
	keyEventFKeyUp             = 0x0002
)

const (
	inputMouse    = 0
	inputKeyboard = 1
)

// mouseInput mirrors Win32 MOUSEINPUT. KEYBDINPUT overlays the same bytes:
//
//	offset 0: wVk (WORD) / wScan (WORD) -> dx (int32)
//	offset 4: dwFlags (DWORD)           -> dy (int32)
//	offset 8: time (DWORD)              -> mouseData (uint32)
//
// The struct is 32 bytes on amd64; INPUT adds DWORD type + padding = 40.
type mouseInput struct {
	dx          int32
	dy          int32
	mouseData   uint32
	dwFlags     uint32
	time        uint32
	_           uint32 // padding to align dwExtraInfo to 8 bytes
	dwExtraInfo uintptr
}

// input mirrors the Win32 INPUT structure: a DWORD type followed by the
// largest union member (MOUSEINPUT, 32 bytes) = 40 bytes on amd64. Laying the
// fields out sequentially as two Go structs would be 64 bytes and SendInput
// rejects the wrong cbSize.
type input struct {
	typ uint32
	_   uint32
	u   mouseInput
}

type windowsInjector struct{}

// New creates an injector for the current platform.
func New() (Injector, error) {
	return &windowsInjector{}, nil
}

func sendInput(inp *input) error {
	r, _, _ := procSendInput.Call(1, uintptr(unsafe.Pointer(inp)), unsafe.Sizeof(*inp))
	if r == 0 {
		return syscall.GetLastError()
	}
	return nil
}

func virtualScreen() (originX, originY, width, height int32) {
	x, _, _ := procGetSystemMetrics.Call(smXVirtualScreen)
	y, _, _ := procGetSystemMetrics.Call(smYVirtualScreen)
	w, _, _ := procGetSystemMetrics.Call(smCxVirtualScreen)
	h, _, _ := procGetSystemMetrics.Call(smCyVirtualScreen)
	return int32(x), int32(y), int32(w), int32(h)
}

func clampInt64(v, min, max int64) int64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func (w *windowsInjector) MovePointer(x, y int32) error {
	ox, oy, vw, vh := virtualScreen()
	if vw <= 0 || vh <= 0 {
		ox, oy, vw, vh = 0, 0, 65536, 65536
	}
	// ABSOLUTE coordinates are normalized over the whole virtual screen;
	// subtract its origin (a secondary display may sit at negative coords).
	nx := clampInt64((int64(x-ox))*65535/int64(vw), 0, 65535)
	ny := clampInt64((int64(y-oy))*65535/int64(vh), 0, 65535)

	inp := &input{typ: inputMouse}
	inp.u.dx = int32(nx)
	inp.u.dy = int32(ny)
	// VIRTUALDESK is required with ABSOLUTE on multi-monitor systems: without
	// it the coordinates map only onto the primary display and the cursor is
	// clamped there (looks like the pointer is frozen on the second screen).
	inp.u.dwFlags = mouseEventFMove | mouseEventFAbsolute | mouseEventFVirtualDesk
	return sendInput(inp)
}

func (w *windowsInjector) Button(b event.MouseButton, down bool) error {
	var flags uint32
	switch b {
	case event.ButtonLeft:
		if down {
			flags = mouseEventFLeftDown
		} else {
			flags = mouseEventFLeftUp
		}
	case event.ButtonRight:
		if down {
			flags = mouseEventFRightDown
		} else {
			flags = mouseEventFRightUp
		}
	case event.ButtonMiddle:
		if down {
			flags = mouseEventFMiddleDown
		} else {
			flags = mouseEventFMiddleUp
		}
	default:
		return nil
	}
	inp := &input{typ: inputMouse}
	inp.u.dwFlags = flags
	return sendInput(inp)
}

// wheelData encodes a signed wheel delta in the high WORD of mouseData.
func wheelData(lines int32) uint32 {
	return uint32(uint16(int16(lines * wheelDelta)))
}

func (w *windowsInjector) Scroll(dx, dy int32) error {
	if dy != 0 {
		inp := &input{typ: inputMouse}
		inp.u.mouseData = wheelData(dy)
		inp.u.dwFlags = mouseEventFWheel
		if err := sendInput(inp); err != nil {
			return err
		}
	}
	if dx != 0 {
		inp := &input{typ: inputMouse}
		inp.u.mouseData = wheelData(dx)
		inp.u.dwFlags = mouseEventFHorizontalWheel
		if err := sendInput(inp); err != nil {
			return err
		}
	}
	return nil
}

func (w *windowsInjector) Key(k keymap.Key, down bool) error {
	vk, ok := keymap.WindowsVK(k)
	if !ok {
		return nil // key not mappable on Windows
	}
	// Mac-to-Windows convention: Command drives Windows shortcuts, so map the
	// neutral GUI keys (⌘) to Control. Option/Alt keeps its natural position.
	switch k {
	case keymap.Key(0xE3): // Left GUI  -> Left Control
		vk = 0xA2
	case keymap.Key(0xE7): // Right GUI -> Right Control
		vk = 0xA3
	}
	inp := &input{typ: inputKeyboard}
	// Union layout note: KEYBDINPUT overlays MOUSEINPUT as
	//   wVk     WORD  -> u.dx low word
	//   wScan   WORD  -> u.dx high word
	//   dwFlags DWORD -> u.dy        (NOT u.mouseData — that is KEYBDINPUT.time)
	//   time    DWORD -> u.mouseData
	inp.u.dx = int32(vk)
	if !down {
		inp.u.dy = int32(keyEventFKeyUp) // KEYEVENTF_KEYUP
	}
	return sendInput(inp)
}
