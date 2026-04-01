package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"urgentry/internal/store"
)

// ReplayDeletionJob tracks a batch replay deletion request.
type ReplayDeletionJob struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	ReplayIDs []string  `json:"replayIds"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"dateCreated"`
	UpdatedAt time.Time `json:"dateUpdated"`
}

// DeleteReplay removes a replay manifest and its associated timeline items
// and asset references.
func (s *ReplayStore) DeleteReplay(ctx context.Context, projectID, replayID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Look up the manifest row ID.
	var manifestID string
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM replay_manifests WHERE project_id = ? AND replay_id = ?`,
		projectID, replayID,
	).Scan(&manifestID)
	if err != nil {
		_ = tx.Rollback()
		if err == sql.ErrNoRows {
			return nil // already deleted
		}
		return err
	}
	// Delete timeline items.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM replay_timeline_items WHERE manifest_id = ?`, manifestID,
	); err != nil {
		_ = tx.Rollback()
		return err
	}
	// Delete asset references.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM replay_assets WHERE manifest_id = ?`, manifestID,
	); err != nil {
		_ = tx.Rollback()
		return err
	}
	// Delete manifest.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM replay_manifests WHERE id = ?`, manifestID,
	); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// CreateDeletionJob creates a replay deletion job.
func (s *ReplayStore) CreateDeletionJob(ctx context.Context, projectID string, replayIDs []string) (*ReplayDeletionJob, error) {
	id := generateID()
	now := time.Now().UTC()
	idsJSON, _ := json.Marshal(replayIDs)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO replay_deletion_jobs (id, project_id, replay_ids_json, status, created_at, updated_at)
		 VALUES (?, ?, ?, 'pending', ?, ?)`,
		id, projectID, string(idsJSON), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, err
	}
	return &ReplayDeletionJob{
		ID:        id,
		ProjectID: projectID,
		ReplayIDs: replayIDs,
		Status:    "pending",
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// GetDeletionJob returns a deletion job by ID.
func (s *ReplayStore) GetDeletionJob(ctx context.Context, projectID, jobID string) (*ReplayDeletionJob, error) {
	var job ReplayDeletionJob
	var idsJSON, createdAt, updatedAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, replay_ids_json, status, created_at, updated_at
		 FROM replay_deletion_jobs
		 WHERE project_id = ? AND id = ?`,
		projectID, jobID,
	).Scan(&job.ID, &job.ProjectID, &idsJSON, &job.Status, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(idsJSON), &job.ReplayIDs)
	job.CreatedAt = parseTime(createdAt)
	job.UpdatedAt = parseTime(updatedAt)
	return &job, nil
}

// ListDeletionJobs returns deletion jobs for a project.
func (s *ReplayStore) ListDeletionJobs(ctx context.Context, projectID string, limit int) ([]ReplayDeletionJob, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, replay_ids_json, status, created_at, updated_at
		 FROM replay_deletion_jobs
		 WHERE project_id = ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		projectID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []ReplayDeletionJob
	for rows.Next() {
		var job ReplayDeletionJob
		var idsJSON, createdAt, updatedAt string
		if err := rows.Scan(&job.ID, &job.ProjectID, &idsJSON, &job.Status, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(idsJSON), &job.ReplayIDs)
		job.CreatedAt = parseTime(createdAt)
		job.UpdatedAt = parseTime(updatedAt)
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// ListOrgReplays returns replays across all projects in an organization.
func (s *ReplayStore) ListOrgReplays(ctx context.Context, orgID string, limit int) ([]store.ReplayManifest, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT rm.id, rm.event_row_id, rm.project_id, rm.replay_id, rm.platform, rm.release, rm.environment,
		       COALESCE(rm.started_at, ''), COALESCE(rm.ended_at, ''), rm.duration_ms, rm.request_url,
		       rm.user_ref_json, rm.trace_ids_json, rm.linked_event_ids_json, rm.linked_issue_ids_json,
		       rm.asset_count, rm.console_count, rm.network_count, rm.click_count, rm.navigation_count, rm.error_marker_count,
		       rm.timeline_start_ms, rm.timeline_end_ms, rm.privacy_policy_version, rm.processing_status, rm.ingest_error,
		       COALESCE(rm.created_at, ''), COALESCE(rm.updated_at, '')
		  FROM replay_manifests rm
		  JOIN projects p ON p.id = rm.project_id
		 WHERE p.organization_id = ?
		 ORDER BY COALESCE(rm.started_at, rm.created_at) DESC, rm.created_at DESC
		 LIMIT ?`,
		orgID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var manifests []store.ReplayManifest
	for rows.Next() {
		item, err := scanReplayManifestRows(rows)
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, item)
	}
	return manifests, rows.Err()
}

// CountOrgReplays returns the count of replays per project in the organization.
func (s *ReplayStore) CountOrgReplays(ctx context.Context, orgID string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT rm.project_id, COUNT(*)
		  FROM replay_manifests rm
		  JOIN projects p ON p.id = rm.project_id
		 WHERE p.organization_id = ?
		 GROUP BY rm.project_id`,
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make(map[string]int)
	for rows.Next() {
		var projectID string
		var count int
		if err := rows.Scan(&projectID, &count); err != nil {
			return nil, err
		}
		counts[projectID] = count
	}
	return counts, rows.Err()
}

// ListReplayClicks returns click timeline items for a replay.
func (s *ReplayStore) ListReplayClicks(ctx context.Context, projectID, replayID string, limit int) ([]store.ReplayTimelineItem, error) {
	return s.ListReplayTimeline(ctx, projectID, replayID, store.ReplayTimelineFilter{
		Kind:  "click",
		Limit: limit,
	})
}

// ListReplayRecordingSegments returns the recording segment assets for a replay.
func (s *ReplayStore) ListReplayRecordingSegments(ctx context.Context, projectID, replayID string) ([]store.ReplayAssetRef, error) {
	manifest, err := s.lookupReplayManifest(ctx, projectID, replayID)
	if err != nil {
		return nil, err
	}
	assets, err := s.listReplayAssetRefs(ctx, manifest.ID)
	if err != nil {
		return nil, err
	}
	// All assets are recording segments in this context.
	segments := append([]store.ReplayAssetRef{}, assets...)
	return segments, nil
}

// ListReplaySelectors returns distinct CSS selectors from replay click events.
func (s *ReplayStore) ListReplaySelectors(ctx context.Context, orgID string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT rti.selector
		  FROM replay_timeline_items rti
		  JOIN replay_manifests rm ON rm.id = rti.manifest_id
		  JOIN projects p ON p.id = rm.project_id
		 WHERE p.organization_id = ?
		   AND rti.kind = 'click'
		   AND rti.selector != ''
		 ORDER BY rti.selector ASC
		 LIMIT ?`,
		orgID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var selectors []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		selectors = append(selectors, s)
	}
	return selectors, rows.Err()
}
