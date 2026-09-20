// Package filexfer streams files that a peer copied (file references in the
// system clipboard) over the existing session connection.
//
// Flow:
//
//  1. The sender's clipboard watcher reads local file paths; PrepareOutbound
//     records them under a transfer ID and returns a wire manifest that is
//     broadcast as a TypeClipboard "application/x-crossscreen-files" event.
//  2. Each receiver validates the manifest: small transfers auto-accept,
//     large ones stay pending until the user confirms in the web panel.
//  3. Acceptance opens a per-peer ordered chunk stream (256 KiB blocks over
//     TypeFileChunk); chunks are written into a sandboxed staging directory.
//  4. On the final chunk the receiver points its system clipboard at the
//     staged files, so Cmd/Ctrl+V pastes real files in Finder/Explorer.
package filexfer

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"crossscreen/core/protocol"
	"crossscreen/platform/clipboard"
)

const (
	// ChunkSize keeps each frame well under the 4 MiB protocol cap.
	ChunkSize = 256 << 10
	// AutoAcceptMax: transfers at or below this size start without asking.
	AutoAcceptMax = 50 << 20
	// MaxFiles bounds a single copy operation.
	MaxFiles = 64
)

// State is the lifecycle state of one transfer.
type State string

const (
	StatePending   State = "pending"   // inbound, awaiting user decision
	StateSending   State = "sending"   // outbound, streaming to a peer
	StateReceiving State = "receiving" // inbound, streaming to disk
	StateDone      State = "done"
	StateDeclined  State = "declined"
	StateFailed    State = "failed"
	StateCanceled  State = "canceled"
)

// Direction is "in" (received) or "out" (offered locally).
type Direction string

const (
	DirIn  Direction = "in"
	DirOut Direction = "out"
)

// FileStatus is one file's progress inside a transfer.
type FileStatus struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Done int64  `json:"done"`
}

// Status is a UI-facing transfer snapshot.
type Status struct {
	ID        string       `json:"id"`
	Direction Direction    `json:"direction"`
	Peer      string       `json:"peer"`
	State     State        `json:"state"`
	Auto      bool         `json:"auto"`
	Files     []FileStatus `json:"files"`
	Total     int64        `json:"total"`
	Done      int64        `json:"done"`
	Error     string       `json:"error,omitempty"`
}

// Manifest is the wire offer carried inside a MimeFiles clipboard event.
type Manifest struct {
	TID   string              `json:"tid"`
	From  string              `json:"from"`
	Files []protocol.FileMeta `json:"files"`
}

// Options configures a Manager.
type Options struct {
	Self       string
	StagingDir string
	// AutoMax defaults to AutoAcceptMax when zero.
	AutoMax int64
	// Open opens source files for reading (defaults to os.Open).
	Open func(path string) (io.ReadCloser, error)
	// WriteClipboard points the active system clipboard at received files.
	WriteClipboard func(clipboard.LocalFiles) error
}

type sourceFile struct {
	protocol.FileMeta
	Path string
}

type outPeer struct {
	mu      sync.Mutex
	state   State
	done    int64
	err     string
	started bool
	cancel  chan struct{}
}

type outTransfer struct {
	id      string
	sources []sourceFile
	total   int64
	order   []string // peer device names in arrival order
	peers   map[string]*outPeer
}

type inTransfer struct {
	id        string
	from      string
	files     []protocol.FileMeta
	safeNames []string
	dir       string
	state     State
	auto      bool
	total     int64

	writeMu sync.Mutex
	done    int64
	curIdx  int
	curDone int64
	curFile *os.File
	errMsg  string
}

// Manager owns all outbound offers and inbound transfers for one node.
type Manager struct {
	self    string
	staging string
	autoMax int64
	open    func(path string) (io.ReadCloser, error)
	writeCb func(clipboard.LocalFiles) error

	sendMu sync.RWMutex
	send   func(to string, env *protocol.Envelope) error

	mu       sync.Mutex
	out      map[string]*outTransfer
	in       map[string]*inTransfer
	outOrder []string
	inOrder  []string

	// Inbound chunks are processed on a dedicated write loop so disk I/O
	// never blocks the session read thread that also delivers input events
	// (a slow write would otherwise stall mouse/keyboard on the same
	// connection).
	recvCh   chan recvItem
	recvStop chan struct{}
}

// recvItem carries one validated inbound chunk to the write loop.
type recvItem struct {
	t  *inTransfer
	ev *protocol.FileChunk
}

// New creates a transfer Manager.
func New(opts Options) *Manager {
	open := opts.Open
	if open == nil {
		open = func(path string) (io.ReadCloser, error) { return os.Open(path) }
	}
	autoMax := opts.AutoMax
	if autoMax <= 0 {
		autoMax = AutoAcceptMax
	}
	m := &Manager{
		self:     opts.Self,
		staging:  opts.StagingDir,
		autoMax:  autoMax,
		open:     open,
		writeCb:  opts.WriteClipboard,
		out:      map[string]*outTransfer{},
		in:       map[string]*inTransfer{},
		recvCh:   make(chan recvItem, 256),
		recvStop: make(chan struct{}),
	}
	go m.writeLoop()
	return m
}

// writeLoop drains validated chunks in FIFO order, doing the actual disk
// writes, state transitions and completion handling off the network thread.
func (m *Manager) writeLoop() {
	for {
		select {
		case <-m.recvStop:
			return
		case item := <-m.recvCh:
			_ = m.processChunk(item.t, item.ev)
		}
	}
}

// Attach binds the manager to a running node: writer points the clipboard at
// finished files, send delivers an envelope to a peer device (the session
// layer may relay it through the server).
func (m *Manager) Attach(writer func(clipboard.LocalFiles) error, send func(to string, env *protocol.Envelope) error) {
	m.sendMu.Lock()
	m.writeCb = writer
	m.send = send
	m.sendMu.Unlock()
}

// Detach is called when the node stops: all active streams are canceled and
// the transfer list is cleared. Staged files on disk are left untouched (the
// clipboard may still reference a previous transfer).
func (m *Manager) Detach() {
	m.sendMu.Lock()
	m.send = nil
	m.sendMu.Unlock()

	// The write loop stays resident for the lifetime of the manager (it is a
	// long-lived singleton in the panel process). Queued chunks are still
	// drained; sendControl simply no-ops once send is nil.

	m.mu.Lock()
	for _, t := range m.out {
		for _, p := range t.peers {
			p.mu.Lock()
			if p.cancel != nil {
				close(p.cancel)
			}
			if p.state == StateSending {
				p.state = StateCanceled
			}
			p.mu.Unlock()
		}
	}
	for _, t := range m.in {
		t.writeMu.Lock()
		if t.curFile != nil {
			_ = t.curFile.Close()
			t.curFile = nil
		}
		if t.state == StateReceiving {
			t.state = StateCanceled
		}
		t.writeMu.Unlock()
	}
	m.out = map[string]*outTransfer{}
	m.in = map[string]*inTransfer{}
	m.outOrder, m.inOrder = nil, nil
	m.mu.Unlock()
}

func (m *Manager) emit(to string, env *protocol.Envelope) error {
	m.sendMu.RLock()
	send := m.send
	m.sendMu.RUnlock()
	if send == nil {
		return errors.New("filexfer: node not running")
	}
	return send(to, env)
}

// PrepareOutbound converts a local clipboard file payload into a wire offer
// and registers the local sources for later streaming.
func (m *Manager) PrepareOutbound(localJSON []byte) ([]byte, error) {
	var lf clipboard.LocalFiles
	if err := json.Unmarshal(localJSON, &lf); err != nil {
		return nil, fmt.Errorf("filexfer: decode local files: %w", err)
	}
	if len(lf.Files) == 0 {
		return nil, errors.New("filexfer: no files copied")
	}
	if len(lf.Files) > MaxFiles {
		return nil, fmt.Errorf("filexfer: too many files (%d, max %d)", len(lf.Files), MaxFiles)
	}

	sources := make([]sourceFile, 0, len(lf.Files))
	var total int64
	seen := map[string]bool{}
	for _, f := range lf.Files {
		if seen[f.Path] {
			continue
		}
		seen[f.Path] = true
		fi, err := os.Stat(f.Path)
		if err != nil || fi.IsDir() || !fi.Mode().IsRegular() {
			continue // missing files, directories and special files are skipped
		}
		name := f.Name
		if name == "" {
			name = filepath.Base(f.Path)
		}
		sources = append(sources, sourceFile{
			FileMeta: protocol.FileMeta{Name: name, Size: fi.Size()},
			Path:     f.Path,
		})
		total += fi.Size()
	}
	if len(sources) == 0 {
		return nil, errors.New("filexfer: no shareable regular files")
	}

	tid := newTransferID()
	t := &outTransfer{
		id:      tid,
		sources: sources,
		total:   total,
		peers:   map[string]*outPeer{},
	}
	m.mu.Lock()
	m.out[tid] = t
	m.outOrder = append(m.outOrder, tid)
	m.trimOrder(&m.outOrder, 32)
	m.mu.Unlock()

	return json.Marshal(Manifest{TID: tid, From: m.self, Files: metasOf(sources)})
}

// HandleOffer processes a wire offer arriving via the clipboard channel.
func (m *Manager) HandleOffer(wireJSON []byte) error {
	var man Manifest
	if err := json.Unmarshal(wireJSON, &man); err != nil {
		return fmt.Errorf("filexfer: decode offer: %w", err)
	}
	if man.TID == "" || man.From == "" || man.From == m.self {
		return nil
	}
	if len(man.Files) == 0 || len(man.Files) > MaxFiles {
		return fmt.Errorf("filexfer: offer has invalid file count %d", len(man.Files))
	}
	safe := make([]string, len(man.Files))
	used := map[string]bool{}
	var total int64
	for i, f := range man.Files {
		name, ok := sanitizeName(f.Name)
		if !ok || f.Size < 0 {
			return fmt.Errorf("filexfer: unsafe file name %q", f.Name)
		}
		name = uniqueName(name, used)
		safe[i] = name
		total += f.Size
	}

	t := &inTransfer{
		id:        man.TID,
		from:      man.From,
		files:     man.Files,
		safeNames: safe,
		dir:       filepath.Join(m.staging, man.TID),
		total:     total,
	}
	auto := total <= m.autoMax
	if auto {
		t.state = StateReceiving
		t.auto = true
	} else {
		t.state = StatePending
	}

	m.mu.Lock()
	if _, exists := m.in[t.id]; exists {
		m.mu.Unlock()
		return nil // duplicate offer broadcast
	}
	m.in[t.id] = t
	m.inOrder = append(m.inOrder, t.id)
	m.trimOrder(&m.inOrder, 32)
	m.mu.Unlock()

	if auto {
		return m.sendControl(t.from, protocol.TypeFileAccept, protocol.FileAccept{
			TID: t.id, From: m.self, To: t.from,
		})
	}
	return nil
}

// Respond accepts or declines a pending inbound transfer from the web panel.
func (m *Manager) Respond(tid string, accept bool) error {
	m.mu.Lock()
	t := m.in[tid]
	m.mu.Unlock()
	if t == nil {
		return fmt.Errorf("filexfer: unknown transfer %s", tid)
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if t.state != StatePending {
		return fmt.Errorf("filexfer: transfer %s is %s", tid, t.state)
	}
	if accept {
		if err := os.MkdirAll(t.dir, 0o755); err != nil {
			t.state = StateFailed
			t.errMsg = err.Error()
			return err
		}
		t.state = StateReceiving
		return m.sendControl(t.from, protocol.TypeFileAccept, protocol.FileAccept{
			TID: t.id, From: m.self, To: t.from,
		})
	}
	t.state = StateDeclined
	return m.sendControl(t.from, protocol.TypeFileCancel, protocol.FileCancel{
		TID: t.id, From: m.self, To: t.from, Reason: "declined",
	})
}

// Handle dispatches a file control message addressed to this node.
func (m *Manager) Handle(env *protocol.Envelope) error {
	switch env.Type {
	case protocol.TypeFileAccept:
		var ev protocol.FileAccept
		if err := env.Decode(&ev); err != nil {
			return err
		}
		if ev.To != m.self {
			return nil
		}
		return m.onAccept(ev)
	case protocol.TypeFileChunk:
		var ev protocol.FileChunk
		if err := env.Decode(&ev); err != nil {
			return err
		}
		if ev.To != m.self {
			return nil
		}
		// Hand off to the write loop immediately: this runs on the session
		// read thread that also delivers input events, and file writes must
		// never block them.
		m.mu.Lock()
		t := m.in[ev.TID]
		m.mu.Unlock()
		if t == nil || ev.From != t.from {
			return nil
		}
		select {
		case m.recvCh <- recvItem{t: t, ev: &ev}:
		case <-m.recvStop:
		}
		return nil
	case protocol.TypeFileCancel:
		var ev protocol.FileCancel
		if err := env.Decode(&ev); err != nil {
			return err
		}
		if ev.To != m.self {
			return nil
		}
		m.onCanceled(ev.TID, ev.From, ev.Reason, false)
		return nil
	case protocol.TypeFileDone:
		var ev protocol.FileDone
		if err := env.Decode(&ev); err != nil {
			return err
		}
		if ev.To != m.self {
			return nil
		}
		m.markOutPeer(ev.TID, ev.From, StateDone, "")
		return nil
	}
	return nil
}

func (m *Manager) onAccept(ev protocol.FileAccept) error {
	m.mu.Lock()
	t := m.out[ev.TID]
	if t == nil {
		m.mu.Unlock()
		// Unknown/stale offer: tell the peer to stop.
		return m.sendControl(ev.From, protocol.TypeFileCancel, protocol.FileCancel{
			TID: ev.TID, From: m.self, To: ev.From, Reason: "unknown transfer",
		})
	}
	if _, seen := t.peers[ev.From]; !seen {
		t.order = append(t.order, ev.From)
		t.peers[ev.From] = &outPeer{state: StateSending, cancel: make(chan struct{})}
	}
	p := t.peers[ev.From]
	m.mu.Unlock()

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return nil
	}
	p.started = true
	go m.stream(t, ev.From, p)
	return nil
}

func (m *Manager) onCanceled(tid, from, reason string, inbound bool) {
	m.mu.Lock()
	if t, ok := m.out[tid]; ok {
		p, seen := t.peers[from]
		if !seen {
			// Cancel/decline arrived before Accept: record the peer so the
			// outcome is visible in the status list.
			st := StateCanceled
			if reason == "declined" {
				st = StateDeclined
			}
			p = &outPeer{state: st, cancel: make(chan struct{}), err: reason}
			t.order = append(t.order, from)
			t.peers[from] = p
		} else {
			p.mu.Lock()
			if p.cancel != nil {
				select {
				case <-p.cancel:
				default:
					close(p.cancel)
				}
			}
			if p.state == StateSending {
				p.state = StateCanceled
				p.err = reason
			}
			p.mu.Unlock()
		}
	}
	if t, ok := m.in[tid]; ok && from == t.from {
		t.writeMu.Lock()
		if t.curFile != nil {
			_ = t.curFile.Close()
			t.curFile = nil
		}
		if t.state == StateReceiving || t.state == StatePending {
			if reason == "declined" {
				t.state = StateDeclined
			} else {
				t.state = StateCanceled
			}
			t.errMsg = reason
		}
		t.writeMu.Unlock()
	}
	m.mu.Unlock()
}

// stream sends every source file in order to one accepting peer.
func (m *Manager) stream(t *outTransfer, peer string, p *outPeer) {
	fail := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		m.markOutPeer(t.id, peer, StateFailed, msg)
		_ = m.sendControl(peer, protocol.TypeFileCancel, protocol.FileCancel{
			TID: t.id, From: m.self, To: peer, Reason: "send failed",
		})
	}

	cancelled := func() bool {
		select {
		case <-p.cancel:
			return true
		default:
			return false
		}
	}

	var sentTotal int64
	last := len(t.sources) - 1
	for i, sf := range t.sources {
		if cancelled() {
			m.markOutPeer(t.id, peer, StateCanceled, "")
			return
		}
		f, err := m.open(sf.Path)
		if err != nil {
			fail("open %s: %v", sf.Name, err)
			return
		}
		var off int64
		for {
			buf := make([]byte, ChunkSize)
			n, err := io.ReadFull(f, buf)
			if err == io.ErrUnexpectedEOF {
				err = io.EOF // final short read
			}
			if n > 0 {
				if cancelled() {
					f.Close()
					m.markOutPeer(t.id, peer, StateCanceled, "")
					return
				}
				chunk := protocol.FileChunk{
					TID: t.id, From: m.self, To: peer,
					Idx: i, Off: off, Data: buf[:n],
				}
				if sendErr := m.sendControl(peer, protocol.TypeFileChunk, chunk); sendErr != nil {
					f.Close()
					fail("send: %v", sendErr)
					return
				}
				off += int64(n)
				sentTotal += int64(n)
				p.mu.Lock()
				p.done = sentTotal
				p.mu.Unlock()
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				f.Close()
				fail("read %s: %v", sf.Name, err)
				return
			}
		}
		f.Close()
	}

	// Empty terminating chunk; the receiver finalizes and re-arms clipboard.
	fin := protocol.FileChunk{
		TID: t.id, From: m.self, To: peer,
		Idx: last + 1, Off: 0, Fin: true,
	}
	if err := m.sendControl(peer, protocol.TypeFileChunk, fin); err != nil {
		fail("send fin: %v", err)
		return
	}
}

// processChunk applies one validated chunk: writes file data, advances
// transfer state and, on the final chunk, re-arms the system clipboard. It
// runs exclusively on the write loop goroutine (see writeLoop).
func (m *Manager) processChunk(t *inTransfer, ev *protocol.FileChunk) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	fail := func(format string, args ...any) error {
		msg := fmt.Sprintf(format, args...)
		t.state = StateFailed
		t.errMsg = msg
		if t.curFile != nil {
			_ = t.curFile.Close()
			t.curFile = nil
		}
		_ = m.sendControl(t.from, protocol.TypeFileCancel, protocol.FileCancel{
			TID: t.id, From: m.self, To: t.from, Reason: "receive failed",
		})
		return errors.New(msg)
	}

	if t.state != StateReceiving {
		return nil // not accepted (yet); ignore stray chunks
	}
	if len(ev.Data) > ChunkSize {
		return fail("chunk too large: %d", len(ev.Data))
	}
	if ev.Idx < t.curIdx || ev.Idx > len(t.files) {
		return fail("chunk index out of range: %d", ev.Idx)
	}

	if ev.Fin {
		if ev.Idx != len(t.files) || t.curIdx != len(t.files) {
			return fail("fin before all files received (got %d of %d)", t.curIdx, len(t.files))
		}
		t.state = StateDone
		paths := make([]clipboard.LocalFile, 0, len(t.files))
		for i, f := range t.files {
			paths = append(paths, clipboard.LocalFile{
				Name: f.Name, Size: f.Size,
				Path: filepath.Join(t.dir, t.safeNames[i]),
			})
		}
		if m.writeCb == nil {
			return fail("clipboard writer unavailable")
		}
		if err := m.writeCb(clipboard.LocalFiles{Files: paths}); err != nil {
			return fail("set clipboard: %v", err)
		}
		return m.sendControl(t.from, protocol.TypeFileDone, protocol.FileDone{
			TID: t.id, From: m.self, To: t.from,
		})
	}

	if ev.Idx >= len(t.files) {
		return fail("chunk index past end: %d", ev.Idx)
	}
	if ev.Idx != t.curIdx {
		return fail("out-of-order chunk: want file %d, got %d", t.curIdx, ev.Idx)
	}
	if ev.Off != t.curDone {
		return fail("out-of-order offset in file %d: want %d, got %d", ev.Idx, t.curDone, ev.Off)
	}
	meta := t.files[ev.Idx]
	if t.curDone+int64(len(ev.Data)) > meta.Size {
		return fail("chunk exceeds declared size for %q", meta.Name)
	}

	if t.curFile == nil {
		if err := os.MkdirAll(t.dir, 0o755); err != nil {
			return fail("mkdir: %v", err)
		}
		f, err := os.OpenFile(filepath.Join(t.dir, t.safeNames[ev.Idx]),
			os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return fail("create %s: %v", t.safeNames[ev.Idx], err)
		}
		t.curFile = f
	}
	if _, err := t.curFile.Write(ev.Data); err != nil {
		return fail("write: %v", err)
	}
	t.curDone += int64(len(ev.Data))
	t.done += int64(len(ev.Data))

	if t.curDone == meta.Size {
		if err := t.curFile.Close(); err != nil {
			t.curFile = nil
			return fail("close: %v", err)
		}
		t.curFile = nil
		t.curIdx++
		t.curDone = 0
	}
	return nil
}

func (m *Manager) markOutPeer(tid, peer string, state State, errMsg string) {
	m.mu.Lock()
	t, ok := m.out[tid]
	m.mu.Unlock()
	if !ok {
		return
	}
	p, ok := t.peers[peer]
	if !ok {
		return
	}
	p.mu.Lock()
	p.state = state
	p.err = errMsg
	p.mu.Unlock()
}

func (m *Manager) sendControl(to string, typ protocol.Type, payload any) error {
	env, err := protocol.NewEnvelope(typ, 0, payload)
	if err != nil {
		return err
	}
	return m.emit(to, env)
}

// Statuses returns UI-facing snapshots of recent transfers.
func (m *Manager) Statuses() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.outOrder)+len(m.inOrder))
	for _, id := range m.inOrder {
		t := m.in[id]
		if t == nil {
			continue
		}
		files := make([]FileStatus, len(t.files))
		for i, f := range t.files {
			done := f.Size
			if t.state != StateDone {
				// Per-file split is only tracked coarsely on inbound.
				if i < t.curIdx {
					// done
				} else if i == t.curIdx {
					done = t.curDone
				} else {
					done = 0
				}
			}
			files[i] = FileStatus{Name: f.Name, Size: f.Size, Done: done}
		}
		out = append(out, Status{
			ID: t.id, Direction: DirIn, Peer: t.from, State: t.state,
			Auto: t.auto, Files: files, Total: t.total, Done: t.done, Error: t.errMsg,
		})
	}
	for _, id := range m.outOrder {
		t := m.out[id]
		if t == nil {
			continue
		}
		for _, peer := range t.order {
			p := t.peers[peer]
			if p == nil {
				continue
			}
			p.mu.Lock()
			state, done, errMsg := p.state, p.done, p.err
			p.mu.Unlock()
			files := []FileStatus{{Name: fmt.Sprintf("%d 个文件", len(t.sources)), Size: t.total, Done: done}}
			out = append(out, Status{
				ID: t.id, Direction: DirOut, Peer: peer, State: state,
				Files: files, Total: t.total, Done: done, Error: errMsg,
			})
		}
	}
	return out
}

// CleanupStaging removes staged transfer directories older than maxAge. Best
// effort; intended to run once at startup.
func (m *Manager) CleanupStaging(maxAge time.Duration) {
	entries, err := os.ReadDir(m.staging)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil || fi.ModTime().After(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(m.staging, e.Name()))
	}
}

func (m *Manager) trimOrder(order *[]string, max int) {
	if len(*order) <= max {
		return
	}
	*order = append([]string(nil), (*order)[len(*order)-max:]...)
}

// ---------- helpers ----------

func newTransferID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%x-%s", time.Now().UnixNano(), hex.EncodeToString(b[:]))
}

func metasOf(src []sourceFile) []protocol.FileMeta {
	out := make([]protocol.FileMeta, len(src))
	for i, s := range src {
		out[i] = s.FileMeta
	}
	return out
}

// sanitizeName accepts a bare file name (no directories) and rejects path
// traversal and control characters.
func sanitizeName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "", false
	}
	// Names must be bare file names: separators/drive colons/control chars
	// are rejected outright rather than basenamed, so a peer can never steer
	// a write outside the staging directory.
	if strings.ContainsAny(name, `/\:*?"<>|`+"\x00") {
		return "", false
	}
	if filepath.Base(name) != name {
		return "", false
	}
	if strings.HasPrefix(name, "~$") { // Office lock temp files
		return "", false
	}
	return name, true
}

// uniqueName returns a name not yet present in used, adding " (n)" before the
// extension on collision.
func uniqueName(name string, used map[string]bool) string {
	if !used[name] {
		used[name] = true
		return name
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if !used[cand] {
			used[cand] = true
			return cand
		}
	}
}
