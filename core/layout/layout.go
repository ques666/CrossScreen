// Package layout models the virtual desktop arrangement shared by all
// devices and the edge-crossing rules that move the pointer between screens.
//
// All coordinates are in a single virtual desktop space: each Screen carries
// an (X, Y) offset placed there by the arrangement editor, and pointer
// positions are virtual until converted to screen-local pixels with ToLocal.
package layout

// ScreenID uniquely identifies one display across the fleet, e.g. "mac:0".
type ScreenID string

// DeviceID identifies a machine, e.g. "mac-studio" or "win-11".
type DeviceID string

// Screen describes one physical display of one device.
type Screen struct {
	ID      ScreenID `json:"id"`
	Device  DeviceID `json:"device"`
	Name    string   `json:"name,omitempty"`
	X       int32    `json:"x"`
	Y       int32    `json:"y"`
	Width   int32    `json:"w"`
	Height  int32    `json:"h"`
	Primary bool     `json:"primary,omitempty"`
}

// Contains reports whether the virtual point (x, y) lies on this screen.
func (s *Screen) Contains(x, y int32) bool {
	return x >= s.X && x < s.X+s.Width && y >= s.Y && y < s.Y+s.Height
}

// ToLocal converts a virtual position to this screen's local pixels.
func (s *Screen) ToLocal(x, y int32) (int32, int32) {
	return x - s.X, y - s.Y
}

// Layout is the whole virtual desktop.
type Layout struct {
	Version int       `json:"version"`
	Screens []*Screen `json:"screens"`
}

// ScreenAt returns the screen containing the virtual point (x, y), or nil.
func (l *Layout) ScreenAt(x, y int32) *Screen {
	for _, s := range l.Screens {
		if s.Contains(x, y) {
			return s
		}
	}
	return nil
}
