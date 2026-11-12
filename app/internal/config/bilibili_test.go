package config

import "testing"

func TestDefaultBilibiliConfigHasFanMedalLevel(t *testing.T) {
	c := DefaultBilibiliConfig()
	if c.VideoFanMedalLevel != 1 { t.Fatalf("expected default video fan medal level 1, got %d", c.VideoFanMedalLevel) }
	if err := c.Validate(); err != nil { t.Fatal(err) }
}

func TestBilibiliConfigFanMedalLevelValidation(t *testing.T) {
	c := DefaultBilibiliConfig()
	c.VideoRequireFanMedal = true
	c.VideoFanMedalLevel = 0
	if err := c.Validate(); err == nil { t.Fatal("expected invalid required medal level to be rejected") }
	c.VideoFanMedalLevel = 120
	if err := c.Validate(); err != nil { t.Fatalf("valid maximum medal level rejected: %v", err) }
	c.VideoFanMedalLevel = 121
	if err := c.Validate(); err == nil { t.Fatal("expected video medal level above 120 to be rejected") }
}
