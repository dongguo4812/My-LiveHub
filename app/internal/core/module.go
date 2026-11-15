package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Module interface {
	Start(context.Context) error
	Stop(context.Context) error
}

// ManualStartModule opts out of automatic application startup while remaining
// available through StartModule/StopModule for explicit user control.
type ManualStartModule interface {
	StartOnApplicationLaunch() bool
}

type ModuleStatus string

const (
	ModuleRegistered ModuleStatus = "registered"
	ModuleStarting   ModuleStatus = "starting"
	ModuleRunning    ModuleStatus = "running"
	ModuleStopping   ModuleStatus = "stopping"
	ModuleStopped    ModuleStatus = "stopped"
	ModuleFailed     ModuleStatus = "failed"
)

const moduleCleanupTimeout = 30 * time.Second

type ModuleSnapshot struct {
	Name   string       `json:"name"`
	Status ModuleStatus `json:"status"`
	Error  string       `json:"error,omitempty"`
}

type registeredModule struct {
	name   string
	module Module
	status ModuleStatus
	err    error
}

// ModuleManager starts modules in registration order and stops them in reverse order.
type ModuleManager struct {
	lifecycleMu sync.Mutex
	mu          sync.RWMutex
	modules     []*registeredModule
	byName      map[string]*registeredModule
	started     bool
	stopped     bool
}

func NewModuleManager() *ModuleManager {
	return &ModuleManager{byName: make(map[string]*registeredModule)}
}

func (m *ModuleManager) Register(name string, module Module) error {
	if m == nil {
		return errors.New("module manager is required")
	}
	if name == "" {
		return errors.New("module name is required")
	}
	if module == nil {
		return fmt.Errorf("module %q is required", name)
	}

	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started || m.stopped {
		return errors.New("modules cannot be registered after lifecycle has started")
	}
	if _, exists := m.byName[name]; exists {
		return fmt.Errorf("module %q is already registered", name)
	}
	entry := &registeredModule{name: name, module: module, status: ModuleRegistered}
	m.modules = append(m.modules, entry)
	m.byName[name] = entry
	return nil
}

func (m *ModuleManager) StartAll(ctx context.Context) error {
	if m == nil {
		return errors.New("module manager is required")
	}
	if ctx == nil {
		return errors.New("startup context is required")
	}

	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	if m.started || m.stopped {
		m.mu.Unlock()
		return errors.New("module lifecycle has already started")
	}
	m.started = true
	modules := append([]*registeredModule(nil), m.modules...)
	m.mu.Unlock()

	started := make([]*registeredModule, 0, len(modules))
	for _, entry := range modules {
		if manual, ok := entry.module.(ManualStartModule); ok && !manual.StartOnApplicationLaunch() {
			m.setStatus(entry, ModuleStopped, nil)
			continue
		}
		m.setStatus(entry, ModuleStarting, nil)
		if err := entry.module.Start(ctx); err != nil {
			startErr := fmt.Errorf("start module %q: %w", entry.name, err)
			cleanupCtx, cancel := newModuleCleanupContext(ctx)
			cleanupErr := m.cleanupFailedStart(cleanupCtx, entry, startErr)
			rollbackErr := m.stopEntries(cleanupCtx, started)
			cancel()

			m.mu.Lock()
			m.stopped = true
			m.mu.Unlock()
			return errors.Join(startErr, cleanupErr, rollbackErr)
		}
		started = append(started, entry)
		m.setStatus(entry, ModuleRunning, nil)
	}
	return nil
}

// StartModule starts one registered module without changing the lifecycle of others.
func (m *ModuleManager) StartModule(ctx context.Context, name string) error {
	if m == nil {
		return errors.New("module manager is required")
	}
	if ctx == nil {
		return errors.New("module startup context is required")
	}
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.RLock()
	if !m.started || m.stopped {
		m.mu.RUnlock()
		return errors.New("application module lifecycle is not running")
	}
	entry := m.byName[name]
	m.mu.RUnlock()
	if entry == nil {
		return fmt.Errorf("module %q is not registered", name)
	}

	status := m.statusOf(entry)
	if status == ModuleRunning {
		return nil
	}
	if status == ModuleStarting || status == ModuleStopping {
		return fmt.Errorf("module %q is busy (status %q)", name, status)
	}
	if status == ModuleFailed {
		// A previous Start may have left partial resources behind. Clean them
		// before attempting another Start instead of stacking another instance.
		cleanupCtx, cancel := newModuleCleanupContext(ctx)
		m.setStatus(entry, ModuleStopping, nil)
		if err := entry.module.Stop(cleanupCtx); err != nil {
			cancel()
			stopErr := fmt.Errorf("cleanup failed module %q before restart: %w", name, err)
			m.setStatus(entry, ModuleFailed, stopErr)
			return stopErr
		}
		cancel()
		m.setStatus(entry, ModuleStopped, nil)
	}

	m.setStatus(entry, ModuleStarting, nil)
	if err := entry.module.Start(ctx); err != nil {
		startErr := fmt.Errorf("start module %q: %w", name, err)
		cleanupCtx, cancel := newModuleCleanupContext(ctx)
		cleanupErr := m.cleanupFailedStart(cleanupCtx, entry, startErr)
		cancel()
		return errors.Join(startErr, cleanupErr)
	}
	m.setStatus(entry, ModuleRunning, nil)
	return nil
}

// StopModule stops one registered module without shutting down the application.
// Failed modules remain stoppable even after StartAll has rolled back the application.
func (m *ModuleManager) StopModule(ctx context.Context, name string) error {
	if m == nil {
		return errors.New("module manager is required")
	}
	if ctx == nil {
		return errors.New("module shutdown context is required")
	}
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.RLock()
	entry := m.byName[name]
	started, stopped := m.started, m.stopped
	m.mu.RUnlock()
	if entry == nil {
		return fmt.Errorf("module %q is not registered", name)
	}

	status := m.statusOf(entry)
	if status == ModuleStopped || status == ModuleRegistered {
		return nil
	}
	if status == ModuleStarting || status == ModuleStopping {
		return fmt.Errorf("module %q is busy (status %q)", name, status)
	}
	if status != ModuleFailed && (!started || stopped) {
		return errors.New("application module lifecycle is not running")
	}

	return m.stopEntry(ctx, entry)
}

func (m *ModuleManager) StopAll(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("shutdown context is required")
	}

	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	m.stopped = true
	modules := append([]*registeredModule(nil), m.modules...)
	m.mu.Unlock()

	var stoppable []*registeredModule
	for _, entry := range modules {
		status := m.statusOf(entry)
		if status == ModuleRunning || status == ModuleFailed {
			stoppable = append(stoppable, entry)
		}
	}
	return m.stopEntries(ctx, stoppable)
}

func newModuleCleanupContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(parent), moduleCleanupTimeout)
}

// cleanupFailedStart attempts to undo resources created by a Start call that
// returned an error. A successful cleanup does not erase the startup failure:
// the module stays Failed and can be explicitly stopped or retried later.
func (m *ModuleManager) cleanupFailedStart(ctx context.Context, entry *registeredModule, startErr error) error {
	if err := entry.module.Stop(ctx); err != nil {
		cleanupErr := fmt.Errorf("cleanup module %q after startup failure: %w", entry.name, err)
		m.setStatus(entry, ModuleFailed, errors.Join(startErr, cleanupErr))
		return cleanupErr
	}
	m.setStatus(entry, ModuleFailed, startErr)
	return nil
}

func (m *ModuleManager) stopEntries(ctx context.Context, entries []*registeredModule) error {
	var stopErrors []error
	for i := len(entries) - 1; i >= 0; i-- {
		if err := m.stopEntry(ctx, entries[i]); err != nil {
			stopErrors = append(stopErrors, err)
		}
	}
	return errors.Join(stopErrors...)
}

func (m *ModuleManager) stopEntry(ctx context.Context, entry *registeredModule) error {
	m.setStatus(entry, ModuleStopping, nil)
	if err := entry.module.Stop(ctx); err != nil {
		stopErr := fmt.Errorf("stop module %q: %w", entry.name, err)
		m.setStatus(entry, ModuleFailed, stopErr)
		return stopErr
	}
	m.setStatus(entry, ModuleStopped, nil)
	return nil
}

func (m *ModuleManager) setStatus(entry *registeredModule, status ModuleStatus, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry.status = status
	entry.err = err
}

func (m *ModuleManager) statusOf(entry *registeredModule) ModuleStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return entry.status
}

func (m *ModuleManager) Snapshot() []ModuleSnapshot {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]ModuleSnapshot, 0, len(m.modules))
	for _, entry := range m.modules {
		snapshot := ModuleSnapshot{Name: entry.name, Status: entry.status}
		if entry.err != nil {
			snapshot.Error = entry.err.Error()
		}
		result = append(result, snapshot)
	}
	return result
}
