package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
)

func TestLive2DListModelsSeparatesMOC2AndMOC3(t *testing.T) {
	root := t.TempDir()
	for _, item := range []struct{ format, id, entry string }{
		{"moc2", "legacy-avatar", "legacy-avatar.model.json"},
		{"moc3", "modern-avatar", "model3.json"},
	} {
		dir := filepath.Join(root, item.format, item.id)
		if err := os.MkdirAll(dir, 0o700); err != nil { t.Fatal(err) }
		if err := os.WriteFile(filepath.Join(dir, item.entry), []byte("{}"), 0o600); err != nil { t.Fatal(err) }
	}
	service := NewLive2DService(nil, config.DefaultLive2DConfig(), root)
	models, err := service.ListModels()
	if err != nil { t.Fatal(err) }
	if len(models) != 2 { t.Fatalf("expected 2 models, got %d", len(models)) }
	if models[0].Format != "moc2" || models[1].Format != "moc3" { t.Fatalf("unexpected model formats: %#v", models) }
	for _, model := range models {
		if !model.RuntimeAvailable { t.Fatalf("local model asset server should expose a runtime URL: %#v", model) }
		if model.CapabilityStatus == "" { t.Fatalf("capability status must be explicit: %#v", model) }
		if model.ModelURL == "" { t.Fatalf("model asset URL must be exposed: %#v", model) }
	}
}

func TestLive2DListModelsIgnoresFoldersWithoutManifest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "models")
	if err := os.MkdirAll(filepath.Join(root, "moc3", "incomplete"), 0o700); err != nil { t.Fatal(err) }
	service := NewLive2DService(nil, config.DefaultLive2DConfig(), root)
	models, err := service.ListModels()
	if err != nil { t.Fatal(err) }
	if len(models) != 0 { t.Fatalf("expected incomplete folder to be ignored, got %#v", models) }
}
