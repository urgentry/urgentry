package pipeline

import (
	"context"
	"database/sql"
	"io"
	"testing"
	"time"

	"urgentry/internal/runtimeasync"
	"urgentry/internal/sqlite"

	"github.com/rs/zerolog/log"
)

func benchmarkItem() Item {
	return Item{
		ProjectID: "bench-project",
		RawEvent:  []byte(`{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","message":"benchmark"}`),
	}
}

func benchmarkOpenDurableStore(b *testing.B) *sql.DB {
	b.Helper()

	db, err := sqlite.Open(b.TempDir())
	if err != nil {
		b.Fatalf("sqlite.Open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('bench-org', 'bench-org', 'Benchmark Org')`); err != nil {
		b.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('bench-project', 'bench-org', 'bench-project', 'Benchmark Project', 'go', 'active')`); err != nil {
		b.Fatalf("seed project: %v", err)
	}
	return db
}

func setBenchmarkQueueTimings(tb testing.TB, idle, wait, retry time.Duration) {
	tb.Helper()

	prevIdle := idlePollInterval
	prevWait := maxEnqueueWait
	prevRetry := enqueueRetryInterval
	idlePollInterval = idle
	maxEnqueueWait = wait
	enqueueRetryInterval = retry
	tb.Cleanup(func() {
		idlePollInterval = prevIdle
		maxEnqueueWait = prevWait
		enqueueRetryInterval = prevRetry
	})
}

func silencePipelineLogs(tb testing.TB) {
	tb.Helper()

	prev := log.Logger
	log.Logger = log.Output(io.Discard)
	tb.Cleanup(func() {
		log.Logger = prev
	})
}

type benchmarkQueue struct {
	enqueue      func(context.Context, string, string, []byte, int) (bool, error)
	claimNext    func(context.Context, string, time.Duration) (*runtimeasync.Job, error)
	markDone     func(context.Context, string) error
	requeue      func(context.Context, string, time.Duration, string) error
	len          func(context.Context) (int, error)
	requeueAging func(context.Context) (int64, error)
}

func (q *benchmarkQueue) Enqueue(ctx context.Context, kind, projectID string, payload []byte, limit int) (bool, error) {
	if q.enqueue != nil {
		return q.enqueue(ctx, kind, projectID, payload, limit)
	}
	return true, nil
}

func (q *benchmarkQueue) ClaimNext(ctx context.Context, workerID string, leaseDuration time.Duration) (*runtimeasync.Job, error) {
	if q.claimNext != nil {
		return q.claimNext(ctx, workerID, leaseDuration)
	}
	return nil, nil
}

func (q *benchmarkQueue) MarkDone(ctx context.Context, jobID string) error {
	if q.markDone != nil {
		return q.markDone(ctx, jobID)
	}
	return nil
}

func (q *benchmarkQueue) Requeue(ctx context.Context, jobID string, delay time.Duration, lastError string) error {
	if q.requeue != nil {
		return q.requeue(ctx, jobID, delay, lastError)
	}
	return nil
}

func (q *benchmarkQueue) Len(ctx context.Context) (int, error) {
	if q.len != nil {
		return q.len(ctx)
	}
	return 0, nil
}

func (q *benchmarkQueue) RequeueExpiredProcessing(ctx context.Context) (int64, error) {
	if q.requeueAging != nil {
		return q.requeueAging(ctx)
	}
	return 0, nil
}

var _ runtimeasync.Queue = (*benchmarkQueue)(nil)
