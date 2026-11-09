package config

import "testing"

func TestDanmakuDisplayConfigDefaultsAreValid(t *testing.T) {
	value:=DefaultDanmakuDisplayConfig()
	if err:=value.Validate();err!=nil { t.Fatalf("default config invalid: %v",err) }
	if !value.ShowPlatformBadge { t.Fatal("platform badges should be enabled by default") }
}

func TestDanmakuDisplayConfigRejectsOutOfRangeValues(t *testing.T) {
	base:=DefaultDanmakuDisplayConfig()
	tests:=[]struct{name string; update func(*DanmakuDisplayConfig)}{
		{"font size",func(c *DanmakuDisplayConfig){c.FontSize=11}},
		{"display duration",func(c *DanmakuDisplayConfig){c.DisplayDurationSeconds=61}},
		{"gift highlight",func(c *DanmakuDisplayConfig){c.GiftHighlightMinutes=0}},
		{"gift merge",func(c *DanmakuDisplayConfig){c.GiftMergeSeconds=121}},
		{"duplicate filter",func(c *DanmakuDisplayConfig){c.DuplicateDanmakuFilterSeconds=-1}},
		{"custom css size",func(c *DanmakuDisplayConfig){c.CustomCSS=string(make([]byte,65537))}},
	}
	for _,tt:=range tests { t.Run(tt.name,func(t *testing.T){value:=base;tt.update(&value);if err:=value.Validate();err==nil {t.Fatal("expected validation error")} }) }
}
