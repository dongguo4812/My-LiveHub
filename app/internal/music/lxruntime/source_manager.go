//go:build lxproduction && cgo

package lxruntime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type managedLXScript struct {
	info        ScriptInfo
	source      string
	runtime     *Runtime
	initialized bool
}

// SourceManager owns isolated, persistent QuickJS contexts for scripts
// explicitly supplied by the script-import layer. Test fixtures and third-party
// source snapshots are never bundled into the production binary by default.
type SourceManager struct {
	mu         sync.Mutex
	dispatchMu sync.Mutex
	closeMu    sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	client     *http.Client
	scripts    []*managedLXScript
	started    bool
	closed     bool
}

type LXSourceScript struct {
	Info   ScriptInfo
	Source string
}

func NewSourceManager(client *http.Client) *SourceManager {
	return NewSourceManagerWithScripts(client, nil)
}

func NewSourceManagerWithScripts(client *http.Client, scripts []LXSourceScript) *SourceManager {
	if client == nil {
		client = &http.Client{Timeout: lxMaxRequestTimeout}
	}
	managerCtx, cancel := context.WithCancel(context.Background())
	manager := &SourceManager{
		ctx: managerCtx,
		cancel: cancel,
		client: client,
		scripts: make([]*managedLXScript, 0, len(scripts)),
	}
	for i, definition := range scripts {
		if strings.TrimSpace(definition.Source) == "" {
			continue
		}
		info := definition.Info
		if strings.TrimSpace(info.Name) == "" {
			info.Name = fmt.Sprintf("LX script %d", i+1)
		}
		if info.RawScript == "" {
			info.RawScript = definition.Source
		}
		manager.scripts = append(manager.scripts, &managedLXScript{info: info, source: definition.Source})
	}
	return manager
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
	if len(m.scripts) == 0 {
		m.started = true
		return nil
	}
	runtime, err := New(Options{})
	if err != nil {
		return fmt.Errorf("create production QuickJS runtime for %s: %w", m.scripts[0].info.Name, err)
	}
	if err := ctx.Err(); err != nil {
		_ = runtime.Close()
		return err
	}
	m.scripts[0].runtime = runtime
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
	managerCtx := m.ctx
	m.mu.Unlock()

	// Closing the manager must interrupt initialization and in-flight network
	// actions before Close waits for the serialized dispatch lock.
	actionCtx, cancelAction := context.WithCancel(ctx)
	stopManagerCancel := context.AfterFunc(managerCtx, cancelAction)
	defer func() {
		stopManagerCancel()
		cancelAction()
	}()
	ctx = actionCtx

	m.dispatchMu.Lock()
	defer m.dispatchMu.Unlock()

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("LX source manager is closed")
	}
	scripts := append([]*managedLXScript(nil), m.scripts...)
	client := m.client
	m.mu.Unlock()

	var failures []string
	for _, script := range scripts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, errors.New("LX source manager is closed")
		}
		if script.runtime == nil {
			runtime, err := New(Options{})
			if err != nil {
				failures = append(failures, script.info.Name+" Runtime 创建失败："+err.Error())
				m.mu.Unlock()
				continue
			}
			script.runtime = runtime
		}
		runtime := script.runtime
		initialized := script.initialized
		m.mu.Unlock()

		if !initialized {
			initCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			err := runtime.InitializeLXSource(initCtx, script.info, script.source, client)
			cancel()
			if err != nil {
				_ = runtime.Close()
				m.mu.Lock()
				if script.runtime == runtime {
					script.runtime = nil
					script.initialized = false
				}
				m.mu.Unlock()
				failures = append(failures, script.info.Name+" 初始化失败："+err.Error())
				continue
			}
			m.mu.Lock()
			if m.closed {
				m.mu.Unlock()
				return nil, errors.New("LX source manager is closed")
			}
			script.initialized = true
			m.mu.Unlock()
		}

		dispatchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		value, err := runtime.dispatchLXRequest(dispatchCtx, source, "musicUrl", info, client)
		cancel()
		if err != nil {
			failures = append(failures, script.info.Name+" 解析失败："+err.Error())
			continue
		}
		resolved, ok := value.(string)
		if !ok || strings.TrimSpace(resolved) == "" {
			failures = append(failures, script.info.Name+" 返回空播放地址")
			continue
		}
		parsed, parseErr := url.Parse(strings.TrimSpace(resolved))
		if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			failures = append(failures, script.info.Name+" 返回无效 HTTP(S) 地址")
			continue
		}
		if err := validateLXPlaybackURL(ctx, parsed); err != nil {
			failures = append(failures, script.info.Name+" 返回不安全的播放地址："+err.Error())
			continue
		}
		return resolved, nil
	}
	if len(failures) == 0 {
		return nil, fmt.Errorf("no LX scripts available for source %q", source)
	}
	return nil, fmt.Errorf("all production LX scripts failed for %s: %s", source, strings.Join(failures, "；"))
}

func (m *SourceManager) Close() error {
	if m == nil {
		return nil
	}
	m.closeMu.Lock()
	defer m.closeMu.Unlock()

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	cancel := m.cancel
	m.mu.Unlock()

	// Cancel before waiting for dispatchMu so an active lx.request cannot
	// hold shutdown hostage until its network timeout expires.
	if cancel != nil {
		cancel()
	}

	m.dispatchMu.Lock()
	defer m.dispatchMu.Unlock()
	m.mu.Lock()
	runtimes := make([]*Runtime, 0, len(m.scripts))
	for _, script := range m.scripts {
		if script.runtime != nil {
			runtimes = append(runtimes, script.runtime)
			script.runtime = nil
		}
	}
	m.mu.Unlock()
	var failures []error
	for _, runtime := range runtimes {
		if err := runtime.Close(); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
