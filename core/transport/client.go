package transport

import (
	"crypto/tls"
	"net"
	"time"
)

// Client is a connected TCP (or TLS) client.
type Client struct {
	Conn *Conn
}

// Dial connects to addr. tlsCfg may be nil for plaintext.
func Dial(addr string, tlsCfg *tls.Config, timeout time.Duration) (*Client, error) {
	nc, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	if tlsCfg != nil {
		tc := tls.Client(nc, tlsCfg)
		if err := tc.Handshake(); err != nil {
			nc.Close()
			return nil, err
		}
		nc = tc
	} else {
		enableKeepAlive(nc)
	}
	return &Client{Conn: NewConn(nc)}, nil
}

// Close closes the underlying connection.
func (c *Client) Close() error { return c.Conn.Close() }
