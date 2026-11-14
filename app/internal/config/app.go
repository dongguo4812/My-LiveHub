package config

import "fmt"

const (
	CurrentAppConfigVersion = 1
	DefaultLogRetentionDays = 14
	MinLogRetentionDays     = 1
	MaxLogRetentionDays     = 365
)

type AppearanceConfig struct {
	Theme       string `json:"theme"`
	AccentColor string `json:"accent_color"`
}

type LoggingConfig struct {
	RetentionDays int `json:"retention_days"`
}

type WindowConfig struct {
	CloseBehavior string `json:"close_behavior"`
}

const (
	WindowCloseMinimizeToTray = "minimize_to_tray"
	WindowCloseExit           = "exit"
)

type AppConfig struct {
	ConfigVersion int              `json:"config_version"`
	Appearance    AppearanceConfig `json:"appearance"`
	Logging       LoggingConfig    `json:"logging"`
	Window        WindowConfig     `json:"window"`
}

func DefaultAppearanceConfig() AppearanceConfig {
	return AppearanceConfig{Theme: "dark", AccentColor: "indigo"}
}

func DefaultLoggingConfig() LoggingConfig {
	return LoggingConfig{RetentionDays: DefaultLogRetentionDays}
}

func DefaultWindowConfig() WindowConfig {
	return WindowConfig{CloseBehavior: WindowCloseMinimizeToTray}
}

func (c AppConfig) Validate() error {
	if c.ConfigVersion != CurrentAppConfigVersion {
		return fmt.Errorf("unsupported app config version %d", c.ConfigVersion)
	}
	if c.Appearance.Theme != "dark" && c.Appearance.Theme != "light" {
		return fmt.Errorf("unsupported appearance theme %q", c.Appearance.Theme)
	}
	switch c.Appearance.AccentColor {
	case "indigo", "blue", "violet", "teal", "rose":
	default:
		return fmt.Errorf("unsupported appearance accent color %q", c.Appearance.AccentColor)
	}
	if c.Logging.RetentionDays != 0 && (c.Logging.RetentionDays < MinLogRetentionDays || c.Logging.RetentionDays > MaxLogRetentionDays) {
		return fmt.Errorf("log retention days must be between %d and %d", MinLogRetentionDays, MaxLogRetentionDays)
	}
	if c.Window.CloseBehavior != "" && c.Window.CloseBehavior != WindowCloseMinimizeToTray && c.Window.CloseBehavior != WindowCloseExit {
		return fmt.Errorf("unsupported window close behavior %q", c.Window.CloseBehavior)
	}
	return nil
}

func (c AppearanceConfig) Validate() error {
	return (AppConfig{ConfigVersion: CurrentAppConfigVersion, Appearance: c, Logging: DefaultLoggingConfig()}).Validate()
}

func (c LoggingConfig) Validate() error {
	return (AppConfig{ConfigVersion: CurrentAppConfigVersion, Appearance: DefaultAppearanceConfig(), Logging: c}).Validate()
}
