package protocol

import (
	"encoding/json"
	"fmt"
)

// Type identifies the kind of a message. The value decides which payload
// struct a receiver should decode from the Envelope.
type Type uint8

const (
	TypeHello         Type = 1
	TypeHelloAck      Type = 2
	TypeLayout        Type = 3
	TypePointerMove   Type = 4
	TypePointerBtn    Type = 5
	TypePointerScroll Type = 6
	TypeKeyboard      Type = 7
	TypeClipboard     Type = 8
	TypePing          Type = 9
	TypePong          Type = 10
	TypeBye           Type = 11
)

func (t Type) String() string {
	switch t {
	case TypeHello:
		return "hello"
	case TypeHelloAck:
		return "hello_ack"
	case TypeLayout:
		return "layout"
	case TypePointerMove:
		return "pointer_move"
	case TypePointerBtn:
		return "pointer_button"
	case TypePointerScroll:
		return "pointer_scroll"
	case TypeKeyboard:
		return "keyboard"
	case TypeClipboard:
		return "clipboard"
	case TypeFileAccept:
		return "file_accept"
	case TypeFileChunk:
		return "file_chunk"
	case TypeFileCancel:
		return "file_cancel"
	case TypeFileDone:
		return "file_done"
	case TypePing:
		return "ping"
	case TypePong:
		return "pong"
	case TypeBye:
		return "bye"
	default:
		return fmt.Sprintf("type(%d)", t)
	}
}

// Role describes how a device participates in a session.
type Role uint8

const (
	// RoleServer owns the physical keyboard & mouse and routes input to clients.
	RoleServer Role = 1
	// RoleClient receives input from the server and injects it locally.
	RoleClient Role = 2
	// RoleSource generates input (e.g. a touch surface or remote keyboard)
	// but cannot inject it locally.
	RoleSource Role = 3
)

func (r Role) String() string {
	switch r {
	case RoleServer:
		return "server"
	case RoleClient:
		return "client"
	case RoleSource:
		return "source"
	default:
		return fmt.Sprintf("role(%d)", r)
	}
}

// Envelope is the JSON payload carried inside every frame.
type Envelope struct {
	Type    Type            `json:"type"`
	Seq     uint64          `json:"seq"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// NewEnvelope builds an Envelope, marshaling payload (may be nil).
func NewEnvelope(t Type, seq uint64, payload any) (*Envelope, error) {
	e := &Envelope{Type: t, Seq: seq}
	if payload == nil {
		return e, nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("protocol: marshal %s payload: %w", t, err)
	}
	e.Payload = raw
	return e, nil
}

// Decode unmarshals the envelope payload into v.
func (e *Envelope) Decode(v any) error {
	if len(e.Payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(e.Payload, v); err != nil {
		return fmt.Errorf("protocol: decode %s payload: %w", e.Type, err)
	}
	return nil
}

// Marshal serializes the envelope to JSON (the frame payload).
func (e *Envelope) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// DecodeEnvelope parses a JSON envelope.
func DecodeEnvelope(data []byte) (*Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("protocol: decode envelope: %w", err)
	}
	return &e, nil
}

// Hello is exchanged on connection setup.
type Hello struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"` // "macos" | "windows"
	Role     Role   `json:"role"`
	Version  string `json:"version"`
}

// Bye is sent before a graceful disconnect.
type Bye struct {
	Reason string `json:"reason,omitempty"`
}
