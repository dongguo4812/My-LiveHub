package core

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
)

type AppStatus string

const (
	AppCreated  AppStatus = "created"
	AppStarting AppStatus = "starting"
	AppRunning  AppStatus = "running"
	AppStopping AppStatus = "stopping"
	AppStopped  AppStatus = "stopped"
	AppFailed   AppStatus = "failed"
)

type AppSnapshot struct {
	Status        AppStatus `json:"status"`
	ConfigVersion int       `json:"config_version"`
}

type App struct {
	configManager *config.Manager
	config        config.AppConfig
	modules       *ModuleManager

	mu     sync.RWMutex
	status AppStatus
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
		status:        AppCreated,
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

func (a *App) Snapshot() AppSnapshot {
	if a == nil {
		return AppSnapshot{Status: AppFailed}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return AppSnapshot{Status: a.status, ConfigVersion: a.config.ConfigVersion}
}

func (a *App) Start(ctx context.Context) error {
	if a == nil {
		return errors.New("application is required")
	}
	if ctx == nil {
		return errors.New("startup context is required")
	}
	a.mu.Lock()
	if a.status != AppCreated {
		status := a.status
		a.mu.Unlock()
		return fmt.Errorf("application cannot start from status %q", status)
	}
	a.status = AppStarting
	a.mu.Unlock()

	if err := a.modules.StartAll(ctx); err != nil {
		a.mu.Lock()
		a.status = AppFailed
		a.mu.Unlock()
		return err
	}
	a.mu.Lock()
	a.status = AppRunning
	a.mu.Unlock()
	return nil
}

func (a *App) Shutdown(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("shutdown context is required")
	}
	a.mu.Lock()
	if a.status == AppStopped {
		a.mu.Unlock()
		return nil
	}
	a.status = AppStopping
	a.mu.Unlock()

	err := a.modules.StopAll(ctx)
	a.mu.Lock()
	if err != nil {
		a.status = AppFailed
	} else {
		a.status = AppStopped
	}
	a.mu.Unlock()
	return err
}
