//go:build (lxquickjs || lxproduction) && cgo

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

 
//export lxqjsGoMD5
func lxqjsGoMD5(data *C.char, dataLen C.size_t) *C.char {
	if dataLen > 8<<20 { return nil }
	var input []byte
	if dataLen > 0 { input = C.GoBytes(unsafe.Pointer(data), C.int(dataLen)) }
	return C.CString(lxCryptoMD5(input))
}

//export lxqjsGoRandomBytes
func lxqjsGoRandomBytes(length C.size_t) unsafe.Pointer {
	if length > 1<<20 { return nil }
	result, err := lxCryptoRandomBytes(int(length))
	if err != nil { return nil }
	if len(result) == 0 { return C.malloc(1) }
	return C.CBytes(result)
}

//export lxqjsGoAESEncrypt
func lxqjsGoAESEncrypt(data *C.char, dataLen C.size_t, mode *C.char,
	key *C.char, keyLen C.size_t, iv *C.char, ivLen C.size_t,
	outLen *C.size_t) unsafe.Pointer {
	if dataLen > 1<<20 || keyLen > 64 || ivLen > 32 || outLen == nil || mode == nil { return nil }
	var input, keyBytes, ivBytes []byte
	if dataLen > 0 { input = C.GoBytes(unsafe.Pointer(data), C.int(dataLen)) }
	if keyLen > 0 { if key == nil { return nil }; keyBytes = C.GoBytes(unsafe.Pointer(key), C.int(keyLen)) }
	if ivLen > 0 { if iv == nil { return nil }; ivBytes = C.GoBytes(unsafe.Pointer(iv), C.int(ivLen)) }
	result, err := lxCryptoAESEncrypt(input, C.GoString(mode), keyBytes, ivBytes)
	if err != nil { return nil }
	*outLen = C.size_t(len(result))
	if len(result) == 0 { return C.malloc(1) }
	return C.CBytes(result)
}
