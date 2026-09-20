// Package adapter wires capture, injection and session into a runnable
// cross-screen node (server or client).
//
// Coordinates: capture reports global display coordinates and raw deltas; the
// session router works in the shared virtual space; injection happens in the
// device's own global space. The adapter converts between them using the
// authoritative layout and the local screen geometry.
package adapter

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"crossscreen/core/event"
	"crossscreen/core/keymap"
	"crossscreen/core/layout"
	"crossscreen/core/protocol"
	"crossscreen/core/session"
	"crossscreen/platform/capture"
	"crossscreen/platform/clipboard"
	"crossscreen/platform/filexfer"
	"crossscreen/platform/inject"
)

// debugEnabled reports whether CROSSSCREEN_DEBUG is set, enabling verbose
// edge/crossing diagnostics.
func debugEnabled() bool {
	v := os.Getenv("CROSSSCREEN_DEBUG")
	return v != "" && v != "0"
}

func debugf(format string, args ...any) {
	if debugEnabled() {
		log.Printf("[cross] "+format, args...)
	}
}

// Clipboard provides system clipboard access for cross-device sync. Any
// implementation must suppress echo: a Write must not surface back through
// its own Watch.
type Clipboard interface {
	Watch(ctx context.Context, h func(clipboard.Data))
	Write(d clipboard.Data) error
}

// ServerConfig configures a session server node.
type ServerConfig struct {
	Hello        *protocol.Hello
	Capture      capture.Capture // may be nil for headless operation
	Injector     inject.Injector
	Clipboard    Clipboard         // may be nil to disable clipboard sync
	Files        *filexfer.Manager // may be nil to disable file copy-paste
	LocalScreens []*layout.Screen  // optional; defaults to display enumeration
}

// ClientConfig configures a session client node.
type ClientConfig struct {
	Hello        *protocol.Hello
	Capture      capture.Capture // used only for RoleSource controllers
	Injector     inject.Injector
	Clipboard    Clipboard         // may be nil to disable clipboard sync
	Files        *filexfer.Manager // may be nil to disable file copy-paste
	LocalScreens []*layout.Screen  // optional; defaults to display enumeration
	// OnDisconnect is invoked once when the connection to the server ends.
	OnDisconnect func()
}

// core is the shared injection + layout conversion logic.
type core struct {
	inj          inject.Injector
	clip         Clipboard
	files        *filexfer.Manager
	local        layout.DeviceID
	localScreens []*layout.Screen

	clipCancel context.CancelFunc

	mu            sync.RWMutex
	authoritative *layout.Layout
}

func newCore(local layout.DeviceID, inj inject.Injector, screens []*layout.Screen) *core {
	return &core{inj: inj, local: local, localScreens: screens}
}

func (c *core) setLayout(l *layout.Layout) {
	c.mu.Lock()
	c.authoritative = l
	c.mu.Unlock()
}

func (c *core) layout() *layout.Layout {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.authoritative
}

func (c *core) screenAtGlobal(x, y int32) *layout.Screen {
	for _, s := range c.localScreens {
		if s.Contains(x, y) {
			return s
		}
	}
	return nil
}

func findScreen(l *layout.Layout, id layout.ScreenID) *layout.Screen {
	for _, s := range l.Screens {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// toGlobal converts a virtual position to this device's own global display
// coordinates. The authoritative placement of a screen may differ from its
// physical position (e.g. the server placed this screen at x=1920 while the
// physical screen sits at x=0); the conversion cancels that offset.
func (c *core) toGlobal(vx, vy int32) (int32, int32, bool) {
	l := c.layout()
	if l == nil {
		return vx, vy, true
	}
	s := l.ScreenAt(vx, vy)
	if s == nil {
		return 0, 0, false
	}
	for _, ls := range c.localScreens {
		if ls.ID == s.ID {
			return ls.X + (vx - s.X), ls.Y + (vy - s.Y), true
		}
	}
	return vx, vy, true
}

// toVirtual converts this device's global coordinates into the shared virtual
// space using the authoritative placement of the screen under the cursor.
func (c *core) toVirtual(x, y int32) (int32, int32, bool) {
	l := c.layout()
	ls := c.screenAtGlobal(x, y)
	if l == nil || ls == nil {
		return 0, 0, false
	}
	s := findScreen(l, ls.ID)
	if s == nil {
		return 0, 0, false
	}
	return s.X + (x - ls.X), s.Y + (y - ls.Y), true
}

// sink injects an incoming envelope on this device.
func (c *core) sink(env *protocol.Envelope) error {
	switch env.Type {
	case protocol.TypePointerMove:
		var m event.PointerMove
		if err := env.Decode(&m); err != nil {
			return err
		}
		gx, gy, ok := c.toGlobal(m.X, m.Y)
		if !ok {
			return nil
		}
		return c.inj.MovePointer(gx, gy)
	case protocol.TypePointerBtn:
		var b event.PointerButton
		if err := env.Decode(&b); err != nil {
			return err
		}
		return c.inj.Button(b.Button, b.Down)
	case protocol.TypePointerScroll:
		var s event.PointerScroll
		if err := env.Decode(&s); err != nil {
			return err
		}
		return c.inj.Scroll(s.DX, s.DY)
	case protocol.TypeKeyboard:
		var k event.KeyEvent
		if err := env.Decode(&k); err != nil {
			return err
		}
		return c.inj.Key(k.KeyCode, k.Down)
	case protocol.TypeClipboard:
		var ev event.ClipboardEvent
		if err := env.Decode(&ev); err != nil {
			return err
		}
		if ev.Mime == clipboard.MimeFiles {
			if c.files != nil {
				return c.files.HandleOffer(ev.Data)
			}
			return nil
		}
		if c.clip != nil {
			return c.clip.Write(clipboard.Data{Mime: ev.Mime, Data: ev.Data})
		}
	case protocol.TypeFileAccept, protocol.TypeFileChunk, protocol.TypeFileCancel, protocol.TypeFileDone:
		if c.files != nil {
			return c.files.Handle(env)
		}
	}
	return nil
}

// watchClipboardSync mirrors local clipboard changes into the session and
// stops when the node closes.
func (c *core) watchClipboardSync(send func(d clipboard.Data) error) {
	if c.clip == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.clipCancel = cancel
	go c.clip.Watch(ctx, func(d clipboard.Data) {
		// Turn local file references into a wire offer (content streams
		// separately after the peer accepts).
		if d.Mime == clipboard.MimeFiles && c.files != nil {
			wire, err := c.files.PrepareOutbound(d.Data)
			if err != nil {
				log.Printf("adapter: file offer: %v", err)
				return
			}
			d = clipboard.Data{Mime: clipboard.MimeFiles, Data: wire}
		}
		if err := send(d); err != nil {
			log.Printf("adapter: clipboard sync: %v", err)
		}
	})
}

func (c *core) stopClipboard() {
	if c.clipCancel != nil {
		c.clipCancel()
		c.clipCancel = nil
	}
}

// Server runs a session server node.
type Server struct {
	core
	srv *session.Server
	cap capture.Capture

	mu         sync.Mutex
	placements map[layout.ScreenID]screenPos

	relative bool // cursor decoupled (shared cursor on a peer's screen)
}

type screenPos struct{ x, y int32 }

// RunServer builds a session server node and starts local input capture.
func RunServer(cfg ServerConfig) (*Server, error) {
	screens, err := resolveScreens(cfg.Hello.Hostname, cfg.LocalScreens)
	if err != nil {
		return nil, err
	}
	d := &Server{
		core:       *newCore(layout.DeviceID(cfg.Hello.Hostname), cfg.Injector, screens),
		placements: map[layout.ScreenID]screenPos{},
	}
	d.srv = session.NewServer(cfg.Hello, d.sink)
	d.srv.SetOnPeersChanged(d.rebuildLayout)
	d.rebuildLayout()
	d.clip = cfg.Clipboard
	d.files = cfg.Files
	attachFiles(cfg.Files, cfg.Clipboard, func(to string, env *protocol.Envelope) error {
		return d.srv.SendToDevice(layout.DeviceID(to), env)
	})
	d.watchClipboardSync(func(data clipboard.Data) error {
		return d.srv.BroadcastClipboard(event.ClipboardEvent{Mime: data.Mime, Data: data.Data})
	})
	if cfg.Capture != nil {
		if err := cfg.Capture.Start(capture.Handler{
			OnMove:   d.onMove,
			OnButton: d.onButton,
			OnScroll: d.onScroll,
			OnKey:    d.onKey,
		}); err != nil {
			return nil, fmt.Errorf("adapter: start capture: %w", err)
		}
		d.cap = cfg.Capture
		if x, y, err := cfg.Capture.Position(); err == nil {
			d.srv.Router().SetPosition(x, y)
		}
	}
	return d, nil
}

// Listen starts the TCP listener.
func (d *Server) Listen(addr string) (net.Addr, error) {
	return d.srv.Listen(addr, nil)
}

// Close stops capture and the session server.
func (d *Server) Close() error {
	if d.cap != nil {
		d.cap.Stop()
	}
	d.stopClipboard()
	if d.files != nil {
		d.files.Detach()
	}
	return d.srv.Close()
}

// attachFiles binds a file transfer manager to the active node: finished
// inbound transfers are written through the concrete clipboard clipper (for
// echo suppression) and control messages are sent via send.
func attachFiles(mgr *filexfer.Manager, clip Clipboard, send func(to string, env *protocol.Envelope) error) {
	if mgr == nil {
		return
	}
	type fileWriter interface {
		WriteFiles(clipboard.LocalFiles) error
	}
	var writer func(clipboard.LocalFiles) error
	if fw, ok := clip.(fileWriter); ok {
		writer = fw.WriteFiles
	}
	mgr.Attach(writer, send)
}

// Session exposes the underlying session server (used by tests and tools).
func (d *Server) Session() *session.Server { return d.srv }

// Layout returns the authoritative virtual arrangement.
func (d *Server) Layout() *layout.Layout { return d.srv.Router().Layout() }

// LocalScreens returns this device's own screens (real geometry).
func (d *Server) LocalScreens() []*layout.Screen { return d.localScreens }

// SetPlacement records a user-defined position for a screen (typically a
// peer's screen) and rebuilds the arrangement, keeping manual placements.
func (d *Server) SetPlacement(id layout.ScreenID, x, y int32) {
	d.mu.Lock()
	d.placements[id] = screenPos{x, y}
	d.mu.Unlock()
	d.rebuildLayout()
}

// ClearPlacement removes a manual placement and falls back to auto-layout.
func (d *Server) ClearPlacement(id layout.ScreenID) {
	d.mu.Lock()
	delete(d.placements, id)
	d.mu.Unlock()
	d.rebuildLayout()
}

// rebuildLayout builds the arrangement from the local screens plus each
// peer's reported screens, honoring any manual placements, and pushes it to
// every peer.
func (d *Server) rebuildLayout() {
	l := &layout.Layout{Version: 1}
	l.Screens = append(l.Screens, d.localScreens...)

	right := int32(0)
	primaryY := int32(0)
	first := true
	for _, s := range l.Screens {
		if first {
			primaryY = s.Y
			first = false
		}
		if s.Primary {
			primaryY = s.Y
		}
		if s.X+s.Width > right {
			right = s.X + s.Width
		}
	}

	d.mu.Lock()
	placements := make(map[layout.ScreenID]screenPos, len(d.placements))
	for k, v := range d.placements {
		placements[k] = v
	}
	d.mu.Unlock()

	for _, p := range d.srv.Peers() {
		if !p.Ready() {
			continue
		}
		pscreens := p.Screens()
		debugf("peer %s ready, reported %d screen(s)", p.Hello().Hostname, len(pscreens))
		for _, ps := range pscreens {
			placed := *ps
			placed.Device = layout.DeviceID(p.Hello().Hostname)
			if pos, ok := placements[ps.ID]; ok {
				placed.X, placed.Y = pos.x, pos.y
			} else {
				placed.X = right
				placed.Y = primaryY
				right += ps.Width
			}
			l.Screens = append(l.Screens, &placed)
		}
	}
	if debugEnabled() {
		log.Printf("[cross] layout rebuilt: %d local + %d peer screen(s)", len(d.localScreens), len(l.Screens)-len(d.localScreens))
		for _, s := range l.Screens {
			log.Printf("[cross]   screen %-22s device=%-20s (%d,%d) %dx%d", s.ID, s.Device, s.X, s.Y, s.Width, s.Height)
		}
	}
	d.setLayout(l)
	d.releaseIfActiveGone(l)
	if err := d.srv.SetLayout(l); err != nil {
		log.Printf("adapter: push layout: %v", err)
	}
}

// releaseIfActiveGone handles a peer disconnect while the shared cursor was
// on that peer: re-enable local input and put the cursor back on the primary
// local screen, otherwise keyboard/mouse would stay suppressed forever.
func (d *Server) releaseIfActiveGone(l *layout.Layout) {
	active := d.srv.Router().Active()
	if active == nil {
		return
	}
	for _, s := range l.Screens {
		if s.Device == active.Device {
			return // device still present
		}
	}
	var primary *layout.Screen
	for _, s := range d.localScreens {
		if s.Primary {
			primary = s
			break
		}
	}
	if primary == nil && len(d.localScreens) > 0 {
		primary = d.localScreens[0]
	}
	if primary != nil {
		cx, cy := primary.X+primary.Width/2, primary.Y+primary.Height/2
		d.srv.Router().SetPosition(cx, cy)
		_ = d.inj.MovePointer(cx, cy)
	}
	if d.isRelative() {
		debugf("peer device %s gone; releasing relative mode", active.Device)
	}
	d.setRelative(false)
}

func (d *Server) onMove(x, y, dx, dy int32) {
	if d.isRelative() {
		// Relative mode: the shared cursor is on a peer's screen and the local
		// cursor is decoupled, so raw deltas keep flowing. No warping happens
		// here, which avoids feeding our own cursor moves back into the tap.
		if dx == 0 && dy == 0 {
			return
		}
		if err := d.srv.OnLocalDeltaMove(dx, dy); err != nil {
			log.Printf("adapter: forward move: %v", err)
			return
		}
		if !d.srv.Router().OnPeer() {
			// The shared cursor returned to a local screen: re-couple and
			// snap the physical cursor back to the shared position.
			d.setRelative(false)
			d.resync()
		}
		return
	}

	// Coupled mode: the local cursor follows the mouse normally (position
	// driven). Within an edge band, outward movement projects the target past
	// the boundary so the shared cursor can cross onto a peer — without
	// requiring the cursor to sit on the exact boundary pixel.
	const edgeBand = 12
	s := d.screenAtGlobal(x, y)
	if s == nil {
		debugf("move (%d,%d) not on any local screen", x, y)
		return
	}
	right := s.X + s.Width - 1
	bottom := s.Y + s.Height - 1
	nx, ny := x, y
	dir := edgeNone
	switch {
	case x >= right-edgeBand+1 && dx > 0:
		// Project beyond the right edge by the reported outward delta.
		nx, dir = right+dx, edgeRight
	case x <= s.X+edgeBand-1 && dx < 0:
		nx, dir = s.X+dx, edgeLeft
	case y >= bottom-edgeBand+1 && dy > 0:
		ny, dir = bottom+dy, edgeBottom
	case y <= s.Y+edgeBand-1 && dy < 0:
		ny, dir = s.Y+dy, edgeTop
	}
	if dir != edgeNone {
		debugf("edge band %s at (%d,%d) delta=(%d,%d) -> target (%d,%d)", dir, x, y, dx, dy, nx, ny)
	}
	if err := d.srv.OnLocalPointerMove(s, nx, ny); err != nil {
		log.Printf("adapter: move: %v", err)
		return
	}
	if cur := d.srv.Router().Active(); cur != nil && cur.Device != d.local {
		// Crossed onto a peer. Decouple (macOS) / enable edge bounce
		// (Windows) and pin the physical cursor a few pixels inside the edge
		// it crossed, so continued movement keeps producing raw deltas.
		const inset = 3
		pinX, pinY := x, y
		switch dir {
		case edgeRight:
			pinX = right - inset
		case edgeLeft:
			pinX = s.X + inset
		case edgeBottom:
			pinY = bottom - inset
		case edgeTop:
			pinY = s.Y + inset
		}
		debugf("CROSSED onto peer screen %s, relative mode, pin=(%d,%d)", cur.ID, pinX, pinY)
		// Pin first (sets the bounce anchor and warps the cursor) and only
		// then enable suppression, so the very first frame is anchored.
		if d.cap != nil {
			d.cap.PinCursor(pinX, pinY)
		}
		d.setRelative(true)
	}
}

type edgeDir string

const (
	edgeNone   edgeDir = "-"
	edgeLeft   edgeDir = "left"
	edgeRight  edgeDir = "right"
	edgeTop    edgeDir = "top"
	edgeBottom edgeDir = "bottom"
)

func (d *Server) setRelative(on bool) {
	d.mu.Lock()
	changed := d.relative != on
	d.relative = on
	cap := d.cap
	d.mu.Unlock()
	if changed && cap != nil {
		cap.SetRelativeMode(on)
	}
}

func (d *Server) isRelative() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.relative
}

// resync snaps the physical cursor back to the shared virtual position after
// returning from a peer. In coupled mode this warp is idempotent for the
// router, so its echo is harmless.
func (d *Server) resync() {
	vx, vy := d.srv.Router().Position()
	_ = d.inj.MovePointer(vx, vy)
}

func (d *Server) onButton(b event.PointerButton) {
	if err := d.srv.OnLocalButton(b); err != nil {
		log.Printf("adapter: forward button: %v", err)
	}
}

func (d *Server) onScroll(s event.PointerScroll) {
	if err := d.srv.OnLocalScroll(s); err != nil {
		log.Printf("adapter: forward scroll: %v", err)
	}
}

func (d *Server) onKey(k keymap.Key, down bool) {
	if err := d.srv.OnLocalKey(event.KeyEvent{KeyCode: k, Down: down}); err != nil {
		log.Printf("adapter: forward key: %v", err)
	}
}

// Client runs a session client node.
type Client struct {
	core
	cl  *session.Client
	cap capture.Capture
}

// RunClient connects to a session server. For RoleClient the node is a
// passive display target (it only injects); for RoleSource it also captures
// local input and forwards it as a controller.
func RunClient(addr string, cfg ClientConfig) (*Client, error) {
	screens, err := resolveScreens(cfg.Hello.Hostname, cfg.LocalScreens)
	if err != nil {
		return nil, err
	}
	d := &Client{core: *newCore(layout.DeviceID(cfg.Hello.Hostname), cfg.Injector, screens)}
	cl, err := session.Dial(addr, cfg.Hello, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("adapter: dial: %w", err)
	}
	d.cl = cl
	cl.SetLayoutHook(d.setLayout)
	cl.SetSink(d.sink)
	d.clip = cfg.Clipboard
	d.files = cfg.Files
	attachFiles(cfg.Files, cfg.Clipboard, func(_ string, env *protocol.Envelope) error {
		// The server relays file messages to other clients by "to".
		return cl.SendEnvelope(env)
	})
	d.watchClipboardSync(func(data clipboard.Data) error {
		return cl.SendClipboard(event.ClipboardEvent{Mime: data.Mime, Data: data.Data})
	})
	if err := cl.SendLayout(screens); err != nil {
		cl.Close()
		return nil, err
	}
	go func() {
		_ = cl.Serve()
		// Connection ended (server gone / network error / local close):
		// release clipboard watcher, transfers and capture resources.
		d.stopClipboard()
		if d.files != nil {
			d.files.Detach()
		}
		if d.cap != nil {
			d.cap.Stop()
			d.cap = nil
		}
		if cfg.OnDisconnect != nil {
			cfg.OnDisconnect()
		}
	}()

	if cfg.Capture != nil && cfg.Hello.Role == protocol.RoleSource {
		if err := cfg.Capture.Start(capture.Handler{
			OnMove:   d.onMove,
			OnButton: d.onButton,
			OnScroll: d.onScroll,
			OnKey:    d.onKey,
		}); err != nil {
			cl.Close()
			return nil, fmt.Errorf("adapter: start capture: %w", err)
		}
		d.cap = cfg.Capture
	}
	return d, nil
}

// Close stops capture and the session client.
func (d *Client) Close() error {
	if d.cap != nil {
		d.cap.Stop()
	}
	d.stopClipboard()
	if d.files != nil {
		d.files.Detach()
	}
	return d.cl.Close()
}

// Client exposes the underlying session client (used by tests and tools).
func (d *Client) Client() *session.Client { return d.cl }

// LocalScreens returns this device's own screens (real geometry).
func (d *Client) LocalScreens() []*layout.Screen { return d.localScreens }

func (d *Client) onMove(x, y, dx, dy int32) {
	if vx, vy, ok := d.toVirtual(x, y); ok {
		if err := d.cl.SendPointerMove(vx, vy); err != nil {
			log.Printf("adapter: send move: %v", err)
		}
	}
}

func (d *Client) onButton(b event.PointerButton) {
	if err := d.cl.SendButton(b); err != nil {
		log.Printf("adapter: send button: %v", err)
	}
}

func (d *Client) onScroll(s event.PointerScroll) {
	if err := d.cl.SendScroll(s); err != nil {
		log.Printf("adapter: send scroll: %v", err)
	}
}

func (d *Client) onKey(k keymap.Key, down bool) {
	if err := d.cl.SendKey(event.KeyEvent{KeyCode: k, Down: down}); err != nil {
		log.Printf("adapter: send key: %v", err)
	}
}

func resolveScreens(hostname string, provided []*layout.Screen) ([]*layout.Screen, error) {
	if provided != nil {
		return provided, nil
	}
	screens, err := inject.EnumerateScreens(layout.DeviceID(hostname))
	if err != nil {
		return nil, fmt.Errorf("adapter: enumerate screens: %w", err)
	}
	return screens, nil
}
