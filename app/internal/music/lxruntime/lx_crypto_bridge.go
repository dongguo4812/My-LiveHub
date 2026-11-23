//go:build lxquickjs || lxproduction

package lxruntime

import (
	"errors"
	"unsafe"
)

var nativeAllocator func(uintptr) unsafe.Pointer

func setNativeAllocator(alloc func(uintptr) unsafe.Pointer) { nativeAllocator = alloc }
func allocNativeBytes(data []byte) unsafe.Pointer {
	if nativeAllocator == nil { return nil }
	ptr := nativeAllocator(uintptr(len(data)+1))
	if ptr == nil { return nil }
	dst := unsafe.Slice((*byte)(ptr), len(data)+1)
	copy(dst, data)
	dst[len(data)] = 0
	return ptr
}
func allocNativeString(value string) unsafe.Pointer { return allocNativeBytes([]byte(value)) }
func readNativeBytes(ptr unsafe.Pointer, length, limit uintptr) ([]byte, error) {
	if length > limit { return nil, errors.New("native input exceeds size limit") }
	if length == 0 { return []byte{}, nil }
	if ptr == nil { return nil, errors.New("native input pointer is null") }
	return append([]byte(nil), unsafe.Slice((*byte)(ptr), int(length))...), nil
}
func readNativeString(ptr unsafe.Pointer, limit int) (string, error) {
	if ptr == nil { return "", nil }
	data := make([]byte, 0, 64)
	for i := 0; i < limit; i++ {
		b := *(*byte)(unsafe.Add(ptr, uintptr(i)))
		if b == 0 { return string(data), nil }
		data = append(data, b)
	}
	return "", errors.New("native string is not terminated within limit")
}
func writeNativeLength(ptr unsafe.Pointer, length int) bool {
	if ptr == nil { return false }
	*(*uintptr)(ptr) = uintptr(length)
	return true
}
func lxqjsGoMD5(data unsafe.Pointer, length uintptr) (result uintptr) {
	defer func(){ if recover()!=nil { result=0 } }()
	input, err := readNativeBytes(data,length,8<<20); if err!=nil{return 0}
	return uintptr(allocNativeString(lxCryptoMD5(input)))
}
func lxqjsGoRandomBytes(length uintptr) (result uintptr) {
	defer func(){ if recover()!=nil { result=0 } }()
	if length > 1<<20 { return 0 }
	value, err := lxCryptoRandomBytes(int(length)); if err!=nil{return 0}
	return uintptr(allocNativeBytes(value))
}
func lxqjsGoBufferFrom(data unsafe.Pointer, length uintptr, encoding, outLength unsafe.Pointer) (result uintptr) {
	defer func(){ if recover()!=nil { result=0 } }()
	input,err:=readNativeBytes(data,length,8<<20);if err!=nil||outLength==nil{return 0}
	enc:="utf8";if encoding!=nil { enc,err=readNativeString(encoding,128);if err!=nil{return 0} }
	value,err:=lxBufferFromString(input,enc);if err!=nil{return 0}
	if !writeNativeLength(outLength,len(value)){return 0}
	return uintptr(allocNativeBytes(value))
}
func lxqjsGoBufferToString(data unsafe.Pointer, length uintptr, encoding, outLength unsafe.Pointer) (result uintptr) {
	defer func(){ if recover()!=nil { result=0 } }()
	input,err:=readNativeBytes(data,length,8<<20);if err!=nil||outLength==nil{return 0}
	enc:="utf8";if encoding!=nil { enc,err=readNativeString(encoding,128);if err!=nil{return 0} }
	value,err:=lxBufferToString(input,enc);if err!=nil{return 0}
	if !writeNativeLength(outLength,len(value)){return 0}
	return uintptr(allocNativeBytes([]byte(value)))
}
func lxqjsGoRSAEncrypt(data unsafe.Pointer, dataLength uintptr, key unsafe.Pointer, keyLength uintptr, outLength unsafe.Pointer) (result uintptr) {
	defer func(){ if recover()!=nil { result=0 } }()
	if outLength==nil||key==nil||keyLength==0||keyLength>1<<20{return 0}
	input,err:=readNativeBytes(data,dataLength,lxRSABlockSize);if err!=nil{return 0}
	keyBytes,err:=readNativeBytes(key,keyLength,1<<20);if err!=nil{return 0}
	value,err:=lxCryptoRSAEncrypt(input,keyBytes);if err!=nil{return 0}
	if !writeNativeLength(outLength,len(value)){return 0}
	return uintptr(allocNativeBytes(value))
}
func lxqjsGoAESEncrypt(data unsafe.Pointer,dataLength uintptr,mode unsafe.Pointer,key unsafe.Pointer,keyLength uintptr,iv unsafe.Pointer,ivLength uintptr,outLength unsafe.Pointer)(result uintptr){
	defer func(){if recover()!=nil{result=0}}()
	if outLength==nil||mode==nil||dataLength>1<<20||keyLength>64||ivLength>32{return 0}
	input,err:=readNativeBytes(data,dataLength,1<<20);if err!=nil{return 0}
	modeText,err:=readNativeString(mode,64);if err!=nil{return 0}
	var keyBytes,ivBytes []byte
	if keyLength>0 { keyBytes,err=readNativeBytes(key,keyLength,64);if err!=nil{return 0} }
	if ivLength>0 { ivBytes,err=readNativeBytes(iv,ivLength,32);if err!=nil{return 0} }
	value,err:=lxCryptoAESEncrypt(input,modeText,keyBytes,ivBytes);if err!=nil{return 0}
	if !writeNativeLength(outLength,len(value)){return 0}
	return uintptr(allocNativeBytes(value))
}
func lxqjsGoZlib(data unsafe.Pointer,length uintptr,inflate uintptr,outLength unsafe.Pointer,errorOut unsafe.Pointer)(result uintptr){
	defer func(){if recover()!=nil{result=0}}()
	if outLength==nil||errorOut==nil||length>lxMaxZlibInputBytes {
		if errorOut!=nil { *(*unsafe.Pointer)(errorOut)=allocNativeString("invalid zlib input or input exceeds limit") }
		return 0
	}
	input,err:=readNativeBytes(data,length,lxMaxZlibInputBytes);if err!=nil{*(*unsafe.Pointer)(errorOut)=allocNativeString(err.Error());return 0}
	var output []byte
	if inflate!=0 { output,err=lxZlibInflate(input) } else { output,err=lxZlibDeflate(input) }
	if err!=nil { *(*unsafe.Pointer)(errorOut)=allocNativeString(err.Error());return 0 }
	if !writeNativeLength(outLength,len(output)){return 0}
	return uintptr(allocNativeBytes(output))
}
