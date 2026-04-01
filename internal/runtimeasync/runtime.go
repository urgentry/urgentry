package runtimeasync

import (
	"context"
	"time"
)

// Job is one claimed async work item.
type Job struct {
	ID        string
	Kind      string
	ProjectID string
	Payload   []byte
	Attempts  int
}

// Queue is the runtime async work queue contract shared by Tiny and serious mode.
type Queue interface {
	Enqueue(ctx context.Context, kind, projectID string, payload []byte, limit int) (bool, error)
	ClaimNext(ctx context.Context, workerID string, leaseDuration time.Duration) (*Job, error)
	MarkDone(ctx context.Context, jobID string) error
	Requeue(ctx context.Context, jobID string, delay time.Duration, lastError string) error
	Len(ctx context.Context) (int, error)
	RequeueExpiredProcessing(ctx context.Context) (int64, error)
}

// KeyedEnqueuer publishes work with a stable dedupe key when the backend supports it.
type KeyedEnqueuer interface {
	EnqueueKeyed(ctx context.Context, kind, projectID, dedupeKey string, payload []byte, limit int) (bool, error)
}

// LeaseStore owns singleton runtime leases.
type LeaseStore interface {
	AcquireLease(ctx context.Context, name, holderID string, ttl time.Duration) (bool, error)
}
