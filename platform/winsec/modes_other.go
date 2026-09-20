//go:build !windows

// Package winsec is a no-op on non-Windows platforms: secure-desktop
// injection is Windows-only.
package winsec

// HandleCommand never consumes arguments on non-Windows.
func HandleCommand(args []string) bool { return false }
