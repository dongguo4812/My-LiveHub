package config

import "testing"

func TestDefaultMusicConfigIsValid(t *testing.T) {
	cfg := DefaultMusicConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Fatal("music module should be opt-in by default")
	}
	if len(cfg.Players) != 5 {
		t.Fatalf("expected five independently configurable players, got %d", len(cfg.Players))
	}
}

func TestMusicConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		mutate func(*MusicConfig)
	}{
		{name:"empty command", mutate:func(c *MusicConfig){ c.CommandPrefix = " " }},
		{name:"queue too large", mutate:func(c *MusicConfig){ c.QueueCapacity = 1001 }},
		{name:"search limit too large", mutate:func(c *MusicConfig){ c.SearchLimit = 51 }},
		{name:"disabled default player", mutate:func(c *MusicConfig){ c.DefaultPlayer = "luoxue"; c.Players["luoxue"] = false }},
		{name:"unknown default player", mutate:func(c *MusicConfig){ c.DefaultPlayer = "unknown" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultMusicConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
