//go:build !windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
)

func writeAtomically(destination string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(destination), ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary config file: %w", err)
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("write temporary config file: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync temporary config file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary config file: %w", err)
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return fmt.Errorf("replace config file: %w", err)
	}
	return nil
}
