package auth

import (
	"context"
	"fmt"
	"strings"
)

// KeyStore is the interface for looking up project keys.
type KeyStore interface {
	LookupKey(ctx context.Context, publicKey string) (*ProjectKey, error)
}

// KeyToucher optionally records project-key usage.
type KeyToucher interface {
	TouchProjectKey(ctx context.Context, publicKey string) error
}

// ProjectKey represents a project's authentication key.
type ProjectKey struct {
	PublicKey string
	ProjectID string
	Status    string // "active", "disabled"
	RateLimit int
}

// EffectiveRateLimit returns the per-key limit if set, otherwise the provided default.
func (k *ProjectKey) EffectiveRateLimit(defaultLimit int) int {
	if k == nil {
		return defaultLimit
	}
	if k.RateLimit > 0 {
		return k.RateLimit
	}
	return defaultLimit
}

// ErrKeyNotFound is returned when a public key is not in the store.
var ErrKeyNotFound = fmt.Errorf("project key not found")

// ParseSentryAuth extracts the sentry_key from an X-Sentry-Auth header value.
// Format: Sentry sentry_key=<public_key>,sentry_version=7,sentry_client=<sdk/version>
func ParseSentryAuth(header string) (string, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", fmt.Errorf("empty auth header")
	}

	// Must start with "Sentry "
	if !strings.HasPrefix(header, "Sentry ") {
		return "", fmt.Errorf("auth header must start with 'Sentry '")
	}

	pairs := header[len("Sentry "):]
	for _, part := range strings.Split(pairs, ",") {
		part = strings.TrimSpace(part)
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(k) == "sentry_key" {
			v = strings.TrimSpace(v)
			if v == "" {
				return "", fmt.Errorf("sentry_key is empty")
			}
			return v, nil
		}
	}

	return "", fmt.Errorf("sentry_key not found in auth header")
}

// ExtractProjectID extracts the project ID from a Sentry-style API path.
// Expected paths: /api/{project_id}/store/ or /api/{project_id}/envelope/
func ExtractProjectID(path string) (string, error) {
	path = strings.TrimPrefix(path, "/")
	parts := strings.Split(path, "/")
	// Expect: api / {project_id} / store|envelope /
	if len(parts) < 3 || parts[0] != "api" {
		return "", fmt.Errorf("path does not match /api/{project_id}/")
	}
	id := parts[1]
	if id == "" {
		return "", fmt.Errorf("empty project ID in path")
	}
	return id, nil
}
