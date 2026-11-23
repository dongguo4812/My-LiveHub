//go:build lxproduction && cgo

package lxruntime

import (
	"context"
	"encoding/json"
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
	supportedSources  map[string]struct{}
	supportedActions  map[string]map[string]struct{}
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


// DetectLXScriptExtensionActions initializes an imported script and derives the
// My-LiveHub extension capabilities exclusively from its LX inited.sources
// declaration. Native musicUrl is intentionally excluded from the returned
// community actions.
func DetectLXScriptExtensionActions(ctx context.Context, info ScriptInfo, source string, client *http.Client) ([]string, []string, error) {
	if ctx == nil { ctx = context.Background() }
	if err := ValidateLXScriptSource(source); err != nil { return nil, nil, err }
	runtime, err := New(Options{})
	if err != nil { return nil, nil, fmt.Errorf("create QuickJS capability probe: %w", err) }
	defer runtime.Close()
	if err := runtime.InitializeLXSource(ctx, info, source, client); err != nil {
		return nil, nil, fmt.Errorf("initialize script to detect capabilities: %w", err)
	}
	text, err := runtime.Eval(ctx, `JSON.stringify((globalThis.__lxHostState && globalThis.__lxHostState.initInfo && globalThis.__lxHostState.initInfo.sources) || {})`, "lx-capability-detection.js")
	if err != nil { return nil, nil, fmt.Errorf("read inited.sources capabilities: %w", err) }
	var sources map[string]struct { Actions []string `json:"actions"` }
	if err := json.Unmarshal([]byte(text), &sources); err != nil {
		return nil, nil, fmt.Errorf("decode inited.sources capabilities: %w", err)
	}
	if len(sources) == 0 { return nil, nil, errors.New("脚本初始化成功，但没有在 inited.sources 中声明任何音源") }
	actionsSet := map[string]struct{}{}
	sourceSet := make([]string, 0, len(sources))
	for sourceName, capability := range sources {
		sourceName = strings.TrimSpace(sourceName)
		if sourceName == "" { continue }
		sourceSet = append(sourceSet, sourceName)
		for _, action := range capability.Actions {
			if isLXCommunityExtensionAction(strings.TrimSpace(action)) { actionsSet[strings.TrimSpace(action)] = struct{}{} }
		}
	}
	actions := make([]string, 0, len(actionsSet))
	for action := range actionsSet { actions = append(actions, action) }
	sort.Strings(actions)
	sort.Strings(sourceSet)
	if len(actions) == 0 {
		return nil, sourceSet, errors.New("脚本已初始化并声明音源，但没有声明 My-LiveHub 支持的社区扩展 action（search/detail/lyric/pic）；musicUrl 属于洛雪原生 action，不计入社区扩展能力")
	}
	return actions, sourceSet, nil
}
\nfunc newSourceManager(client *http.Client) *SourceManager {
	if client == nil {
		client = &http.Client{Timeout: lxMaxRequestTimeout}
	}
	managerCtx, cancel := context.WithCancel(context.Background())
	return &SourceManager{ctx: managerCtx, cancel: cancel, client: client}
}

func NewSourceManager(client *http.Client) *SourceManager {
	return newSourceManager(client)
}

// HasExtensionAction reports an explicitly enabled action for a source. Source
// membership is learned from each script's own LX inited handshake; before a
// script is initialized, the declaration is treated as a candidate and the
// dispatcher performs the authoritative source check.
func (m *SourceManager) HasExtensionAction(source, action string) bool {
	if m == nil || strings.TrimSpace(source) == "" || !isLXCommunityExtensionAction(action) { return false }
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || !m.started { return false }
	for _, script := range m.scripts {
		if _, enabled := script.extensionActions[action]; !enabled { continue }
		if script.supportedSources == nil { return true }
		if _, supported := script.supportedSources[source]; supported { return true }
	}
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

type lxSourceUnsupportedError struct { source string }
func (e *lxSourceUnsupportedError) Error() string { return fmt.Sprintf("LX script does not advertise source %q", e.source) }
func (e *lxSourceUnsupportedError) LXExtensionSourceUnsupported() bool { return true }

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
		sourcesText, sourceErr := runtime.Eval(ctx, `JSON.stringify((globalThis.__lxHostState && globalThis.__lxHostState.initInfo && globalThis.__lxHostState.initInfo.sources) || {})`, "lx-source-capabilities.js")
		if sourceErr != nil {
			_ = runtime.Close()
			m.mu.Lock()
			if script.runtime == runtime { script.runtime = nil; script.initialized = false; script.supportedSources = nil; script.supportedActions = nil }
			m.mu.Unlock()
			return nil, fmt.Errorf("%s 读取音源能力失败：%w", script.info.Name, sourceErr)
		}
		var sourceCapabilities map[string]struct { Actions []string `json:"actions"` }
		if sourceErr = json.Unmarshal([]byte(sourcesText), &sourceCapabilities); sourceErr != nil {
			_ = runtime.Close()
			m.mu.Lock()
			if script.runtime == runtime { script.runtime = nil; script.initialized = false; script.supportedSources = nil; script.supportedActions = nil }
			m.mu.Unlock()
			return nil, fmt.Errorf("%s 音源能力格式无效：%w", script.info.Name, sourceErr)
		}
		supported := make(map[string]struct{}, len(sourceCapabilities))
		actions := make(map[string]map[string]struct{}, len(sourceCapabilities))
		for name, capability := range sourceCapabilities {
			supported[name] = struct{}{}
			actions[name] = make(map[string]struct{}, len(capability.Actions))
			for _, action := range capability.Actions { actions[name][action] = struct{}{} }
		}
		m.mu.Lock()
		if m.closed { m.mu.Unlock(); return nil, errors.New("LX source manager is closed") }
		script.supportedSources = supported
		script.supportedActions = actions
		script.initialized = true
		m.mu.Unlock()
	}
	m.mu.Lock()
	_, supportsSource := script.supportedSources[source]
	m.mu.Unlock()
	if !supportsSource { return nil, &lxSourceUnsupportedError{source: source} }
	if action == "musicUrl" {
		m.mu.Lock()
		_, advertised := script.supportedActions[source]["musicUrl"]
		m.mu.Unlock()
		if !advertised { return nil, &lxSourceUnsupportedError{source: source} }
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
	declared := false
	for _, script := range scripts {
		if err := ctx.Err(); err != nil { return nil, err }
		if _, enabled := script.extensionActions[action]; !enabled { continue }
		declared = true
		value, err := m.dispatchScriptAction(ctx, script, source, action, info, client)
		if err != nil {
			// Cancellation and deadlines belong to the caller, not to an
			// individual script. They take precedence over source fallback.
			if ctxErr := ctx.Err(); ctxErr != nil { return nil, ctxErr }
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) { return nil, err }
			var unsupported *lxSourceUnsupportedError
			if errors.As(err, &unsupported) { continue }
			failures = append(failures, script.info.Name+" "+action+" 失败："+err.Error())
			continue
		}
		return value, nil
	}
	if len(failures) == 0 {
		if declared { return nil, &lxSourceUnsupportedError{source: source} }
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
			if ctxErr := ctx.Err(); ctxErr != nil { return nil, ctxErr }
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) { return nil, err }
			var unsupported *lxSourceUnsupportedError
			if errors.As(err, &unsupported) { continue }
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
