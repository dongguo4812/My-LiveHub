package config

import (
	"fmt"
	"strings"
)

const CurrentMusicConfigVersion = 1

type MusicConfig struct {
	ConfigVersion  int             `json:"config_version"`
	Enabled        bool            `json:"enabled"`
	CommandEnabled bool            `json:"command_enabled"`
	CommandPrefix  string          `json:"command_prefix"`
	DefaultPlayer  string          `json:"default_player"`
	QueueCapacity  int             `json:"queue_capacity"`
	SearchLimit    int             `json:"search_limit"`
	Players        map[string]bool `json:"players"`
}

func DefaultMusicConfig() MusicConfig {
	return MusicConfig{
		ConfigVersion: CurrentMusicConfigVersion,
		Enabled: false,
		CommandEnabled: true,
		CommandPrefix: "点歌",
		DefaultPlayer: "",
		QueueCapacity: 100,
		SearchLimit: 20,
		Players: map[string]bool{
			"qq_music": true,
			"netease": true,
			"kugou": true,
			"kuwo": true,
			"luoxue": true,
		},
	}
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
	for id := range c.Players {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("music player ID cannot be empty")
		}
	}
	if c.DefaultPlayer != "" {
		if _, ok := c.Players[c.DefaultPlayer]; !ok {
			return fmt.Errorf("default music player %q is not configured", c.DefaultPlayer)
		}
		if !c.Players[c.DefaultPlayer] {
			return fmt.Errorf("default music player %q is disabled", c.DefaultPlayer)
		}
	}
	return nil
}
