//go:build !darwin && !windows

package capture

import "errors"

func newCapture() (Capture, error) {
	return nil, errors.New("capture: unsupported platform")
}
