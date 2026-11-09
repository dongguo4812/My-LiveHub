package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

type testTypedConfig struct {
	ConfigVersion int    `json:"config_version"`
	DefaultOnly   string `json:"default_only"`
	LocalOnly     string `json:"local_only"`
}

func TestLoadTypedMergesDefaultsAndLocalOverrides(t *testing.T) {
	localDir := t.TempDir()
	manager, err := NewManager(fstest.MapFS{
		"app.json": {Data: []byte(`{"config_version":1,"default_only":"from-default","local_only":"default"}`)},
	}, localDir)
	if err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(localDir, "app.json")
	if err := os.WriteFile(localPath, []byte(`{"local_only":"from-local"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadTyped[testTypedConfig](manager, "app.json", func(value testTypedConfig) error {
		if value.ConfigVersion != 1 {
			t.Fatalf("config version = %d, want 1", value.ConfigVersion)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultOnly != "from-default" || got.LocalOnly != "from-local" {
		t.Fatalf("unexpected merged config: %#v", got)
	}

	// Loading is read-only and must not rewrite the user's local override.
	raw, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"local_only":"from-local"}` {
		t.Fatalf("loading changed local config: %s", raw)
	}
}

func TestLoadTypedRejectsUnknownFields(t *testing.T) {
	manager, err := NewManager(fstest.MapFS{
		"app.json": {Data: []byte(`{"config_version":1,"unexpected_setting":true}`)},
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTyped[AppConfig](manager, "app.json", func(value AppConfig) error {
		return value.Validate()
	}); err == nil {
		t.Fatal("expected unknown configuration field to be rejected")
	}
}

func TestLoadTypedValidatesEffectiveConfiguration(t *testing.T) {
	manager, err := NewManager(fstest.MapFS{
		"app.json": {Data: []byte(`{"config_version":1}`)},
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	_, err = LoadTyped[AppConfig](manager, "app.json", func(value AppConfig) error {
		return errors.New("invalid effective configuration")
	})
	if err == nil {
		t.Fatal("expected validation failure")
	}
}

func TestLoadTypedRejectsMalformedConfiguration(t *testing.T) {
	manager, err := NewManager(fstest.MapFS{
		"app.json": {Data: []byte(`{"config_version":"one"}`)},
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTyped[AppConfig](manager, "app.json", func(value AppConfig) error {
		return value.Validate()
	}); err == nil {
		t.Fatal("expected type decoding error")
	}
}

func TestSaveTypedWritesOnlyChangedFields(t *testing.T) {
	localDir := t.TempDir()
	manager, err := NewManager(fstest.MapFS{
		"app.json": {Data: []byte(`{"config_version":1,"default_only":"from-default","local_only":"default"}`)},
	}, localDir)
	if err != nil {
		t.Fatal(err)
	}

	value := testTypedConfig{
		ConfigVersion: 1,
		DefaultOnly:   "from-default",
		LocalOnly:     "custom",
	}
	if err := SaveTyped(manager, "app.json", value, nil); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(localDir, "app.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{\n  \"local_only\": \"custom\"\n}\n" {
		t.Fatalf("local config should contain only changed fields, got: %s", raw)
	}

	effective, err := LoadTyped[testTypedConfig](manager, "app.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if effective != value {
		t.Fatalf("effective config = %#v, want %#v", effective, value)
	}
}

type nestedDisplayConfig struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type nestedTypedConfig struct {
	ConfigVersion int                  `json:"config_version"`
	Display       nestedDisplayConfig `json:"display"`
}

func TestSaveTypedWritesOnlyChangedNestedFields(t *testing.T) {
	localDir := t.TempDir()
	manager, err := NewManager(fstest.MapFS{
		"app.json": {Data: []byte(`{"config_version":1,"display":{"width":100,"height":200}}`)},
	}, localDir)
	if err != nil {
		t.Fatal(err)
	}

	value := nestedTypedConfig{ConfigVersion: 1, Display: nestedDisplayConfig{Width: 120, Height: 200}}
	if err := SaveTyped(manager, "app.json", value, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(localDir, "app.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{\n  \"display\": {\n    \"width\": 120\n  }\n}\n" {
		t.Fatalf("nested local config should contain only changed fields, got: %s", raw)
	}
}

func TestSaveTypedResetsLocalFileWhenValuesMatchDefaults(t *testing.T) {
	localDir := t.TempDir()
	manager, err := NewManager(fstest.MapFS{
		"app.json": {Data: []byte(`{"config_version":1,"default_only":"from-default","local_only":"default"}`)},
	}, localDir)
	if err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(localDir, "app.json")
	if err := os.WriteFile(localPath, []byte(`{"local_only":"custom"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	value := testTypedConfig{ConfigVersion: 1, DefaultOnly: "from-default", LocalOnly: "default"}
	if err := SaveTyped(manager, "app.json", value, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Fatalf("local override should be removed when no values differ, stat error = %v", err)
	}
}

func TestSaveTypedValidatesBeforeWriting(t *testing.T) {
	localDir := t.TempDir()
	manager, err := NewManager(fstest.MapFS{
		"app.json": {Data: []byte(`{"config_version":1}`)},
	}, localDir)
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
