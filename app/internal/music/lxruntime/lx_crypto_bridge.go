//go:build lxquickjs && cgo

package lxruntime

/*
#include <stddef.h>
#include <stdlib.h>
*/
import "C"

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"unicode/utf16"
	"encoding/binary"
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
	if err != nil { return nil }
	*outLen = C.size_t(len(result))
	return C.CBytes(result)
}


//export lxqjsGoBufferFrom
func lxqjsGoBufferFrom(data *C.char, dataLen C.size_t, encoding *C.char, outLen *C.size_t) unsafe.Pointer {
	if dataLen > 1<<20 || encoding == nil || outLen == nil { return nil }
	input := C.GoBytes(unsafe.Pointer(data), C.int(dataLen))
	enc := strings.ToLower(C.GoString(encoding))
	var output []byte
	var err error
	switch enc {
	case "", "utf8", "utf-8":
		output = input
	case "hex":
		output, err = hex.DecodeString(string(input))
	case "base64":
		output, err = base64.StdEncoding.DecodeString(string(input))
	case "base64url":
		output, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(string(input), "="))
	case "latin1", "binary":
		runes := []rune(string(input))
		output = make([]byte, len(runes))
		for i, r := range runes { output[i] = byte(r) }
	case "ascii":
		runes := []rune(string(input))
		output = make([]byte, len(runes))
		for i, r := range runes { output[i] = byte(r) & 0x7f }
	case "utf16le", "utf-16le", "ucs2", "ucs-2":
		units := utf16.Encode([]rune(string(input)))
		output = make([]byte, len(units)*2)
		for i, unit := range units { binary.LittleEndian.PutUint16(output[i*2:], unit) }
	default:
		return nil
	}
	if err != nil { return nil }
	*outLen = C.size_t(len(output))
	if len(output) == 0 { return C.malloc(1) }
	return C.CBytes(output)
}

//export lxqjsGoBufferToString
func lxqjsGoBufferToString(data *C.char, dataLen C.size_t, encoding *C.char, outLen *C.size_t) unsafe.Pointer {
	if dataLen > 1<<20 || encoding == nil || outLen == nil { return nil }
	input := C.GoBytes(unsafe.Pointer(data), C.int(dataLen))
	enc := strings.ToLower(C.GoString(encoding))
	var output string
	switch enc {
	case "", "utf8", "utf-8":
		output = string(input)
	case "hex":
		output = hex.EncodeToString(input)
	case "base64":
		output = base64.StdEncoding.EncodeToString(input)
	case "base64url":
		output = base64.RawURLEncoding.EncodeToString(input)
	case "latin1", "binary":
		runes := make([]rune, len(input))
		for i, b := range input { runes[i] = rune(b) }
		output = string(runes)
	case "ascii":
		runes := make([]rune, len(input))
		for i, b := range input { runes[i] = rune(b & 0x7f) }
		output = string(runes)
	case "utf16le", "utf-16le", "ucs2", "ucs-2":
		evenLength := len(input) &^ 1
		units := make([]uint16, evenLength/2)
		for i := range units { units[i] = binary.LittleEndian.Uint16(input[i*2:]) }
		output = string(utf16.Decode(units))
	default:
		return nil
	}
	bytes := []byte(output)
	*outLen = C.size_t(len(bytes))
	if len(bytes) == 0 { return C.malloc(1) }
	return C.CBytes(bytes)
}
