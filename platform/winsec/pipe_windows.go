//go:build windows

package winsec

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	advapi32                 = syscall.NewLazyDLL("advapi32.dll")
	procCreateNamedPipeW     = kernel32.NewProc("CreateNamedPipeW")
	procConnectNamedPipe     = kernel32.NewProc("ConnectNamedPipe")
	procWaitNamedPipeW       = kernel32.NewProc("WaitNamedPipeW")
	procCreateFileW          = kernel32.NewProc("CreateFileW")
	procConvertStringSDToSDW = advapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	procLocalFree            = kernel32.NewProc("LocalFree")
	procCloseHandle          = kernel32.NewProc("CloseHandle")
)

const (
	pipeAccessDuplex   = 0x00000003
	fileFlagFirstPipe  = 0x00080000
	pipeTypeByte       = 0x00000000
	pipeWait           = 0x00000000
	pipeRejectRemote   = 0x00000008
	genericReadWrite   = 0xC0000000
	openExisting       = 3
	fileShareReadWrite = 0x00000003
	errorPipeBusy      = 231
)

// securityDescriptor grants full control to SYSTEM and Administrators only,
// so an unprivileged user process cannot inject keystrokes into the agent.
const pipeSecurityDescriptor = "D:P(A;;GA;;;SY)(A;;GA;;;BA)"

func makeSA() (*syscall.SecurityAttributes, func(), error) {
	var sd uintptr
	r, _, callErr := procConvertStringSDToSDW.Call(
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(pipeSecurityDescriptor))),
		1, // SDDL_REVISION_1
		uintptr(unsafe.Pointer(&sd)),
		0,
	)
	if r == 0 {
		return nil, nil, callErr
	}
	sa := &syscall.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(syscall.SecurityAttributes{})),
		SecurityDescriptor: uintptr(sd),
		InheritHandle:      0,
	}
	return sa, func() { procLocalFree.Call(sd) }, nil
}

// PipeListener serves the agent's control channel. Only one instance exists
// per machine; the agent accepts one connection at a time (the interactive
// app holds the single client connection).
type PipeListener struct {
	handle syscall.Handle
}

// NewPipeListener creates the first named-pipe instance and blocks waiting
// for a client to connect, returning a Go net.Conn-like wrapper (file conn).
func NewPipeListener() (*PipeListener, error) {
	sa, free, err := makeSA()
	if err != nil {
		return nil, err
	}
	defer free()

	name, _ := syscall.UTF16PtrFromString(PipeName)
	h, _, callErr := procCreateNamedPipeW.Call(
		uintptr(unsafe.Pointer(name)),
		pipeAccessDuplex|fileFlagFirstPipe,
		pipeTypeByte|pipeWait|pipeRejectRemote,
		1,            // single instance
		1<<20, 1<<20, // out/in buffers
		0, // default timeout
		uintptr(unsafe.Pointer(sa)),
	)
	if syscall.Handle(h) == syscall.InvalidHandle {
		return nil, callErr
	}
	return &PipeListener{handle: syscall.Handle(h)}, nil
}

// Accept blocks until a client connects. Ownership of the underlying handle
// transfers to the returned file; the listener must not be reused for it.
func (l *PipeListener) Accept() (*os.File, error) {
	r, _, callErr := procConnectNamedPipe.Call(uintptr(l.handle), 0)
	if r == 0 {
		// ERROR_PIPE_CONNECTED means a client connected between Create and
		// Connect; anything else is a real failure.
		if errno, ok := callErr.(syscall.Errno); !ok || errno != 535 {
			return nil, callErr
		}
	}
	f := osNewFile(uintptr(l.handle))
	l.handle = 0 // ownership transferred to the file
	return f, nil
}

func (l *PipeListener) Close() error {
	if l.handle != 0 {
		return syscall.CloseHandle(l.handle)
	}
	return nil
}

// ProbePipeState explains why a connection attempt fails, distinguishing an
// agent that is not listening (pipe missing) from an agent that is busy
// serving the app (pipe exists but already connected). Read-only-ish: a
// successful probe briefly connects to an idle pipe then closes it.
func ProbePipeState() string {
	name, _ := syscall.UTF16PtrFromString(PipeName)
	h, _, callErr := procCreateFileW.Call(
		uintptr(unsafe.Pointer(name)),
		genericReadWrite,
		fileShareReadWrite,
		0,
		openExisting,
		0,
		0,
	)
	if syscall.Handle(h) != syscall.InvalidHandle {
		procCloseHandle.Call(h)
		return "pipe exists (agent listening)"
	}
	if errno, ok := callErr.(syscall.Errno); ok {
		switch errno {
		case 2: // ERROR_FILE_NOT_FOUND
			return "pipe missing: agent not listening"
		case errorPipeBusy: // ERROR_PIPE_BUSY
			return "pipe exists but busy: the app already holds the single connection"
		default:
			return fmt.Sprintf("pipe probe error %d", errno)
		}
	}
	return "pipe probe failed"
}

// DialPipe connects to the agent with a short timeout and returns the pipe
// as an *os.File (named pipes are byte streams, not sockets — net.FileConn
// must not be used here).
func DialPipe(timeout time.Duration) (*os.File, error) {
	name, _ := syscall.UTF16PtrFromString(PipeName)
	waitName, _ := syscall.UTF16PtrFromString(PipeName)

	deadline := time.Now().Add(timeout)
	for {
		h, _, callErr := procCreateFileW.Call(
			uintptr(unsafe.Pointer(name)),
			genericReadWrite,
			fileShareReadWrite,
			0,
			openExisting,
			0,
			0,
		)
		if syscall.Handle(h) != syscall.InvalidHandle {
			return osNewFile(h), nil
		}
		if time.Now().After(deadline) {
			return nil, errors.New("winsec: secure agent pipe unavailable")
		}
		if errno, ok := callErr.(syscall.Errno); ok && errno == errorPipeBusy {
			procWaitNamedPipeW.Call(uintptr(unsafe.Pointer(waitName)), 500)
			continue
		}
		time.Sleep(200 * time.Millisecond)
	}
}
