package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestLoadTypedMigratesLocalOverrideAndPersistsAfterValidation(t *testing.T) {
	localDir := t.TempDir()
	manager, err := NewManager(fstest.MapFS{
		"app.json": {Data: []byte(`{"config_version":1,"default_only":"keep"}`)},
	}, localDir)
	if err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(localDir, "app.json")
	if err := os.WriteFile(localPath, []byte(`{"config_version":0,"legacy_setting":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	migrated := false
	got, err := LoadTyped[map[string]any](manager, "app.json", 1, map[int]Migration{
		0: func(data map[string]any) error {
			migrated = true
			data["renamed_setting"] = data["legacy_setting"]
			delete(data, "legacy_setting")
			return nil
		},
	}, func(value map[string]any) error {
		if value["default_only"] != "keep" || value["renamed_setting"] != true {
			t.Fatalf("unexpected merged config: %#v", value)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !migrated || got["default_only"] != "keep" || got["renamed_setting"] != true {
		t.Fatalf("migration result = %#v, migrated=%v", got, migrated)
	}
	// Only the local override is persisted; defaults remain inherited.
	raw, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" {
		t.Fatal("migrated local override was not persisted")
	}
	reloaded, err := manager.Load("app.json")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded["default_only"] != "keep" || reloaded["renamed_setting"] != true {
		t.Fatalf("reloaded merged config = %#v", reloaded)
	}
}

func TestLoadTypedDoesNotPersistWhenValidationFails(t *testing.T) {
	localDir := t.TempDir()
	manager, err := NewManager(fstest.MapFS{"app.json": {Data: []byte(`{"config_version":1}`)}}, localDir)
	if err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(localDir, "app.json")
	original := `{"config_version":0,"bad":true}`
	if err := os.WriteFile(localPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = LoadTyped[map[string]any](manager, "app.json", 1, map[int]Migration{
		0: func(data map[string]any) error { data["migrated"] = true; return nil },
	}, func(map[string]any) error { return errors.New("invalid effective configuration") })
	if err == nil {
		t.Fatal("expected validation failure")
	}
	raw, readErr := os.ReadFile(localPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(raw) != original {
		t.Fatalf("failed migration changed source config: %s", raw)
	}
}

func TestLoadTypedRejectsUnsupportedVersionsAndMigrationFailures(t *testing.T) {
	tests := []struct {
		name string
		data string
		migrations map[int]Migration
	}{
		{name: "future version", data: `{"config_version":2}`},
		{name: "missing migration", data: `{"config_version":0}`},
		{name: "migration failure", data: `{"config_version":0}`, migrations: map[int]Migration{
			0: func(map[string]any) error { return errors.New("broken migration") },
		}},
		{name: "invalid version type", data: `{"config_version":"one"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, err := NewManager(fstest.MapFS{"app.json": {Data: []byte(tt.data)}}, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			_, err = LoadTyped[AppConfig](manager, "app.json", 1, tt.migrations, nil)
			if err == nil {
				t.Fatal("expected configuration loading error")
			}
		})
	}
}

func TestSaveTypedValidatesBeforeWriting(t *testing.T) {
	localDir := t.TempDir()
	manager, err := NewManager(fstest.MapFS{"app.json": {Data: []byte(`{"config_version":1}`)}}, localDir)
	if err != nil {
		t.Fatal(err)
	}
	invalid := AppConfig{ConfigVersion: 2}
	if err := SaveTyped(manager, "app.json", invalid, func(value AppConfig) error {
		return value.Validate()
	}); err == nil {
		t.Fatal("expected invalid config to be rejected")
	}
	if _, err := os.Stat(filepath.Join(localDir, "app.json")); !os.IsNotExist(err) {
		t.Fatalf("invalid config was written, stat error = %v", err)
	}

	valid := AppConfig{ConfigVersion: CurrentAppConfigVersion}
	if err := SaveTyped(manager, "app.json", valid, func(value AppConfig) error {
		return value.Validate()
	}); err != nil {
		t.Fatal(err)
	}
}
