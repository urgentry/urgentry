// Package sourcemap handles JavaScript source map upload, storage, and
// resolution, including the test-only in-memory implementation used by issue
// processing and compatibility tests.
package sourcemap

import (
	"context"
	"sync"
	"time"
)

// Artifact represents a source map file associated with a release.
type Artifact struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	ReleaseID string    `json:"releaseId,omitempty"`
	Name      string    `json:"name"`
	Dist      string    `json:"dist,omitempty"`
	ObjectKey string    `json:"-"`
	Size      int64     `json:"size"`
	Checksum  string    `json:"sha1,omitempty"`
	CreatedAt time.Time `json:"dateCreated"`
}

// Store manages source map artifacts.
type Store interface {
	SaveArtifact(ctx context.Context, a *Artifact, data []byte) error
	GetArtifact(ctx context.Context, id string) (*Artifact, []byte, error)
	ListByRelease(ctx context.Context, projectID, releaseVersion string) ([]*Artifact, error)
	DeleteArtifact(ctx context.Context, id string) error
	LookupByName(ctx context.Context, projectID, releaseVersion, name string) (*Artifact, []byte, error)
}

type MemoryStore struct {
	mu        sync.RWMutex
	artifacts map[string]*Artifact
	data      map[string][]byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		artifacts: make(map[string]*Artifact),
		data:      make(map[string][]byte),
	}
}

func (s *MemoryStore) SaveArtifact(_ context.Context, a *Artifact, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.artifacts[a.ID] = a
	s.data[a.ID] = data
	return nil
}

func (s *MemoryStore) GetArtifact(_ context.Context, id string) (*Artifact, []byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.artifacts[id]
	if !ok {
		return nil, nil, nil
	}
	return a, s.data[id], nil
}

func (s *MemoryStore) ListByRelease(_ context.Context, projectID, releaseVersion string) ([]*Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Artifact
	for _, a := range s.artifacts {
		if a.ProjectID == projectID && a.ReleaseID == releaseVersion {
			result = append(result, a)
		}
	}
	return result, nil
}

func (s *MemoryStore) DeleteArtifact(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.artifacts, id)
	delete(s.data, id)
	return nil
}

func (s *MemoryStore) LookupByName(_ context.Context, projectID, releaseVersion, name string) (*Artifact, []byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.artifacts {
		if a.ProjectID == projectID && a.ReleaseID == releaseVersion && a.Name == name {
			return a, s.data[a.ID], nil
		}
	}
	return nil, nil, nil
}
