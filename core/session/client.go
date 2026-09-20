package session

import (
	"crypto/tls"
	"fmt"
	"sync"
	"time"

	"crossscreen/core/event"
	"crossscreen/core/layout"
	"crossscreen/core/protocol"
	"crossscreen/core/transport"
)

// Client is a device connecting to a session server (a desktop client or a
// dedicated input source).
type Client struct {
	conn        *transport.Conn
	hello       *protocol.Hello
	serverHello *protocol.Hello
	sink        EventSink
	layoutHook  func(*layout.Layout)

	mu        sync.RWMutex
	layout    *layout.Layout
	seq       uint64
	closedCh  chan struct{}
	closeOnce sync.Once
}

// Closed returns a channel closed when the receive loop exits (server went
// away, network error, or local Close).
func (c *Client) Closed() <-chan struct{} { return c.closedCh }

// Dial connects to a session server and completes the handshake.
func Dial(addr string, hello *protocol.Hello, timeout time.Duration) (*Client, error) {
	return DialTLS(addr, hello, nil, timeout)
}

// DialTLS connects over TLS; tlsCfg may be nil for plaintext.
func DialTLS(addr string, hello *protocol.Hello, tlsCfg *tls.Config, timeout time.Duration) (*Client, error) {
	c, err := transport.Dial(addr, tlsCfg, timeout)
	if err != nil {
		return nil, err
	}
	cl := &Client{conn: c.Conn, hello: hello, closedCh: make(chan struct{})}
	if err := cl.handshake(); err != nil {
		c.Close()
		return nil, err
	}
	return cl, nil
}

func (c *Client) handshake() error {
	env, err := protocol.NewEnvelope(protocol.TypeHello, c.nextSeq(), c.hello)
	if err != nil {
		return err
	}
	if err := c.conn.WriteEnvelope(env); err != nil {
		return err
	}
	ack, err := c.conn.ReadEnvelope()
	if err != nil {
		return err
	}
	if ack.Type != protocol.TypeHelloAck {
		return fmt.Errorf("session: expected hello_ack, got %s", ack.Type)
	}
	var h protocol.Hello
	if err := ack.Decode(&h); err != nil {
		return err
	}
	c.serverHello = &h
	return nil
}

// SetSink registers the receiver for events that must be applied locally
// (input injection, clipboard). May be nil.
func (c *Client) SetSink(sink EventSink) { c.sink = sink }

// SetLayoutHook registers a callback invoked whenever the authoritative
// layout is received from the server.
func (c *Client) SetLayoutHook(fn func(*layout.Layout)) { c.layoutHook = fn }

// ServerHello returns the server's handshake information.
func (c *Client) ServerHello() *protocol.Hello { return c.serverHello }

// Layout returns the authoritative layout last received from the server.
func (c *Client) Layout() *layout.Layout {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.layout
}

// SendLayout reports this device's screens to the server.
func (c *Client) SendLayout(screens []*layout.Screen) error {
	env, err := protocol.NewEnvelope(protocol.TypeLayout, c.nextSeq(), &layout.Layout{Version: 1, Screens: screens})
	if err != nil {
		return err
	}
	return c.conn.WriteEnvelope(env)
}

// SendPointerMove sends a pointer move in virtual coordinates.
func (c *Client) SendPointerMove(vx, vy int32) error {
	env, err := protocol.NewEnvelope(protocol.TypePointerMove, c.nextSeq(), event.PointerMove{X: vx, Y: vy})
	if err != nil {
		return err
	}
	return c.conn.WriteEnvelope(env)
}

// SendButton sends a mouse button event.
func (c *Client) SendButton(b event.PointerButton) error {
	env, err := protocol.NewEnvelope(protocol.TypePointerBtn, c.nextSeq(), b)
	if err != nil {
		return err
	}
	return c.conn.WriteEnvelope(env)
}

// SendScroll sends a scroll event.
func (c *Client) SendScroll(s event.PointerScroll) error {
	env, err := protocol.NewEnvelope(protocol.TypePointerScroll, c.nextSeq(), s)
	if err != nil {
		return err
	}
	return c.conn.WriteEnvelope(env)
}

// SendKey sends a keyboard event.
func (c *Client) SendKey(k event.KeyEvent) error {
	env, err := protocol.NewEnvelope(protocol.TypeKeyboard, c.nextSeq(), k)
	if err != nil {
		return err
	}
	return c.conn.WriteEnvelope(env)
}

// SendClipboard sends a clipboard change to be shared with the session.
func (c *Client) SendClipboard(cl event.ClipboardEvent) error {
	env, err := protocol.NewEnvelope(protocol.TypeClipboard, c.nextSeq(), cl)
	if err != nil {
		return err
	}
	return c.conn.WriteEnvelope(env)
}

// SendEnvelope writes a pre-built envelope (used by file transfer control
// messages; the server relays by the payload's "to" field when needed).
func (c *Client) SendEnvelope(env *protocol.Envelope) error {
	return c.conn.WriteEnvelope(env)
}

// SendPing sends a liveness probe.
func (c *Client) SendPing() error {
	env, err := protocol.NewEnvelope(protocol.TypePing, c.nextSeq(), nil)
	if err != nil {
		return err
	}
	return c.conn.WriteEnvelope(env)
}

// Serve runs the receive loop until the connection closes.
func (c *Client) Serve() error {
	defer c.closeOnce.Do(func() { close(c.closedCh) })
	for {
		env, err := c.conn.ReadEnvelope()
		if err != nil {
			return err
		}
		if err := c.dispatch(env); err != nil {
			return err
		}
	}
}

func (c *Client) dispatch(env *protocol.Envelope) error {
	switch env.Type {
	case protocol.TypeLayout:
		var l layout.Layout
		if err := env.Decode(&l); err != nil {
			return err
		}
		c.mu.Lock()
		c.layout = &l
		hook := c.layoutHook
		c.mu.Unlock()
		if hook != nil {
			hook(&l)
		}
	case protocol.TypePointerMove, protocol.TypePointerBtn, protocol.TypePointerScroll,
		protocol.TypeKeyboard, protocol.TypeClipboard,
		protocol.TypeFileAccept, protocol.TypeFileChunk, protocol.TypeFileCancel, protocol.TypeFileDone:
		if c.sink != nil {
			return c.sink(env)
		}
	case protocol.TypePing:
		pong, err := protocol.NewEnvelope(protocol.TypePong, c.nextSeq(), nil)
		if err != nil {
			return err
		}
		return c.conn.WriteEnvelope(pong)
	case protocol.TypeBye:
		return c.conn.Close()
	}
	return nil
}

// Close closes the connection.
func (c *Client) Close() error {
	c.closeOnce.Do(func() { close(c.closedCh) })
	return c.conn.Close()
}

func (c *Client) nextSeq() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	return c.seq
}
