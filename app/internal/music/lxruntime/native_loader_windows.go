//go:build windows

package lxruntime

import (
	"fmt"
	"syscall"

	"github.com/ebitengine/purego"
)

type nativeLibrary struct { handle syscall.Handle }
func openNativeLibrary(path string) (*nativeLibrary, error) {
	handle, err := syscall.LoadLibrary(path)
	if err != nil { return nil, err }
	return &nativeLibrary{handle:handle}, nil
}
func (l *nativeLibrary) bind(target any, symbol string) error {
	address, err := syscall.GetProcAddress(l.handle, symbol)
	if err != nil { return fmt.Errorf("%s: %w", symbol, err) }
	purego.RegisterFunc(target, address)
	return nil
}
