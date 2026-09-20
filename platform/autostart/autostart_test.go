package autostart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Runs only on darwin in this environment; exercises plist create/remove.
func TestEnableDisableDarwin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Point os.Executable-independent paths: our darwin impl uses HomeDir.
	plist := filepath.Join(home, "Library", "LaunchAgents", label+".plist")

	if Enabled() {
		t.Fatal("should start disabled in temp home")
	}
	if err := Enable(); err != nil {
		t.Skipf("enable unsupported here: %v", err)
	}
	if !Enabled() {
		t.Fatal("login item missing after enable")
	}
	b, err := os.ReadFile(plist)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), label) || !strings.Contains(string(b), "RunAtLoad") {
		t.Fatalf("plist content wrong:\n%s", b)
	}
	if err := Disable(); err != nil {
		t.Fatal(err)
	}
	if Enabled() {
		t.Fatal("login item still present after disable")
	}
}
