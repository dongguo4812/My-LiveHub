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
	ModelScale float64 `json:"model_scale"`
	ModelOffsetX float64 `json:"model_offset_x"`
	ModelOffsetY float64 `json:"model_offset_y"`
}

func DefaultLive2DConfig() Live2DConfig {
	return Live2DConfig{
		Enabled: true,
		RuntimeVersion: "moc3",
		MicrophoneSensitivity: 1.8,
		MicrophoneSmoothing: 0.65,
		ModelScale: 0.65,
	}
}

// UnmarshalJSON migrates the former shared transform values into the active runtime\n// when reading an existing live2d.json. New configurations keep independent values.\nfunc (c *Live2DConfig) UnmarshalJSON(data []byte) error {\n\ttype plain Live2DConfig\n\tdefaults := plain(DefaultLive2DConfig())\n\tif err := json.Unmarshal(data, &defaults); err != nil { return err }\n\t*c = Live2DConfig(defaults)\n\n\tvar fields map[string]json.RawMessage\n\tif err := json.Unmarshal(data, &fields); err != nil { return err }\n\tnewTransformPresent := false\n\tfor _, key := range []string{"model_scale_moc2", "model_offset_x_moc2", "model_offset_y_moc2", "model_scale_moc3", "model_offset_x_moc3", "model_offset_y_moc3"} {\n\t\tif _, ok := fields[key]; ok { newTransformPresent = true; break }\n\t}\n\tif newTransformPresent { return nil }\n\n\tvar legacy struct {\n\t\tScale *float64 `json:"model_scale"`\n\t\tOffsetX *float64 `json:"model_offset_x"`\n\t\tOffsetY *float64 `json:"model_offset_y"`\n\t}\n\tif err := json.Unmarshal(data, &legacy); err != nil { return err }\n\tif legacy.Scale == nil && legacy.OffsetX == nil && legacy.OffsetY == nil { return nil }\n\tscale, x, y := c.ModelScaleMOC3, 0.0, 0.0\n\tif legacy.Scale != nil { scale = *legacy.Scale }\n\tif legacy.OffsetX != nil { x = *legacy.OffsetX }\n\tif legacy.OffsetY != nil { y = *legacy.OffsetY }\n\tif c.RuntimeVersion == "moc2" {\n\t\tc.ModelScaleMOC2, c.ModelOffsetXMOC2, c.ModelOffsetYMOC2 = scale, x, y\n\t} else {\n\t\tc.ModelScaleMOC3, c.ModelOffsetXMOC3, c.ModelOffsetYMOC3 = scale, x, y\n\t}\n\treturn nil\n}\n\nfunc (c Live2DConfig) Validate() error {
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
