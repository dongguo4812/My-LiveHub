package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
)

// App is the application service container and owns module lifecycle.
type App struct {
	configManager *config.Manager
	config        config.AppConfig
	modules       *ModuleManager
}

func New(configManager *config.Manager, appConfig config.AppConfig) (*App, error) {
	if configManager == nil {
		return nil, errors.New("configuration manager is required")
	}
	if err := appConfig.Validate(); err != nil {
		return nil, fmt.Errorf("validate application configuration: %w", err)
	}
	return &App{
		configManager: configManager,
		config:        appConfig,
		modules:       NewModuleManager(),
	}, nil
}

func (a *App) Config() config.AppConfig {
	return a.config
}

func (a *App) ConfigManager() *config.Manager {
	return a.configManager
}

func (a *App) Modules() *ModuleManager {
	return a.modules
}

func (a *App) Start(ctx context.Context) error {
	if a == nil {
		return errors.New("application is required")
	}
	return a.modules.StartAll(ctx)
}

func (a *App) Shutdown(ctx context.Context) error {
	if a == nil {
		return nil
	}
	return a.modules.StopAll(ctx)
}
