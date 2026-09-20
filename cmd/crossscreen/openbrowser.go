package main

import (
	"os/exec"
	"runtime"
)

// openBrowser launches the default browser without flashing a console window
// on Windows.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		// cmd /c start opens the URL with the default handler; CREATE_NO_WINDOW
		// prevents a black console flash.
		cmd = exec.Command("cmd", "/c", "start", "", url)
		hideWindow(cmd)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
