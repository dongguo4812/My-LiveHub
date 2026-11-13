package config

import (
	"encoding/json"
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
	ModelScaleMOC2 float64 `json:"model_scale_moc2"`
	ModelOffsetXMOC2 float64 `json:"model_offset_x_moc2"`
	ModelOffsetYMOC2 float64 `json:"model_offset_y_moc2"`
	ModelScaleMOC3 float64 `json:"model_scale_moc3"`
	ModelOffsetXMOC3 float64 `json:"model_offset_x_moc3"`
	ModelOffsetYMOC3 float64 `json:"model_offset_y_moc3"`
}

func DefaultLive2DConfig() Live2DConfig {
	return Live2DConfig{
		Enabled: true,
		RuntimeVersion: "moc3",
		MicrophoneSensitivity: 1.8,
		MicrophoneSmoothing: 0.65,
		ModelScaleMOC2: 0.65,
		ModelScaleMOC3: 0.65,
	}
}

// UnmarshalJSON migrates legacy shared transform values into the active runtime.
func (c *Live2DConfig) UnmarshalJSON(data []byte) error {
	type plain Live2DConfig
	defaults := plain(DefaultLive2DConfig())
	if err := json.Unmarshal(data, &defaults); err != nil {
		return err
	}
	*c = Live2DConfig(defaults)

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"model_scale_moc2", "model_offset_x_moc2", "model_offset_y_moc2", "model_scale_moc3", "model_offset_x_moc3", "model_offset_y_moc3"} {
		if _, exists := fields[key]; exists {
			return nil
		}
	}

	var legacy struct {
		Scale   *float64 `json:"model_scale"`
		OffsetX *float64 `json:"model_offset_x"`
		OffsetY *float64 `json:"model_offset_y"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if legacy.Scale == nil && legacy.OffsetX == nil && legacy.OffsetY == nil {
		return nil
	}
	scale, x, y := c.ModelScaleMOC3, 0.0, 0.0
	if legacy.Scale != nil { scale = *legacy.Scale }
	if legacy.OffsetX != nil { x = *legacy.OffsetX }
	if legacy.OffsetY != nil { y = *legacy.OffsetY }
	if c.RuntimeVersion == "moc2" {
		c.ModelScaleMOC2, c.ModelOffsetXMOC2, c.ModelOffsetYMOC2 = scale, x, y
	} else {
		c.ModelScaleMOC3, c.ModelOffsetXMOC3, c.ModelOffsetYMOC3 = scale, x, y
	}
	return nil
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
	if c.ModelScaleMOC2 < 0.1 || c.ModelScaleMOC2 > 2.5 || c.ModelScaleMOC3 < 0.1 || c.ModelScaleMOC3 > 2.5 {
		return fmt.Errorf("模型缩放比例必须在 0.1 到 2.5 之间")
	}
	if c.ModelOffsetXMOC2 < -500 || c.ModelOffsetXMOC2 > 500 || c.ModelOffsetYMOC2 < -500 || c.ModelOffsetYMOC2 > 500 || c.ModelOffsetXMOC3 < -500 || c.ModelOffsetXMOC3 > 500 || c.ModelOffsetYMOC3 < -500 || c.ModelOffsetYMOC3 > 500 {
		return fmt.Errorf("模型位置偏移必须在 -500 到 500 之间")
	}
	if strings.TrimSpace(c.DefaultModelMOC2) != c.DefaultModelMOC2 || strings.TrimSpace(c.DefaultModelMOC3) != c.DefaultModelMOC3 {
		return fmt.Errorf("Live2D 模型标识不能包含首尾空格")
	}
	return nil
}
