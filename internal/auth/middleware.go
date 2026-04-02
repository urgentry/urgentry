package auth

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"urgentry/internal/httputil"
)

type contextKey int

const projectKeyContextKey contextKey = iota

// Middleware returns HTTP middleware that authenticates ingest requests using
// the X-Sentry-Auth header or the sentry_key query parameter.
func Middleware(store KeyStore, limiter RateLimiter, defaultRateLimit int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			publicKey, err := extractKey(r)
			if err != nil {
				httputil.WriteError(w, http.StatusUnauthorized, "Missing or invalid authentication")
				return
			}

			pk, err := store.LookupKey(r.Context(), publicKey)
			if err != nil {
				httputil.WriteError(w, http.StatusUnauthorized, "Invalid project key")
				return
			}

			if pk.Status != "active" {
				httputil.WriteError(w, http.StatusUnauthorized, "Project key is disabled")
				return
			}

			projectID, err := ExtractProjectID(r.URL.Path)
			if err != nil || projectID == "" || pk.ProjectID != projectID {
				httputil.WriteError(w, http.StatusUnauthorized, "Project key does not match the requested project")
				return
			}

			limit := pk.EffectiveRateLimit(defaultRateLimit)
			if limiter != nil {
				if retryAfter, allowed := limiter.Allow(publicKey, limit, time.Now().UTC()); !allowed {
					retryAfterSeconds := int(retryAfter / time.Second)
					if retryAfter%time.Second != 0 {
						retryAfterSeconds++
					}
					if retryAfterSeconds < 1 {
						retryAfterSeconds = 1
					}
					w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
					w.Header().Set("X-Sentry-Rate-Limits", fmt.Sprintf("%d:error:key::", retryAfterSeconds))
					httputil.WriteError(w, http.StatusTooManyRequests, "Rate limit exceeded")
					return
				}
			}

			if toucher, ok := store.(KeyToucher); ok {
				_ = toucher.TouchProjectKey(r.Context(), publicKey)
			}

			// Set empty rate limits header on successful responses for SDK compat.
			w.Header().Set("X-Sentry-Rate-Limits", "")

			ctx := context.WithValue(r.Context(), projectKeyContextKey, pk)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ProjectKeyFromContext retrieves the authenticated ProjectKey from the context.
// Returns nil if no key is present.
func ProjectKeyFromContext(ctx context.Context) *ProjectKey {
	pk, _ := ctx.Value(projectKeyContextKey).(*ProjectKey)
	return pk
}

// extractKey tries X-Sentry-Auth header first, then sentry_key query param.
func extractKey(r *http.Request) (string, error) {
	if header := r.Header.Get("X-Sentry-Auth"); header != "" {
		return ParseSentryAuth(header)
	}
	if key := r.URL.Query().Get("sentry_key"); key != "" {
		return key, nil
	}
	return "", ErrKeyNotFound
}
