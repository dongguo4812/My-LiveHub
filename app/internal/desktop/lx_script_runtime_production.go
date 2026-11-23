//go:build lxproduction && cgo

package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
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
	Enabled          bool     `json:"enabled"`
	LastError        string   `json:"last_error,omitempty"`
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

func summarizeLXScript(script storedLXScript) LXScriptSummary {
	return LXScriptSummary{
		ID: script.ID, Name: script.Name, Description: script.Description,
		Version: script.Version, Author: script.Author, Homepage: script.Homepage,
		Enabled: script.Enabled, ExtensionActions: append([]string(nil), script.ExtensionActions...),
		LastError: script.LastError,
	}
}

func (r *productionMusicLXRuntime) Available() bool {
	if r == nil { return false }
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.started && r.manager != nil && r.manager.HasScripts()
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
	r.scripts = scripts
	return nil
}

func (r *productionMusicLXRuntime) persistLocked() error {
	data, err := json.MarshalIndent(r.scripts, "", "  ")
	if err != nil { return fmt.Errorf("encode LX script store: %w", err) }
	return r.config.WriteLocalJSON(lxScriptStoreFile, data, lxScriptStoreLimit)
}

func (r *productionMusicLXRuntime) rebuildLocked(ctx context.Context) error {
	definitions := make([]lxruntime.LXSourceScript, 0, len(r.scripts))
	total := 0
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
		script.LastError = ""
	}
	next, err := lxruntime.NewSourceManagerWithScripts(nil, definitions)
	if err != nil { return fmt.Errorf("construct LX source manager: %w", err) }
	if r.started {
		if err := next.Start(ctx); err != nil { _ = next.Close(); return fmt.Errorf("start imported LX scripts: %w", err) }
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

func (r *productionMusicLXRuntime) ImportScript(ctx context.Context, name, source string, extensionActions []string) (LXScriptSummary, error) {
	if r == nil { return LXScriptSummary{}, errors.New("production LX source runtime is unavailable") }
	name = strings.TrimSpace(name)
	if name == "" { return LXScriptSummary{}, errors.New("LX script name is required") }
	if len([]rune(name)) > 128 { return LXScriptSummary{}, errors.New("LX script name exceeds 128 characters") }
	if err := lxruntime.ValidateLXScriptSource(source); err != nil { return LXScriptSummary{}, err }
	if len(extensionActions) > 4 { return LXScriptSummary{}, errors.New("at most four community extension actions may be declared") }
	info := parseImportedLXScriptInfo(name, source)
	probe, err := lxruntime.NewSourceManagerWithScripts(nil, []lxruntime.LXSourceScript{{
		Info: info, Source: source, ExtensionActions: extensionActions,
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
	totalEnabledBytes := len(source)
	for _, existing := range r.scripts {
		if existing.Enabled { totalEnabledBytes += len(existing.Source) }
	}
	if totalEnabledBytes > 16<<20 { return LXScriptSummary{}, errors.New("enabled LX scripts would exceed the aggregate 16 MiB source limit") }
	r.scripts = append(r.scripts, storedLXScript{
		ID: id, Name: info.Name, Description: info.Description, Version: info.Version,
		Author: info.Author, Homepage: info.Homepage, Source: source,
		ExtensionActions: append([]string(nil), extensionActions...), Enabled: true,
	})
	if err := r.rebuildLocked(ctx); err != nil {
		r.scripts = r.scripts[:len(r.scripts)-1]
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
		previous := r.scripts[i].Enabled
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
		r.scripts[i].Enabled = enabled
		r.scripts[i].LastError = ""
		if err := r.rebuildLocked(ctx); err != nil { r.scripts[i].Enabled = previous; return err }
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
