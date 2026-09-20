//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideWindow prevents helper processes (cmd.exe) from showing a console.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
