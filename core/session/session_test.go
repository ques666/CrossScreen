package session

import (
	"net"
	"testing"
	"time"

	"crossscreen/core/event"
	"crossscreen/core/layout"
	"crossscreen/core/protocol"
	"crossscreen/core/transport"
)

func testHello(name, os string, role protocol.Role) *protocol.Hello {
	return &protocol.Hello{Hostname: name, OS: os, Role: role, Version: "test"}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func testLayout() *layout.Layout {
	return &layout.Layout{Version: 1, Screens: []*layout.Screen{
		{ID: "mac:0", Device: "mac", X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true},
		{ID: "win:0", Device: "win", X: 1920, Y: 0, Width: 1280, Height: 800},
	}}
}

func startServer(t *testing.T, sink EventSink) (*Server, net.Addr) {
	t.Helper()
	srv := NewServer(testHello("mac", "macos", protocol.RoleServer), sink)
	addr, err := srv.Listen("127.0.0.1:0", nil)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { srv.Close() })
	return srv, addr
}

func newClient(t *testing.T, addr string, hello *protocol.Hello) *Client {
	t.Helper()
	cl, err := Dial(addr, hello, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	go cl.Serve()
	t.Cleanup(func() { cl.Close() })
	return cl
}

func TestHandshakeAndLayoutDistribution(t *testing.T) {
	srv, addr := startServer(t, nil)
	cl := newClient(t, addr.String(), testHello("win", "windows", protocol.RoleClient))

	if err := srv.SetLayout(testLayout()); err != nil {
		t.Fatalf("SetLayout: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		got := cl.Layout()
		return got != nil && len(got.Screens) == 2
	})
	if cl.ServerHello().Hostname != "mac" {
		t.Fatalf("server hello mismatch: %+v", cl.ServerHello())
	}
}

func TestServerRoutesPointerToClient(t *testing.T) {
	srv, addr := startServer(t, nil)
	got := make(chan *protocol.Envelope, 8)
	cl := newClient(t, addr.String(), testHello("win", "windows", protocol.RoleClient))
	cl.SetSink(func(env *protocol.Envelope) error {
		got <- env
		return nil
	})

	l := testLayout()
	if err := srv.SetLayout(l); err != nil {
		t.Fatalf("SetLayout: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return cl.Layout() != nil })

	mac := l.Screens[0]
	if err := srv.OnLocalPointerMove(mac, 1919, 400); err != nil {
		t.Fatalf("OnLocalPointerMove: %v", err)
	}
	if err := srv.OnLocalPointerMove(mac, 1921, 400); err != nil {
		t.Fatalf("OnLocalPointerMove: %v", err)
	}

	select {
	case env := <-got:
		if env.Type != protocol.TypePointerMove {
			t.Fatalf("want pointer_move, got %s", env.Type)
		}
		var m event.PointerMove
		if err := env.Decode(&m); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if m.X != 1921 || m.Y != 400 {
			t.Fatalf("coords mismatch: %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client never received the pointer event")
	}
}

func TestClientRoutesToServerWhenActiveLocal(t *testing.T) {
	sinkCh := make(chan *protocol.Envelope, 8)
	srv := NewServer(testHello("mac", "macos", protocol.RoleServer), func(env *protocol.Envelope) error {
		sinkCh <- env
		return nil
	})
	addr, err := srv.Listen("127.0.0.1:0", nil)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { srv.Close() })

	cl := newClient(t, addr.String(), testHello("win", "windows", protocol.RoleClient))
	if err := srv.SetLayout(testLayout()); err != nil {
		t.Fatalf("SetLayout: %v", err)
	}

	// Cursor starts at (0,0) on mac; the client's move lands on mac → server sink.
	if err := cl.SendPointerMove(100, 100); err != nil {
		t.Fatalf("SendPointerMove: %v", err)
	}
	select {
	case env := <-sinkCh:
		if env.Type != protocol.TypePointerMove {
			t.Fatalf("want pointer_move at sink, got %s", env.Type)
		}
		var m event.PointerMove
		if err := env.Decode(&m); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if m.X != 100 || m.Y != 100 {
			t.Fatalf("coords mismatch: %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server sink never received the event")
	}
}

func TestClipboardBroadcast(t *testing.T) {
	sinkCh := make(chan *protocol.Envelope, 8)
	srv := NewServer(testHello("mac", "macos", protocol.RoleServer), func(env *protocol.Envelope) error {
		sinkCh <- env
		return nil
	})
	addr, err := srv.Listen("127.0.0.1:0", nil)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { srv.Close() })

	cl1 := newClient(t, addr.String(), testHello("win1", "windows", protocol.RoleClient))
	got2 := make(chan *protocol.Envelope, 8)
	cl2 := newClient(t, addr.String(), testHello("win2", "windows", protocol.RoleClient))
	cl2.SetSink(func(env *protocol.Envelope) error {
		got2 <- env
		return nil
	})

	payload := event.ClipboardEvent{Mime: "text/plain", Data: []byte("hello")}
	if err := cl1.SendClipboard(payload); err != nil {
		t.Fatalf("SendClipboard: %v", err)
	}

	// The server sink receives the change…
	select {
	case env := <-sinkCh:
		if env.Type != protocol.TypeClipboard {
			t.Fatalf("want clipboard at sink, got %s", env.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server sink never received clipboard")
	}
	// …and so does the other client (cl1 is excluded).
	select {
	case env := <-got2:
		if env.Type != protocol.TypeClipboard {
			t.Fatalf("want clipboard at cl2, got %s", env.Type)
		}
		var c event.ClipboardEvent
		if err := env.Decode(&c); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if string(c.Data) != "hello" {
			t.Fatalf("clipboard payload mismatch: %q", c.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cl2 never received clipboard")
	}
}

func TestPeerScreensRecorded(t *testing.T) {
	srv, addr := startServer(t, nil)
	cl := newClient(t, addr.String(), testHello("win", "windows", protocol.RoleClient))

	screens := []*layout.Screen{{ID: "win:0", Device: "win", Width: 1280, Height: 800}}
	if err := cl.SendLayout(screens); err != nil {
		t.Fatalf("SendLayout: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		for _, p := range srv.peers {
			if len(p.screens) == 1 && p.screens[0].ID == "win:0" {
				return true
			}
		}
		return false
	})
}

func TestPingPongRawTransport(t *testing.T) {
	srv, addr := startServer(t, nil)
	_ = srv

	cl, err := transport.Dial(addr.String(), nil, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cl.Close()

	env, _ := protocol.NewEnvelope(protocol.TypePing, 9, nil)
	if err := cl.Conn.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	resp, err := cl.Conn.ReadEnvelope()
	if err != nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	if resp.Type != protocol.TypePong {
		t.Fatalf("want pong, got %s", resp.Type)
	}
}
