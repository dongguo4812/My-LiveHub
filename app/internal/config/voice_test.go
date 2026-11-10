package config

import "testing"

func TestDefaultVoiceConfigIsValidAndDisabled(t *testing.T) {
	cfg := DefaultVoiceConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("DefaultVoiceConfig().Validate() error = %v", err)
	}
	if cfg.Enabled {
		t.Fatal("voice should be disabled until the user enables audio")
	}
	if cfg.Engine != "edge" || cfg.Voice != "zh-CN-XiaoxiaoNeural" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestVoiceConfigRejectsInvalidEngineAndProsody(t *testing.T) {
	tests := []struct {
		name string
		edit func(*VoiceConfig)
	}{
		{"engine", func(c *VoiceConfig) { c.Engine = "" }},
		{"voice", func(c *VoiceConfig) { c.Voice = "" }},
		{"rate", func(c *VoiceConfig) { c.Rate = "+101%" }},
		{"rate sign", func(c *VoiceConfig) { c.Rate = "0%" }},
		{"volume", func(c *VoiceConfig) { c.Volume = "fast" }},
		{"pitch", func(c *VoiceConfig) { c.Pitch = "+101Hz" }},
		{"pitch sign", func(c *VoiceConfig) { c.Pitch = "0Hz" }},
		{"version", func(c *VoiceConfig) { c.ConfigVersion++ }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultVoiceConfig()
			tt.edit(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() expected an error")
			}
		})
	}
}

func TestVoiceConfigAllowsFutureEngineIdentifiers(t *testing.T) {
	cfg := DefaultVoiceConfig()
	cfg.Engine = "future-provider"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() should not hard-code the current provider: %v", err)
	}
}

func TestVoiceConfigAcceptsBoundaryProsodyValues(t *testing.T) {
	cfg := DefaultVoiceConfig()
	cfg.Rate, cfg.Volume, cfg.Pitch = "-100%", "+100%", "-100Hz"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
