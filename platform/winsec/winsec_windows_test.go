//go:build windows

package winsec

import (
	"bytes"
	"testing"

	"crossscreen/core/event"
)

func TestPipeMsgRoundTrip(t *testing.T) {
	cases := []Msg{
		{T: MsgPing},
		{T: MsgMove, Move: &event.PointerMove{X: -120, Y: 2048}},
		{T: MsgButton, Button: &event.PointerButton{Button: 2, Down: true, Count: 1}},
		{T: MsgScroll, Scroll: &event.PointerScroll{DX: 0, DY: -3}},
		{T: MsgKey, Key: &event.KeyEvent{KeyCode: 0x123, Down: true, Mods: 0b1011}},
	}
	for _, m := range cases {
		var buf bytes.Buffer
		if err := WriteMsg(&buf, m); err != nil {
			t.Fatalf("write %s: %v", m.T, err)
		}
		got, err := ReadMsg(&buf)
		if err != nil {
			t.Fatalf("read %s: %v", m.T, err)
		}
		if got.T != m.T {
			t.Fatalf("type mismatch: %s != %s", got.T, m.T)
		}
		if (m.Key != nil) != (got.Key != nil) || (m.Key != nil && *got.Key != *m.Key) {
			t.Fatalf("key payload mismatch: %+v vs %+v", m.Key, got.Key)
		}
		if (m.Move != nil) != (got.Move != nil) || (m.Move != nil && *got.Move != *m.Move) {
			t.Fatalf("move payload mismatch: %+v vs %+v", m.Move, got.Move)
		}
	}
}

func TestBadFrameLength(t *testing.T) {
	if _, err := ReadMsg(bytes.NewReader([]byte{0, 0})); err == nil {
		t.Fatal("expected truncated header error")
	}
	big := []byte{0, 0, 0xff, 0xff}
	if _, err := ReadMsg(bytes.NewReader(big)); err == nil {
		t.Fatal("expected oversize frame error")
	}
}

func TestServiceCommandLine(t *testing.T) {
	// Simulate the SCM argument layout handled by modes_windows.go.
	args := [][]string{
		{`C:\Program Files\CrossScreen\CrossScreen.exe`, "svc"},
		{`CrossScreen.exe`, "agent"},
	}
	for _, a := range args {
		if len(a) < 2 || (a[1] != "svc" && a[1] != agentCmd) {
			t.Fatalf("internal mode not recognized: %v", a)
		}
	}
}
