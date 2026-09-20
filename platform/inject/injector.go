// Package inject applies input events to the local system and enumerates the
// local displays. Implementations are platform-specific: macOS uses CGo
// CoreGraphics events, Windows uses user32 SendInput via syscall.
package inject

import (
	"crossscreen/core/event"
	"crossscreen/core/keymap"
	"crossscreen/core/layout"
)

// Injector applies input events to the local system. Pointer coordinates are
// in the global display coordinate space reported by EnumerateScreens.
type Injector interface {
	MovePointer(x, y int32) error
	Button(b event.MouseButton, down bool) error
	Scroll(dx, dy int32) error
	Key(k keymap.Key, down bool) error
}

// EnumerateScreens returns the device's displays in the global display
// coordinate space (the primary display sits at the origin).
func EnumerateScreens(device layout.DeviceID) ([]*layout.Screen, error) {
	return enumerateScreens(device)
}
