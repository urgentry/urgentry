package outboundhttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ValidateTargetURL rejects empty, non-HTTP(S), and private/local targets.
func ValidateTargetURL(raw string) (*url.URL, error) {
	target := strings.TrimSpace(raw)
	if target == "" {
		return nil, fmt.Errorf("empty target URL")
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("invalid target URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported target URL scheme %q", parsed.Scheme)
	}
	host := parsed.Hostname()
	if host == "" {
		return nil, fmt.Errorf("target URL host is required")
	}
	if err := validateHost(host); err != nil {
		return nil, err
	}
	return parsed, nil
}

// NewClient returns an HTTP client whose transport refuses private or local targets.
func NewClient(timeout time.Duration, base *http.Transport) *http.Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: RestrictTransport(base),
	}
}

// RestrictTransport clones the given transport and installs a dialer that
// resolves and dials only public IPs.
func RestrictTransport(base *http.Transport) *http.Transport {
	if base == nil {
		base = http.DefaultTransport.(*http.Transport).Clone()
	} else {
		base = base.Clone()
	}
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	base.DialContext = restrictedDialContext(dialer)
	return base
}

func restrictedDialContext(dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if err := validateHost(host); err != nil {
			return nil, err
		}

		if ip := parseIP(host); ip != nil {
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}

		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}

		var lastErr error
		for _, item := range ips {
			if err := validateIP(item.IP); err != nil {
				lastErr = err
				continue
			}
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(item.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("no dialable public IPs for %s", host)
	}
}

func validateHost(host string) error {
	trimmed := strings.TrimSpace(strings.ToLower(host))
	if trimmed == "" {
		return fmt.Errorf("target URL host is required")
	}
	if trimmed == "localhost" || strings.HasSuffix(trimmed, ".localhost") {
		return fmt.Errorf("refusing private or local target host %q", host)
	}
	if ip := parseIP(host); ip != nil {
		return validateIP(ip)
	}
	return nil
}

func validateIP(ip net.IP) error {
	switch {
	case ip == nil:
		return fmt.Errorf("invalid target IP")
	case ip.IsLoopback(),
		ip.IsPrivate(),
		ip.IsLinkLocalMulticast(),
		ip.IsLinkLocalUnicast(),
		ip.IsInterfaceLocalMulticast(),
		ip.IsMulticast(),
		ip.IsUnspecified():
		return fmt.Errorf("refusing private or local target IP %s", ip.String())
	default:
		return nil
	}
}

func parseIP(host string) net.IP {
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	return net.ParseIP(host)
}
