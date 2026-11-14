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

func TestReadLive2DManifestMetadata(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "model3.json")
	content := `{
		"FileReferences": {
			"Expressions": [{"Name":"F01","File":"expressions/F01.exp3.json"}],
			"Motions": {"Idle":[{"File":"motions/idle.motion3.json"},{"File":"motions/idle2.motion3.json"}],"TapBody":[{"File":"motions/tap.motion3.json"}]},
			"Physics":"physics/physics3.json",
			"Pose":"pose/pose3.json"
		},
		"HitAreas":[{"Name":"Head","Id":"HitAreaHead"},{"Name":"Body","Id":"HitAreaBody"}]
	}`
	if err := os.WriteFile(manifest, []byte(content), 0o600); err != nil { t.Fatal(err) }
	metadata := readLive2DManifest(manifest, "moc3")
	if len(metadata.Expressions) != 1 || metadata.Expressions[0] != "F01" { t.Fatalf("unexpected expressions: %#v", metadata.Expressions) }
	if len(metadata.MotionGroups) != 2 || metadata.MotionGroups[0].Count == 0 { t.Fatalf("unexpected motion groups: %#v", metadata.MotionGroups) }
	if !metadata.HasPhysics || !metadata.HasPose || len(metadata.HitAreas) != 2 { t.Fatalf("capabilities were not detected: %#v", metadata) }
}

func TestReadLive2DManifestMetadataSupportsMOC2LowercaseKeys(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "model.model.json")
	content := `{"expressions":[{"name":"smile","file":"smile.exp.json"}],"motions":{"idle":[{"file":"idle.mtn"}]},"physics":"physics.json"}`
	if err := os.WriteFile(manifest, []byte(content), 0o600); err != nil { t.Fatal(err) }
	metadata := readLive2DManifest(manifest, "moc2")
	if len(metadata.Expressions) != 1 || metadata.Expressions[0] != "smile" { t.Fatalf("unexpected MOC2 expressions: %#v", metadata.Expressions) }
	if len(metadata.MotionGroups) != 1 || metadata.MotionGroups[0].Name != "idle" { t.Fatalf("unexpected MOC2 motions: %#v", metadata.MotionGroups) }
	if !metadata.HasPhysics { t.Fatal("expected MOC2 physics capability") }
}


func TestLive2DServiceLifecycleControlsAssetsAndCanRestart(t *testing.T) {
	root := t.TempDir()
	modelDir := filepath.Join(root, "moc3", "avatar")
	if err := os.MkdirAll(modelDir, 0o700); err != nil { t.Fatal(err) }
	manifest := filepath.Join(modelDir, "model3.json")
	if err := os.WriteFile(manifest, []byte("{}"), 0o600); err != nil { t.Fatal(err) }

	service := NewLive2DService(nil, config.DefaultLive2DConfig(), root)
	handler := WrapLive2DAssetHandler(service, http.NotFoundHandler())
	request := func() int {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/models/moc3/avatar/model3.json", nil))
		return recorder.Code
	}
	if got := request(); got != http.StatusServiceUnavailable { t.Fatalf("asset status before start = %d, want %d", got, http.StatusServiceUnavailable) }
	if err := service.Start(); err != nil { t.Fatal(err) }
	if got := request(); got != http.StatusOK { t.Fatalf("asset status after start = %d, want %d", got, http.StatusOK) }
	if err := service.Stop(); err != nil { t.Fatal(err) }
	if got := request(); got != http.StatusServiceUnavailable { t.Fatalf("asset status after stop = %d, want %d", got, http.StatusServiceUnavailable) }
	if err := service.Start(); err != nil { t.Fatal(err) }
	if got := request(); got != http.StatusOK { t.Fatalf("asset status after restart = %d, want %d", got, http.StatusOK) }
}
