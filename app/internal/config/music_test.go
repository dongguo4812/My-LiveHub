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
	if !c.Enabled || !c.CommandEnabled || c.QueueCapacity != 100 || c.QueueDisplayCount != 5 || c.Volume != 80 || c.LoudnessNormalization {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestMusicConfigMigrationDropsPlatformConfig(t *testing.T) {
	c, changed := MigrateMusicConfig(MusicConfig{
		ConfigVersion: 3, Enabled: true, CommandEnabled: true, Volume: 42,
		CommandPrefix: "点歌", QueueCapacity: 80,
	})
	if !changed {
		t.Fatal("expected migration")
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.ConfigVersion != CurrentMusicConfigVersion || c.QueueCapacity != 80 || c.Volume != 42 || c.LoudnessNormalization {
		t.Fatalf("migrated: %+v", c)
	}
}

func TestMusicConfigV7MigrationPreservesSkipCommandAndNormalization(t *testing.T) {
	got, changed := MigrateMusicConfig(MusicConfig{
		ConfigVersion: 7, Enabled: true, CommandEnabled: true, CommandPrefix: "点歌",
		SkipCommandEnabled: false, SkipCommand: "下一首", QueueCapacity: 100, Volume: 66,
		LoudnessNormalization: true,
	})
	if !changed { t.Fatal("expected v7 configuration migration") }
	if got.SkipCommandEnabled || got.SkipCommand != "下一首" || !got.LoudnessNormalization || got.Volume != 66 {
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

func TestLXUserSourceProxyConfigValidation(t *testing.T) {
	if err := (LXUserSourceProxyConfig{}).Validate(); err != nil { t.Fatalf("disabled proxy should be valid: %v", err) }
	valid := LXUserSourceProxyConfig{Enabled: true, Host: "127.0.0.1", Port: "7890"}
	if err := valid.Validate(); err != nil { t.Fatalf("valid proxy rejected: %v", err) }
	for _, value := range []LXUserSourceProxyConfig{{Enabled:true, Host:"", Port:"7890"}, {Enabled:true, Host:"127.0.0.1", Port:"0"}, {Enabled:true, Host:"http://127.0.0.1", Port:"7890"}} {
		if err := value.Validate(); err == nil { t.Errorf("invalid proxy config accepted: %+v", value) }
	}
}

func TestMusicConfigV8MigrationAddsIndependentFanMedalDefaults(t *testing.T) {
	got, changed := MigrateMusicConfig(MusicConfig{
		ConfigVersion: 8, Enabled: true, CommandEnabled: true, CommandPrefix: "点歌",
		SkipCommandEnabled: true, SkipCommand: "切歌", QueueCapacity: 100, Volume: 80,
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
