package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"urgentry/internal/issue"
	"urgentry/internal/runtimeasync"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"

	"github.com/nats-io/nats-server/v2/server"
)

func TestPipeline_ProcessesEvents(t *testing.T) {
	events := store.NewMemoryEventStore()
	groups := issue.NewMemoryGroupStore()
	blobs := store.NewMemoryBlobStore()

	proc := &issue.Processor{
		Events: events,
		Groups: groups,
		Blobs:  blobs,
	}

	p := New(proc, 100, 1)
	ctx := context.Background()
	p.Start(ctx)

	// Enqueue a valid event.
	payload, _ := json.Marshal(map[string]any{
		"event_id":  "aaaabbbbccccdddd1111222233334444",
		"platform":  "go",
		"level":     "error",
		"message":   "test error",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})

	ok := p.Enqueue(Item{ProjectID: "proj-1", RawEvent: payload})
	if !ok {
		t.Fatal("Enqueue returned false")
	}
	waitForStoredEvents(t, events, "proj-1", 1)
	p.Stop()
}

func TestPipeline_GracefulShutdown(t *testing.T) {
	events := store.NewMemoryEventStore()
	groups := issue.NewMemoryGroupStore()
	blobs := store.NewMemoryBlobStore()

	proc := &issue.Processor{
		Events: events,
		Groups: groups,
		Blobs:  blobs,
	}

	p := New(proc, 1000, 2)
	ctx := context.Background()
	p.Start(ctx)

	// Enqueue several events.
	for i := 0; i < 10; i++ {
		payload, _ := json.Marshal(map[string]any{
			"platform":  "go",
			"level":     "error",
			"message":   "test error",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
		p.Enqueue(Item{ProjectID: "proj-drain", RawEvent: payload})
	}

	// Stop should drain the queue.
	p.Stop()

	evts, _ := events.ListEvents(context.Background(), "proj-drain", store.ListOpts{Limit: 100})
	if len(evts) != 10 {
		t.Errorf("after drain: expected 10 events, got %d", len(evts))
	}
}

func TestPipeline_NonBlockingRejectsFull(t *testing.T) {
	events := store.NewMemoryEventStore()
	groups := issue.NewMemoryGroupStore()
	blobs := store.NewMemoryBlobStore()

	proc := &issue.Processor{
		Events: events,
		Groups: groups,
		Blobs:  blobs,
	}

	// Queue size of 1.
	p := New(proc, 1, 1)
	// Don't start workers, so the queue stays full.

	payload := []byte(`{"platform":"go","level":"error","message":"x"}`)
	p.EnqueueNonBlocking(Item{ProjectID: "proj-1", RawEvent: payload})
	// Second enqueue should fail (non-blocking, queue full, no workers).
	ok := p.EnqueueNonBlocking(Item{ProjectID: "proj-1", RawEvent: payload})
	if ok {
		t.Error("EnqueueNonBlocking should return false when queue is full")
	}

	p.Stop()
}

func TestPipeline_AlertCallbackRunsAsync(t *testing.T) {
	events := store.NewMemoryEventStore()
	groups := issue.NewMemoryGroupStore()
	blobs := store.NewMemoryBlobStore()

	proc := &issue.Processor{
		Events: events,
		Groups: groups,
		Blobs:  blobs,
	}

	p := New(proc, 100, 1)
	var alertCalls atomic.Int32
	started := make(chan struct{}, 1)
	p.SetAlertCallback(func(ctx context.Context, projectID string, result issue.ProcessResult) {
		alertCalls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		time.Sleep(200 * time.Millisecond)
	})

	p.Start(context.Background())
	defer p.Stop()

	payload, _ := json.Marshal(map[string]any{
		"event_id":  "bbbbccccdddd11112222333344445555",
		"platform":  "go",
		"level":     "error",
		"message":   "alert async test",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})

	if ok := p.Enqueue(Item{ProjectID: "proj-alert", RawEvent: payload}); !ok {
		t.Fatal("Enqueue returned false")
	}

	select {
	case <-started:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("alert callback did not start")
	}

	waitForPipelineCondition(t, "wait for event before alert callback completes", func() error {
		evts, err := events.ListEvents(context.Background(), "proj-alert", store.ListOpts{Limit: 10})
		if err != nil {
			return err
		}
		if len(evts) != 1 {
			return fmt.Errorf("events = %d, want 1", len(evts))
		}
		if alertCalls.Load() != 1 {
			return fmt.Errorf("alertCalls = %d, want 1", alertCalls.Load())
		}
		return nil
	})
}

func TestPipeline_ConfigurationFreezesAfterStart(t *testing.T) {
	events := store.NewMemoryEventStore()
	groups := issue.NewMemoryGroupStore()
	blobs := store.NewMemoryBlobStore()

	proc := &issue.Processor{
		Events: events,
		Groups: groups,
		Blobs:  blobs,
	}

	p := New(proc, 10, 1)
	p.Start(context.Background())
	defer p.Stop()

	assertPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatalf("%s should panic after Start", name)
			}
		}()
		fn()
	}

	assertPanic("SetMetrics", func() { p.SetMetrics(nil) })
	assertPanic("SetAlertCallback", func() { p.SetAlertCallback(func(context.Context, string, issue.ProcessResult) {}) })
	assertPanic("SetNativeJobProcessor", func() {
		p.SetNativeJobProcessor(NativeJobProcessorFunc(func(context.Context, string, []byte) error { return nil }))
	})
}

func TestPipeline_StartIsIdempotent(t *testing.T) {
	events := store.NewMemoryEventStore()
	groups := issue.NewMemoryGroupStore()
	blobs := store.NewMemoryBlobStore()

	proc := &issue.Processor{
		Events: events,
		Groups: groups,
		Blobs:  blobs,
	}

	p := New(proc, 10, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p.Start(ctx)
	p.Start(ctx)
	p.Stop()
	p.Stop()
}

func TestPipeline_EnqueueReturnsFalseAfterStop(t *testing.T) {
	events := store.NewMemoryEventStore()
	groups := issue.NewMemoryGroupStore()
	blobs := store.NewMemoryBlobStore()

	proc := &issue.Processor{
		Events: events,
		Groups: groups,
		Blobs:  blobs,
	}

	p := New(proc, 10, 1)
	p.Start(context.Background())
	p.Stop()

	payload := []byte(`{"platform":"go","level":"error","message":"x"}`)
	if ok := p.Enqueue(Item{ProjectID: "proj-1", RawEvent: payload}); ok {
		t.Fatal("Enqueue should return false after Stop")
	}
	if ok := p.EnqueueNonBlocking(Item{ProjectID: "proj-1", RawEvent: payload}); ok {
		t.Fatal("EnqueueNonBlocking should return false after Stop")
	}
}

func TestPipeline_DurableWorkerSkipsDuplicateCompletedRedelivery(t *testing.T) {
	events := store.NewMemoryEventStore()
	groups := issue.NewMemoryGroupStore()
	blobs := store.NewMemoryBlobStore()

	proc := &issue.Processor{
		Events: events,
		Groups: groups,
		Blobs:  blobs,
	}

	payload, _ := json.Marshal(map[string]any{
		"event_id":  "dddd1111222233334444555566667777",
		"platform":  "go",
		"level":     "error",
		"message":   "duplicate delivery",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
	queue := &durableTestQueue{
		jobs: []*runtimeasync.Job{
			{ID: "job-1", Kind: sqlite.JobKindEvent, ProjectID: "proj-dup", Payload: payload, Attempts: 1},
			{ID: "job-2", Kind: sqlite.JobKindEvent, ProjectID: "proj-dup", Payload: payload, Attempts: 2},
		},
	}

	p := NewDurable(proc, queue, 10, 1)
	var alerts atomic.Int32
	p.SetAlertCallback(func(context.Context, string, issue.ProcessResult) {
		alerts.Add(1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)

	waitForPipelineCondition(t, "wait for duplicate redelivery jobs to finish", func() error {
		if got := len(queue.doneIDs()); got != 2 {
			return fmt.Errorf("done jobs = %d, want 2", got)
		}
		return nil
	})
	cancel()
	p.Stop()

	if got := len(queue.doneIDs()); got != 2 {
		t.Fatalf("done jobs = %d, want 2", got)
	}
	evts, err := events.ListEvents(context.Background(), "proj-dup", store.ListOpts{Limit: 10})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(evts) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(evts))
	}
	if alerts.Load() != 1 {
		t.Fatalf("alerts = %d, want 1", alerts.Load())
	}
}

func TestPipeline_JetStreamBacklogRecovery(t *testing.T) {
	t.Parallel()

	srv := startPipelineJetStreamTestServer(t)
	queue, err := runtimeasync.NewJetStreamQueue(srv.ClientURL(), "pipeline-backlog", sqlite.JobKindEvent)
	if err != nil {
		t.Fatalf("NewJetStreamQueue: %v", err)
	}
	t.Cleanup(func() {
		if err := queue.Close(); err != nil {
			t.Fatalf("Close queue: %v", err)
		}
	})

	events := store.NewMemoryEventStore()
	groups := issue.NewMemoryGroupStore()
	blobs := store.NewMemoryBlobStore()
	proc := &issue.Processor{
		Events: events,
		Groups: groups,
		Blobs:  blobs,
	}

	payload, _ := json.Marshal(map[string]any{
		"event_id":  "eeee1111222233334444555566667777",
		"platform":  "go",
		"level":     "error",
		"message":   "jetstream backlog recovery",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
	if ok, err := queue.Enqueue(context.Background(), sqlite.JobKindEvent, "proj-backlog", payload, 10); err != nil || !ok {
		t.Fatalf("Enqueue backlog event = (%v, %v), want (true, nil)", ok, err)
	}

	p := NewDurable(proc, queue, 10, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	waitForStoredEvents(t, events, "proj-backlog", 1)
}

type durableTestQueue struct {
	mu   sync.Mutex
	jobs []*runtimeasync.Job
	done []string
}

func (q *durableTestQueue) Enqueue(context.Context, string, string, []byte, int) (bool, error) {
	return true, nil
}

func (q *durableTestQueue) ClaimNext(context.Context, string, time.Duration) (*runtimeasync.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.jobs) == 0 {
		return nil, nil
	}
	job := q.jobs[0]
	q.jobs = q.jobs[1:]
	return job, nil
}

func (q *durableTestQueue) MarkDone(_ context.Context, jobID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.done = append(q.done, jobID)
	return nil
}

func (q *durableTestQueue) Requeue(context.Context, string, time.Duration, string) error {
	return nil
}

func (q *durableTestQueue) Len(context.Context) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.jobs), nil
}

func (q *durableTestQueue) RequeueExpiredProcessing(context.Context) (int64, error) {
	return 0, nil
}

func (q *durableTestQueue) doneIDs() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]string(nil), q.done...)
}

var _ runtimeasync.Queue = (*durableTestQueue)(nil)

func startPipelineJetStreamTestServer(t *testing.T) *server.Server {
	t.Helper()

	srv, err := server.NewServer(&server.Options{
		JetStream: true,
		StoreDir:  t.TempDir(),
		Host:      "127.0.0.1",
		Port:      -1,
		HTTPPort:  -1,
		NoLog:     true,
		NoSigs:    true,
	})
	if err != nil {
		t.Fatalf("server.NewServer: %v", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		srv.Shutdown()
		t.Fatal("nats test server did not become ready")
	}
	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})
	return srv
}
