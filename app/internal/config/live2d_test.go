package config

import (
	"encoding/json"
	"testing"
)

func TestLive2DConfigValidation(t *testing.T) {
	cfg := DefaultLive2DConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	if cfg.ModelScaleMOC2 <= 0 || cfg.ModelScaleMOC3 <= 0 {
		t.Fatal("default model scales must be positive")
	}
	cfg.RuntimeVersion = "moc4"
	if err := cfg.Validate(); err == nil { t.Fatal("expected invalid runtime version") }
	cfg = DefaultLive2DConfig()
	cfg.MicrophoneSensitivity = 11
	if err := cfg.Validate(); err == nil { t.Fatal("expected invalid microphone sensitivity") }
	cfg = DefaultLive2DConfig()
	cfg.MicrophoneSmoothing = 1
	if err := cfg.Validate(); err == nil { t.Fatal("expected invalid microphone smoothing") }
}

func TestLive2DConfigMigratesLegacyTransformToActiveRuntime(t *testing.T) {
	data := []byte(`{"runtime_version":"moc2","model_scale":1.2,"model_offset_x":15,"model_offset_y":-8}`)
	var cfg Live2DConfig
	if err := json.Unmarshal(data, &cfg); err != nil { t.Fatalf("unmarshal legacy config: %v", err) }
	if cfg.ModelScaleMOC2 != 1.2 || cfg.ModelOffsetXMOC2 != 15 || cfg.ModelOffsetYMOC2 != -8 {
		t.Fatalf("legacy values not migrated to MOC2: %+v", cfg)
	}
	if cfg.ModelScaleMOC3 != 0.65 || cfg.ModelOffsetXMOC3 != 0 || cfg.ModelOffsetYMOC3 != 0 {
		t.Fatalf("MOC3 defaults unexpectedly changed: %+v", cfg)
	}
}

func TestLive2DConfigKeepsIndependentRuntimeTransforms(t *testing.T) {
	data := []byte(`{"runtime_version":"moc3","model_scale_moc2":1.1,"model_offset_x_moc2":10,"model_offset_y_moc2":20,"model_scale_moc3":0.8,"model_offset_x_moc3":-5,"model_offset_y_moc3":-6}`)
	var cfg Live2DConfig
	if err := json.Unmarshal(data, &cfg); err != nil { t.Fatalf("unmarshal config: %v", err) }
	if cfg.ModelScaleMOC2 != 1.1 || cfg.ModelOffsetXMOC2 != 10 || cfg.ModelOffsetYMOC2 != 20 {
		t.Fatalf("MOC2 transform changed: %+v", cfg)
	}
	if cfg.ModelScaleMOC3 != 0.8 || cfg.ModelOffsetXMOC3 != -5 || cfg.ModelOffsetYMOC3 != -6 {
		t.Fatalf("MOC3 transform changed: %+v", cfg)
	}
}
