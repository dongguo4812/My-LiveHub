package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestLoadTypedMigratesAndValidates(t *testing.T) {
	localDir := t.TempDir()
	manager, err := NewManager(fstest.MapFS{
		"app.json": {Data: []byte(`{"config_version":1}`)},
	}, localDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "app.json"), []byte(`{"config_version":0}`), 0o600); err != nil {
		t.Fatal(err)
	}

	migrated := false
	got, err := LoadTyped[AppConfig](manager, "app.json", CurrentAppConfigVersion, map[int]Migration{
		0: func(data map[string]any) error {
			migrated = true
			data["legacy_setting"] = true
			return nil
		},
	}, func(value AppConfig) error { return value.Validate() })
	if err != nil {
		t.Fatal(err)
	}
	if !migrated || got.ConfigVersion != CurrentAppConfigVersion {
		t.Fatalf("migration result = %#v, migrated=%v", got, migrated)
	}
	// Loading/migrating is intentionally non-destructive to the source override.
	raw, err := os.ReadFile(filepath.Join(localDir, "app.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"config_version":0}` {
		t.Fatalf("migration unexpectedly rewrote local config: %s", raw)
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
