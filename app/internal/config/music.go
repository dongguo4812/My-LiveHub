package config

import (
	"fmt"
	"math"
	"net/url"
	"strings"
)

const CurrentMusicConfigVersion = 16

type MusicConfig struct {
	ConfigVersion  int    `json:"config_version"`
	Volume float64 `json:"volume"`
	Enabled        bool   `json:"enabled"`
	CommandEnabled bool   `json:"command_enabled"`
	CommandPrefix  string `json:"command_prefix"`
	SkipCommandEnabled bool `json:"skip_command_enabled"`
	SkipCommand string `json:"skip_command"`
	QueueCapacity  int    `json:"queue_capacity"`
	RequestCooldownSeconds int `json:"request_cooldown_seconds"`
	QueueDisplayCount int `json:"queue_display_count"`
	PlaylistPlaybackMode string `json:"playlist_playback_mode"`
	LXAPIEndpoints []string `json:"lx_api_endpoints"`
	EnabledSearchSources []string `json:"enabled_search_sources"`
	RequireFanMedal bool `json:"require_fan_medal"`
	FanMedalLevel int `json:"fan_medal_level"`
}

func DefaultMusicConfig() MusicConfig {
	return MusicConfig{
		ConfigVersion: CurrentMusicConfigVersion,
		Volume: 1,
		Enabled: true,
		CommandEnabled: true,
		CommandPrefix: "点歌",
		SkipCommandEnabled: true,
		SkipCommand: "切歌",
		QueueCapacity: 100,
		RequestCooldownSeconds: 0,
		QueueDisplayCount: 5,
		PlaylistPlaybackMode: "sequential",
		FanMedalLevel: 1,
		LXAPIEndpoints: []string{"http://127.0.0.1:23330"},
		EnabledSearchSources: []string{"tx", "kw", "kg", "wy", "mg"},
	}
}

func MigrateMusicConfig(c MusicConfig) (MusicConfig, bool) {
	if c.ConfigVersion >= CurrentMusicConfigVersion {
		return c, false
	}
	n := DefaultMusicConfig()
	// Volume did not exist before v15; preserve the historical 100% default.
	n.Volume = 1
	n.Enabled = c.Enabled
	n.CommandEnabled = c.CommandEnabled
	if c.ConfigVersion >= 7 {
		n.SkipCommandEnabled = c.SkipCommandEnabled
		if strings.TrimSpace(c.SkipCommand) != "" { n.SkipCommand = c.SkipCommand }
	}
	if strings.TrimSpace(c.CommandPrefix) != "" {
		n.CommandPrefix = c.CommandPrefix
	}
	if c.QueueCapacity >= 1 && c.QueueCapacity <= 1000 {
		n.QueueCapacity = c.QueueCapacity
	}
	if c.RequestCooldownSeconds >= 0 && c.RequestCooldownSeconds <= 86400 { n.RequestCooldownSeconds = c.RequestCooldownSeconds }
	if c.QueueDisplayCount >= 1 && c.QueueDisplayCount <= 1000 {
		n.QueueDisplayCount = c.QueueDisplayCount
	}
	if c.LXAPIEndpoints != nil {
		n.LXAPIEndpoints = append([]string{}, c.LXAPIEndpoints...)
		if c.ConfigVersion < 13 && len(n.LXAPIEndpoints) > 1 {
			n.LXAPIEndpoints = n.LXAPIEndpoints[:1]
		}
	}
	if c.EnabledSearchSources != nil { n.EnabledSearchSources = append([]string{}, c.EnabledSearchSources...) }
	if c.PlaylistPlaybackMode == "sequential" || c.PlaylistPlaybackMode == "loop" || c.PlaylistPlaybackMode == "random" { n.PlaylistPlaybackMode = c.PlaylistPlaybackMode }
	n.RequireFanMedal = c.RequireFanMedal
	if c.FanMedalLevel >= 1 && c.FanMedalLevel <= 120 { n.FanMedalLevel = c.FanMedalLevel }
	return n, true
}

func (c MusicConfig) Validate() error {
	if c.ConfigVersion != CurrentMusicConfigVersion {
		return fmt.Errorf("unsupported music configuration version %d", c.ConfigVersion)
	}
	if math.IsNaN(c.Volume) || math.IsInf(c.Volume, 0) || c.Volume < 0 || c.Volume > 1 { return fmt.Errorf("music volume must be between 0 and 1") }
	if strings.TrimSpace(c.CommandPrefix) == "" {
		return fmt.Errorf("music command prefix is required")
	}
	if len([]rune(c.CommandPrefix)) > 16 {
		return fmt.Errorf("music command prefix is too long")
	}
	if c.SkipCommandEnabled && strings.TrimSpace(c.SkipCommand) == "" { return fmt.Errorf("music skip command is required") }
	if len([]rune(c.SkipCommand)) > 16 { return fmt.Errorf("music skip command is too long") }
	if c.PlaylistPlaybackMode != "sequential" && c.PlaylistPlaybackMode != "loop" && c.PlaylistPlaybackMode != "random" { return fmt.Errorf("不支持的歌单播放模式：%s", c.PlaylistPlaybackMode) }
	if c.RequireFanMedal && (c.FanMedalLevel < 1 || c.FanMedalLevel > 120) { return fmt.Errorf("音乐点播粉丝勋章等级必须在 1 到 120 之间") }
	if c.RequestCooldownSeconds < 0 || c.RequestCooldownSeconds > 86400 { return fmt.Errorf("普通观众点播冷却时间必须在 0 到 86400 秒之间") }
	if c.QueueCapacity < 1 || c.QueueCapacity > 1000 {
		return fmt.Errorf("music queue capacity must be between 1 and 1000")
	}
	if c.QueueDisplayCount < 1 || c.QueueDisplayCount > 1000 {
		return fmt.Errorf("music queue display count must be between 1 and 1000")
	}
	if len(c.LXAPIEndpoints) > 10 { return fmt.Errorf("洛雪 API 地址最多允许配置 10 个") }
	seenEndpoints := make(map[string]bool, len(c.LXAPIEndpoints))
	for _, endpoint := range c.LXAPIEndpoints {
		parsed, err := url.Parse(strings.TrimSpace(endpoint))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("无效的洛雪 API 地址：%s", endpoint)
		}
		key := strings.TrimRight(parsed.String(), "/")
		if seenEndpoints[key] { return fmt.Errorf("洛雪 API 地址不能重复：%s", endpoint) }
		seenEndpoints[key] = true
	}
	seenSources := make(map[string]bool, len(c.EnabledSearchSources))
	for _, source := range c.EnabledSearchSources {
		if source != "tx" && source != "kw" && source != "kg" && source != "wy" && source != "mg" { return fmt.Errorf("不支持的音乐搜索来源：%s", source) }
		if seenSources[source] { return fmt.Errorf("音乐搜索来源不能重复：%s", source) }
		seenSources[source] = true
	}
	return nil
}

// LoadMusicConfig drops fields from retired built-in platform configuration
// before strict typed decoding. This lets installations upgrade without
// requiring users to manually edit their existing music.json.
func LoadMusicConfig(m *Manager) (MusicConfig, error) {
	if m == nil {
		return MusicConfig{}, fmt.Errorf("configuration manager is required")
	}
	_, local, localExists, err := m.LoadParts("music.json")
	if err != nil {
		return MusicConfig{}, err
	}
	if localExists {
		allowed := map[string]bool{
			"config_version": true,
			"volume": true,
			"enabled": true,
			"command_enabled": true,
			"command_prefix": true,
			"skip_command_enabled": true,
			"skip_command": true,
			"queue_capacity": true,
			"request_cooldown_seconds": true,
			"queue_display_count": true,
			"playlist_playback_mode": true,
			"lx_api_endpoints": true,
			"enabled_search_sources": true,
			"require_fan_medal": true,
			"fan_medal_level": true,
		}
		cleaned := make(map[string]any, len(local))
		changed := false
		for key, value := range local {
			if !allowed[key] {
				changed = true
				continue
			}
			cleaned[key] = value
		}
		if changed {
			if err := m.Save("music.json", cleaned); err != nil {
				return MusicConfig{}, fmt.Errorf("remove retired music configuration fields: %w", err)
			}
		}
	}
	v, err := LoadTyped[MusicConfig](m, "music.json", func(c MusicConfig) error {
		if c.ConfigVersion < CurrentMusicConfigVersion {
			return nil
		}
		return c.Validate()
	})
	if err != nil {
		return MusicConfig{}, err
	}
	n, changed := MigrateMusicConfig(v)
	if !changed {
		return v, nil
	}
	if err := SaveTyped(m, "music.json", n, func(c MusicConfig) error { return c.Validate() }); err != nil {
		return MusicConfig{}, fmt.Errorf("migrate music configuration: %w", err)
	}
	return n, nil
}
