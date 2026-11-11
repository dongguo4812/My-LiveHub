package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestDefaultMusicConfigIsValid(t *testing.T) {
	c := DefaultMusicConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if !c.Enabled || !c.CommandEnabled || c.QueueCapacity != 100 {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestMusicConfigMigrationDropsPlatformConfig(t *testing.T) {
	c, changed := MigrateMusicConfig(MusicConfig{
		ConfigVersion: 3, Enabled: true, CommandEnabled: true,
		CommandPrefix: "点歌", QueueCapacity: 80,
	})
	if !changed {
		t.Fatal("expected migration")
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.ConfigVersion != 4 || c.QueueCapacity != 80 {
		t.Fatalf("migrated: %+v", c)
	}
}

func TestLoadMusicConfigRemovesRetiredFieldsBeforeStrictDecode(t *testing.T) {
	localDir := t.TempDir()
	manager, err := NewManager(fstest.MapFS{
		"music.json": {Data: []byte(`{
			"config_version": 3,
			"enabled": true,
			"command_enabled": true,
			"command_prefix": "点歌",
			"queue_capacity": 80
		}`)},
	}, localDir)
	if err != nil {
		t.Fatal(err)
	}
	legacy := `{
		"config_version": 3,
		"enabled": true,
		"command_enabled": true,
		"command_prefix": "点歌",
		"queue_capacity": 80,
		"search_limit": 20,
		"qq_music": {"enabled": true},
		"netease": {"enabled": true}
	}`
	if err := os.WriteFile(filepath.Join(localDir, "music.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadMusicConfig(manager)
	if err != nil {
		t.Fatalf("LoadMusicConfig should migrate legacy config: %v", err)
	}
	if got.ConfigVersion != CurrentMusicConfigVersion || !got.Enabled || got.QueueCapacity != 80 {
		t.Fatalf("unexpected migrated config: %+v", got)
	}

	data, err := os.ReadFile(filepath.Join(localDir, "music.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{"search_limit", "qq_music", "netease"} {
		if _, exists := saved[retired]; exists {
			t.Errorf("retired field %q remains in saved config", retired)
		}
	}
}
