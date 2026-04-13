// Package attachment handles file attachments linked to events.
package attachment

import (
	"context"
	"sync"
	"time"
)

type Attachment struct {
	ID          string    `json:"id"`
	EventID     string    `json:"eventId"`
	ProjectID   string    `json:"projectId"`
	Name        string    `json:"name"`
	ContentType string    `json:"contentType,omitempty"`
	Size        int64     `json:"size"`
	ObjectKey   string    `json:"-"` // blob store key
	CreatedAt   time.Time `json:"dateCreated"`
}

type Store interface {
	SaveAttachment(ctx context.Context, a *Attachment, data []byte) error
	GetAttachment(ctx context.Context, id string) (*Attachment, []byte, error)
	ListByEvent(ctx context.Context, eventID string) ([]*Attachment, error)
}

type MemoryStore struct {
	mu          sync.RWMutex
	attachments map[string]*Attachment
	data        map[string][]byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		attachments: make(map[string]*Attachment),
		data:        make(map[string][]byte),
	}
}

func (s *MemoryStore) SaveAttachment(_ context.Context, a *Attachment, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attachments[a.ID] = a
	s.data[a.ID] = data
	return nil
}

func (s *MemoryStore) GetAttachment(_ context.Context, id string) (*Attachment, []byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.attachments[id]
	if !ok {
		return nil, nil, nil
	}
	return a, s.data[id], nil
}

func (s *MemoryStore) ListByEvent(_ context.Context, eventID string) ([]*Attachment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Attachment
	for _, a := range s.attachments {
		if a.EventID == eventID {
			result = append(result, a)
		}
	}
	return result, nil
}
