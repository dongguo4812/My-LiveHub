//go:build lxproduction

package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/music"
	"github.com/dongguo4812/My-LiveHub/app/internal/music/lxruntime"
)

const lxScriptStoreFile = "lx_sources.json"
const lxScriptStoreLimit = 40 << 20

type storedLXScript struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	Version          string   `json:"version,omitempty"`
	Author           string   `json:"author,omitempty"`
	Homepage         string   `json:"homepage,omitempty"`
	Source           string   `json:"source"`
	ExtensionActions []string `json:"extension_actions,omitempty"`
	NativeActions []string `json:"native_actions,omitempty"`
	SupportedSources []string `json:"supported_sources,omitempty"`
	SourceActions map[string][]string `json:"source_actions,omitempty"`
	LastTest *LXScriptTestResult `json:"last_test,omitempty"`
	Enabled          bool     `json:"enabled"`
	LastError        string   `json:"last_error,omitempty"`
	InitializationStatus string `json:"initialization_status,omitempty"`
	InitializationStage string `json:"initialization_stage,omitempty"`
	InitializationAt time.Time `json:"initialization_at,omitempty"`
}

type productionMusicLXRuntime struct {
	mu      sync.Mutex
	config  *config.Manager
	manager *lxruntime.SourceManager
	scripts []storedLXScript
	started bool
}

var lxMetadataPattern = regexp.MustCompile("(?m)^[[:space:]]*\\*?[[:space:]]*@([a-zA-Z]+)[[:space:]]+(.+?)[[:space:]]*$")

func newLXScriptRuntime(manager *config.Manager) lxScriptRuntime {
	return &productionMusicLXRuntime{config: manager}
}

func parseImportedLXScriptInfo(fallbackName, source string) lxruntime.ScriptInfo {
	info := lxruntime.ScriptInfo{Name: fallbackName, RawScript: source}
	for _, match := range lxMetadataPattern.FindAllStringSubmatch(source, 16) {
		value := strings.TrimSpace(match[2])
		switch strings.ToLower(match[1]) {
		case "name":
			if value != "" { info.Name = value }
		case "description":
			info.Description = value
		case "version":
			info.Version = value
		case "author":
			info.Author = value
		case "homepage":
			info.Homepage = value
		}
	}
	return info
}


func cloneLXSourceActions(source map[string][]string) map[string][]string {
	if len(source) == 0 { return map[string][]string{} }
	clone := make(map[string][]string, len(source))
	for provider, actions := range source { clone[provider] = append([]string(nil), actions...) }
	return clone
}

func summarizeLXScript(script storedLXScript) LXScriptSummary {
	return LXScriptSummary{
		ID: script.ID, Name: script.Name, Description: script.Description,
		Version: script.Version, Author: script.Author, Homepage: script.Homepage,
		Enabled: script.Enabled, ExtensionActions: append([]string(nil), script.ExtensionActions...),
		NativeActions: append([]string(nil), script.NativeActions...), SupportedSources: append([]string(nil), script.SupportedSources...), SourceActions: cloneLXSourceActions(script.SourceActions), LastTest: cloneLXScriptTestResult(script.LastTest), LastError: script.LastError,
		InitializationStatus: script.InitializationStatus, InitializationStage: script.InitializationStage, InitializationAt: script.InitializationAt,
	}
}

func cloneLXScriptTestResult(result *LXScriptTestResult) *LXScriptTestResult {
	if result == nil { return nil }
	copy := *result
	copy.Platforms = append([]LXSourcePlatformTest(nil), result.Platforms...)
	return &copy
}
type isolatedLXTestRuntime struct { manager *lxruntime.SourceManager }
func (r *isolatedLXTestRuntime) Available() bool { return r != nil && r.manager != nil && r.manager.HasScripts() }
func (r *isolatedLXTestRuntime) Start(ctx context.Context) error { return r.manager.Start(ctx) }
func (r *isolatedLXTestRuntime) Close() error { return r.manager.Close() }
func (r *isolatedLXTestRuntime) HasExtensionAction(source, action string) bool { return r.manager.HasExtensionAction(source, action) }
func (r *isolatedLXTestRuntime) DispatchExtensionAction(ctx context.Context, source, action string, info any) (any, error) { return r.manager.DispatchExtensionAction(ctx, source, action, info) }
func (r *isolatedLXTestRuntime) ResolvePlaybackURL(ctx context.Context, source string, info any) (string, error) {
	value, err := r.manager.ResolvePlaybackURL(ctx, source, info)
	if err != nil { return "", err }
	resolved, ok := value.(string); if !ok || strings.TrimSpace(resolved) == "" { return "", errors.New("LX source returned an invalid playback URL") }
	return resolved, nil
}
func (r *productionMusicLXRuntime) TestScript(ctx context.Context, id string) (LXScriptTestResult, error) {
	return r.testScript(ctx, id)
}

// testScript exercises a stored script through the same QuickJS-only search
// path used by production; missing search actions fail closed.
func (r *productionMusicLXRuntime) testScript(ctx context.Context, id string) (LXScriptTestResult, error) {
	if r == nil { return LXScriptTestResult{}, errors.New("production LX source runtime is unavailable") }
	if ctx == nil { ctx = context.Background() }
	r.mu.Lock()
	if !r.started { if err := r.loadStoreLocked(); err != nil { r.mu.Unlock(); return LXScriptTestResult{}, err } }
	var selected *storedLXScript
	for i := range r.scripts { if r.scripts[i].ID == id { copy := r.scripts[i]; selected = &copy; break } }
	r.mu.Unlock()
	if selected == nil { return LXScriptTestResult{}, fmt.Errorf("LX script %q was not found", id) }
	result := LXScriptTestResult{Status:"passed", TestedAt:time.Now().UTC(), Platforms:[]LXSourcePlatformTest{}}
	// Re-run the real QuickJS handshake for every test. Persisted action
	// snapshots can be stale after script upgrades; testing against them can
	// report false missing-search/lyric failures for providers that now declare
	// those actions in inited.sources.
	capabilities, capabilityErr := lxruntime.DetectLXScriptCapabilities(ctx, lxruntime.ScriptInfo{Name:selected.Name, Description:selected.Description, Version:selected.Version, Author:selected.Author, Homepage:selected.Homepage, RawScript:selected.Source}, selected.Source, nil)
	if capabilityErr != nil {
		result.Status = "failed"
		result.Message = "读取脚本平台能力失败：" + capabilityErr.Error()
	} else {
		selected.ExtensionActions = append([]string(nil), capabilities.ExtensionActions...)
		selected.NativeActions = append([]string(nil), capabilities.NativeActions...)
		selected.SupportedSources = append([]string(nil), capabilities.SupportedSources...)
		selected.SourceActions = cloneLXSourceActions(capabilities.SourceActions)
	}
	definition := lxruntime.LXSourceScript{Info:lxruntime.ScriptInfo{Name:selected.Name, Description:selected.Description, Version:selected.Version, Author:selected.Author, Homepage:selected.Homepage, RawScript:selected.Source}, Source:selected.Source, ExtensionActions:append([]string(nil), selected.ExtensionActions...)}
	// Match the production registry: imported scripts may override search for
	// their declared providers, while the trusted built-in QuickJS adapter
	// supplies search for providers that expose only musicUrl/playback actions.
	// Without this adapter, every provider test falsely reports that search is
	// not ready even though production search is registered independently.
	definitions := []lxruntime.LXSourceScript{definition, lxruntime.BundledSearchAction(), lxruntime.BundledLyricsAction()}
	manager, err := lxruntime.NewSourceManagerWithScripts(nil, definitions)
	if err != nil { return LXScriptTestResult{}, fmt.Errorf("create isolated test runtime: %w", err) }
	defer manager.Close()
	if err := manager.Start(ctx); err != nil { return LXScriptTestResult{}, fmt.Errorf("initialize isolated test runtime: %w", err) }
	adapter := &isolatedLXTestRuntime{manager:manager}
	service := &MusicService{scriptRuntime:adapter}
	searcher := scriptAwareSourceSearcher{service:service}
	unified := music.NewUnifiedSourceService(searcher, adapter)
	// Include every platform declared by the script, even when it does not
	// expose musicUrl. Keep the action map as a compatibility fallback for
	// older stored scripts whose SupportedSources snapshot is empty.
	platformSet := make(map[string]struct{}, len(selected.SupportedSources)+len(selected.SourceActions))
	for _, platform := range selected.SupportedSources {
		if strings.TrimSpace(platform) != "" { platformSet[platform] = struct{}{} }
	}
	for platform := range selected.SourceActions { platformSet[platform] = struct{}{} }
	platforms := make([]string, 0, len(platformSet))
	for platform := range platformSet { platforms = append(platforms, platform) }
	sort.Strings(platforms)
	for _, platform := range platforms {
		// Search is independent from musicUrl: use the script search action
		// when present; otherwise the QuickJS search path fails closed.
		actions := selected.SourceActions[platform]
		item := LXSourcePlatformTest{Platform:platform, Name:platform, Status:"failed"}
		for _, source := range searcher.Sources() { if source.ID == platform { item.Name = source.Name; break } }
		if !containsLXAction(actions, "musicUrl") {
			item.Status = "unsupported"
			item.Message = "该平台未声明 musicUrl，无法检测播放地址"
			result.Platforms = append(result.Platforms, item)
			continue
		}
		for _, source := range searcher.Sources() { if source.ID == platform { item.Name = source.Name; break } }
		page, searchErr := unified.SearchSource(ctx, platform, "稻香 周杰伦", 1)
		if searchErr != nil { item.Message = "搜索失败：" + searchErr.Error(); result.Platforms = append(result.Platforms, item); continue }
		if len(page.Tracks) == 0 { item.Message = "搜索未返回歌曲"; result.Platforms = append(result.Platforms, item); continue }
		track := page.Tracks[0]; item.Song = strings.TrimSpace(track.Title + " - " + track.Artist)
		playURL, resolveErr := unified.ResolvePlaybackURL(ctx, track, "")
		if resolveErr != nil { item.Message = "获取播放地址失败：" + resolveErr.Error(); result.Platforms = append(result.Platforms, item); continue }
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, playURL, nil)
		if requestErr != nil { item.Message = "构造音频请求失败：" + requestErr.Error(); result.Platforms = append(result.Platforms, item); continue }
		request.Header.Set("Range", "bytes=0-1023")
		// Do not follow redirects: the source URL has already passed the
		// LX SSRF guard, but a redirect could otherwise escape that check.
		probeClient := &http.Client{Timeout:15*time.Second, CheckRedirect:func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
		response, requestErr := probeClient.Do(request)
		if requestErr != nil { item.Message = "音频请求失败：" + requestErr.Error(); result.Platforms = append(result.Platforms, item); continue }
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1024)); _ = response.Body.Close()
		if readErr != nil { item.Message = "读取音频数据失败：" + readErr.Error() } else if response.StatusCode < 200 || response.StatusCode >= 300 { item.Message = fmt.Sprintf("音频服务器返回 HTTP %d", response.StatusCode) } else if len(body) == 0 { item.Message = "播放地址未返回音频数据" } else { item.Status = "passed"; item.Message = fmt.Sprintf("播放地址可访问，已读取 %d 字节音频数据", len(body)) }
		result.Platforms = append(result.Platforms, item)
	}
	if len(result.Platforms) == 0 { result.Status = "failed"; if result.Message == "" { result.Message = "脚本没有声明 musicUrl 能力的平台" } } else { for _, item := range result.Platforms { if item.Status != "passed" { result.Status = "failed"; break } } }
	r.mu.Lock(); defer r.mu.Unlock()
	for i := range r.scripts { if r.scripts[i].ID == id { r.scripts[i].SourceActions = cloneLXSourceActions(selected.SourceActions); r.scripts[i].LastTest = cloneLXScriptTestResult(&result); if err := r.persistLocked(); err != nil { return result, fmt.Errorf("persist latest LX test result: %w", err) }; return result, nil } }
	return result, fmt.Errorf("LX script %q was deleted while its test was running", id)
}
func containsLXAction(actions []string, expected string) bool { for _, action := range actions { if action == expected { return true } }; return false }

func (r *productionMusicLXRuntime) Available() bool {
	if r == nil { return false }
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started || r.manager == nil { return false }
	// Available describes imported playback/lyric resolvers, not the
	// independently registered built-in search Action.
	for _, script := range r.scripts {
		if script.Enabled { return true }
	}
	return false
}

func (r *productionMusicLXRuntime) loadStoreLocked() error {
	if r.config == nil { return errors.New("LX script persistence requires the application configuration manager") }
	data, exists, err := r.config.ReadLocalJSON(lxScriptStoreFile, lxScriptStoreLimit)
	if err != nil {
		_, quarantineErr := r.config.QuarantineLocalJSON(lxScriptStoreFile)
		if quarantineErr != nil { return errors.Join(err, quarantineErr) }
		r.scripts = nil
		return nil
	}
	if !exists { r.scripts = nil; return nil }
	var scripts []storedLXScript
	if err := json.Unmarshal(data, &scripts); err != nil {
		_, quarantineErr := r.config.QuarantineLocalJSON(lxScriptStoreFile)
		if quarantineErr != nil { return errors.Join(fmt.Errorf("decode LX script store: %w", err), quarantineErr) }
		r.scripts = nil
		return nil
	}
	if len(scripts) > 32 { return fmt.Errorf("LX script store contains %d entries; maximum is 32", len(scripts)) }
	// Validate persisted source identities before resolving legacy multiple-enabled
	// stores. Otherwise a tampered script could be hidden by the single-active
	// normalization and lose its actionable hash-verification error.
	for i := range scripts {
		digest := sha256.Sum256([]byte(scripts[i].Source))
		if scripts[i].ID != hex.EncodeToString(digest[:]) {
			scripts[i].Enabled = false
			scripts[i].LastError = "stored script hash does not match executable source"
		}
	}
	// Enforce the single-active-script invariant when restoring legacy stores.
	// If older data contains multiple valid enabled scripts, the last enabled entry wins.
	active := -1
	for i := range scripts {
		if scripts[i].Enabled { active = i }
	}
	if active >= 0 {
		for i := range scripts {
			if i != active && scripts[i].Enabled {
				scripts[i].Enabled = false
				scripts[i].LastError = "已被更新启用的音源脚本自动停用（同一时间只允许一个脚本启用）"
			}
		}
	}
	r.scripts = scripts
	return nil
}

func (r *productionMusicLXRuntime) persistLocked() error {
	data, err := json.MarshalIndent(r.scripts, "", "  ")
	if err != nil { return fmt.Errorf("encode LX script store: %w", err) }
	return r.config.WriteLocalJSON(lxScriptStoreFile, data, lxScriptStoreLimit)
}

func (r *productionMusicLXRuntime) rebuildLocked(ctx context.Context) error {
	// The trusted built-in search adapter is always registered after imported
	// scripts: a user script may override search for sources it explicitly
	// declares, while the built-in adapter handles the remaining platforms.
	// It is not persisted and cannot be disabled by the user.
	builtinSearch := lxruntime.BundledSearchAction()
	builtinLyrics := lxruntime.BundledLyricsAction()
	definitions := make([]lxruntime.LXSourceScript, 0, len(r.scripts)+2)
	enabledIndexes := make([]int, 0, len(r.scripts))
	total := len(builtinSearch.Source) + len(builtinLyrics.Source)
	for i := range r.scripts {
		script := &r.scripts[i]
		if !script.Enabled { continue }
		digest := sha256.Sum256([]byte(script.Source))
		if script.ID != hex.EncodeToString(digest[:]) {
			script.LastError = "stored script hash does not match executable source"
			script.Enabled = false
			continue
		}
		info := lxruntime.ScriptInfo{
			Name: script.Name, Description: script.Description, Version: script.Version,
			Author: script.Author, Homepage: script.Homepage, RawScript: script.Source,
		}
		probe, validationErr := lxruntime.NewSourceManagerWithScripts(nil, []lxruntime.LXSourceScript{{
			Info: info, Source: script.Source,
			ExtensionActions: append([]string(nil), script.ExtensionActions...),
		}})
		if validationErr != nil {
			script.LastError = validationErr.Error()
			script.Enabled = false
			continue
		}
		_ = probe.Close()
		total += len(script.Source)
		if total > 16<<20 {
			script.LastError = "enabled LX scripts exceed the aggregate 16 MiB source limit"
			script.Enabled = false
			continue
		}
		definitions = append(definitions, lxruntime.LXSourceScript{
			Info: info, Source: script.Source,
			ExtensionActions: append([]string(nil), script.ExtensionActions...),
		})
		enabledIndexes = append(enabledIndexes, i)
		script.InitializationStatus = "initializing"
		script.InitializationStage = "startup"
		script.InitializationAt = time.Time{}
		script.LastError = ""
	}
	definitions = append(definitions, builtinSearch, builtinLyrics)
	next, err := lxruntime.NewSourceManagerWithScripts(nil, definitions)
	if err != nil { return fmt.Errorf("construct LX source manager: %w", err) }
	if r.started {
		if err := next.Start(ctx); err != nil { _ = next.Close(); return fmt.Errorf("start imported LX scripts: %w", err) }
		for position, result := range next.InitializationResults() {
			if position < len(enabledIndexes) {
				script := &r.scripts[enabledIndexes[position]]
				script.InitializationStatus = result.Status
				script.InitializationStage = result.Stage
				script.InitializationAt = result.At
				script.LastError = result.Error
				continue
			}
			// The built-in adapter is the final definition and is required
			// for every platform that does not supply its own search Action.
			if result.Status != "ready" {
				_ = next.Close()
				return fmt.Errorf("built-in QuickJS search actions failed to initialize（阶段：%s）：%s", result.Stage, result.Error)
			}
		}
	}
	// Persist the new registry before swapping the live manager. A disk error
	// must not leave the process running a script set that will disappear on restart.
	if err := r.persistLocked(); err != nil {
		_ = next.Close()
		return err
	}
	old := r.manager
	r.manager = next
	if old != nil {
		if err := old.Close(); err != nil { return fmt.Errorf("close previous LX source manager after registry swap: %w", err) }
	}
	return nil
}

func (r *productionMusicLXRuntime) Start(ctx context.Context) error {
	if r == nil { return errors.New("production LX source runtime is unavailable") }
	if ctx == nil { ctx = context.Background() }
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started { return nil }
	if err := r.loadStoreLocked(); err != nil { return err }
	r.started = true
	if err := r.rebuildLocked(ctx); err != nil { r.started = false; return err }
	return nil
}

// InitializationError reports per-script failures after Start has completed.
// QuickJS action failures remain visible; the five built-in search platforms
// fail closed rather than falling back to Go-native search.
func (r *productionMusicLXRuntime) InitializationError() error {
	if r == nil { return errors.New("production LX source runtime is unavailable") }
	r.mu.Lock()
	defer r.mu.Unlock()
	var failures []string
	for _, script := range r.scripts {
		if script.Enabled && script.InitializationStatus != "ready" {
			failures = append(failures, fmt.Sprintf("%s 初始化失败（阶段：%s）：%s", script.Name, script.InitializationStage, script.LastError))
		}
	}
	if len(failures) == 0 { return nil }
	return errors.New(strings.Join(failures, "；"))
}

func (r *productionMusicLXRuntime) ImportScript(ctx context.Context, name, source string) (LXScriptSummary, error) {
	if r == nil { return LXScriptSummary{}, errors.New("production LX source runtime is unavailable") }
	name = strings.TrimSpace(name)
	if name == "" { return LXScriptSummary{}, errors.New("LX script name is required") }
	if len([]rune(name)) > 128 { return LXScriptSummary{}, errors.New("LX script name exceeds 128 characters") }
	if err := lxruntime.ValidateLXScriptSource(source); err != nil { return LXScriptSummary{}, err }
	info := parseImportedLXScriptInfo(name, source)
	if ctx == nil { ctx = context.Background() }
	capabilities, err := lxruntime.DetectLXScriptCapabilities(ctx, info, source, nil)
	if err != nil { return LXScriptSummary{}, fmt.Errorf("无法识别脚本能力：%w", err) }
	if len(capabilities.ExtensionActions) == 0 && len(capabilities.NativeActions) == 0 {
		return LXScriptSummary{}, errors.New("脚本没有声明 My-LiveHub 支持的社区扩展 action 或洛雪原生 musicUrl action，无法导入")
	}
	probe, err := lxruntime.NewSourceManagerWithScripts(nil, []lxruntime.LXSourceScript{{
		Info: info, Source: source, ExtensionActions: capabilities.ExtensionActions,
	}})
	if err != nil { return LXScriptSummary{}, fmt.Errorf("validate imported LX script: %w", err) }
	_ = probe.Close()
	digest := sha256.Sum256([]byte(source))
	id := hex.EncodeToString(digest[:])
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.config == nil { return LXScriptSummary{}, errors.New("LX script persistence is unavailable") }
	if !r.started {
		if err := r.loadStoreLocked(); err != nil { return LXScriptSummary{}, err }
	}
	for _, existing := range r.scripts {
		if existing.ID == id { return LXScriptSummary{}, fmt.Errorf("LX script %q is already imported", name) }
	}
	if len(r.scripts) >= 32 { return LXScriptSummary{}, errors.New("maximum of 32 imported LX scripts reached") }
	if len(source) > 16<<20 {
		return LXScriptSummary{}, errors.New("LX script source exceeds the 16 MiB active-script limit")
	}
	// Import-and-enable is a replacement operation: retain all scripts, but
	// disable every previous script before activating the newly imported one.
	previous := append([]storedLXScript(nil), r.scripts...)
	for i := range r.scripts {
		if r.scripts[i].Enabled {
			r.scripts[i].Enabled = false
			r.scripts[i].LastError = "已被新导入并启用的音源脚本自动停用"
		}
	}
	r.scripts = append(r.scripts, storedLXScript{
		ID: id, Name: info.Name, Description: info.Description, Version: info.Version,
		Author: info.Author, Homepage: info.Homepage, Source: source,
		ExtensionActions: append([]string(nil), capabilities.ExtensionActions...), NativeActions: append([]string(nil), capabilities.NativeActions...), SupportedSources: append([]string(nil), capabilities.SupportedSources...), SourceActions: cloneLXSourceActions(capabilities.SourceActions), Enabled: true,
	})
	if err := r.rebuildLocked(ctx); err != nil {
		r.scripts = previous
		return LXScriptSummary{}, err
	}
	return summarizeLXScript(r.scripts[len(r.scripts)-1]), nil
}

func (r *productionMusicLXRuntime) ListScripts() []LXScriptSummary {
	if r == nil { return []LXScriptSummary{} }
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started { _ = r.loadStoreLocked() }
	result := make([]LXScriptSummary, 0, len(r.scripts))
	for _, script := range r.scripts { result = append(result, summarizeLXScript(script)) }
	return result
}

func (r *productionMusicLXRuntime) SetScriptEnabled(ctx context.Context, id string, enabled bool) error {
	if r == nil { return errors.New("production LX source runtime is unavailable") }
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started { if err := r.loadStoreLocked(); err != nil { return err } }
	for i := range r.scripts {
		if r.scripts[i].ID != id { continue }
		previous := append([]storedLXScript(nil), r.scripts...)
		if enabled {
			digest := sha256.Sum256([]byte(r.scripts[i].Source))
			if r.scripts[i].ID != hex.EncodeToString(digest[:]) {
				return errors.New("stored LX script hash does not match executable source")
			}
			probe, err := lxruntime.NewSourceManagerWithScripts(nil, []lxruntime.LXSourceScript{{
				Info: lxruntime.ScriptInfo{Name: r.scripts[i].Name, Description: r.scripts[i].Description, Version: r.scripts[i].Version, Author: r.scripts[i].Author, Homepage: r.scripts[i].Homepage, RawScript: r.scripts[i].Source},
				Source: r.scripts[i].Source, ExtensionActions: append([]string(nil), r.scripts[i].ExtensionActions...),
			}})
			if err != nil { return fmt.Errorf("cannot enable LX script: %w", err) }
			_ = probe.Close()
		}
		if enabled {
			for j := range r.scripts {
				if j != i && r.scripts[j].Enabled {
					r.scripts[j].Enabled = false
					r.scripts[j].LastError = "已被其他音源脚本替换并自动停用"
				}
			}
		}
		r.scripts[i].Enabled = enabled
		r.scripts[i].LastError = ""
		if err := r.rebuildLocked(ctx); err != nil { r.scripts = previous; return err }
		return nil
	}
	return fmt.Errorf("LX script %q was not found", id)
}

func (r *productionMusicLXRuntime) DeleteScript(ctx context.Context, id string) error {
	if r == nil { return errors.New("production LX source runtime is unavailable") }
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started { if err := r.loadStoreLocked(); err != nil { return err } }
	for i := range r.scripts {
		if r.scripts[i].ID != id { continue }
		previous := append([]storedLXScript(nil), r.scripts...)
		r.scripts = append(r.scripts[:i], r.scripts[i+1:]...)
		if err := r.rebuildLocked(ctx); err != nil { r.scripts = previous; return err }
		return nil
	}
	return fmt.Errorf("LX script %q was not found", id)
}

func (r *productionMusicLXRuntime) ResolvePlaybackURL(ctx context.Context, source string, info any) (string, error) {
	if r == nil { return "", errors.New("production LX source runtime is unavailable") }
	r.mu.Lock()
	manager := r.manager
	r.mu.Unlock()
	if manager == nil { return "", errors.New("production LX source runtime has not started") }
	value, err := manager.ResolvePlaybackURL(ctx, source, info)
	if err != nil { return "", err }
	url, ok := value.(string)
	if !ok || url == "" { return "", errors.New("LX source returned an invalid playback URL") }
	return url, nil
}

func (r *productionMusicLXRuntime) Close() error {
	if r == nil { return nil }
	r.mu.Lock()
	manager := r.manager
	r.manager = nil
	r.started = false
	r.mu.Unlock()
	if manager == nil { return nil }
	return manager.Close()
}


// HasExtensionAction reports whether an enabled imported script explicitly
// opted into a My-LiveHub community extension action.
func (r *productionMusicLXRuntime) HasExtensionAction(source, action string) bool {
 if r==nil { return false }
 r.mu.Lock(); manager:=r.manager; r.mu.Unlock()
 return manager!=nil && manager.HasExtensionAction(source, action)
}

// DispatchExtensionAction dispatches only community extension actions. The
// native 洛雪 musicUrl action remains on ResolvePlaybackURL.
func (r *productionMusicLXRuntime) DispatchExtensionAction(ctx context.Context, source, action string, info any) (any,error) {
 if r==nil { return nil,errors.New("production LX source runtime is unavailable") }
 r.mu.Lock(); manager:=r.manager; r.mu.Unlock()
 if manager==nil { return nil,errors.New("production LX source runtime has not started") }
 return manager.DispatchExtensionAction(ctx,source,action,info)
}
