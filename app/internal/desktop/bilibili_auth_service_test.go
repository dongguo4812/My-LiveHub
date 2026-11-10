package desktop

import (
	"context"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/dongguo4812/My-LiveHub/app/internal/platform"
)

type authServiceTestAdapter struct {
	connects int
}

func (a *authServiceTestAdapter) ID() string { return "bilibili" }
func (a *authServiceTestAdapter) Connect(context.Context) error {
	a.connects++
	return nil
}
func (a *authServiceTestAdapter) Disconnect(context.Context) error { return nil }

func TestBilibiliAuthServiceConnectsAfterLogin(t *testing.T) {
	manager := platform.NewManager()
	adapter := &authServiceTestAdapter{}
	if err := manager.Register(adapter); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	service := NewBilibiliAuthService(nil, manager)
	service.connectAfterLogin()

	snapshot := manager.Snapshot()
	if adapter.connects != 1 {
		t.Fatalf("expected one automatic connection attempt, got %d", adapter.connects)
	}
	if len(snapshot) != 1 || snapshot[0].Status != platform.StatusConnected {
		t.Fatalf("expected connected platform snapshot, got %#v", snapshot)
	}
}

func TestBilibiliAuthServiceStartupWithoutAuthDoesNotConnect(t *testing.T) {
	manager := platform.NewManager()
	adapter := &authServiceTestAdapter{}
	if err := manager.Register(adapter); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	service := NewBilibiliAuthService(nil, manager)
	if err := service.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	if adapter.connects != 0 {
		t.Fatalf("expected no connection without an authentication manager, got %d", adapter.connects)
	}
}
