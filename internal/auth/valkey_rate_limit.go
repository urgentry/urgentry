package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type ValkeyRateLimiter struct {
	client *redis.Client
	window time.Duration
	prefix string
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
	const script = `
local current = tonumber(redis.call("GET", KEYS[1]) or "0")
if current >= tonumber(ARGV[1]) then
  return {0, current}
end
current = redis.call("INCR", KEYS[1])
if current == 1 then
  redis.call("EXPIRE", KEYS[1], tonumber(ARGV[2]))
end
if current > tonumber(ARGV[1]) then
  return {0, current}
end
return {1, current}
`
	valuesRaw, err := l.client.Eval(context.Background(), script, []string{redisKey}, limit, int(l.window.Seconds())+1).Result()
	if err != nil {
		return retryAfter, false
	}
	values, ok := valuesRaw.([]any)
	return retryAfter, ok && len(values) > 0 && values[0] == int64(1)
}

func (l *ValkeyRateLimiter) Close() error {
	if l == nil || l.client == nil {
		return nil
	}
	return l.client.Close()
}
