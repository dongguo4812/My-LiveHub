package config

import (
	"fmt"
	"strings"
)

const CurrentMusicConfigVersion = 2

type MusicConfig struct {
	ConfigVersion  int             `json:"config_version"`
	Enabled        bool            `json:"enabled"`
	CommandEnabled bool            `json:"command_enabled"`
	CommandPrefix  string          `json:"command_prefix"`
	QueueCapacity  int             `json:"queue_capacity"`
	SearchLimit    int             `json:"search_limit"`
	Sources        map[string]bool `json:"sources"`

	// Legacy fields are accepted only to migrate existing local configuration.
	LegacyDefaultPlayer string          `json:"default_player,omitempty"`
	LegacyPlayers       map[string]bool `json:"players,omitempty"`
}

func DefaultMusicConfig() MusicConfig {
	return MusicConfig{
		ConfigVersion: CurrentMusicConfigVersion,
		Enabled: false,
		CommandEnabled: true,
		CommandPrefix: "点歌",
		QueueCapacity: 100,
		SearchLimit: 20,
		Sources: map[string]bool{
			"netease": true,
			"qq_music": true,
			"kugou": true,
			"kuwo": true,
		},
	}
}

// MigrateMusicConfig converts the former independent-player settings into
// source preferences. Luoxue was a player, not a source, so it is not carried
// forward as an enabled source.
func MigrateMusicConfig(c MusicConfig) (MusicConfig, bool) {
	if c.ConfigVersion >= CurrentMusicConfigVersion && c.LegacyPlayers == nil && c.LegacyDefaultPlayer == "" {
		return c, false
	}
	migrated := DefaultMusicConfig()
	migrated.Enabled = c.Enabled
	migrated.CommandEnabled = c.CommandEnabled
	if strings.TrimSpace(c.CommandPrefix) != "" {
		migrated.CommandPrefix = c.CommandPrefix
	}
	if c.QueueCapacity >= 1 && c.QueueCapacity <= 1000 {
		migrated.QueueCapacity = c.QueueCapacity
	}
	if c.SearchLimit >= 1 && c.SearchLimit <= 50 {
		migrated.SearchLimit = c.SearchLimit
	}
	if c.LegacyPlayers != nil {
		for id, enabled := range c.LegacyPlayers {
			if id == "netease" || id == "qq_music" || id == "kugou" || id == "kuwo" {
				migrated.Sources[id] = enabled
			}
		}
	}
	// A usable source must remain selected. The old "luoxue only" selection
	// cannot be migrated because Luoxue is no longer a playback backend.
	hasEnabled := false
	for _, enabled := range migrated.Sources {
		hasEnabled = hasEnabled || enabled
	}
	if !hasEnabled {
		migrated.Sources["netease"] = true
	}
	return migrated, true
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
	if c.SearchLimit < 1 || c.SearchLimit > 50 {
		return fmt.Errorf("music search limit must be between 1 and 50")
	}
	if len(c.Sources) == 0 {
		return fmt.Errorf("at least one music source must be configured")
	}
	hasEnabled := false
	for id, enabled := range c.Sources {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("music source ID cannot be empty")
		}
		hasEnabled = hasEnabled || enabled
	}
	if !hasEnabled {
		return fmt.Errorf("at least one music source must be enabled")
	}
	return nil
}
