package auth

import (
	"context"
	"sync"
)

// MemoryKeyStore is an in-memory KeyStore for testing and development.
type MemoryKeyStore struct {
	mu   sync.RWMutex
	keys map[string]*ProjectKey
}

// NewMemoryKeyStore creates a MemoryKeyStore pre-loaded with the given keys.
func NewMemoryKeyStore(keys ...*ProjectKey) *MemoryKeyStore {
	m := &MemoryKeyStore{keys: make(map[string]*ProjectKey, len(keys))}
	for _, k := range keys {
		m.keys[k.PublicKey] = k
	}
	return m
}

// LookupKey returns the ProjectKey for the given public key, or ErrKeyNotFound.
func (s *MemoryKeyStore) LookupKey(_ context.Context, publicKey string) (*ProjectKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pk, ok := s.keys[publicKey]
	if !ok {
		return nil, ErrKeyNotFound
	}
	return pk, nil
}

// AddKey adds or replaces a key in the store.
func (s *MemoryKeyStore) AddKey(pk *ProjectKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[pk.PublicKey] = pk
}
