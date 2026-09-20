//go:build !windows

package inject

// Startup is a no-op off Windows (DPI/elevation are handled by the OS there).
func Startup(requireElevated bool) bool { return false }

// IsElevated is always false off Windows.
func IsElevated() bool { return false }
