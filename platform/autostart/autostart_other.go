//go:build !darwin && !windows

package autostart

import "errors"

var errUnsupported = errors.New("autostart: unsupported platform")

func enabled() bool  { return false }
func enable() error  { return errUnsupported }
func disable() error { return nil }
