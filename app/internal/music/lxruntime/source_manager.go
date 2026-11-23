//go:build lxproduction && cgo

package lxruntime

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

//go:embed testdata/sources/xinglan.js
var productionXinglanSource []byte

//go:embed testdata/sources/helloworld.js
var productionHelloWorldSource []byte

//go:embed testdata/sources/huanyin.js
var productionHuanyinSource []byte

type managedLXScript struct {
	info        ScriptInfo
	source      string
	runtime     *Runtime
	initialized bool
}

// SourceManager owns isolated, persistent QuickJS contexts for all three real
// LX scripts. Each script is initialized lazily on first use; a failed script
// is isolated and the next real script is tried before returning an error.
type SourceManager struct {
	mu         sync.Mutex
	dispatchMu sync.Mutex
	client     *http.Client
	scripts    []*managedLXScript
	started    bool
	closed     bool
}

func NewSourceManager(client *http.Client) *SourceManager {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &SourceManager{
		client: client,
		scripts: []*managedLXScript{
			{info: ScriptInfo{Name: "Xinglan production source", Version: "4.0.2"}, source: string(productionXinglanSource)},
			{info: ScriptInfo{Name: "HelloWorld production source", Version: "1.0.0"}, source: string(productionHelloWorldSource)},
			{info: ScriptInfo{Name: "Huanyin production source", Version: "1.0.0"}, source: string(productionHuanyinSource)},
		},
	}
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
	created := make([]*Runtime, 0, len(m.scripts))
	for _, script := range m.scripts {
		runtime, err := New(Options{})
		if err != nil {
			for _, previous := range created {
				_ = previous.Close()
			}
			return fmt.Errorf("create production QuickJS runtime for %s: %w", script.info.Name, err)
		}
		if err := ctx.Err(); err != nil {
			_ = runtime.Close()
			for _, previous := range created {
				_ = previous.Close()
			}
			return err
		}
		script.runtime = runtime
		created = append(created, runtime)
	}
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
	m.dispatchMu.Lock()
	defer m.dispatchMu.Unlock()

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("LX source manager is closed")
	}
	if !m.started {
		m.mu.Unlock()
		return nil, errors.New("LX source manager has not started")
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
		if !script.initialized {
			initCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			err := script.runtime.InitializeLXSource(initCtx, script.info, script.source, client)
			cancel()
			if err != nil {
				failures = append(failures, script.info.Name+" 初始化失败："+err.Error())
				m.mu.Unlock()
				continue
			}
			script.initialized = true
		}
		runtime := script.runtime
		m.mu.Unlock()

		dispatchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		value, err := runtime.DispatchLXRequest(dispatchCtx, source, "musicUrl", info)
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
	m.dispatchMu.Lock()
	defer m.dispatchMu.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
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
