package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"urgentry/internal/runtimeasync"
)

type queueStub struct {
	enqueued []string
}

func (q *queueStub) Enqueue(_ context.Context, kind, projectID string, _ []byte, _ int) (bool, error) {
	q.enqueued = append(q.enqueued, kind+":"+projectID)
	return true, nil
}

func (q *queueStub) ClaimNext(context.Context, string, time.Duration) (*runtimeasync.Job, error) {
	return nil, nil
}

func (q *queueStub) MarkDone(context.Context, string) error                       { return nil }
func (q *queueStub) Requeue(context.Context, string, time.Duration, string) error { return nil }
func (q *queueStub) Len(context.Context) (int, error)                             { return 0, nil }
func (q *queueStub) RequeueExpiredProcessing(context.Context) (int64, error)      { return 0, nil }

func TestAppendOnlyEventQueueEventLifecycle(t *testing.T) {
	db, err := OpenQueue(t.TempDir())
	if err != nil {
		t.Fatalf("OpenQueue: %v", err)
	}
	defer db.Close()

	queue := NewAppendOnlyEventQueue(db, &queueStub{})
	ctx := context.Background()

	ok, err := queue.Enqueue(ctx, JobKindEvent, "proj-1", []byte(`{"event_id":"evt-1"}`), 10)
	if err != nil || !ok {
		t.Fatalf("Enqueue = (%v, %v), want (true, nil)", ok, err)
	}
	job, err := queue.ClaimNext(ctx, "worker-1", time.Minute)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if job == nil || job.Kind != JobKindEvent || job.ProjectID != "proj-1" {
		t.Fatalf("unexpected job: %+v", job)
	}
	if err := queue.Requeue(ctx, job.ID, 10*time.Millisecond, "retry"); err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	job, err = queue.ClaimNext(ctx, "worker-1", time.Minute)
	if err != nil {
		t.Fatalf("ClaimNext retry: %v", err)
	}
	if job == nil || job.Attempts < 2 {
		t.Fatalf("retry job = %+v, want attempts >= 2", job)
	}
	if err := queue.MarkDone(ctx, job.ID); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	job, err = queue.ClaimNext(ctx, "worker-1", time.Minute)
	if err != nil {
		t.Fatalf("ClaimNext after done: %v", err)
	}
	if job != nil {
		t.Fatalf("expected no job after checkpoint, got %+v", job)
	}
}

func TestAppendOnlyEventQueueDelegatesNonEventJobs(t *testing.T) {
	db, err := OpenQueue(t.TempDir())
	if err != nil {
		t.Fatalf("OpenQueue: %v", err)
	}
	defer db.Close()

	stub := &queueStub{}
	queue := NewAppendOnlyEventQueue(db, stub)
	ok, err := queue.Enqueue(context.Background(), JobKindNativeStackwalk, "proj-1", []byte(`{}`), 10)
	if err != nil || !ok {
		t.Fatalf("Enqueue = (%v, %v), want (true, nil)", ok, err)
	}
	if len(stub.enqueued) != 1 || stub.enqueued[0] != JobKindNativeStackwalk+":proj-1" {
		t.Fatalf("delegated enqueue = %+v", stub.enqueued)
	}
}

func TestAppendOnlyEventQueueLenAndCheckpointResume(t *testing.T) {
	db, err := OpenQueue(t.TempDir())
	if err != nil {
		t.Fatalf("OpenQueue: %v", err)
	}
	defer db.Close()

	queue := NewAppendOnlyEventQueue(db, &queueStub{})
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		ok, err := queue.Enqueue(ctx, JobKindEvent, "proj-1", []byte(`{"event_id":"evt-1"}`), 10)
		if err != nil || !ok {
			t.Fatalf("Enqueue #%d = (%v, %v), want (true, nil)", i, ok, err)
		}
	}
	if pending, err := queue.Len(ctx); err != nil {
		t.Fatalf("Len: %v", err)
	} else if pending != 2 {
		t.Fatalf("Len = %d, want 2", pending)
	}

	job, err := queue.ClaimNext(ctx, "worker-1", time.Minute)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if job == nil {
		t.Fatal("ClaimNext = nil, want job")
	}
	if err := queue.MarkDone(ctx, job.ID); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	reopened := NewAppendOnlyEventQueue(db, &queueStub{})
	if pending, err := reopened.Len(ctx); err != nil {
		t.Fatalf("Len after checkpoint: %v", err)
	} else if pending != 1 {
		t.Fatalf("Len after checkpoint = %d, want 1", pending)
	}
	job, err = reopened.ClaimNext(ctx, "worker-2", time.Minute)
	if err != nil {
		t.Fatalf("ClaimNext after checkpoint: %v", err)
	}
	if job == nil || job.ID == "" {
		t.Fatalf("ClaimNext after checkpoint = %+v, want remaining job", job)
	}
}

func TestAppendOnlyEventQueueRespectsBacklogLimit(t *testing.T) {
	db, err := OpenQueue(t.TempDir())
	if err != nil {
		t.Fatalf("OpenQueue: %v", err)
	}
	defer db.Close()

	queue := NewAppendOnlyEventQueue(db, &queueStub{})
	ctx := context.Background()

	ok, err := queue.Enqueue(ctx, JobKindEvent, "proj-1", []byte(`{"event_id":"evt-1"}`), 1)
	if err != nil || !ok {
		t.Fatalf("first Enqueue = (%v, %v), want (true, nil)", ok, err)
	}
	ok, err = queue.Enqueue(ctx, JobKindEvent, "proj-1", []byte(`{"event_id":"evt-2"}`), 1)
	if err != nil {
		t.Fatalf("second Enqueue err = %v, want nil", err)
	}
	if ok {
		t.Fatal("second Enqueue = true, want false because backlog limit was reached")
	}
}

func TestAppendOnlyEventQueueTrimsCheckpointedRows(t *testing.T) {
	db, err := OpenQueue(t.TempDir())
	if err != nil {
		t.Fatalf("OpenQueue: %v", err)
	}
	defer db.Close()

	queue := NewAppendOnlyEventQueue(db, &queueStub{})
	ctx := context.Background()

	for i := int64(0); i < appendOnlyTrimInterval; i++ {
		ok, err := queue.Enqueue(ctx, JobKindEvent, "proj-1", []byte(`{"event_id":"evt-1"}`), int(appendOnlyTrimInterval)+1)
		if err != nil || !ok {
			t.Fatalf("Enqueue #%d = (%v, %v), want (true, nil)", i, ok, err)
		}
		job, err := queue.ClaimNext(ctx, "worker-1", time.Minute)
		if err != nil {
			t.Fatalf("ClaimNext #%d: %v", i, err)
		}
		if job == nil {
			t.Fatalf("ClaimNext #%d = nil, want job", i)
		}
		if err := queue.MarkDone(ctx, job.ID); err != nil {
			t.Fatalf("MarkDone #%d: %v", i, err)
		}
	}

	if rows := countRows(t, db, "SELECT COUNT(*) FROM ingest_log"); rows != 0 {
		t.Fatalf("ingest_log rows = %d, want 0 after trim", rows)
	}
}

func countRows(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}
