package desktop

import (
	"errors"
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
	RuntimeAvailable bool `json:"runtime_available"`
	CapabilityStatus string `json:"capability_status"`
}

type Live2DService struct {
	manager *config.Manager
	modelRoot string
	mu sync.RWMutex
	current config.Live2DConfig
}

func NewLive2DService(manager *config.Manager, current config.Live2DConfig, modelRoot string) *Live2DService {
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

// ListModels scans user-provided model folders. SDK parameter capability is
// intentionally reported as unknown until the matching licensed runtime loads it.
func (s *Live2DService) ListModels() ([]Live2DModel, error) {
	if s == nil || s.modelRoot == "" { return nil, errors.New("Live2D 模型目录不可用") }
	var models []Live2DModel
	for _, format := range []string{"moc2", "moc3"} {
		root := filepath.Join(s.modelRoot, format)
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) { continue }
		if err != nil { return nil, err }
		for _, entry := range entries {
			if !entry.IsDir() { continue }
			dir := filepath.Join(root, entry.Name())
			entryFile := findModelEntry(dir, format)
			if entryFile == "" { continue }
			models = append(models, Live2DModel{
				ID: entry.Name(), Name: entry.Name(), Format: format, Directory: dir,
				EntryFile: entryFile, RuntimeAvailable: false,
				CapabilityStatus: "待运行时加载后检测",
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

func findModelEntry(dir, format string) string {
	candidates := []string{"model3.json", "model.json", "model.model.json"}
	if format == "moc2" { candidates = []string{"model.json", "model.model.json"} }
	for _, name := range candidates {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() { return path }
	}
	return ""
}
