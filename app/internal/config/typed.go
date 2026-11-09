package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Migration upgrades a decoded configuration object from version N to N+1.
// The supplied object is the effective default/local configuration.
type Migration func(map[string]any) error

// LoadTyped loads, migrates, decodes, and validates a configuration.
// Migrations are applied in memory; they are not written to disk automatically.
func LoadTyped[T any](manager *Manager, name string, currentVersion int, migrations map[int]Migration, validate func(T) error) (T, error) {
	var zero T
	if manager == nil {
		return zero, errors.New("configuration manager is required")
	}
	if currentVersion < 1 {
		return zero, fmt.Errorf("current configuration version must be positive")
	}

	data, err := manager.Load(name)
	if err != nil {
		return zero, err
	}
	version, err := readConfigVersion(data)
	if err != nil {
		return zero, fmt.Errorf("configuration %q: %w", name, err)
	}
	if version > currentVersion {
		return zero, fmt.Errorf("configuration %q uses newer version %d (supported: %d)", name, version, currentVersion)
	}

	for version < currentVersion {
		migration, ok := migrations[version]
		if !ok {
			return zero, fmt.Errorf("configuration %q has no migration from version %d to %d", name, version, version+1)
		}
		if err := migration(data); err != nil {
			return zero, fmt.Errorf("migrate configuration %q from version %d: %w", name, version, err)
		}
		version++
		data["config_version"] = json.Number(strconv.Itoa(version))
	}

	encoded, err := json.Marshal(data)
	if err != nil {
		return zero, fmt.Errorf("encode migrated configuration %q: %w", name, err)
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

func readConfigVersion(data map[string]any) (int, error) {
	raw, exists := data["config_version"]
	if !exists {
		return 0, nil
	}
	switch value := raw.(type) {
	case json.Number:
		version, err := value.Int64()
		if err != nil || version < 0 || version > int64(^uint(0)>>1) {
			return 0, fmt.Errorf("config_version must be a non-negative integer")
		}
		return int(version), nil
	case int:
		if value < 0 {
			return 0, errors.New("config_version must be a non-negative integer")
		}
		return value, nil
	default:
		return 0, fmt.Errorf("config_version must be an integer, got %T", raw)
	}
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
