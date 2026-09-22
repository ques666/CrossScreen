//go:build windows

package capture

import (
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"crossscreen/core/event"
	"crossscreen/core/keymap"
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	procSetWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetCursorPos        = user32.NewProc("SetCursorPos")
	procGetCurrentThreadId  = user32.NewProc("GetCurrentThreadId")
	procPostThreadMessageW  = user32.NewProc("PostThreadMessageW")
)

const (
	whMouseLL    = 14
	whKeyboardLL = 13
	// The injected-event flag differs per hook type: LLMHF_INJECTED (mouse
	// flags) is 0x01 while LLKHF_INJECTED (keyboard flags) is 0x10. Sharing
	// the keyboard value for mouse events meant our own injected input was
	// not filtered, so it could be captured and forwarded again.
	llmhfInjected = 0x01
	llkhfInjected = 0x10

	wmQuit        = 0x0012
	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmLButtonUp   = 0x0202
	wmRButtonDown = 0x0204
	wmRButtonUp   = 0x0205
	wmMButtonDown = 0x0207
	wmMButtonUp   = 0x0208
	wmMouseWheel  = 0x020A
	wmMouseHWheel = 0x020E
	wmKeyDown     = 0x0100
	wmKeyUp       = 0x0101
	wmSysKeyDown  = 0x0104
	wmSysKeyUp    = 0x0105
)

type point struct{ x, y int32 }

type msllhookstruct struct {
	pt          point
	mouseData   uint32
	flags       uint32
	time        uint32
	dwExtraInfo uintptr
}

type kbdllhookstruct struct {
	vkCode      uint32
	scanCode    uint32
	flags       uint32
	time        uint32
	dwExtraInfo uintptr
}

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

// queuedEvent carries non-move input from the hook callbacks (which must
// return within the low-level hook timeout) to the dispatcher goroutine.
type queuedEvent struct {
	kind uint8 // 1 button, 2 scroll, 3 key
	down bool
	vk   uint32
	btn  event.MouseButton
	dx   int32
	dy   int32
}

const (
	evButton = 1
	evScroll = 2
	evKey    = 3
)

// windowsCapture uses low-level hooks. The callbacks perform no locking and
// no I/O: they only enqueue data and return, because Windows silently removes
// a low-level hook whose callback exceeds LowLevelHooksTimeout (~300ms) once.
type windowsCapture struct {
	stop     chan struct{}
	once     sync.Once
	wg       sync.WaitGroup
	threadID uint32

	handler Handler // set once before the hook thread starts; immutable after

	// move aggregation (all accessed lock-free from callbacks/dispatcher)
	moveSig chan struct{}
	pullCh  chan struct{} // coalesced edge-bounce warp requests

	relative atomic.Int32
	anchorX  atomic.Int32
	anchorY  atomic.Int32
	lastX    atomic.Int32
	lastY    atomic.Int32
	haveLast atomic.Int32

	// pending aggregated move for the current dispatch tick
	pendDX   atomic.Int32
	pendDY   atomic.Int32
	pendAbsX atomic.Int32
	pendAbsY atomic.Int32
	pendRel  atomic.Int32

	events chan queuedEvent
}

func newCapture() (Capture, error) {
	return &windowsCapture{
		stop:    make(chan struct{}),
		moveSig: make(chan struct{}, 1),
		pullCh:  make(chan struct{}, 1),
		events:  make(chan queuedEvent, 1024),
	}, nil
}

func (c *windowsCapture) Start(h Handler) error {
	c.handler = h
	c.wg.Add(3)
	go c.run()
	go c.dispatch()
	go c.pullLoop()
	return nil
}

func (c *windowsCapture) run() {
	defer c.wg.Done()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	c.threadID = getThreadID()

	mouseCb := syscall.NewCallback(c.mouseProc)
	keyCb := syscall.NewCallback(c.keyProc)
	hMouse, _, _ := procSetWindowsHookExW.Call(whMouseLL, mouseCb, 0, 0)
	hKey, _, _ := procSetWindowsHookExW.Call(whKeyboardLL, keyCb, 0, 0)
	defer func() {
		if hMouse != 0 {
			procUnhookWindowsHookEx.Call(hMouse)
		}
		if hKey != 0 {
			procUnhookWindowsHookEx.Call(hKey)
		}
	}()

	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 || r == ^uintptr(0) { // WM_QUIT or error
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		select {
		case <-c.stop:
			procPostThreadMessageW.Call(uintptr(c.threadID), wmQuit, 0, 0)
			return
		default:
		}
	}
}

// SetRelativeMode toggles edge-bounce mode.
func (c *windowsCapture) SetRelativeMode(on bool) {
	if on {
		c.relative.Store(1)
	} else {
		c.relative.Store(0)
	}
}

// PinCursor warps the cursor to (x, y) and sets the bounce anchor.
func (c *windowsCapture) PinCursor(x, y int32) {
	c.anchorX.Store(x)
	c.anchorY.Store(y)
	setCursorPos(x, y)
}

// mouseProc is the WH_MOUSE_LL callback. It must never block: no locks, no
// network, no waits — just aggregate/enqueue and return.
func (c *windowsCapture) mouseProc(nCode int, wParam, lParam uintptr) uintptr {
	if nCode < 0 {
		return callNextHook(nCode, wParam, lParam)
	}
	info := (*msllhookstruct)(unsafe.Pointer(lParam))
	relative := c.relative.Load() == 1
	if info.flags&llmhfInjected != 0 {
		// Input injected by another process (remote control, automation) is
		// never suppressed: it always reaches the system untouched.
		return callNextHook(nCode, wParam, lParam)
	}

	switch wParam {
	case wmMouseMove:
		// Aggregate into atomic pending slots; never block.
		if relative {
			ax, ay := c.anchorX.Load(), c.anchorY.Load()
			c.pendDX.Add(info.pt.x - ax)
			c.pendDY.Add(info.pt.y - ay)
			c.pendAbsX.Store(ax)
			c.pendAbsY.Store(ay)
			c.pendRel.Store(1)
		} else {
			c.pendAbsX.Store(info.pt.x)
			c.pendAbsY.Store(info.pt.y)
			c.pendRel.Store(0)
		}
		c.signalMove()
	case wmLButtonDown, wmLButtonUp:
		c.enqueue(queuedEvent{kind: evButton, btn: event.ButtonLeft, down: wParam == wmLButtonDown})
	case wmRButtonDown, wmRButtonUp:
		c.enqueue(queuedEvent{kind: evButton, btn: event.ButtonRight, down: wParam == wmRButtonDown})
	case wmMButtonDown, wmMButtonUp:
		c.enqueue(queuedEvent{kind: evButton, btn: event.ButtonMiddle, down: wParam == wmMButtonDown})
	case wmMouseWheel, wmMouseHWheel:
		delta := int32(int16(info.mouseData>>16)) / 120
		if wParam == wmMouseWheel {
			c.enqueue(queuedEvent{kind: evScroll, dy: delta})
		} else {
			c.enqueue(queuedEvent{kind: evScroll, dx: delta})
		}
	}

	if relative {
		return 1 // swallow: input routes to the peer only
	}
	return callNextHook(nCode, wParam, lParam)
}

// keyProc is the WH_KEYBOARD_LL callback — enqueue only, never block.
func (c *windowsCapture) keyProc(nCode int, wParam, lParam uintptr) uintptr {
	if nCode < 0 {
		return callNextHook(nCode, wParam, lParam)
	}
	info := (*kbdllhookstruct)(unsafe.Pointer(lParam))
	relative := c.relative.Load() == 1
	if info.flags&llkhfInjected != 0 {
		// Never suppress injected input from other processes.
		return callNextHook(nCode, wParam, lParam)
	}
	switch wParam {
	case wmKeyDown, wmKeyUp, wmSysKeyDown, wmSysKeyUp:
		c.enqueue(queuedEvent{
			kind: evKey,
			vk:   info.vkCode,
			down: wParam == wmKeyDown || wParam == wmSysKeyDown,
		})
	}
	if relative {
		return 1
	}
	return callNextHook(nCode, wParam, lParam)
}

// signalMove wakes the dispatcher for aggregated movement.
func (c *windowsCapture) signalMove() {
	select {
	case c.moveSig <- struct{}{}:
	default: // a dispatch is already pending
	}
}

func (c *windowsCapture) enqueue(e queuedEvent) {
	select {
	case c.events <- e:
	default: // drop on overflow rather than blocking the hook
	}
}

// dispatch consumes aggregated moves and discrete events off the hook thread,
// so the handler (which may do network I/O) never runs inside the timeout-
// bounded callback.
func (c *windowsCapture) dispatch() {
	defer c.wg.Done()
	for {
		select {
		case <-c.stop:
			return
		case <-c.moveSig:
			// Drain any duplicate signals coalesced during one tick.
			drained := true
			for drained {
				select {
				case <-c.moveSig:
				default:
					drained = false
				}
			}
			x, y := c.pendAbsX.Load(), c.pendAbsY.Load()
			if c.pendRel.Load() == 1 {
				dx, dy := c.pendDX.Swap(0), c.pendDY.Swap(0)
				if dx != 0 || dy != 0 {
					if c.handler.OnMove != nil {
						c.handler.OnMove(x, y, dx, dy)
					}
					c.requestPull()
				}
			} else if c.handler.OnMove != nil {
				var dx, dy int32
				if c.haveLast.Load() == 1 {
					dx, dy = x-c.lastX.Load(), y-c.lastY.Load()
				}
				c.haveLast.Store(1)
				c.lastX.Store(x)
				c.lastY.Store(y)
				c.handler.OnMove(x, y, dx, dy)
			}
		case e := <-c.events:
			switch e.kind {
			case evButton:
				if c.handler.OnButton != nil {
					c.handler.OnButton(event.PointerButton{Button: e.btn, Down: e.down})
				}
			case evScroll:
				if c.handler.OnScroll != nil {
					c.handler.OnScroll(event.PointerScroll{DX: e.dx, DY: e.dy})
				}
			case evKey:
				if c.handler.OnKey != nil {
					if k, ok := keymap.KeyFromWindows(uint16(e.vk)); ok {
						c.handler.OnKey(k, e.down)
					}
				}
			}
		}
	}
}

// pullLoop performs edge bounce warps off the hook thread, coalesced and
// throttled to ~8ms so fast movement does not trigger one warp per event.
func (c *windowsCapture) pullLoop() {
	defer c.wg.Done()
	var last time.Time
	for {
		select {
		case <-c.stop:
			return
		case <-c.pullCh:
			if c.relative.Load() == 1 && time.Since(last) >= 8*time.Millisecond {
				setCursorPos(c.anchorX.Load(), c.anchorY.Load())
				last = time.Now()
			}
		}
	}
}

func (c *windowsCapture) requestPull() {
	select {
	case c.pullCh <- struct{}{}:
	default:
	}
}

func (c *windowsCapture) Stop() {
	c.once.Do(func() { close(c.stop) })
	procPostThreadMessageW.Call(uintptr(c.threadID), wmQuit, 0, 0)
	c.wg.Wait()
}

func (c *windowsCapture) Position() (int32, int32, error) {
	var p point
	if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p))); r == 0 {
		return 0, 0, syscall.GetLastError()
	}
	return p.x, p.y, nil
}

func setCursorPos(x, y int32) {
	procSetCursorPos.Call(uintptr(x), uintptr(y))
}

func getThreadID() uint32 {
	r, _, _ := procGetCurrentThreadId.Call()
	return uint32(r)
}

func callNextHook(nCode int, wParam, lParam uintptr) uintptr {
	r, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return r
}
