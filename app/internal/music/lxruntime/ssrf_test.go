//go:build (lxquickjs || lxproduction) && cgo

package lxruntime

import (
	"context"
	"net/netip"
	"net/http"
	"sync"
	"errors"
	"strings"
	"net/url"
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
