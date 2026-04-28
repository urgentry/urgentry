package runtimeasync

import (
	"context"
	"testing"
	"time"
)

func TestJetStreamQueueRequeuesAndAcknowledges(t *testing.T) {
	srv := startJetStreamTestServer(t)
	queue, err := NewJetStreamQueue(srv.ClientURL(), "worker-a", sqliteEventKind)
	if err != nil {
		t.Fatalf("NewJetStreamQueue: %v", err)
	}
	t.Cleanup(func() {
		if err := queue.Close(); err != nil {
			t.Fatalf("Close queue: %v", err)
		}
	})

	ctx := context.Background()
	if ok, err := queue.Enqueue(ctx, sqliteEventKind, "proj-1", []byte(`{"event_id":"evt-1"}`), 1); err != nil || !ok {
		t.Fatalf("Enqueue() = (%v, %v), want (true, nil)", ok, err)
	}

	job, err := queue.ClaimNext(ctx, "worker-1", time.Second)
	if err != nil {
		t.Fatalf("ClaimNext first: %v", err)
	}
	if job == nil || job.Kind != sqliteEventKind || job.ProjectID != "proj-1" || string(job.Payload) != `{"event_id":"evt-1"}` {
		t.Fatalf("unexpected first job: %+v", job)
	}
	if job.Attempts != 1 {
		t.Fatalf("Attempts = %d, want 1", job.Attempts)
	}

	if err := queue.Requeue(ctx, job.ID, 10*time.Millisecond, "retry"); err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	time.Sleep(25 * time.Millisecond)

	job, err = queue.ClaimNext(ctx, "worker-2", time.Second)
	if err != nil {
		t.Fatalf("ClaimNext second: %v", err)
	}
	if job == nil {
		t.Fatal("expected requeued job")
	}
	if job.Attempts < 2 {
		t.Fatalf("Attempts = %d, want >= 2", job.Attempts)
	}
	if err := queue.MarkDone(ctx, job.ID); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
}

func TestJetStreamQueueSharesOneConsumerAcrossWorkers(t *testing.T) {
	srv := startJetStreamTestServer(t)
	queueA, err := NewJetStreamQueue(srv.ClientURL(), "worker-a", sqliteEventKind)
	if err != nil {
		t.Fatalf("NewJetStreamQueue queueA: %v", err)
	}
	t.Cleanup(func() {
		if err := queueA.Close(); err != nil {
			t.Fatalf("Close queueA: %v", err)
		}
	})
	queueB, err := NewJetStreamQueue(srv.ClientURL(), "worker-b", sqliteEventKind)
	if err != nil {
		t.Fatalf("NewJetStreamQueue queueB: %v", err)
	}
	t.Cleanup(func() {
		if err := queueB.Close(); err != nil {
			t.Fatalf("Close queueB: %v", err)
		}
	})

	ctx := context.Background()
	if ok, err := queueA.Enqueue(ctx, sqliteEventKind, "proj-1", []byte("first"), 1); err != nil || !ok {
		t.Fatalf("Enqueue first = (%v, %v), want (true, nil)", ok, err)
	}
	if ok, err := queueA.Enqueue(ctx, sqliteEventKind, "proj-1", []byte("second"), 1); err != nil || !ok {
		t.Fatalf("Enqueue second = (%v, %v), want (true, nil)", ok, err)
	}

	jobA, err := queueA.ClaimNext(ctx, "worker-a", time.Second)
	if err != nil {
		t.Fatalf("queueA ClaimNext: %v", err)
	}
	jobB, err := queueB.ClaimNext(ctx, "worker-b", time.Second)
	if err != nil {
		t.Fatalf("queueB ClaimNext: %v", err)
	}
	if jobA == nil || jobB == nil {
		t.Fatalf("expected both workers to claim a distinct job, got jobA=%+v jobB=%+v", jobA, jobB)
	}
	if jobA.ID == jobB.ID {
		t.Fatalf("expected distinct jobs, both workers claimed %q", jobA.ID)
	}
	if err := queueA.MarkDone(ctx, jobA.ID); err != nil {
		t.Fatalf("MarkDone jobA: %v", err)
	}
	if err := queueB.MarkDone(ctx, jobB.ID); err != nil {
		t.Fatalf("MarkDone jobB: %v", err)
	}

	jobA, err = queueA.ClaimNext(ctx, "worker-a", time.Second)
	if err != nil {
		t.Fatalf("queueA second ClaimNext: %v", err)
	}
	jobB, err = queueB.ClaimNext(ctx, "worker-b", time.Second)
	if err != nil {
		t.Fatalf("queueB second ClaimNext: %v", err)
	}
	if jobA != nil || jobB != nil {
		t.Fatalf("expected queue to drain after shared consumer claims, got jobA=%+v jobB=%+v", jobA, jobB)
	}
}

func TestJetStreamQueueOnlyDedupesSupportedKinds(t *testing.T) {
	srv := startJetStreamTestServer(t)
	queue, err := NewJetStreamQueue(srv.ClientURL(), "worker-a", sqliteNativeKind, "backfill")
	if err != nil {
		t.Fatalf("NewJetStreamQueue: %v", err)
	}
	t.Cleanup(func() {
		if err := queue.Close(); err != nil {
			t.Fatalf("Close queue: %v", err)
		}
	})

	ctx := context.Background()
	if ok, err := queue.EnqueueKeyed(ctx, sqliteNativeKind, "proj-1", "crash-1", []byte("native"), 1); err != nil || !ok {
		t.Fatalf("EnqueueKeyed native first = (%v, %v), want (true, nil)", ok, err)
	}
	if ok, err := queue.EnqueueKeyed(ctx, sqliteNativeKind, "proj-1", "crash-1", []byte("native"), 1); err != nil || !ok {
		t.Fatalf("EnqueueKeyed native duplicate = (%v, %v), want (true, nil)", ok, err)
	}
	if ok, err := queue.EnqueueKeyed(ctx, "backfill", "", "backfill:tick", []byte("{}"), 1); err != nil || !ok {
		t.Fatalf("EnqueueKeyed backfill first = (%v, %v), want (true, nil)", ok, err)
	}
	if ok, err := queue.EnqueueKeyed(ctx, "backfill", "", "backfill:tick", []byte("{}"), 1); err != nil || !ok {
		t.Fatalf("EnqueueKeyed backfill second = (%v, %v), want (true, nil)", ok, err)
	}

	nativeOne, err := queue.ClaimNext(ctx, "worker-1", time.Second)
	if err != nil {
		t.Fatalf("ClaimNext nativeOne: %v", err)
	}
	nativeTwo, err := queue.ClaimNext(ctx, "worker-1", time.Second)
	if err != nil {
		t.Fatalf("ClaimNext nativeTwo: %v", err)
	}
	backfillOne, err := queue.ClaimNext(ctx, "worker-1", time.Second)
	if err != nil {
		t.Fatalf("ClaimNext backfillOne: %v", err)
	}
	backfillTwo, err := queue.ClaimNext(ctx, "worker-1", time.Second)
	if err != nil {
		t.Fatalf("ClaimNext backfillTwo: %v", err)
	}

	claimed := []*Job{nativeOne, nativeTwo, backfillOne, backfillTwo}
	var nativeCount, backfillCount int
	for _, job := range claimed {
		if job == nil {
			continue
		}
		switch job.Kind {
		case sqliteNativeKind:
			nativeCount++
		case "backfill":
			backfillCount++
		}
	}
	if nativeCount != 1 {
		t.Fatalf("nativeCount = %d, want 1", nativeCount)
	}
	if backfillCount != 2 {
		t.Fatalf("backfillCount = %d, want 2", backfillCount)
	}
}
