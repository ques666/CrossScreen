//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly

package discovery

import (
	"net"
	"syscall"
)

// setBroadcast enables SO_BROADCAST so the beacon can reach the subnet
// broadcast address.
func setBroadcast(conn *net.UDPConn) error {
	rc, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := rc.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	}); err != nil {
		return err
	}
	return serr
}
