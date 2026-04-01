// Package store defines storage interfaces for events and raw payloads.
package store

import (
	"context"
	"encoding/json"
	"time"
)

// BlobStore persists raw payloads to object storage.
type BlobStore interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}

// EventStore persists normalized events.
type EventStore interface {
	SaveEvent(ctx context.Context, evt *StoredEvent) error
	GetEvent(ctx context.Context, projectID, eventID string) (*StoredEvent, error)
	ListEvents(ctx context.Context, projectID string, opts ListOpts) ([]*StoredEvent, error)
}

// TraceStore persists transactions and their spans.
type TraceStore interface {
	SaveTransaction(ctx context.Context, txn *StoredTransaction) error
	GetTransaction(ctx context.Context, projectID, eventID string) (*StoredTransaction, error)
	ListTransactions(ctx context.Context, projectID string, limit int) ([]*StoredTransaction, error)
	ListTransactionsByTrace(ctx context.Context, projectID, traceID string) ([]*StoredTransaction, error)
	ListTraceSpans(ctx context.Context, projectID, traceID string) ([]StoredSpan, error)
}

// StoredEvent is the persistence representation of a normalized event.
type StoredEvent struct {
	ID               string
	ProjectID        string
	EventID          string
	GroupID          string
	ReleaseID        string
	Environment      string
	Platform         string
	Level            string
	EventType        string
	OccurredAt       time.Time
	IngestedAt       time.Time
	Message          string
	Title            string
	Culprit          string
	Fingerprint      []string
	Tags             map[string]string
	NormalizedJSON   json.RawMessage
	PayloadKey       string // blob store reference
	UserIdentifier   string // extracted user ID/email/IP for unique user counts
	ProcessingStatus EventProcessingStatus
	IngestError      string
}

// StoredMeasurement captures one performance measurement on a transaction.
type StoredMeasurement struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit,omitempty"`
}

// StoredSpan is a persisted trace span row.
type StoredSpan struct {
	ID                 string
	ProjectID          string
	TransactionEventID string
	TraceID            string
	SpanID             string
	ParentSpanID       string
	Op                 string
	Description        string
	Status             string
	StartTimestamp     time.Time
	EndTimestamp       time.Time
	DurationMS         float64
	Tags               map[string]string
	Data               map[string]any
}

// StoredTransaction is a persisted performance transaction with child spans.
type StoredTransaction struct {
	ID             string
	ProjectID      string
	EventID        string
	TraceID        string
	SpanID         string
	ParentSpanID   string
	Transaction    string
	Op             string
	Status         string
	Platform       string
	Environment    string
	ReleaseID      string
	StartTimestamp time.Time
	EndTimestamp   time.Time
	DurationMS     float64
	Tags           map[string]string
	Measurements   map[string]StoredMeasurement
	NormalizedJSON json.RawMessage
	PayloadKey     string
	Spans          []StoredSpan
}

// ListOpts controls pagination and sorting for list queries.
type ListOpts struct {
	Limit  int
	Cursor string
	Sort   string // "occurred_at_asc", "occurred_at_desc" (default)
}

// ForwardingConfig is a per-project rule for forwarding processed events to
// an external system (e.g. a webhook URL).
type ForwardingConfig struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	Type      string    `json:"type"` // "webhook"
	URL       string    `json:"url"`
	Status    string    `json:"status"` // "active", "disabled"
	CreatedAt time.Time `json:"createdAt"`
}

// ForwardingStore abstracts persistence of data forwarding configurations.
type ForwardingStore interface {
	CreateForwarding(ctx context.Context, cfg *ForwardingConfig) error
	ListForwardingByProject(ctx context.Context, projectID string) ([]*ForwardingConfig, error)
	DeleteForwarding(ctx context.Context, id string) error
}
