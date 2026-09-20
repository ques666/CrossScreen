package protocol

import (
	"bytes"
	"io"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	hello := &Hello{Hostname: "mac-studio", OS: "macos", Role: RoleServer, Version: "0.1.0"}
	env, err := NewEnvelope(TypeHello, 1, hello)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	payload, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var buf bytes.Buffer
	if err := WriteFrame(&buf, payload); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("frame payload mismatch:\n got %s\nwant %s", got, payload)
	}

	env2, err := DecodeEnvelope(got)
	if err != nil {
		t.Fatalf("DecodeEnvelope: %v", err)
	}
	if env2.Type != TypeHello || env2.Seq != 1 {
		t.Fatalf("envelope fields mismatch: %+v", env2)
	}
	var h Hello
	if err := env2.Decode(&h); err != nil {
		t.Fatalf("Decode payload: %v", err)
	}
	if h != *hello {
		t.Fatalf("payload mismatch: got %+v want %+v", h, *hello)
	}
}

func TestRejectBadMagic(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{0x00, 0x00, version, 0, 0, 0, 0})
	if _, err := ReadFrame(&buf); err != ErrBadMagic {
		t.Fatalf("want ErrBadMagic, got %v", err)
	}
}

func TestRejectBadVersion(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{magic0, magic1, 99, 0, 0, 0, 0})
	if _, err := ReadFrame(&buf); err == nil {
		t.Fatal("want version error, got nil")
	}
}

func TestRejectOversizedWrite(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, make([]byte, MaxFrameLen+1)); err != ErrTooLarge {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestRejectOversizedRead(t *testing.T) {
	var buf bytes.Buffer
	hdr := []byte{magic0, magic1, version, 0xFF, 0xFF, 0xFF, 0xFF}
	buf.Write(hdr)
	if _, err := ReadFrame(&buf); err != ErrTooLarge {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestStreamMultipleFrames(t *testing.T) {
	var buf bytes.Buffer
	want := [][]byte{[]byte("one"), []byte("two"), []byte("three")}
	for _, p := range want {
		if err := WriteFrame(&buf, p); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
	}
	for i, p := range want {
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame #%d: %v", i, err)
		}
		if !bytes.Equal(got, p) {
			t.Fatalf("frame #%d mismatch: got %q want %q", i, got, p)
		}
	}
	if _, err := ReadFrame(&buf); err != io.EOF {
		t.Fatalf("want EOF after stream, got %v", err)
	}
}

func TestTruncatedFrame(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, []byte("payload")); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	raw := buf.Bytes()[:len(buf.Bytes())-3] // cut the tail
	if _, err := ReadFrame(bytes.NewReader(raw)); err == nil {
		t.Fatal("want truncated error, got nil")
	}
}

func TestNilPayloadEnvelope(t *testing.T) {
	env, err := NewEnvelope(TypePing, 2, nil)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	payload, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	env2, err := DecodeEnvelope(payload)
	if err != nil {
		t.Fatalf("DecodeEnvelope: %v", err)
	}
	if env2.Type != TypePing {
		t.Fatalf("type mismatch: got %v", env2.Type)
	}
	var v any
	if err := env2.Decode(&v); err != nil {
		t.Fatalf("Decode empty payload: %v", err)
	}
}
