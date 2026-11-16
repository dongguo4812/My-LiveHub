//go:build !windows

package singleinstance

// Acquire is a no-op on platforms where this Windows desktop guard is not needed.
func Acquire() (bool, error) { return true, nil }

// Release is a no-op on non-Windows platforms.
func Release() {}
