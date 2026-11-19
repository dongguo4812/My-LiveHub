//go:build lxquickjs && cgo

package lxruntime

/*
#include <stddef.h>
*/
import "C"

import (
	"crypto/md5"
	"crypto/rand"
	"fmt"
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


//export lxqjsGoAESEncrypt
func lxqjsGoAESEncrypt(data *C.char, dataLen C.size_t, mode *C.char, key *C.char, keyLen C.size_t, iv *C.char, ivLen C.size_t, outLen *C.size_t) unsafe.Pointer {
	if dataLen > 1<<20 || keyLen > 64 || ivLen > 32 || mode == nil || key == nil || outLen == nil { return nil }
	plaintext := C.GoBytes(unsafe.Pointer(data), C.int(dataLen))
	keyBytes := C.GoBytes(unsafe.Pointer(key), C.int(keyLen))
	var ivBytes []byte
	if iv != nil && ivLen > 0 { ivBytes = C.GoBytes(unsafe.Pointer(iv), C.int(ivLen)) }
	result, err := lxCryptoAESEncrypt(plaintext, C.GoString(mode), keyBytes, ivBytes)
	if err != nil { _ = fmt.Sprint(err); return nil }
	*outLen = C.size_t(len(result))
	return C.CBytes(result)
}
