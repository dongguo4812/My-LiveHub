package emoticoncache

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCacheKeyRestrictsHostsAndSchemes(t *testing.T) {
	for _, raw := range []string{
		"http://i0.hdslb.com/bfs/live/test.png",
		"https://example.com/test.png",
	} {
		if _, _, err := cacheKey(raw,BilibiliURLPolicy); err == nil { t.Fatalf("expected URL to be rejected: %s", raw) }
	}
	key, ext, err := cacheKey("https://i0.hdslb.com/bfs/live/test.png",BilibiliURLPolicy)
	if err != nil || len(key) != 64 || ext != ".png" { t.Fatalf("unexpected cache key: %q %q %v", key, ext, err) }
}

func TestPlatformPoliciesAreSeparated(t *testing.T){m,err:=NewWithPolicies(t.TempDir(),nil,map[string]URLPolicy{"bilibili":BilibiliURLPolicy,"other":func(u *url.URL)bool{return u.Scheme=="https"&&u.Hostname()=="cdn.other.test"}});if err!=nil{t.Fatal(err)};if m.CachedURLFor("bilibili","https://cdn.other.test/a.png")!=""{t.Fatal("Bilibili accepted other CDN")};if m.CachedURLFor("other","https://i0.hdslb.com/a.png")!=""{t.Fatal("other accepted Bilibili CDN")}}
func TestServeHTTPOnlyServesCacheFilesAndFallsBack(t *testing.T) {
	root := t.TempDir()
	fallback := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	m, err := New(root, fallback)
	if err != nil { t.Fatal(err) }
	key := strings.Repeat("a", 64)
	path := filepath.Join(root, "bilibili", key+".png")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { t.Fatal(err) }
	if err := os.WriteFile(path, []byte("image"), 0o600); err != nil { t.Fatal(err) }
	req := httptest.NewRequest(http.MethodGet, routePrefix+"bilibili/"+key+".png", nil)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "image" { t.Fatalf("unexpected cache response: %d %q", res.Code, res.Body.String()) }
	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/index.html", nil))
	if res.Code != http.StatusNoContent { t.Fatalf("fallback was not called: %d", res.Code) }
}

func TestCacheFileIdentityFollowsImageURLAndPreservesHistory(t *testing.T) {
	policy := BilibiliURLPolicy
	oldURL := "https://i0.hdslb.com/a.png?token=one"
	newURL := "https://i0.hdslb.com/b.png?token=two"

	oldKey, oldExt, err := cacheKeyForResource(oldURL, "bilibili:common:emoji_331", policy)
	if err != nil { t.Fatal(err) }
	newKey, newExt, err := cacheKeyForResource(newURL, "bilibili:common:emoji_331", policy)
	if err != nil { t.Fatal(err) }
	if oldKey == newKey { t.Fatal("a changed image URL must receive a new immutable cache file") }
	if oldExt != newExt { t.Fatalf("unexpected extensions: %s and %s", oldExt, newExt) }

	// The same URL is shared even when referenced by different logical resources.
	sharedKey, sharedExt, err := cacheKeyForResource(oldURL, "bilibili:room_123:emoji_9", policy)
	if err != nil { t.Fatal(err) }
	if sharedKey != oldKey || sharedExt != oldExt {
		t.Fatal("the same source URL should reuse one image file across resources")
	}

	root := t.TempDir()
	oldPath := filepath.Join(root, "bilibili", oldKey+oldExt)
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o755); err != nil { t.Fatal(err) }
	if err := os.WriteFile(oldPath, []byte("historical image"), 0o600); err != nil { t.Fatal(err) }
	manager, err := New(root, nil)
	if err != nil { t.Fatal(err) }
	if got := manager.CachedURLForResource("bilibili", "common:emoji_331", oldURL); got != routePrefix+"bilibili/"+oldKey+oldExt {
		t.Fatalf("historical image cache URL = %q", got)
	}
	if got := manager.CachedURLForResource("bilibili", "common:emoji_331", newURL); got != "" {
		t.Fatalf("uncached replacement should fall back to original URL, got %q", got)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("historical image file was removed: %v", err)
	}
}

func TestCacheFilesAreIsolatedByPlatform(t *testing.T) {
	root := t.TempDir()
	manager, err := NewWithPolicies(root, nil, map[string]URLPolicy{
		"bilibili": BilibiliURLPolicy,
		"other": func(u *url.URL) bool { return u.Scheme == "https" && u.Hostname() == "cdn.other.test" },
	})
	if err != nil { t.Fatal(err) }
	key, ext, err := cacheKey("https://i0.hdslb.com/shared.png", BilibiliURLPolicy)
	if err != nil { t.Fatal(err) }
	for _, platformID := range []string{"bilibili", "other"} {
		dir := filepath.Join(root, platformID)
		if err := os.MkdirAll(dir, 0o755); err != nil { t.Fatal(err) }
		if err := os.WriteFile(filepath.Join(dir, key+ext), []byte(platformID), 0o600); err != nil { t.Fatal(err) }
	}
	for _, tc := range []struct{ platform, want string }{{"bilibili", "bilibili"}, {"other", "other"}} {
		req := httptest.NewRequest(http.MethodGet, routePrefix+tc.platform+"/"+key+ext, nil)
		res := httptest.NewRecorder()
		manager.ServeHTTP(res, req)
		if res.Code != http.StatusOK || res.Body.String() != tc.want { t.Fatalf("%s served %d %q", tc.platform, res.Code, res.Body.String()) }
	}
	req := httptest.NewRequest(http.MethodGet, routePrefix+"unknown/"+key+ext, nil)
	res := httptest.NewRecorder()
	manager.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound { t.Fatalf("unknown platform route returned %d", res.Code) }
}
