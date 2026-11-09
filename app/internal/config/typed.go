package config

import (
	"encoding/json"
	"errors"
	"fmt"
)

// LoadTyped loads a configuration, decodes it into a typed value, and validates it.
// Local values override embedded defaults; loading never modifies configuration files.
func LoadTyped[T any](manager *Manager, name string, validate func(T) error) (T, error) {
	var zero T
	if manager == nil {
		return zero, errors.New("configuration manager is required")
	}

	merged, err := manager.Load(name)
	if err != nil {
		return zero, err
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return zero, fmt.Errorf("encode configuration %q: %w", name, err)
	}

	var value T
	if err := json.Unmarshal(encoded, &value); err != nil {
		return zero, fmt.Errorf("decode configuration %q: %w", name, err)
	}
	if validate != nil {
		if err := validate(value); err != nil {
			return zero, fmt.Errorf("validate configuration %q: %w", name, err)
		}
	}
	return value, nil
}

// SaveTyped validates and serializes a typed configuration before writing it.
func SaveTyped[T any](manager *Manager, name string, value T, validate func(T) error) error {
	if manager == nil {
		return errors.New("configuration manager is required")
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
	object, err := decodeObject(encoded)
	if err != nil {
		return fmt.Errorf("configuration %q must encode as an object: %w", name, err)
	}
	return manager.Save(name, object)
}
