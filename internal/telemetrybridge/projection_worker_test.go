package telemetrybridge

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"urgentry/internal/runtimeasync"
)

func TestFamiliesForEventType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		eventType string
		want      []Family
	}{
		{"error", []Family{FamilyEvents}},
		{"warning", []Family{FamilyEvents}},
		{"info", []Family{FamilyEvents}},
		{"transaction", []Family{FamilyTransactions, FamilySpans}},
		{"Transaction", []Family{FamilyTransactions, FamilySpans}},
		{"log", []Family{FamilyLogs}},
		{"LOG", []Family{FamilyLogs}},
		{"replay", []Family{FamilyReplays, FamilyReplayTimeline}},
		{"profile", []Family{FamilyProfiles}},
		{"", []Family{FamilyEvents}},
		{"unknown", []Family{FamilyEvents}},
	}
	for _, tt := range tests {
		got := FamiliesForEventType(tt.eventType)
		if len(got) != len(tt.want) {
			t.Errorf("FamiliesForEventType(%q) = %v, want %v", tt.eventType, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("FamiliesForEventType(%q)[%d] = %q, want %q", tt.eventType, i, got[i], tt.want[i])
			}
		}
	}
}

func TestProjectionEnqueuerNilQueueIsNoOp(t *testing.T) {
	t.Parallel()

	enqueuer := NewProjectionEnqueuer(nil, 100)
	if err := enqueuer.EnqueueProjection(context.Background(), "proj-1", "error", FamilyEvents); err != nil {
		t.Fatalf("EnqueueProjection with nil queue: %v", err)
	}
}

func TestProjectionEnqueuerNilSelfIsNoOp(t *testing.T) {
	t.Parallel()

	var enqueuer *ProjectionEnqueuer
	if err := enqueuer.EnqueueProjection(context.Background(), "proj-1", "error", FamilyEvents); err != nil {
		t.Fatalf("EnqueueProjection on nil enqueuer: %v", err)
	}
}

type projectionTestQueue struct {
	mu   sync.Mutex
	jobs []projectionTestJob
}

type projectionTestJob struct {
	kind      string
	projectID string
	payload   []byte
}

func (q *projectionTestQueue) Enqueue(_ context.Context, kind, projectID string, payload []byte, _ int) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs = append(q.jobs, projectionTestJob{kind: kind, projectID: projectID, payload: append([]byte(nil), payload...)})
	return true, nil
}

func (q *projectionTestQueue) ClaimNext(context.Context, string, time.Duration) (*runtimeasync.Job, error) {
	return nil, nil
}
func (q *projectionTestQueue) MarkDone(context.Context, string) error  { return nil }
func (q *projectionTestQueue) Requeue(context.Context, string, time.Duration, string) error {
	return nil
}
func (q *projectionTestQueue) Len(context.Context) (int, error)                { return 0, nil }
func (q *projectionTestQueue) RequeueExpiredProcessing(context.Context) (int64, error) {
	return 0, nil
}

func TestProjectionEnqueuerPublishesValidJob(t *testing.T) {
	t.Parallel()

	q := &projectionTestQueue{}
	enqueuer := NewProjectionEnqueuer(q, 100)
	if err := enqueuer.EnqueueProjection(context.Background(), "proj-1", "transaction", FamilyTransactions, FamilySpans); err != nil {
		t.Fatalf("EnqueueProjection: %v", err)
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(q.jobs))
	}
	job := q.jobs[0]
	if job.kind != "bridge_projection" {
		t.Fatalf("job kind = %q, want bridge_projection", job.kind)
	}
	if job.projectID != "proj-1" {
		t.Fatalf("job projectID = %q, want proj-1", job.projectID)
	}
	var pj ProjectionJob
	if err := json.Unmarshal(job.payload, &pj); err != nil {
		t.Fatalf("unmarshal job payload: %v", err)
	}
	if pj.ProjectID != "proj-1" {
		t.Fatalf("ProjectionJob.ProjectID = %q, want proj-1", pj.ProjectID)
	}
	if pj.EventType != "transaction" {
		t.Fatalf("ProjectionJob.EventType = %q, want transaction", pj.EventType)
	}
	if len(pj.Families) != 2 {
		t.Fatalf("ProjectionJob.Families len = %d, want 2", len(pj.Families))
	}
	if pj.Families[0] != "transactions" || pj.Families[1] != "spans" {
		t.Fatalf("ProjectionJob.Families = %v, want [transactions spans]", pj.Families)
	}
	if pj.EnqueuedAt == "" {
		t.Fatal("ProjectionJob.EnqueuedAt is empty")
	}
}

func TestProjectionRetryDelay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		attempts int
		wantMin  time.Duration
		wantMax  time.Duration
	}{
		{0, time.Second, time.Second},
		{1, time.Second, time.Second},
		{2, 2 * time.Second, 2 * time.Second},
		{3, 4 * time.Second, 4 * time.Second},
		{10, 30 * time.Second, 30 * time.Second},
	}
	for _, tt := range tests {
		got := projectionRetryDelay(tt.attempts)
		if got < tt.wantMin || got > tt.wantMax {
			t.Errorf("projectionRetryDelay(%d) = %s, want [%s, %s]", tt.attempts, got, tt.wantMin, tt.wantMax)
		}
	}
}

func TestProjectionWorkerNilDepsBlocksUntilCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	worker := NewProjectionWorker(nil, nil, "test-worker")
	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not exit after context cancellation")
	}
}

func TestProjectionWorkerResolvesOrganizationScopeFromProjectCatalog(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := openProjectorSourceDB(t)
	seedProjectorSource(t, source)
	bridge := openMigratedTelemetryTestDatabase(t)
	projector := NewProjector(source, bridge)
	projector.batchSize = 128
	worker := NewProjectionWorker(projector, nil, "test-worker")

	payload, err := json.Marshal(ProjectionJob{
		ProjectID:  "proj-1",
		Families:   []string{string(FamilyTransactions), string(FamilySpans)},
		EventType:  "transaction",
		EnqueuedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("marshal projection job: %v", err)
	}
	if err := worker.processJob(ctx, &runtimeasync.Job{
		ID:        "job-1",
		Kind:      jobKindBridgeProjection,
		ProjectID: "proj-1",
		Payload:   payload,
	}); err != nil {
		t.Fatalf("processJob() error = %v", err)
	}

	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.transaction_facts WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.span_facts WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.projector_cursors WHERE scope_id = 'proj-1' AND cursor_family IN ('transactions', 'spans')`, 2)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.projector_cursors WHERE scope_id = 'org-1' AND cursor_family IN ('transactions', 'spans')`, 2)
}
