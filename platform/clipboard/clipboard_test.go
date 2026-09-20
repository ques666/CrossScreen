package clipboard

import "testing"

// TestRoundTripPreservesClipboard verifies the platform clipboard writes and
// reads back text, restoring the previous content so the test does not
// disturb the user's clipboard. Skips on unsupported platforms.
func TestRoundTripPreservesClipboard(t *testing.T) {
	if err := platformInit(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	const probe = "crossscreen-clipboard-roundtrip-probe"

	old, hadOld, err := platformRead()
	if err != nil {
		t.Fatalf("read old: %v", err)
	}
	if err := platformWrite(Text(probe)); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, ok, err := platformRead()
	if err != nil || !ok {
		t.Fatalf("read back: ok=%v err=%v", ok, err)
	}
	if string(got.Data) != probe {
		t.Fatalf("round trip mismatch: %q", got.Data)
	}

	// Restore the previous clipboard.
	if hadOld {
		_ = platformWrite(old)
	} else {
		_ = platformWrite(Text(""))
	}
}

// TestEchoSuppression ensures a Write is not re-surfaced by the watcher.
func TestEchoSuppression(t *testing.T) {
	if err := platformInit(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	old, hadOld, _ := platformRead()
	defer func() {
		if hadOld {
			_ = platformWrite(old)
		} else {
			_ = platformWrite(Text(""))
		}
	}()

	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(Text("echo-suppress-probe")); err != nil {
		t.Fatal(err)
	}
	d, ok, err := c.Read()
	if err != nil || !ok {
		t.Fatalf("read: ok=%v err=%v", ok, err)
	}
	c.mu.Lock()
	own := len(c.lastWritten) > 0 && string(c.lastWritten) == string(d.Data)
	c.mu.Unlock()
	if !own {
		t.Fatal("Write did not mark content as own (echo suppression broken)")
	}
}
