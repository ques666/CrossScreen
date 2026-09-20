package discovery

import (
	"testing"
	"time"

	"crossscreen/core/protocol"
)

func TestDiscoveryRoundTrip(t *testing.T) {
	old := AnnounceInterval
	AnnounceInterval = 40 * time.Millisecond
	defer func() { AnnounceInterval = old }()

	ch, addr, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	b, err := NewBeacon(addr.String(), Announcement{
		Name: "mac", Host: "127.0.0.1", Port: 53318,
		OS: "macos", Role: protocol.RoleServer, Version: "1.0",
	})
	if err != nil {
		t.Fatalf("NewBeacon: %v", err)
	}
	defer b.Close()

	select {
	case a := <-ch:
		if a.Name != "mac" || a.OS != "macos" || a.Role != protocol.RoleServer || a.Port != 53318 {
			t.Fatalf("unexpected announcement: %+v", a)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no announcement received")
	}
}

func TestBeaconCloseStops(t *testing.T) {
	old := AnnounceInterval
	AnnounceInterval = 20 * time.Millisecond
	defer func() { AnnounceInterval = old }()

	ch, addr, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	b, err := NewBeacon(addr.String(), Announcement{Name: "x"})
	if err != nil {
		t.Fatalf("NewBeacon: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// After close, no more announcements should arrive.
	select {
	case a := <-ch:
		t.Fatalf("unexpected announcement after close: %+v", a)
	case <-time.After(120 * time.Millisecond):
	}
}
