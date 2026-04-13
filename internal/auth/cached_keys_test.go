package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubKeyStore struct {
	lookupCount int
	touchCount  int
	key         *ProjectKey
	err         error
}

func (s *stubKeyStore) LookupKey(context.Context, string) (*ProjectKey, error) {
	s.lookupCount++
	if s.err != nil {
		return nil, s.err
	}
	if s.key == nil {
		return nil, nil
	}
	cloned := *s.key
	return &cloned, nil
}

func (s *stubKeyStore) TouchProjectKey(context.Context, string) error {
	s.touchCount++
	return nil
}

func TestCachedKeyStoreCachesLookupsWithinTTL(t *testing.T) {
	base := &stubKeyStore{key: &ProjectKey{PublicKey: "pk-1", ProjectID: "proj-1", Status: "active"}}
	cache := NewCachedKeyStore(base, time.Minute, time.Minute)
	now := time.Date(2026, 4, 11, 16, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }

	first, err := cache.LookupKey(context.Background(), "pk-1")
	if err != nil {
		t.Fatalf("LookupKey first: %v", err)
	}
	second, err := cache.LookupKey(context.Background(), "pk-1")
	if err != nil {
		t.Fatalf("LookupKey second: %v", err)
	}
	if first == nil || second == nil || first.ProjectID != "proj-1" || second.ProjectID != "proj-1" {
		t.Fatalf("unexpected lookup results: %+v %+v", first, second)
	}
	if base.lookupCount != 1 {
		t.Fatalf("lookupCount = %d, want 1", base.lookupCount)
	}
}

func TestCachedKeyStoreRefreshesExpiredEntry(t *testing.T) {
	base := &stubKeyStore{key: &ProjectKey{PublicKey: "pk-1", ProjectID: "proj-1", Status: "active"}}
	cache := NewCachedKeyStore(base, time.Second, time.Minute)
	now := time.Date(2026, 4, 11, 16, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }

	if _, err := cache.LookupKey(context.Background(), "pk-1"); err != nil {
		t.Fatalf("LookupKey first: %v", err)
	}
	now = now.Add(2 * time.Second)
	if _, err := cache.LookupKey(context.Background(), "pk-1"); err != nil {
		t.Fatalf("LookupKey second: %v", err)
	}
	if base.lookupCount != 2 {
		t.Fatalf("lookupCount = %d, want 2 after expiry", base.lookupCount)
	}
}

func TestCachedKeyStoreThrottlesTouches(t *testing.T) {
	base := &stubKeyStore{key: &ProjectKey{PublicKey: "pk-1", ProjectID: "proj-1", Status: "active"}}
	cache := NewCachedKeyStore(base, time.Minute, 10*time.Second)
	now := time.Date(2026, 4, 11, 16, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }

	if _, err := cache.LookupKey(context.Background(), "pk-1"); err != nil {
		t.Fatalf("LookupKey: %v", err)
	}
	if err := cache.TouchProjectKey(context.Background(), "pk-1"); err != nil {
		t.Fatalf("TouchProjectKey first: %v", err)
	}
	if err := cache.TouchProjectKey(context.Background(), "pk-1"); err != nil {
		t.Fatalf("TouchProjectKey second: %v", err)
	}
	if base.touchCount != 1 {
		t.Fatalf("touchCount = %d, want 1", base.touchCount)
	}
	now = now.Add(11 * time.Second)
	if err := cache.TouchProjectKey(context.Background(), "pk-1"); err != nil {
		t.Fatalf("TouchProjectKey third: %v", err)
	}
	if base.touchCount != 2 {
		t.Fatalf("touchCount = %d, want 2 after interval", base.touchCount)
	}
}

func TestCachedKeyStoreDoesNotCacheErrors(t *testing.T) {
	base := &stubKeyStore{err: errors.New("boom")}
	cache := NewCachedKeyStore(base, time.Minute, time.Minute)

	if _, err := cache.LookupKey(context.Background(), "pk-1"); err == nil {
		t.Fatal("LookupKey = nil error, want failure")
	}
	if _, err := cache.LookupKey(context.Background(), "pk-1"); err == nil {
		t.Fatal("LookupKey second = nil error, want failure")
	}
	if base.lookupCount != 2 {
		t.Fatalf("lookupCount = %d, want 2 because errors must not be cached", base.lookupCount)
	}
}
