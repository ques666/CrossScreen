//go:build windows

package clipboard

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procEmptyClipboard             = user32.NewProc("EmptyClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procSetClipboardData           = user32.NewProc("SetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procGetClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")

	procGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	procGlobalLock   = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
	procGlobalFree   = kernel32.NewProc("GlobalFree")
	procLstrlenW     = kernel32.NewProc("lstrlenW")
)

const (
	cfUnicodeText = 13
	cfHDrop       = 15
	gmemMoveable  = 0x0002
)

// dropFiles is the Windows DROPFILES header placed before the file list in a
// CF_HDROP global object.
type dropFiles struct {
	PFiles uint32 // offset to file list from start of structure
	PtX    int32
	PtY    int32
	FNC    int32 // non-client area flag
	FWide  int32 // 1 = UTF-16 list
}

func platformInit() error { return nil }

func platformWrite(d Data) error {
	switch d.Mime {
	case MimeFiles:
		var lf LocalFiles
		if err := json.Unmarshal(d.Data, &lf); err != nil {
			return err
		}
		if len(lf.Files) == 0 {
			return errors.New("clipboard: empty file list")
		}
		return writeHDrop(lf.Files)
	default:
		return writeText(string(d.Data))
	}
}

func writeText(s string) error {
	u16, err := syscall.UTF16FromString(s)
	if err != nil {
		return err
	}
	size := uintptr(len(u16)) * 2
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return errors.New("clipboard: GlobalAlloc failed")
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return errors.New("clipboard: GlobalLock failed")
	}
	raw := unsafe.Slice((*byte)(unsafe.Pointer(p)), len(u16)*2)
	for i, u := range u16 {
		raw[i*2] = byte(u)
		raw[i*2+1] = byte(u >> 8)
	}
	procGlobalUnlock.Call(h)

	if r, _, _ := procOpenClipboard.Call(0); r == 0 {
		procGlobalFree.Call(h)
		return errors.New("clipboard: OpenClipboard failed")
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	// Ownership of h transfers to the system on success.
	if r, _, _ := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		procGlobalFree.Call(h)
		return errors.New("clipboard: SetClipboardData failed")
	}
	return nil
}

// writeHDrop publishes a CF_HDROP so Explorer pastes real files.
func writeHDrop(files []LocalFile) error {
	headerSize := uint32(unsafe.Sizeof(dropFiles{}))

	var list []uint16
	for _, f := range files {
		u, err := syscall.UTF16FromString(f.Path)
		if err != nil {
			return err
		}
		list = append(list, u...) // includes terminating NUL
	}
	list = append(list, 0) // extra NUL ends the list

	total := int(headerSize) + len(list)*2
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(total))
	if h == 0 {
		return errors.New("clipboard: GlobalAlloc failed")
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return errors.New("clipboard: GlobalLock failed")
	}
	hdr := dropFiles{PFiles: headerSize, FWide: 1}
	var hdrBuf [20]byte
	binary.LittleEndian.PutUint32(hdrBuf[0:], hdr.PFiles)
	binary.LittleEndian.PutUint32(hdrBuf[4:], 0)
	binary.LittleEndian.PutUint32(hdrBuf[8:], 0)
	binary.LittleEndian.PutUint32(hdrBuf[12:], 0)
	binary.LittleEndian.PutUint32(hdrBuf[16:], 1)

	dst := unsafe.Slice((*byte)(unsafe.Pointer(p)), total)
	copy(dst[:20], hdrBuf[:])
	for i, u := range list {
		dst[int(headerSize)+i*2] = byte(u)
		dst[int(headerSize)+i*2+1] = byte(u >> 8)
	}
	procGlobalUnlock.Call(h)

	if r, _, _ := procOpenClipboard.Call(0); r == 0 {
		procGlobalFree.Call(h)
		return errors.New("clipboard: OpenClipboard failed")
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	if r, _, _ := procSetClipboardData.Call(cfHDrop, h); r == 0 {
		procGlobalFree.Call(h)
		return errors.New("clipboard: SetClipboardData(CF_HDROP) failed")
	}
	return nil
}

func platformRead() (Data, bool, error) {
	// File references take precedence over text.
	if files, ok := readHDrop(); ok && len(files) > 0 {
		b, err := json.Marshal(LocalFiles{Files: files})
		if err == nil {
			return Data{Mime: MimeFiles, Data: b}, true, nil
		}
	}
	return readUnicodeText()
}

// readHDrop parses a CF_HDROP list of wide-char file paths.
func readHDrop() ([]LocalFile, bool) {
	if r, _, _ := procIsClipboardFormatAvailable.Call(cfHDrop); r == 0 {
		return nil, false
	}
	if r, _, _ := procOpenClipboard.Call(0); r == 0 {
		return nil, false
	}
	defer procCloseClipboard.Call()
	h, _, _ := procGetClipboardData.Call(cfHDrop)
	if h == 0 {
		return nil, false
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return nil, false
	}
	defer procGlobalUnlock.Call(h)

	var hdr dropFiles
	hdrBytes := unsafe.Slice((*byte)(unsafe.Pointer(p)), 20)
	hdr.PFiles = binary.LittleEndian.Uint32(hdrBytes[0:])
	hdr.FWide = int32(binary.LittleEndian.Uint32(hdrBytes[16:]))
	if hdr.FWide == 0 {
		return nil, false // ANSI lists are not used by modern Explorer
	}

	base := unsafe.Pointer(uintptr(p) + uintptr(hdr.PFiles))
	var out []LocalFile
	cursor := uintptr(0)
	for {
		// Walk to the next NUL to bound this UTF-16 string.
		var n uintptr
		for {
			ch := *(*uint16)(unsafe.Pointer(uintptr(base) + cursor + n*2))
			if ch == 0 {
				break
			}
			n++
		}
		if n == 0 {
			break // final empty string ends the list
		}
		u16 := unsafe.Slice((*uint16)(unsafe.Pointer(uintptr(base)+cursor)), int(n))
		path := syscall.UTF16ToString(u16)
		cursor += (n + 1) * 2
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			out = append(out, LocalFile{Name: fi.Name(), Size: fi.Size(), Path: path})
		}
	}
	return out, len(out) > 0
}

func readUnicodeText() (Data, bool, error) {
	if r, _, _ := procIsClipboardFormatAvailable.Call(cfUnicodeText); r == 0 {
		return Data{}, false, nil
	}
	if r, _, _ := procOpenClipboard.Call(0); r == 0 {
		return Data{}, false, nil
	}
	defer procCloseClipboard.Call()
	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return Data{}, false, nil
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return Data{}, false, nil
	}
	defer procGlobalUnlock.Call(h)

	n, _, _ := procLstrlenW.Call(p)
	u16 := unsafe.Slice((*uint16)(unsafe.Pointer(p)), int(n))
	return Text(syscall.UTF16ToString(u16)), true, nil
}

func platformSequence() (uint64, error) {
	r, _, _ := procGetClipboardSequenceNumber.Call()
	return uint64(r), nil
}
