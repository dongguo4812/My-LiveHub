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
)

type managedLXScript struct {
	info             ScriptInfo
	source            string
	extensionActions  map[string]struct{}
	runtime           *Runtime
	initialized       bool
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

const (
	maxLXImportedScripts        = 32
	maxLXTotalScriptSourceBytes = 16 << 20
)

type LXSourceScript struct {
	Info             ScriptInfo
	Source            string
	ExtensionActions  []string
}

func newSourceManager(client *http.Client) *SourceManager {
	if client == nil {
		client = &http.Client{Timeout: lxMaxRequestTimeout}
	}
	managerCtx, cancel := context.WithCancel(context.Background())
	return &SourceManager{ctx: managerCtx, cancel: cancel, client: client}
}

func NewSourceManager(client *http.Client) *SourceManager {
	return newSourceManager(client)
}

// HasExtensionAction reports an explicitly enabled community action.
func (m *SourceManager) HasExtensionAction(action string) bool {
 if m==nil || !isLXCommunityExtensionAction(action) { return false }
 m.mu.Lock(); defer m.mu.Unlock()
 if m.closed || !m.started { return false }
 for _, script:=range m.scripts { if _,ok:=script.extensionActions[action]; ok { return true } }
 return false
}

func (m *SourceManager) HasScripts() bool {
	if m == nil { return false }
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.closed && len(m.scripts) > 0
}

func NewSourceManagerWithScripts(client *http.Client, scripts []LXSourceScript) (*SourceManager, error) {
	if len(scripts) > maxLXImportedScripts {
		return nil, fmt.Errorf("too many imported LX scripts: %d exceeds limit %d", len(scripts), maxLXImportedScripts)
	}
	manager := newSourceManager(client)
	manager.scripts = make([]*managedLXScript, 0, len(scripts))
	totalBytes := 0
	for i, definition := range scripts {
		if err := ValidateLXScriptSource(definition.Source); err != nil {
			_ = manager.Close()
			return nil, fmt.Errorf("LX script %d: %w", i+1, err)
		}
		info := definition.Info
		if len(info.RawScript) > maxLXScriptSourceBytes {
			_ = manager.Close()
			return nil, fmt.Errorf("LX script %d metadata exceeds %d-byte limit", i+1, maxLXScriptSourceBytes)
		}
		totalBytes += len(definition.Source)
		if info.RawScript != "" && info.RawScript != definition.Source {
			_ = manager.Close()
			return nil, fmt.Errorf("LX script %q metadata rawScript does not match executable source", info.Name)
		}
		if totalBytes > maxLXTotalScriptSourceBytes {
			_ = manager.Close()
			return nil, fmt.Errorf("total imported LX script source exceeds %d-byte limit", maxLXTotalScriptSourceBytes)
		}
		if strings.TrimSpace(info.Name) == "" {
			info.Name = fmt.Sprintf("LX script %d", i+1)
		}
		// Keep script identity and digest bound to the exact bytes QuickJS evaluates.
		info.RawScript = definition.Source
		extensions := make(map[string]struct{})
		for _, action := range definition.ExtensionActions {
			action = strings.TrimSpace(action)
			if !isLXCommunityExtensionAction(action) {
				_ = manager.Close()
				return nil, fmt.Errorf("LX script %q declares unsupported community extension action %q", info.Name, action)
			}
			extensions[action] = struct{}{}
		}
		manager.scripts = append(manager.scripts, &managedLXScript{info: info, source: definition.Source, extensionActions: extensions})
	}
	return manager, nil
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

func isLXCommunityExtensionAction(action string) bool {
	switch action {
	case "search", "detail", "lyric", "pic":
		return true
	default:
		return false
	}
}

func (m *SourceManager) dispatchScriptAction(ctx context.Context, script *managedLXScript, source, action string, info any, client *http.Client) (any, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("LX source manager is closed")
	}
	if script.runtime == nil {
		runtime, err := New(Options{})
		if err != nil {
			m.mu.Unlock()
			return nil, fmt.Errorf("%s Runtime 创建失败：%w", script.info.Name, err)
		}
		script.runtime = runtime
	}
	runtime := script.runtime
	initialized := script.initialized
	m.mu.Unlock()

	if !initialized {
		initCtx, cancel := context.WithTimeout(ctx, lxMaxRequestTimeout)
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
			return nil, fmt.Errorf("%s 初始化失败：%w", script.info.Name, err)
		}
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, errors.New("LX source manager is closed")
		}
		script.initialized = true
		m.mu.Unlock()
	}

	dispatchCtx, cancel := context.WithTimeout(ctx, lxMaxRequestTimeout)
	defer cancel()
	return runtime.dispatchLXRequest(dispatchCtx, source, action, info, client)
}

func (m *SourceManager) DispatchExtensionAction(ctx context.Context, source, action string, info any) (any, error) {
	if m == nil {
		return nil, errors.New("LX source manager is nil")
	}
	if !isLXCommunityExtensionAction(action) {
		return nil, fmt.Errorf("%q is not a supported My-LiveHub community extension action", action)
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

	actionCtx, cancelAction := context.WithTimeout(ctx, lxMaxRequestTimeout)
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
		if _, enabled := script.extensionActions[action]; !enabled {
			continue
		}
		value, err := m.dispatchScriptAction(ctx, script, source, action, info, client)
		if err != nil {
			failures = append(failures, script.info.Name+" "+action+" 失败："+err.Error())
			continue
		}
		return value, nil
	}
	if len(failures) == 0 {
		return nil, fmt.Errorf("no imported LX script has explicitly enabled community extension action %q", action)
	}
	return nil, fmt.Errorf("all scripts failed community extension action %s: %s", action, strings.Join(failures, "；"))
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
	actionCtx, cancelAction := context.WithTimeout(ctx, lxMaxRequestTimeout)
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
		value, err := m.dispatchScriptAction(ctx, script, source, "musicUrl", info, client)
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
