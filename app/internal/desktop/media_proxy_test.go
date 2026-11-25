package desktop

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

type mediaProxyRoundTripper func(*http.Request) (*http.Response, error)

func (f mediaProxyRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestMediaProxyForwardsRangeAndCORS(t *testing.T) {
	now := time.Now()
	token := strings.Repeat("a", 64)
	proxy := &mediaProxy{
		resources: map[string]mediaProxyEntry{
			token: {resource: music.PlaybackResource{URL: "https://media.example.test/song.mp3"}, expiresAt: now.Add(time.Minute)},
		},
		now: func() time.Time { return now },
	}
	proxy.client = &http.Client{Transport: mediaProxyRoundTripper(func(request *http.Request) (*http.Response, error) {
		if got := request.Header.Get("Range"); got != "bytes=100-199" { t.Fatalf("upstream Range = %q", got) }
		if got := request.Header.Get("Accept-Encoding"); got != "identity" { t.Fatalf("upstream Accept-Encoding = %q", got) }
		return &http.Response{
			StatusCode: http.StatusPartialContent,
			Header: http.Header{
				"Accept-Ranges": {"bytes"},
				"Content-Range": {"bytes 100-199/1000"},
				"Content-Length": {"13"},
				"Content-Type": {"audio/mpeg"},
			},
			Body: io.NopCloser(strings.NewReader("partial-audio")),
		}, nil
	})}

	request := httptest.NewRequest(http.MethodGet, mediaProxyPath+token, nil)
	request.Header.Set("Range", "bytes=100-199")
	recorder := httptest.NewRecorder()
	proxy.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusPartialContent { t.Fatalf("status = %d, want 206", recorder.Code) }
	if recorder.Header().Get("Access-Control-Allow-Origin") != "*" { t.Fatal("missing CORS allow-origin") }
	if !strings.Contains(recorder.Header().Get("Access-Control-Expose-Headers"), "Content-Range") { t.Fatal("Content-Range is not exposed to the browser") }
	if recorder.Header().Get("Content-Range") != "bytes 100-199/1000" { t.Fatalf("Content-Range = %q", recorder.Header().Get("Content-Range")) }
	if recorder.Body.String() != "partial-audio" { t.Fatalf("body = %q", recorder.Body.String()) }
}

func TestMediaProxyPreflightAndMethodPolicy(t *testing.T) {
	proxy := &mediaProxy{resources: make(map[string]mediaProxyEntry), now: time.Now}
	preflight := httptest.NewRecorder()
	proxy.ServeHTTP(preflight, httptest.NewRequest(http.MethodOptions, mediaProxyPath, nil))
	if preflight.Code != http.StatusNoContent { t.Fatalf("OPTIONS status = %d", preflight.Code) }
	post := httptest.NewRecorder()
	proxy.ServeHTTP(post, httptest.NewRequest(http.MethodPost, mediaProxyPath, nil))
	if post.Code != http.StatusMethodNotAllowed { t.Fatalf("POST status = %d", post.Code) }
}

func TestMediaProxyRejectsExpiredAndUnknownTokens(t *testing.T) {
	now := time.Now()
	token := strings.Repeat("b", 64)
	proxy := &mediaProxy{
		resources: map[string]mediaProxyEntry{
			token: {resource: music.PlaybackResource{URL: "https://media.example.test/song.mp3"}, expiresAt: now.Add(-time.Second)},
		},
		now: func() time.Time { return now },
	}
	if _, ok := proxy.lookup(token); ok { t.Fatal("expired media token remained valid") }
	if _, ok := proxy.lookup(strings.Repeat("c", 64)); ok { t.Fatal("unknown media token was accepted") }
}

func TestMediaProxyPublicAddressPolicy(t *testing.T) {
	cases := []struct { address string; want bool }{
		{"1.1.1.1", true}, {"8.8.8.8", true}, {"127.0.0.1", false},
		{"10.0.0.1", false}, {"169.254.1.1", false}, {"100.64.0.1", false},
		{"192.0.2.1", false}, {"198.18.0.1", false}, {"224.0.0.1", false},
		{"::1", false}, {"fc00::1", false}, {"2001:db8::1", false},
		{"2606:4700:4700::1111", true},
	}
	for _, test := range cases {
		address := netip.MustParseAddr(test.address)
		if got := isPublicMediaIP(address); got != test.want { t.Errorf("isPublicMediaIP(%s) = %v, want %v", address, got, test.want) }
	}
}

func TestMediaProxyRedirectPolicy(t *testing.T) {
	client := newMediaProxyHTTPClient()
	previous, _ := http.NewRequest(http.MethodGet, "https://media.example.test/song.mp3", nil)
	downgrade, _ := http.NewRequest(http.MethodGet, "http://media.example.test/song.mp3", nil)
	if err := client.CheckRedirect(downgrade, []*http.Request{previous}); err == nil { t.Fatal("HTTPS downgrade redirect was accepted") }
	private, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1/private", nil)
	if err := client.CheckRedirect(private, []*http.Request{previous}); err == nil { t.Fatal("redirect to loopback was accepted") }
}
