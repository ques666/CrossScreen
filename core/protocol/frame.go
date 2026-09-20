// Package protocol implements the wire-level framing and message envelope
// used by every CrossScreen endpoint (macOS and Windows).
//
// Wire format (integers big-endian):
//
//	+--------+--------+--------+----------------+----------+
//	| magic0 | magic1 | ver    | length (uint32)| payload  |
//	+--------+--------+--------+----------------+----------+
//
// payload is the JSON-encoded Envelope (see message.go).
package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	magic0     = 0x43 // 'C'
	magic1     = 0x53 // 'S'
	version    = 1
	headerSize = 2 + 1 + 4
	// MaxFrameLen caps a single frame (4 MiB) to keep memory bounded.
	MaxFrameLen = 4 << 20
)

var (
	ErrBadMagic   = errors.New("protocol: bad frame magic")
	ErrBadVersion = errors.New("protocol: unsupported protocol version")
	ErrTooLarge   = errors.New("protocol: frame exceeds maximum size")
	ErrTruncated  = errors.New("protocol: truncated frame")
)

// WriteFrame writes one length-prefixed frame to w.
func WriteFrame(w io.Writer, payload []byte) error {
	if len(payload) > MaxFrameLen {
		return ErrTooLarge
	}
	var hdr [headerSize]byte
	hdr[0], hdr[1], hdr[2] = magic0, magic1, version
	binary.BigEndian.PutUint32(hdr[3:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// ReadFrame reads one frame from r.
func ReadFrame(r io.Reader) ([]byte, error) {
	var hdr [headerSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	if hdr[0] != magic0 || hdr[1] != magic1 {
		return nil, ErrBadMagic
	}
	if hdr[2] != version {
		return nil, fmt.Errorf("%w: got %d", ErrBadVersion, hdr[2])
	}
	n := binary.BigEndian.Uint32(hdr[3:])
	if n > MaxFrameLen {
		return nil, ErrTooLarge
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTruncated, err)
	}
	return buf, nil
}
