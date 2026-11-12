package desktop

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
)

type Live2DModel struct {
	ID string `json:"id"`
	Name string `json:"name"`
	Format string `json:"format"`
	Directory string `json:"directory"`
	EntryFile string `json:"entry_file"`
	ModelURL string `json:"model_url"`
	RuntimeAvailable bool `json:"runtime_available"`
	CapabilityStatus string `json:"capability_status"`
	Expressions []string `json:"expressions"`
	MotionGroups []Live2DMotionGroup `json:"motion_groups"`
	HasPhysics bool `json:"has_physics"`
	HasPose bool `json:"has_pose"`
	HitAreas []string `json:"hit_areas"`
}

type Live2DMotionGroup struct {
	Name string `json:"name"`
	Count int `json:"count"`
}

type Live2DService struct {
	manager *config.Manager
	modelRoot string
	mu sync.RWMutex
	current config.Live2DConfig
}

func NewLive2DService(manager *config.Manager, current config.Live2DConfig, modelRoot string) *Live2DService {
	if modelRoot != "" { _ = os.MkdirAll(modelRoot, 0o700) }
	return &Live2DService{manager: manager, current: current, modelRoot: modelRoot}
}

func (s *Live2DService) GetSettings() config.Live2DConfig {
	if s == nil { return config.DefaultLive2DConfig() }
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *Live2DService) UpdateSettings(next config.Live2DConfig) (config.Live2DConfig, error) {
	if s == nil || s.manager == nil { return config.Live2DConfig{}, errors.New("Live2D 设置服务不可用") }
	if err := next.Validate(); err != nil { return config.Live2DConfig{}, err }
	if err := config.SaveTyped(s.manager, "live2d.json", next, func(v config.Live2DConfig) error { return v.Validate() }); err != nil {
		return config.Live2DConfig{}, err
	}
	s.mu.Lock()
	s.current = next
	s.mu.Unlock()
	return next, nil
}

func (s *Live2DService) ListModels() ([]Live2DModel, error) {
	if s == nil || s.modelRoot == "" { return nil, errors.New("Live2D 模型目录不可用") }
	var models []Live2DModel
	for _, format := range []string{"moc2", "moc3"} {
		root := filepath.Join(s.modelRoot, format)
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) { continue }
		if err != nil { return nil, err }
		for _, entry := range entries {
			if !entry.IsDir() || !safeLive2DPathPart(entry.Name()) { continue }
			dir := filepath.Join(root, entry.Name())
			entryFile := findModelEntry(dir, format)
			if entryFile == "" { continue }
			manifestName := filepath.Base(entryFile)
			modelURL := "/models/" + format + "/" + entry.Name() + "/" + manifestName
			status := "等待运行时加载后检测"
			if format == "moc2" { status = "等待 MOC2 运行时加载后检测" } else if hasMOC3LipSyncGroup(entryFile) { status = "模型声明了 LipSync 参数组，待运行时验证" } else { status = "未声明 LipSync 参数组，仍需运行时检查兼容嘴型参数" }
			metadata := readLive2DManifest(entryFile, format)
			models = append(models, Live2DModel{
				ID: entry.Name(), Name: entry.Name(), Format: format, Directory: dir,
				EntryFile: manifestName, ModelURL: modelURL,
				RuntimeAvailable: true, CapabilityStatus: status,
				Expressions: metadata.Expressions, MotionGroups: metadata.MotionGroups,
				HasPhysics: metadata.HasPhysics, HasPose: metadata.HasPose, HitAreas: metadata.HitAreas,
			})
		}
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].Format == models[j].Format { return strings.ToLower(models[i].Name) < strings.ToLower(models[j].Name) }
		return models[i].Format < models[j].Format
	})
	if models == nil { models = []Live2DModel{} }
	return models, nil
}

type live2DManifestMetadata struct {
	Expressions []string
	MotionGroups []Live2DMotionGroup
	HasPhysics bool
	HasPose bool
	HitAreas []string
}

func readLive2DManifest(manifestPath, format string) live2DManifestMetadata {
	result := live2DManifestMetadata{Expressions: []string{}, MotionGroups: []Live2DMotionGroup{}, HitAreas: []string{}}
	data, err := os.ReadFile(manifestPath)
	if err != nil { return result }
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil { return result }
	refs := root
	if raw, ok := root["FileReferences"]; ok {
		var nested map[string]json.RawMessage
		if json.Unmarshal(raw, &nested) == nil { refs = nested }
	}
	decodeList := func(raw json.RawMessage) []map[string]json.RawMessage {
		var values []map[string]json.RawMessage
		_ = json.Unmarshal(raw, &values)
		return values
	}
	if raw, ok := refs["Expressions"]; ok {
		for _, item := range decodeList(raw) {
			var name string
			for _, key := range []string{"Name", "name"} {
				if value, ok := item[key]; ok { _ = json.Unmarshal(value, &name); if name != "" { break } }
			}
			if name != "" { result.Expressions = append(result.Expressions, name) }
		}
	}
	if raw, ok := refs["Motions"]; ok {
		var groups map[string]json.RawMessage
		if json.Unmarshal(raw, &groups) == nil {
			for name, value := range groups {
				var motions []json.RawMessage
				if json.Unmarshal(value, &motions) == nil && len(motions) > 0 {
					result.MotionGroups = append(result.MotionGroups, Live2DMotionGroup{Name: name, Count: len(motions)})
				}
			}
		}
	}
	if raw, ok := refs["Physics"]; ok { var value string; _ = json.Unmarshal(raw, &value); result.HasPhysics = value != "" }
	if raw, ok := refs["Pose"]; ok { var value string; _ = json.Unmarshal(raw, &value); result.HasPose = value != "" }
	if raw, ok := root["HitAreas"]; ok {
		for _, item := range decodeList(raw) {
			var name string
			for _, key := range []string{"Name", "name"} {
				if value, ok := item[key]; ok { _ = json.Unmarshal(value, &name); if name != "" { break } }
			}
			if name != "" { result.HitAreas = append(result.HitAreas, name) }
		}
	}
	sort.Slice(result.MotionGroups, func(i, j int) bool { return strings.ToLower(result.MotionGroups[i].Name) < strings.ToLower(result.MotionGroups[j].Name) })
	sort.Strings(result.Expressions)
	sort.Strings(result.HitAreas)
	return result
}

func hasMOC3LipSyncGroup(manifestPath string) bool {
	data, err := os.ReadFile(manifestPath)
	if err != nil { return false }
	var manifest struct {
		Groups []struct {
			Name string `json:"Name"`
			Target string `json:"Target"`
			IDs []struct { ID string `json:"Id"` } `json:"Ids"`
		} `json:"Groups"`
	}
	if json.Unmarshal(data, &manifest) != nil { return false }
	for _, group := range manifest.Groups {
		if strings.EqualFold(group.Name, "LipSync") && strings.EqualFold(group.Target, "Parameter") && len(group.IDs) > 0 { return true }
	}
	return false
}

func safeLive2DPathPart(value string) bool {
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value && !strings.ContainsAny(value, "/\\")
}

// WrapLive2DAssetHandler serves user-provided model files on the Wails asset
// origin, avoiding cross-origin and private-network restrictions in WebView2.
func WrapLive2DAssetHandler(service *Live2DService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if service != nil && strings.HasPrefix(r.URL.Path, "/models/") {
			service.serveModelAsset(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Live2DService) serveModelAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead { http.Error(w, "method not allowed", http.StatusMethodNotAllowed); return }
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/models/"), "/")
	if len(parts) < 3 || (parts[0] != "moc2" && parts[0] != "moc3") || !safeLive2DPathPart(parts[1]) {
		http.NotFound(w, r); return
	}
	for _, part := range parts[2:] { if !safeLive2DPathPart(part) { http.NotFound(w, r); return } }
	candidate := filepath.Join(s.modelRoot, parts[0], parts[1], filepath.Join(parts[2:]...))
	absoluteRoot, rootErr := filepath.EvalSymlinks(s.modelRoot)
	resolved, resolveErr := filepath.EvalSymlinks(candidate)
	if rootErr != nil || resolveErr != nil { http.NotFound(w, r); return }
	relative, relErr := filepath.Rel(absoluteRoot, resolved)
	if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) { http.NotFound(w, r); return }
	file, err := os.Open(resolved)
	if err != nil { http.NotFound(w, r); return }
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() { http.NotFound(w, r); return }
	if contentType := mime.TypeByExtension(filepath.Ext(resolved)); contentType != "" { w.Header().Set("Content-Type", contentType) }
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, filepath.Base(resolved), info.ModTime(), file)
}

func findModelEntry(dir, format string) string {
	candidates := []string{"model3.json", "model.json", "model.model.json"}
	pattern := "*.model3.json"
	if format == "moc2" {
		candidates = []string{"model.json", "model.model.json"}
		pattern = "*.model.json"
	}
	for _, name := range candidates {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() { return path }
	}
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err == nil && len(matches) == 1 {
		if info, statErr := os.Stat(matches[0]); statErr == nil && !info.IsDir() { return matches[0] }
	}
	return ""
}
