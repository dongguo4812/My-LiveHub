package desktop

import "testing"

func TestValidateLayoutConfig(t *testing.T) {
	valid := LayoutConfig{Scenes: []LayoutScene{{
		ID: "main", Name: "主场景", Width: 1920, Height: 1080, Background: "transparent",
		Layers: []LayoutLayer{{ID: "title", Type: "text", Name: "标题", Width: 400, Height: 80, Visible: true, Opacity: 1}},
	}}}
	if err := validateLayoutConfig(valid); err != nil {
		t.Fatalf("valid layout rejected: %v", err)
	}
}

func TestValidateLayoutConfigRejectsInvalidScenes(t *testing.T) {
	tests := []struct {
		name  string
		value LayoutConfig
	}{
		{name: "no scenes", value: LayoutConfig{}},
		{name: "duplicate scene IDs", value: LayoutConfig{Scenes: []LayoutScene{
			{ID: "same", Name: "场景一", Width: 1920, Height: 1080},
			{ID: "same", Name: "场景二", Width: 1920, Height: 1080},
		}}},
		{name: "unsupported layer", value: LayoutConfig{Scenes: []LayoutScene{{
			ID: "main", Name: "主场景", Width: 1920, Height: 1080,
			Layers: []LayoutLayer{{ID: "bad", Type: "video", Width: 100, Height: 100, Opacity: 1}},
		}}}},
		{name: "invalid opacity", value: LayoutConfig{Scenes: []LayoutScene{{
			ID: "main", Name: "主场景", Width: 1920, Height: 1080,
			Layers: []LayoutLayer{{ID: "bad", Type: "text", Width: 100, Height: 100, Opacity: 1.5}},
		}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateLayoutConfig(test.value); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
