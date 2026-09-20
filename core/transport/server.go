package transport

import (
	"crypto/tls"
	"net"
	"sync"

	"crossscreen/core/protocol"
)

// ConnHandler processes an envelope received from c. Returning an error ends
// the connection.
type ConnHandler func(c *Conn, env *protocol.Envelope) error

// ConnCallbacks optionally observe connection lifecycle.
type ConnCallbacks struct {
	OnConnect    func(c *Conn)
	OnDisconnect func(c *Conn)
}

// Server accepts TCP clients and serves each connection on its own goroutine.
type Server struct {
	ln      net.Listener
	tlsCfg  *tls.Config // nil for plaintext
	handler ConnHandler
	cb      ConnCallbacks

	mu     sync.Mutex
	conns  map[*Conn]struct{}
	closed bool
}

// Listen creates a TCP listener. tlsCfg may be nil for plaintext.
func Listen(addr string, tlsCfg *tls.Config, handler ConnHandler, cb ConnCallbacks) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &Server{ln: ln, tlsCfg: tlsCfg, handler: handler, cb: cb, conns: map[*Conn]struct{}{}}, nil
}

// Addr returns the bound listener address (use with ":0").
func (s *Server) Addr() net.Addr { return s.ln.Addr() }

// Serve runs the accept loop until Close is called.
func (s *Server) Serve() error {
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			if s.isClosed() {
				return nil
			}
			return err
		}
		if s.tlsCfg != nil {
			tc := tls.Server(nc, s.tlsCfg)
			if err := tc.Handshake(); err != nil {
				nc.Close()
				continue
			}
			nc = tc
		} else {
			enableKeepAlive(nc)
		}
		c := NewConn(nc)
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			nc.Close()
			return nil
		}
		s.conns[c] = struct{}{}
		s.mu.Unlock()
		if s.cb.OnConnect != nil {
			s.cb.OnConnect(c)
		}
		go s.serveConn(c)
	}
}

func (s *Server) serveConn(c *Conn) {
	defer func() {
		s.remove(c)
		if s.cb.OnDisconnect != nil {
			s.cb.OnDisconnect(c)
		}
	}()
	for {
		env, err := c.ReadEnvelope()
		if err != nil {
			return
		}
		if err := s.handler(c, env); err != nil {
			return
		}
	}
}

func (s *Server) remove(c *Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	c.Close()
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Close stops the listener and closes all active connections.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()

	lnErr := s.ln.Close()

	s.mu.Lock()
	for c := range s.conns {
		c.Close()
	}
	s.conns = map[*Conn]struct{}{}
	s.mu.Unlock()
	return lnErr
}
