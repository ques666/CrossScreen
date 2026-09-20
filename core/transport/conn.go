// Package transport provides connection-level frame I/O and TCP server/client
// helpers built on top of the core/protocol codec.
package transport

import (
	"net"
	"sync"
	"time"

	"crossscreen/core/protocol"
)

// Conn is a concurrency-safe frame connection: writes from multiple goroutines
// are serialized, and the read loop owns the reader exclusively.
type Conn struct {
	nc  net.Conn
	wmu sync.Mutex
	rmu sync.Mutex
}

// NewConn wraps an established net.Conn.
func NewConn(nc net.Conn) *Conn {
	return &Conn{nc: nc}
}

// WriteEnvelope marshals and sends one envelope.
func (c *Conn) WriteEnvelope(env *protocol.Envelope) error {
	payload, err := env.Marshal()
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return protocol.WriteFrame(c.nc, payload)
}

// ReadEnvelope reads and decodes one envelope.
func (c *Conn) ReadEnvelope() (*protocol.Envelope, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	payload, err := protocol.ReadFrame(c.nc)
	if err != nil {
		return nil, err
	}
	return protocol.DecodeEnvelope(payload)
}

func (c *Conn) SetDeadline(t time.Time) error { return c.nc.SetDeadline(t) }

func (c *Conn) Close() error { return c.nc.Close() }

// RemoteAddr returns the peer address.
func (c *Conn) RemoteAddr() net.Addr { return c.nc.RemoteAddr() }
