package telemetrybridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"urgentry/internal/runtimeasync"
)

const (
	jobKindBridgeProjection = "bridge_projection"
	projectionIdlePoll      = 100 * time.Millisecond
	projectionLeaseDuration = 30 * time.Second
)

// ProjectionJob describes a bridge projection work item enqueued after a
// source-of-truth write. Families lists which projector families should
// catch up for the given scope.
type ProjectionJob struct {
	OrganizationID string   `json:"organizationId"`
	ProjectID      string   `json:"projectId"`
	Families       []string `json:"families"`
	EventType      string   `json:"eventType,omitempty"`
	EnqueuedAt     string   `json:"enqueuedAt"`
}

// ProjectionEnqueuer durably enqueues bridge projection work after
// source-of-truth writes succeed. It supports both the durable JetStream
// queue and the SQLite fallback queue.
type ProjectionEnqueuer struct {
	queue runtimeasync.Queue
	limit int
}

// NewProjectionEnqueuer creates an enqueuer backed by the given queue.
func NewProjectionEnqueuer(queue runtimeasync.Queue, limit int) *ProjectionEnqueuer {
	if limit <= 0 {
		limit = 1000
	}
	return &ProjectionEnqueuer{queue: queue, limit: limit}
}

// EnqueueProjection enqueues a projection job for the given project and
// families. The deduplication key prevents duplicate projection work for
// the same project within a NATS dedup window.
func (e *ProjectionEnqueuer) EnqueueProjection(ctx context.Context, projectID string, eventType string, families ...Family) error {
	if e == nil || e.queue == nil {
		return nil
	}
	familyStrings := make([]string, 0, len(families))
	for _, f := range families {
		familyStrings = append(familyStrings, string(f))
	}
	job := ProjectionJob{
		ProjectID:  strings.TrimSpace(projectID),
		Families:   familyStrings,
		EventType:  eventType,
		EnqueuedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal projection job: %w", err)
	}
	if _, err := e.queue.Enqueue(ctx, jobKindBridgeProjection, projectID, payload, e.limit); err != nil {
		return fmt.Errorf("enqueue projection job: %w", err)
	}
	return nil
}

// FamiliesForEventType returns the projector families that need to be
// caught up when a given event type is written to the source of truth.
func FamiliesForEventType(eventType string) []Family {
	switch strings.TrimSpace(strings.ToLower(eventType)) {
	case "transaction":
		return []Family{FamilyTransactions, FamilySpans}
	case "log":
		return []Family{FamilyLogs}
	case "replay":
		return []Family{FamilyReplays, FamilyReplayTimeline}
	case "profile":
		return []Family{FamilyProfiles}
	default:
		// error, warning, info, debug, fatal, etc. all live in events
		return []Family{FamilyEvents}
	}
}

// ProjectionWorker processes bridge projection jobs from a durable queue.
// Each job triggers a StepFamilies call on the projector for the affected
// families and scope.
type ProjectionWorker struct {
	projector *Projector
	queue     runtimeasync.Queue
	workerID  string
}

// NewProjectionWorker creates a worker that drains projection jobs.
func NewProjectionWorker(projector *Projector, queue runtimeasync.Queue, workerID string) *ProjectionWorker {
	return &ProjectionWorker{
		projector: projector,
		queue:     queue,
		workerID:  workerID,
	}
}

// Run processes projection jobs until the context is cancelled.
func (w *ProjectionWorker) Run(ctx context.Context) {
	if w == nil || w.projector == nil || w.queue == nil {
		<-ctx.Done()
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, err := w.queue.ClaimNext(ctx, w.workerID, projectionLeaseDuration)
		if err != nil {
			log.Error().Err(err).Str("worker_id", w.workerID).Msg("bridge projection: claim job failed")
			sleepCtx(ctx, projectionIdlePoll)
			continue
		}
		if job == nil {
			sleepCtx(ctx, projectionIdlePoll)
			continue
		}
		if job.Kind != jobKindBridgeProjection {
			_ = w.queue.MarkDone(ctx, job.ID)
			continue
		}

		if processErr := w.processJob(ctx, job); processErr != nil {
			backoff := projectionRetryDelay(job.Attempts)
			if requeueErr := w.queue.Requeue(ctx, job.ID, backoff, processErr.Error()); requeueErr != nil {
				log.Error().Err(requeueErr).Str("job_id", job.ID).Msg("bridge projection: requeue failed")
			}
			continue
		}

		if err := w.queue.MarkDone(ctx, job.ID); err != nil {
			log.Error().Err(err).Str("job_id", job.ID).Msg("bridge projection: mark done failed")
		}
	}
}

func (w *ProjectionWorker) processJob(ctx context.Context, job *runtimeasync.Job) error {
	var pj ProjectionJob
	if err := json.Unmarshal(job.Payload, &pj); err != nil {
		// Permanently bad payload -- log and discard.
		log.Error().Err(err).Str("job_id", job.ID).Msg("bridge projection: invalid job payload")
		return nil
	}

	scope := Scope{
		OrganizationID: pj.OrganizationID,
		ProjectID:      pj.ProjectID,
	}
	families := make([]Family, 0, len(pj.Families))
	for _, f := range pj.Families {
		families = append(families, Family(f))
	}

	// Step families rather than full sync -- this processes one batch per
	// family and returns, keeping latency bounded. If there is more work
	// the next job will pick it up.
	_, err := w.projector.StepFamilies(ctx, scope, families...)
	if err != nil {
		log.Error().Err(err).
			Str("job_id", job.ID).
			Str("project_id", pj.ProjectID).
			Strs("families", pj.Families).
			Msg("bridge projection: step families failed")
		return err
	}
	return nil
}

func projectionRetryDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := time.Second << min(attempts-1, 5)
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}

func sleepCtx(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
