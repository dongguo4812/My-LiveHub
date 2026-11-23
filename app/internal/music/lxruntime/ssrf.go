//go:build (lxquickjs || lxproduction) && cgo

package lxruntime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// lxSafeHTTPClient applies the script network boundary independently of the
// caller's HTTP client. The default transport pins each connection to an IP
// address that was resolved and checked immediately before dialing, preventing
// DNS rebinding between validation and connection.
func lxSafeHTTPClient(client *http.Client) (*http.Client, error) {
	return lxSafeHTTPClientWithPolicy(client, false)
}

// lxSafeHTTPClientWithPolicy allows private destinations only for isolated local HTTP tests.
func lxSafeHTTPClientWithPolicy(client *http.Client, allowPrivate bool) (*http.Client, error) {
	return lxSafeHTTPClientWithResolver(client, allowPrivate, net.DefaultResolver)
}

// lxSafeHTTPClientWithResolver keeps DNS behavior injectable for deterministic
// rebinding tests; production always supplies net.DefaultResolver.
func lxSafeHTTPClientWithResolver(client *http.Client, allowPrivate bool, resolver lxHostResolver) (*http.Client, error) {
	if resolver == nil { resolver = net.DefaultResolver }
	if client == nil {
		client = http.DefaultClient
	}
	clone := *client
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	if transport, ok := base.(*http.Transport); ok {
		if !allowPrivate && transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
			return nil, errors.New("lx.request: insecure TLS verification is not allowed")
		}
		if !allowPrivate && len(transport.TLSNextProto) > 0 {
			return nil, errors.New("lx.request: custom TLS protocol handlers are not allowed")
		}
		safeTransport := transport.Clone()
		// Do not let an ambient proxy bypass the address policy.
		safeTransport.Proxy = nil
		// Force HTTPS through the validated DialContext path as well.
		safeTransport.DialTLS = nil
		safeTransport.DialTLSContext = nil
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		safeTransport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("lx.request: invalid destination: %w", err)
			}
			ips, err := resolveLXHostWithResolver(ctx, host, allowPrivate, resolver)
			if err != nil {
				return nil, err
			}
			var failures []string
			for _, ip := range ips {
				conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if dialErr == nil {
					return conn, nil
				}
				failures = append(failures, dialErr.Error())
			}
			return nil, fmt.Errorf("lx.request: connect failed: %s", strings.Join(failures, "; "))
		}
		clone.Transport = safeTransport
	} else {
		// A custom RoundTripper can ignore req.URL and dial an arbitrary address;
		// URL validation cannot constrain its actual network destination.
		// Production therefore rejects it. Tests may opt into an injected
		// transport only through the explicit allowPrivate test policy.
		if !allowPrivate {
			return nil, errors.New("lx.request: custom HTTP transport is not allowed by strict network policy")
		}
		clone.Transport = base
	}
	previousRedirect := client.CheckRedirect
	if !allowPrivate && previousRedirect != nil {
		return nil, errors.New("lx.request: custom redirect callbacks are not allowed by strict network policy")
	}
	if !allowPrivate {
		// CookieJar is an ambient credential source outside the script request
		// descriptor; scripts must supply any required Cookie header explicitly.
		clone.Jar = nil
	}
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("lx.request: too many redirects")
		}
		if previousRedirect != nil {
			if err := previousRedirect(req, via); err != nil {
				return err
			}
		}
		if len(via) > 0 {
			previousURL := via[len(via)-1].URL
			if previousURL.Scheme == "https" && req.URL.Scheme != "https" {
				return errors.New("lx.request: HTTPS redirect downgrade is not allowed")
			}
			if !sameLXOrigin(previousURL, req.URL) {
				if req.Body != nil && req.Body != http.NoBody {
					return errors.New("lx.request: cross-origin redirect with a request body is not allowed")
				}
				for key := range req.Header {
					switch strings.ToLower(key) {
					case "user-agent", "accept", "accept-encoding", "range":
					default:
						req.Header.Del(key)
					}
				}
			}
		}
		if err := validateLXDestinationWithResolver(req.Context(), req.URL, allowPrivate, resolver); err != nil {
			return err
		}
		return nil
	}
	return &clone, nil
}

func sameLXOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		lxEffectivePort(a) == lxEffectivePort(b)
}

func lxEffectivePort(target *url.URL) string {
	if target == nil {
		return ""
	}
	if port := target.Port(); port != "" {
		return port
	}
	switch strings.ToLower(target.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func validateLXDestination(ctx context.Context, target *url.URL) error {
	return validateLXDestinationWithPolicy(ctx, target, false)
}

func validateLXDestinationWithPolicy(ctx context.Context, target *url.URL, allowPrivate bool) error {
	return validateLXDestinationWithResolver(ctx, target, allowPrivate, net.DefaultResolver)
}

func validateLXPlaybackURL(ctx context.Context, target *url.URL) error {
	if ctx == nil { ctx = context.Background() }
	select {
	case lxRequestSemaphore <- struct{}{}:
		defer func() { <-lxRequestSemaphore }()
	case <-ctx.Done():
		return ctx.Err()
	}
	return validateLXDestinationWithPolicy(ctx, target, false)
}

func validateLXDestinationWithResolver(ctx context.Context, target *url.URL, allowPrivate bool, resolver lxHostResolver) error {
	if target == nil || (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" {
		return errors.New("lx.request: only absolute HTTP(S) URLs are supported")
	}
	if target.User != nil {
		return errors.New("lx.request: URL credentials are not allowed")
	}
	// allowPrivate is an unexported test-only policy used by newTestRuntime
	// and httptest transports. Production callers always use strict DNS checks.
	if allowPrivate {
		return nil
	}
	_, err := resolveLXHostWithResolver(ctx, target.Hostname(), false, resolver)
	return err
}

func resolvePublicLXHost(ctx context.Context, host string) ([]netip.Addr, error) {
	return resolveLXHost(ctx, host, false)
}

type lxHostResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

func resolveLXHost(ctx context.Context, host string, allowPrivate bool) ([]netip.Addr, error) {
	return resolveLXHostWithResolver(ctx, host, allowPrivate, net.DefaultResolver)
}

func resolveLXHostWithResolver(ctx context.Context, host string, allowPrivate bool, resolver lxHostResolver) ([]netip.Addr, error) {
	if resolver == nil { resolver = net.DefaultResolver }
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if host == "" {
		return nil, errors.New("lx.request: empty destination host")
	}
	if strings.Contains(host, "%") {
		return nil, errors.New("lx.request: IPv6 zone identifiers are not allowed")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		if !allowPrivate && !isPublicLXIP(ip) {
			return nil, fmt.Errorf("lx.request: destination address is not public: %s", ip)
		}
		return []netip.Addr{ip}, nil
	}
	if isNonCanonicalIPv4Host(host) {
		return nil, fmt.Errorf("lx.request: non-canonical numeric IPv4 host is not allowed: %s", host)
	}
	ips, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("lx.request: resolve destination: %w", err)
	}
	if len(ips) == 0 {
		return nil, errors.New("lx.request: destination has no IP addresses")
	}
	return validateLXIPAnswers(host, ips, allowPrivate)
}

func isNonCanonicalIPv4Host(host string) bool {
	parts := strings.Split(host, ".")
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		allDigits := true
		for _, char := range part {
			if char < '0' || char > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			continue
		}
		lower := strings.ToLower(part)
		if strings.HasPrefix(lower, "0x") && len(lower) > 2 {
			allHex := true
			for _, char := range lower[2:] {
				if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
					allHex = false
					break
				}
			}
			if allHex {
				continue
			}
		}
		return false
	}
	return true
}

func validateLXIPAnswers(host string, ips []netip.Addr, allowPrivate bool) ([]netip.Addr, error) {
	if len(ips) == 0 {
		return nil, errors.New("lx.request: destination has no IP addresses")
	}
	checked := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		ip = ip.Unmap()
		if !allowPrivate && !isPublicLXIP(ip) {
			return nil, fmt.Errorf("lx.request: host %q resolves to a non-public address: %s", host, ip)
		}
		checked = append(checked, ip)
	}
	return checked, nil
}

func isPublicLXIP(ip netip.Addr) bool {
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	// Also deny shared address space, benchmarking and special-use ranges
	// that are not consistently classified as private by all Go versions.
	blocked := []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("192.88.99.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("224.0.0.0/4"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("64:ff9b::/96"),
		netip.MustParsePrefix("64:ff9b:1::/48"),
		netip.MustParsePrefix("::/96"),
		netip.MustParsePrefix("100::/64"),
		netip.MustParsePrefix("2001::/23"),
		netip.MustParsePrefix("2001:db8::/32"),
		netip.MustParsePrefix("2002::/16"),
	}
	for _, prefix := range blocked {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}
