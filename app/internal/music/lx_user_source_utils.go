package music

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf16"

	"crypto/x509"

	"github.com/dop251/goja"
)

func installLXSourceUtilities(vm *goja.Runtime, lx *goja.Object) error {
	utils := vm.NewObject()
	buffer := vm.NewObject()
	if err := buffer.Set("from", func(call goja.FunctionCall) goja.Value {
		data, err := lxBufferInput(call.Argument(0), call.Argument(1).String())
		if err != nil { panic(vm.ToValue(err.Error())) }
		return newLXBuffer(vm, data)
	}); err != nil { return err }
	if err := buffer.Set("bufToString", func(call goja.FunctionCall) goja.Value {
		data, err := lxBufferBytes(call.Argument(0))
		if err != nil { panic(vm.ToValue(err.Error())) }
		encoding := strings.ToLower(strings.TrimSpace(call.Argument(1).String()))
		return vm.ToValue(decodeLXBufferString(data, encoding))
	}); err != nil { return err }
	if err := utils.Set("buffer", buffer); err != nil { return err }

	crypto := vm.NewObject()
	if err := crypto.Set("md5", func(call goja.FunctionCall) goja.Value {
		sum := md5.Sum([]byte(call.Argument(0).String()))
		return vm.ToValue(hex.EncodeToString(sum[:]))
	}); err != nil { return err }
	if err := crypto.Set("randomBytes", func(call goja.FunctionCall) goja.Value {
		size := call.Argument(0).ToInteger()
		if size < 0 || size > 1<<30 { panic(vm.ToValue("Invalid randomBytes size")) }
		data := make([]byte, int(size))
		if _, err := rand.Read(data); err != nil { panic(vm.ToValue(err.Error())) }
		return newLXBuffer(vm, data)
	}); err != nil { return err }
	if err := crypto.Set("aesEncrypt", func(call goja.FunctionCall) goja.Value {
		data, err := lxBufferBytes(call.Argument(0))
		if err != nil { panic(vm.ToValue(err.Error())) }
		key, err := lxBufferBytes(call.Argument(2))
		if err != nil { panic(vm.ToValue(err.Error())) }
		var iv []byte
		if !goja.IsUndefined(call.Argument(3)) && !goja.IsNull(call.Argument(3)) {
			iv, err = lxBufferBytes(call.Argument(3))
			if err != nil { panic(vm.ToValue(err.Error())) }
		}
		encrypted, err := lxAESEncrypt(data, call.Argument(1).String(), key, iv)
		if err != nil { panic(vm.ToValue(err.Error())) }
		return newLXBuffer(vm, encrypted)
	}); err != nil { return err }
	if err := crypto.Set("rsaEncrypt", func(call goja.FunctionCall) goja.Value {
		data, err := lxBufferBytes(call.Argument(0))
		if err != nil { panic(vm.ToValue(err.Error())) }
		encrypted, err := lxRSAEncryptNoPadding(data, []byte(call.Argument(1).String()))
		if err != nil { panic(vm.ToValue(err.Error())) }
		return newLXBuffer(vm, encrypted)
	}); err != nil { return err }
	if err := utils.Set("crypto", crypto); err != nil { return err }

	zlibUtils := vm.NewObject()
	if err := zlibUtils.Set("inflate", func(call goja.FunctionCall) goja.Value {
		data, err := lxBufferBytes(call.Argument(0))
		if err != nil { panic(vm.ToValue(err.Error())) }
		inflated, err := lxZlibInflate(data)
		if err != nil { panic(vm.ToValue(err.Error())) }
		promise, resolve, _ := vm.NewPromise()
		_ = resolve(newLXBuffer(vm, inflated))
		return vm.ToValue(promise)
	}); err != nil { return err }
	if err := zlibUtils.Set("deflate", func(call goja.FunctionCall) goja.Value {
		data, err := lxBufferBytes(call.Argument(0))
		if err != nil { panic(vm.ToValue(err.Error())) }
		compressed, err := lxZlibDeflate(data)
		if err != nil { panic(vm.ToValue(err.Error())) }
		promise, resolve, _ := vm.NewPromise()
		_ = resolve(newLXBuffer(vm, compressed))
		return vm.ToValue(promise)
	}); err != nil { return err }
	if err := utils.Set("zlib", zlibUtils); err != nil { return err }
	return lx.Set("utils", utils)
}

func newLXBuffer(vm *goja.Runtime, data []byte) *goja.Object {
	data = append([]byte(nil), data...)
	buffer := vm.NewObject()
	_ = buffer.Set("type", "Buffer")
	_ = buffer.Set("length", len(data))
	_ = buffer.Set("__lx_buffer_data", base64.StdEncoding.EncodeToString(data))
	for index, value := range data {
		_ = buffer.Set(strconv.Itoa(index), int(value))
	}
	_ = buffer.Set("toString", func(call goja.FunctionCall) goja.Value {
		encoding := "utf8"
		if !goja.IsUndefined(call.Argument(0)) { encoding = call.Argument(0).String() }
		start, end := int64(0), int64(len(data))
		if !goja.IsUndefined(call.Argument(1)) { start = call.Argument(1).ToInteger() }
		if !goja.IsUndefined(call.Argument(2)) { end = call.Argument(2).ToInteger() }
		start, end = normalizeLXBufferRange(start, end, len(data))
		return vm.ToValue(decodeLXBufferString(data[start:end], encoding))
	})
	makeSlice := func(call goja.FunctionCall) goja.Value {
		start, end := int64(0), int64(len(data))
		if !goja.IsUndefined(call.Argument(0)) { start = call.Argument(0).ToInteger() }
		if !goja.IsUndefined(call.Argument(1)) { end = call.Argument(1).ToInteger() }
		start, end = normalizeLXBufferRange(start, end, len(data))
		return vm.ToValue(newLXBuffer(vm, data[start:end]))
	}
	_ = buffer.Set("slice", makeSlice)
	_ = buffer.Set("subarray", makeSlice)
	_ = buffer.Set("equals", func(call goja.FunctionCall) goja.Value {
		other, err := lxBufferBytes(call.Argument(0))
		return vm.ToValue(err == nil && bytes.Equal(data, other))
	})
	_ = buffer.Set("indexOf", func(call goja.FunctionCall) goja.Value {
		needle, err := lxBufferInput(call.Argument(0), "utf8")
		if err != nil { return vm.ToValue(int64(-1)) }
		offset := int64(0)
		if !goja.IsUndefined(call.Argument(1)) { offset = call.Argument(1).ToInteger() }
		if offset < 0 { offset = int64(len(data)) + offset }
		if offset < 0 { offset = 0 }
		if offset > int64(len(data)) { return vm.ToValue(int64(-1)) }
		index := bytes.Index(data[offset:], needle)
		if index < 0 { return vm.ToValue(int64(-1)) }
		return vm.ToValue(int64(index) + offset)
	})
	_ = buffer.Set("includes", func(call goja.FunctionCall) goja.Value {
		needle, err := lxBufferInput(call.Argument(0), "utf8")
		if err != nil { return vm.ToValue(false) }
		return vm.ToValue(bytes.Contains(data, needle))
	})
	_ = buffer.Set("toJSON", func(goja.FunctionCall) goja.Value {
		values := make([]int, len(data))
		for index, value := range data { values[index] = int(value) }
		return vm.ToValue(map[string]any{"type": "Buffer", "data": values})
	})
	return buffer
}

func normalizeLXBufferRange(start, end int64, length int) (int, int) {
	size := int64(length)
	if start < 0 { start += size }
	if end < 0 { end += size }
	if start < 0 { start = 0 }
	if start > size { start = size }
	if end < start { end = start }
	if end > size { end = size }
	return int(start), int(end)
}

func decodeLXBufferString(data []byte, encoding string) string {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		return base64.StdEncoding.EncodeToString(data)
	case "base64url", "base64-url":
		return base64.RawURLEncoding.EncodeToString(data)
	case "hex":
		return hex.EncodeToString(data)
	case "ascii":
		runes := make([]rune, len(data))
		for i, value := range data { runes[i] = rune(value & 0x7f) }
		return string(runes)
	case "latin1", "binary":
		runes := make([]rune, len(data))
		for i, value := range data { runes[i] = rune(value) }
		return string(runes)
	case "utf16le", "utf-16le", "ucs2", "ucs-2":
		units := make([]uint16, len(data)/2)
		for i := range units { units[i] = uint16(data[i*2]) | uint16(data[i*2+1])<<8 }
		return string(utf16.Decode(units))
	default:
		return string(data)
	}
}

func lxBufferInput(value goja.Value, encoding string) ([]byte, error) {
	if goja.IsUndefined(value) || goja.IsNull(value) { return nil, errors.New("LX buffer input is empty") }
	if text, ok := value.Export().(string); ok {
		switch strings.ToLower(strings.TrimSpace(encoding)) {
		case "base64":
			data, err := base64.StdEncoding.DecodeString(text)
			if err != nil { return nil, fmt.Errorf("invalid base64 input: %w", err) }
			return data, nil
		case "base64url", "base64-url":
			data, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(text, "="))
			if err != nil { return nil, fmt.Errorf("invalid base64url input: %w", err) }
			return data, nil
		case "hex":
			data, err := hex.DecodeString(text)
			if err != nil { return nil, fmt.Errorf("invalid hex input: %w", err) }
			return data, nil
		case "ascii":
			data := []byte(text)
			for i := range data { data[i] &= 0x7f }
			return data, nil
		case "latin1", "binary":
			data := make([]byte, 0, len(text))
			for _, value := range text { data = append(data, byte(value)) }
			return data, nil
		case "utf16le", "utf-16le", "ucs2", "ucs-2":
			units := utf16.Encode([]rune(text))
			data := make([]byte, len(units)*2)
			for i, value := range units { data[i*2], data[i*2+1] = byte(value), byte(value>>8) }
			return data, nil
		default:
			return []byte(text), nil
		}
	}
	return lxBufferBytes(value)
}

func lxBufferBytes(value goja.Value) ([]byte, error) {
	if goja.IsUndefined(value) || goja.IsNull(value) { return nil, errors.New("LX buffer value is empty") }
	if text, ok := value.Export().(string); ok { return []byte(text), nil }
	if object, ok := value.(*goja.Object); ok {
		if encoded := object.Get("__lx_buffer_data"); encoded != nil && !goja.IsUndefined(encoded) {
			data, err := base64.StdEncoding.DecodeString(encoded.String())
			if err != nil { return nil, fmt.Errorf("invalid LX buffer: %w", err) }
			return data, nil
		}
	}
	switch data := value.Export().(type) {
	case []byte:
		return append([]byte(nil), data...), nil
	case []interface{}:
		result := make([]byte, len(data))
		for i, item := range data {
			switch number := item.(type) {
			case int:
				result[i] = byte(number)
			case int64:
				result[i] = byte(number)
			case float64:
				result[i] = byte(int64(number))
			case uint8:
				result[i] = number
			default:
				return nil, fmt.Errorf("unsupported LX buffer byte %T", item)
			}
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported LX buffer value %T", data)
	}
}


func lxAESEncrypt(data []byte, mode string, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil { return nil, fmt.Errorf("LX AES key invalid: %w", err) }
	mode = strings.ToLower(strings.TrimSpace(mode))
	blockSize := block.BlockSize()
	switch {
	case strings.HasSuffix(mode, "-ecb") || mode == "ecb":
		padded := lxPKCS7Pad(data, blockSize)
		result := make([]byte, len(padded))
		for offset := 0; offset < len(padded); offset += blockSize {
			block.Encrypt(result[offset:offset+blockSize], padded[offset:offset+blockSize])
		}
		return result, nil
	case strings.HasSuffix(mode, "-cbc") || mode == "cbc":
		if len(iv) != blockSize { return nil, fmt.Errorf("LX AES-CBC IV must be %d bytes", blockSize) }
		padded := lxPKCS7Pad(data, blockSize)
		result := make([]byte, len(padded))
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(result, padded)
		return result, nil
	case strings.HasSuffix(mode, "-ctr") || mode == "ctr":
		if len(iv) != blockSize { return nil, fmt.Errorf("LX AES-CTR IV must be %d bytes", blockSize) }
		result := make([]byte, len(data))
		cipher.NewCTR(block, iv).XORKeyStream(result, data)
		return result, nil
	case strings.HasSuffix(mode, "-cfb") || mode == "cfb":
		if len(iv) != blockSize { return nil, fmt.Errorf("LX AES-CFB IV must be %d bytes", blockSize) }
		result := make([]byte, len(data))
		cipher.NewCFBEncrypter(block, iv).XORKeyStream(result, data)
		return result, nil
	case strings.HasSuffix(mode, "-ofb") || mode == "ofb":
		if len(iv) != blockSize { return nil, fmt.Errorf("LX AES-OFB IV must be %d bytes", blockSize) }
		result := make([]byte, len(data))
		cipher.NewOFB(block, iv).XORKeyStream(result, data)
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported LX AES mode %q", mode)
	}
}

func lxPKCS7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	result := make([]byte, len(data)+padding)
	copy(result, data)
	for i := len(data); i < len(result); i++ { result[i] = byte(padding) }
	return result
}

func lxRSAEncryptNoPadding(data, keyPEM []byte) ([]byte, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil { return nil, errors.New("LX RSA public key PEM is invalid") }
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		var publicKey *rsa.PublicKey
		publicKey, err = x509.ParsePKCS1PublicKey(block.Bytes)
		if err == nil { parsed = publicKey }
	}
	if err != nil { return nil, fmt.Errorf("LX RSA public key is invalid: %w", err) }
	publicKey, ok := parsed.(*rsa.PublicKey)
	if !ok { return nil, errors.New("LX RSA key is not an RSA public key") }
	size := publicKey.Size()
	if len(data) > size { return nil, fmt.Errorf("LX RSA_NO_PADDING input exceeds %d bytes: got %d", size, len(data)) }
	// LX Music's preload pads short RSA_NO_PADDING inputs on the left with
	// zero bytes before calling Node's publicEncrypt().
	padded := make([]byte, size)
	copy(padded[size-len(data):], data)
	message := new(big.Int).SetBytes(padded)
	if message.Cmp(publicKey.N) >= 0 { return nil, errors.New("LX RSA input is greater than or equal to the modulus") }
	encrypted := new(big.Int).Exp(message, big.NewInt(int64(publicKey.E)), publicKey.N)
	return encrypted.FillBytes(make([]byte, size)), nil
}

func lxZlibInflate(data []byte) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil { return nil, fmt.Errorf("LX zlib inflate failed: %w", err) }
	defer reader.Close()
	result, err := io.ReadAll(reader)
	if err != nil { return nil, fmt.Errorf("LX zlib inflate failed: %w", err) }
	return result, nil
}

func lxZlibDeflate(data []byte) ([]byte, error) {
	var output bytes.Buffer
	writer := zlib.NewWriter(&output)
	if _, err := writer.Write(data); err != nil { return nil, err }
	if err := writer.Close(); err != nil { return nil, err }
	return output.Bytes(), nil
}
