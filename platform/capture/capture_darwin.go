//go:build darwin

package capture

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>
#include <pthread.h>
#include <string.h>
#include <unistd.h>

#define CS_EV_CAP 128

struct cs_ev {
	// 1 move, 2 button, 3 scroll, 4 key, 5 flags
	int typ;
	double x, y;     // position
	double d1, d2;   // scroll deltas (d1 vertical, d2 horizontal) / flags
	unsigned int code; // keycode or button (1 left, 2 right, 3 middle)
	int down;
};

static struct cs_ev g_buf[CS_EV_CAP];
static int g_head = 0;
static int g_tail = 0;
static pthread_mutex_t g_mu = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t g_cond = PTHREAD_COND_INITIALIZER;
static volatile int g_closed = 0;

// g_suppress: when nonzero (shared cursor on a peer), the tap swallows
// physical input events so they no longer reach this Mac's foreground app.
static volatile int g_suppress = 0;
static CFMachPortRef g_tap = NULL;

// Edge-bounce anchor: while suppressing, the cursor is repeatedly warped
// back here so physical mouse movement keeps producing raw deltas without
// the visible cursor leaving the edge.
static volatile int g_bounce = 0;
static double g_anchor_x = 0;
static double g_anchor_y = 0;

void cs_set_suppress(int on) { g_suppress = on ? 1 : 0; }
int cs_get_suppress(void) { return g_suppress; }

void cs_set_anchor(double x, double y) {
	g_anchor_x = x;
	g_anchor_y = y;
	g_bounce = 1;
}
void cs_clear_anchor(void) { g_bounce = 0; }
int cs_get_bounce(void) { return g_bounce; }
double cs_anchor_xv(void) { return g_anchor_x; }
double cs_anchor_yv(void) { return g_anchor_y; }

void cs_close_queue(void) {
	pthread_mutex_lock(&g_mu);
	g_closed = 1;
	pthread_cond_broadcast(&g_cond);
	pthread_mutex_unlock(&g_mu);
}

// cs_warp_to relocates the cursor. Called from the Go drain goroutine (never
// from the tap callback thread, where warping stalls event dispatch).
void cs_warp_to(double x, double y) {
	CGWarpMouseCursorPosition(CGPointMake(x, y));
}

static void cs_push(const struct cs_ev *e) {
	pthread_mutex_lock(&g_mu);
	int next = (g_head + 1) % CS_EV_CAP;
	if (next != g_tail) {
		g_buf[g_head] = *e;
		g_head = next;
		pthread_cond_signal(&g_cond); // wake the Go drain immediately
	}
	pthread_mutex_unlock(&g_mu);
}

// Blocks until at least one event is available, the queue closes, or
// timeoutMs elapses. Drains ALL currently queued events into out and returns
// the count. Locking once per batch keeps mutex/cond traffic minimal under
// fast movement.
static int cs_drain_batch(struct cs_ev *out, int cap, int timeoutMs) {
	struct timespec ts;
	clock_gettime(CLOCK_REALTIME, &ts);
	ts.tv_sec += timeoutMs / 1000;
	ts.tv_nsec += (long)(timeoutMs % 1000) * 1000000L;
	if (ts.tv_nsec >= 1000000000L) { ts.tv_sec++; ts.tv_nsec -= 1000000000L; }

	pthread_mutex_lock(&g_mu);
	while (g_head == g_tail && !g_closed) {
		if (pthread_cond_timedwait(&g_cond, &g_mu, &ts) != 0) break;
	}
	int n = 0;
	while (g_head != g_tail && n < cap) {
		out[n++] = g_buf[g_tail];
		g_tail = (g_tail + 1) % CS_EV_CAP;
	}
	pthread_mutex_unlock(&g_mu);
	return n;
}

static int cs_mouse_location(double *x, double *y) {
	CGEventRef e = CGEventCreate(NULL);
	if (!e) return -1;
	CGPoint p = CGEventGetLocation(e);
	CFRelease(e);
	*x = p.x;
	*y = p.y;
	return 0;
}

static int64_t cs_source_pid(CGEventRef ev) {
	return CGEventGetIntegerValueField(ev, kCGEventSourceUnixProcessID);
}

static CGEventRef cs_tap_cb(CGEventTapProxy proxy, CGEventType type, CGEventRef ev, void *refcon) {
	(void)proxy;
	(void)refcon;
	if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
		// The system auto-disables a slow tap; re-enable it.
		if (g_tap) CGEventTapEnable(g_tap, true);
		return ev;
	}
	// ignore events we injected ourselves
	if (cs_source_pid(ev) == (int64_t)getpid()) {
		return ev;
	}
	struct cs_ev e;
	memset(&e, 0, sizeof(e));
	CGPoint p = CGEventGetLocation(ev);
	e.x = p.x;
	e.y = p.y;
	switch (type) {
	case kCGEventMouseMoved:
	case kCGEventLeftMouseDragged:
	case kCGEventRightMouseDragged:
	case kCGEventOtherMouseDragged:
		e.typ = 1;
		e.d1 = CGEventGetDoubleValueField(ev, kCGMouseEventDeltaX);
		e.d2 = CGEventGetDoubleValueField(ev, kCGMouseEventDeltaY);
		break;
	case kCGEventLeftMouseDown:   e.typ = 2; e.code = 1; e.down = 1; break;
	case kCGEventLeftMouseUp:     e.typ = 2; e.code = 1; break;
	case kCGEventRightMouseDown:  e.typ = 2; e.code = 2; e.down = 1; break;
	case kCGEventRightMouseUp:    e.typ = 2; e.code = 2; break;
	case kCGEventOtherMouseDown:  e.typ = 2; e.code = 3; e.down = 1; break;
	case kCGEventOtherMouseUp:    e.typ = 2; e.code = 3; break;
	case kCGEventScrollWheel:
		e.typ = 3;
		e.d1 = CGEventGetIntegerValueField(ev, kCGScrollWheelEventDeltaAxis1);
		e.d2 = CGEventGetIntegerValueField(ev, kCGScrollWheelEventDeltaAxis2);
		break;
	case kCGEventKeyDown:
		e.typ = 4;
		e.code = (unsigned int)CGEventGetIntegerValueField(ev, kCGKeyboardEventKeycode);
		e.down = 1;
		break;
	case kCGEventKeyUp:
		e.typ = 4;
		e.code = (unsigned int)CGEventGetIntegerValueField(ev, kCGKeyboardEventKeycode);
		break;
	case kCGEventFlagsChanged:
		e.typ = 5;
		e.code = (unsigned int)CGEventGetIntegerValueField(ev, kCGKeyboardEventKeycode);
		e.d1 = (double)CGEventGetFlags(ev);
		break;
	default:
		return ev;
	}
	cs_push(&e);
	// While the shared cursor is on a peer, swallow the event so it is routed
	// over the network only. Cursor bounce-back runs on the Go drain
	// goroutine instead of this thread (warps here would stall event
	// dispatch and make input feel laggy).
	if (g_suppress) {
		return NULL;
	}
	return ev;
}

static CGEventMask cs_event_mask(void) {
	CGEventMask m = 0;
	m |= CGEventMaskBit(kCGEventMouseMoved);
	m |= CGEventMaskBit(kCGEventLeftMouseDragged);
	m |= CGEventMaskBit(kCGEventRightMouseDragged);
	m |= CGEventMaskBit(kCGEventOtherMouseDragged);
	m |= CGEventMaskBit(kCGEventLeftMouseDown);
	m |= CGEventMaskBit(kCGEventLeftMouseUp);
	m |= CGEventMaskBit(kCGEventRightMouseDown);
	m |= CGEventMaskBit(kCGEventRightMouseUp);
	m |= CGEventMaskBit(kCGEventOtherMouseDown);
	m |= CGEventMaskBit(kCGEventOtherMouseUp);
	m |= CGEventMaskBit(kCGEventScrollWheel);
	m |= CGEventMaskBit(kCGEventKeyDown);
	m |= CGEventMaskBit(kCGEventKeyUp);
	m |= CGEventMaskBit(kCGEventFlagsChanged);
	return m;
}

static CFMachPortRef cs_create_tap(void) {
	// Default (filter) placement lets the callback swallow events by
	// returning NULL while the shared cursor is on a peer.
	CFMachPortRef tap = CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap,
		kCGEventTapOptionDefault, cs_event_mask(), cs_tap_cb, NULL);
	g_tap = tap;
	return tap;
}

static int cs_runloop(CFMachPortRef tap) {
	if (!tap) return -1;
	CFRunLoopSourceRef src = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, tap, 0);
	if (!src) return -1;
	CFRunLoopAddSource(CFRunLoopGetCurrent(), src, kCFRunLoopCommonModes);
	CFRelease(src);
	return 0;
}

static int cs_tap_is_null(CFMachPortRef tap) { return tap == NULL; }
static int cs_rl_is_null(CFRunLoopRef rl) { return rl == NULL; }
static void cs_release_tap(CFMachPortRef tap) { CFRelease(tap); }
*/
import "C"

import (
	"fmt"
	"runtime"
	"sync"
	"time"

	"crossscreen/core/event"
	"crossscreen/core/keymap"
)

type darwinCapture struct {
	mu   sync.Mutex
	rl   C.CFRunLoopRef
	stop chan struct{}
	once sync.Once
	wg   sync.WaitGroup
}

func newCapture() (Capture, error) {
	return &darwinCapture{stop: make(chan struct{})}, nil
}

func (c *darwinCapture) Start(h Handler) error {
	tap := C.cs_create_tap()
	if C.cs_tap_is_null(tap) != 0 {
		return fmt.Errorf("capture: event tap creation failed (grant Input Monitoring permission)")
	}

	c.wg.Add(2)
	go c.runLoop(tap)
	go c.drain(h)
	return nil
}

func (c *darwinCapture) runLoop(tap C.CFMachPortRef) {
	defer c.wg.Done()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if C.cs_runloop(tap) != 0 {
		C.cs_release_tap(tap)
		return
	}
	c.mu.Lock()
	c.rl = C.CFRunLoopGetCurrent()
	c.mu.Unlock()
	C.CFRunLoopRun() // blocks until Stop calls CFRunLoopStop
}

// drainBatchSize caps how many raw events are handled in one wakeup.
const drainBatchSize = 256

func (c *darwinCapture) drain(h Handler) {
	defer c.wg.Done()
	mods := map[uint16]bool{}
	buf := make([]C.struct_cs_ev, drainBatchSize)
	lastWarp := time.Now()
	for {
		select {
		case <-c.stop:
			return
		default:
		}
		// Block until events arrive (cond var), or 8ms for a periodic warp.
		// All queued events are drained in one locked batch, so fast
		// movement becomes a single callback instead of N wakeups.
		n := int(C.cs_drain_batch(&buf[0], C.int(len(buf)), 8))
		if n == 0 {
			continue
		}
		var (
			sumDX, sumDY C.double
			lastX, lastY C.double
			moves        int
		)
		for i := 0; i < n; i++ {
			ev := &buf[i]
			if ev.typ == 1 {
				sumDX += ev.d1
				sumDY += ev.d2
				lastX, lastY = ev.x, ev.y
				moves++
				continue
			}
			dispatchDarwin(h, ev, mods)
		}
		if moves > 0 && h.OnMove != nil {
			h.OnMove(int32(lastX), int32(lastY), int32(sumDX), int32(sumDY))
		}
		// Throttle edge bounce warps to ~every 8ms and off the data path:
		// a warp per batch under fast movement is what made crossing stutter.
		if moves > 0 && C.cs_get_bounce() != 0 && time.Since(lastWarp) >= 8*time.Millisecond {
			C.cs_warp_to(C.cs_anchor_xv(), C.cs_anchor_yv())
			lastWarp = time.Now()
		}
	}
}

func (c *darwinCapture) Stop() {
	c.once.Do(func() { close(c.stop) })
	// Restore normal input delivery even if we stop while on a peer.
	C.cs_set_suppress(0)
	C.cs_clear_anchor()
	C.CGAssociateMouseAndMouseCursorPosition(1)
	C.cs_close_queue() // unblock the drain goroutine immediately
	c.mu.Lock()
	rl := c.rl
	c.mu.Unlock()
	if C.cs_rl_is_null(rl) == 0 {
		C.CFRunLoopStop(rl)
	}
	c.wg.Wait()
}

// SetRelativeMode toggles event suppression so physical keyboard/mouse do
// not also reach this Mac while the shared cursor is on a peer. Cursor
// confinement is provided by PinCursor + edge bounce, which is reliable
// across macOS versions (unlike CGAssociateMouseAndMouseCursorPosition).
func (c *darwinCapture) SetRelativeMode(on bool) {
	if on {
		C.cs_set_suppress(1)
	} else {
		C.cs_set_suppress(0)
		C.cs_clear_anchor()
	}
}

// PinCursor places the physical cursor at (x, y) and makes it the edge-bounce
// anchor used while relative mode is on.
func (c *darwinCapture) PinCursor(x, y int32) {
	C.cs_set_anchor(C.double(x), C.double(y))
	C.cs_warp_to(C.double(x), C.double(y))
}

func (c *darwinCapture) Position() (int32, int32, error) {
	var x, y C.double
	if C.cs_mouse_location(&x, &y) != 0 {
		return 0, 0, fmt.Errorf("capture: cannot read mouse location")
	}
	return int32(x), int32(y), nil
}

func dispatchDarwin(h Handler, ev *C.struct_cs_ev, mods map[uint16]bool) {
	switch ev.typ {
	case 1: // move
		if h.OnMove != nil {
			h.OnMove(int32(ev.x), int32(ev.y), int32(ev.d1), int32(ev.d2))
		}
	case 2: // button
		var b event.MouseButton
		switch ev.code {
		case 1:
			b = event.ButtonLeft
		case 2:
			b = event.ButtonRight
		case 3:
			b = event.ButtonMiddle
		default:
			return
		}
		if h.OnButton != nil {
			h.OnButton(event.PointerButton{Button: b, Down: ev.down != 0})
		}
	case 3: // scroll
		if h.OnScroll != nil {
			h.OnScroll(event.PointerScroll{DX: int32(ev.d2), DY: int32(ev.d1)})
		}
	case 4: // key
		if h.OnKey != nil {
			if k, ok := keymap.KeyFromMac(uint16(ev.code)); ok {
				h.OnKey(k, ev.down != 0)
			}
		}
	case 5: // flags changed (modifiers)
		if h.OnKey != nil {
			flags := uint64(ev.d1)
			bit, ok := macModifierBit(uint16(ev.code))
			if !ok {
				return
			}
			down := flags&bit != 0
			if mods[uint16(ev.code)] != down {
				mods[uint16(ev.code)] = down
				if k, ok := keymap.KeyFromMac(uint16(ev.code)); ok {
					h.OnKey(k, down)
				}
			}
		}
	}
}

func macModifierBit(code uint16) (uint64, bool) {
	switch code {
	case 0x38, 0x3C: // shift keys
		return uint64(C.kCGEventFlagMaskShift), true
	case 0x3B, 0x3E: // control keys
		return uint64(C.kCGEventFlagMaskControl), true
	case 0x3A, 0x3D: // option keys
		return uint64(C.kCGEventFlagMaskAlternate), true
	case 0x37, 0x36: // command keys
		return uint64(C.kCGEventFlagMaskCommand), true
	}
	return 0, false
}
