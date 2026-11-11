package config

import (
	"fmt"
	"strings"
)

const CurrentMusicConfigVersion = 4

type MusicConfig struct {
	ConfigVersion  int    `json:"config_version"`
	Enabled        bool   `json:"enabled"`
	CommandEnabled bool   `json:"command_enabled"`
	CommandPrefix  string `json:"command_prefix"`
	QueueCapacity  int    `json:"queue_capacity"`
}

func DefaultMusicConfig() MusicConfig {
	return MusicConfig{
		ConfigVersion: CurrentMusicConfigVersion,
		Enabled: true,
		CommandEnabled: true,
		CommandPrefix: "点歌",
		QueueCapacity: 100,
	}
}

func MigrateMusicConfig(c MusicConfig) (MusicConfig, bool) {
	if c.ConfigVersion >= CurrentMusicConfigVersion {
		return c, false
	}
	n := DefaultMusicConfig()
	n.Enabled = c.Enabled
	n.CommandEnabled = c.CommandEnabled
	if strings.TrimSpace(c.CommandPrefix) != "" {
		n.CommandPrefix = c.CommandPrefix
	}
	if c.QueueCapacity >= 1 && c.QueueCapacity <= 1000 {
		n.QueueCapacity = c.QueueCapacity
	}
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
	if c.QueueCapacity < 1 || c.QueueCapacity > 1000 {
		return fmt.Errorf("music queue capacity must be between 1 and 1000")
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
			"enabled": true,
			"command_enabled": true,
			"command_prefix": true,
			"queue_capacity": true,
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
