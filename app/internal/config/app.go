package config

import "fmt"

const CurrentAppConfigVersion = 1

type AppearanceConfig struct {
 Theme string `json:"theme"`
 AccentColor string `json:"accent_color"`
}
type AppConfig struct {
 ConfigVersion int `json:"config_version"`
 Appearance AppearanceConfig `json:"appearance"`
}
func DefaultAppearanceConfig() AppearanceConfig { return AppearanceConfig{Theme:"dark",AccentColor:"indigo"} }
func (c AppConfig) Validate() error {
 if c.ConfigVersion!=CurrentAppConfigVersion{return fmt.Errorf("unsupported app config version %d",c.ConfigVersion)}
 if c.Appearance.Theme!="dark"&&c.Appearance.Theme!="light"{return fmt.Errorf("unsupported appearance theme %q",c.Appearance.Theme)}
 switch c.Appearance.AccentColor{case "indigo","blue","violet","teal","rose":default:return fmt.Errorf("unsupported appearance accent color %q",c.Appearance.AccentColor)}
 return nil
}
func (c AppearanceConfig) Validate() error { return (AppConfig{ConfigVersion:CurrentAppConfigVersion,Appearance:c}).Validate() }
