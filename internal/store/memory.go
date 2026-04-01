package store

import (
	"context"
	"errors"
	"sort"
	"sync"
)

var (
	ErrNotFound = errors.New("not found")
)

// MemoryBlobStore is an in-memory BlobStore for testing and development.
type MemoryBlobStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMemoryBlobStore creates a new in-memory blob store.
func NewMemoryBlobStore() *MemoryBlobStore {
	return &MemoryBlobStore{data: make(map[string][]byte)}
}

func (m *MemoryBlobStore) Put(_ context.Context, key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.data[key] = cp
	return nil
}

func (m *MemoryBlobStore) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.data[key]
	if !ok {
		return nil, ErrNotFound
	}
	cp := make([]byte, len(d))
	copy(cp, d)
	return cp, nil
}

func (m *MemoryBlobStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.data[key]; !ok {
		return ErrNotFound
	}
	delete(m.data, key)
	return nil
}

// MemoryEventStore is an in-memory EventStore for testing and development.
type MemoryEventStore struct {
	mu     sync.RWMutex
	events []*StoredEvent
	index  map[string]int // "projectID:eventID" -> slice index
}

// NewMemoryEventStore creates a new in-memory event store.
func NewMemoryEventStore() *MemoryEventStore {
	return &MemoryEventStore{
		index: make(map[string]int),
	}
}

func eventKey(projectID, eventID string) string {
	return projectID + ":" + eventID
}

func (m *MemoryEventStore) SaveEvent(_ context.Context, evt *StoredEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := eventKey(evt.ProjectID, evt.EventID)
	if _, exists := m.index[key]; exists {
		return errors.New("duplicate event")
	}

	cp := *evt
	m.index[key] = len(m.events)
	m.events = append(m.events, &cp)
	return nil
}

func (m *MemoryEventStore) GetEvent(_ context.Context, projectID, eventID string) (*StoredEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := eventKey(projectID, eventID)
	idx, ok := m.index[key]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *m.events[idx]
	return &cp, nil
}

func (m *MemoryEventStore) ListEvents(_ context.Context, projectID string, opts ListOpts) ([]*StoredEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Filter by project
	var filtered []*StoredEvent
	for _, e := range m.events {
		if e.ProjectID == projectID {
			filtered = append(filtered, e)
		}
	}

	// Sort
	switch opts.Sort {
	case "occurred_at_asc":
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].OccurredAt.Before(filtered[j].OccurredAt)
		})
	default: // "occurred_at_desc" or empty
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].OccurredAt.After(filtered[j].OccurredAt)
		})
	}

	// Apply cursor (cursor is an event ID; skip everything up to and including it)
	if opts.Cursor != "" {
		cursorIdx := -1
		for i, e := range filtered {
			if e.EventID == opts.Cursor {
				cursorIdx = i
				break
			}
		}
		if cursorIdx >= 0 && cursorIdx+1 < len(filtered) {
			filtered = filtered[cursorIdx+1:]
		} else if cursorIdx >= 0 {
			filtered = nil
		}
	}

	// Apply limit
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	// Return copies
	result := make([]*StoredEvent, len(filtered))
	for i, e := range filtered {
		cp := *e
		result[i] = &cp
	}
	return result, nil
}

// MemoryTraceStore is an in-memory TraceStore for tests.
type MemoryTraceStore struct {
	mu           sync.RWMutex
	transactions []*StoredTransaction
	index        map[string]int
}

// NewMemoryTraceStore creates a new in-memory trace store.
func NewMemoryTraceStore() *MemoryTraceStore {
	return &MemoryTraceStore{
		index: make(map[string]int),
	}
}

func (m *MemoryTraceStore) SaveTransaction(_ context.Context, txn *StoredTransaction) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := eventKey(txn.ProjectID, txn.EventID)
	cp := *txn
	cp.Spans = append([]StoredSpan(nil), txn.Spans...)
	if idx, exists := m.index[key]; exists {
		m.transactions[idx] = &cp
		return nil
	}
	m.index[key] = len(m.transactions)
	m.transactions = append(m.transactions, &cp)
	return nil
}

func (m *MemoryTraceStore) GetTransaction(_ context.Context, projectID, eventID string) (*StoredTransaction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	idx, ok := m.index[eventKey(projectID, eventID)]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *m.transactions[idx]
	cp.Spans = append([]StoredSpan(nil), m.transactions[idx].Spans...)
	return &cp, nil
}

func (m *MemoryTraceStore) ListTransactions(_ context.Context, projectID string, limit int) ([]*StoredTransaction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit <= 0 {
		limit = 100
	}
	var filtered []*StoredTransaction
	for _, txn := range m.transactions {
		if txn.ProjectID == projectID {
			cp := *txn
			cp.Spans = append([]StoredSpan(nil), txn.Spans...)
			filtered = append(filtered, &cp)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].EndTimestamp.After(filtered[j].EndTimestamp)
	})
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, nil
}

func (m *MemoryTraceStore) ListTransactionsByTrace(_ context.Context, projectID, traceID string) ([]*StoredTransaction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var filtered []*StoredTransaction
	for _, txn := range m.transactions {
		if txn.ProjectID == projectID && txn.TraceID == traceID {
			cp := *txn
			cp.Spans = append([]StoredSpan(nil), txn.Spans...)
			filtered = append(filtered, &cp)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].StartTimestamp.Before(filtered[j].StartTimestamp)
	})
	return filtered, nil
}

func (m *MemoryTraceStore) ListTraceSpans(_ context.Context, projectID, traceID string) ([]StoredSpan, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var items []StoredSpan
	for _, txn := range m.transactions {
		if txn.ProjectID == projectID && txn.TraceID == traceID {
			items = append(items, txn.Spans...)
		}
	}
	cp := append([]StoredSpan(nil), items...)
	sort.Slice(cp, func(i, j int) bool {
		return cp[i].StartTimestamp.Before(cp[j].StartTimestamp)
	})
	return cp, nil
}
