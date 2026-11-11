package music

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

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
		switch encoding {
		case "base64":
			return vm.ToValue(base64.StdEncoding.EncodeToString(data))
		case "hex":
			return vm.ToValue(hex.EncodeToString(data))
		case "utf8", "utf-8", "", "ascii", "latin1", "binary":
			return vm.ToValue(string(data))
		default:
			return vm.ToValue(string(data))
		}
	}); err != nil { return err }
	if err := utils.Set("buffer", buffer); err != nil { return err }

	crypto := vm.NewObject()
	if err := crypto.Set("md5", func(call goja.FunctionCall) goja.Value {
		sum := md5.Sum([]byte(call.Argument(0).String()))
		return vm.ToValue(hex.EncodeToString(sum[:]))
	}); err != nil { return err }
	if err := crypto.Set("randomBytes", func(call goja.FunctionCall) goja.Value {
		size := call.Argument(0).ToInteger()
		if size < 0 || size > 1<<20 { panic(vm.ToValue("LX randomBytes size must be between 0 and 1048576")) }
		data := make([]byte, int(size))
		if _, err := rand.Read(data); err != nil { panic(vm.ToValue(err.Error())) }
		return newLXBuffer(vm, data)
	}); err != nil { return err }
	if err := utils.Set("crypto", crypto); err != nil { return err }
	return lx.Set("utils", utils)
}

func newLXBuffer(vm *goja.Runtime, data []byte) *goja.Object {
	buffer := vm.NewObject()
	_ = buffer.Set("type", "Buffer")
	_ = buffer.Set("length", len(data))
	_ = buffer.Set("__lx_buffer_data", base64.StdEncoding.EncodeToString(data))
	return buffer
}

func lxBufferInput(value goja.Value, encoding string) ([]byte, error) {
	if goja.IsUndefined(value) || goja.IsNull(value) { return nil, errors.New("LX buffer input is empty") }
	if text, ok := value.Export().(string); ok {
		switch strings.ToLower(strings.TrimSpace(encoding)) {
		case "base64":
			data, err := base64.StdEncoding.DecodeString(text)
			if err != nil { return nil, fmt.Errorf("invalid base64 input: %w", err) }
			return data, nil
		case "hex":
			data, err := hex.DecodeString(text)
			if err != nil { return nil, fmt.Errorf("invalid hex input: %w", err) }
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
		for i, item := range data { result[i] = byte(goja.ToValue(item).ToInteger()) }
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported LX buffer value %T", data)
	}
}
