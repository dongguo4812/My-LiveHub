package platform

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dongguo4812/My-LiveHub/app/internal/core"
)

type ConnectionStatus string

const (
	StatusDisconnected  ConnectionStatus = "disconnected"
	StatusConnecting    ConnectionStatus = "connecting"
	StatusConnected     ConnectionStatus = "connected"
	StatusDisconnecting ConnectionStatus = "disconnecting"
	StatusFailed        ConnectionStatus = "failed"
)

type ConnectionSnapshot struct {
	PlatformID string           `json:"platform_id"`
	Status     ConnectionStatus `json:"status"`
	Error      string           `json:"error,omitempty"`
}

type connectionEntry struct {
	adapter Adapter
	status  ConnectionStatus
	err     error
}

// Manager owns platform adapters and serializes their connection operations.
// It is also registered as a core module so shutdown disconnects adapters.
type Manager struct {
	opMu     sync.Mutex
	mu       sync.RWMutex
	adapters []*connectionEntry
	byID     map[string]*connectionEntry
	started  bool
	stopped  bool
}

func NewManager() *Manager {
	return &Manager{byID: make(map[string]*connectionEntry)}
}

func (m *Manager) Register(adapter Adapter) error {
	if m == nil {
		return errors.New("platform manager is required")
	}
	if adapter == nil {
		return errors.New("platform adapter is required")
	}
	id := adapter.ID()
	if id == "" {
		return errors.New("platform adapter ID is required")
	}

	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started || m.stopped {
		return errors.New("platform adapters cannot be registered after lifecycle has started")
	}
	if _, exists := m.byID[id]; exists {
		return fmt.Errorf("platform adapter %q is already registered", id)
	}
	entry := &connectionEntry{adapter: adapter, status: StatusDisconnected}
	if reporter, ok := adapter.(ConnectionFailureReporter); ok {
		reporter.SetConnectionFailureHandler(func(cause error) {
			if cause == nil {
				cause = errors.New("connection ended unexpectedly")
			}
			m.setStatus(entry, StatusFailed, fmt.Errorf("platform %q connection lost: %w", id, cause))
		})
	}
	m.adapters = append(m.adapters, entry)
	m.byID[id] = entry
	return nil
}

// Start prepares the manager. Adapters remain disconnected until explicitly
// requested by the application or user.
func (m *Manager) Start(ctx context.Context) error {
	if m == nil {
		return errors.New("platform manager is required")
	}
	if ctx == nil {
		return errors.New("platform startup context is required")
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started || m.stopped {
		return errors.New("platform manager lifecycle has already started")
	}
	m.started = true
	return nil
}

func (m *Manager) Connect(ctx context.Context, platformID string) error {
	if m == nil {
		return errors.New("platform manager is required")
	}
	if ctx == nil {
		return errors.New("platform connection context is required")
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.RLock()
	if !m.started || m.stopped {
		m.mu.RUnlock()
		return errors.New("platform manager is not running")
	}
	entry := m.byID[platformID]
	m.mu.RUnlock()
	if entry == nil {
		return fmt.Errorf("platform adapter %q is not registered", platformID)
	}

	m.mu.Lock()
	if entry.status == StatusConnected {
		m.mu.Unlock()
		return nil
	}
	if entry.status != StatusDisconnected && entry.status != StatusFailed {
		status := entry.status
		m.mu.Unlock()
		return fmt.Errorf("platform %q cannot connect from status %q", platformID, status)
	}
	entry.status = StatusConnecting
	entry.err = nil
	m.mu.Unlock()

	if err := entry.adapter.Connect(ctx); err != nil {
		wrapped := fmt.Errorf("connect platform %q: %w", platformID, err)
		m.setStatus(entry, StatusFailed, wrapped)
		return wrapped
	}
	m.setStatus(entry, StatusConnected, nil)
	return nil
}

func (m *Manager) Disconnect(ctx context.Context, platformID string) error {
	if m == nil {
		return errors.New("platform manager is required")
	}
	if ctx == nil {
		return errors.New("platform disconnection context is required")
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.RLock()
	if !m.started || m.stopped {
		m.mu.RUnlock()
		return errors.New("platform manager is not running")
	}
	entry := m.byID[platformID]
	m.mu.RUnlock()
	if entry == nil {
		return fmt.Errorf("platform adapter %q is not registered", platformID)
	}

	m.mu.Lock()
	if entry.status == StatusDisconnected {
		m.mu.Unlock()
		return nil
	}
	if entry.status != StatusConnected && entry.status != StatusFailed {
		status := entry.status
		m.mu.Unlock()
		return fmt.Errorf("platform %q cannot disconnect from status %q", platformID, status)
	}
	entry.status = StatusDisconnecting
	entry.err = nil
	m.mu.Unlock()

	if err := entry.adapter.Disconnect(ctx); err != nil {
		wrapped := fmt.Errorf("disconnect platform %q: %w", platformID, err)
		m.setStatus(entry, StatusFailed, wrapped)
		return wrapped
	}
	m.setStatus(entry, StatusDisconnected, nil)
	return nil
}

// Stop disconnects active adapters in reverse registration order.
func (m *Manager) Stop(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("platform shutdown context is required")
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return nil
	}
	m.stopped = true
	wasStarted := m.started
	adapters := append([]*connectionEntry(nil), m.adapters...)
	m.mu.Unlock()
	if !wasStarted {
		return nil
	}

	var stopErrors []error
	for i := len(adapters) - 1; i >= 0; i-- {
		entry := adapters[i]
		m.mu.Lock()
		if entry.status != StatusConnected && entry.status != StatusFailed {
			m.mu.Unlock()
			continue
		}
		entry.status = StatusDisconnecting
		entry.err = nil
		m.mu.Unlock()

		if err := entry.adapter.Disconnect(ctx); err != nil {
			wrapped := fmt.Errorf("disconnect platform %q during shutdown: %w", entry.adapter.ID(), err)
			m.setStatus(entry, StatusFailed, wrapped)
			stopErrors = append(stopErrors, wrapped)
			continue
		}
		m.setStatus(entry, StatusDisconnected, nil)
	}
	return errors.Join(stopErrors...)
}

func (m *Manager) setStatus(entry *connectionEntry, status ConnectionStatus, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry.status = status
	entry.err = err
}

func (m *Manager) Snapshot() []ConnectionSnapshot {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]ConnectionSnapshot, 0, len(m.adapters))
	for _, entry := range m.adapters {
		snapshot := ConnectionSnapshot{PlatformID: entry.adapter.ID(), Status: entry.status}
		if entry.err != nil {
			snapshot.Error = entry.err.Error()
		}
		result = append(result, snapshot)
	}
	return result
}

var _ core.Module = (*Manager)(nil)
