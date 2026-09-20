//go:build !darwin && !windows

package clipboard

import "errors"

func platformInit() error {
	return errors.New("clipboard: unsupported platform")
}

func platformWrite(d Data) error {
	return errors.New("clipboard: unsupported platform")
}

func platformRead() (Data, bool, error) {
	return Data{}, false, errors.New("clipboard: unsupported platform")
}

func platformSequence() (uint64, error) {
	return 0, errors.New("clipboard: unsupported platform")
}
