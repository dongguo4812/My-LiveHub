package emoticoncache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	routePrefix = "/__local_cache/emoticons/"
	maxImageSize = 8 << 20
)

type Manager struct {
	root string
	fallback http.Handler
	client *http.Client
	queue chan string
	mu sync.RWMutex
	inflight map[string]bool
	failedUntil map[string]time.Time
	ready map[string]string
}

func New(root string, fallback http.Handler) (*Manager, error) {
	if strings.TrimSpace(root) == "" { return nil, errors.New("图片缓存目录不能为空") }
	if err := os.MkdirAll(root, 0o755); err != nil { return nil, fmt.Errorf("创建图片缓存目录: %w", err) }
	m := &Manager{
		root: root, fallback: fallback,
		client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { if len(via) >= 3 { return errors.New("图片重定向次数过多") }; _, _, err := cacheKey(req.URL.String()); return err }},
		queue: make(chan string, 64),
		inflight: make(map[string]bool), failedUntil: make(map[string]time.Time), ready: make(map[string]string),
	}
	for i := 0; i < 3; i++ { go m.worker() }
	return m, nil
}

func cacheKey(raw string) (string, string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" { return "", "", errors.New("只允许 HTTPS 图片地址") }
	host := strings.ToLower(u.Hostname())
	if host != "bilibili.com" && !strings.HasSuffix(host, ".bilibili.com") && host != "hdslb.com" && !strings.HasSuffix(host, ".hdslb.com") {
		return "", "", errors.New("图片地址不属于 Bilibili 图片域名")
	}
	sum := sha256.Sum256([]byte(raw))
	ext := strings.ToLower(filepath.Ext(u.Path))
	if at := strings.IndexByte(ext, '@'); at >= 0 { ext = ext[:at] }
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".bmp":
	default: ext = ".img"
	}
	return hex.EncodeToString(sum[:]), ext, nil
}

// CachedURL returns a local resource URL only after the image is fully persisted.
func (m *Manager) CachedURL(raw string) string {
	if m == nil { return "" }
	key, ext, err := cacheKey(raw)
	if err != nil { return "" }
	m.mu.RLock()
	path := m.ready[raw]
	m.mu.RUnlock()
	if path == "" {
		path = filepath.Join(m.root, key+ext)
		if info, statErr := os.Stat(path); statErr != nil || !info.Mode().IsRegular() { return "" }
		m.mu.Lock()
		m.ready[raw] = path
		m.mu.Unlock()
	}
	return routePrefix + key + ext
}

// Prefetch schedules a bounded, de-duplicated download and never waits for network or disk I/O.
func (m *Manager) Prefetch(raw string) {
	if m == nil { return }
	if _, _, err := cacheKey(raw); err != nil { return }
	m.mu.Lock()
	if m.ready[raw] != "" || m.inflight[raw] || time.Now().Before(m.failedUntil[raw]) { m.mu.Unlock(); return }
	m.inflight[raw] = true
	m.mu.Unlock()
	select {
	case m.queue <- raw:
	default:
		m.mu.Lock()
		delete(m.inflight, raw)
		m.mu.Unlock()
	}
}

func (m *Manager) worker() {
	for raw := range m.queue {
		err := m.download(raw)
		m.mu.Lock()
		delete(m.inflight, raw)
		if err != nil { m.failedUntil[raw] = time.Now().Add(30 * time.Second) } else { delete(m.failedUntil, raw) }
		m.mu.Unlock()
	}
}

func (m *Manager) download(raw string) error {
	key, ext, err := cacheKey(raw)
	if err != nil { return err }
	target := filepath.Join(m.root, key+ext)
	if info, statErr := os.Stat(target); statErr == nil && info.Mode().IsRegular() {
		m.mu.Lock(); m.ready[raw] = target; m.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil { return err }
	req.Header.Set("User-Agent", "My-LiveHub/1.0")
	resp, err := m.client.Do(req)
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "image/") { return fmt.Errorf("图片服务器返回无效响应") }
	tmp, err := os.CreateTemp(m.root, ".download-*")
	if err != nil { return err }
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	n, copyErr := io.Copy(tmp, io.LimitReader(resp.Body, maxImageSize+1))
	if copyErr != nil || n <= 0 || n > maxImageSize { tmp.Close(); return fmt.Errorf("图片内容为空、过大或读取失败") }
	if err := tmp.Sync(); err != nil { tmp.Close(); return err }
	if err := tmp.Close(); err != nil { return err }
	if err := os.Rename(tmpName, target); err != nil {
		if info, statErr := os.Stat(target); statErr != nil || !info.Mode().IsRegular() { return err }
	}
	m.mu.Lock()
	m.ready[raw] = target
	m.mu.Unlock()
	return nil
}

// ServeHTTP serves only validated cache filenames and delegates all other paths to the app.
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m != nil && strings.HasPrefix(r.URL.Path, routePrefix) {
		name := strings.TrimPrefix(r.URL.Path, routePrefix)
		if len(name) < 64 || len(name) > 70 { http.NotFound(w, r); return }
		hash, ext := name[:64], name[64:]
		if _, err := hex.DecodeString(hash); err != nil { http.NotFound(w, r); return }
		switch ext { case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".bmp", ".img": default: http.NotFound(w, r); return }
		path := filepath.Join(m.root, hash+ext)
		file, err := os.Open(path)
		if err != nil { http.NotFound(w, r); return }
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() { http.NotFound(w, r); return }
		contentType := mime.TypeByExtension(ext)
		if contentType == "" || ext == ".img" { contentType = "application/octet-stream" }
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		http.ServeContent(w, r, name, info.ModTime(), file)
		return
	}
	if m != nil && m.fallback != nil { m.fallback.ServeHTTP(w, r); return }
	http.NotFound(w, r)
}
