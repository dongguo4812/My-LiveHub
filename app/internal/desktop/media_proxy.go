package desktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

const (
	mediaProxyPath       = "/__mylivehub_media/"
	mediaProxyTokenTTL   = 6 * time.Hour
	mediaProxyMaxEntries = 512
)

type mediaProxyEntry struct {
	resource  music.PlaybackResource
	expiresAt time.Time
}

type mediaProxy struct {
	mu        sync.Mutex
	listener  net.Listener
	server    *http.Server
	client    *http.Client
	baseURL   string
	resources map[string]mediaProxyEntry
	now       func() time.Time
}

func newMediaProxy() (*mediaProxy, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for local music media proxy: %w", err)
	}
	proxy := &mediaProxy{
		listener: listener,
		baseURL: "http://" + listener.Addr().String(),
		resources: make(map[string]mediaProxyEntry),
		now: time.Now,
	}
	proxy.client = newMediaProxyHTTPClient()
	proxy.server = &http.Server{
		Handler:           proxy,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	go func() {
		if serveErr := proxy.server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			// A closed listener is expected during application shutdown.
		}
	}()
	return proxy, nil
}

func (p *mediaProxy) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	server := p.server
	p.resources = make(map[string]mediaProxyEntry)
	p.mu.Unlock()
	if server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		_ = server.Close()
		return err
	}
	return nil
}

// Register stores the upstream URL server-side and returns an unguessable
// loopback URL. The browser never receives the upstream URL or request headers.
func (p *mediaProxy) Register(resource music.PlaybackResource) (string, error) {
	if p == nil {
		return "", errors.New("music media proxy is unavailable")
	}
	target, err := url.Parse(strings.TrimSpace(resource.URL))
	if err != nil {
		return "", errors.New("music media URL is invalid")
	}
	if err := validateMediaProxyURL(target); err != nil {
		return "", err
	}
	var tokenBytes [32]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return "", fmt.Errorf("create music media token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes[:])
	now := p.now()
	expiresAt := now.Add(mediaProxyTokenTTL)
	if !resource.ExpiresAt.IsZero() && resource.ExpiresAt.Before(expiresAt) {
		expiresAt = resource.ExpiresAt
	}
	if !expiresAt.After(now) {
		return "", errors.New("music media URL has already expired")
	}
	resource.Headers = cloneMediaProxyHeaders(resource.Headers)

	p.mu.Lock()
	defer p.mu.Unlock()
	for key, entry := range p.resources {
		if !now.Before(entry.expiresAt) {
			delete(p.resources, key)
		}
	}
	if len(p.resources) >= mediaProxyMaxEntries {
		oldestKey := ""
		var oldest time.Time
		for key, entry := range p.resources {
			if oldestKey == "" || entry.expiresAt.Before(oldest) {
				oldestKey, oldest = key, entry.expiresAt
			}
		}
		delete(p.resources, oldestKey)
	}
	p.resources[token] = mediaProxyEntry{resource: resource, expiresAt: expiresAt}
	return p.baseURL + mediaProxyPath + token, nil
}

func (p *mediaProxy) lookup(token string) (mediaProxyEntry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.resources[token]
	if !ok {
		return mediaProxyEntry{}, false
	}
	if !p.now().Before(entry.expiresAt) {
		delete(p.resources, token)
		return mediaProxyEntry{}, false
	}
	return entry, true
}

func (p *mediaProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setMediaProxyCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !strings.HasPrefix(r.URL.Path, mediaProxyPath) {
		http.NotFound(w, r)
		return
	}
	token := strings.TrimPrefix(r.URL.Path, mediaProxyPath)
	if len(token) != 64 {
		http.NotFound(w, r)
		return
	}
	entry, ok := p.lookup(token)
	if !ok {
		http.NotFound(w, r)
		return
	}
	target, err := url.Parse(entry.resource.URL)
	if err != nil || validateMediaProxyURL(target) != nil {
		http.Error(w, "invalid upstream media URL", http.StatusBadGateway)
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), nil)
	if err != nil {
		http.Error(w, "invalid upstream media request", http.StatusBadGateway)
		return
	}
	for _, name := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since", "Accept", "Accept-Language", "User-Agent"} {
		if value := r.Header.Get(name); value != "" {
			request.Header.Set(name, value)
		}
	}
	for name, value := range entry.resource.Headers {
		switch strings.ToLower(name) {
		case "accept", "accept-language", "referer", "user-agent":
			request.Header.Set(name, value)
		}
	}
	request.Header.Set("Accept-Encoding", "identity")

	response, err := p.client.Do(request)
	if err != nil {
		http.Error(w, "upstream media request failed", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	for _, name := range []string{
		"Accept-Ranges", "Content-Range", "Content-Length", "Content-Type",
		"Content-Encoding", "ETag", "Last-Modified",
	} {
		if value := response.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(response.StatusCode)
	if r.Method == http.MethodHead || response.Body == nil {
		return
	}
	_, _ = io.Copy(w, response.Body)
}

func setMediaProxyCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Range, If-Range, If-None-Match, If-Modified-Since")
	w.Header().Set("Access-Control-Expose-Headers", "Accept-Ranges, Content-Range, Content-Length, Content-Type, ETag, Last-Modified")
}

func cloneMediaProxyHeaders(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "accept", "accept-language", "referer", "user-agent":
			result[key] = value
		}
	}
	return result
}

func validateMediaProxyURL(target *url.URL) error {
	if target == nil || (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" {
		return errors.New("music media URL must be an absolute HTTP(S) URL")
	}
	if target.User != nil || target.Fragment != "" {
		return errors.New("music media URL cannot contain credentials or a fragment")
	}
	if ip, err := netip.ParseAddr(strings.Trim(target.Hostname(), "[]")); err == nil && !isPublicMediaIP(ip) {
		return errors.New("music media URL cannot target a non-public address")
	}
	return nil
}

func newMediaProxyHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		DialContext:           dialPublicMediaAddress,
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("music media redirect limit exceeded")
			}
			if err := validateMediaProxyURL(request.URL); err != nil {
				return err
			}
			if len(via) > 0 {
				previous := via[len(via)-1].URL
				if previous.Scheme == "https" && request.URL.Scheme != "https" {
					return errors.New("music media HTTPS redirect downgrade is not allowed")
				}
				if !sameMediaProxyOrigin(previous, request.URL) {
					for _, name := range []string{"Authorization", "Cookie", "Referer", "Origin"} {
						request.Header.Del(name)
					}
				}
			}
			return nil
		},
	}
}

func sameMediaProxyOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		mediaProxyPort(a) == mediaProxyPort(b)
}

func mediaProxyPort(target *url.URL) string {
	if port := target.Port(); port != "" {
		return port
	}
	if target.Scheme == "https" {
		return "443"
	}
	return "80"
}

// Resolve and pin a public address for each connection. The transport still
// performs TLS verification against the original hostname from the request URL.
func dialPublicMediaAddress(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream media address: %w", err)
	}
	var ips []netip.Addr
	if ip, parseErr := netip.ParseAddr(strings.Trim(host, "[]")); parseErr == nil {
		ips = []netip.Addr{ip.Unmap()}
	} else {
		ips, err = net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("resolve upstream media host: %w", err)
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("upstream media host has no IP addresses")
	}
	for _, ip := range ips {
		if !isPublicMediaIP(ip) {
			return nil, fmt.Errorf("upstream media host resolves to a non-public address: %s", ip)
		}
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	var failures []string
	for _, ip := range ips {
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		failures = append(failures, dialErr.Error())
	}
	return nil, fmt.Errorf("connect to upstream media host: %s", strings.Join(failures, "; "))
}

func isPublicMediaIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, raw := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
		"192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"64:ff9b::/96", "64:ff9b:1::/48", "2001::/23",
		"2001:db8::/32", "2002::/16",
	} {
		if netip.MustParsePrefix(raw).Contains(ip) {
			return false
		}
	}
	return true
}
