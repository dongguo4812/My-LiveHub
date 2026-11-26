package config

import (
	"encoding/json"
	"math"
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

func TestLoadMusicConfigMigratesLegacySettingsWithoutLosingUserPreferences(t *testing.T) {
	localDir := t.TempDir()
	manager, err := NewManager(fstest.MapFS{
		"music.json": {Data: []byte(`{"config_version":17}`)},
	}, localDir)
	if err != nil {
		t.Fatal(err)
	}
	legacy := `{
		"config_version": 16,
		"volume": 0.42,
		"loudness_normalization": true,
		"enabled": true,
		"command_enabled": false,
		"command_prefix": "点歌呀",
		"skip_command_enabled": false,
		"skip_command": "下一首",
		"queue_capacity": 80,
		"request_cooldown_seconds": 30,
		"queue_display_count": 8,
		"playlist_playback_mode": "random",
		"lx_api_endpoints": ["http://127.0.0.1:23331", "http://127.0.0.1:23332"],
		"enabled_search_sources": ["kg", "tx", "mg"],
		"require_fan_medal": true,
		"fan_medal_level": 6,
		"retired_platform_config": {"legacy": true}
	}`
	if err := os.WriteFile(filepath.Join(localDir, "music.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadMusicConfig(manager)
	if err != nil {
		t.Fatalf("LoadMusicConfig should migrate legacy settings: %v", err)
	}
	if got.ConfigVersion != CurrentMusicConfigVersion {
		t.Fatalf("config version = %d, want %d", got.ConfigVersion, CurrentMusicConfigVersion)
	}
	if got.Volume != 0.42 || !got.LoudnessNormalization || got.CommandEnabled || got.CommandPrefix != "点歌呀" {
		t.Fatalf("playback/command preferences were not preserved: %+v", got)
	}
	if got.SkipCommandEnabled || got.SkipCommand != "下一首" || got.QueueCapacity != 80 ||
		got.RequestCooldownSeconds != 30 || got.QueueDisplayCount != 8 || got.PlaylistPlaybackMode != "random" {
		t.Fatalf("queue/command preferences were not preserved: %+v", got)
	}
	if len(got.EnabledSearchSources) != 3 || got.EnabledSearchSources[0] != "kg" ||
		got.EnabledSearchSources[1] != "tx" || got.EnabledSearchSources[2] != "mg" {
		t.Fatalf("search source priority was not preserved: %#v", got.EnabledSearchSources)
	}
	if !got.RequireFanMedal || got.FanMedalLevel != 6 {
		t.Fatalf("fan medal preferences were not preserved: %+v", got)
	}

	data, err := os.ReadFile(filepath.Join(localDir, "music.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if _, exists := saved["retired_platform_config"]; exists {
		t.Fatal("retired platform configuration should be removed during migration")
	}
	if _, exists := saved["lx_api_endpoints"]; exists {
		t.Fatal("legacy LX API endpoints should be removed during migration")
	}
}

func TestLoadMusicConfigV17RemovesLXAPIEndpointsWithoutLosingSettings(t *testing.T) {
	localDir := t.TempDir()
	manager, err := NewManager(fstest.MapFS{
		"music.json": {Data: []byte(`{"config_version":17}`)},
	}, localDir)
	if err != nil { t.Fatal(err) }
	legacy := `{
		"config_version": 17,
		"volume": 0.37,
		"loudness_normalization": true,
		"enabled": true,
		"command_enabled": true,
		"command_prefix": "点歌",
		"skip_command_enabled": true,
		"skip_command": "切歌",
		"queue_capacity": 90,
		"queue_display_count": 7,
		"playlist_playback_mode": "loop",
		"lx_api_endpoints": ["http://127.0.0.1:23330"],
		"enabled_search_sources": ["mg", "kg", "tx"]
	}`
	if err := os.WriteFile(filepath.Join(localDir, "music.json"), []byte(legacy), 0o600); err != nil { t.Fatal(err) }
	got, err := LoadMusicConfig(manager)
	if err != nil { t.Fatal(err) }
	if got.ConfigVersion != 18 || got.Volume != 0.37 || !got.LoudnessNormalization ||
		got.QueueCapacity != 90 || got.QueueDisplayCount != 7 || got.PlaylistPlaybackMode != "loop" {
		t.Fatalf("v17 preferences were not preserved during cleanup: %+v", got)
	}
	if len(got.EnabledSearchSources) != 3 || got.EnabledSearchSources[0] != "mg" || got.EnabledSearchSources[1] != "kg" || got.EnabledSearchSources[2] != "tx" {
		t.Fatalf("search source order was not preserved: %#v", got.EnabledSearchSources)
	}
	data, err := os.ReadFile(filepath.Join(localDir, "music.json"))
	if err != nil { t.Fatal(err) }
	var saved map[string]any
	if err := json.Unmarshal(data, &saved); err != nil { t.Fatal(err) }
	if _, exists := saved["lx_api_endpoints"]; exists { t.Fatal("legacy LX API endpoints remain in migrated config") }
}

func TestMusicConfigV7MigrationPreservesSkipCommand(t *testing.T) {
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
		SkipCommandEnabled: true, SkipCommand: "切歌", QueueCapacity: 100,
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


func TestMusicConfigV11MigrationAddsSearchSourceDefaults(t *testing.T) {
	got, changed := MigrateMusicConfig(MusicConfig{
		ConfigVersion: 11, Enabled: true, CommandEnabled: true, CommandPrefix: "点歌",
		SkipCommandEnabled: true, SkipCommand: "切歌", QueueCapacity: 100, QueueDisplayCount: 5,
	})
	if !changed { t.Fatal("expected v11 configuration migration") }
	if err := got.Validate(); err != nil { t.Fatal(err) }
	if len(got.EnabledSearchSources) != 5 || got.EnabledSearchSources[0] != "tx" {
		t.Fatalf("unexpected search source priority: %#v", got.EnabledSearchSources)
	}
}

func TestMusicConfigValidatesSearchSources(t *testing.T) {
	c := DefaultMusicConfig()
	if err := c.Validate(); err != nil { t.Fatalf("defaults should validate: %v", err) }
	c = DefaultMusicConfig()
	c.EnabledSearchSources = []string{"tx", "unknown"}
	if err := c.Validate(); err == nil { t.Fatal("expected unknown search source to be rejected") }
	c = DefaultMusicConfig()
	c.EnabledSearchSources = []string{"tx", "tx"}
	if err := c.Validate(); err == nil { t.Fatal("expected duplicate search source to be rejected") }
}

func TestMusicConfigMigrationPreservesExplicitEmptySelections(t *testing.T) {
	got, changed := MigrateMusicConfig(MusicConfig{
		ConfigVersion: 11, Enabled: true, CommandEnabled: true, CommandPrefix: "点歌",
		QueueCapacity: 100, EnabledSearchSources: []string{},
	})
	if !changed { t.Fatal("expected v11 configuration migration") }
	if got.EnabledSearchSources == nil || len(got.EnabledSearchSources) != 0 {
		t.Fatalf("explicit empty search source selection should remain empty, got %#v", got.EnabledSearchSources)
	}
}



func TestMusicConfigRequestCooldownValidation(t *testing.T) {
	c := DefaultMusicConfig()
	c.RequestCooldownSeconds = 0
	if err := c.Validate(); err != nil { t.Fatalf("zero should disable cooldown: %v", err) }
	c.RequestCooldownSeconds = 86400
	if err := c.Validate(); err != nil { t.Fatalf("maximum cooldown rejected: %v", err) }
	c.RequestCooldownSeconds = -1
	if err := c.Validate(); err == nil { t.Fatal("negative cooldown should be rejected") }
	c.RequestCooldownSeconds = 86401
	if err := c.Validate(); err == nil { t.Fatal("cooldown above 24 hours should be rejected") }
}

func TestMusicConfigV13MigrationAddsRequestCooldownDefault(t *testing.T) {
	got, changed := MigrateMusicConfig(MusicConfig{
		ConfigVersion: 13, Enabled: true, CommandEnabled: true, CommandPrefix: "点歌",
		SkipCommandEnabled: true, SkipCommand: "切歌", QueueCapacity: 100, QueueDisplayCount: 5,
	})
	if !changed { t.Fatal("expected v13 configuration migration") }
	if err := got.Validate(); err != nil { t.Fatal(err) }
	if got.RequestCooldownSeconds != 0 { t.Fatalf("expected cooldown disabled by default, got %d", got.RequestCooldownSeconds) }
}


func TestMusicConfigV14MigrationAddsPersistentVolumeDefault(t *testing.T) {
	got, changed := MigrateMusicConfig(MusicConfig{
		ConfigVersion: 14, Enabled: true, CommandEnabled: true, CommandPrefix: "点歌",
		SkipCommandEnabled: true, SkipCommand: "切歌", QueueCapacity: 100,
	})
	if !changed { t.Fatal("expected v14 configuration migration") }
	if err := got.Validate(); err != nil { t.Fatal(err) }
	if got.ConfigVersion != CurrentMusicConfigVersion || got.Volume != 1 {
		t.Fatalf("v14 migration should default volume to 100%%, got version=%d volume=%v", got.ConfigVersion, got.Volume)
	}
}

func TestMusicConfigVolumeValidation(t *testing.T) {
	c := DefaultMusicConfig()
	for _, volume := range []float64{0, 0.35, 1} {
		c.Volume = volume
		if err := c.Validate(); err != nil { t.Fatalf("valid volume %v rejected: %v", volume, err) }
	}
	for _, volume := range []float64{-0.01, 1.01, math.NaN(), math.Inf(1)} {
		c.Volume = volume
		if err := c.Validate(); err == nil { t.Errorf("invalid volume %v was accepted", volume) }
	}
}

func TestMusicConfigVolumeSurvivesConfigReload(t *testing.T) {
	localDir := t.TempDir()
	defaults, err := json.Marshal(DefaultMusicConfig())
	if err != nil { t.Fatal(err) }
	manager, err := NewManager(fstest.MapFS{"music.json": {Data: defaults}}, localDir)
	if err != nil { t.Fatal(err) }
	want := DefaultMusicConfig()
	want.Volume = 0.37
	if err := SaveTyped(manager, "music.json", want, func(c MusicConfig) error { return c.Validate() }); err != nil { t.Fatal(err) }
	reloaded, err := LoadMusicConfig(manager)
	if err != nil { t.Fatal(err) }
	if reloaded.Volume != want.Volume { t.Fatalf("reloaded volume = %v, want %v", reloaded.Volume, want.Volume) }
}


func TestMusicConfigV15MigrationPreservesPlaylistPlaybackMode(t *testing.T) {
	for _, mode := range []string{"sequential", "loop", "random"} {
		got, changed := MigrateMusicConfig(MusicConfig{
			ConfigVersion: 15, Enabled: true, CommandEnabled: true, CommandPrefix: "点歌",
			SkipCommandEnabled: true, SkipCommand: "切歌", QueueCapacity: 100,
			QueueDisplayCount: 5, PlaylistPlaybackMode: mode,
		})
		if !changed { t.Fatalf("mode %q: expected v15 migration", mode) }
		if err := got.Validate(); err != nil { t.Fatalf("mode %q: %v", mode, err) }
		if got.PlaylistPlaybackMode != mode { t.Fatalf("mode %q migrated as %q", mode, got.PlaylistPlaybackMode) }
	}
	got, changed := MigrateMusicConfig(MusicConfig{ConfigVersion: 15, Enabled: true, CommandEnabled: true, CommandPrefix: "点歌"})
	if !changed || got.PlaylistPlaybackMode != "sequential" {
		t.Fatalf("missing legacy mode should default to sequential: %+v", got)
	}
}

func TestMusicConfigRejectsUnsupportedPlaylistPlaybackMode(t *testing.T) {
	value := DefaultMusicConfig()
	value.PlaylistPlaybackMode = "unsupported"
	if err := value.Validate(); err == nil { t.Fatal("unsupported playlist playback mode should be rejected") }
}


func TestMusicConfigV16MigrationAddsLoudnessNormalizationOffByDefault(t *testing.T) {
	got, changed := MigrateMusicConfig(MusicConfig{
		ConfigVersion: 16, Enabled: true, CommandEnabled: true, CommandPrefix: "点歌",
		SkipCommandEnabled: true, SkipCommand: "切歌", QueueCapacity: 100, Volume: 0.65,
	})
	if !changed { t.Fatal("expected v16 configuration migration") }
	if err := got.Validate(); err != nil { t.Fatal(err) }
	if got.ConfigVersion != CurrentMusicConfigVersion || got.LoudnessNormalization {
		t.Fatalf("v16 migration should default normalization off, got version=%d enabled=%v", got.ConfigVersion, got.LoudnessNormalization)
	}
	if got.Volume != 0.65 { t.Fatalf("v16 migration lost saved volume: %v", got.Volume) }
}

func TestMusicConfigMigrationPreservesLoudnessNormalization(t *testing.T) {
	got, changed := MigrateMusicConfig(MusicConfig{
		ConfigVersion: 16, Enabled: true, CommandEnabled: true, CommandPrefix: "点歌",
		SkipCommandEnabled: true, SkipCommand: "切歌", QueueCapacity: 100,
		LoudnessNormalization: true,
	})
	if !changed { t.Fatal("expected v16 configuration migration") }
	if !got.LoudnessNormalization { t.Fatal("explicit loudness normalization setting was not preserved") }
}
