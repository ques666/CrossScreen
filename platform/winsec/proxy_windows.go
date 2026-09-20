//go:build windows

// ProxyInjector is the interactive app's injector: while the session is
// unlocked it injects directly via SendInput (existing behavior); on the
// secure lock desktop it forwards neutral input events to the SYSTEM agent
// over the named pipe, which injects from a thread attached to Winlogon.
package winsec

import (
	"os"
	"sync"
	"sync/atomic"
	"time"

	"crossscreen/core/event"
	"crossscreen/core/keymap"
	"crossscreen/platform/inject"
)

// NewProxyInjector wraps the platform injector with secure-desktop routing.
func NewProxyInjector() (inject.Injector, error) {
	inner, err := inject.New()
	if err != nil {
		return nil, err
	}
	return &proxyInjector{inner: inner}, nil
}

type proxyInjector struct {
	inner inject.Injector

	mu   sync.Mutex
	conn *os.File

	// writeMu serializes frames to the pipe: forward() is called from the
	// input event path which is concurrent (mouse moves arrive far faster
	// than any single frame write), and a named pipe handle must not receive
	// concurrent writes.
	writeMu sync.Mutex

	// Secure-desktop state cache: the input desktop only changes on
	// lock/unlock, so querying it once per desktopCheckInterval avoids a
	// syscall storm on every high-frequency input event. Atomics keep the
	// per-event path lock-free.
	secure    atomic.Int32 // 0 = normal desktop, 1 = secure desktop
	checkedAt atomic.Int64 // unix nanos of last query

	// retryAt is the earliest time a fresh pipe dial may be attempted after
	// a failure. Without this, a locked machine with the service not running
	// would block every input event on DialPipe's timeout (severe lag).
	retryAt time.Time
}

// desktopCheckInterval bounds how often the input desktop is re-queried.
const desktopCheckInterval = 300 * time.Millisecond

// pipeDialTimeout bounds a single attempt to reach the agent. Short on
// purpose: failures fall back to direct injection immediately.
const pipeDialTimeout = 500 * time.Millisecond

// pipeRetryBackoff is how long to skip dialing after a failure, so a missing
// service cannot stall input.
const pipeRetryBackoff = 3 * time.Second

func (p *proxyInjector) routed() bool {
	now := time.Now().UnixNano()
	if now-p.checkedAt.Load() < int64(desktopCheckInterval) {
		return p.secure.Load() == 1
	}
	p.checkedAt.Store(now)

	name, err := inputDesktopName(desktopReadObjects)
	if err != nil {
		// The Winlogon secure desktop denies desktop access to user-mode
		// processes, so this query typically fails exactly when we most need
		// the pipe (machine locked). Treat an unreadable desktop as "locked":
		// try the agent pipe first, and fall back to direct injection only if
		// the service/agent is not running.
		p.secure.Store(1)
		return true
	}
	secure := name != defaultDesktopName
	if secure {
		p.secure.Store(1)
	} else {
		p.secure.Store(0)
	}
	return secure
}

func (p *proxyInjector) pipe() *os.File {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		return p.conn
	}
	if time.Now().Before(p.retryAt) {
		return nil // in backoff: caller falls back to direct injection
	}
	c, err := DialPipe(pipeDialTimeout)
	if err != nil {
		p.retryAt = time.Now().Add(pipeRetryBackoff)
		return nil
	}
	p.retryAt = time.Time{}
	p.conn = c
	return c
}

func (p *proxyInjector) dropConn() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		_ = p.conn.Close()
		p.conn = nil
	}
}

// forward sends an event to the agent; on failure it drops the cached
// connection and returns false so callers fall back to direct injection
// (preserving the pre-service behavior, e.g. cursor movement on the lock
// screen when the service is not installed). The write is bounded by a
// timeout goroutine because named pipes do not support deadline I/O.
func (p *proxyInjector) forward(m Msg) bool {
	c := p.pipe()
	if c == nil {
		return false
	}
	done := make(chan error, 1)
	go func() {
		p.writeMu.Lock()
		defer p.writeMu.Unlock()
		done <- WriteMsg(c, m)
	}()
	select {
	case err := <-done:
		if err != nil {
			p.dropConn()
			return false
		}
		return true
	case <-time.After(500 * time.Millisecond):
		p.dropConn()
		return false
	}
}

func (p *proxyInjector) MovePointer(x, y int32) error {
	if p.routed() && p.forward(Msg{T: MsgMove, Move: &event.PointerMove{X: x, Y: y}}) {
		return nil
	}
	return p.inner.MovePointer(x, y)
}

func (p *proxyInjector) Button(b event.MouseButton, down bool) error {
	if p.routed() && p.forward(Msg{T: MsgButton, Button: &event.PointerButton{Button: b, Down: down}}) {
		return nil
	}
	return p.inner.Button(b, down)
}

func (p *proxyInjector) Scroll(dx, dy int32) error {
	if p.routed() && p.forward(Msg{T: MsgScroll, Scroll: &event.PointerScroll{DX: dx, DY: dy}}) {
		return nil
	}
	return p.inner.Scroll(dx, dy)
}

func (p *proxyInjector) Key(k keymap.Key, down bool) error {
	if p.routed() && p.forward(Msg{T: MsgKey, Key: &event.KeyEvent{KeyCode: k, Down: down}}) {
		return nil
	}
	return p.inner.Key(k, down)
}
