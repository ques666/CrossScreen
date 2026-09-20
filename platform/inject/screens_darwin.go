//go:build darwin

package inject

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>
*/
import "C"

import (
	"fmt"

	"crossscreen/core/layout"
)

// enumerateScreens returns the device's displays in the global display
// coordinate space (the main display sits at the origin).
func enumerateScreens(device layout.DeviceID) ([]*layout.Screen, error) {
	var count C.uint32_t
	if err := C.CGGetActiveDisplayList(0, nil, &count); err != 0 {
		return nil, fmt.Errorf("inject: CGGetActiveDisplayList: %d", err)
	}
	if count == 0 {
		return nil, nil
	}
	ids := make([]C.CGDirectDisplayID, count)
	if err := C.CGGetActiveDisplayList(count, &ids[0], &count); err != 0 {
		return nil, fmt.Errorf("inject: CGGetActiveDisplayList: %d", err)
	}
	main := C.CGMainDisplayID()
	screens := make([]*layout.Screen, 0, count)
	for i, id := range ids {
		b := C.CGDisplayBounds(id)
		screens = append(screens, &layout.Screen{
			ID:      layout.ScreenID(fmt.Sprintf("%s:%d", device, i)),
			Device:  device,
			X:       int32(b.origin.x),
			Y:       int32(b.origin.y),
			Width:   int32(b.size.width),
			Height:  int32(b.size.height),
			Primary: id == main,
		})
	}
	return screens, nil
}
