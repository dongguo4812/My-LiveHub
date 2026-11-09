package config

import "testing"

func TestDanmakuWindowConfigDefaultsClosed(t *testing.T) {
	value := DefaultDanmakuWindowConfig()
	if value.OpenOnStartup {
		t.Fatal("danmaku window should be closed by default")
	}
	if err := value.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
}

func TestDanmakuWindowConfigValidatesOpenPreferenceWithoutChangingGeometryRules(t *testing.T) {
	value := DefaultDanmakuWindowConfig()
	value.OpenOnStartup = true
	value.PositionSaved = true
	value.X, value.Y = -1200, 80
	if err := value.Validate(); err != nil {
		t.Fatalf("valid saved window config rejected: %v", err)
	}
}
