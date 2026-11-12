package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

const CurrentMusicConfigVersion = 9

type LXUserSourceProxyConfig struct {
	Enabled bool `json:"enabled"`
	Host string `json:"host"`
	Port string `json:"port"`
}

func (c LXUserSourceProxyConfig) Validate() error {
	if !c.Enabled { return nil }
	host := strings.TrimSpace(c.Host)
	if host == "" || strings.Contains(host, "://") || strings.ContainsAny(host, "/?#@ ") { return fmt.Errorf("LX 音源代理主机地址无效") }
	port, err := strconv.Atoi(strings.TrimSpace(c.Port))
	if err != nil || port < 1 || port > 65535 { return fmt.Errorf("LX 音源代理端口必须为 1 到 65535 之间的数字") }
	if net.ParseIP(host) == nil && strings.Contains(host, ":") { return fmt.Errorf("LX 音源代理主机地址无效") }
	return nil
}

type MusicConfig struct {
	ConfigVersion  int    `json:"config_version"`
	Enabled        bool   `json:"enabled"`
	CommandEnabled bool   `json:"command_enabled"`
	CommandPrefix  string `json:"command_prefix"`
	SkipCommandEnabled bool `json:"skip_command_enabled"`
	SkipCommand string `json:"skip_command"`
	QueueCapacity  int    `json:"queue_capacity"`
	Volume int `json:"volume"`
	LoudnessNormalization bool `json:"loudness_normalization"`
	LXUserSourceProxy LXUserSourceProxyConfig `json:"lx_user_source_proxy"`
	RequireFanMedal bool `json:"require_fan_medal"`
	FanMedalLevel int `json:"fan_medal_level"`
}

func DefaultMusicConfig() MusicConfig {
	return MusicConfig{
		ConfigVersion: CurrentMusicConfigVersion,
		Enabled: true,
		CommandEnabled: true,
		CommandPrefix: "点歌",
		SkipCommandEnabled: true,
		SkipCommand: "切歌",
		QueueCapacity: 100,
		Volume: 80,
		FanMedalLevel: 1,
	}
}

func MigrateMusicConfig(c MusicConfig) (MusicConfig, bool) {
	if c.ConfigVersion >= CurrentMusicConfigVersion {
		return c, false
	}
	n := DefaultMusicConfig()
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
	n.LXUserSourceProxy = c.LXUserSourceProxy
	n.RequireFanMedal = c.RequireFanMedal
	if c.FanMedalLevel >= 1 && c.FanMedalLevel <= 100 { n.FanMedalLevel = c.FanMedalLevel }
	n.LoudnessNormalization = c.LoudnessNormalization
	if c.Volume > 0 && c.Volume <= 100 { n.Volume = c.Volume }
	return n, true
}

func (c MusicConfig) Validate() error {
	if c.ConfigVersion != CurrentMusicConfigVersion {
		return fmt.Errorf("unsupported music configuration version %d", c.ConfigVersion)
	}
	if strings.TrimSpace(c.CommandPrefix) == "" {
		return fmt.Errorf("music command prefix is required")
	}
	if len([]rune(c.CommandPrefix)) > 16 {
		return fmt.Errorf("music command prefix is too long")
	}
	if c.SkipCommandEnabled && strings.TrimSpace(c.SkipCommand) == "" { return fmt.Errorf("music skip command is required") }
	if len([]rune(c.SkipCommand)) > 16 { return fmt.Errorf("music skip command is too long") }
	if c.Volume < 0 || c.Volume > 100 { return fmt.Errorf("music volume must be between 0 and 100") }
	if c.FanMedalLevel < 1 || c.FanMedalLevel > 100 { return fmt.Errorf("音乐点播粉丝勋章等级必须在 1 到 100 之间") }
	if c.QueueCapacity < 1 || c.QueueCapacity > 1000 {
		return fmt.Errorf("music queue capacity must be between 1 and 1000")
	}
	if err := c.LXUserSourceProxy.Validate(); err != nil { return err }
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
			"enabled": true,
			"command_enabled": true,
			"command_prefix": true,
			"skip_command_enabled": true,
			"skip_command": true,
			"queue_capacity": true,
			"volume": true,
			"loudness_normalization": true,
			"lx_user_source_proxy": true,
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
