//go:build !windows

package lxruntime

import (
	"fmt"

	"github.com/ebitengine/purego"
)

type nativeLibrary struct { handle uintptr }
func openNativeLibrary(path string) (*nativeLibrary, error) {
	handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil { return nil, err }
	return &nativeLibrary{handle:handle}, nil
}
func (l *nativeLibrary) bind(target any, symbol string) error {
	address, err := purego.Dlsym(l.handle, symbol)
	if err != nil { return fmt.Errorf("%s: %w", symbol, err) }
	purego.RegisterFunc(target, address)
	return nil
}
