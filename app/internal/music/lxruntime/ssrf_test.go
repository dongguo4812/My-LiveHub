//go:build lxquickjs || lxproduction

package lxruntime

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestIsPublicLXIP(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"1.1.1.1", true},
		{"2606:4700:4700::1111", true},
		{"127.0.0.1", false},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false},
		{"100.64.0.1", false},
		{"0.1.2.3", false},
		{"240.1.2.3", false},
		{"2002::1", false},
		{"2001::1", false},
		{"::192.0.2.1", false},
		{"64:ff9b::a00:1", false},
		{"198.18.0.1", false},
		{"::1", false},
		{"fc00::1", false},
		{"fe80::1", false},
		{"::", false},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			ip := netip.MustParseAddr(test.value)
			if got := isPublicLXIP(ip); got != test.want {
				t.Fatalf("isPublicLXIP(%s) = %v, want %v", ip, got, test.want)
			}
		})
	}
}

func TestResolvePublicLXHostRejectsPrivateLiteral(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "::1", "fc00::1"} {
		if _, err := resolvePublicLXHost(context.Background(), host); err == nil {
			t.Errorf("resolvePublicLXHost(%q) unexpectedly allowed a private address", host)
		}
	}
}

func TestValidateLXDestinationRejectsCredentialsAndNonHTTP(t *testing.T) {
	for _, raw := range []string{
		"http://user:pass@example.com/audio",
		"file:///etc/passwd",
		"ftp://example.com/audio",
	} {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateLXDestination(context.Background(), parsed); err == nil {
			t.Errorf("validateLXDestination(%q) unexpectedly succeeded", raw)
		}
	}
}


func TestLXHTTPTasksAbortDropsActiveRequest(t *testing.T) {
	tasks := newLXHTTPTasks(nil)
	cancelled := false
	tasks.active[7] = func() { cancelled = true }
	tasks.abort([]uint64{7})
	if !cancelled { t.Fatal("abort did not cancel the request") }
	if _, exists := tasks.active[7]; exists { t.Fatal("aborted request remains active") }
}


func TestLXDestinationRejectsMixedPublicAndPrivateDNSAnswers(t *testing.T) {
	answers := []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("127.0.0.1")}
	if _, err := validateLXIPAnswers("mixed.example", answers, false); err == nil {
		t.Fatal("mixed public/private DNS answer set must be rejected as a whole")
	}
}

type lxSequenceResolver struct {
	mu sync.Mutex
	answers [][]netip.Addr
}

func (r *lxSequenceResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.answers) == 0 { return nil, errors.New("unexpected extra DNS lookup") }
	answer := r.answers[0]
	r.answers = r.answers[1:]
	return answer, nil
}

func TestLXDNSRebindingBetweenValidationAndDialIsBlocked(t *testing.T) {
	resolver := &lxSequenceResolver{answers: [][]netip.Addr{
		{netip.MustParseAddr("1.1.1.1")},
		{netip.MustParseAddr("127.0.0.1")},
	}}
	safe, err := lxSafeHTTPClientWithResolver(&http.Client{}, false, resolver)
	if err != nil { t.Fatal(err) }
	request, err := http.NewRequest(http.MethodGet, "http://rebind.example.test/audio", nil)
	if err != nil { t.Fatal(err) }
	if err := validateLXDestinationWithResolver(request.Context(), request.URL, false, resolver); err != nil {
		t.Fatalf("initial public DNS validation failed: %v", err)
	}
	_, err = safe.Do(request)
	if err == nil || !strings.Contains(err.Error(), "non-public address") {
		t.Fatalf("DNS rebinding from public to loopback should be rejected at dial time, got %v", err)
	}
	resolver.mu.Lock()
	remaining := len(resolver.answers)
	resolver.mu.Unlock()
	if remaining != 0 { t.Fatalf("expected validation and dial to perform two independent lookups; %d answers remain", remaining) }
}

func TestLXSafeHTTPClientDisablesAmbientProxy(t *testing.T) {
	client := &http.Client{Transport: http.DefaultTransport}
	safe, err := lxSafeHTTPClient(client)
	if err != nil { t.Fatal(err) }
	transport, ok := safe.Transport.(*http.Transport)
	if !ok { t.Fatalf("safe transport type = %T", safe.Transport) }
	if transport.Proxy != nil { t.Fatal("safe LX transport retained ambient proxy configuration") }
	if safe.Jar != nil { t.Fatal("safe LX client retained an ambient CookieJar") }
	if transport.DialTLS != nil || transport.DialTLSContext != nil {
		t.Fatal("safe LX transport retained a TLS dial bypass")
	}
}


type lxRejectingRoundTripper struct{}
func (lxRejectingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("test transport should never be called")
}

func TestLXSafeHTTPClientRejectsCustomTransportInProduction(t *testing.T) {
	_, err := lxSafeHTTPClient(&http.Client{Transport: lxRejectingRoundTripper{}})
	if err == nil || !strings.Contains(err.Error(), "custom HTTP transport") {
		t.Fatalf("strict policy should reject custom RoundTripper, got %v", err)
	}
}


func TestLXPlaybackURLValidationRejectsLoopback(t *testing.T) {
	target, err := url.Parse("http://127.0.0.1:23330/audio.mp3")
	if err != nil { t.Fatal(err) }
	if err := validateLXPlaybackURL(context.Background(), target); err == nil || !strings.Contains(err.Error(), "not public") {
		t.Fatalf("loopback media URL should be rejected, got %v", err)
	}
}


func TestLXSafeHTTPClientRejectsCustomRedirectCallback(t *testing.T) {
	client := &http.Client{
		Transport: http.DefaultTransport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return nil },
	}
	if _, err := lxSafeHTTPClient(client); err == nil || !strings.Contains(err.Error(), "custom redirect callbacks") {
		t.Fatalf("strict policy should reject custom redirect callbacks, got %v", err)
	}
}

func TestLXSafeHTTPClientRejectsInsecureTLSConfig(t *testing.T) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	if _, err := lxSafeHTTPClient(&http.Client{Transport: transport}); err == nil || !strings.Contains(err.Error(), "insecure TLS") {
		t.Fatalf("strict policy should reject insecure TLS config, got %v", err)
	}
}

func TestLXCrossOriginRedirectStripsSensitiveHeaders(t *testing.T) {
	calls := 0
	var finalHeaders http.Header
	transport := lxRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header: http.Header{"Location": []string{"https://8.8.8.8/final"}},
				Body: io.NopCloser(strings.NewReader("")),
				Request: req,
			}, nil
		}
		finalHeaders = req.Header.Clone()
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
	})
	safe, err := lxSafeHTTPClientWithPolicy(&http.Client{Transport: transport}, true)
	if err != nil { t.Fatal(err) }
	request, err := http.NewRequest(http.MethodGet, "https://1.1.1.1/start", nil)
	if err != nil { t.Fatal(err) }
	request.Header.Set("X-Api-Key", "secret")
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Cookie", "session=secret")
	request.Header.Set("Referer", "https://private.example/")
	request.Header.Set("User-Agent", "fixture-agent")
	request.Header.Set("Accept", "audio/*")
	request.Header.Set("Range", "bytes=0-")
	response, err := safe.Do(request)
	if err != nil { t.Fatal(err) }
	_ = response.Body.Close()
	for _, key := range []string{"X-Api-Key", "Authorization", "Cookie", "Referer"} {
		if finalHeaders.Get(key) != "" { t.Errorf("cross-origin redirect leaked %s", key) }
	}
	for key, want := range map[string]string{"User-Agent": "fixture-agent", "Accept": "audio/*", "Range": "bytes=0-"} {
		if got := finalHeaders.Get(key); got != want { t.Errorf("safe header %s = %q, want %q", key, got, want) }
	}
}


func TestLXSafeHTTPClientStripsCustomTLSProtocolHandlers(t *testing.T) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{
		"h2": func(string, *tls.Conn) http.RoundTripper { return nil },
	}
	safe, err := lxSafeHTTPClient(&http.Client{Transport: transport})
	if err != nil { t.Fatal(err) }
	safeTransport, ok := safe.Transport.(*http.Transport)
	if !ok { t.Fatalf("safe transport type = %T", safe.Transport) }
	if len(safeTransport.TLSNextProto) != 0 || safeTransport.ForceAttemptHTTP2 {
		t.Fatal("safe transport retained a custom TLS protocol handler or HTTP/2 override")
	}
}


func TestResolveLXHostRejectsNonCanonicalIPv4Literals(t *testing.T) {
	for _, host := range []string{"127.1", "2130706433", "0177.0.0.1", "0x7f000001", "0x7f.0.0.1", "1.2.3.4.5"} {
		_, err := resolveLXHostWithResolver(context.Background(), host, false, &lxSequenceResolver{})
		if err == nil || !strings.Contains(err.Error(), "non-canonical numeric IPv4") {
			t.Errorf("resolveLXHostWithResolver(%q) = %v, want non-canonical numeric IPv4 rejection", host, err)
		}
	}
}


func TestLXSafeHTTPClientStripsTLSHandlersFromReplacedDefaultTransport(t *testing.T) {
	original := http.DefaultTransport
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{
		"h2": func(string, *tls.Conn) http.RoundTripper { return nil },
	}
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = original }()
	safe, err := lxSafeHTTPClient(&http.Client{Transport: http.DefaultTransport})
	if err != nil { t.Fatal(err) }
	safeTransport, ok := safe.Transport.(*http.Transport)
	if !ok { t.Fatalf("safe transport type = %T", safe.Transport) }
	if len(safeTransport.TLSNextProto) != 0 || safeTransport.ForceAttemptHTTP2 {
		t.Fatal("safe transport retained TLS handlers from a replaced DefaultTransport")
	}
}
