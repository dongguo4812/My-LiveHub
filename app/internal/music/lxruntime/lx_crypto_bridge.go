//go:build lxquickjs && cgo

package lxruntime

/*
#include <stddef.h>
#include <stdlib.h>
*/
import "C"

import "unsafe"

//export lxqjsGoRSAEncrypt
func lxqjsGoRSAEncrypt(data *C.char, dataLen C.size_t, key *C.char, keyLen C.size_t, outLen *C.size_t) unsafe.Pointer {
	if dataLen > lxRSABlockSize || keyLen == 0 || keyLen > 1<<20 || outLen == nil || key == nil {
		return nil
	}
	var input []byte
	if dataLen > 0 {
		input = C.GoBytes(unsafe.Pointer(data), C.int(dataLen))
	}
	keyBytes := C.GoBytes(unsafe.Pointer(key), C.int(keyLen))
	result, err := lxCryptoRSAEncrypt(input, keyBytes)
	if err != nil {
		return nil
	}
	*outLen = C.size_t(len(result))
	return C.CBytes(result)
}

 
//export lxqjsGoBufferFrom
func lxqjsGoBufferFrom(data *C.char, dataLen C.size_t, encoding *C.char, outLen *C.size_t) unsafe.Pointer {
	if dataLen > 8<<20 || outLen == nil { return nil }
	var input []byte
	if dataLen > 0 { input = C.GoBytes(unsafe.Pointer(data), C.int(dataLen)) }
	enc := "utf8"
	if encoding != nil { enc = C.GoString(encoding) }
	result, err := lxBufferFromString(input, enc)
	if err != nil { return nil }
	*outLen = C.size_t(len(result))
	if len(result) == 0 { return C.malloc(1) }
	return C.CBytes(result)
}

//export lxqjsGoBufferToString
func lxqjsGoBufferToString(data *C.char, dataLen C.size_t, encoding *C.char, outLen *C.size_t) unsafe.Pointer {
	if dataLen > 8<<20 || outLen == nil { return nil }
	var input []byte
	if dataLen > 0 { input = C.GoBytes(unsafe.Pointer(data), C.int(dataLen)) }
	enc := "utf8"
	if encoding != nil { enc = C.GoString(encoding) }
	result, err := lxBufferToString(input, enc)
	if err != nil { return nil }
	*outLen = C.size_t(len(result))
	if len(result) == 0 { return C.malloc(1) }
	return C.CBytes([]byte(result))
}
