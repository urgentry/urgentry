package auth

import (
	"sync"
	"time"
)

// RateLimiter tracks request counts per key over a fixed window.
type RateLimiter interface {
	Allow(key string, limit int, now time.Time) (retryAfter time.Duration, allowed bool)
}

// FixedWindowRateLimiter enforces a simple per-key request cap over a fixed window.
type FixedWindowRateLimiter struct {
	window  time.Duration
	mu      sync.Mutex
	buckets map[string]*rateBucket
}

type rateBucket struct {
	windowStart time.Time
	count       int
}

// NewFixedWindowRateLimiter creates a limiter with the provided window duration.
func NewFixedWindowRateLimiter(window time.Duration) *FixedWindowRateLimiter {
	if window <= 0 {
		window = time.Minute
	}
	return &FixedWindowRateLimiter{
		window:  window,
		buckets: make(map[string]*rateBucket),
	}
}

// Allow consumes one request for the given key if it remains under the limit.
func (l *FixedWindowRateLimiter) Allow(key string, limit int, now time.Time) (time.Duration, bool) {
	if limit <= 0 {
		return 0, true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	bucket := l.buckets[key]
	if bucket == nil || now.Sub(bucket.windowStart) >= l.window {
		l.buckets[key] = &rateBucket{windowStart: now, count: 1}
		return 0, true
	}

	if bucket.count >= limit {
		retryAfter := l.window - now.Sub(bucket.windowStart)
		if retryAfter < 0 {
			retryAfter = 0
		}
		return retryAfter, false
	}

	bucket.count++
	return 0, true
}
