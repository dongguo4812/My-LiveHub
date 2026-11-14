package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
			m.setStatus(entry, ModuleFailed, startErr)
			rollbackErr := m.stopEntries(ctx, started)
			m.mu.Lock()
			m.stopped = true
			m.mu.Unlock()
			return errors.Join(startErr, rollbackErr)
		}
		started = append(started, entry)
		m.setStatus(entry, ModuleRunning, nil)
	}
	return nil
}

// StartModule starts one registered module without changing the lifecycle of others.
func (m *ModuleManager) StartModule(ctx context.Context, name string) error {
	if m == nil { return errors.New("module manager is required") }
	if ctx == nil { return errors.New("module startup context is required") }
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.RLock()
	if !m.started || m.stopped { m.mu.RUnlock(); return errors.New("application module lifecycle is not running") }
	entry := m.byName[name]
	m.mu.RUnlock()
	if entry == nil { return fmt.Errorf("module %q is not registered", name) }
	status := m.statusOf(entry)
	if status == ModuleRunning { return nil }
	if status == ModuleStarting || status == ModuleStopping { return fmt.Errorf("module %q is busy (status %q)", name, status) }
	m.setStatus(entry, ModuleStarting, nil)
	if err := entry.module.Start(ctx); err != nil {
		startErr := fmt.Errorf("start module %q: %w", name, err)
		m.setStatus(entry, ModuleFailed, startErr)
		return startErr
	}
	m.setStatus(entry, ModuleRunning, nil)
	return nil
}

// StopModule stops one registered module without shutting down the application.
func (m *ModuleManager) StopModule(ctx context.Context, name string) error {
	if m == nil { return errors.New("module manager is required") }
	if ctx == nil { return errors.New("module shutdown context is required") }
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.RLock()
	if !m.started || m.stopped { m.mu.RUnlock(); return errors.New("application module lifecycle is not running") }
	entry := m.byName[name]
	m.mu.RUnlock()
	if entry == nil { return fmt.Errorf("module %q is not registered", name) }
	status := m.statusOf(entry)
	if status == ModuleStopped || status == ModuleRegistered { return nil }
	if status != ModuleRunning { return fmt.Errorf("module %q cannot stop from status %q", name, status) }
	m.setStatus(entry, ModuleStopping, nil)
	if err := entry.module.Stop(ctx); err != nil {
		stopErr := fmt.Errorf("stop module %q: %w", name, err)
		m.setStatus(entry, ModuleFailed, stopErr)
		return stopErr
	}
	m.setStatus(entry, ModuleStopped, nil)
	return nil
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
	if m.stopped {
		m.mu.Unlock()
		return nil
	}
	m.stopped = true
	modules := append([]*registeredModule(nil), m.modules...)
	m.mu.Unlock()

	var started []*registeredModule
	for _, entry := range modules {
		if entryStatus := m.statusOf(entry); entryStatus == ModuleRunning || entryStatus == ModuleFailed {
			if entryStatus == ModuleRunning {
				started = append(started, entry)
			}
		}
	}
	return m.stopEntries(ctx, started)
}

func (m *ModuleManager) stopEntries(ctx context.Context, entries []*registeredModule) error {
	var stopErrors []error
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		m.setStatus(entry, ModuleStopping, nil)
		if err := entry.module.Stop(ctx); err != nil {
			stopErr := fmt.Errorf("stop module %q: %w", entry.name, err)
			m.setStatus(entry, ModuleFailed, stopErr)
			stopErrors = append(stopErrors, stopErr)
			continue
		}
		m.setStatus(entry, ModuleStopped, nil)
	}
	return errors.Join(stopErrors...)
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
