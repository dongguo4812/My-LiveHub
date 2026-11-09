package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Migration upgrades a decoded configuration object from version N to N+1.
type Migration func(map[string]any) error

// LoadTyped loads, migrates, decodes, and validates a configuration.
// Existing local overrides are migrated and atomically saved only after the
// fully merged configuration passes decoding and validation. Embedded defaults
// are migrated in memory and are never written to the user's local override.
func LoadTyped[T any](manager *Manager, name string, currentVersion int, migrations map[int]Migration, validate func(T) error) (T, error) {
	var zero T
	if manager == nil {
		return zero, errors.New("configuration manager is required")
	}
	if currentVersion < 1 {
		return zero, fmt.Errorf("current configuration version must be positive")
	}

	defaults, local, localExists, err := manager.LoadParts(name)
	if err != nil {
		return zero, err
	}

	migrationTarget := defaults
	if localExists {
		migrationTarget = local
	}
	version, err := readConfigVersion(migrationTarget)
	if err != nil {
		return zero, fmt.Errorf("configuration %q: %w", name, err)
	}
	if version > currentVersion {
		return zero, fmt.Errorf("configuration %q uses newer version %d (supported: %d)", name, version, currentVersion)
	}
	migrated := false
	for version < currentVersion {
		migration, ok := migrations[version]
		if !ok {
			return zero, fmt.Errorf("configuration %q has no migration from version %d to %d", name, version, version+1)
		}
		if err := migration(migrationTarget); err != nil {
			return zero, fmt.Errorf("migrate configuration %q from version %d: %w", name, version, err)
		}
		version++
		migrationTarget["config_version"] = json.Number(strconv.Itoa(version))
		migrated = true
	}

	effective := defaults
	if localExists {
		effective = mergeObjects(defaults, local)
	}
	encoded, err := json.Marshal(effective)
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

	if localExists && migrated {
		if err := manager.Save(name, local); err != nil {
			return zero, fmt.Errorf("persist migrated configuration %q: %w", name, err)
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
