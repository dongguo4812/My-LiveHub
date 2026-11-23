//go:build (lxquickjs || lxproduction) && cgo

package lxruntime

/*
#include <stddef.h>
#include <stdlib.h>
*/
import "C"

import (
	"unsafe"
)

//export lxqjsGoZlib
func lxqjsGoZlib(data *C.char, length C.size_t, inflate C.int, outLen *C.size_t, errorOut **C.char) unsafe.Pointer {
	if errorOut != nil { *errorOut = nil }
	if length > lxMaxZlibInputBytes || outLen == nil || errorOut == nil {
		if errorOut != nil { *errorOut = C.CString("invalid zlib input or input exceeds limit") }
		return nil
	}
	input := C.GoBytes(unsafe.Pointer(data), C.int(length))
	var output []byte
	var err error
	if inflate != 0 { output, err = lxZlibInflate(input) } else { output, err = lxZlibDeflate(input) }
	if err != nil {
		*errorOut = C.CString(err.Error())
		return nil
	}
	*outLen = C.size_t(len(output))
	if len(output) == 0 { return C.malloc(1) }
	return C.CBytes(output)
}
