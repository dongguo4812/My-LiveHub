package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestDefaultMusicConfigIsValid(t *testing.T) {
	cfg := DefaultMusicConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Fatal("music module should be enabled by default")
	}
	if len(cfg.Sources) != 5 {
		t.Fatalf("expected five independently configurable sources, got %d", len(cfg.Sources))
	}
}

func TestMusicConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		mutate func(*MusicConfig)
	}{
		{name: "empty command", mutate: func(c *MusicConfig) { c.CommandPrefix = " " }},
		{name: "queue too large", mutate: func(c *MusicConfig) { c.QueueCapacity = 1001 }},
		{name: "search limit too large", mutate: func(c *MusicConfig) { c.SearchLimit = 51 }},
		{name: "no sources", mutate: func(c *MusicConfig) { c.Sources = map[string]bool{} }},
		{name: "all sources disabled", mutate: func(c *MusicConfig) { for id := range c.Sources { c.Sources[id] = false } }},
		{name: "empty source ID", mutate: func(c *MusicConfig) { c.Sources[" "] = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultMusicConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestMigrateLegacyMusicPlayersToSources(t *testing.T) {
	legacy := MusicConfig{
		ConfigVersion: 1,
		Enabled: true,
		CommandEnabled: false,
		CommandPrefix: "点歌",
		QueueCapacity: 80,
		SearchLimit: 12,
		LegacyDefaultPlayer: "luoxue",
		LegacyPlayers: map[string]bool{
			"qq_music": false,
			"netease": true,
			"kugou": false,
			"kuwo": true,
			"luoxue": true,
		},
	}
	got, changed := MigrateMusicConfig(legacy)
	if !changed {
		t.Fatal("expected legacy configuration to migrate")
	}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.CommandEnabled || got.QueueCapacity != 80 || got.SearchLimit != 12 {
		t.Fatalf("legacy settings were not preserved: %+v", got)
	}
	if got.Sources["netease"] != true || got.Sources["qq_music"] != false || got.Sources["kuwo"] != true || got.Sources["kugou"] != false {
		t.Fatalf("source selections were not preserved: %+v", got.Sources)
	}
	if _, exists := got.Sources["luoxue"]; exists {
		t.Fatal("Luoxue must not be migrated as a source")
	}
}

func TestMigrateLegacyLuoxueOnlySelectionKeepsOneSourceEnabled(t *testing.T) {
	got, changed := MigrateMusicConfig(MusicConfig{
		ConfigVersion: 1,
		CommandPrefix: "点歌",
		QueueCapacity: 100,
		SearchLimit: 20,
		LegacyPlayers: map[string]bool{"luoxue": true},
	})
	if !changed || !got.Sources["netease"] {
		t.Fatalf("expected a fallback source to be enabled: %+v", got)
	}
}

func TestMigrateMusicConfigAddsMiguAndPreservesExistingSourcePreferences(t *testing.T){
 got,changed:=MigrateMusicConfig(MusicConfig{ConfigVersion:2,Enabled:true,CommandEnabled:true,CommandPrefix:"点歌",QueueCapacity:100,SearchLimit:20,Sources:map[string]bool{"netease":true,"qq_music":false,"kugou":true,"kuwo":false}})
 if !changed||got.ConfigVersion!=CurrentMusicConfigVersion{t.Fatalf("expected v2 config migration: %+v",got)}
 if !got.Sources["migu"]||got.Sources["qq_music"]||got.Sources["kuwo"]{t.Fatalf("source preferences not preserved: %+v",got.Sources)}
}


func TestLoadMusicConfigMigratesVersionTwoBeforeValidation(t *testing.T) {
	defaults := fstest.MapFS{
		"music.json": &fstest.MapFile{Data: []byte(`{
			"config_version": 3,
			"enabled": true,
			"command_enabled": true,
			"command_prefix": "点歌",
			"queue_capacity": 100,
			"search_limit": 20,
			"sources": {
				"netease": true,
				"qq_music": true,
				"kugou": true,
				"kuwo": true,
				"migu": true
			}
		}`)},
	}
	localDir := t.TempDir()
	localPath := filepath.Join(localDir, "music.json")
	if err := os.WriteFile(localPath, []byte(`{
		"config_version": 2,
		"enabled": true,
		"command_enabled": true,
		"command_prefix": "点歌",
		"queue_capacity": 100,
		"search_limit": 20,
		"sources": {
			"netease": true,
			"qq_music": false,
			"kugou": true,
			"kuwo": false
		}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(defaults, localDir)
	if err != nil {
		t.Fatal(err)
	}

	got, err := LoadMusicConfig(manager)
	if err != nil {
		t.Fatalf("load version 2 music config: %v", err)
	}
	if got.ConfigVersion != CurrentMusicConfigVersion {
		t.Fatalf("expected migrated config version %d, got %d", CurrentMusicConfigVersion, got.ConfigVersion)
	}
	if !got.Sources["netease"] || got.Sources["qq_music"] || !got.Sources["kugou"] || got.Sources["kuwo"] || !got.Sources["migu"] {
		t.Fatalf("source selections were not preserved/migrated: %+v", got.Sources)
	}
	persisted, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	var saved MusicConfig
	if err := json.Unmarshal(persisted, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.ConfigVersion != CurrentMusicConfigVersion {
		t.Fatalf("expected persisted config version %d, got %d", CurrentMusicConfigVersion, saved.ConfigVersion)
	}
}
