package adapter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crossscreen/core/event"
	"crossscreen/core/keymap"
	"crossscreen/core/layout"
	"crossscreen/core/protocol"
	"crossscreen/platform/capture"
	"crossscreen/platform/clipboard"
	"crossscreen/platform/filexfer"
)

// fakeCapture stores the handler for tests to drive and returns a fixed
// starting pointer position.
type fakeCapture struct {
	h       capture.Handler
	started bool
}

func (f *fakeCapture) Start(h capture.Handler) error {
	f.h = h
	f.started = true
	return nil
}
func (f *fakeCapture) Stop()                           {}
func (f *fakeCapture) Position() (int32, int32, error) { return 100, 100, nil }
func (f *fakeCapture) SetRelativeMode(on bool)         {}
func (f *fakeCapture) PinCursor(x, y int32)            {}

func (f *fakeCapture) move(dx, dy int32) { f.h.OnMove(0, 0, dx, dy) }
func (f *fakeCapture) button(b event.MouseButton, down bool) {
	f.h.OnButton(event.PointerButton{Button: b, Down: down})
}
func (f *fakeCapture) key(k keymap.Key, down bool) { f.h.OnKey(k, down) }

type fakeInjector struct {
	mu      sync.Mutex
	moves   []struct{ x, y int32 }
	buttons []event.PointerButton
	keys    []struct {
		k    keymap.Key
		down bool
	}
}

func (f *fakeInjector) MovePointer(x, y int32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.moves = append(f.moves, struct{ x, y int32 }{x, y})
	return nil
}
func (f *fakeInjector) Button(b event.MouseButton, down bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.buttons = append(f.buttons, event.PointerButton{Button: b, Down: down})
	return nil
}
func (f *fakeInjector) Scroll(dx, dy int32) error { return nil }
func (f *fakeInjector) Key(k keymap.Key, down bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, struct {
		k    keymap.Key
		down bool
	}{k, down})
	return nil
}
func (f *fakeInjector) lastMove() (int32, int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.moves[len(f.moves)-1]
	return m.x, m.y
}
func (f *fakeInjector) moveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.moves)
}
func (f *fakeInjector) buttonCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.buttons)
}
func (f *fakeInjector) lastButton() event.PointerButton {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buttons[len(f.buttons)-1]
}
func (f *fakeInjector) keyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.keys)
}
func (f *fakeInjector) lastKey() (keymap.Key, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := f.keys[len(f.keys)-1]
	return k.k, k.down
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

// fakeClipboard records applied writes and lets the test simulate a local
// clipboard change.
type fakeClipboard struct {
	mu      sync.Mutex
	h       func(clipboard.Data)
	started bool
	written []clipboard.Data
}

func (f *fakeClipboard) Watch(ctx context.Context, h func(clipboard.Data)) {
	f.mu.Lock()
	f.h = h
	f.started = true
	f.mu.Unlock()
	<-ctx.Done()
}

func (f *fakeClipboard) Write(d clipboard.Data) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.written = append(f.written, d)
	return nil
}

func (f *fakeClipboard) change(s string) {
	f.mu.Lock()
	h := f.h
	f.mu.Unlock()
	if h != nil {
		h(clipboard.Text(s))
	}
}

// emit simulates a local clipboard change with an arbitrary payload.
func (f *fakeClipboard) emit(d clipboard.Data) {
	f.mu.Lock()
	h := f.h
	f.mu.Unlock()
	if h != nil {
		h(d)
	}
}

// WriteFiles records a file-reference clipboard write (filexfer receiver).
func (f *fakeClipboard) WriteFiles(lf clipboard.LocalFiles) error {
	b, _ := json.Marshal(lf)
	return f.Write(clipboard.Data{Mime: clipboard.MimeFiles, Data: b})
}

func (f *fakeClipboard) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.written)
}

func (f *fakeClipboard) lastWritten() clipboard.Data {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.written[len(f.written)-1]
}

func (f *fakeClipboard) startedOK() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started
}

func macScreen() *layout.Screen {
	return &layout.Screen{ID: "mac:0", Device: "mac", X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true}
}
func winScreen() *layout.Screen {
	return &layout.Screen{ID: "win:0", Device: "win", X: 0, Y: 0, Width: 1280, Height: 800}
}

// TestServerRoutesToClientViaDelta drives the full loop: the server's mouse
// reaches the edge of its local screen and pushes outward, the shared cursor
// crosses onto the client's screen, the client injects at its own global
// coordinates, and pulling back returns control to the server.
func TestServerRoutesToClientViaDelta(t *testing.T) {
	srvCap := &fakeCapture{}
	srvInj := &fakeInjector{}
	d, err := RunServer(ServerConfig{
		Hello:        &protocol.Hello{Hostname: "mac", OS: "macos", Role: protocol.RoleServer, Version: "t"},
		Capture:      srvCap,
		Injector:     srvInj,
		LocalScreens: []*layout.Screen{macScreen()},
	})
	if err != nil {
		t.Fatalf("RunServer: %v", err)
	}
	addr, err := d.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer d.Close()

	winInj := &fakeInjector{}
	cl, err := RunClient(addr.String(), ClientConfig{
		Hello:        &protocol.Hello{Hostname: "win", OS: "windows", Role: protocol.RoleClient, Version: "t"},
		Injector:     winInj,
		LocalScreens: []*layout.Screen{winScreen()},
	})
	if err != nil {
		t.Fatalf("RunClient: %v", err)
	}
	defer cl.Close()

	// The client must have received the authoritative layout (mac + win) so
	// its injection conversion is correct.
	waitFor(t, 2*time.Second, func() bool {
		l := cl.Client().Layout()
		return l != nil && len(l.Screens) == 2
	})

	// The cursor sits at the local right edge and pushes outward → crosses.
	srvCap.h.OnMove(1919, 400, 2, 0)
	waitFor(t, 2*time.Second, func() bool { return winInj.moveCount() > 0 })

	// Client injected at its own global coords: virtual (1921,400) minus the
	// placement offset (1920,0) → (1,400).
	gx, gy := winInj.lastMove()
	if gx != 1 || gy != 400 {
		t.Fatalf("client injected move at (%d,%d), want (1,400)", gx, gy)
	}

	// Buttons while the cursor is on the client must reach the client.
	srvCap.button(event.ButtonLeft, true)
	waitFor(t, 2*time.Second, func() bool { return winInj.buttonCount() > 0 })
	if b := winInj.lastButton(); b.Button != event.ButtonLeft || !b.Down {
		t.Fatalf("client button mismatch: %+v", b)
	}

	// Keys while on the client must reach the client.
	srvCap.key(keymap.KeyA, true)
	waitFor(t, 2*time.Second, func() bool { return winInj.keyCount() > 0 })
	if k, down := winInj.lastKey(); k != keymap.KeyA || !down {
		t.Fatalf("client key mismatch: k=%v down=%v", k, down)
	}

	// Pull back: the shared cursor returns to the local screen and the server
	// resyncs its physical cursor to the shared position.
	srvCap.h.OnMove(1919, 400, -2, 0)
	waitFor(t, 2*time.Second, func() bool {
		sx, sy := srvInj.lastMove()
		return sx == 1919 && sy == 400
	})
}

// TestClipboardSync verifies bidirectional clipboard sharing: a copy on the
// server reaches the client and vice versa.
func TestClipboardSync(t *testing.T) {
	srvCap := &fakeCapture{}
	srvClip := &fakeClipboard{}
	d, err := RunServer(ServerConfig{
		Hello:        &protocol.Hello{Hostname: "mac", OS: "macos", Role: protocol.RoleServer, Version: "t"},
		Capture:      srvCap,
		Injector:     &fakeInjector{},
		Clipboard:    srvClip,
		LocalScreens: []*layout.Screen{macScreen()},
	})
	if err != nil {
		t.Fatalf("RunServer: %v", err)
	}
	addr, err := d.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer d.Close()

	winClip := &fakeClipboard{}
	cl, err := RunClient(addr.String(), ClientConfig{
		Hello:        &protocol.Hello{Hostname: "win", OS: "windows", Role: protocol.RoleClient, Version: "t"},
		Injector:     &fakeInjector{},
		Clipboard:    winClip,
		LocalScreens: []*layout.Screen{winScreen()},
	})
	if err != nil {
		t.Fatalf("RunClient: %v", err)
	}
	defer cl.Close()

	waitFor(t, 2*time.Second, func() bool { return srvClip.startedOK() && winClip.startedOK() })

	// Server copies → client applies.
	srvClip.change("hello from mac")
	waitFor(t, 2*time.Second, func() bool { return winClip.writeCount() > 0 })
	if got := winClip.lastWritten(); string(got.Data) != "hello from mac" {
		t.Fatalf("client clipboard: %q", got.Data)
	}

	// Client copies → server applies.
	winClip.change("hello from win")
	waitFor(t, 2*time.Second, func() bool { return srvClip.writeCount() > 0 })
	if got := srvClip.lastWritten(); string(got.Data) != "hello from win" {
		t.Fatalf("server clipboard: %q", got.Data)
	}
}

// TestFileCopyPaste runs the full file feature over a real TCP session:
// a copied file on the server is offered, accepted, streamed in chunks and
// materialized in the client's staging directory, then applied to its
// clipboard as file references.
func TestFileCopyPaste(t *testing.T) {
	// Source file (large enough to span several chunks).
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "report.txt")
	content := strings.Repeat("CrossScreen-", 40000) // ~480 KiB
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	srcFI, _ := os.Stat(src)

	srvMgr := filexfer.New(filexfer.Options{
		Self: "mac", StagingDir: filepath.Join(t.TempDir(), "a"), AutoMax: 1 << 30,
	})
	stagingB := filepath.Join(t.TempDir(), "b")
	cliMgr := filexfer.New(filexfer.Options{
		Self: "win", StagingDir: stagingB, AutoMax: 1 << 30,
	})

	srvClip := &fakeClipboard{}
	d, err := RunServer(ServerConfig{
		Hello:        &protocol.Hello{Hostname: "mac", OS: "macos", Role: protocol.RoleServer, Version: "t"},
		Injector:     &fakeInjector{},
		Clipboard:    srvClip,
		Files:        srvMgr,
		LocalScreens: []*layout.Screen{macScreen()},
	})
	if err != nil {
		t.Fatalf("RunServer: %v", err)
	}
	addr, err := d.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer d.Close()

	winClip := &fakeClipboard{}
	cl, err := RunClient(addr.String(), ClientConfig{
		Hello:        &protocol.Hello{Hostname: "win", OS: "windows", Role: protocol.RoleClient, Version: "t"},
		Injector:     &fakeInjector{},
		Clipboard:    winClip,
		Files:        cliMgr,
		LocalScreens: []*layout.Screen{winScreen()},
	})
	if err != nil {
		t.Fatalf("RunClient: %v", err)
	}
	defer cl.Close()
	waitFor(t, 2*time.Second, func() bool { return srvClip.startedOK() && winClip.startedOK() })

	// Simulate Cmd+C on the server.
	local := clipboard.LocalFiles{Files: []clipboard.LocalFile{{
		Name: "report.txt", Size: srcFI.Size(), Path: src,
	}}}
	lb, _ := json.Marshal(local)
	srvClip.emit(clipboard.Data{Mime: clipboard.MimeFiles, Data: lb})

	// Wait until the receiver applies file references to its clipboard.
	var applied clipboard.Data
	waitFor(t, 3*time.Second, func() bool {
		for _, w := range winClip.written {
			if w.Mime == clipboard.MimeFiles {
				applied = w
				return true
			}
		}
		return false
	})

	var lf clipboard.LocalFiles
	if err := json.Unmarshal(applied.Data, &lf); err != nil {
		t.Fatal(err)
	}
	if len(lf.Files) != 1 || lf.Files[0].Name != "report.txt" {
		t.Fatalf("unexpected applied files: %+v", lf.Files)
	}
	got, err := os.ReadFile(lf.Files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("transferred content mismatch (len %d vs %d)", len(got), len(content))
	}
	rel, err := filepath.Rel(stagingB, lf.Files[0].Path)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("file landed outside staging dir: %s", lf.Files[0].Path)
	}
}

// TestPeerDisconnectReleasesRelativeMode verifies that when the client
// vanishes while the shared cursor is on its screen, the server leaves
// relative mode (un-suppresses input) and restores the cursor locally.
func TestPeerDisconnectReleasesRelativeMode(t *testing.T) {
	srvCap := &fakeCapture{}
	srvInj := &fakeInjector{}
	d, err := RunServer(ServerConfig{
		Hello:        &protocol.Hello{Hostname: "mac", OS: "macos", Role: protocol.RoleServer, Version: "t"},
		Capture:      srvCap,
		Injector:     srvInj,
		LocalScreens: []*layout.Screen{macScreen()},
	})
	if err != nil {
		t.Fatal(err)
	}
	addr, err := d.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	cl, err := RunClient(addr.String(), ClientConfig{
		Hello:        &protocol.Hello{Hostname: "win", OS: "windows", Role: protocol.RoleClient, Version: "t"},
		Injector:     &fakeInjector{},
		LocalScreens: []*layout.Screen{winScreen()},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		l := cl.Client().Layout()
		return l != nil && len(l.Screens) == 2
	})

	// Cross onto the client.
	srvCap.h.OnMove(1919, 400, 2, 0)
	waitFor(t, 2*time.Second, d.isRelative)

	// Client disconnects (network drop / closed app).
	_ = cl.Close()
	waitFor(t, 2*time.Second, func() bool { return !d.isRelative() })
	if d.srv.Router().Active() == nil {
		t.Fatal("router should have a local active screen after release")
	}
	if act := d.srv.Router().Active(); act.Device != "mac" {
		t.Fatalf("active device after release: %s, want mac", act.Device)
	}
}

// TestClientPassiveDoesNotCapture verifies a RoleClient does not forward its
// own capture (it is a passive display target).
func TestClientPassiveDoesNotCapture(t *testing.T) {
	d, err := RunServer(ServerConfig{
		Hello:        &protocol.Hello{Hostname: "mac", OS: "macos", Role: protocol.RoleServer, Version: "t"},
		Injector:     &fakeInjector{},
		LocalScreens: []*layout.Screen{macScreen()},
	})
	if err != nil {
		t.Fatalf("RunServer: %v", err)
	}
	addr, err := d.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer d.Close()

	winCap := &fakeCapture{}
	cl, err := RunClient(addr.String(), ClientConfig{
		Hello:        &protocol.Hello{Hostname: "win", OS: "windows", Role: protocol.RoleClient, Version: "t"},
		Capture:      winCap,
		Injector:     &fakeInjector{},
		LocalScreens: []*layout.Screen{winScreen()},
	})
	if err != nil {
		t.Fatalf("RunClient: %v", err)
	}
	defer cl.Close()

	if winCap.started {
		t.Fatal("RoleClient must not start input capture")
	}
}
