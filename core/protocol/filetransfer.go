package protocol

// File transfer control messages. The file OFFER itself rides the existing
// TypeClipboard channel as Mime "application/x-crossscreen-files"; only the
// transfer handshake and data use these types. Every payload carries From/To
// device names so a server can relay messages between two client peers.

const (
	// TypeFileAccept accepts an offer (auto or user-confirmed) and asks the
	// sender to start streaming chunks. Payload: FileAccept.
	TypeFileAccept Type = 12
	// TypeFileChunk is one ordered slice of file content. Payload: FileChunk.
	TypeFileChunk Type = 13
	// TypeFileCancel aborts a pending/active transfer from either side.
	TypeFileCancel Type = 14
	// TypeFileDone tells the sender that every chunk landed and the receiver
	// has updated its clipboard. Payload: FileDone.
	TypeFileDone Type = 15
)

// FileMeta describes one offered file (name only; paths never leave a host).
type FileMeta struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// FileAccept starts a transfer: To is the offerer, From the receiver.
type FileAccept struct {
	TID  string `json:"tid"`
	From string `json:"from"`
	To   string `json:"to"`
}

// FileChunk is one block of the ordered file stream. Idx is the file index
// within the offer; blocks arrive in order, Off is the byte offset in that
// file; Fin marks the final block of the whole transfer.
type FileChunk struct {
	TID  string `json:"tid"`
	From string `json:"from"`
	To   string `json:"to"`
	Idx  int    `json:"idx"`
	Off  int64  `json:"off"`
	Data []byte `json:"data"`
	Fin  bool   `json:"fin"`
}

// FileCancel aborts a transfer with an optional human-readable reason.
type FileCancel struct {
	TID    string `json:"tid"`
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason,omitempty"`
}

// FileDone confirms a completed inbound transfer.
type FileDone struct {
	TID  string `json:"tid"`
	From string `json:"from"`
	To   string `json:"to"`
}
