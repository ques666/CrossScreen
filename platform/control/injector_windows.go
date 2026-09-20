//go:build windows

package control

import (
	"crossscreen/platform/inject"
	"crossscreen/platform/winsec"
)

// newPlatformInjector returns the secure-desktop-aware routing injector:
// direct SendInput on the normal desktop, pipe forwarding to the SYSTEM agent
// while the machine is locked.
func newPlatformInjector() (inject.Injector, error) {
	return winsec.NewProxyInjector()
}
