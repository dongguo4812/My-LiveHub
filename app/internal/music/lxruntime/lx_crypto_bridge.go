//go:build lxquickjs || lxproduction

package lxruntime

import (
	"errors"
	"unsafe"
)

var nativeAllocator func(uintptr) uintptr

func setNativeAllocator(alloc func(uintptr) uintptr) { nativeAllocator = alloc }
func allocNativeBytes(data []byte) uintptr {
	if nativeAllocator == nil { return 0 }
	ptr := nativeAllocator(uintptr(len(data)+1))
	if ptr == 0 { return 0 }
	dst := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), len(data)+1)
	copy(dst, data)
	dst[len(data)] = 0
	return ptr
}
func allocNativeString(value string) uintptr { return allocNativeBytes([]byte(value)) }
func readNativeBytes(ptr, length uintptr, limit uintptr) ([]byte, error) {
	if length > limit { return nil, errors.New("native input exceeds size limit") }
	if length == 0 { return []byte{}, nil }
	if ptr == 0 { return nil, errors.New("native input pointer is null") }
	return append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(ptr)), int(length))...), nil
}
func readNativeString(ptr uintptr, limit int) (string, error) {
	if ptr == 0 { return "", nil }
	data := make([]byte, 0, 64)
	for i := 0; i < limit; i++ {
		b := *(*byte)(unsafe.Pointer(ptr + uintptr(i)))
		if b == 0 { return string(data), nil }
		data = append(data, b)
	}
	return "", errors.New("native string is not terminated within limit")
}
func writeNativeLength(ptr uintptr, length int) bool {
	if ptr == 0 { return false }
	*(*uintptr)(unsafe.Pointer(ptr)) = uintptr(length)
	return true
}
func lxqjsGoMD5(data, length uintptr) (result uintptr) {
	defer func(){ if recover()!=nil { result=0 } }()
	input, err := readNativeBytes(data,length,8<<20); if err!=nil{return 0}
	return allocNativeString(lxCryptoMD5(input))
}
func lxqjsGoRandomBytes(length uintptr) (result uintptr) {
	defer func(){ if recover()!=nil { result=0 } }()
	if length > 1<<20 { return 0 }
	value, err := lxCryptoRandomBytes(int(length)); if err!=nil{return 0}
	return allocNativeBytes(value)
}
func lxqjsGoBufferFrom(data, length, encoding, outLength uintptr) (result uintptr) {
	defer func(){ if recover()!=nil { result=0 } }()
	input,err:=readNativeBytes(data,length,8<<20);if err!=nil||outLength==0{return 0}
	enc:="utf8";if encoding!=0 { enc,err=readNativeString(encoding,128);if err!=nil{return 0} }
	value,err:=lxBufferFromString(input,enc);if err!=nil{return 0}
	if !writeNativeLength(outLength,len(value)){return 0}
	return allocNativeBytes(value)
}
func lxqjsGoBufferToString(data, length, encoding, outLength uintptr) (result uintptr) {
	defer func(){ if recover()!=nil { result=0 } }()
	input,err:=readNativeBytes(data,length,8<<20);if err!=nil||outLength==0{return 0}
	enc:="utf8";if encoding!=0 { enc,err=readNativeString(encoding,128);if err!=nil{return 0} }
	value,err:=lxBufferToString(input,enc);if err!=nil{return 0}
	if !writeNativeLength(outLength,len(value)){return 0}
	return allocNativeBytes([]byte(value))
}
func lxqjsGoRSAEncrypt(data, dataLength, key, keyLength, outLength uintptr) (result uintptr) {
	defer func(){ if recover()!=nil { result=0 } }()
	if outLength==0||key==0||keyLength==0||keyLength>1<<20{return 0}
	input,err:=readNativeBytes(data,dataLength,lxRSABlockSize);if err!=nil{return 0}
	keyBytes,err:=readNativeBytes(key,keyLength,1<<20);if err!=nil{return 0}
	value,err:=lxCryptoRSAEncrypt(input,keyBytes);if err!=nil{return 0}
	if !writeNativeLength(outLength,len(value)){return 0}
	return allocNativeBytes(value)
}
func lxqjsGoAESEncrypt(data,dataLength,mode,key,keyLength,iv,ivLength,outLength uintptr)(result uintptr){
	defer func(){if recover()!=nil{result=0}}()
	if outLength==0||mode==0||dataLength>1<<20||keyLength>64||ivLength>32{return 0}
	input,err:=readNativeBytes(data,dataLength,1<<20);if err!=nil{return 0}
	modeText,err:=readNativeString(mode,64);if err!=nil{return 0}
	var keyBytes,ivBytes []byte
	if keyLength>0 { keyBytes,err=readNativeBytes(key,keyLength,64);if err!=nil{return 0} }
	if ivLength>0 { ivBytes,err=readNativeBytes(iv,ivLength,32);if err!=nil{return 0} }
	value,err:=lxCryptoAESEncrypt(input,modeText,keyBytes,ivBytes);if err!=nil{return 0}
	if !writeNativeLength(outLength,len(value)){return 0}
	return allocNativeBytes(value)
}
func lxqjsGoZlib(data,length,inflate,outLength,errorOut uintptr)(result uintptr){
	defer func(){if recover()!=nil{result=0}}()
	if outLength==0||errorOut==0||length>lxMaxZlibInputBytes {
		if errorOut!=0 { *(*uintptr)(unsafe.Pointer(errorOut))=allocNativeString("invalid zlib input or input exceeds limit") }
		return 0
	}
	input,err:=readNativeBytes(data,length,lxMaxZlibInputBytes);if err!=nil{*(*uintptr)(unsafe.Pointer(errorOut))=allocNativeString(err.Error());return 0}
	var output []byte
	if inflate!=0 { output,err=lxZlibInflate(input) } else { output,err=lxZlibDeflate(input) }
	if err!=nil { *(*uintptr)(unsafe.Pointer(errorOut))=allocNativeString(err.Error());return 0 }
	if !writeNativeLength(outLength,len(output)){return 0}
	return allocNativeBytes(output)
}
