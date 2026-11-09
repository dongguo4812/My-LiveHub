package config

import "fmt"

const CurrentAppConfigVersion = 1

// AppConfig contains settings that control the application itself.
type AppConfig struct {
	ConfigVersion int `json:"config_version"`
}

func (c AppConfig) Validate() error {
	if c.ConfigVersion != CurrentAppConfigVersion {
		return fmt.Errorf("unsupported app config version %d", c.ConfigVersion)
	}
	return nil
}

// AppConfigMigrations contains sequential migrations keyed by their source version.
var AppConfigMigrations = map[int]Migration{
	0: func(data map[string]any) error {
		// Version 1 establishes the versioned app configuration envelope.
		return nil
	},
}
