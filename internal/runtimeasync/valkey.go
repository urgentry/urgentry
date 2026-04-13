package runtimeasync

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type ValkeyLeaseStore struct {
	client *redis.Client
	prefix string
}

func NewValkeyLeaseStore(url string) (*ValkeyLeaseStore, error) {
	if strings.TrimSpace(url) == "" {
		return nil, fmt.Errorf("valkey url is required")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse valkey url: %w", err)
	}
	opts.DisableIdentity = true
	return &ValkeyLeaseStore{
		client: redis.NewClient(opts),
		prefix: "urgentry:lease:",
	}, nil
}

func (s *ValkeyLeaseStore) AcquireLease(ctx context.Context, name, holderID string, ttl time.Duration) (bool, error) {
	if s == nil || s.client == nil {
		return false, fmt.Errorf("valkey lease store is not configured")
	}
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	key := s.prefix + strings.TrimSpace(name)
	current, err := s.client.Get(ctx, key).Result()
	if err == redis.Nil {
		status, err := s.client.SetArgs(ctx, key, holderID, redis.SetArgs{
			TTL:  ttl,
			Mode: "NX",
		}).Result()
		if err == redis.Nil {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("acquire valkey lease: %w", err)
		}
		return status == "OK", nil
	}
	if err != nil {
		return false, fmt.Errorf("get valkey lease: %w", err)
	}
	if current != holderID {
		return false, nil
	}
	if err := s.client.Expire(ctx, key, ttl).Err(); err != nil {
		return false, fmt.Errorf("refresh valkey lease: %w", err)
	}
	return true, nil
}

func (s *ValkeyLeaseStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}
