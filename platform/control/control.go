// Package control exposes a cross-screen node to a browser UI over HTTP:
// settings persistence, node lifecycle controls and screen arrangement
// placement. The static web UI is embedded in the single binary.
package control

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"runtime"
	"sync"

	"crossscreen/core/layout"
	"crossscreen/core/protocol"
	"crossscreen/platform/adapter"
	"crossscreen/platform/autostart"
	"crossscreen/platform/capture"
	"crossscreen/platform/clipboard"
	"crossscreen/platform/config"
	"crossscreen/platform/filexfer"
	"crossscreen/platform/inject"
)

//go:embed web
var webFS embed.FS

const appVersion = "0.4.0"

type nodeKind uint8

const (
	nodeNone nodeKind = iota
	nodeServer
	nodeClient
)

// Config configures the control panel.
type Config struct {
	// NodeAddr is the TCP listen address used when starting a server node
	// (e.g. ":53317").
	NodeAddr string
	// LocalScreens optionally overrides display enumeration (tests).
	LocalScreens []*layout.Screen
	// NewCapture / NewInjector override the platform factories (tests).
	NewCapture  func() (capture.Capture, error)
	NewInjector func() (inject.Injector, error)
	// NewClipboard overrides the platform clipboard factory (tests).
	NewClipboard func() (adapter.Clipboard, error)
	// Settings, when provided, is read on start/connect and updated when the
	// user changes addresses/name so they persist across launches.
	Settings *config.Settings
	// Files is the optional cross-device file copy-paste manager.
	Files *filexfer.Manager
}

// App owns a cross-screen node and exposes it over HTTP.
type App struct {
	cfg Config

	mu     sync.Mutex
	kind   nodeKind
	server *adapter.Server
	client *adapter.Client
	name   string
	addr   net.Addr
	capOK  bool
	gen    uint64 // bumped on every node start/stop to invalidate stale disconnect callbacks
}

// New creates a control panel app.
func New(cfg Config) *App {
	hostname, _ := os.Hostname()
	return &App{cfg: cfg, name: hostname}
}

// Handler returns the HTTP handler for the control panel and API.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", a.handleState)
	mux.HandleFunc("/api/config", a.handleConfig)
	mux.HandleFunc("/api/server/start", a.handleServerStart)
	mux.HandleFunc("/api/client/connect", a.handleClientConnect)
	mux.HandleFunc("/api/stop", a.handleStop)
	mux.HandleFunc("/api/placement", a.handlePlacement)
	mux.HandleFunc("/api/transfer/respond", a.handleTransferRespond)
	mux.HandleFunc("/api/win/service", a.handleWinService)
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return mux
}

// PeerInfo describes a connected peer.
type PeerInfo struct {
	Hostname string           `json:"hostname"`
	OS       string           `json:"os"`
	Role     string           `json:"role"`
	Screens  []*layout.Screen `json:"screens"`
}

// State is a snapshot of the node for the UI.
type State struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Role         string            `json:"role"` // "none" | "server" | "client"
	Connected    bool              `json:"connected"`
	NodeAddr     string            `json:"node_addr"`
	Capture      bool              `json:"capture"`
	Layout       *layout.Layout    `json:"layout"`
	LocalScreens []*layout.Screen  `json:"local_screens"`
	Peers        []PeerInfo        `json:"peers"`
	Active       string            `json:"active"`
	Server       string            `json:"server,omitempty"`
	Transfers    []filexfer.Status `json:"transfers,omitempty"`
	OS           string            `json:"os"`
}

// State returns a snapshot of the current node.
func (a *App) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stateLocked()
}

func (a *App) stateLocked() State {
	st := State{Name: a.name, Version: appVersion, Capture: a.capOK, OS: osName()}
	switch a.kind {
	case nodeServer:
		st.Role = "server"
		st.Connected = true
		if a.addr != nil {
			st.NodeAddr = a.addr.String()
		}
		st.Layout = a.server.Layout()
		st.LocalScreens = a.server.LocalScreens()
		for _, p := range a.server.Session().Peers() {
			if !p.Ready() {
				continue
			}
			st.Peers = append(st.Peers, PeerInfo{
				Hostname: p.Hello().Hostname,
				OS:       p.Hello().OS,
				Role:     p.Hello().Role.String(),
				Screens:  p.Screens(),
			})
		}
		if act := a.server.Session().Router().Active(); act != nil {
			st.Active = string(act.Device)
		}
	case nodeClient:
		st.Role = "client"
		st.Connected = true
		if sh := a.client.Client().ServerHello(); sh != nil {
			st.Server = sh.Hostname
		}
		st.Layout = a.client.Client().Layout()
		st.LocalScreens = a.client.LocalScreens()
	default:
		st.Role = "none"
	}
	if a.cfg.Files != nil {
		st.Transfers = a.cfg.Files.Statuses()
	}
	return st
}

func (a *App) stopLocked() {
	if a.server != nil {
		_ = a.server.Close()
	}
	if a.client != nil {
		_ = a.client.Close()
	}
	a.server, a.client = nil, nil
	a.addr = nil
	a.kind = nodeNone
	a.capOK = false
}

// StartServer begins a session server node for the device named name,
// listening on listenAddr (falling back to the configured default when empty).
func (a *App) StartServer(name, listenAddr string) error {
	if name == "" {
		name = a.name
	}
	if listenAddr == "" {
		listenAddr = a.cfg.NodeAddr
	}
	newCapture := a.cfg.NewCapture
	if newCapture == nil {
		newCapture = capture.New
	}
	newInjector := a.cfg.NewInjector
	if newInjector == nil {
		newInjector = newPlatformInjector
	}
	newClipboard := a.cfg.NewClipboard
	if newClipboard == nil {
		newClipboard = func() (adapter.Clipboard, error) { return clipboard.New() }
	}

	inj, err := newInjector()
	if err != nil {
		return err
	}
	clip, _ := newClipboard()
	var cap capture.Capture
	if cap, err = newCapture(); err != nil {
		// Capture is best-effort: the node can still run headless.
		cap = nil
	}

	a.mu.Lock()
	a.stopLocked()
	a.gen++

	srv, err := adapter.RunServer(adapter.ServerConfig{
		Hello:        &protocol.Hello{Hostname: name, OS: osName(), Role: protocol.RoleServer, Version: appVersion},
		Capture:      cap,
		Injector:     inj,
		Clipboard:    clip,
		Files:        a.cfg.Files,
		LocalScreens: a.cfg.LocalScreens,
	})
	if err != nil {
		a.mu.Unlock()
		return fmt.Errorf("control: start server: %w", err)
	}
	addr, err := srv.Listen(listenAddr)
	if err != nil {
		a.mu.Unlock()
		_ = srv.Close()
		return fmt.Errorf("control: listen: %w", err)
	}
	a.server, a.addr, a.kind = srv, addr, nodeServer
	a.name = name
	a.capOK = cap != nil
	a.mu.Unlock()

	a.persist(func(s *config.Settings) {
		s.DeviceName = name
		s.ListenAddr = listenAddr
	})
	return nil
}

// ConnectClient connects to a session server as a client node.
func (a *App) ConnectClient(serverAddr, name string) error {
	if name == "" {
		name = a.name
	}
	newCapture := a.cfg.NewCapture
	if newCapture == nil {
		newCapture = capture.New
	}
	newInjector := a.cfg.NewInjector
	if newInjector == nil {
		newInjector = newPlatformInjector
	}
	newClipboard := a.cfg.NewClipboard
	if newClipboard == nil {
		newClipboard = func() (adapter.Clipboard, error) { return clipboard.New() }
	}

	inj, err := newInjector()
	if err != nil {
		return err
	}
	clip, _ := newClipboard()
	var cap capture.Capture
	if cap, err = newCapture(); err != nil {
		cap = nil
	}

	a.mu.Lock()
	a.stopLocked()
	a.gen++
	gen := a.gen
	a.mu.Unlock()

	cl, err := adapter.RunClient(serverAddr, adapter.ClientConfig{
		Hello:        &protocol.Hello{Hostname: name, OS: osName(), Role: protocol.RoleClient, Version: appVersion},
		Capture:      cap,
		Injector:     inj,
		Clipboard:    clip,
		Files:        a.cfg.Files,
		LocalScreens: a.cfg.LocalScreens,
		OnDisconnect: func() { a.onClientDisconnected(gen) },
	})
	if err != nil {
		return fmt.Errorf("control: connect: %w", err)
	}
	a.mu.Lock()
	a.client, a.kind = cl, nodeClient
	a.name = name
	a.capOK = cap != nil
	a.mu.Unlock()

	a.persist(func(s *config.Settings) {
		s.DeviceName = name
		s.ClientAddr = serverAddr
	})
	return nil
}

// persist applies fn to the shared settings and writes them to disk.
func (a *App) persist(fn func(s *config.Settings)) {
	if a.cfg.Settings == nil {
		return
	}
	fn(a.cfg.Settings)
	_ = a.cfg.Settings.Save()
}

// onClientDisconnected resets the node state when the client's connection
// ends, so the UI stops showing it as connected and resources are released.
// A stale callback from an earlier generation is ignored.
func (a *App) onClientDisconnected(gen uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if gen != a.gen || a.kind != nodeClient {
		return
	}
	a.client = nil
	a.kind = nodeNone
	a.capOK = false
	a.gen++
}

// StopNode stops the current node.
func (a *App) StopNode() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopLocked()
	a.gen++
}

// SetPlacement applies a manual screen position on the server node.
func (a *App) SetPlacement(id string, x, y int32) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.kind != nodeServer {
		return fmt.Errorf("control: not a server node")
	}
	a.server.SetPlacement(layout.ScreenID(id), x, y)
	return nil
}

// ClearPlacement removes a manual screen position on the server node.
func (a *App) ClearPlacement(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.kind != nodeServer {
		return fmt.Errorf("control: not a server node")
	}
	a.server.ClearPlacement(layout.ScreenID(id))
	return nil
}

// SettingsView is the persisted configuration exchanged with the browser.
// AutoStart is a pointer so an omitted field leaves the login item untouched.
type SettingsView struct {
	DeviceName string `json:"device_name"`
	ListenAddr string `json:"listen_addr"`
	ClientAddr string `json:"client_addr"`
	UIPort     int    `json:"ui_port"`
	AutoStart  *bool  `json:"auto_start,omitempty"`
}

// Settings returns a copy of the persisted settings. AutoStart reflects the
// actual OS login-item state. Without a settings store the panel defaults are
// returned.
func (a *App) Settings() SettingsView {
	enabled := autostart.Enabled()
	v := SettingsView{AutoStart: &enabled}
	if s := a.cfg.Settings; s != nil {
		v.DeviceName = s.DeviceName
		v.ListenAddr = s.ListenAddr
		v.ClientAddr = s.ClientAddr
		v.UIPort = s.UIPort
	} else {
		d := config.Default()
		v.ListenAddr = d.ListenAddr
		v.UIPort = d.UIPort
	}
	a.mu.Lock()
	if v.DeviceName == "" {
		v.DeviceName = a.name
	}
	a.mu.Unlock()
	return v
}

// UpdateSettings validates and persists settings. The auto-start login item
// is created or removed when auto_start differs from the current OS state.
// UIPort changes apply on the next launch.
func (a *App) UpdateSettings(v SettingsView) error {
	if s := a.cfg.Settings; s != nil {
		if v.ListenAddr != "" {
			if _, _, err := net.SplitHostPort(v.ListenAddr); err != nil {
				return fmt.Errorf("control: invalid listen address: %w", err)
			}
		}
		if v.ClientAddr != "" {
			if _, _, err := net.SplitHostPort(v.ClientAddr); err != nil {
				return fmt.Errorf("control: invalid server address: %w", err)
			}
		}
		if v.UIPort != 0 && (v.UIPort < 1 || v.UIPort > 65535) {
			return fmt.Errorf("control: ui port out of range: %d", v.UIPort)
		}
		if v.DeviceName != "" {
			s.DeviceName = v.DeviceName
			a.mu.Lock()
			a.name = v.DeviceName
			a.mu.Unlock()
		}
		if v.ListenAddr != "" {
			s.ListenAddr = v.ListenAddr
		}
		s.ClientAddr = v.ClientAddr
		if v.UIPort != 0 {
			s.UIPort = v.UIPort
		}
		if v.AutoStart != nil {
			switch {
			case *v.AutoStart && !autostart.Enabled():
				if err := autostart.Enable(); err != nil {
					return fmt.Errorf("control: enable auto-start: %w", err)
				}
			case !*v.AutoStart && autostart.Enabled():
				if err := autostart.Disable(); err != nil {
					return fmt.Errorf("control: disable auto-start: %w", err)
				}
			}
			s.AutoStart = autostart.Enabled()
		}
		return s.Save()
	}
	return nil
}

// handleTransferRespond accepts or declines a pending inbound file transfer.
func (a *App) handleTransferRespond(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if a.cfg.Files == nil {
		writeErr(w, http.StatusServiceUnavailable, "file transfer disabled")
		return
	}
	var req struct {
		TID    string `json:"tid"`
		Accept bool   `json:"accept"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.TID == "" {
		writeErr(w, http.StatusBadRequest, "tid required")
		return
	}
	if err := a.cfg.Files.Respond(req.TID, req.Accept); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.State())
}

// --- HTTP handlers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (a *App) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.State())
}

func (a *App) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.Settings())
	case http.MethodPost:
		var v SettingsView
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		if err := a.UpdateSettings(v); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, a.Settings())
	default:
		writeErr(w, http.StatusMethodNotAllowed, "GET or POST required")
	}
}

func (a *App) handleServerStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Name string `json:"name"`
		Addr string `json:"addr"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := a.StartServer(req.Name, req.Addr); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.State())
}

func (a *App) handleClientConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Addr string `json:"addr"`
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Addr == "" {
		writeErr(w, http.StatusBadRequest, "addr required")
		return
	}
	if err := a.ConnectClient(req.Addr, req.Name); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.State())
}

func (a *App) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	a.StopNode()
	writeJSON(w, http.StatusOK, a.State())
}

func (a *App) handlePlacement(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		ID    string `json:"id"`
		X     int32  `json:"x"`
		Y     int32  `json:"y"`
		Reset bool   `json:"reset"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.ID == "" {
		writeErr(w, http.StatusBadRequest, "id required")
		return
	}
	var err error
	if req.Reset {
		err = a.ClearPlacement(req.ID)
	} else {
		err = a.SetPlacement(req.ID, req.X, req.Y)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.State())
}

func osName() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	default:
		return runtime.GOOS
	}
}
