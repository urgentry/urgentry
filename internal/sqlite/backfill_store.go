package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/sqlutil"
)

type BackfillKind string

const (
	BackfillKindNativeReprocess  BackfillKind = "native_reprocess"
	BackfillKindTelemetryRebuild BackfillKind = "telemetry_rebuild"
)

type BackfillStatus string

const (
	BackfillStatusPending   BackfillStatus = "pending"
	BackfillStatusRunning   BackfillStatus = "running"
	BackfillStatusCompleted BackfillStatus = "completed"
	BackfillStatusFailed    BackfillStatus = "failed"
	BackfillStatusCancelled BackfillStatus = "cancelled"
)

type BackfillRun struct {
	ID                string
	Kind              BackfillKind
	Status            BackfillStatus
	OrganizationID    string
	ProjectID         string
	ReleaseVersion    string
	DebugFileID       string
	StartedAfter      time.Time
	EndedBefore       time.Time
	CursorRowID       int64
	TotalItems        int
	ProcessedItems    int
	UpdatedItems      int
	FailedItems       int
	RequestedByUserID string
	RequestedVia      string
	WorkerID          string
	LastError         string
	CreatedAt         time.Time
	StartedAt         time.Time
	FinishedAt        time.Time
	UpdatedAt         time.Time
	LeaseUntil        time.Time
}

type CreateBackfillRun struct {
	Kind              BackfillKind
	OrganizationID    string
	ProjectID         string
	ReleaseVersion    string
	DebugFileID       string
	StartedAfter      time.Time
	EndedBefore       time.Time
	RequestedByUserID string
	RequestedVia      string
}

var ErrBackfillLeaseLost = errors.New("backfill run lease lost")
var ErrBackfillConflict = errors.New("backfill run conflicts with an active run")

type BackfillConflictError struct {
	Run BackfillRun
}

func (e *BackfillConflictError) Error() string {
	if e == nil {
		return ErrBackfillConflict.Error()
	}
	if strings.TrimSpace(e.Run.ID) == "" {
		return ErrBackfillConflict.Error()
	}
	return fmt.Sprintf("%s: %s", ErrBackfillConflict, e.Run.ID)
}

func (e *BackfillConflictError) Is(target error) bool {
	return target == ErrBackfillConflict
}

type BackfillStore struct {
	db *sql.DB
}

func NewBackfillStore(db *sql.DB) *BackfillStore {
	return &BackfillStore{db: db}
}

func IsBackfillConflict(err error) bool {
	return errors.Is(err, ErrBackfillConflict)
}

func (s *BackfillStore) CreateRun(ctx context.Context, in CreateBackfillRun) (*BackfillRun, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("backfill store is not configured")
	}
	if in.OrganizationID == "" {
		return nil, fmt.Errorf("organization id is required")
	}
	if in.Kind == "" {
		in.Kind = BackfillKindNativeReprocess
	}
	now := time.Now().UTC()

	existing, err := s.findActiveDuplicate(ctx, in)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	run := &BackfillRun{
		ID:                generateID(),
		Kind:              in.Kind,
		Status:            BackfillStatusPending,
		OrganizationID:    in.OrganizationID,
		ProjectID:         in.ProjectID,
		ReleaseVersion:    in.ReleaseVersion,
		DebugFileID:       in.DebugFileID,
		StartedAfter:      in.StartedAfter.UTC(),
		EndedBefore:       in.EndedBefore.UTC(),
		RequestedByUserID: in.RequestedByUserID,
		RequestedVia:      in.RequestedVia,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	inserted, err := s.insertRunIfNoConflict(ctx, run)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			existing, findErr := s.findActiveDuplicate(ctx, in)
			if findErr != nil {
				return nil, findErr
			}
			if existing != nil {
				return existing, nil
			}
		}
		return nil, fmt.Errorf("create backfill run: %w", err)
	}
	if !inserted {
		existing, err := s.findActiveDuplicate(ctx, in)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return existing, nil
		}
		conflict, err := s.findActiveConflict(ctx, in)
		if err != nil {
			return nil, err
		}
		if conflict != nil {
			return nil, &BackfillConflictError{Run: *conflict}
		}
		return nil, fmt.Errorf("create backfill run: no row inserted")
	}
	return run, nil
}

func (s *BackfillStore) ListRuns(ctx context.Context, organizationID string, limit int) ([]BackfillRun, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, status, organization_id, COALESCE(project_id, ''), COALESCE(release_version, ''),
			        COALESCE(debug_file_id, ''), COALESCE(started_after, ''), COALESCE(ended_before, ''), cursor_rowid,
			        total_items, processed_items, updated_items, failed_items,
		        COALESCE(requested_by_user_id, ''), COALESCE(requested_via, ''), COALESCE(worker_id, ''),
		        COALESCE(last_error, ''), created_at, COALESCE(started_at, ''), COALESCE(finished_at, ''),
		        updated_at, COALESCE(lease_until, '')
		   FROM backfill_runs
		  WHERE organization_id = ?
		  ORDER BY created_at DESC
		  LIMIT ?`,
		organizationID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list backfill runs: %w", err)
	}
	defer rows.Close()

	runs := make([]BackfillRun, 0, limit)
	for rows.Next() {
		run, err := scanBackfillRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *BackfillStore) ListScopedRuns(ctx context.Context, organizationID, projectID, releaseVersion, debugFileID string, limit int) ([]BackfillRun, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `SELECT id, kind, status, organization_id, COALESCE(project_id, ''), COALESCE(release_version, ''),
			        COALESCE(debug_file_id, ''), COALESCE(started_after, ''), COALESCE(ended_before, ''), cursor_rowid,
			        total_items, processed_items, updated_items, failed_items,
		        COALESCE(requested_by_user_id, ''), COALESCE(requested_via, ''), COALESCE(worker_id, ''),
		        COALESCE(last_error, ''), created_at, COALESCE(started_at, ''), COALESCE(finished_at, ''),
		        updated_at, COALESCE(lease_until, '')
		   FROM backfill_runs
		  WHERE organization_id = ? AND kind = ?`
	args := []any{organizationID, string(BackfillKindNativeReprocess)}
	if projectID != "" {
		query += ` AND COALESCE(project_id, '') = ?`
		args = append(args, projectID)
	}
	if releaseVersion != "" {
		query += ` AND COALESCE(release_version, '') = ?`
		args = append(args, releaseVersion)
	}
	if debugFileID != "" {
		query += ` AND COALESCE(debug_file_id, '') = ?`
		args = append(args, debugFileID)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list scoped backfill runs: %w", err)
	}
	defer rows.Close()
	runs := make([]BackfillRun, 0, limit)
	for rows.Next() {
		run, err := scanBackfillRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *BackfillStore) LatestScopedRun(ctx context.Context, organizationID, projectID, releaseVersion, debugFileID string) (*BackfillRun, error) {
	runs, err := s.ListScopedRuns(ctx, organizationID, projectID, releaseVersion, debugFileID, 1)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, nil
	}
	return &runs[0], nil
}

func (s *BackfillStore) GetRun(ctx context.Context, organizationID, runID string) (*BackfillRun, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, kind, status, organization_id, COALESCE(project_id, ''), COALESCE(release_version, ''),
			        COALESCE(debug_file_id, ''), COALESCE(started_after, ''), COALESCE(ended_before, ''), cursor_rowid,
			        total_items, processed_items, updated_items, failed_items,
		        COALESCE(requested_by_user_id, ''), COALESCE(requested_via, ''), COALESCE(worker_id, ''),
		        COALESCE(last_error, ''), created_at, COALESCE(started_at, ''), COALESCE(finished_at, ''),
		        updated_at, COALESCE(lease_until, '')
		   FROM backfill_runs
		  WHERE organization_id = ? AND id = ?`,
		organizationID, runID,
	)
	run, err := scanBackfillRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get backfill run: %w", err)
	}
	return &run, nil
}

func (s *BackfillStore) CancelRun(ctx context.Context, organizationID, runID string) (*BackfillRun, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	row := s.db.QueryRowContext(ctx,
		`UPDATE backfill_runs
			    SET status = ?, lease_until = NULL, worker_id = NULL, finished_at = COALESCE(finished_at, ?), updated_at = ?
			  WHERE organization_id = ? AND id = ? AND status = ?
			  RETURNING id, kind, status, organization_id, COALESCE(project_id, ''), COALESCE(release_version, ''),
			            COALESCE(debug_file_id, ''), COALESCE(started_after, ''), COALESCE(ended_before, ''), cursor_rowid,
			            total_items, processed_items, updated_items, failed_items,
		            COALESCE(requested_by_user_id, ''), COALESCE(requested_via, ''), COALESCE(worker_id, ''),
		            COALESCE(last_error, ''), created_at, COALESCE(started_at, ''), COALESCE(finished_at, ''),
		            updated_at, COALESCE(lease_until, '')`,
		string(BackfillStatusCancelled), now, now, organizationID, runID,
		string(BackfillStatusPending),
	)
	run, err := scanBackfillRun(row)
	if err == sql.ErrNoRows {
		return s.GetRun(ctx, organizationID, runID)
	}
	if err != nil {
		return nil, fmt.Errorf("cancel backfill run: %w", err)
	}
	return &run, nil
}

func (s *BackfillStore) ClaimNextRunnable(ctx context.Context, workerID string, leaseDuration time.Duration) (*BackfillRun, error) {
	if leaseDuration <= 0 {
		leaseDuration = 30 * time.Second
	}
	now := time.Now().UTC()
	row := s.db.QueryRowContext(ctx,
		`UPDATE backfill_runs
		    SET status = ?,
		        worker_id = ?,
		        lease_until = ?,
		        started_at = COALESCE(started_at, ?),
		        updated_at = ?
			  WHERE id = (
		        SELECT r.id
		          FROM backfill_runs r
		         WHERE r.status IN (?, ?)
		           AND (r.status = ? OR r.lease_until IS NULL OR r.lease_until <= ?)
		           AND NOT EXISTS (
		                SELECT 1
		                  FROM backfill_runs active
		                 WHERE active.id <> r.id
		                   AND active.kind = r.kind
		                   AND active.organization_id = r.organization_id
		                   AND active.status = ?
		                   AND COALESCE(active.lease_until, '') > ?
		                   AND (
		                        (r.kind = ? AND (
		                            COALESCE(active.project_id, '') = ''
		                            OR COALESCE(r.project_id, '') = ''
		                            OR COALESCE(active.project_id, '') = COALESCE(r.project_id, '')
		                        ))
		                        OR
		                        (r.kind = ? AND (
		                            (COALESCE(active.project_id, '') = ''
		                             OR COALESCE(r.project_id, '') = ''
		                             OR COALESCE(active.project_id, '') = COALESCE(r.project_id, ''))
		                            AND
		                            (COALESCE(active.release_version, '') = ''
		                             OR COALESCE(r.release_version, '') = ''
		                             OR COALESCE(active.release_version, '') = COALESCE(r.release_version, ''))
		                            AND
		                            ((COALESCE(active.started_after, '') = ''
		                              OR COALESCE(r.ended_before, '') = ''
		                              OR COALESCE(active.started_after, '') <= COALESCE(r.ended_before, ''))
		                             AND
		                             (COALESCE(r.started_after, '') = ''
		                              OR COALESCE(active.ended_before, '') = ''
		                              OR COALESCE(r.started_after, '') <= COALESCE(active.ended_before, '')))
		                        ))
		                   )
		           )
		         ORDER BY r.created_at ASC, r.id ASC
		         LIMIT 1
		  )
			  RETURNING id, kind, status, organization_id, COALESCE(project_id, ''), COALESCE(release_version, ''),
			            COALESCE(debug_file_id, ''), COALESCE(started_after, ''), COALESCE(ended_before, ''), cursor_rowid,
			            total_items, processed_items, updated_items, failed_items,
		            COALESCE(requested_by_user_id, ''), COALESCE(requested_via, ''), COALESCE(worker_id, ''),
		            COALESCE(last_error, ''), created_at, COALESCE(started_at, ''), COALESCE(finished_at, ''),
		            updated_at, COALESCE(lease_until, '')`,
		string(BackfillStatusRunning),
		workerID,
		now.Add(leaseDuration).Format(time.RFC3339),
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
		string(BackfillStatusPending),
		string(BackfillStatusRunning),
		string(BackfillStatusPending),
		now.Format(time.RFC3339),
		string(BackfillStatusRunning),
		now.Format(time.RFC3339),
		string(BackfillKindTelemetryRebuild),
		string(BackfillKindNativeReprocess),
	)
	run, err := scanBackfillRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim backfill run: %w", err)
	}
	return &run, nil
}

func (s *BackfillStore) SetTotalItems(ctx context.Context, runID, workerID string, total int) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE backfill_runs
		    SET total_items = ?, updated_at = ?
		  WHERE id = ? AND status = ? AND worker_id = ? AND COALESCE(lease_until, '') > ?`,
		total, time.Now().UTC().Format(time.RFC3339), runID, string(BackfillStatusRunning), workerID, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return false, fmt.Errorf("set backfill total items: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("set backfill total items rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return false, ErrBackfillLeaseLost
	}
	return true, nil
}

func (s *BackfillStore) AdvanceRun(ctx context.Context, runID, workerID string, cursorRowID int64, processed, updated, failed int, done bool, lastError string) (*BackfillRun, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	status := string(BackfillStatusPending)
	finishedAt := ""
	leaseUntil := any(nil)
	workerValue := any(nil)
	if done {
		status = string(BackfillStatusCompleted)
		finishedAt = now
	}
	row := s.db.QueryRowContext(ctx,
		`UPDATE backfill_runs
		    SET status = ?,
		        cursor_rowid = ?,
		        processed_items = processed_items + ?,
		        updated_items = updated_items + ?,
		        failed_items = failed_items + ?,
		        last_error = ?,
		        lease_until = ?,
		        worker_id = ?,
		        finished_at = CASE WHEN ? = '' THEN finished_at ELSE ? END,
		        updated_at = ?
		  WHERE id = ? AND status = ? AND worker_id = ? AND COALESCE(lease_until, '') > ?
		  RETURNING id, kind, status, organization_id, COALESCE(project_id, ''), COALESCE(release_version, ''),
		            COALESCE(debug_file_id, ''), COALESCE(started_after, ''), COALESCE(ended_before, ''), cursor_rowid,
		            total_items, processed_items, updated_items, failed_items,
		            COALESCE(requested_by_user_id, ''), COALESCE(requested_via, ''), COALESCE(worker_id, ''),
		            COALESCE(last_error, ''), created_at, COALESCE(started_at, ''), COALESCE(finished_at, ''),
		            updated_at, COALESCE(lease_until, '')`,
		status,
		cursorRowID,
		processed,
		updated,
		failed,
		lastError,
		leaseUntil,
		workerValue,
		finishedAt,
		finishedAt,
		now,
		runID,
		string(BackfillStatusRunning),
		workerID,
		now,
	)
	run, err := scanBackfillRun(row)
	if err == sql.ErrNoRows {
		return nil, ErrBackfillLeaseLost
	}
	if err != nil {
		return nil, fmt.Errorf("advance backfill run: %w", err)
	}
	return &run, nil
}

func (s *BackfillStore) MarkFailed(ctx context.Context, runID, workerID, lastError string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE backfill_runs
		    SET status = ?, last_error = ?, lease_until = NULL, worker_id = NULL, finished_at = ?, updated_at = ?
		  WHERE id = ? AND status = ? AND worker_id = ? AND COALESCE(lease_until, '') > ?`,
		string(BackfillStatusFailed),
		lastError,
		time.Now().UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
		runID,
		string(BackfillStatusRunning),
		workerID,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("mark backfill failed: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark backfill failed rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrBackfillLeaseLost
	}
	return nil
}

func (s *BackfillStore) insertRunIfNoConflict(ctx context.Context, run *BackfillRun) (bool, error) {
	if run == nil {
		return false, nil
	}
	query, args := backfillInsertQuery(run)
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("create backfill run rows affected: %w", err)
	}
	return rowsAffected > 0, nil
}

func (s *BackfillStore) findActiveDuplicate(ctx context.Context, in CreateBackfillRun) (*BackfillRun, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, kind, status, organization_id, COALESCE(project_id, ''), COALESCE(release_version, ''),
			        COALESCE(debug_file_id, ''), COALESCE(started_after, ''), COALESCE(ended_before, ''), cursor_rowid,
			        total_items, processed_items, updated_items, failed_items,
		        COALESCE(requested_by_user_id, ''), COALESCE(requested_via, ''), COALESCE(worker_id, ''),
		        COALESCE(last_error, ''), created_at, COALESCE(started_at, ''), COALESCE(finished_at, ''),
		        updated_at, COALESCE(lease_until, '')
		   FROM backfill_runs
		  WHERE kind = ? AND organization_id = ? AND COALESCE(project_id, '') = ? AND COALESCE(release_version, '') = ?
		    AND COALESCE(debug_file_id, '') = ? AND COALESCE(started_after, '') = ? AND COALESCE(ended_before, '') = ?
		    AND status IN (?, ?)
		  ORDER BY created_at DESC
		  LIMIT 1`,
		string(in.Kind),
		in.OrganizationID,
		in.ProjectID,
		in.ReleaseVersion,
		in.DebugFileID,
		timeValue(in.StartedAfter),
		timeValue(in.EndedBefore),
		string(BackfillStatusPending),
		string(BackfillStatusRunning),
	)
	run, err := scanBackfillRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find active duplicate backfill run: %w", err)
	}
	return &run, nil
}

func (s *BackfillStore) findActiveConflict(ctx context.Context, in CreateBackfillRun) (*BackfillRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, status, organization_id, COALESCE(project_id, ''), COALESCE(release_version, ''),
			        COALESCE(debug_file_id, ''), COALESCE(started_after, ''), COALESCE(ended_before, ''), cursor_rowid,
			        total_items, processed_items, updated_items, failed_items,
		        COALESCE(requested_by_user_id, ''), COALESCE(requested_via, ''), COALESCE(worker_id, ''),
		        COALESCE(last_error, ''), created_at, COALESCE(started_at, ''), COALESCE(finished_at, ''),
		        updated_at, COALESCE(lease_until, '')
		   FROM backfill_runs
		  WHERE kind = ? AND organization_id = ? AND status IN (?, ?)
		  ORDER BY created_at DESC, id DESC`,
		string(in.Kind),
		in.OrganizationID,
		string(BackfillStatusPending),
		string(BackfillStatusRunning),
	)
	if err != nil {
		return nil, fmt.Errorf("find active conflicting backfill run: %w", err)
	}
	defer rows.Close()
	scope := backfillScopeForCreate(in)
	for rows.Next() {
		run, err := scanBackfillRun(rows)
		if err != nil {
			return nil, err
		}
		if backfillExactScopeMatch(scope, backfillScopeForRun(run)) {
			continue
		}
		if backfillScopesConflict(scope, backfillScopeForRun(run)) {
			return &run, nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active conflicting backfill runs: %w", err)
	}
	return nil, nil
}

type backfillScope struct {
	Kind           BackfillKind
	OrganizationID string
	ProjectID      string
	ReleaseVersion string
	DebugFileID    string
	StartedAfter   time.Time
	EndedBefore    time.Time
}

func backfillScopeForCreate(in CreateBackfillRun) backfillScope {
	return backfillScope{
		Kind:           in.Kind,
		OrganizationID: strings.TrimSpace(in.OrganizationID),
		ProjectID:      strings.TrimSpace(in.ProjectID),
		ReleaseVersion: strings.TrimSpace(in.ReleaseVersion),
		DebugFileID:    strings.TrimSpace(in.DebugFileID),
		StartedAfter:   in.StartedAfter.UTC(),
		EndedBefore:    in.EndedBefore.UTC(),
	}
}

func backfillScopeForRun(run BackfillRun) backfillScope {
	return backfillScope{
		Kind:           run.Kind,
		OrganizationID: strings.TrimSpace(run.OrganizationID),
		ProjectID:      strings.TrimSpace(run.ProjectID),
		ReleaseVersion: strings.TrimSpace(run.ReleaseVersion),
		DebugFileID:    strings.TrimSpace(run.DebugFileID),
		StartedAfter:   run.StartedAfter.UTC(),
		EndedBefore:    run.EndedBefore.UTC(),
	}
}

func backfillExactScopeMatch(a, b backfillScope) bool {
	return a.Kind == b.Kind &&
		a.OrganizationID == b.OrganizationID &&
		a.ProjectID == b.ProjectID &&
		a.ReleaseVersion == b.ReleaseVersion &&
		a.DebugFileID == b.DebugFileID &&
		a.StartedAfter.Equal(b.StartedAfter) &&
		a.EndedBefore.Equal(b.EndedBefore)
}

func backfillScopesConflict(a, b backfillScope) bool {
	if a.Kind != b.Kind || a.OrganizationID != b.OrganizationID {
		return false
	}
	switch a.Kind {
	case BackfillKindTelemetryRebuild:
		return backfillProjectOverlap(a.ProjectID, b.ProjectID)
	case BackfillKindNativeReprocess:
		return backfillProjectOverlap(a.ProjectID, b.ProjectID) &&
			backfillReleaseOverlap(a.ReleaseVersion, b.ReleaseVersion) &&
			backfillTimeOverlap(a.StartedAfter, a.EndedBefore, b.StartedAfter, b.EndedBefore)
	default:
		return false
	}
}

func backfillProjectOverlap(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	return a == "" || b == "" || a == b
}

func backfillReleaseOverlap(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	return a == "" || b == "" || a == b
}

func backfillTimeOverlap(startA, endA, startB, endB time.Time) bool {
	if !startA.IsZero() && !endB.IsZero() && startA.After(endB) {
		return false
	}
	if !startB.IsZero() && !endA.IsZero() && startB.After(endA) {
		return false
	}
	return true
}

func backfillInsertQuery(run *BackfillRun) (string, []any) {
	baseArgs := []any{
		run.ID,
		string(run.Kind),
		string(run.Status),
		run.OrganizationID,
		nullIfEmpty(run.ProjectID),
		nullIfEmpty(run.ReleaseVersion),
		nullIfEmpty(run.DebugFileID),
		nullTimeValue(run.StartedAfter),
		nullTimeValue(run.EndedBefore),
		nullIfEmpty(run.RequestedByUserID),
		nullIfEmpty(run.RequestedVia),
		run.CreatedAt.Format(time.RFC3339),
		run.UpdatedAt.Format(time.RFC3339),
	}
	switch run.Kind {
	case BackfillKindTelemetryRebuild:
		query := `INSERT INTO backfill_runs
				(id, kind, status, organization_id, project_id, release_version, debug_file_id, started_after, ended_before,
				 cursor_rowid, total_items, processed_items, updated_items, failed_items,
				 requested_by_user_id, requested_via, last_error, created_at, updated_at)
			SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 0, 0, 0, ?, ?, '', ?, ?
			 WHERE NOT EXISTS (
			        SELECT 1
			          FROM backfill_runs active
			         WHERE active.kind = ?
			           AND active.organization_id = ?
			           AND active.status IN (?, ?)
			           AND (
			                COALESCE(active.project_id, '') = ''
			                OR ? = ''
			                OR COALESCE(active.project_id, '') = ?
			           )
			 )`
		args := append(baseArgs,
			string(BackfillKindTelemetryRebuild),
			run.OrganizationID,
			string(BackfillStatusPending),
			string(BackfillStatusRunning),
			run.ProjectID,
			run.ProjectID,
		)
		return query, args
	default:
		startedAfter := timeValue(run.StartedAfter)
		endedBefore := timeValue(run.EndedBefore)
		query := `INSERT INTO backfill_runs
				(id, kind, status, organization_id, project_id, release_version, debug_file_id, started_after, ended_before,
				 cursor_rowid, total_items, processed_items, updated_items, failed_items,
				 requested_by_user_id, requested_via, last_error, created_at, updated_at)
			SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 0, 0, 0, ?, ?, '', ?, ?
			 WHERE NOT EXISTS (
			        SELECT 1
			          FROM backfill_runs active
			         WHERE active.kind = ?
			           AND active.organization_id = ?
			           AND active.status IN (?, ?)
			           AND (
			                COALESCE(active.project_id, '') = ''
			                OR ? = ''
			                OR COALESCE(active.project_id, '') = ?
			           )
			           AND (
			                COALESCE(active.release_version, '') = ''
			                OR ? = ''
			                OR COALESCE(active.release_version, '') = ?
			           )
			           AND (
			                (COALESCE(active.started_after, '') = ''
			                 OR ? = ''
			                 OR COALESCE(active.started_after, '') <= ?)
			                AND
			                (? = ''
			                 OR COALESCE(active.ended_before, '') = ''
			                 OR ? <= COALESCE(active.ended_before, ''))
			           )
			 )`
		args := append(baseArgs,
			string(BackfillKindNativeReprocess),
			run.OrganizationID,
			string(BackfillStatusPending),
			string(BackfillStatusRunning),
			run.ProjectID,
			run.ProjectID,
			run.ReleaseVersion,
			run.ReleaseVersion,
			endedBefore,
			endedBefore,
			startedAfter,
			startedAfter,
		)
		return query, args
	}
}

func scanBackfillRun(scanner interface {
	Scan(dest ...any) error
}) (BackfillRun, error) {
	var (
		run                                     BackfillRun
		kind, status                            string
		projectID, releaseVersion, debugFileID  sql.NullString
		startedAfter, endedBefore               sql.NullString
		requestedByUserID, requestedVia, worker sql.NullString
		lastError, createdAt, startedAt         sql.NullString
		finishedAt, updatedAt, leaseUntil       sql.NullString
	)
	err := scanner.Scan(
		&run.ID,
		&kind,
		&status,
		&run.OrganizationID,
		&projectID,
		&releaseVersion,
		&debugFileID,
		&startedAfter,
		&endedBefore,
		&run.CursorRowID,
		&run.TotalItems,
		&run.ProcessedItems,
		&run.UpdatedItems,
		&run.FailedItems,
		&requestedByUserID,
		&requestedVia,
		&worker,
		&lastError,
		&createdAt,
		&startedAt,
		&finishedAt,
		&updatedAt,
		&leaseUntil,
	)
	if err != nil {
		return BackfillRun{}, err
	}
	run.Kind = BackfillKind(kind)
	run.Status = BackfillStatus(status)
	run.ProjectID = sqlutil.NullStr(projectID)
	run.ReleaseVersion = sqlutil.NullStr(releaseVersion)
	run.DebugFileID = sqlutil.NullStr(debugFileID)
	run.StartedAfter = sqlutil.ParseDBTime(sqlutil.NullStr(startedAfter))
	run.EndedBefore = sqlutil.ParseDBTime(sqlutil.NullStr(endedBefore))
	run.RequestedByUserID = sqlutil.NullStr(requestedByUserID)
	run.RequestedVia = sqlutil.NullStr(requestedVia)
	run.WorkerID = sqlutil.NullStr(worker)
	run.LastError = sqlutil.NullStr(lastError)
	run.CreatedAt = sqlutil.ParseDBTime(sqlutil.NullStr(createdAt))
	run.StartedAt = sqlutil.ParseDBTime(sqlutil.NullStr(startedAt))
	run.FinishedAt = sqlutil.ParseDBTime(sqlutil.NullStr(finishedAt))
	run.UpdatedAt = sqlutil.ParseDBTime(sqlutil.NullStr(updatedAt))
	run.LeaseUntil = sqlutil.ParseDBTime(sqlutil.NullStr(leaseUntil))
	return run, nil
}

func timeValue(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func nullTimeValue(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339)
}
