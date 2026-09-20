//go:build windows

package winsec

import (
	"os"
)

// HandleCommand intercepts special process modes ("svc" / "agent") before the
// normal panel startup. Returns true if the invocation was consumed.
func HandleCommand(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[1] {
	case "svc":
		if err := RunService(); err != nil {
			os.Exit(1)
		}
		return true
	case agentCmd:
		stop := make(chan struct{})
		if err := RunAgent(stop); err != nil {
			os.Exit(1)
		}
		return true
	case "diag":
		Diagnose()
		return true
	}
	return false
}
