package auth

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type ValkeyRateLimiter struct {
	client *redis.Client
	window time.Duration
	prefix string
	mu     sync.Mutex
	local  map[string]*localReservation
}

type localReservation struct {
	remaining int
}

func NewValkeyRateLimiter(url string, window time.Duration) (*ValkeyRateLimiter, error) {
	if strings.TrimSpace(url) == "" {
		return nil, fmt.Errorf("valkey url is required")
	}
	if window <= 0 {
		window = time.Minute
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse valkey url: %w", err)
	}
	opts.DisableIdentity = true
	return &ValkeyRateLimiter{
		client: redis.NewClient(opts),
		window: window,
		prefix: "urgentry:ratelimit:",
		local:  make(map[string]*localReservation),
	}, nil
}

func (l *ValkeyRateLimiter) Allow(key string, limit int, now time.Time) (time.Duration, bool) {
	if limit <= 0 {
		return 0, true
	}
	if l == nil || l.client == nil {
		return time.Second, false
	}
	windowStart := now.UTC().Truncate(l.window)
	retryAfter := l.window - now.UTC().Sub(windowStart)
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	redisKey := fmt.Sprintf("%s%s:%d", l.prefix, key, windowStart.Unix())
	localKey := fmt.Sprintf("%s:%d", key, windowStart.Unix())
	if l.consumeLocalReservation(localKey) {
		return retryAfter, true
	}
	batch := reservationBatch(limit)
	const script = `
local current = tonumber(redis.call("GET", KEYS[1]) or "0")
local limit = tonumber(ARGV[1])
local ttl = tonumber(ARGV[2])
local want = tonumber(ARGV[3])
if current >= limit then
  return {0, current}
end
local remaining = limit - current
local grant = want
if remaining < want then
  grant = remaining
end
current = redis.call("INCRBY", KEYS[1], grant)
if current == grant then
  redis.call("EXPIRE", KEYS[1], ttl)
end
return {grant, current}
`
	valuesRaw, err := l.client.Eval(context.Background(), script, []string{redisKey}, limit, int(l.window.Seconds())+1, batch).Result()
	if err != nil {
		return retryAfter, false
	}
	values, ok := valuesRaw.([]any)
	if !ok || len(values) == 0 {
		return retryAfter, false
	}
	grant, ok := values[0].(int64)
	if !ok || grant <= 0 {
		return retryAfter, false
	}
	if grant > 1 {
		l.storeLocalReservation(localKey, int(grant)-1)
	}
	return retryAfter, true
}

func (l *ValkeyRateLimiter) Close() error {
	if l == nil || l.client == nil {
		return nil
	}
	return l.client.Close()
}

func (l *ValkeyRateLimiter) consumeLocalReservation(key string) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	reservation := l.local[key]
	if reservation == nil || reservation.remaining <= 0 {
		delete(l.local, key)
		return false
	}
	reservation.remaining--
	if reservation.remaining == 0 {
		delete(l.local, key)
	}
	return true
}

func (l *ValkeyRateLimiter) storeLocalReservation(key string, remaining int) {
	if l == nil || remaining <= 0 {
		return
	}
	l.mu.Lock()
	l.local[key] = &localReservation{remaining: remaining}
	l.mu.Unlock()
}

func reservationBatch(limit int) int {
	if limit < 1000 {
		return 1
	}
	if limit < 10_000 {
		return 8
	}
	return 32
}
