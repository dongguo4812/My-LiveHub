package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLOLHexRuntimeDataCopiesLegacyFilesWithoutOverwriting(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "extensions", "lol_hex_display")
	runtime := filepath.Join(root, "modules", "lol_hex_display")
	legacyFiles := map[string]string{
		"data/state.json": "legacy-state",
		"screenshots/inbox/game.png": "legacy-image",
		"logs/2026-11-18.log": "legacy-log",
		"dist/lol_hex_display/logs/2026-11-18.log": "frozen-log",
	}
	for relative, content := range legacyFiles {
		path := filepath.Join(legacy, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	existing := filepath.Join(runtime, "data", "state.json")
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("newer-state"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := MigrateLOLHexRuntimeData(legacy, runtime); err != nil {
		t.Fatalf("MigrateLOLHexRuntimeData() error = %v", err)
	}
	for relative, want := range legacyFiles {
		if relative == "data/state.json" {
			continue // This destination already existed and must not be overwritten.
		}
		path := filepath.Join(runtime, filepath.FromSlash(relative))
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read migrated %s: %v", relative, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", relative, got, want)
		}
	}
	got, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "newer-state" {
		t.Fatalf("existing destination was overwritten: %q", got)
	}
}

func TestMigrateLOLHexRuntimeDataIgnoresMissingLegacyDirectory(t *testing.T) {
	root := t.TempDir()
	if err := MigrateLOLHexRuntimeData(filepath.Join(root, "missing"), filepath.Join(root, "modules", "lol_hex_display")); err != nil {
		t.Fatalf("MigrateLOLHexRuntimeData() error = %v", err)
	}
}

func TestMigrateLOLHexRuntimeDataRejectsEmptyPaths(t *testing.T) {
	if err := MigrateLOLHexRuntimeData("", t.TempDir()); err == nil {
		t.Fatal("expected empty legacy path to fail")
	}
}
