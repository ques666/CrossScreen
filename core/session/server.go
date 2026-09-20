package session

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"crossscreen/core/event"
	"crossscreen/core/layout"
	"crossscreen/core/protocol"
	"crossscreen/core/transport"
)

// EventSink receives envelopes that must be applied on the local machine
// (input injection in M3, clipboard apply). May be nil to ignore.
type EventSink func(env *protocol.Envelope) error

// Server is the hub of a cross-screen session: it owns the shared keyboard &
// mouse, keeps peers registered, and routes input events and clipboard
// between devices according to the virtual layout.
type Server struct {
	self   *protocol.Hello
	local  layout.DeviceID
	router *Router
	sink   EventSink

	onPeersChanged func()

	mu       sync.Mutex
	peers    map[string]*Peer // key: connection remote address
	tcp      *transport.Server
	stopPing chan struct{}
	seq      uint64
}

// NewServer creates a session server for the local device described by self.
func NewServer(self *protocol.Hello, sink EventSink) *Server {
	return &Server{
		self:   self,
		local:  layout.DeviceID(self.Hostname),
		router: NewRouter(&layout.Layout{}, layout.DeviceID(self.Hostname)),
		sink:   sink,
		peers:  make(map[string]*Peer),
	}
}

// SetOnPeersChanged registers a callback invoked whenever a peer joins,
// reports screens, or leaves, so the host can rebuild the arrangement.
func (s *Server) SetOnPeersChanged(fn func()) {
	s.mu.Lock()
	s.onPeersChanged = fn
	s.mu.Unlock()
}

func (s *Server) firePeersChanged() {
	s.mu.Lock()
	fn := s.onPeersChanged
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Listen starts the TCP listener; tlsCfg may be nil for plaintext.
func (s *Server) Listen(addr string, tlsCfg *tls.Config) (net.Addr, error) {
	t, err := transport.Listen(addr, tlsCfg, s.handle, transport.ConnCallbacks{
		OnDisconnect: s.handleDisconnect,
	})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.tcp = t
	s.stopPing = make(chan struct{})
	stopPing := s.stopPing
	s.mu.Unlock()
	go t.Serve()
	go s.pingLoop(stopPing)
	return t.Addr(), nil
}

// pingLoop probes every ready peer so a dead connection is detected even when
// no input events flow. A failed write closes the connection, which triggers
// the normal disconnect cleanup.
func (s *Server) pingLoop(stop <-chan struct{}) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			for _, p := range s.snapshotPeers() {
				if !p.Ready() {
					continue
				}
				env, err := protocol.NewEnvelope(protocol.TypePing, s.nextSeq(), nil)
				if err != nil || p.conn.WriteEnvelope(env) != nil {
					p.conn.Close()
				}
			}
		}
	}
}

// Close stops the listener and drops all peers.
func (s *Server) Close() error {
	s.mu.Lock()
	peers := make([]*Peer, 0, len(s.peers))
	for _, p := range s.peers {
		peers = append(peers, p)
	}
	s.peers = map[string]*Peer{}
	tcp := s.tcp
	if s.stopPing != nil {
		close(s.stopPing)
		s.stopPing = nil
	}
	s.mu.Unlock()
	for _, p := range peers {
		p.conn.Close()
	}
	if tcp != nil {
		return tcp.Close()
	}
	return nil
}

// SetLayout installs the authoritative virtual arrangement and pushes it to
// every ready peer.
func (s *Server) SetLayout(l *layout.Layout) error {
	s.router.SetLayout(l)
	env, err := protocol.NewEnvelope(protocol.TypeLayout, s.nextSeq(), l)
	if err != nil {
		return err
	}
	for _, p := range s.snapshotPeers() {
		if p.Ready() {
			if err := p.conn.WriteEnvelope(env); err != nil {
				return err
			}
		}
	}
	return nil
}

// Router exposes the shared cursor router (used by the platform adapter to
// feed local mouse moves and by tests).
func (s *Server) Router() *Router { return s.router }

// OnLocalPointerMove routes a pointer move that originated on the local
// machine. screen is the local screen the pointer physically sits on.
func (s *Server) OnLocalPointerMove(screen *layout.Screen, x, y int32) error {
	cur := s.router.OnLocalMove(screen, x, y)
	if cur == nil || cur.Device == s.local {
		return nil // cursor stays on the local machine
	}
	p := s.peerForDevice(cur.Device)
	if p == nil {
		return nil
	}
	vx, vy := s.router.Position()
	env, err := protocol.NewEnvelope(protocol.TypePointerMove, s.nextSeq(), event.PointerMove{X: vx, Y: vy})
	if err != nil {
		return err
	}
	return p.conn.WriteEnvelope(env)
}

// OnLocalDeltaMove advances the shared cursor by (dx, dy) — the raw input
// delta of the local mouse — and, if the cursor lands on a peer's screen,
// forwards the new virtual position to that peer.
func (s *Server) OnLocalDeltaMove(dx, dy int32) error {
	cur := s.router.MoveByDelta(dx, dy)
	if cur == nil || cur.Device == s.local {
		return nil
	}
	p := s.peerForDevice(cur.Device)
	if p == nil {
		return nil
	}
	vx, vy := s.router.Position()
	env, err := protocol.NewEnvelope(protocol.TypePointerMove, s.nextSeq(), event.PointerMove{X: vx, Y: vy})
	if err != nil {
		return err
	}
	return p.conn.WriteEnvelope(env)
}

// OnLocalButton routes a mouse button event that originated locally.
func (s *Server) OnLocalButton(b event.PointerButton) error {
	return s.routeLocalEvent(protocol.TypePointerBtn, b)
}

// OnLocalScroll routes a scroll event that originated locally.
func (s *Server) OnLocalScroll(sc event.PointerScroll) error {
	return s.routeLocalEvent(protocol.TypePointerScroll, sc)
}

// OnLocalKey routes a keyboard event that originated locally.
func (s *Server) OnLocalKey(k event.KeyEvent) error {
	return s.routeLocalEvent(protocol.TypeKeyboard, k)
}

// routeLocalEvent sends a locally-originated event to the device that
// currently owns the cursor. When the cursor is on the local machine the
// event is dropped: it was already produced by the physical device.
func (s *Server) routeLocalEvent(t protocol.Type, payload any) error {
	target := s.router.Active()
	if target == nil || target.Device == s.local {
		return nil // cursor outside any screen, or on the local machine
	}
	env, err := protocol.NewEnvelope(t, s.nextSeq(), payload)
	if err != nil {
		return err
	}
	p := s.peerForDevice(target.Device)
	if p == nil {
		return nil
	}
	return p.conn.WriteEnvelope(env)
}

func (s *Server) emitLocal(env *protocol.Envelope) error {
	if s.sink != nil {
		return s.sink(env)
	}
	return nil
}

// handle dispatches envelopes from any peer connection.
func (s *Server) handle(conn *transport.Conn, env *protocol.Envelope) error {
	switch env.Type {
	case protocol.TypeHello:
		return s.handleHello(conn, env)
	case protocol.TypeLayout:
		return s.handleLayout(conn, env)
	case protocol.TypePing:
		pong, err := protocol.NewEnvelope(protocol.TypePong, s.nextSeq(), nil)
		if err != nil {
			return err
		}
		return conn.WriteEnvelope(pong)
	case protocol.TypeClipboard:
		return s.handleClipboard(conn, env)
	case protocol.TypeFileAccept, protocol.TypeFileChunk, protocol.TypeFileCancel, protocol.TypeFileDone:
		return s.routeFile(conn, env)
	case protocol.TypeBye:
		return s.removePeer(conn)
	case protocol.TypePointerMove:
		return s.routePeerMove(conn, env)
	case protocol.TypePointerBtn, protocol.TypePointerScroll, protocol.TypeKeyboard:
		return s.routeFromPeer(conn, env)
	default:
		return nil
	}
}

func (s *Server) handleHello(conn *transport.Conn, env *protocol.Envelope) error {
	var h protocol.Hello
	if err := env.Decode(&h); err != nil {
		return err
	}
	p := s.addPeer(conn, &h)
	// Mark ready before the ack so a layout broadcast right after Dial can
	// reach this peer.
	p.state = peerReady
	ack, err := protocol.NewEnvelope(protocol.TypeHelloAck, s.nextSeq(), s.self)
	if err != nil {
		return err
	}
	if err := conn.WriteEnvelope(ack); err != nil {
		return err
	}
	s.firePeersChanged()
	return nil
}

// handleLayout records the screens a peer reported. Offsets are not touched:
// the authoritative arrangement comes from the server's SetLayout.
func (s *Server) handleLayout(conn *transport.Conn, env *protocol.Envelope) error {
	var l layout.Layout
	if err := env.Decode(&l); err != nil {
		return err
	}
	p := s.peerByConn(conn)
	if p == nil {
		return nil
	}
	s.mu.Lock()
	p.setScreens(l.Screens)
	s.mu.Unlock()
	s.firePeersChanged()
	return nil
}

// handleClipboard delivers a clipboard change to the local machine and every
// other peer.
func (s *Server) handleClipboard(from *transport.Conn, env *protocol.Envelope) error {
	if err := s.emitLocal(env); err != nil {
		return err
	}
	for _, p := range s.snapshotPeers() {
		if p.conn != from {
			if err := p.conn.WriteEnvelope(env); err != nil {
				return err
			}
		}
	}
	return nil
}

// BroadcastClipboard sends a clipboard change that originated on the local
// machine to every ready peer.
func (s *Server) BroadcastClipboard(ev event.ClipboardEvent) error {
	env, err := protocol.NewEnvelope(protocol.TypeClipboard, s.nextSeq(), ev)
	if err != nil {
		return err
	}
	for _, p := range s.snapshotPeers() {
		if p.Ready() {
			if err := p.conn.WriteEnvelope(env); err != nil {
				return err
			}
		}
	}
	return nil
}

// routeFile delivers a file-transfer control message. Messages addressed to
// this server are applied locally; messages addressed to another device are
// relayed to that peer (so two clients can exchange files via the server).
func (s *Server) routeFile(from *transport.Conn, env *protocol.Envelope) error {
	var hdr struct {
		To string `json:"to"`
	}
	if err := json.Unmarshal(env.Payload, &hdr); err != nil {
		return err
	}
	if hdr.To == "" || hdr.To == string(s.local) {
		return s.emitLocal(env)
	}
	p := s.peerForDevice(layout.DeviceID(hdr.To))
	if p == nil || p.conn == from {
		return nil
	}
	return p.conn.WriteEnvelope(env)
}

// SendToDevice sends a raw envelope to one ready peer device.
func (s *Server) SendToDevice(d layout.DeviceID, env *protocol.Envelope) error {
	p := s.peerForDevice(d)
	if p == nil {
		return fmt.Errorf("session: no peer %q", d)
	}
	return p.conn.WriteEnvelope(env)
}

// routePeerMove applies a pointer move reported by a peer (a controller or a
// client crossing back) to the shared cursor. Whoever the cursor ends on now
// owns the input.
func (s *Server) routePeerMove(from *transport.Conn, env *protocol.Envelope) error {
	var m event.PointerMove
	if err := env.Decode(&m); err != nil {
		return err
	}
	cur := s.router.MoveTo(m.X, m.Y)
	if cur == nil {
		return nil
	}
	if cur.Device == s.local {
		return s.emitLocal(env)
	}
	p := s.peerForDevice(cur.Device)
	if p == nil || p.conn == from {
		return nil
	}
	return p.conn.WriteEnvelope(env)
}

// routeFromPeer routes an input event sent by a peer (a client crossing back
// or a RoleSource controller) to the device that currently owns the cursor.
func (s *Server) routeFromPeer(conn *transport.Conn, env *protocol.Envelope) error {
	target := s.router.Active()
	if target == nil {
		return nil
	}
	if target.Device == s.local {
		return s.emitLocal(env)
	}
	p := s.peerForDevice(target.Device)
	if p == nil || p.conn == conn {
		return nil
	}
	return p.conn.WriteEnvelope(env)
}

func (s *Server) handleDisconnect(conn *transport.Conn) {
	s.mu.Lock()
	delete(s.peers, conn.RemoteAddr().String())
	s.mu.Unlock()
	s.firePeersChanged()
}

func (s *Server) removePeer(conn *transport.Conn) error {
	s.handleDisconnect(conn)
	return conn.Close()
}

func (s *Server) addPeer(conn *transport.Conn, h *protocol.Hello) *Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := conn.RemoteAddr().String()
	p := &Peer{
		conn:      conn,
		hello:     h,
		state:     peerHandshaking,
		connected: timeNow(),
	}
	if old, ok := s.peers[key]; ok {
		old.state = peerClosed
	}
	s.peers[key] = p
	return p
}

func (s *Server) peerByConn(conn *transport.Conn) *Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peers[conn.RemoteAddr().String()]
}

func (s *Server) peerForDevice(d layout.DeviceID) *Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.peers {
		if p.hello.Hostname == string(d) && p.state == peerReady {
			return p
		}
	}
	return nil
}

func (s *Server) snapshotPeers() []*Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Peer, 0, len(s.peers))
	for _, p := range s.peers {
		out = append(out, p)
	}
	return out
}

// Peers returns a snapshot of the connected peers.
func (s *Server) Peers() []*Peer { return s.snapshotPeers() }

// PeerForDevice returns the ready peer for a device id, or nil.
func (s *Server) PeerForDevice(d layout.DeviceID) *Peer { return s.peerForDevice(d) }

func (s *Server) nextSeq() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return s.seq
}
