package config

import "testing"

func TestLive2DConfigValidation(t *testing.T) {
	cfg := DefaultLive2DConfig()
	if err := cfg.Validate(); err != nil { t.Fatalf("default config invalid: %v", err) }
	cfg.RuntimeVersion = "moc4"
	if err := cfg.Validate(); err == nil { t.Fatal("expected invalid runtime version") }
	cfg = DefaultLive2DConfig()
	cfg.MicrophoneSensitivity = 11
	if err := cfg.Validate(); err == nil { t.Fatal("expected invalid microphone sensitivity") }
	cfg = DefaultLive2DConfig()
	cfg.MicrophoneSmoothing = 1
	if err := cfg.Validate(); err == nil { t.Fatal("expected invalid microphone smoothing") }
}
