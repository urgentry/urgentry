// Package proguard handles ProGuard/R8 mapping file upload, storage, and lookup.
package proguard

import (
	"context"
	"sync"
	"time"
)

// Mapping represents a ProGuard mapping file.
type Mapping struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	ReleaseID string    `json:"releaseId,omitempty"`
	Name      string    `json:"name"`
	UUID      string    `json:"uuid,omitempty"` // debug_id / ProGuard UUID
	CodeID    string    `json:"codeId,omitempty"`
	ObjectKey string    `json:"-"`
	Size      int64     `json:"size"`
	Checksum  string    `json:"sha1,omitempty"`
	CreatedAt time.Time `json:"dateCreated"`
}

// Store manages ProGuard mapping files.
type Store interface {
	SaveMapping(ctx context.Context, m *Mapping, data []byte) error
	GetMapping(ctx context.Context, id string) (*Mapping, []byte, error)
	LookupByUUID(ctx context.Context, projectID, releaseVersion, uuid string) (*Mapping, []byte, error)
	ListByRelease(ctx context.Context, projectID, releaseVersion string) ([]*Mapping, error)
}

type MemoryStore struct {
	mu       sync.RWMutex
	mappings map[string]*Mapping
	data     map[string][]byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		mappings: make(map[string]*Mapping),
		data:     make(map[string][]byte),
	}
}

func (s *MemoryStore) SaveMapping(_ context.Context, m *Mapping, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mappings[m.ID] = m
	s.data[m.ID] = data
	return nil
}

func (s *MemoryStore) GetMapping(_ context.Context, id string) (*Mapping, []byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.mappings[id]
	if !ok {
		return nil, nil, nil
	}
	return m, s.data[id], nil
}

func (s *MemoryStore) LookupByUUID(_ context.Context, projectID, releaseVersion, uuid string) (*Mapping, []byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.mappings {
		if m.ProjectID == projectID && m.ReleaseID == releaseVersion && m.UUID == uuid {
			return m, s.data[m.ID], nil
		}
	}
	return nil, nil, nil
}

func (s *MemoryStore) ListByRelease(_ context.Context, projectID, releaseVersion string) ([]*Mapping, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Mapping
	for _, m := range s.mappings {
		if m.ProjectID == projectID && m.ReleaseID == releaseVersion {
			result = append(result, m)
		}
	}
	return result, nil
}
