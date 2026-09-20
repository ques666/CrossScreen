package transport

import (
	"net"
	"time"
)

// enableKeepAlive turns on TCP keepalive with a short probe period so a dead
// peer (laptop closed, network dropped) is detected promptly instead of a
// half-open connection lingering for the OS default (~hours).
func enableKeepAlive(nc net.Conn) {
	tc, ok := nc.(*net.TCPConn)
	if !ok {
		return
	}
	_ = tc.SetKeepAlive(true)
	_ = tc.SetKeepAlivePeriod(15 * time.Second)
}
