//go:build windows

package winsec

import (
	"syscall"

	"crossscreen/platform/inject"
)

type syscallHandle = syscall.Handle

// applyMsg maps one neutral pipe message back to an injector call. Kept
// identical on both sides by sharing core/event, so virtual-key mapping
// lives only in the platform injector.
func applyMsg(inj inject.Injector, m Msg) error {
	switch m.T {
	case MsgMove:
		if m.Move == nil {
			return nil
		}
		return inj.MovePointer(m.Move.X, m.Move.Y)
	case MsgButton:
		if m.Button == nil {
			return nil
		}
		return inj.Button(m.Button.Button, m.Button.Down)
	case MsgScroll:
		if m.Scroll == nil {
			return nil
		}
		return inj.Scroll(m.Scroll.DX, m.Scroll.DY)
	case MsgKey:
		if m.Key == nil {
			return nil
		}
		return inj.Key(m.Key.KeyCode, m.Key.Down)
	case MsgPing:
		return nil
	default:
		return nil
	}
}
