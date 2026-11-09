package config

import "fmt"

// DanmakuWindowConfig stores the independent overlay's last frame and startup preferences.
type DanmakuWindowConfig struct {
	X             int  `json:"x"`
	Y             int  `json:"y"`
	Width         int  `json:"width"`
	Height        int  `json:"height"`
	PositionSaved bool `json:"position_saved"`
	OpenOnStartup bool `json:"open_on_startup"`
	Pinned        bool `json:"pinned"`
}

func DefaultDanmakuWindowConfig() DanmakuWindowConfig {
	return DanmakuWindowConfig{Width: 1000, Height: 560}
}

func (c DanmakuWindowConfig) Validate() error {
	if c.Width < 420 || c.Width > 8192 {
		return fmt.Errorf("弹幕展示窗口宽度必须在 420 到 8192 之间")
	}
	if c.Height < 180 || c.Height > 8192 {
		return fmt.Errorf("弹幕展示窗口高度必须在 180 到 8192 之间")
	}
	if c.X < -32768 || c.X > 32768 || c.Y < -32768 || c.Y > 32768 {
		return fmt.Errorf("弹幕展示窗口位置超出有效范围")
	}
	return nil
}
