//go:build !darwin && !windows

// Package tray is unsupported off macOS/Windows: Run is a no-op so the rest
// of the program still builds on Linux CI.
package tray

// Options mirrors the supported platform's tray options.
type Options struct {
	PageURL    string
	OnOpenPage func()
	OnQuit     func()
}

// Run returns immediately on unsupported platforms.
func Run(opts Options) {}
