package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
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
	transport.Proxy = nil
	transport.DialContext = store.dialPublic
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

func (s *LXUserSourceStore) dialPublic(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil { return nil, err }
	if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
		if !isPublicLXIP(ip) { return nil, errors.New("音源脚本 URL 不允许访问本机或内网地址") }
		return (&net.Dialer{Timeout: 8*time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil { return nil, fmt.Errorf("解析音源服务器地址失败：%w", err) }
	for _, ip := range ips {
		if !isPublicLXIP(ip) { return nil, errors.New("音源脚本 URL 解析到了本机或内网地址，已阻止请求") }
	}
	var lastErr error
	for _, ip := range ips {
		conn, dialErr := (&net.Dialer{Timeout: 8*time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil { return conn, nil }
		lastErr = dialErr
	}
	if lastErr == nil { lastErr = errors.New("音源服务器没有可用 IP 地址") }
	return nil, lastErr
}

func isPublicLXIP(ip netip.Addr) bool {
	return ip.IsValid() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast() && !ip.IsUnspecified()
}

func validateLXSourceURL(value *url.URL) error {
	if value == nil || (value.Scheme != "https" && value.Scheme != "http") || value.Hostname() == "" || value.User != nil {
		return errors.New("音源 URL 必须是有效的 HTTP 或 HTTPS 地址")
	}
	host := strings.TrimSuffix(strings.ToLower(value.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return errors.New("音源 URL 不允许使用本机域名")
	}
	if ip, err := netip.ParseAddr(host); err == nil && !isPublicLXIP(ip) {
		return errors.New("音源 URL 不允许访问本机或内网地址")
	}
	return nil
}

func (s *LXUserSourceStore) ImportScript(script, sourceURL string) (LXUserSourceMetadata, error) {
	if s == nil { return LXUserSourceMetadata{}, errors.New("LX 音源存储不可用") }
	source, err := ParseLXUserSource(script)
	if err != nil { return LXUserSourceMetadata{}, err }
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
	if response.ContentLength > MaxLXUserSourceBytes { return LXUserSourceMetadata{}, errors.New("LX 音源脚本超过 1 MB 限制") }
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxLXUserSourceBytes+1))
	if err != nil { return LXUserSourceMetadata{}, fmt.Errorf("读取音源脚本失败：%w", err) }
	if len(data) > MaxLXUserSourceBytes { return LXUserSourceMetadata{}, errors.New("LX 音源脚本超过 1 MB 限制") }
	return s.ImportScript(string(data), parsed.String())
}

func (s *LXUserSourceStore) save(source LXUserSource) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil { return err }
	base := filepath.Join(s.dir, source.Metadata.ID)
	scriptPath, metadataPath := base+".js", base+".json"
	if err := writeLXAtomic(scriptPath, []byte(source.Script)); err != nil { return fmt.Errorf("保存音源脚本失败：%w", err) }
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
