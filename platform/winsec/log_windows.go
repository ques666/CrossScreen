//go:build windows

package winsec

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// AgentLogPath is the file the SYSTEM-spawned agent appends its diagnostics
// to. The agent has no console (it is created by the service), so every
// log.Printf it emits is otherwise invisible. The path is fixed under the
// system temp directory so the elevated diag command can read it back.
func AgentLogPath() string {
	return filepath.Join(os.Getenv("SystemRoot"), "Temp", "crossscreen-agent.log")
}

// appendAgentLog writes a timestamped line to the agent log file. It is used
// by the agent and also by the elevated diag command (which writes to the
// same system temp location) so the whole chain has one readable trail.
func appendAgentLog(format string, args ...any) {
	line := fmt.Sprintf("%s %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
	f, err := os.OpenFile(AgentLogPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}
