// Package discovery implements LAN device discovery: a Beacon periodically
// broadcasts an announcement over UDP, and a Listener receives those
// announcements on the same port.
package discovery

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"crossscreen/core/protocol"
)

const (
	// DefaultPort is the UDP discovery port.
	DefaultPort = 53317
	// BroadcastAddr is the IPv4 subnet broadcast address.
	BroadcastAddr = "255.255.255.255"
)

// AnnounceInterval is how often a beacon broadcasts. Exposed so tests can
// shorten it.
var AnnounceInterval = 2 * time.Second

// Announcement describes a device available on the LAN.
type Announcement struct {
	Name    string        `json:"name"`
	Host    string        `json:"host"`
	Port    int           `json:"port"` // TCP port to connect to
	OS      string        `json:"os"`
	Role    protocol.Role `json:"role"`
	Version string        `json:"version"`
}

// DefaultBroadcastTarget returns the standard broadcast endpoint.
func DefaultBroadcastTarget() string {
	return fmt.Sprintf("%s:%d", BroadcastAddr, DefaultPort)
}

// Beacon periodically broadcasts an announcement until Close.
type Beacon struct {
	conn   *net.UDPConn
	target *net.UDPAddr
	a      Announcement
	stop   chan struct{}
	once   sync.Once
	wg     sync.WaitGroup
}

// NewBeacon starts broadcasting a to target (e.g. "255.255.255.255:53317").
func NewBeacon(target string, a Announcement) (*Beacon, error) {
	ua, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialUDP("udp", nil, ua)
	if err != nil {
		return nil, err
	}
	if err := setBroadcast(conn); err != nil {
		conn.Close()
		return nil, err
	}
	b := &Beacon{conn: conn, target: ua, a: a, stop: make(chan struct{})}
	b.wg.Add(1)
	go b.loop()
	return b, nil
}

func (b *Beacon) loop() {
	defer b.wg.Done()
	payload, err := json.Marshal(b.a)
	if err != nil {
		return
	}
	t := time.NewTicker(AnnounceInterval)
	defer t.Stop()
	for {
		select {
		case <-b.stop:
			return
		case <-t.C:
			// Best-effort; listeners may be absent.
			b.conn.Write(payload)
		}
	}
}

// Close stops the beacon.
func (b *Beacon) Close() error {
	b.once.Do(func() { close(b.stop) })
	b.wg.Wait()
	return b.conn.Close()
}

// Listen returns a channel of announcements heard on addr (e.g. ":53317")
// plus the bound local address. The channel closes when the listener stops.
func Listen(addr string) (<-chan Announcement, net.Addr, error) {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, nil, err
	}
	ch := make(chan Announcement, 16)
	go func() {
		defer close(ch)
		defer pc.Close()
		buf := make([]byte, 1500)
		for {
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var a Announcement
			if json.Unmarshal(buf[:n], &a) == nil && a.Name != "" {
				select {
				case ch <- a:
				default: // drop if the consumer is slow
				}
			}
		}
	}()
	return ch, pc.LocalAddr(), nil
}
