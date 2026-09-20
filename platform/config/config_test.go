package config

import "testing"

func TestLoadSaveRoundTrip(t *testing.T) {
	t.Setenv("CROSSSCREEN_CONFIG_DIR", t.TempDir())

	s := Load()
	if s.ListenAddr == "" || s.UIPort == 0 {
		t.Fatalf("defaults not applied: %+v", s)
	}
	s.DeviceName = "my-device"
	s.ClientAddr = "10.0.0.5:53317"
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	got := Load()
	if got.DeviceName != "my-device" || got.ClientAddr != "10.0.0.5:53317" {
		t.Fatalf("persisted values mismatch: %+v", got)
	}
}

func TestDefaultsOnMissingFile(t *testing.T) {
	t.Setenv("CROSSSCREEN_CONFIG_DIR", t.TempDir())
	s := Load()
	if s.UIPort != 8080 || s.ListenAddr != "0.0.0.0:53317" {
		t.Fatalf("unexpected defaults: %+v", s)
	}
}
