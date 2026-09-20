//go:build darwin

package inject

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>

static CGEventRef cs_mouse(CGEventType t, double x, double y, CGMouseButton b) {
	return CGEventCreateMouseEvent(NULL, t, CGPointMake(x, y), b);
}
static void cs_warp(double x, double y) {
	CGWarpMouseCursorPosition(CGPointMake(x, y));
}
static CGEventRef cs_scroll(int32_t dy, int32_t dx) {
	return CGEventCreateScrollWheelEvent(NULL, kCGScrollEventUnitLine, 2, dy, dx);
}
static CGEventRef cs_key(CGKeyCode code, int down) {
	return CGEventCreateKeyboardEvent(NULL, code, down);
}
static void cs_post(CGEventRef e) {
	CGEventPost(kCGHIDEventTap, e);
}
static void cs_release(CGEventRef e) {
	CFRelease(e);
}
static int cs_is_null(CGEventRef e) {
	return e == NULL;
}
*/
import "C"

import (
	"fmt"
	"sync"

	"crossscreen/core/event"
	"crossscreen/core/keymap"
)

func isNil(ev C.CGEventRef) bool {
	return C.cs_is_null(ev) != 0
}

type darwinInjector struct {
	mu           sync.Mutex
	lastX, lastY int32
}

// New creates an injector for the current platform.
func New() (Injector, error) {
	return &darwinInjector{}, nil
}

func (d *darwinInjector) MovePointer(x, y int32) error {
	d.mu.Lock()
	d.lastX, d.lastY = x, y
	d.mu.Unlock()
	// CGWarpMouseCursorPosition moves the cursor in both coupled and
	// decoupled modes (CGEventPost mouse moves do not move a decoupled
	// cursor). This is the cursor-placement primitive for the server.
	C.cs_warp(C.double(x), C.double(y))
	return nil
}

func (d *darwinInjector) Button(b event.MouseButton, down bool) error {
	d.mu.Lock()
	x, y := d.lastX, d.lastY
	d.mu.Unlock()
	var (
		t   C.CGEventType
		cgb C.CGMouseButton
	)
	switch b {
	case event.ButtonLeft:
		cgb = C.kCGMouseButtonLeft
		if down {
			t = C.kCGEventLeftMouseDown
		} else {
			t = C.kCGEventLeftMouseUp
		}
	case event.ButtonRight:
		cgb = C.kCGMouseButtonRight
		if down {
			t = C.kCGEventRightMouseDown
		} else {
			t = C.kCGEventRightMouseUp
		}
	case event.ButtonMiddle:
		cgb = C.kCGMouseButtonCenter
		if down {
			t = C.kCGEventOtherMouseDown
		} else {
			t = C.kCGEventOtherMouseUp
		}
	default:
		return nil
	}
	ev := C.cs_mouse(t, C.double(x), C.double(y), cgb)
	if isNil(ev) {
		return fmt.Errorf("inject: CGEventCreateMouseEvent failed")
	}
	C.cs_post(ev)
	C.cs_release(ev)
	return nil
}

func (d *darwinInjector) Scroll(dx, dy int32) error {
	ev := C.cs_scroll(C.int32_t(dy), C.int32_t(dx))
	if isNil(ev) {
		return fmt.Errorf("inject: CGEventCreateScrollWheelEvent failed")
	}
	C.cs_post(ev)
	C.cs_release(ev)
	return nil
}

func (d *darwinInjector) Key(k keymap.Key, down bool) error {
	code, ok := keymap.MacKeyCode(k)
	if !ok {
		return nil // key not mappable on macOS
	}
	downInt := 0
	if down {
		downInt = 1
	}
	ev := C.cs_key(C.CGKeyCode(code), C.int(downInt))
	if isNil(ev) {
		return fmt.Errorf("inject: CGEventCreateKeyboardEvent failed")
	}
	C.cs_post(ev)
	C.cs_release(ev)
	return nil
}
