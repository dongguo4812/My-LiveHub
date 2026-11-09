package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"
)

func TestResolveEnvironment(t *testing.T) {
	if got := ResolveEnvironment([]string{"dev"}); got != EnvironmentDevelopment {
		t.Fatalf("ResolveEnvironment(dev) = %q", got)
	}
	if got := ResolveEnvironment(nil); got != EnvironmentProduction {
		t.Fatalf("ResolveEnvironment(nil) = %q", got)
	}
	if got := ResolveEnvironment([]string{"--some-flag"}); got != EnvironmentProduction {
		t.Fatalf("ResolveEnvironment(flag) = %q", got)
	}
}

func TestResolvePaths(t *testing.T) {
	got, err := ResolvePaths(EnvironmentDevelopment, filepath.Join("work", "app"), "ignored")
	if err != nil {
		t.Fatal(err)
	}
	want := Paths{
		DefaultDir: filepath.Join("work", "app", "config", "default"),
		LocalDir: filepath.Join("work", "app", "config", "local"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("development paths = %#v, want %#v", got, want)
	}

	got, err = ResolvePaths(EnvironmentProduction, "", filepath.Join("user", "AppData", "Roaming"))
	if err != nil {
		t.Fatal(err)
	}
	want = Paths{LocalDir: filepath.Join("user", "AppData", "Roaming", "My-LiveHub", "config", "local")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("production paths = %#v, want %#v", got, want)
	}
}

func TestLoadMergesLocalOverrides(t *testing.T) {
	localDir := t.TempDir()
	manager, err := NewManager(fstest.MapFS{
		"app.json": {Data: []byte(`{"name":"default","nested":{"a":1,"b":2},"items":[1,2]}`)},
	}, localDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "app.json"), []byte(`{"nested":{"b":9},"items":[3]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := manager.Load("app.json")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"name": "default",
		"nested": map[string]any{"a": json.Number("1"), "b": json.Number("9")},
		"items": []any{json.Number("3")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged config = %#v, want %#v", got, want)
	}
}

func TestSaveAndReset(t *testing.T) {
	localDir := t.TempDir()
	manager, err := NewManager(fstest.MapFS{"app.json": {Data: []byte(`{"enabled":true}`)}}, localDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Save("app.json", map[string]any{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	got, err := manager.Load("app.json")
	if err != nil {
		t.Fatal(err)
	}
	if got["enabled"] != false {
		t.Fatalf("saved override not loaded: %#v", got)
	}
	if err := manager.Reset("app.json"); err != nil {
		t.Fatal(err)
	}
	got, err = manager.Load("app.json")
	if err != nil {
		t.Fatal(err)
	}
	if got["enabled"] != true {
		t.Fatalf("default not restored: %#v", got)
	}
}

func TestRejectsPathTraversal(t *testing.T) {
	manager, err := NewManager(fstest.MapFS{"app.json": {Data: []byte(`{}`)}}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Load("../app.json"); err == nil {
		t.Fatal("expected invalid filename error")
	}
}

func TestValidateRequiresObject(t *testing.T) {
	manager, err := NewManager(fstest.MapFS{"app.json": {Data: []byte(`{}`)}}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{`[]`, `null`, `{"a":1} {"b":2}`, `invalid`} {
		if err := manager.Validate([]byte(input)); err == nil {
			t.Errorf("Validate(%q) expected error", input)
		}
	}
}
