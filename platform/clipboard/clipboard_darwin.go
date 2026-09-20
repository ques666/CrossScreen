//go:build darwin

package clipboard

/*
#cgo LDFLAGS: -framework AppKit -framework Foundation
#include <stdlib.h>

extern int cs_pb_write_text(const char *utf8);
extern char *cs_pb_read_text(void);
extern char *cs_pb_read_files_json(void);
extern int cs_pb_write_files_json(const char *json);
extern long long cs_pb_change_count(void);
*/
import "C"

import (
	"encoding/json"
	"errors"
	"unsafe"
)

func platformInit() error { return nil }

func platformWrite(d Data) error {
	switch d.Mime {
	case MimeFiles:
		cstr := C.CString(string(d.Data))
		defer C.free(unsafe.Pointer(cstr))
		if C.cs_pb_write_files_json(cstr) != 0 {
			return errors.New("clipboard: NSPasteboard file write failed")
		}
		return nil
	default:
		cstr := C.CString(string(d.Data))
		defer C.free(unsafe.Pointer(cstr))
		if C.cs_pb_write_text(cstr) != 0 {
			return errors.New("clipboard: NSPasteboard write failed")
		}
		return nil
	}
}

func platformRead() (Data, bool, error) {
	// File references take precedence over text.
	if p := C.cs_pb_read_files_json(); p != nil {
		defer C.free(unsafe.Pointer(p))
		raw := C.GoString(p)
		var files []LocalFile
		if err := json.Unmarshal([]byte(raw), &files); err == nil && len(files) > 0 {
			b, err := json.Marshal(LocalFiles{Files: files})
			if err == nil {
				return Data{Mime: MimeFiles, Data: b}, true, nil
			}
		}
	}

	p := C.cs_pb_read_text()
	if p == nil {
		return Data{}, false, nil
	}
	defer C.free(unsafe.Pointer(p))
	return Text(C.GoString(p)), true, nil
}

func platformSequence() (uint64, error) {
	return uint64(C.cs_pb_change_count()), nil
}
