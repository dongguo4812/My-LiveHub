package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

func mergeObjects(defaults, overrides map[string]any) map[string]any {
	merged := make(map[string]any, len(defaults)+len(overrides))
	for key, value := range defaults {
		merged[key] = value
	}
	for key, override := range overrides {
		if defaultObject, ok := merged[key].(map[string]any); ok {
			if overrideObject, ok := override.(map[string]any); ok {
				merged[key] = mergeObjects(defaultObject, overrideObject)
				continue
			}
		}
		merged[key] = override
	}
	return merged
}

func validateJSONName(name string) error {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || !strings.HasSuffix(strings.ToLower(name), ".json") {
		return fmt.Errorf("configuration name %q must be a simple .json file name", name)
	}
	return nil
}
