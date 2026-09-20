//go:build windows

// Secure desktop agent: launched by the CrossScreen service as SYSTEM inside
// the logged-in user's session. It owns the named pipe and performs input
// injection from an OS thread attached to the current input desktop, so
// SendInput reaches the Winlogon/LogonUI secure desktop while the machine is
// locked.
package winsec

import (
	"errors"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	"crossscreen/platform/inject"
)

// RunAgent runs the secure-input agent until stop is closed. It is invoked
// only via "CrossScreen.exe agent" by the SYSTEM service.
func RunAgent(stop <-chan struct{}) error {
	appendAgentLog("agent: start pid=%d", os.Getpid())
	defer appendAgentLog("agent: exit")

	// Match the interactive app's DPI awareness so absolute mouse
	// coordinates use the same physical-pixel virtual-screen metrics. No
	// elevation: the agent already runs as SYSTEM under the service.
	inject.Startup(false)

	inj, err := inject.New()
	if err != nil {
		appendAgentLog("agent: inject.New: %v", err)
		return err
	}

	// One OS thread performs all injection. Pipe readers feed events into
	// injectCh; the worker keeps its thread attached to whatever desktop is
	// currently receiving input.
	injectCh := make(chan Msg, 512)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		injectWorker(inj, injectCh, stop)
	}()

	for {
		ln, err := NewPipeListener()
		if err != nil {
			// Another instance already owns the pipe.
			appendAgentLog("agent: NewPipeListener: %v", err)
			return err
		}
		connCh := make(chan *os.File, 1)
		errCh := make(chan error, 1)
		go func() {
			c, err := ln.Accept()
			if err != nil {
				errCh <- err
				return
			}
			connCh <- c
		}()

		select {
		case <-stop:
			ln.Close()
			close(injectCh)
			wg.Wait()
			return nil
		case err := <-errCh:
			ln.Close()
			if errors.Is(err, io.ErrClosedPipe) {
				return nil
			}
			appendAgentLog("agent: accept: %v", err)
			time.Sleep(time.Second)
		case conn := <-connCh:
			serveAgentConn(conn, injectCh, stop)
			conn.Close()
			ln.Close()
			// Drain anything queued from the dropped connection before the
			// app reconnects.
			drainInject(injectCh)
		}
	}
}

func drainInject(ch <-chan Msg) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func serveAgentConn(conn *os.File, out chan<- Msg, stop <-chan struct{}) {
	done := make(chan struct{})
	go func() {
		select {
		case <-stop:
			conn.Close()
		case <-done:
		}
	}()
	defer close(done)

	for {
		m, err := ReadMsg(conn)
		if err != nil {
			appendAgentLog("agent: read error: %v", err)
			return
		}
		select {
		case out <- m:
		case <-stop:
			return
		default:
			// Backpressure: the inject worker cannot keep up (very unlikely
			// for human input); drop the event rather than stall the pipe.
		}
	}
}

// injectWorker performs every SendInput on one pinned OS thread. The agent
// process is created on the Winlogon desktop (spawnAgentInSession), so the
// thread already belongs to the secure desktop and no SetThreadDesktop dance
// is needed — that approach is silently dropped on modern Windows.
func injectWorker(inj inject.Injector, in <-chan Msg, stop <-chan struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	for {
		select {
		case <-stop:
			return
		case m, ok := <-in:
			if !ok {
				return
			}
			if err := applyMsg(inj, m); err != nil {
				appendAgentLog("agent: inject %s: %v", m.T, err)
			}
		}
	}
}
