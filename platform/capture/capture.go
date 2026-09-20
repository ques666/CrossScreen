// Package capture captures local input events (mouse, keyboard, scroll) and
// exposes the current pointer position. Implementations are platform-specific:
// macOS uses a CoreGraphics event tap, Windows uses low-level hooks.
package capture

import (
	"crossscreen/core/event"
	"crossscreen/core/keymap"
)

// Handler receives captured local input events. All callbacks may be nil.
type Handler struct {
	// OnMove reports an absolute position plus the raw movement delta
	// (dx, dy). Deltas are essential on platforms that clamp the cursor at
	// the display edge: the absolute position stops changing there while the
	// delta keeps reflecting the user's movement.
	OnMove   func(x, y, dx, dy int32)
	OnButton func(b event.PointerButton)
	OnScroll func(s event.PointerScroll)
	OnKey    func(k keymap.Key, down bool)
}

// Capture captures local input. Start installs the listener; Stop removes it.
type Capture interface {
	Start(h Handler) error
	Stop()
	// Position returns the current pointer position in global display
	// coordinates (used to seed the shared cursor on startup).
	Position() (x, y int32, err error)
	// SetRelativeMode decouples the cursor from the mouse (macOS) so raw
	// movement deltas keep flowing while the shared cursor is on a peer's
	// screen. Implementations without this concept treat it as a no-op.
	SetRelativeMode(on bool)
	// PinCursor places the physical cursor at (x, y) and, on platforms that
	// need edge-bouncing (Windows), uses it as the anchor the cursor is
	// repeatedly pulled back to while relative mode is on. macOS treats it
	// as a no-op (decoupling already keeps the cursor fixed).
	PinCursor(x, y int32)
}

// New creates a capture for the current platform.
func New() (Capture, error) {
	return newCapture()
}
