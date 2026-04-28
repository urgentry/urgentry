package requestmeta

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
)

var (
	mu             sync.RWMutex
	trustedProxies []*net.IPNet
)

func ConfigureTrustedProxies(raw string) error {
	nets, err := parseTrustedProxies(raw)
	if err != nil {
		return err
	}
	mu.Lock()
	trustedProxies = nets
	mu.Unlock()
	return nil
}

func ClientIP(r *http.Request) string {
	remote := remoteIP(r)
	if remote == "" {
		return strings.TrimSpace(r.RemoteAddr)
	}
	if !remoteAddrTrusted(remote) {
		return remote
	}
	if forwarded := firstHeaderIP(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		return forwarded
	}
	if realIP := firstHeaderIP(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}
	return remote
}

func IsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if !remoteAddrTrusted(remoteIP(r)) {
		return false
	}
	return strings.EqualFold(firstHeaderValue(r.Header.Get("X-Forwarded-Proto")), "https")
}

func Scheme(r *http.Request) string {
	if IsSecure(r) {
		return "https"
	}
	return "http"
}

func Host(r *http.Request) string {
	if remoteAddrTrusted(remoteIP(r)) {
		if host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); host != "" {
			return firstHeaderValue(host)
		}
	}
	return r.Host
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func remoteAddrTrusted(remote string) bool {
	ip := net.ParseIP(remote)
	if ip == nil {
		return false
	}
	mu.RLock()
	defer mu.RUnlock()
	for _, network := range trustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func firstHeaderValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	first, _, _ := strings.Cut(raw, ",")
	first = strings.TrimSpace(first)
	if containsControl(first) {
		return ""
	}
	return first
}

func firstHeaderIP(raw string) string {
	value := firstHeaderValue(raw)
	if value == "" || net.ParseIP(value) == nil {
		return ""
	}
	return value
}

func containsControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func parseTrustedProxies(raw string) ([]*net.IPNet, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var result []*net.IPNet
	for _, part := range strings.Split(raw, ",") {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		if strings.Contains(value, "/") {
			_, network, err := net.ParseCIDR(value)
			if err != nil {
				return nil, fmt.Errorf("parse trusted proxy CIDR %q: %w", value, err)
			}
			result = append(result, network)
			continue
		}
		ip := net.ParseIP(value)
		if ip == nil {
			return nil, fmt.Errorf("parse trusted proxy IP %q", value)
		}
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		result = append(result, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}
	return result, nil
}
