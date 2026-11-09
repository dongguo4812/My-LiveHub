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

func newTestTypedManager(t *testing.T, localDir string, defaultJSON string) *Manager {
	t.Helper()
	manager, err := NewManager(fstest.MapFS{
		"app.json": {Data: []byte(defaultJSON)},
	}, localDir)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestLoadTypedCreatesCompleteLocalFileOnFirstLaunch(t *testing.T) {
	localDir := t.TempDir()
	manager := newTestTypedManager(t, localDir, `{"config_version":1,"default_only":"from-default","local_only":"default"}`)

	got, err := LoadTyped[testTypedConfig](manager, "app.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := testTypedConfig{ConfigVersion: 1, DefaultOnly: "from-default", LocalOnly: "default"}
	if got != want {
		t.Fatalf("loaded config = %#v, want %#v", got, want)
	}

	raw, err := os.ReadFile(filepath.Join(localDir, "app.json"))
	if err != nil {
		t.Fatalf("first launch did not create local config: %v", err)
	}
	if string(raw) != "{\n  \"config_version\": 1,\n  \"default_only\": \"from-default\",\n  \"local_only\": \"default\"\n}\n" {
		t.Fatalf("local config is not complete: %s", raw)
	}
}

func TestLoadTypedAddsNewDefaultFieldsAndPreservesLocalValues(t *testing.T) {
	localDir := t.TempDir()
	manager := newTestTypedManager(t, localDir, `{"config_version":1,"default_only":"new-default","local_only":"default"}`)
	localPath := filepath.Join(localDir, "app.json")
	if err := os.WriteFile(localPath, []byte(`{"local_only":"user-value"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadTyped[testTypedConfig](manager, "app.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := testTypedConfig{ConfigVersion: 1, DefaultOnly: "new-default", LocalOnly: "user-value"}
	if got != want {
		t.Fatalf("loaded config = %#v, want %#v", got, want)
	}

	raw, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{\n  \"config_version\": 1,\n  \"default_only\": \"new-default\",\n  \"local_only\": \"user-value\"\n}\n" {
		t.Fatalf("local config was not completed while preserving user values: %s", raw)
	}
}

func TestLoadTypedDoesNotRewriteUnchangedLocalFile(t *testing.T) {
	localDir := t.TempDir()
	manager := newTestTypedManager(t, localDir, `{"config_version":1,"default_only":"from-default","local_only":"default"}`)
	localPath := filepath.Join(localDir, "app.json")
	original := []byte("{\n  \"config_version\": 1,\n  \"default_only\": \"from-default\",\n  \"local_only\": \"user-value\"\n}\n")
	if err := os.WriteFile(localPath, original, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadTyped[testTypedConfig](manager, "app.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.LocalOnly != "user-value" {
		t.Fatalf("local value was not preserved: %#v", got)
	}

	after, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("unchanged local file was rewritten: %s", after)
	}
}

func TestLoadTypedDoesNotPersistInvalidConfiguration(t *testing.T) {
	localDir := t.TempDir()
	manager := newTestTypedManager(t, localDir, `{"config_version":1,"unexpected_setting":true}`)

	if _, err := LoadTyped[AppConfig](manager, "app.json", func(value AppConfig) error {
		return value.Validate()
	}); err == nil {
		t.Fatal("expected unknown configuration field to be rejected")
	}
	if _, err := os.Stat(filepath.Join(localDir, "app.json")); !os.IsNotExist(err) {
		t.Fatalf("invalid configuration was persisted, stat error = %v", err)
	}
}

func TestLoadTypedValidatesBeforePersisting(t *testing.T) {
	localDir := t.TempDir()
	manager := newTestTypedManager(t, localDir, `{"config_version":1}`)

	_, err := LoadTyped[AppConfig](manager, "app.json", func(value AppConfig) error {
		return errors.New("invalid effective configuration")
	})
	if err == nil {
		t.Fatal("expected validation failure")
	}
	if _, err := os.Stat(filepath.Join(localDir, "app.json")); !os.IsNotExist(err) {
		t.Fatalf("invalid configuration was persisted, stat error = %v", err)
	}
}

func TestLoadTypedRejectsMalformedConfiguration(t *testing.T) {
	manager := newTestTypedManager(t, t.TempDir(), `{"config_version":"one"}`)
	if _, err := LoadTyped[AppConfig](manager, "app.json", func(value AppConfig) error {
		return value.Validate()
	}); err == nil {
		t.Fatal("expected type decoding error")
	}
}

type nestedDisplayConfig struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type nestedTypedConfig struct {
	ConfigVersion int                 `json:"config_version"`
	Display       nestedDisplayConfig `json:"display"`
}

func TestSaveTypedWritesCompleteConfiguration(t *testing.T) {
	localDir := t.TempDir()
	manager := newTestTypedManager(t, localDir, `{"config_version":1,"default_only":"from-default","local_only":"default"}`)
	value := testTypedConfig{ConfigVersion: 1, DefaultOnly: "from-default", LocalOnly: "custom"}

	if err := SaveTyped(manager, "app.json", value, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(localDir, "app.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{\n  \"config_version\": 1,\n  \"default_only\": \"from-default\",\n  \"local_only\": \"custom\"\n}\n" {
		t.Fatalf("local config should contain the complete configuration, got: %s", raw)
	}
}

func TestSaveTypedKeepsFieldsMatchingDefaults(t *testing.T) {
	localDir := t.TempDir()
	manager := newTestTypedManager(t, localDir, `{"config_version":1,"display":{"width":100,"height":200}}`)
	value := nestedTypedConfig{ConfigVersion: 1, Display: nestedDisplayConfig{Width: 100, Height: 200}}

	if err := SaveTyped(manager, "app.json", value, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(localDir, "app.json")); err != nil {
		t.Fatalf("complete local config should remain present even when equal to defaults: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(localDir, "app.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{\n  \"config_version\": 1,\n  \"display\": {\n    \"width\": 100,\n    \"height\": 200\n  }\n}\n" {
		t.Fatalf("local config should contain all nested fields, got: %s", raw)
	}
}

func TestSaveTypedValidatesBeforeWriting(t *testing.T) {
	localDir := t.TempDir()
	manager := newTestTypedManager(t, localDir, `{"config_version":1}`)

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
