//go:build lxquickjs && cgo

package lxruntime

/*
#include <stddef.h>
*/
import "C"

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"unsafe"
)

//export lxqjsGoMD5
func lxqjsGoMD5(data *C.char, length C.size_t) *C.char {
	if length > 1<<20 { return nil }
	input := C.GoBytes(unsafe.Pointer(data), C.int(length))
	digest := md5.Sum(input)
	return C.CString(hex.EncodeToString(digest[:]))
}

//export lxqjsGoRandomBytes
func lxqjsGoRandomBytes(length C.size_t) unsafe.Pointer {
	if length > 1<<20 || length == 0 { return nil }
	buffer := make([]byte, int(length))
	if _, err := rand.Read(buffer); err != nil { return nil }
	return C.CBytes(buffer)
}
