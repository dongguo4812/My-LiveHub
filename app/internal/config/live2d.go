package config

import (
	"fmt"
	"strings"
)

type Live2DConfig struct {
	Enabled bool `json:"enabled"`
	RuntimeVersion string `json:"runtime_version"`
	DefaultModelMOC2 string `json:"default_model_moc2"`
	DefaultModelMOC3 string `json:"default_model_moc3"`
	MicrophoneEnabled bool `json:"microphone_enabled"`
	MicrophoneDeviceID string `json:"microphone_device_id"`
	MicrophoneSensitivity float64 `json:"microphone_sensitivity"`
	MicrophoneSmoothing float64 `json:"microphone_smoothing"`
}

func DefaultLive2DConfig() Live2DConfig {
	return Live2DConfig{
		Enabled: true,
		RuntimeVersion: "moc3",
		MicrophoneSensitivity: 1.8,
		MicrophoneSmoothing: 0.65,
	}
}

func (c Live2DConfig) Validate() error {
	if c.RuntimeVersion != "moc2" && c.RuntimeVersion != "moc3" {
		return fmt.Errorf("Live2D 运行时版本必须为 moc2 或 moc3")
	}
	if c.MicrophoneSensitivity < 0.1 || c.MicrophoneSensitivity > 10 {
		return fmt.Errorf("麦克风灵敏度必须在 0.1 到 10 之间")
	}
	if c.MicrophoneSmoothing < 0 || c.MicrophoneSmoothing > 0.99 {
		return fmt.Errorf("口型平滑度必须在 0 到 0.99 之间")
	}
	if strings.TrimSpace(c.DefaultModelMOC2) != c.DefaultModelMOC2 || strings.TrimSpace(c.DefaultModelMOC3) != c.DefaultModelMOC3 {
		return fmt.Errorf("Live2D 模型标识不能包含首尾空格")
	}
	return nil
}
