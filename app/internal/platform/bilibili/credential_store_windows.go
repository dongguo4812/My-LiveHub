//go:build windows

package bilibili

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

func protect(data []byte) ([]byte, error) {
	if len(data) == 0 { return nil, errors.New("cannot protect empty credentials") }
	input := windows.DataBlob{Size:uint32(len(data)), Data:&data[0]}
	var output windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil { return nil, err }
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	return append([]byte(nil), unsafe.Slice(output.Data, output.Size)...), nil
}

func unprotect(data []byte) ([]byte, error) {
	if len(data) == 0 { return nil, errors.New("encrypted credential data is empty") }
	input := windows.DataBlob{Size:uint32(len(data)), Data:&data[0]}
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil { return nil, err }
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	return append([]byte(nil), unsafe.Slice(output.Data, output.Size)...), nil
}
