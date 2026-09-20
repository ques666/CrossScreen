//go:build windows

// Frame codec and message types for the local named pipe between the
// interactive CrossScreen app and the SYSTEM secure-desktop agent.
//
// Wire format mirrors the network framing: 4-byte big-endian length followed
// by a JSON payload. Only neutral input events (core/event) are exchanged,
// so the agent reuses the platform injector for virtual-key mapping.
package winsec

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"

	"crossscreen/core/event"
)

// PipeName is the single per-machine control channel. One console session is
// supported (the typical KVM case); RDP multi-session is out of scope.
const PipeName = `\\.\pipe\CrossScreenInput`

// maxPipeMsg caps one frame (largest useful input message is a few hundred
// bytes; 1 MiB is generous headroom).
const maxPipeMsg = 1 << 20

// MsgType selects which neutral event a frame carries.
type MsgType string

const (
	MsgMove   MsgType = "move"   // event.PointerMove
	MsgButton MsgType = "button" // event.PointerButton
	MsgScroll MsgType = "scroll" // event.PointerScroll
	MsgKey    MsgType = "key"    // event.KeyEvent
	MsgPing   MsgType = "ping"   // liveness probe (no payload)
)

// Msg is the envelope exchanged over the pipe. Exactly one payload is set.
type Msg struct {
	T      MsgType              `json:"t"`
	Move   *event.PointerMove   `json:"m,omitempty"`
	Button *event.PointerButton `json:"b,omitempty"`
	Scroll *event.PointerScroll `json:"s,omitempty"`
	Key    *event.KeyEvent      `json:"k,omitempty"`
}

// WriteMsg writes one length-prefixed frame as a single write so that
// concurrent writers on the same pipe handle cannot interleave the length
// header with a previous frame's payload.
func WriteMsg(w io.Writer, m Msg) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("winsec: marshal msg: %w", err)
	}
	if len(raw) > maxPipeMsg {
		return fmt.Errorf("winsec: pipe message too large: %d", len(raw))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(raw)))
	buf := make([]byte, 4+len(raw))
	copy(buf, hdr[:])
	copy(buf[4:], raw)
	_, err = w.Write(buf)
	return err
}

// ReadMsg reads one length-prefixed frame.
func ReadMsg(r io.Reader) (Msg, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Msg{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > maxPipeMsg {
		return Msg{}, fmt.Errorf("winsec: bad pipe frame length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return Msg{}, err
	}
	var m Msg
	if err := json.Unmarshal(buf, &m); err != nil {
		return Msg{}, fmt.Errorf("winsec: decode msg: %w", err)
	}
	return m, nil
}
