package auth

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func testRedisURL(addr string) string {
	return "redis://" + addr
}

func startRateLimitMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()

	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

func TestValkeyRateLimiterSharesWindowAndFailsClosedOnOutage(t *testing.T) {
	t.Parallel()

	redis := startRateLimitMiniRedis(t)
	limiter, err := NewValkeyRateLimiter(testRedisURL(redis.Addr()), time.Minute)
	if err != nil {
		t.Fatalf("NewValkeyRateLimiter: %v", err)
	}
	t.Cleanup(func() {
		if err := limiter.Close(); err != nil {
			t.Fatalf("Close limiter: %v", err)
		}
	})

	now := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	if retryAfter, allowed := limiter.Allow("project-key", 2, now); !allowed || retryAfter <= 0 {
		t.Fatalf("first Allow = (%v, %v), want allowed with positive retry", retryAfter, allowed)
	}
	if retryAfter, allowed := limiter.Allow("project-key", 2, now.Add(10*time.Second)); !allowed || retryAfter <= 0 {
		t.Fatalf("second Allow = (%v, %v), want allowed with positive retry", retryAfter, allowed)
	}
	if retryAfter, allowed := limiter.Allow("project-key", 2, now.Add(20*time.Second)); allowed || retryAfter <= 0 {
		t.Fatalf("third Allow = (%v, %v), want denied with retry", retryAfter, allowed)
	}

	redis.Close()
	if retryAfter, allowed := limiter.Allow("project-key", 2, now.Add(30*time.Second)); allowed || retryAfter <= 0 {
		t.Fatalf("Allow during outage = (%v, %v), want fail-closed deny", retryAfter, allowed)
	}
}
