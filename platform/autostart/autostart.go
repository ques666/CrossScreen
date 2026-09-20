// Package autostart manages the OS "launch at login" item for CrossScreen.
package autostart

// Enabled reports whether the login item currently exists.
func Enabled() bool { return enabled() }

// Enable creates the login item pointing at the running executable.
func Enable() error { return enable() }

// Disable removes the login item.
func Disable() error { return disable() }
