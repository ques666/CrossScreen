// Package config persists user settings (device name, server listen
// address, last client target, UI port) across launches.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Settings holds the persisted cross-screen configuration.
type Settings struct {
	// DeviceName is advertised to peers; defaults to the hostname.
	DeviceName string `json:"device_name"`
	// ListenAddr is where the node listens as a server.
	ListenAddr string `json:"listen_addr"`
	// ClientAddr is the last server this device connected to as a client.
	ClientAddr string `json:"client_addr"`
	// UIPort is the local control-panel port.
	UIPort int `json:"ui_port"`
	// AutoStart mirrors the OS login-item state for display in the UI.
	AutoStart bool `json:"auto_start"`
}

const (
	defaultListen = "0.0.0.0:53317"
	defaultUI     = 8080
	dirName       = "crossscreen"
	fileName      = "settings.json"
)

// Dir returns the per-user configuration directory, creating it if needed.
// CROSSSCREEN_CONFIG_DIR overrides the location (mainly for tests).
func Dir() (string, error) {
	var base string
	if override := os.Getenv("CROSSSCREEN_CONFIG_DIR"); override != "" {
		base = override
	} else {
		b, err := os.UserConfigDir()
		if err != nil {
			home, herr := os.UserHomeDir()
			if herr != nil {
				return "", err
			}
			b = filepath.Join(home, ".config")
		}
		base = b
	}
	dir := filepath.Join(base, dirName)
	return dir, os.MkdirAll(dir, 0o755)
}

func path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

// StagingDir returns the sandbox directory where files received from peers
// are materialized before the user pastes them, creating it if needed. The
// system clipboard only references files inside this directory.
func StagingDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(dir, "received")
	return d, os.MkdirAll(d, 0o755)
}

// Default returns settings with built-in defaults filled in.
func Default() *Settings {
	return &Settings{
		ListenAddr: defaultListen,
		ClientAddr: "",
		UIPort:     defaultUI,
	}
}

// Load reads settings; missing file yields defaults.
func Load() *Settings {
	s := Default()
	p, err := path()
	if err != nil {
		return s
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(b, s)
	if s.ListenAddr == "" {
		s.ListenAddr = defaultListen
	}
	if s.UIPort == 0 {
		s.UIPort = defaultUI
	}
	return s
}

// Save writes settings atomically.
func (s *Settings) Save() error {
	p, err := path()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
