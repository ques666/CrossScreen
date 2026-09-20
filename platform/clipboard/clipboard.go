// Package clipboard reads, writes and watches the system clipboard so a
// cross-screen session can share text between devices.
//
// Echo prevention: every Write records the content it produced; the watcher
// ignores changes whose content matches, so a change applied from a peer is
// never re-broadcast back.
package clipboard

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"time"
)

// MIME types exchanged over the session.
const (
	MimeText  = "text/plain"
	MimeFiles = "application/x-crossscreen-files"
)

// Data is a clipboard payload: text (MimeText) or a local file-reference list
// (MimeFiles). File payloads never contain file contents — only absolute
// paths on the local machine, which the file transfer layer turns into a
// content stream.
type Data struct {
	Mime string `json:"mime"`
	Data []byte `json:"data"`
}

// Text returns a text/plain payload.
func Text(s string) Data {
	return Data{Mime: MimeText, Data: []byte(s)}
}

// LocalFile is one file referenced by the system clipboard.
type LocalFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Path string `json:"path"`
}

// LocalFiles is the JSON shape carried in a MimeFiles clipboard payload.
type LocalFiles struct {
	Files []LocalFile `json:"files"`
}

// WriteFiles sets the clipboard to reference the given local files and
// records the payload for echo suppression.
func (c *Clipper) WriteFiles(files LocalFiles) error {
	b, err := json.Marshal(files)
	if err != nil {
		return err
	}
	return c.Write(Data{Mime: MimeFiles, Data: b})
}

// PollInterval is how often the clipboard change sequence is sampled.
var PollInterval = 400 * time.Millisecond

// Clipper wraps the platform clipboard with echo suppression.
type Clipper struct {
	mu          sync.Mutex
	lastWritten []byte
}

// New creates a clipper for the current platform.
func New() (*Clipper, error) {
	if err := platformInit(); err != nil {
		return nil, err
	}
	return &Clipper{}, nil
}

// Read returns the current clipboard content; ok is false when the clipboard
// holds no supported text.
func (c *Clipper) Read() (Data, bool, error) {
	return platformRead()
}

// Write sets the clipboard and records the content to suppress echo.
func (c *Clipper) Write(d Data) error {
	if err := platformWrite(d); err != nil {
		return err
	}
	c.mu.Lock()
	c.lastWritten = append([]byte(nil), d.Data...)
	c.mu.Unlock()
	return nil
}

// Watch polls for clipboard changes and calls h with new content. It exits
// when ctx is cancelled. Changes produced by this process (via Write) are
// skipped so they are not re-broadcast.
func (c *Clipper) Watch(ctx context.Context, h func(Data)) {
	last, _ := platformSequence()
	t := time.NewTicker(PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			seq, err := platformSequence()
			if err != nil || seq == last {
				continue
			}
			last = seq
			d, ok, err := platformRead()
			if err != nil || !ok {
				continue
			}
			c.mu.Lock()
			own := len(c.lastWritten) > 0 && bytes.Equal(c.lastWritten, d.Data)
			if own {
				c.lastWritten = nil // consume the marker
			}
			c.mu.Unlock()
			if own {
				continue
			}
			h(d)
		}
	}
}
