package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"crossscreen/core/event"
	"crossscreen/core/keymap"
	"crossscreen/core/layout"
	"crossscreen/platform/adapter"
	"crossscreen/platform/capture"
	"crossscreen/platform/clipboard"
	"crossscreen/platform/config"
	"crossscreen/platform/inject"
)

type fakeCapture struct{ h capture.Handler }

func (f *fakeCapture) Start(h capture.Handler) error   { f.h = h; return nil }
func (f *fakeCapture) Stop()                           {}
func (f *fakeCapture) Position() (int32, int32, error) { return 100, 100, nil }
func (f *fakeCapture) SetRelativeMode(on bool)         {}
func (f *fakeCapture) PinCursor(x, y int32)            {}

type fakeInjector struct{}

func (f *fakeInjector) MovePointer(x, y int32) error                { return nil }
func (f *fakeInjector) Button(b event.MouseButton, down bool) error { return nil }
func (f *fakeInjector) Scroll(dx, dy int32) error                   { return nil }
func (f *fakeInjector) Key(k keymap.Key, down bool) error           { return nil }

type fakeClipboard struct{}

func (f *fakeClipboard) Watch(ctx context.Context, h func(clipboard.Data)) {}
func (f *fakeClipboard) Write(d clipboard.Data) error                      { return nil }

// newTestApp creates a control app whose "local machine" is named name.
func newTestApp(name string) *App {
	return New(Config{
		NodeAddr: "127.0.0.1:0",
		LocalScreens: []*layout.Screen{
			{ID: layout.ScreenID(name + ":0"), Device: layout.DeviceID(name), X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true},
		},
		NewCapture:   func() (capture.Capture, error) { return &fakeCapture{}, nil },
		NewInjector:  func() (inject.Injector, error) { return &fakeInjector{}, nil },
		NewClipboard: func() (adapter.Clipboard, error) { return &fakeClipboard{}, nil },
	})
}

func post(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func decode(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func getState(t *testing.T, url string) State {
	t.Helper()
	resp, err := http.Get(url + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	var st State
	decode(t, resp, &st)
	return st
}

func waitState(t *testing.T, url string, cond func(State) bool) State {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		st := getState(t, url)
		if cond(st) {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("state condition not met within timeout")
	return State{}
}

func TestServerStartAndState(t *testing.T) {
	app := newTestApp("mac")
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	defer app.StopNode()

	var st State
	decode(t, post(t, ts.URL+"/api/server/start", `{"name":"mac"}`), &st)
	if st.Role != "server" || !st.Connected {
		t.Fatalf("unexpected state: %+v", st)
	}
	if !st.Capture {
		t.Fatal("capture should be enabled with fake factories")
	}
	if len(st.LocalScreens) != 1 || st.LocalScreens[0].Width != 1920 {
		t.Fatalf("local screens: %+v", st.LocalScreens)
	}
	if st.NodeAddr == "" {
		t.Fatal("node addr missing")
	}
}

// TestClientConnectAndRole uses two independent apps (two machines): a
// server "mac" and a client "win" that connects to it.
func TestClientConnectAndRole(t *testing.T) {
	srvApp := newTestApp("mac")
	srvTS := httptest.NewServer(srvApp.Handler())
	defer srvTS.Close()
	defer srvApp.StopNode()

	var st State
	decode(t, post(t, srvTS.URL+"/api/server/start", `{"name":"mac"}`), &st)
	nodeAddr := st.NodeAddr

	cliApp := newTestApp("win")
	cliTS := httptest.NewServer(cliApp.Handler())
	defer cliTS.Close()
	defer cliApp.StopNode()

	decode(t, post(t, cliTS.URL+"/api/client/connect", `{"addr":"`+nodeAddr+`","name":"win"}`), &st)
	if st.Role != "client" || !st.Connected {
		t.Fatalf("unexpected client state: %+v", st)
	}
	if st.Server != "mac" {
		t.Fatalf("server field: %q, want mac", st.Server)
	}
}

func TestPlacementWithClient(t *testing.T) {
	srvApp := newTestApp("mac")
	srvTS := httptest.NewServer(srvApp.Handler())
	defer srvTS.Close()
	defer srvApp.StopNode()

	var st State
	decode(t, post(t, srvTS.URL+"/api/server/start", `{"name":"mac"}`), &st)
	nodeAddr := st.NodeAddr

	cliApp := newTestApp("win")
	cliTS := httptest.NewServer(cliApp.Handler())
	defer cliTS.Close()
	defer cliApp.StopNode()
	post(t, cliTS.URL+"/api/client/connect", `{"addr":"`+nodeAddr+`","name":"win"}`)

	// Wait until the server auto-placed the peer and reports it.
	st = waitState(t, srvTS.URL, func(s State) bool {
		return len(s.Peers) == 1 && len(s.Layout.Screens) == 2
	})
	peerID := st.Peers[0].Screens[0].ID

	// Manual placement must take effect in the authoritative layout.
	decode(t, post(t, srvTS.URL+"/api/placement", `{"id":"`+string(peerID)+`","x":100,"y":200}`), &st)
	found := false
	for _, s := range st.Layout.Screens {
		if s.ID == peerID {
			found = true
			if s.X != 100 || s.Y != 200 {
				t.Fatalf("placement not applied: %+v", s)
			}
		}
	}
	if !found {
		t.Fatal("peer screen missing from layout")
	}

	// Reset falls back to auto-layout (right of the local screen).
	decode(t, post(t, srvTS.URL+"/api/placement", `{"id":"`+string(peerID)+`","reset":true}`), &st)
	for _, s := range st.Layout.Screens {
		if s.ID == peerID && s.X != 1920 {
			t.Fatalf("reset placement: %+v, want x=1920", s)
		}
	}
}

func TestStopNode(t *testing.T) {
	app := newTestApp("mac")
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()

	decode(t, post(t, ts.URL+"/api/server/start", `{"name":"mac"}`), &State{})
	var st State
	decode(t, post(t, ts.URL+"/api/stop", `{}`), &st)
	if st.Role != "none" || st.Connected {
		t.Fatalf("unexpected state after stop: %+v", st)
	}
}

// TestConfigPersists verifies the GET/POST settings endpoint and that
// connecting as a client persists the server address for the next launch.
func TestConfigPersists(t *testing.T) {
	t.Setenv("CROSSSCREEN_CONFIG_DIR", t.TempDir())

	app := New(Config{
		NodeAddr:     "127.0.0.1:0",
		NewCapture:   func() (capture.Capture, error) { return &fakeCapture{}, nil },
		NewInjector:  func() (inject.Injector, error) { return &fakeInjector{}, nil },
		NewClipboard: func() (adapter.Clipboard, error) { return &fakeClipboard{}, nil },
		Settings:     config.Load(),
	})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	defer app.StopNode()

	// GET returns built-in defaults.
	resp, err := http.Get(ts.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	var got SettingsView
	decode(t, resp, &got)
	if got.ListenAddr != "0.0.0.0:53317" || got.UIPort != 8080 {
		t.Fatalf("unexpected defaults: %+v", got)
	}

	// POST validates addresses and persists.
	resp = post(t, ts.URL+"/api/config", `{"device_name":"mac-mini","client_addr":"bad-addr"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for bad address, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	decode(t, post(t, ts.URL+"/api/config", `{"device_name":"mac-mini","listen_addr":"0.0.0.0:53318","client_addr":"10.0.0.9:53317"}`), &got)
	if got.DeviceName != "mac-mini" || got.ClientAddr != "10.0.0.9:53317" || got.ListenAddr != "0.0.0.0:53318" {
		t.Fatalf("settings not applied: %+v", got)
	}

	// A fresh load sees the on-disk values.
	reloaded := config.Load()
	if reloaded.DeviceName != "mac-mini" || reloaded.ClientAddr != "10.0.0.9:53317" || reloaded.ListenAddr != "0.0.0.0:53318" {
		t.Fatalf("settings not persisted: %+v", reloaded)
	}
}
