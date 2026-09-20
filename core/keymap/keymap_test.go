package keymap

import "testing"

func TestKeyByName(t *testing.T) {
	cases := []struct {
		name string
		want Key
	}{
		{"A", KeyA}, {"Z", KeyZ}, {"0", Key0}, {"Enter", KeyEnter},
		{"Escape", KeyEscape}, {"Space", KeySpace}, {"LeftShift", KeyLeftShift},
		{"F5", KeyF5}, {"Left", KeyLeft}, {"Delete", KeyDelete},
	}
	for _, c := range cases {
		k, ok := KeyByName(c.name)
		if !ok || k != c.want {
			t.Errorf("KeyByName(%q): got %v, %v; want %v", c.name, k, ok, c.want)
		}
	}
	if _, ok := KeyByName("NoSuchKey"); ok {
		t.Error("KeyByName(NoSuchKey) should be false")
	}
}

// TestNativeRoundTrip verifies that every key with a native code maps forward
// to that code, and that the reverse lookup is consistent: if another key
// shares the code (e.g. Enter vs KeypadEnter on Windows), it must map back to
// that same code too.
func TestNativeRoundTrip(t *testing.T) {
	for _, d := range keys {
		if d.mac != noCode {
			c, ok := MacKeyCode(d.k)
			if !ok || c != d.mac {
				t.Errorf("MacKeyCode(%v): got %#x (ok=%v), want %#x", d.k, c, ok, d.mac)
			}
			if k, ok := KeyFromMac(d.mac); ok && k != d.k {
				if c2, ok2 := MacKeyCode(k); !ok2 || c2 != d.mac {
					t.Errorf("mac %#x shared by %v and %v, but %v maps to %#x", d.mac, k, d.k, k, c2)
				}
			}
		}
		if d.win != noCode {
			c, ok := WindowsVK(d.k)
			if !ok || c != d.win {
				t.Errorf("WindowsVK(%v): got %#x (ok=%v), want %#x", d.k, c, ok, d.win)
			}
			if k, ok := KeyFromWindows(d.win); ok && k != d.k {
				if c2, ok2 := WindowsVK(k); !ok2 || c2 != d.win {
					t.Errorf("win %#x shared by %v and %v, but %v maps to %#x", d.win, k, d.k, k, c2)
				}
			}
		}
	}
}

// TestMacZeroKeycodeIsA guards the macOS 'A' key: its virtual keycode is
// 0x00, which used to collide with the "no native code" sentinel and made the
// key — and shortcuts like Cmd+A — silently dead when forwarding to Windows.
func TestMacZeroKeycodeIsA(t *testing.T) {
	k, ok := KeyFromMac(0x00)
	if !ok || k != KeyA {
		t.Fatalf("KeyFromMac(0x00): got %v (ok=%v), want KeyA", k, ok)
	}
	vk, ok := WindowsVK(k)
	if !ok || vk != 0x41 {
		t.Fatalf("WindowsVK(A): got %#x (ok=%v), want 0x41", vk, ok)
	}
	if code, ok := MacKeyCode(KeyA); !ok || code != 0x00 {
		t.Fatalf("MacKeyCode(A): got %#x (ok=%v), want 0x00", code, ok)
	}
	// A Windows-only key must not claim the macOS 0x00 code.
	if _, ok := KeyFromMac(0xFFFF); ok {
		t.Error("KeyFromMac(0xFFFF) should not resolve (sentinel leaked into the map)")
	}
}

// TestNameRoundTrip checks that every key with a name resolves back to itself.
func TestNameRoundTrip(t *testing.T) {
	for _, d := range keys {
		if d.k.Name() != d.name {
			t.Errorf("Name() of %v: got %q, want %q", d.k, d.k.Name(), d.name)
		}
		if k, ok := KeyByName(d.name); !ok || k != d.k {
			t.Errorf("KeyByName(%q): got %v (ok=%v), want %v", d.name, k, ok, d.k)
		}
	}
}
