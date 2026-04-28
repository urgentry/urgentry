package api

import (
	"net/http"
	"testing"

	"urgentry/internal/requestmeta"
)

func TestBaseURLFromRequestIgnoresUntrustedForwardedHeaders(t *testing.T) {
	t.Cleanup(func() { _ = requestmeta.ConfigureTrustedProxies("") })
	if err := requestmeta.ConfigureTrustedProxies(""); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://internal.test/api/0/projects/org/proj/keys/", nil)
	req.RemoteAddr = "198.51.100.10:4567"
	req.Host = "internal.test"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "public.test")

	if got := baseURLFromRequest(req); got != "http://internal.test" {
		t.Fatalf("baseURLFromRequest = %q", got)
	}
}

func TestBaseURLFromRequestUsesTrustedForwardedHeaders(t *testing.T) {
	t.Cleanup(func() { _ = requestmeta.ConfigureTrustedProxies("") })
	if err := requestmeta.ConfigureTrustedProxies("198.51.100.10"); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://internal.test/api/0/projects/org/proj/keys/", nil)
	req.RemoteAddr = "198.51.100.10:4567"
	req.Host = "internal.test"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "public.test")

	if got := baseURLFromRequest(req); got != "https://public.test" {
		t.Fatalf("baseURLFromRequest = %q", got)
	}
}
