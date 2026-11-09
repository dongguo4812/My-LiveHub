package desktop

import (
	"context"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/core"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// CoreService exposes read-only core state to the frontend and connects the
// core lifecycle to Wails' service lifecycle.
type CoreService struct {
	app *core.App
}

func NewCoreService(app *core.App) *CoreService {
	return &CoreService{app: app}
}

func (s *CoreService) GetStatus() core.AppSnapshot {
	return s.app.Snapshot()
}

func (s *CoreService) GetModules() []core.ModuleSnapshot {
	return s.app.Modules().Snapshot()
}

func (s *CoreService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	return s.app.Start(ctx)
}

func (s *CoreService) ServiceShutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.app.Shutdown(ctx)
}
