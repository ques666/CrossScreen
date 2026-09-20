//go:build windows

package winsec

import (
	"os"
	"syscall"
)

// osNewFile wraps a raw handle in an *os.File so net.FileConn can serve the
// named pipe as a standard connection.
func osNewFile(h uintptr) *os.File {
	return os.NewFile(uintptr(h), PipeName)
}

var _ = syscall.InvalidHandle
