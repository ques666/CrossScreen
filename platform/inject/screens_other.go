//go:build !darwin && !windows

package inject

import (
	"errors"

	"crossscreen/core/layout"
)

func enumerateScreens(device layout.DeviceID) ([]*layout.Screen, error) {
	return nil, errors.New("inject: unsupported platform")
}
