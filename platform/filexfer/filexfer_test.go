package filexfer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crossscreen/core/protocol"
	"crossscreen/platform/clipboard"
)

type recorder struct {
	mu    chan struct{}
	calls []clipboard.LocalFiles
}

func newRecorder() *recorder { return &recorder{mu: make(chan struct{}, 8)} }

func (r *recorder) write(lf clipboard.LocalFiles) error {
	r.calls = append(r.calls, lf)
	select {
	case r.mu <- struct{}{}:
	default:
	}
	return nil
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timeout waiting for " + msg)
}

func writeSrc(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func localJSON(t *testing.T, paths ...string) []byte {
	t.Helper()
	var files []clipboard.LocalFile
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, clipboard.LocalFile{Name: filepath.Base(p), Size: fi.Size(), Path: p})
	}
	b, err := json.Marshal(clipboard.LocalFiles{Files: files})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func findStatus(ss []Status, dir Direction, state State) *Status {
	for i := range ss {
		if ss[i].Direction == dir && ss[i].State == state {
			return &ss[i]
		}
	}
	return nil
}

func TestSmallTransferAutoAccepts(t *testing.T) {
	src1 := writeSrc(t, "hello.txt", "hello cross-screen")
	src2 := writeSrc(t, "big.bin", string(make([]byte, 300000))) // forces chunking

	a := New(Options{Self: "A", StagingDir: t.TempDir(), AutoMax: 50 << 20})
	stagingB := t.TempDir()
	b := New(Options{Self: "B", StagingDir: stagingB, AutoMax: 50 << 20})
	rb := newRecorder()
	a.Attach(nil, func(_ string, env *protocol.Envelope) error { return b.Handle(env) })
	b.Attach(rb.write, func(_ string, env *protocol.Envelope) error { return a.Handle(env) })
	t.Cleanup(func() { a.Detach(); b.Detach() })

	wire, err := a.PrepareOutbound(localJSON(t, src1, src2))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.HandleOffer(wire); err != nil {
		t.Fatal(err)
	}

	select {
	case <-rb.mu:
	case <-time.After(3 * time.Second):
		t.Fatal("receiver never updated its clipboard")
	}
	if len(rb.calls) != 1 || len(rb.calls[0].Files) != 2 {
		t.Fatalf("want 2 files in clipboard, got %+v", rb.calls)
	}

	got1, err := os.ReadFile(rb.calls[0].Files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got1) != "hello cross-screen" {
		t.Fatalf("file1 content mismatch: %q", got1)
	}
	got2, err := os.ReadFile(rb.calls[0].Files[1].Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 300000 {
		t.Fatalf("file2 size: %d", len(got2))
	}
	if st := findStatus(a.Statuses(), DirOut, StateDone); st == nil {
		t.Fatalf("sender transfer not done: %+v", a.Statuses())
	}
	if st := findStatus(b.Statuses(), DirIn, StateDone); st == nil {
		t.Fatalf("receiver transfer not done: %+v", b.Statuses())
	}
}

func TestLargeTransferRequiresAccept(t *testing.T) {
	content := make([]byte, 4096)
	for i := range content {
		content[i] = byte(i)
	}
	src := writeSrc(t, "movie.mkv", string(content))

	a := New(Options{Self: "A", StagingDir: t.TempDir(), AutoMax: 1 << 30})
	rb := newRecorder()
	b := New(Options{Self: "B", StagingDir: t.TempDir(), AutoMax: 1024}) // tiny threshold
	a.Attach(nil, func(_ string, env *protocol.Envelope) error { return b.Handle(env) })
	b.Attach(rb.write, func(_ string, env *protocol.Envelope) error { return a.Handle(env) })
	t.Cleanup(func() { a.Detach(); b.Detach() })

	wire, err := a.PrepareOutbound(localJSON(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.HandleOffer(wire); err != nil {
		t.Fatal(err)
	}
	pending := findStatus(b.Statuses(), DirIn, StatePending)
	if pending == nil {
		t.Fatalf("want pending transfer: %+v", b.Statuses())
	}

	if err := b.Respond(pending.ID, true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-rb.mu:
	case <-time.After(3 * time.Second):
		t.Fatal("accepted transfer never completed")
	}
	got, err := os.ReadFile(rb.calls[0].Files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(content) || string(got) != string(content) {
		t.Fatal("received content mismatch")
	}
}

func TestDeclineCancelsTransfer(t *testing.T) {
	src := writeSrc(t, "x.zip", "0123456789")
	a := New(Options{Self: "A", StagingDir: t.TempDir(), AutoMax: 1 << 30})
	b := New(Options{Self: "B", StagingDir: t.TempDir(), AutoMax: 1})
	a.Attach(nil, func(_ string, env *protocol.Envelope) error { return b.Handle(env) })
	b.Attach(nil, func(_ string, env *protocol.Envelope) error { return a.Handle(env) })
	t.Cleanup(func() { a.Detach(); b.Detach() })

	wire, _ := a.PrepareOutbound(localJSON(t, src))
	if err := b.HandleOffer(wire); err != nil {
		t.Fatal(err)
	}
	pending := findStatus(b.Statuses(), DirIn, StatePending)
	if pending == nil {
		t.Fatal("want pending")
	}
	if err := b.Respond(pending.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		return findStatus(b.Statuses(), DirIn, StateDeclined) != nil
	}, "receiver declined")
	waitFor(t, func() bool {
		return findStatus(a.Statuses(), DirOut, StateDeclined) != nil
	}, "sender sees decline")
}

func TestUnsafeOfferRejected(t *testing.T) {
	b := New(Options{Self: "B", StagingDir: t.TempDir(), AutoMax: 1 << 30})
	b.Attach(nil, func(_ string, _ *protocol.Envelope) error { return nil })
	t.Cleanup(b.Detach)

	for _, bad := range []string{"../../etc/passwd", "..", "a/b.txt", "a\\b.txt", "~$lock.docx"} {
		man := Manifest{TID: "t-" + bad, From: "A", Files: []protocol.FileMeta{{Name: bad, Size: 1}}}
		raw, _ := json.Marshal(man)
		if err := b.HandleOffer(raw); err == nil {
			t.Fatalf("expected rejection for %q", bad)
		}
	}
}

func TestOversizedChunkFails(t *testing.T) {
	staging := t.TempDir()
	b := New(Options{Self: "B", StagingDir: staging, AutoMax: 1 << 30})
	b.Attach(nil, func(_ string, _ *protocol.Envelope) error { return nil })
	t.Cleanup(b.Detach)

	man := Manifest{TID: "t1", From: "A", Files: []protocol.FileMeta{{Name: "f.txt", Size: 5}}}
	raw, _ := json.Marshal(man)
	if err := b.HandleOffer(raw); err != nil {
		t.Fatal(err) // auto-accepted (large AutoMax)
	}
	env, err := protocol.NewEnvelope(protocol.TypeFileChunk, 1, protocol.FileChunk{
		TID: "t1", From: "A", To: "B", Idx: 0, Off: 0, Data: []byte("way too much data!"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Handle(env); err != nil {
		t.Fatal(err)
	}
	// Chunk processing is now asynchronous (dedicated write loop), so the
	// failure surfaces on the next poll.
	waitFor(t, func() bool {
		return findStatus(b.Statuses(), DirIn, StateFailed) != nil
	}, "oversized chunk to fail")
	if st := findStatus(b.Statuses(), DirIn, StateFailed); st == nil {
		t.Fatalf("transfer should be failed: %+v", b.Statuses())
	}
}

func TestSanitizeAndUnique(t *testing.T) {
	if _, ok := sanitizeName("normal.txt"); !ok {
		t.Fatal("normal name rejected")
	}
	used := map[string]bool{}
	n1 := uniqueName("a.txt", used)
	n2 := uniqueName("a.txt", used)
	if n1 == n2 {
		t.Fatalf("duplicate name not made unique: %s", n1)
	}
	if n2 != "a (2).txt" {
		t.Fatalf("unexpected unique name: %s", n2)
	}
}
