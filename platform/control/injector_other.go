//go:build !windows

package control

import "crossscreen/platform/inject"

// newPlatformInjector is the ordinary platform injector off Windows.
func newPlatformInjector() (inject.Injector, error) {
	return inject.New()
}
