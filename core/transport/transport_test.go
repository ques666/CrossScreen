package transport

import (
	"net"
	"testing"
	"time"

	"crossscreen/core/protocol"
)

func TestConnRoundTrip(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	ca, cb := NewConn(a), NewConn(b)

	env, err := protocol.NewEnvelope(protocol.TypeHello, 7, &protocol.Hello{Hostname: "x"})
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	go func() {
		if err := ca.WriteEnvelope(env); err != nil {
			t.Errorf("WriteEnvelope: %v", err)
		}
	}()

	got, err := cb.ReadEnvelope()
	if err != nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	if got.Type != protocol.TypeHello || got.Seq != 7 {
		t.Fatalf("mismatch: %+v", got)
	}
}

func TestServerEcho(t *testing.T) {
	s, err := Listen("127.0.0.1:0", nil, func(c *Conn, env *protocol.Envelope) error {
		return c.WriteEnvelope(env) // echo back
	}, ConnCallbacks{})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go s.Serve()
	defer s.Close()

	cl, err := Dial(s.Addr().String(), nil, 2*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer cl.Close()

	env, _ := protocol.NewEnvelope(protocol.TypeHello, 1, nil)
	if err := cl.Conn.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	got, err := cl.Conn.ReadEnvelope()
	if err != nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	if got.Type != protocol.TypeHello {
		t.Fatalf("echo mismatch: got %s", got.Type)
	}
}

func TestServerCloseClosesConnections(t *testing.T) {
	connected := make(chan *Conn, 1)
	s, err := Listen("127.0.0.1:0", nil, func(c *Conn, env *protocol.Envelope) error {
		return nil
	}, ConnCallbacks{OnConnect: func(c *Conn) { connected <- c }})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go s.Serve()

	cl, err := Dial(s.Addr().String(), nil, 2*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer cl.Close()

	select {
	case <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("connection never accepted")
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// The server closed the accepted side, so the client read must fail.
	cl.Conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := cl.Conn.ReadEnvelope(); err == nil {
		t.Fatal("want read error after server close")
	}
}
