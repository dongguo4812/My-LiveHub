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
	if !c.Enabled || !c.CommandEnabled || c.QueueCapacity != 100 || c.QueueDisplayCount != 5 {
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
	if c.ConfigVersion != CurrentMusicConfigVersion || c.QueueCapacity != 80 {
		t.Fatalf("migrated: %+v", c)
	}
}

func TestMusicConfigV7MigrationPreservesSkipCommandAndNormalization(t *testing.T) {
	got, changed := MigrateMusicConfig(MusicConfig{
		ConfigVersion: 7, Enabled: true, CommandEnabled: true, CommandPrefix: "点歌",
		SkipCommandEnabled: false, SkipCommand: "下一首", QueueCapacity: 100,
	})
	if !changed { t.Fatal("expected v7 configuration migration") }
	if got.SkipCommandEnabled || got.SkipCommand != "下一首" {
		t.Fatalf("v7 settings were not preserved: %+v", got)
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

func TestMusicConfigV8MigrationAddsIndependentFanMedalDefaults(t *testing.T) {
	got, changed := MigrateMusicConfig(MusicConfig{
		ConfigVersion: 8, Enabled: true, CommandEnabled: true, CommandPrefix: "点歌",
		SkipCommandEnabled: true, SkipCommand: "切歌", QueueCapacity: 100,
		RequireFanMedal: true, FanMedalLevel: 6,
	})
	if !changed { t.Fatal("expected v8 configuration migration") }
	if err := got.Validate(); err != nil { t.Fatal(err) }
	if !got.RequireFanMedal || got.FanMedalLevel != 6 { t.Fatalf("fan medal settings were not preserved: %+v", got) }
}


func TestMusicConfigV9MigrationAddsQueueDisplayCount(t *testing.T) {
	got, changed := MigrateMusicConfig(MusicConfig{
		ConfigVersion: 9, Enabled: true, CommandEnabled: true, CommandPrefix: "点歌",
		SkipCommandEnabled: true, SkipCommand: "切歌", QueueCapacity: 100, Volume: 80,
	})
	if !changed { t.Fatal("expected v9 configuration migration") }
	if err := got.Validate(); err != nil { t.Fatal(err) }
	if got.QueueDisplayCount != 5 { t.Fatalf("expected default display count 5, got %d", got.QueueDisplayCount) }
}

func TestMusicConfigQueueDisplayCountValidation(t *testing.T) {
	c := DefaultMusicConfig()
	c.QueueDisplayCount = 0
	if err := c.Validate(); err == nil { t.Fatal("expected zero display count to be rejected") }
	c.QueueDisplayCount = 1000
	if err := c.Validate(); err != nil { t.Fatalf("valid maximum display count rejected: %v", err) }
	c.QueueDisplayCount = 1001
	if err := c.Validate(); err == nil { t.Fatal("expected display count above 1000 to be rejected") }
}

func TestMusicConfigFanMedalLevelValidation(t *testing.T) {
	c := DefaultMusicConfig()
	c.RequireFanMedal = true
	c.FanMedalLevel = 0
	if err := c.Validate(); err == nil { t.Fatal("expected invalid required medal level to be rejected") }
	c.FanMedalLevel = 120
	if err := c.Validate(); err != nil { t.Fatalf("valid maximum medal level rejected: %v", err) }
	c.FanMedalLevel = 121
	if err := c.Validate(); err == nil { t.Fatal("expected medal level above 120 to be rejected") }
}


func TestMusicConfigV11MigrationAddsLXConnectionDefaults(t *testing.T) {
	got, changed := MigrateMusicConfig(MusicConfig{
		ConfigVersion: 11, Enabled: true, CommandEnabled: true, CommandPrefix: "点歌",
		SkipCommandEnabled: true, SkipCommand: "切歌", QueueCapacity: 100, QueueDisplayCount: 5,
	})
	if !changed { t.Fatal("expected v10 configuration migration") }
	if err := got.Validate(); err != nil { t.Fatal(err) }
	if len(got.LXAPIEndpoints) != 2 || got.LXAPIEndpoints[0] != "http://127.0.0.1:23330" || got.LXAPIEndpoints[1] != "http://192.168.1.3:23330" {
		t.Fatalf("unexpected 洛雪 API defaults: %#v", got.LXAPIEndpoints)
	}
	if len(got.EnabledSearchSources) != 5 || got.EnabledSearchSources[0] != "tx" {
		t.Fatalf("unexpected search source priority: %#v", got.EnabledSearchSources)
	}
}

func TestMusicConfigValidatesLXAPIEndpointsAndSearchSources(t *testing.T) {
	c := DefaultMusicConfig()
	if err := c.Validate(); err != nil { t.Fatalf("defaults should validate: %v", err) }
	c.LXAPIEndpoints = []string{"ftp://127.0.0.1:23330"}
	if err := c.Validate(); err == nil { t.Fatal("expected invalid API scheme to be rejected") }
	c = DefaultMusicConfig()
	c.LXAPIEndpoints = []string{"http://127.0.0.1:23330", "http://127.0.0.1:23330/"}
	if err := c.Validate(); err == nil { t.Fatal("expected duplicate API endpoints to be rejected") }
	c = DefaultMusicConfig()
	c.EnabledSearchSources = []string{"tx", "unknown"}
	if err := c.Validate(); err == nil { t.Fatal("expected unknown search source to be rejected") }
	c = DefaultMusicConfig()
	c.EnabledSearchSources = []string{"tx", "tx"}
	if err := c.Validate(); err == nil { t.Fatal("expected duplicate search source to be rejected") }
}
