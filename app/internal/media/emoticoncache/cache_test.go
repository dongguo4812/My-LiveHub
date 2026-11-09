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
	path := filepath.Join(root, key+".png")
	if err := os.WriteFile(path, []byte("image"), 0o600); err != nil { t.Fatal(err) }
	req := httptest.NewRequest(http.MethodGet, routePrefix+key+".png", nil)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "image" { t.Fatalf("unexpected cache response: %d %q", res.Code, res.Body.String()) }
	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/index.html", nil))
	if res.Code != http.StatusNoContent { t.Fatalf("fallback was not called: %d", res.Code) }
}

func TestResourceIdentityProducesStableSingleCacheKey(t *testing.T) {
	policy := BilibiliURLPolicy
	first, ext1, err := cacheKeyForResource("https://i0.hdslb.com/a.png?token=one", "bilibili:common:[dog]", policy)
	if err != nil { t.Fatal(err) }
	second, ext2, err := cacheKeyForResource("https://i0.hdslb.com/b.png?token=two", "bilibili:common:[dog]", policy)
	if err != nil { t.Fatal(err) }
	if first != second || ext1 != ext2 { t.Fatalf("same resource should share one cache file: %s %s %s %s", first, ext1, second, ext2) }
	other, _, err := cacheKeyForResource("https://i0.hdslb.com/a.png?token=one", "bilibili:room_123:[dog]", policy)
	if err != nil { t.Fatal(err) }
	if other == first { t.Fatal("different resource identities must not share cache files") }
}
