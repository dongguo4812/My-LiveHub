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
	if client == nil {
		client = http.DefaultClient
	}
	clone := *client
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	if transport, ok := base.(*http.Transport); ok {
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
			ips, err := resolveLXHost(ctx, host, allowPrivate)
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
		// Custom RoundTrippers are useful for deterministic tests. They cannot
		// provide DNS pinning, so only permit literal public IP destinations.
		clone.Transport = lxLiteralIPPolicyTransport{next: base, allowPrivate: allowPrivate}
	}
	previousRedirect := client.CheckRedirect
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("lx.request: too many redirects")
		}
		if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return errors.New("lx.request: HTTPS redirect downgrade is not allowed")
		}
		if err := validateLXDestinationWithPolicy(req.Context(), req.URL, allowPrivate); err != nil {
			return err
		}
		if previousRedirect != nil {
			return previousRedirect(req, via)
		}
		return nil
	}
	return &clone, nil
}

type lxLiteralIPPolicyTransport struct {
	next http.RoundTripper
	allowPrivate bool
}

func (t lxLiteralIPPolicyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.allowPrivate {
		if _, err := netip.ParseAddr(req.URL.Hostname()); err != nil {
			return nil, errors.New("lx.request: custom transport cannot guarantee DNS pinning")
		}
	}
	if err := validateLXDestinationWithPolicy(req.Context(), req.URL, t.allowPrivate); err != nil {
		return nil, err
	}
	return t.next.RoundTrip(req)
}

func validateLXDestination(ctx context.Context, target *url.URL) error {
	return validateLXDestinationWithPolicy(ctx, target, false)
}

func validateLXDestinationWithPolicy(ctx context.Context, target *url.URL, allowPrivate bool) error {
	if target == nil || (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" {
		return errors.New("lx.request: only absolute HTTP(S) URLs are supported")
	}
	if target.User != nil {
		return errors.New("lx.request: URL credentials are not allowed")
	}
	_, err := resolveLXHost(ctx, target.Hostname(), allowPrivate)
	return err
}

func resolvePublicLXHost(ctx context.Context, host string) ([]netip.Addr, error) {
	return resolveLXHost(ctx, host, false)
}

func resolveLXHost(ctx context.Context, host string, allowPrivate bool) ([]netip.Addr, error) {
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
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("lx.request: resolve destination: %w", err)
	}
	if len(ips) == 0 {
		return nil, errors.New("lx.request: destination has no IP addresses")
	}
	checked := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		ip = ip.Unmap()
		if !allowPrivate && !isPublicLXIP(ip) {
			return nil, fmt.Errorf("lx.request: host resolves to a non-public address: %s", ip)
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
