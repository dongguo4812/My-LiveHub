//go:build lxproduction && cgo

package lxruntime

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

//go:embed testdata/sources/xinglan.js
var productionXinglanSource []byte

// SourceManager owns the long-lived QuickJS context used by the production
// music service. The context is created at module startup and the real source
// script is initialized lazily on the first URL request, so an offline source
// API cannot prevent the rest of the music module from starting.
type SourceManager struct {
	mu          sync.Mutex
	dispatchMu  sync.Mutex
	client      *http.Client
	runtime     *Runtime
	started     bool
	initialized bool
	closed      bool
}

func NewSourceManager(client *http.Client) *SourceManager {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &SourceManager{client: client}
}

func (m *SourceManager) Start(ctx context.Context) error {
	if m == nil {
		return errors.New("LX source manager is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("LX source manager is closed")
	}
	if m.started {
		return nil
	}
	runtime, err := New(Options{})
	if err != nil {
		return fmt.Errorf("create production LX QuickJS runtime: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = runtime.Close()
		return err
	}
	m.runtime = runtime
	m.started = true
	return nil
}

func (m *SourceManager) ResolvePlaybackURL(ctx context.Context, source string, info any) (any, error) {
	if m == nil {
		return nil, errors.New("LX source manager is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("LX source manager is closed")
	}
	if !m.started {
		m.mu.Unlock()
		return nil, errors.New("LX source manager has not started")
	}
	runtime := m.runtime
	if !m.initialized {
		initCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := runtime.InitializeLXSource(initCtx, ScriptInfo{
			Name: "Xinglan production source",
			Version: "4.0.2",
			RawScript: string(productionXinglanSource),
		}, string(productionXinglanSource), m.client)
		cancel()
		if err != nil {
			m.mu.Unlock()
			return nil, fmt.Errorf("initialize real Xinglan source: %w", err)
		}
		m.initialized = true
	}
	m.mu.Unlock()

	m.dispatchMu.Lock()
	defer m.dispatchMu.Unlock()
	dispatchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	value, err := runtime.DispatchLXRequest(dispatchCtx, source, "musicUrl", info)
	if err != nil {
		return nil, fmt.Errorf("resolve %s playback URL through production LX source: %w", source, err)
	}
	if url, ok := value.(string); !ok || url == "" {
		return nil, fmt.Errorf("LX source %s returned an empty or non-string playback URL", source)
	}
	return value, nil
}

func (m *SourceManager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	runtime := m.runtime
	m.runtime = nil
	m.mu.Unlock()
	if runtime != nil {
		return runtime.Close()
	}
	return nil
}
