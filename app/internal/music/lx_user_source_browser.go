package music

import (
	"encoding/base64"
	"errors"
	"strings"
	"unicode/utf16"

	"github.com/dop251/goja"
)

// installLXBrowserGlobals adds small, standards-based browser primitives that
// are available in LX's Chromium renderer but are not part of Goja itself.
// It deliberately does not pretend to provide a DOM or a full Fetch stack.
func installLXBrowserGlobals(vm *goja.Runtime, loop *lxRuntimeEventLoop) {
	_ = vm.Set("atob", func(call goja.FunctionCall) goja.Value {
		encoded := strings.Map(func(value rune) rune {
			switch value {
			case '\t', '\n', '\f', '\r', ' ':
				return -1
			default:
				return value
			}
		}, call.Argument(0).String())
		var decoded []byte
		var err error
		if strings.Contains(encoded, "=") {
			decoded, err = base64.StdEncoding.DecodeString(encoded)
		} else {
			decoded, err = base64.RawStdEncoding.DecodeString(encoded)
		}
		if err != nil {
			panic(vm.NewGoError(errors.New("InvalidCharacterError: invalid base64 string")))
		}
		runes := make([]rune, len(decoded))
		for index, value := range decoded { runes[index] = rune(value) }
		return vm.ToValue(string(runes))
	})

	_ = vm.Set("btoa", func(call goja.FunctionCall) goja.Value {
		units := utf16.Encode([]rune(call.Argument(0).String()))
		encoded := make([]byte, len(units))
		for index, value := range units {
			if value > 0xff {
				panic(vm.NewGoError(errors.New("InvalidCharacterError: the string contains characters outside the Latin1 range")))
			}
			encoded[index] = byte(value)
		}
		return vm.ToValue(base64.StdEncoding.EncodeToString(encoded))
	})

	_ = vm.Set("queueMicrotask", func(call goja.FunctionCall) goja.Value {
		callback := call.Argument(0)
		if _, ok := goja.AssertFunction(callback); !ok {
			panic(vm.NewGoError(errors.New("TypeError: queueMicrotask callback must be a function")))
		}
		promise, resolve, _ := vm.NewPromise()
		_ = resolve(goja.Undefined())
		promiseValue := vm.ToValue(promise)
		then, ok := goja.AssertFunction(promiseValue.ToObject(vm).Get("then"))
		if !ok { return goja.Undefined() }
		onRejected := vm.ToValue(func(call goja.FunctionCall) goja.Value {
			loop.recordAsyncError(errors.New(lxUserAPIErrorMessage(vm, call.Argument(0))))
			return goja.Undefined()
		})
		if _, err := then(promiseValue, callback, onRejected); err != nil {
			loop.recordAsyncError(err)
		}
		return goja.Undefined()
	})
}
