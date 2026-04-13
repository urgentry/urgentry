package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/runtimeasync"
)

const appendOnlyEventConsumer = "event"
const appendOnlyJobPrefix = "ilog:"
const appendOnlyTrimInterval int64 = 256

type AppendOnlyEventQueue struct {
	db       *sql.DB
	fallback runtimeasync.Queue
}

var _ runtimeasync.Queue = (*AppendOnlyEventQueue)(nil)
var _ runtimeasync.KeyedEnqueuer = (*AppendOnlyEventQueue)(nil)

func NewAppendOnlyEventQueue(db *sql.DB, fallback runtimeasync.Queue) *AppendOnlyEventQueue {
	return &AppendOnlyEventQueue{db: db, fallback: fallback}
}

func (q *AppendOnlyEventQueue) Enqueue(ctx context.Context, kind, projectID string, payload []byte, limit int) (bool, error) {
	if strings.TrimSpace(kind) != JobKindEvent {
		return q.fallback.Enqueue(ctx, kind, projectID, payload, limit)
	}
	if limit <= 0 {
		limit = 1000
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var pending int
	if err := withBusyRetry(30*time.Second, func() error {
		return q.db.QueryRowContext(ctx, `
			WITH last AS (
				SELECT COALESCE((SELECT last_seq FROM ingest_consumers WHERE consumer = ?), 0) AS last_seq
			),
			tail AS (
				SELECT COALESCE(MAX(seq), 0) AS max_seq FROM ingest_log
			)
			SELECT max_seq - last_seq FROM tail, last
		`, appendOnlyEventConsumer).Scan(&pending)
	}); err != nil {
		return false, fmt.Errorf("count append-only ingest backlog: %w", err)
	}
	if pending >= limit {
		return false, nil
	}
	if err := withBusyRetry(30*time.Second, func() error {
		_, err := q.db.ExecContext(ctx,
			`INSERT INTO ingest_log (project_id, kind, payload, created_at) VALUES (?, ?, ?, ?)`,
			projectID, JobKindEvent, payload, now,
		)
		return err
	}); err != nil {
		return false, fmt.Errorf("append ingest log row: %w", err)
	}
	return true, nil
}

func (q *AppendOnlyEventQueue) EnqueueKeyed(ctx context.Context, kind, projectID, dedupeKey string, payload []byte, limit int) (bool, error) {
	if strings.TrimSpace(kind) != JobKindEvent {
		if keyed, ok := q.fallback.(runtimeasync.KeyedEnqueuer); ok {
			return keyed.EnqueueKeyed(ctx, kind, projectID, dedupeKey, payload, limit)
		}
	}
	return q.Enqueue(ctx, kind, projectID, payload, limit)
}

func (q *AppendOnlyEventQueue) ClaimNext(ctx context.Context, workerID string, leaseDuration time.Duration) (*runtimeasync.Job, error) {
	if job, err := q.fallback.ClaimNext(ctx, workerID, leaseDuration); err != nil || job != nil {
		return job, err
	}

	var (
		job       runtimeasync.Job
		createdAt string
		attempts  int
	)
	err := withBusyRetry(30*time.Second, func() error {
		return q.db.QueryRowContext(ctx, `
			WITH last AS (
				SELECT COALESCE((SELECT last_seq FROM ingest_consumers WHERE consumer = ?), 0) AS last_seq
			)
			SELECT l.seq, l.project_id, l.payload, l.created_at, COALESCE(r.attempts, 0)
			FROM ingest_log l
			LEFT JOIN ingest_retries r ON r.seq = l.seq
			CROSS JOIN last
			WHERE l.seq > last.last_seq
			  AND (r.available_at IS NULL OR r.available_at <= ?)
			ORDER BY l.seq ASC
			LIMIT 1
		`, appendOnlyEventConsumer, time.Now().UTC().Format(time.RFC3339)).Scan(&job.ID, &job.ProjectID, &job.Payload, &createdAt, &attempts)
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("claim append-only ingest row: %w", err)
	}
	seq, err := strconv.ParseInt(job.ID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse append-only seq: %w", err)
	}
	job.ID = appendOnlyJobPrefix + strconv.FormatInt(seq, 10)
	job.Kind = JobKindEvent
	job.Attempts = attempts + 1
	job.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
	if err != nil {
		job.CreatedAt = time.Now().UTC()
	}
	return &job, nil
}

func (q *AppendOnlyEventQueue) MarkDone(ctx context.Context, jobID string) error {
	seq, ok := parseAppendOnlySeq(jobID)
	if !ok {
		return q.fallback.MarkDone(ctx, jobID)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return withBusyRetry(30*time.Second, func() error {
		_, err := q.db.ExecContext(ctx, `
			INSERT INTO ingest_consumers (consumer, last_seq, updated_at)
			VALUES (?, ?, ?)
			ON CONFLICT(consumer) DO UPDATE SET
				last_seq = CASE WHEN excluded.last_seq > ingest_consumers.last_seq THEN excluded.last_seq ELSE ingest_consumers.last_seq END,
				updated_at = excluded.updated_at
		`, appendOnlyEventConsumer, seq, now)
		if err != nil {
			return err
		}
		if _, err = q.db.ExecContext(ctx, `DELETE FROM ingest_retries WHERE seq = ?`, seq); err != nil {
			return err
		}
		if seq%appendOnlyTrimInterval != 0 {
			return nil
		}
		if _, err = q.db.ExecContext(ctx, `DELETE FROM ingest_log WHERE seq <= ?`, seq); err != nil {
			return err
		}
		_, err = q.db.ExecContext(ctx, `DELETE FROM ingest_retries WHERE seq <= ?`, seq)
		return err
	})
}

func (q *AppendOnlyEventQueue) Requeue(ctx context.Context, jobID string, delay time.Duration, lastError string) error {
	seq, ok := parseAppendOnlySeq(jobID)
	if !ok {
		return q.fallback.Requeue(ctx, jobID, delay, lastError)
	}
	now := time.Now().UTC()
	if delay <= 0 {
		delay = time.Second
	}
	return withBusyRetry(30*time.Second, func() error {
		_, err := q.db.ExecContext(ctx, `
			INSERT INTO ingest_retries (seq, attempts, available_at, last_error, updated_at)
			VALUES (?, 1, ?, ?, ?)
			ON CONFLICT(seq) DO UPDATE SET
				attempts = ingest_retries.attempts + 1,
				available_at = excluded.available_at,
				last_error = excluded.last_error,
				updated_at = excluded.updated_at
		`, seq, now.Add(delay).Format(time.RFC3339), lastError, now.Format(time.RFC3339))
		return err
	})
}

func (q *AppendOnlyEventQueue) Len(ctx context.Context) (int, error) {
	count, err := appendOnlyBacklog(ctx, q.db)
	if err != nil {
		return 0, err
	}
	if fallbackLen, err := q.fallback.Len(ctx); err == nil {
		count += fallbackLen
	}
	return count, nil
}

func (q *AppendOnlyEventQueue) RequeueExpiredProcessing(ctx context.Context) (int64, error) {
	return q.fallback.RequeueExpiredProcessing(ctx)
}

func appendOnlyBacklog(ctx context.Context, db *sql.DB) (int, error) {
	var pending int
	if err := withBusyRetry(30*time.Second, func() error {
		return db.QueryRowContext(ctx, `
			WITH last AS (
				SELECT COALESCE((SELECT last_seq FROM ingest_consumers WHERE consumer = ?), 0) AS last_seq
			),
			tail AS (
				SELECT COALESCE(MAX(seq), 0) AS max_seq FROM ingest_log
			)
			SELECT max_seq - last_seq FROM tail, last
		`, appendOnlyEventConsumer).Scan(&pending)
	}); err != nil {
		return 0, fmt.Errorf("count append-only ingest backlog: %w", err)
	}
	return pending, nil
}

func parseAppendOnlySeq(jobID string) (int64, bool) {
	if !strings.HasPrefix(jobID, appendOnlyJobPrefix) {
		return 0, false
	}
	seq, err := strconv.ParseInt(strings.TrimPrefix(jobID, appendOnlyJobPrefix), 10, 64)
	if err != nil {
		return 0, false
	}
	return seq, true
}
