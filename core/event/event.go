// Package event defines the abstract input events exchanged between devices.
// These structs are platform-neutral: the transport encodes them as JSON and
// each platform adapter maps them to native events (CGEvent / SendInput).
package event

import "crossscreen/core/keymap"

// Modifier bitmask shared by all platforms.
const (
	ModShift uint32 = 1 << iota
	ModCtrl
	ModAlt
	ModMeta // Meta on Windows/Linux, Command on macOS
)

// MouseButton identifies a physical mouse button.
type MouseButton uint8

const (
	ButtonNone   MouseButton = 0
	ButtonLeft   MouseButton = 1
	ButtonRight  MouseButton = 2
	ButtonMiddle MouseButton = 3
)

// PointerMove is an absolute pointer position, in the destination screen's
// local pixel coordinates (origin at the screen's top-left).
type PointerMove struct {
	X int32 `json:"x"`
	Y int32 `json:"y"`
}

// PointerButton is a mouse button press/release.
type PointerButton struct {
	Button MouseButton `json:"b"`
	Down   bool        `json:"d"`
	Count  uint32      `json:"n,omitempty"` // click count, defaults to 1
}

// PointerScroll is a wheel / trackpad scroll delta.
type PointerScroll struct {
	DX int32 `json:"dx,omitempty"`
	DY int32 `json:"dy,omitempty"`
}

// KeyEvent is a keyboard press/release. KeyCode is a neutral cross-platform
// keycode (see package keymap); the injecting adapter maps it to its local
// keycode. Modifier keys are sent as ordinary key events.
type KeyEvent struct {
	KeyCode keymap.Key `json:"k"`
	Down    bool       `json:"d"`
	Mods    uint32     `json:"m,omitempty"`
}

// ClipboardEvent carries a clipboard payload to be pasted on the peer.
// Data is []byte, which encoding/json renders as base64.
type ClipboardEvent struct {
	Mime string `json:"mime"`
	Data []byte `json:"data"`
}
