package requestmeta

import (
	"net/http"
	"strings"
	"testing"
)

func TestClientIPIgnoresForwardedHeadersWithoutTrustedProxy(t *testing.T) {
	t.Cleanup(func() { _ = ConfigureTrustedProxies("") })
	if err := ConfigureTrustedProxies(""); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.test/", nil)
	req.RemoteAddr = "198.51.100.10:44321"
	req.Header.Set("X-Forwarded-For", "203.0.113.77")
	req.Header.Set("X-Real-IP", "203.0.113.88")

	if got := ClientIP(req); got != "198.51.100.10" {
		t.Fatalf("ClientIP = %q, want remote address", got)
	}
}

func TestClientIPUsesForwardedHeadersFromTrustedProxy(t *testing.T) {
	t.Cleanup(func() { _ = ConfigureTrustedProxies("") })
	if err := ConfigureTrustedProxies("198.51.100.0/24"); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.test/", nil)
	req.RemoteAddr = "198.51.100.10:44321"
	req.Header.Set("X-Forwarded-For", "203.0.113.77, 198.51.100.10")

	if got := ClientIP(req); got != "203.0.113.77" {
		t.Fatalf("ClientIP = %q, want first forwarded address", got)
	}
}

func TestClientIPRejectsInvalidForwardedHeadersFromTrustedProxy(t *testing.T) {
	t.Cleanup(func() { _ = ConfigureTrustedProxies("") })
	if err := ConfigureTrustedProxies("198.51.100.10"); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.test/", nil)
	req.RemoteAddr = "198.51.100.10:44321"
	req.Header.Set("X-Forwarded-For", "0\r0")
	req.Header.Set("X-Real-IP", "not an ip")

	if got := ClientIP(req); got != "198.51.100.10" {
		t.Fatalf("ClientIP = %q, want remote address", got)
	}
}

func TestSchemeAndHostTrustOnlyConfiguredProxy(t *testing.T) {
	t.Cleanup(func() { _ = ConfigureTrustedProxies("") })
	req, _ := http.NewRequest(http.MethodGet, "http://internal.test/", nil)
	req.RemoteAddr = "198.51.100.10:44321"
	req.Host = "internal.test"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "public.test")

	if got := Scheme(req); got != "http" {
		t.Fatalf("Scheme without trusted proxy = %q, want http", got)
	}
	if got := Host(req); got != "internal.test" {
		t.Fatalf("Host without trusted proxy = %q, want internal host", got)
	}

	if err := ConfigureTrustedProxies("198.51.100.10"); err != nil {
		t.Fatal(err)
	}
	if got := Scheme(req); got != "https" {
		t.Fatalf("Scheme with trusted proxy = %q, want https", got)
	}
	if got := Host(req); got != "public.test" {
		t.Fatalf("Host with trusted proxy = %q, want forwarded host", got)
	}
}

func TestConfigureTrustedProxiesRejectsInvalidValues(t *testing.T) {
	if err := ConfigureTrustedProxies("not-an-ip"); err == nil {
		t.Fatal("ConfigureTrustedProxies accepted an invalid proxy value")
	}
}

func FuzzClientIP(f *testing.F) {
	for _, seed := range []string{"203.0.113.1", "203.0.113.1, 198.51.100.10", "", "not an ip"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, forwarded string) {
		t.Cleanup(func() { _ = ConfigureTrustedProxies("") })
		if err := ConfigureTrustedProxies("198.51.100.10"); err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest(http.MethodGet, "http://example.test/", nil)
		req.RemoteAddr = "198.51.100.10:12345"
		req.Header.Set("X-Forwarded-For", forwarded)
		if got := ClientIP(req); strings.Contains(got, "\n") || strings.Contains(got, "\r") {
			t.Fatalf("ClientIP returned control characters: %q", got)
		}
	})
}
