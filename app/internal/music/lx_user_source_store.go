package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxLXSourceRedirects = 4

type LXUserSourceStore struct {
	dir string
	client *http.Client
}

func NewLXUserSourceStore(dir string) (*LXUserSourceStore, error) {
	if strings.TrimSpace(dir) == "" { return nil, errors.New("LX 音源存储目录不能为空") }
	store := &LXUserSourceStore{dir: dir}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	store.client = &http.Client{
		Transport: transport,
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxLXSourceRedirects { return errors.New("LX 音源 URL 重定向次数过多") }
			return validateLXSourceURL(req.URL)
		},
	}
	if err := os.MkdirAll(dir, 0o700); err != nil { return nil, fmt.Errorf("创建音源目录失败：%w", err) }
	return store, nil
}

func validateLXSourceURL(value *url.URL) error {
	// LX's Electron renderer allows ordinary HTTP(S) destinations, including
	// private-network endpoints. Custom source authors may intentionally use
	// LAN services, so do not add a host allowlist that LX does not have.
	if value == nil || (value.Scheme != "https" && value.Scheme != "http") || value.Hostname() == "" {
		return errors.New("音源 URL 必须是有效的 HTTP 或 HTTPS 地址")
	}
	return nil
}

func (s *LXUserSourceStore) ImportScript(script, sourceURL string) (LXUserSourceMetadata, error) {
	return s.importScript(script, sourceURL, true)
}

func (s *LXUserSourceStore) importScript(script, sourceURL string, rejectDuplicate bool) (LXUserSourceMetadata, error) {
	if s == nil { return LXUserSourceMetadata{}, errors.New("LX 音源存储不可用") }
	source, err := ParseLXUserSource(script)
	if err != nil { return LXUserSourceMetadata{}, err }
	if rejectDuplicate {
		name, duplicate, duplicateErr := s.findDuplicateScript(script)
		if duplicateErr != nil { return LXUserSourceMetadata{}, duplicateErr }
		if duplicate {
			if name == "" { name = "已有音源" }
			return LXUserSourceMetadata{}, fmt.Errorf("导入失败，脚本内容与已有的源「%s」相同", name)
		}
	}
	if sourceURL != "" {
		parsed, parseErr := url.Parse(strings.TrimSpace(sourceURL))
		if parseErr != nil { return LXUserSourceMetadata{}, errors.New("音源来源 URL 无效") }
		if validateErr := validateLXSourceURL(parsed); validateErr != nil { return LXUserSourceMetadata{}, validateErr }
		source.Metadata.SourceURL = parsed.String()
	}
	if err := s.save(source); err != nil { return LXUserSourceMetadata{}, err }
	return source.Metadata, nil
}

func (s *LXUserSourceStore) ImportURL(ctx context.Context, rawURL string) (LXUserSourceMetadata, error) {
	return s.importURL(ctx, rawURL, true)
}

func (s *LXUserSourceStore) importURL(ctx context.Context, rawURL string, rejectDuplicate bool) (LXUserSourceMetadata, error) {
	if s == nil || s.client == nil { return LXUserSourceMetadata{}, errors.New("LX 音源存储不可用") }
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil { return LXUserSourceMetadata{}, errors.New("音源 URL 无效") }
	if err := validateLXSourceURL(parsed); err != nil { return LXUserSourceMetadata{}, err }
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil { return LXUserSourceMetadata{}, err }
	request.Header.Set("User-Agent", "My-LiveHub-LXSource/1.0")
	response, err := s.client.Do(request)
	if err != nil { return LXUserSourceMetadata{}, fmt.Errorf("下载音源脚本失败：%w", err) }
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 { return LXUserSourceMetadata{}, fmt.Errorf("下载音源脚本失败：HTTP %d", response.StatusCode) }
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxLXUserSourceBytes*4+1))
	if err != nil { return LXUserSourceMetadata{}, fmt.Errorf("读取音源脚本失败：%w", err) }
	script := string(data)
	if lxUserSourceStringLength(script) > MaxLXUserSourceBytes { return LXUserSourceMetadata{}, errors.New("LX 音源脚本超过 9 MB 限制") }
	return s.importScript(script, parsed.String(), rejectDuplicate)
}

func (s *LXUserSourceStore) save(source LXUserSource) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil { return err }
	base := filepath.Join(s.dir, source.Metadata.ID)
	scriptPath, metadataPath := base+".js", base+".json"
	if existing, readErr := os.ReadFile(scriptPath); readErr != nil || string(existing) != source.Script {
		if err := writeLXAtomic(scriptPath, []byte(source.Script)); err != nil { return fmt.Errorf("保存音源脚本失败：%w", err) }
	}
	metadata, err := json.MarshalIndent(source.Metadata, "", "  ")
	if err != nil { _ = os.Remove(scriptPath); return err }
	if err := writeLXAtomic(metadataPath, append(metadata, '\n')); err != nil { _ = os.Remove(scriptPath); return fmt.Errorf("保存音源信息失败：%w", err) }
	return nil
}

func writeLXAtomic(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".lx-source-*.tmp")
	if err != nil { return err }
	temp := file.Name()
	defer os.Remove(temp)
	if _, err = file.Write(data); err == nil { err = file.Sync() }
	closeErr := file.Close()
	if err != nil { return err }
	if closeErr != nil { return closeErr }
	return os.Rename(temp, path)
}


func (s *LXUserSourceStore) Update(ctx context.Context, id string) (LXUserSourceMetadata, error) {
	current, err := s.Get(id)
	if err != nil { return LXUserSourceMetadata{}, err }
	if strings.TrimSpace(current.SourceURL) == "" { return LXUserSourceMetadata{}, errors.New("该音源没有更新 URL，请重新导入本地脚本或使用 URL 导入") }
	updated, err := s.importURL(ctx, current.SourceURL, false)
	if err != nil { return LXUserSourceMetadata{}, err }
	updated.AllowShowUpdateAlert = current.AllowShowUpdateAlert
	updatedScript, err := s.ReadScript(updated.ID)
	if err != nil { return LXUserSourceMetadata{}, err }
	if err := s.save(LXUserSource{Metadata: updated, Script: updatedScript}); err != nil { return LXUserSourceMetadata{}, err }
	if updated.ID != id {
		if err := s.Delete(id); err != nil { return LXUserSourceMetadata{}, fmt.Errorf("新版本已保存，但旧版本清理失败：%w", err) }
	}
	return updated, nil
}

func (s *LXUserSourceStore) ReadScript(id string) (string, error) {
	if s == nil { return "", errors.New("LX 音源存储不可用") }
	if !validLXUserSourceID(id) { return "", errors.New("LX 音源 ID 无效") }
	data, err := os.ReadFile(filepath.Join(s.dir, id+".js"))
	if err != nil { return "", fmt.Errorf("读取音源脚本失败：%w", err) }
	if len(data) == 0 || lxUserSourceStringLength(string(data)) > MaxLXUserSourceBytes { return "", errors.New("LX 音源脚本为空或超过 9 MB 限制") }
	return string(data), nil
}

func (s *LXUserSourceStore) findDuplicateScript(script string) (string, bool, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) { return "", false, nil }
	if err != nil { return "", false, err }
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".js") { continue }
		path := filepath.Join(s.dir, entry.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil { return "", false, readErr }
		if string(data) != script { continue }
		name := ""
		if metadataBytes, metadataErr := os.ReadFile(strings.TrimSuffix(path, ".js") + ".json"); metadataErr == nil {
			var metadata LXUserSourceMetadata
			if json.Unmarshal(metadataBytes, &metadata) == nil { name = metadata.Name }
		}
		return name, true, nil
	}
	return "", false, nil
}

func validLXUserSourceID(id string) bool {
	if strings.HasPrefix(id, "lx_") {
		if len(id) != 19 { return false }
		for _, char := range id[3:] {
			if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) { return false }
		}
		return true
	}
	parts := strings.Split(id, "_")
	if len(parts) != 4 || parts[0] != "user" || parts[1] != "api" || len(parts[2]) != 3 || len(parts[3]) < 10 { return false }
	for _, part := range parts[2:] { for _, char := range part { if char < '0' || char > '9' { return false } } }
	return true
}

func (s *LXUserSourceStore) Get(id string) (LXUserSourceMetadata, error) {
	if s == nil { return LXUserSourceMetadata{}, errors.New("LX 音源存储不可用") }
	if !validLXUserSourceID(id) { return LXUserSourceMetadata{}, errors.New("LX 音源 ID 无效") }
	data, err := os.ReadFile(filepath.Join(s.dir, id+".json"))
	if err != nil { return LXUserSourceMetadata{}, fmt.Errorf("读取音源信息失败：%w", err) }
	var metadata LXUserSourceMetadata
	if err := json.Unmarshal(data, &metadata); err != nil { return LXUserSourceMetadata{}, fmt.Errorf("解析音源信息失败：%w", err) }
	if metadata.ID != id || metadata.Name == "" { return LXUserSourceMetadata{}, errors.New("LX 音源信息与 ID 不匹配") }
	return metadata, nil
}

// SetValidation stores a successful initialization result alongside the exact
// source version. A changed script must never inherit an old validation result.
func (s *LXUserSourceStore) SetValidation(id string, result LXSourceRuntimeResult) error {
	if !result.Initialized {
		return errors.New("不能保存未通过初始化的 LX 音源验证结果")
	}
	metadata, err := s.Get(id)
	if err != nil { return err }
	script, err := s.ReadScript(id)
	if err != nil { return err }
	source, err := ParseLXUserSource(script)
	if err != nil { return err }
	if metadata.SHA256 != source.Metadata.SHA256 {
		return errors.New("LX 音源脚本已变化，请重新验证")
	}
	now := time.Now().UTC()
	metadata.Validation = &result
	metadata.ValidatedAt = &now
	return s.save(LXUserSource{Metadata: metadata, Script: script})
}

// SetEnabledSearchSources persists the user's search-provider selection for one source.
// allowed is the set of providers supported by both the LX script and the local search SDK.
func (s *LXUserSourceStore) SetEnabledSearchSources(id string, allowed, enabled []string) error {
	metadata, err := s.Get(id)
	if err != nil { return err }
	allowedSet := make(map[string]bool, len(allowed))
	for _, key := range allowed { allowedSet[key] = true }
	selected := make(map[string]bool, len(enabled))
	for _, key := range enabled {
		if !allowedSet[key] { return fmt.Errorf("当前音源不支持搜索来源：%s", key) }
		selected[key] = true
	}
	ordered := make([]string, 0, len(selected))
	seen := make(map[string]bool, len(selected))
	for _, key := range enabled {
		if selected[key] && !seen[key] { ordered = append(ordered, key); seen[key] = true }
	}
	metadata.EnabledSearchSources = ordered
	script, err := s.ReadScript(id)
	if err != nil { return err }
	return s.save(LXUserSource{Metadata: metadata, Script: script})
}

func (s *LXUserSourceStore) SetAllowShowUpdateAlert(id string, enabled bool) error {
	metadata, err := s.Get(id)
	if err != nil { return err }
	metadata.AllowShowUpdateAlert = enabled
	script, err := s.ReadScript(id)
	if err != nil { return err }
	return s.save(LXUserSource{Metadata: metadata, Script: script})
}

func (s *LXUserSourceStore) GetActiveID() (string, error) {
	if s == nil { return "", errors.New("LX 音源存储不可用") }
	data, err := os.ReadFile(filepath.Join(s.dir, "active.json"))
	if errors.Is(err, os.ErrNotExist) { return "", nil }
	if err != nil { return "", err }
	var state struct { ID string `json:"id"` }
	if err := json.Unmarshal(data, &state); err != nil { return "", err }
	if state.ID != "" && !validLXUserSourceID(state.ID) { return "", errors.New("保存的 LX 当前音源 ID 无效") }
	return state.ID, nil
}

func (s *LXUserSourceStore) SetActiveID(id string) error {
	if s == nil { return errors.New("LX 音源存储不可用") }
	if id != "" && !validLXUserSourceID(id) { return errors.New("LX 音源 ID 无效") }
	data, err := json.Marshal(struct { ID string `json:"id"` }{ID:id})
	if err != nil { return err }
	return writeLXAtomic(filepath.Join(s.dir, "active.json"), append(data, '\n'))
}

func (s *LXUserSourceStore) List() ([]LXUserSourceMetadata, error) {
	if s == nil { return nil, errors.New("LX 音源存储不可用") }
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) { return []LXUserSourceMetadata{}, nil }
	if err != nil { return nil, err }
	result := make([]LXUserSourceMetadata, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") { continue }
		data, readErr := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if readErr != nil { continue }
		var metadata LXUserSourceMetadata
		if json.Unmarshal(data, &metadata) != nil || metadata.ID == "" || metadata.Name == "" { continue }
		result = append(result, metadata)
	}
	sort.SliceStable(result, func(i,j int) bool {
		if result[i].ImportedAt.Equal(result[j].ImportedAt) { return result[i].Name < result[j].Name }
		return result[i].ImportedAt.Before(result[j].ImportedAt)
	})
	return result, nil
}

func (s *LXUserSourceStore) Delete(id string) error {
	if s == nil { return errors.New("LX 音源存储不可用") }
	if !validLXUserSourceID(id) { return errors.New("LX 音源 ID 无效") }
	for _, ext := range []string{".js", ".json"} {
		if err := os.Remove(filepath.Join(s.dir, id+ext)); err != nil && !errors.Is(err, os.ErrNotExist) { return err }
	}
	return nil
}
