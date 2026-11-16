//go:build windows

package singleinstance

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutexW = kernel32.NewProc("CreateMutexW")
	mutexHandle syscall.Handle
)

const errorAlreadyExists syscall.Errno = 183

// Acquire reserves a per-user-session mutex for this application.
// The handle stays open for the lifetime of the process.
func Acquire() (bool, error) {
	name, err := syscall.UTF16PtrFromString("Local\\My-LiveHub-SingleInstance")
	if err != nil { return false, fmt.Errorf("encode single-instance mutex name: %w", err) }
	handle, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		if callErr != syscall.Errno(0) { return false, fmt.Errorf("create single-instance mutex: %w", callErr) }
		return false, fmt.Errorf("create single-instance mutex failed")
	}
	mutexHandle = syscall.Handle(handle)
	if callErr == errorAlreadyExists { return false, nil }
	return true, nil
}

// Release closes the mutex handle. Windows releases mutex ownership on process exit too.
func Release() {
	if mutexHandle != 0 {
		_ = syscall.CloseHandle(mutexHandle)
		mutexHandle = 0
	}
}
