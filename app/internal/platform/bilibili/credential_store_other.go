//go:build !windows

package bilibili

import "errors"

func protect([]byte) ([]byte, error) { return nil, errors.New("Bilibili credential storage is supported only on Windows") }
func unprotect([]byte) ([]byte, error) { return nil, errors.New("Bilibili credential storage is supported only on Windows") }
