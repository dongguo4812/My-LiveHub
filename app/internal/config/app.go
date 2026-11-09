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
