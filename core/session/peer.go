// Package session orchestrates a cross-screen session: handshake, peer
// management, shared layout distribution and input event routing.
//
// Roles: a Server owns the shared keyboard & mouse and routes input to the
// device whose screen currently holds the virtual cursor. Clients inject
// received input locally. RoleSource peers (a dedicated input source)
// generate input but cannot inject it; their events are routed to the active
// device.
package session

import (
	"sync"
	"time"

	"crossscreen/core/layout"
	"crossscreen/core/protocol"
	"crossscreen/core/transport"
)

type peerState uint8

const (
	peerHandshaking peerState = iota
	peerReady
	peerClosed
)

// Peer is a connected remote device.
type Peer struct {
	conn      *transport.Conn
	hello     *protocol.Hello
	state     peerState
	screens   []*layout.Screen // screens the peer reported (dimensions only)
	connected time.Time
	mu        sync.Mutex
}

// Hello returns the peer's handshake information.
func (p *Peer) Hello() *protocol.Hello { return p.hello }

// Screens returns the screens the peer reported.
func (p *Peer) Screens() []*layout.Screen {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.screens
}

// setScreens records the screens the peer reported.
func (p *Peer) setScreens(s []*layout.Screen) {
	p.mu.Lock()
	p.screens = s
	p.mu.Unlock()
}

// Ready reports whether the handshake completed.
func (p *Peer) Ready() bool { return p.state == peerReady }

func timeNow() time.Time { return time.Now() }
