// Package keymap defines the neutral cross-platform keycode space used by
// events on the wire, plus conversions to/from native keycodes.
//
// Neutral codes are USB HID keyboard usage IDs (0x04 = 'a', 0x28 = Enter,
// 0xE0-0xE7 = modifiers), which are the canonical representation for mapping
// between macOS virtual keycodes and Windows virtual-key codes.
package keymap

// Key is a neutral cross-platform keycode (USB HID usage ID).
type Key uint16

// Alphanumeric and punctuation.
const (
	KeyA Key = 0x04
	KeyB Key = 0x05
	KeyC Key = 0x06
	KeyD Key = 0x07
	KeyE Key = 0x08
	KeyF Key = 0x09
	KeyG Key = 0x0A
	KeyH Key = 0x0B
	KeyI Key = 0x0C
	KeyJ Key = 0x0D
	KeyK Key = 0x0E
	KeyL Key = 0x0F
	KeyM Key = 0x10
	KeyN Key = 0x11
	KeyO Key = 0x12
	KeyP Key = 0x13
	KeyQ Key = 0x14
	KeyR Key = 0x15
	KeyS Key = 0x16
	KeyT Key = 0x17
	KeyU Key = 0x18
	KeyV Key = 0x19
	KeyW Key = 0x1A
	KeyX Key = 0x1B
	KeyY Key = 0x1C
	KeyZ Key = 0x1D

	Key1 Key = 0x1E
	Key2 Key = 0x1F
	Key3 Key = 0x20
	Key4 Key = 0x21
	Key5 Key = 0x22
	Key6 Key = 0x23
	Key7 Key = 0x24
	Key8 Key = 0x25
	Key9 Key = 0x26
	Key0 Key = 0x27

	KeyEnter        Key = 0x28
	KeyEscape       Key = 0x29
	KeyBackspace    Key = 0x2A
	KeyTab          Key = 0x2B
	KeySpace        Key = 0x2C
	KeyMinus        Key = 0x2D
	KeyEqual        Key = 0x2E
	KeyLeftBracket  Key = 0x2F
	KeyRightBracket Key = 0x30
	KeyBackslash    Key = 0x31
	KeySemicolon    Key = 0x33
	KeyQuote        Key = 0x34
	KeyGrave        Key = 0x35
	KeyComma        Key = 0x36
	KeyPeriod       Key = 0x37
	KeySlash        Key = 0x38
	KeyCapsLock     Key = 0x39
)

// Function keys.
const (
	KeyF1  Key = 0x3A
	KeyF2  Key = 0x3B
	KeyF3  Key = 0x3C
	KeyF4  Key = 0x3D
	KeyF5  Key = 0x3E
	KeyF6  Key = 0x3F
	KeyF7  Key = 0x40
	KeyF8  Key = 0x41
	KeyF9  Key = 0x42
	KeyF10 Key = 0x43
	KeyF11 Key = 0x44
	KeyF12 Key = 0x45
)

// Navigation and editing.
const (
	KeyPrintScreen Key = 0x46
	KeyScrollLock  Key = 0x47
	KeyPause       Key = 0x48
	KeyInsert      Key = 0x49
	KeyHome        Key = 0x4A
	KeyPageUp      Key = 0x4B
	KeyDelete      Key = 0x4C
	KeyEnd         Key = 0x4D
	KeyPageDown    Key = 0x4E
	KeyRight       Key = 0x4F
	KeyLeft        Key = 0x50
	KeyDown        Key = 0x51
	KeyUp          Key = 0x52
	KeyNumLock     Key = 0x53
)

// Keypad.
const (
	KeypadSlash    Key = 0x54
	KeypadAsterisk Key = 0x55
	KeypadMinus    Key = 0x56
	KeypadPlus     Key = 0x57
	KeypadEnter    Key = 0x58
	Keypad1        Key = 0x59
	Keypad2        Key = 0x5A
	Keypad3        Key = 0x5B
	Keypad4        Key = 0x5C
	Keypad5        Key = 0x5D
	Keypad6        Key = 0x5E
	Keypad7        Key = 0x5F
	Keypad8        Key = 0x60
	Keypad9        Key = 0x61
	Keypad0        Key = 0x62
	KeypadDecimal  Key = 0x63
)

// Modifiers.
const (
	KeyLeftCtrl   Key = 0xE0
	KeyLeftShift  Key = 0xE1
	KeyLeftAlt    Key = 0xE2
	KeyLeftGUI    Key = 0xE3
	KeyRightCtrl  Key = 0xE4
	KeyRightShift Key = 0xE5
	KeyRightAlt   Key = 0xE6
	KeyRightGUI   Key = 0xE7
)

// noCode marks a key that has no native code on a platform. It must not be
// zero: macOS virtual keycode 0x00 is the 'A' key, so using 0 as the sentinel
// silently dropped 'A' (and every shortcut built on it, e.g. Cmd+A).
const noCode = 0xFFFF

// keyDef is the single source of truth for every supported key: its neutral
// code, its name (used by KeyByName and demos), and its native codes.
type keyDef struct {
	name string
	k    Key
	mac  uint16
	win  uint16
}

var keys = []keyDef{
	{"A", KeyA, 0x00, 0x41}, {"S", KeyS, 0x01, 0x53}, {"D", KeyD, 0x02, 0x44},
	{"F", KeyF, 0x03, 0x46}, {"H", KeyH, 0x04, 0x48}, {"G", KeyG, 0x05, 0x47},
	{"Z", KeyZ, 0x06, 0x5A}, {"X", KeyX, 0x07, 0x58}, {"C", KeyC, 0x08, 0x43},
	{"V", KeyV, 0x09, 0x56}, {"B", KeyB, 0x0B, 0x42}, {"Q", KeyQ, 0x0C, 0x51},
	{"W", KeyW, 0x0D, 0x57}, {"E", KeyE, 0x0E, 0x45}, {"R", KeyR, 0x0F, 0x52},
	{"Y", KeyY, 0x10, 0x59}, {"T", KeyT, 0x11, 0x54}, {"1", Key1, 0x12, 0x31},
	{"2", Key2, 0x13, 0x32}, {"3", Key3, 0x14, 0x33}, {"4", Key4, 0x15, 0x34},
	{"6", Key6, 0x16, 0x36}, {"5", Key5, 0x17, 0x35}, {"=", KeyEqual, 0x18, 0xBB},
	{"9", Key9, 0x19, 0x39}, {"7", Key7, 0x1A, 0x37}, {"-", KeyMinus, 0x1B, 0xBD},
	{"8", Key8, 0x1C, 0x38}, {"0", Key0, 0x1D, 0x30}, {"]", KeyRightBracket, 0x1E, 0xDD},
	{"O", KeyO, 0x1F, 0x4F}, {"U", KeyU, 0x20, 0x55}, {"[", KeyLeftBracket, 0x21, 0xDB},
	{"I", KeyI, 0x22, 0x49}, {"P", KeyP, 0x23, 0x50}, {"Enter", KeyEnter, 0x24, 0x0D},
	{"L", KeyL, 0x25, 0x4C}, {"J", KeyJ, 0x26, 0x4A}, {"'", KeyQuote, 0x27, 0xDE},
	{"K", KeyK, 0x28, 0x4B}, {";", KeySemicolon, 0x29, 0xBA}, {"\\", KeyBackslash, 0x2A, 0xDC},
	{",", KeyComma, 0x2B, 0xBC}, {"/", KeySlash, 0x2C, 0xBF}, {"N", KeyN, 0x2D, 0x4E},
	{"M", KeyM, 0x2E, 0x4D}, {".", KeyPeriod, 0x2F, 0xBE}, {"Tab", KeyTab, 0x30, 0x09},
	{"Space", KeySpace, 0x31, 0x20}, {"`", KeyGrave, 0x32, 0xC0}, {"Backspace", KeyBackspace, 0x33, 0x08},
	{"Escape", KeyEscape, 0x35, 0x1B},

	{"LeftGUI", KeyLeftGUI, 0x37, 0x5B}, {"RightGUI", KeyRightGUI, 0x36, 0x5C},
	{"LeftShift", KeyLeftShift, 0x38, 0xA0}, {"RightShift", KeyRightShift, 0x3C, 0xA1},
	{"CapsLock", KeyCapsLock, 0x39, 0x14}, {"LeftAlt", KeyLeftAlt, 0x3A, 0xA4},
	{"RightAlt", KeyRightAlt, 0x3D, 0xA5}, {"LeftCtrl", KeyLeftCtrl, 0x3B, 0xA2},
	{"RightCtrl", KeyRightCtrl, 0x3E, 0xA3},

	{"F1", KeyF1, 0x7A, 0x70}, {"F2", KeyF2, 0x78, 0x71}, {"F3", KeyF3, 0x63, 0x72},
	{"F4", KeyF4, 0x76, 0x73}, {"F5", KeyF5, 0x60, 0x74}, {"F6", KeyF6, 0x61, 0x75},
	{"F7", KeyF7, 0x62, 0x76}, {"F8", KeyF8, 0x64, 0x77}, {"F9", KeyF9, 0x65, 0x78},
	{"F10", KeyF10, 0x6D, 0x79}, {"F11", KeyF11, 0x67, 0x7A}, {"F12", KeyF12, 0x6F, 0x7B},

	{"Insert", KeyInsert, 0x72, 0x2D}, {"Home", KeyHome, 0x73, 0x24},
	{"PageUp", KeyPageUp, 0x74, 0x21}, {"Delete", KeyDelete, 0x75, 0x2E},
	{"End", KeyEnd, 0x77, 0x23}, {"PageDown", KeyPageDown, 0x79, 0x22},
	{"Left", KeyLeft, 0x7B, 0x25}, {"Right", KeyRight, 0x7C, 0x27},
	{"Down", KeyDown, 0x7D, 0x28}, {"Up", KeyUp, 0x7E, 0x26},

	{"PrintScreen", KeyPrintScreen, noCode, 0x2C}, {"ScrollLock", KeyScrollLock, noCode, 0x91},
	{"Pause", KeyPause, noCode, 0x13}, {"NumLock", KeyNumLock, noCode, 0x90},

	{"KeypadSlash", KeypadSlash, noCode, 0x6F}, {"KeypadAsterisk", KeypadAsterisk, noCode, 0x6A},
	{"KeypadMinus", KeypadMinus, noCode, 0x6D}, {"KeypadPlus", KeypadPlus, noCode, 0x6B},
	{"KeypadEnter", KeypadEnter, 0x4C, 0x0D}, {"Keypad0", Keypad0, noCode, 0x60},
	{"Keypad1", Keypad1, noCode, 0x61}, {"Keypad2", Keypad2, noCode, 0x62},
	{"Keypad3", Keypad3, noCode, 0x63}, {"Keypad4", Keypad4, noCode, 0x64},
	{"Keypad5", Keypad5, noCode, 0x65}, {"Keypad6", Keypad6, noCode, 0x66},
	{"Keypad7", Keypad7, noCode, 0x67}, {"Keypad8", Keypad8, noCode, 0x68},
	{"Keypad9", Keypad9, noCode, 0x69}, {"KeypadDecimal", KeypadDecimal, noCode, 0x6E},
}

var (
	macKey   map[Key]uint16
	macToKey map[uint16]Key
	winKey   map[Key]uint16
	winToKey map[uint16]Key
	keyNames map[string]Key
)

func init() {
	macKey = make(map[Key]uint16, len(keys))
	macToKey = make(map[uint16]Key, len(keys))
	winKey = make(map[Key]uint16, len(keys))
	winToKey = make(map[uint16]Key, len(keys))
	keyNames = make(map[string]Key, len(keys))
	for _, d := range keys {
		keyNames[d.name] = d.k
		if d.mac != noCode {
			if _, exists := macKey[d.k]; !exists {
				macKey[d.k] = d.mac
			}
			// Reverse maps are first-wins: if two keys share a native code
			// (e.g. Enter vs KeypadEnter on Windows), the canonical one wins.
			if _, exists := macToKey[d.mac]; !exists {
				macToKey[d.mac] = d.k
			}
		}
		if d.win != noCode {
			if _, exists := winKey[d.k]; !exists {
				winKey[d.k] = d.win
			}
			if _, exists := winToKey[d.win]; !exists {
				winToKey[d.win] = d.k
			}
		}
	}
}

// MacKeyCode converts a neutral key to a macOS virtual keycode.
func MacKeyCode(k Key) (uint16, bool) {
	c, ok := macKey[k]
	return c, ok
}

// KeyFromMac converts a macOS virtual keycode to a neutral key.
func KeyFromMac(code uint16) (Key, bool) {
	k, ok := macToKey[code]
	return k, ok
}

// WindowsVK converts a neutral key to a Windows virtual-key code.
func WindowsVK(k Key) (uint16, bool) {
	c, ok := winKey[k]
	return c, ok
}

// KeyFromWindows converts a Windows virtual-key code to a neutral key.
func KeyFromWindows(vk uint16) (Key, bool) {
	k, ok := winToKey[vk]
	return k, ok
}

// KeyByName returns a key by its identifier, e.g. "A", "Enter", "LeftArrow".
func KeyByName(name string) (Key, bool) {
	k, ok := keyNames[name]
	return k, ok
}

// Name returns the identifier for a key ("" if unknown).
func (k Key) Name() string {
	for name, kk := range keyNames {
		if kk == k {
			return name
		}
	}
	return ""
}
