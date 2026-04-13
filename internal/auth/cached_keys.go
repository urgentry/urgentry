package auth

import (
	"context"
	"sync"
	"time"
)

const (
	defaultProjectKeyCacheTTL      = 5 * time.Second
	defaultProjectKeyTouchInterval = 30 * time.Second
)

type cachedProjectKey struct {
	key           ProjectKey
	expiresAt     time.Time
	lastTouchedAt time.Time
}

// CachedKeyStore wraps a project-key store with a short-lived in-memory cache
// and a throttled last-used write path. This is intended for serious
// self-hosted ingest where the same key is often hit at high rate.
type CachedKeyStore struct {
	base          KeyStore
	toucher       KeyToucher
	ttl           time.Duration
	touchInterval time.Duration
	now           func() time.Time

	mu      sync.RWMutex
	entries map[string]cachedProjectKey
}

var _ KeyStore = (*CachedKeyStore)(nil)
var _ KeyToucher = (*CachedKeyStore)(nil)

func NewCachedKeyStore(base KeyStore, ttl, touchInterval time.Duration) *CachedKeyStore {
	if ttl <= 0 {
		ttl = defaultProjectKeyCacheTTL
	}
	if touchInterval <= 0 {
		touchInterval = defaultProjectKeyTouchInterval
	}
	store := &CachedKeyStore{
		base:          base,
		ttl:           ttl,
		touchInterval: touchInterval,
		now:           func() time.Time { return time.Now().UTC() },
		entries:       map[string]cachedProjectKey{},
	}
	if toucher, ok := base.(KeyToucher); ok {
		store.toucher = toucher
	}
	return store
}

func (s *CachedKeyStore) LookupKey(ctx context.Context, publicKey string) (*ProjectKey, error) {
	now := s.now()
	s.mu.RLock()
	entry, ok := s.entries[publicKey]
	s.mu.RUnlock()
	if ok && now.Before(entry.expiresAt) {
		key := entry.key
		return &key, nil
	}

	key, err := s.base.LookupKey(ctx, publicKey)
	if err != nil {
		return nil, err
	}
	if key == nil {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.entries[publicKey]
	s.entries[publicKey] = cachedProjectKey{
		key:           *key,
		expiresAt:     now.Add(s.ttl),
		lastTouchedAt: prev.lastTouchedAt,
	}
	cloned := *key
	return &cloned, nil
}

func (s *CachedKeyStore) TouchProjectKey(ctx context.Context, publicKey string) error {
	if s.toucher == nil {
		return nil
	}
	now := s.now()
	s.mu.Lock()
	entry := s.entries[publicKey]
	if !entry.lastTouchedAt.IsZero() && now.Sub(entry.lastTouchedAt) < s.touchInterval {
		s.mu.Unlock()
		return nil
	}
	entry.lastTouchedAt = now
	if entry.key.PublicKey != "" {
		entry.expiresAt = now.Add(s.ttl)
	}
	s.entries[publicKey] = entry
	s.mu.Unlock()
	return s.toucher.TouchProjectKey(ctx, publicKey)
}
