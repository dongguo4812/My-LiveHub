package config

import "testing"

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
