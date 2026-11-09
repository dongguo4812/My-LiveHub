package core

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	manager, err := config.NewManager(fstest.MapFS{
		"app.json": &fstest.MapFile{Data: []byte(`{"config_version":1,"appearance":{"theme":"dark","accent_color":"indigo"}}`)},
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app, err := New(manager, config.AppConfig{ConfigVersion: config.CurrentAppConfigVersion, Appearance: config.DefaultAppearanceConfig()})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func TestNewRequiresConfigurationManagerAndValidConfig(t *testing.T) {
	app := newTestApp(t)
	if _, err := New(nil, config.AppConfig{ConfigVersion: 1}); err == nil {
		t.Fatal("expected missing configuration manager to fail")
	}
	if _, err := New(app.ConfigManager(), config.AppConfig{ConfigVersion: 99}); err == nil {
		t.Fatal("expected invalid application config to fail")
	}
	if app.Config().ConfigVersion != config.CurrentAppConfigVersion {
		t.Fatalf("unexpected app config: %+v", app.Config())
	}
	if app.ConfigManager() == nil {
		t.Fatal("configuration manager was not retained")
	}
	if app.Modules() == nil {
		t.Fatal("module manager was not initialized")
	}
	if got := app.Snapshot().Status; got != AppCreated {
		t.Fatalf("initial status = %q, want %q", got, AppCreated)
	}
}

func TestAppLifecycleStatusTransitions(t *testing.T) {
	app := newTestApp(t)
	var calls []string
	if err := app.Modules().Register("sample", testModule{name: "sample", calls: &calls}); err != nil {
		t.Fatal(err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := app.Snapshot().Status; got != AppRunning {
		t.Fatalf("status after Start = %q, want %q", got, AppRunning)
	}
	if err := app.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := app.Snapshot().Status; got != AppStopped {
		t.Fatalf("status after Shutdown = %q, want %q", got, AppStopped)
	}
}

func TestAppReportsFailedStartup(t *testing.T) {
	app := newTestApp(t)
	startFailure := context.Canceled
	if err := app.Modules().Register("failing", testModule{
		name: "failing", calls: new([]string), startFail: startFailure,
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.Start(context.Background()); err == nil {
		t.Fatal("expected startup failure")
	}
	if got := app.Snapshot().Status; got != AppFailed {
		t.Fatalf("status after failed Start = %q, want %q", got, AppFailed)
	}
}
