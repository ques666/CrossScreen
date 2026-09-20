package session

import (
	"sync"

	"crossscreen/core/layout"
)

// Router tracks the shared virtual cursor and resolves which screen the input
// targets. The session layer maps that screen to a peer connection (or the
// local machine).
//
// All coordinates are virtual (see package layout). Pointer events are
// exchanged in virtual coordinates; each endpoint converts them to local
// pixels with Screen.ToLocal when injecting.
type Router struct {
	mu     sync.RWMutex
	layout *layout.Layout
	local  layout.DeviceID
	vx, vy int32          // current virtual cursor position
	cur    *layout.Screen // screen currently under the cursor
}

// NewRouter creates a router for the local device.
func NewRouter(l *layout.Layout, local layout.DeviceID) *Router {
	r := &Router{layout: l, local: local}
	r.cur = l.ScreenAt(r.vx, r.vy)
	return r
}

// SetLayout installs the authoritative arrangement (from the arrangement
// editor) and re-resolves the cursor position.
func (r *Router) SetLayout(l *layout.Layout) {
	r.mu.Lock()
	r.layout = l
	r.cur = l.ScreenAt(r.vx, r.vy)
	r.mu.Unlock()
}

// Layout returns the authoritative arrangement.
func (r *Router) Layout() *layout.Layout {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.layout
}

// OnLocalMove advances the cursor for a move that originated on the local
// machine. s is the local screen the pointer physically sits on; lx, ly are
// its local pixel coordinates. It returns the screen the cursor ends on.
func (r *Router) OnLocalMove(s *layout.Screen, lx, ly int32) *layout.Screen {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := r.layout.MovePointer(r.vx, r.vy, s.X+lx, s.Y+ly)
	r.vx, r.vy = res.X, res.Y
	r.cur = res.Screen
	return r.cur
}

// Position returns the current virtual cursor position.
func (r *Router) Position() (int32, int32) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.vx, r.vy
}

// SetPosition seeds the cursor to an absolute virtual position (used on
// startup, before the first delta arrives).
func (r *Router) SetPosition(vx, vy int32) {
	r.mu.Lock()
	r.vx, r.vy = vx, vy
	r.cur = r.layout.ScreenAt(vx, vy)
	r.mu.Unlock()
}

// MoveByDelta advances the cursor by (dx, dy) from its current virtual
// position and returns the screen it ends on. This is the primary move
// primitive on platforms where the OS clamps the physical cursor at the
// display edge (raw input deltas are the only reliable signal there).
func (r *Router) MoveByDelta(dx, dy int32) *layout.Screen {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := r.layout.MovePointer(r.vx, r.vy, r.vx+dx, r.vy+dy)
	r.vx, r.vy = res.X, res.Y
	r.cur = res.Screen
	return r.cur
}

// MoveTo advances the cursor to an absolute virtual position (reported by a
// peer or controller) and returns the screen it lands on.
func (r *Router) MoveTo(vx, vy int32) *layout.Screen {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := r.layout.MovePointer(r.vx, r.vy, vx, vy)
	r.vx, r.vy = res.X, res.Y
	r.cur = res.Screen
	return r.cur
}

// Active returns the screen the cursor currently occupies (nil if off-screen).
func (r *Router) Active() *layout.Screen {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cur
}

// OnLocal reports whether the cursor sits on a local screen.
func (r *Router) OnLocal() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cur != nil && r.cur.Device == r.local
}

// OnPeer reports whether the cursor sits on a peer's screen.
func (r *Router) OnPeer() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cur != nil && r.cur.Device != r.local
}
