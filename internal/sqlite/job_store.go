package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"urgentry/internal/runtimeasync"
)

const (
	JobKindEvent            = "event"
	JobKindNativeStackwalk  = "native_stackwalk"
	JobKindBackfill         = "backfill"
	JobKindBridgeProjection = "bridge_projection"
)

// Job is a claimed unit of work from the durable queue.
type Job = runtimeasync.Job

var _ runtimeasync.Queue = (*JobStore)(nil)
var _ runtimeasync.KeyedEnqueuer = (*JobStore)(nil)
var _ runtimeasync.LeaseStore = (*JobStore)(nil)

// JobStore manages durable queued work in SQLite.
type JobStore struct {
	db *sql.DB
}

// NewJobStore creates a durable job queue store.
func NewJobStore(db *sql.DB) *JobStore {
	return &JobStore{db: db}
}

// Enqueue inserts a job when the queue is below the configured limit.
func (s *JobStore) Enqueue(ctx context.Context, kind, projectID string, payload []byte, limit int) (bool, error) {
	if limit <= 0 {
		limit = 1000
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO jobs (id, kind, project_id, payload, status, attempts, available_at, created_at, updated_at)
		 SELECT ?, ?, ?, ?, 'pending', 0, ?, ?, ?
		 WHERE (SELECT COUNT(*) FROM jobs WHERE status IN ('pending', 'processing')) < ?`,
		generateID(), kind, projectID, payload, now, now, now, limit,
	)
	if err != nil {
		return false, fmt.Errorf("enqueue job: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("enqueue job rows affected: %w", err)
	}
	return affected == 1, nil
}

func (s *JobStore) EnqueueKeyed(ctx context.Context, kind, projectID, dedupeKey string, payload []byte, limit int) (bool, error) {
	if kind == JobKindNativeStackwalk && dedupeKey != "" {
		var count int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*)
			   FROM jobs
			  WHERE kind = ?
			    AND status IN ('pending', 'processing')
			    AND COALESCE(json_extract(payload, '$.crashId'), '') = ?`,
			JobKindNativeStackwalk, dedupeKey,
		).Scan(&count); err != nil {
			return false, fmt.Errorf("count keyed native jobs: %w", err)
		}
		if count > 0 {
			return true, nil
		}
	}
	return s.Enqueue(ctx, kind, projectID, payload, limit)
}

// ClaimNext atomically claims the next available job.
func (s *JobStore) ClaimNext(ctx context.Context, workerID string, leaseDuration time.Duration) (*runtimeasync.Job, error) {
	if leaseDuration <= 0 {
		leaseDuration = 30 * time.Second
	}
	now := time.Now().UTC()
	leaseUntil := now.Add(leaseDuration).Format(time.RFC3339)
	row := s.db.QueryRowContext(ctx,
		`UPDATE jobs
		 SET status = 'processing',
		     attempts = attempts + 1,
		     lease_until = ?,
		     worker_id = ?,
		     updated_at = ?
		 WHERE id = (
		     SELECT id
		     FROM jobs
		     WHERE status = 'pending' AND available_at <= ?
		     ORDER BY available_at ASC, created_at ASC
		     LIMIT 1
		 )
		 RETURNING id, kind, project_id, payload, attempts`,
		leaseUntil, workerID, now.Format(time.RFC3339), now.Format(time.RFC3339),
	)

	var job runtimeasync.Job
	if err := row.Scan(&job.ID, &job.Kind, &job.ProjectID, &job.Payload, &job.Attempts); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("claim next job: %w", err)
	}
	return &job, nil
}

// MarkDone deletes a completed job.
func (s *JobStore) MarkDone(ctx context.Context, jobID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE id = ?`, jobID); err != nil {
		return fmt.Errorf("delete completed job: %w", err)
	}
	return nil
}

// Requeue moves a failed job back to pending after a delay.
func (s *JobStore) Requeue(ctx context.Context, jobID string, delay time.Duration, lastError string) error {
	now := time.Now().UTC()
	availableAt := now.Add(delay).Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx,
		`UPDATE jobs
		 SET status = 'pending',
		     available_at = ?,
		     lease_until = NULL,
		     worker_id = NULL,
		     last_error = ?,
		     updated_at = ?
		 WHERE id = ?`,
		availableAt, lastError, now.Format(time.RFC3339), jobID,
	); err != nil {
		return fmt.Errorf("requeue job: %w", err)
	}
	return nil
}

// Len returns the queued + leased job count for health reporting.
func (s *JobStore) Len(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE status IN ('pending', 'processing')`,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count jobs: %w", err)
	}
	return count, nil
}

// RequeueExpiredProcessing returns expired processing jobs to the pending queue.
func (s *JobStore) RequeueExpiredProcessing(ctx context.Context) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`UPDATE jobs
		 SET status = 'pending',
		     available_at = ?,
		     lease_until = NULL,
		     worker_id = NULL,
		     updated_at = ?
		 WHERE status = 'processing' AND lease_until IS NOT NULL AND lease_until <= ?`,
		now, now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("requeue expired jobs: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("requeue expired jobs rows affected: %w", err)
	}
	return affected, nil
}

// AcquireLease acquires or refreshes a named runtime lease.
func (s *JobStore) AcquireLease(ctx context.Context, name, holderID string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO runtime_leases (name, holder_id, lease_until, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
		     holder_id = excluded.holder_id,
		     lease_until = excluded.lease_until,
		     updated_at = excluded.updated_at
		 WHERE runtime_leases.lease_until <= excluded.updated_at
		    OR runtime_leases.holder_id = excluded.holder_id`,
		name, holderID, now.Add(ttl).Format(time.RFC3339), now.Format(time.RFC3339),
	)
	if err != nil {
		return false, fmt.Errorf("acquire runtime lease: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("acquire runtime lease rows affected: %w", err)
	}
	return affected == 1, nil
}
