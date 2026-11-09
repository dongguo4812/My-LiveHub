package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// LoadTyped loads, merges, decodes, and validates a configuration. After the
// effective configuration passes validation, it persists the complete result
// to the local file so newly introduced default fields are added immediately.
func LoadTyped[T any](manager *Manager, name string, validate func(T) error) (T, error) {
	var zero T
	if manager == nil {
		return zero, errors.New("configuration manager is required")
	}

	defaults, local, _, err := manager.LoadParts(name)
	if err != nil {
		return zero, err
	}
	merged := mergeObjects(defaults, local)
	encoded, err := json.Marshal(merged)
	if err != nil {
		return zero, fmt.Errorf("encode configuration %q: %w", name, err)
	}

	var value T
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return zero, fmt.Errorf("decode configuration %q: %w", name, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return zero, fmt.Errorf("decode configuration %q: unexpected trailing JSON data", name)
		}
		return zero, fmt.Errorf("decode configuration %q: %w", name, err)
	}
	if validate != nil {
		if err := validate(value); err != nil {
			return zero, fmt.Errorf("validate configuration %q: %w", name, err)
		}
	}

	// Persist only after decoding and validation succeed. This creates the local
	// file on first launch and fills newly added default fields on later launches.
	if err := manager.Save(name, merged); err != nil {
		return zero, fmt.Errorf("persist configuration %q: %w", name, err)
	}
	return value, nil
}

// SaveTyped validates and writes the complete typed configuration to local.
func SaveTyped[T any](manager *Manager, name string, value T, validate func(T) error) error {
	if manager == nil {
		return errors.New("configuration manager is required")
	}
	if err := validateJSONName(name); err != nil {
		return err
	}
	if validate != nil {
		if err := validate(value); err != nil {
			return fmt.Errorf("validate configuration %q: %w", name, err)
		}
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode configuration %q: %w", name, err)
	}
	current, err := decodeObject(encoded)
	if err != nil {
		return fmt.Errorf("configuration %q must encode as an object: %w", name, err)
	}
	return manager.Save(name, current)
}
