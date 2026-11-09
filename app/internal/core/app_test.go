package core

import (
	"testing"
	"testing/fstest"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
)

func TestNewRequiresConfigurationManagerAndValidConfig(t *testing.T) {
	validManager, err := config.NewManager(fstest.MapFS{
		"app.json": &fstest.MapFile{Data: []byte(`{"config_version":1}`)},
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(nil, config.AppConfig{ConfigVersion: 1}); err == nil {
		t.Fatal("expected missing configuration manager to fail")
	}
	if _, err := New(validManager, config.AppConfig{ConfigVersion: 99}); err == nil {
		t.Fatal("expected invalid application config to fail")
	}
	app, err := New(validManager, config.AppConfig{ConfigVersion: config.CurrentAppConfigVersion})
	if err != nil {
		t.Fatal(err)
	}
	if app.Config().ConfigVersion != config.CurrentAppConfigVersion {
		t.Fatalf("unexpected app config: %+v", app.Config())
	}
	if app.ConfigManager() != validManager {
		t.Fatal("configuration manager was not retained")
	}
	if app.Modules() == nil {
		t.Fatal("module manager was not initialized")
	}
}
