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
	if sourceURL != "" {
		parsed, parseErr := url.Parse(strings.TrimSpace(sourceURL))
		if parseErr != nil { return LXUserSourceMetadata{}, errors.New("音源来源 URL 无效") }
		if validateErr := validateLXSourceURL(parsed); validateErr != nil { return LXUserSourceMetadata{}, validateErr }
		source.Metadata.SourceURL = parsed.String()
	}
	if rejectDuplicate {
		existing, readErr := os.ReadFile(filepath.Join(s.dir, source.Metadata.ID+".js"))
		if readErr == nil && string(existing) == script {
			name := "已有音源"
			if metadataBytes, metadataErr := os.ReadFile(filepath.Join(s.dir, source.Metadata.ID+".json")); metadataErr == nil {
				var metadata LXUserSourceMetadata
				if json.Unmarshal(metadataBytes, &metadata) == nil && metadata.Name != "" { name = metadata.Name }
			}
			return LXUserSourceMetadata{}, fmt.Errorf("导入失败，脚本内容与已有的源「%s」相同", name)
		}
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) { return LXUserSourceMetadata{}, readErr }
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
	if response.ContentLength > MaxLXUserSourceBytes { return LXUserSourceMetadata{}, errors.New("LX 音源脚本超过 9 MB 限制") }
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxLXUserSourceBytes+1))
	if err != nil { return LXUserSourceMetadata{}, fmt.Errorf("读取音源脚本失败：%w", err) }
	if len(data) > MaxLXUserSourceBytes { return LXUserSourceMetadata{}, errors.New("LX 音源脚本超过 9 MB 限制") }
	return s.importScript(string(data), parsed.String(), rejectDuplicate)
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
	if s == nil { return LXUserSourceMetadata{}, errors.New("LX 音源存储不可用") }
	if len(id) != 19 || !strings.HasPrefix(id, "lx_") { return LXUserSourceMetadata{}, errors.New("LX 音源 ID 无效") }
	data, err := os.ReadFile(filepath.Join(s.dir, id+".json"))
	if err != nil { return LXUserSourceMetadata{}, fmt.Errorf("读取音源信息失败：%w", err) }
	var current LXUserSourceMetadata
	if err := json.Unmarshal(data, &current); err != nil { return LXUserSourceMetadata{}, fmt.Errorf("解析音源信息失败：%w", err) }
	if strings.TrimSpace(current.SourceURL) == "" { return LXUserSourceMetadata{}, errors.New("该音源没有更新 URL，请重新导入本地脚本或使用 URL 导入") }
	updated, err := s.importURL(ctx, current.SourceURL, false)
	if err != nil { return LXUserSourceMetadata{}, err }
	if updated.ID != id {
		if err := s.Delete(id); err != nil { return LXUserSourceMetadata{}, fmt.Errorf("新版本已保存，但旧版本清理失败：%w", err) }
	}
	return updated, nil
}

func (s *LXUserSourceStore) ReadScript(id string) (string, error) {
	if s == nil { return "", errors.New("LX 音源存储不可用") }
	if len(id) != 19 || !strings.HasPrefix(id, "lx_") { return "", errors.New("LX 音源 ID 无效") }
	for _, char := range id[3:] {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return "", errors.New("LX 音源 ID 无效")
		}
	}
	data, err := os.ReadFile(filepath.Join(s.dir, id+".js"))
	if err != nil { return "", fmt.Errorf("读取音源脚本失败：%w", err) }
	if len(data) == 0 || len(data) > MaxLXUserSourceBytes { return "", errors.New("LX 音源脚本为空或超过 9 MB 限制") }
	return string(data), nil
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
	sort.Slice(result, func(i,j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (s *LXUserSourceStore) Delete(id string) error {
	if s == nil { return errors.New("LX 音源存储不可用") }
	if len(id) != 19 || !strings.HasPrefix(id, "lx_") { return errors.New("LX 音源 ID 无效") }
	for _, char := range id[3:] {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) { return errors.New("LX 音源 ID 无效") }
	}
	for _, ext := range []string{".js", ".json"} {
		if err := os.Remove(filepath.Join(s.dir, id+ext)); err != nil && !errors.Is(err, os.ErrNotExist) { return err }
	}
	return nil
}
