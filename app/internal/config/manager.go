package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

type Manager struct {
	defaults fs.FS
	localDir string
}

func NewManager(defaults fs.FS, localDir string) (*Manager, error) {
	if defaults == nil {
		return nil, errors.New("default configuration filesystem is required")
	}
	if localDir == "" {
		return nil, errors.New("local configuration directory is required")
	}
	return &Manager{defaults: defaults, localDir: localDir}, nil
}

// Load reads a default JSON file and recursively overlays its local counterpart.
// Objects merge recursively; arrays and scalar values are replaced as a whole.
func (m *Manager) Load(name string) (map[string]any, error) {
	if err := validateJSONName(name); err != nil {
		return nil, err
	}
	defaultData, err := fs.ReadFile(m.defaults, name)
	if err != nil {
		return nil, fmt.Errorf("read default config %q: %w", name, err)
	}
	defaults, err := decodeObject(defaultData)
	if err != nil {
		return nil, fmt.Errorf("parse default config %q: %w", name, err)
	}

	localPath := filepath.Join(m.localDir, name)
	localData, err := os.ReadFile(localPath)
	if errors.Is(err, os.ErrNotExist) {
		return defaults, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read local config %q: %w", localPath, err)
	}
	overrides, err := decodeObject(localData)
	if err != nil {
		return nil, fmt.Errorf("parse local config %q: %w", localPath, err)
	}
	return mergeObjects(defaults, overrides), nil
}

// Save validates and writes a complete local override file.
func (m *Manager) Save(name string, value map[string]any) error {
	if err := validateJSONName(name); err != nil {
		return err
	}
	if value == nil {
		return fmt.Errorf("configuration %q cannot be nil", name)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config %q: %w", name, err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(m.localDir, 0o700); err != nil {
		return fmt.Errorf("create local config directory: %w", err)
	}
	return writeAtomically(filepath.Join(m.localDir, name), data)
}

// Reset removes the local override so the default values take effect again.
func (m *Manager) Reset(name string) error {
	if err := validateJSONName(name); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(m.localDir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reset local config %q: %w", name, err)
	}
	return nil
}

// Validate checks JSON syntax and requires a top-level JSON object.
func (m *Manager) Validate(data []byte) error {
	_, err := decodeObject(data)
	return err
}

// Reload loads the latest values from disk; Manager deliberately keeps no cache.
func (m *Manager) Reload(name string) (map[string]any, error) {
	return m.Load(name)
}

func decodeObject(data []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, errors.New("top-level JSON value must be an object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("unexpected trailing JSON data")
		}
		return nil, err
	}
	return value, nil
}
