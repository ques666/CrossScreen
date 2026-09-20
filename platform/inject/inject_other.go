//go:build !darwin && !windows

package inject

import "errors"

// New returns an error on platforms without an input injector.
func New() (Injector, error) {
	return nil, errors.New("inject: unsupported platform")
}
