package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
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
	return value, nil
}

// SaveTyped validates and serializes a typed configuration, then persists only
// values that differ from the defaults. Unchanged values remain inherited.
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

	defaults, _, _, err := manager.LoadParts(name)
	if err != nil {
		return err
	}
	overrides := diffObjects(defaults, current)
	if len(overrides) == 0 {
		return manager.Reset(name)
	}
	return manager.Save(name, overrides)
}

// diffObjects returns only values in current that differ from defaults.
// Nested JSON objects are diffed recursively; arrays and scalar values replace as a whole.
func diffObjects(defaults, current map[string]any) map[string]any {
	diff := make(map[string]any)
	for key, currentValue := range current {
		defaultValue, exists := defaults[key]
		if !exists {
			diff[key] = currentValue
			continue
		}

		defaultObject, defaultIsObject := defaultValue.(map[string]any)
		currentObject, currentIsObject := currentValue.(map[string]any)
		if defaultIsObject && currentIsObject {
			nested := diffObjects(defaultObject, currentObject)
			if len(nested) > 0 {
				diff[key] = nested
			}
			continue
		}
		if !reflect.DeepEqual(defaultValue, currentValue) {
			diff[key] = currentValue
		}
	}
	return diff
}
