package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"urgentry/internal/store"
)

func (s *ReplayStore) ListReplays(ctx context.Context, projectID string, limit int) ([]store.ReplayManifest, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, event_row_id, project_id, replay_id, platform, release, environment,
		       COALESCE(started_at, ''), COALESCE(ended_at, ''), duration_ms, request_url,
		       user_ref_json, trace_ids_json, linked_event_ids_json, linked_issue_ids_json,
		       asset_count, console_count, network_count, click_count, navigation_count, error_marker_count,
		       timeline_start_ms, timeline_end_ms, privacy_policy_version, processing_status, ingest_error,
		       COALESCE(created_at, ''), COALESCE(updated_at, '')
		  FROM replay_manifests
		 WHERE project_id = ?
		 ORDER BY COALESCE(started_at, created_at) DESC, created_at DESC
		 LIMIT ?`,
		projectID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list replay manifests: %w", err)
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

func (s *ReplayStore) GetReplay(ctx context.Context, projectID, replayID string) (*store.ReplayRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, event_row_id, project_id, replay_id, platform, release, environment,
		       COALESCE(started_at, ''), COALESCE(ended_at, ''), duration_ms, request_url,
		       user_ref_json, trace_ids_json, linked_event_ids_json, linked_issue_ids_json,
		       asset_count, console_count, network_count, click_count, navigation_count, error_marker_count,
		       timeline_start_ms, timeline_end_ms, privacy_policy_version, processing_status, ingest_error,
		       COALESCE(created_at, ''), COALESCE(updated_at, '')
		  FROM replay_manifests
		 WHERE project_id = ? AND replay_id = ?`,
		projectID, replayID,
	)
	manifest, err := scanReplayManifest(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	assets, err := s.listReplayAssetRefs(ctx, manifest.ID)
	if err != nil {
		return nil, err
	}
	timeline, err := s.listReplayTimelineByManifest(ctx, manifest.ID, store.ReplayTimelineFilter{Limit: 500})
	if err != nil {
		return nil, err
	}
	record := &store.ReplayRecord{
		Manifest: manifest,
		Assets:   assets,
		Timeline: timeline,
	}
	var payload string
	err = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(payload_json, '') FROM events WHERE id = ?`,
		manifest.EventRowID,
	).Scan(&payload)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("load replay payload: %w", err)
	}
	if payload != "" {
		record.Payload = json.RawMessage(payload)
	}
	return record, nil
}

func (s *ReplayStore) ListReplayTimeline(ctx context.Context, projectID, replayID string, filter store.ReplayTimelineFilter) ([]store.ReplayTimelineItem, error) {
	manifest, err := s.lookupReplayManifest(ctx, projectID, replayID)
	if err != nil {
		return nil, err
	}
	return s.listReplayTimelineByManifest(ctx, manifest.ID, filter)
}

func (s *ReplayStore) lookupReplayManifest(ctx context.Context, projectID, replayID string) (*store.ReplayManifest, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, event_row_id, project_id, replay_id, platform, release, environment,
		       COALESCE(started_at, ''), COALESCE(ended_at, ''), duration_ms, request_url,
		       user_ref_json, trace_ids_json, linked_event_ids_json, linked_issue_ids_json,
		       asset_count, console_count, network_count, click_count, navigation_count, error_marker_count,
		       timeline_start_ms, timeline_end_ms, privacy_policy_version, processing_status, ingest_error,
		       COALESCE(created_at, ''), COALESCE(updated_at, '')
		  FROM replay_manifests
		 WHERE project_id = ? AND replay_id = ?`,
		projectID, replayID,
	)
	manifest, err := scanReplayManifest(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return &manifest, nil
}

func (s *ReplayStore) listReplayAssetRefs(ctx context.Context, manifestID string) ([]store.ReplayAssetRef, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, replay_id, attachment_id, kind, name, content_type, size_bytes, object_key, chunk_index, COALESCE(created_at, '')
		  FROM replay_assets
		 WHERE manifest_id = ?
		 ORDER BY chunk_index ASC, created_at ASC, name ASC`,
		manifestID,
	)
	if err != nil {
		return nil, fmt.Errorf("list replay asset refs: %w", err)
	}
	defer rows.Close()
	var assets []store.ReplayAssetRef
	for rows.Next() {
		var item store.ReplayAssetRef
		var createdAt string
		if err := rows.Scan(&item.ID, &item.ReplayID, &item.AttachmentID, &item.Kind, &item.Name, &item.ContentType, &item.SizeBytes, &item.ObjectKey, &item.ChunkIndex, &createdAt); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTime(createdAt)
		assets = append(assets, item)
	}
	return assets, rows.Err()
}

func (s *ReplayStore) listReplayTimelineByManifest(ctx context.Context, manifestID string, filter store.ReplayTimelineFilter) ([]store.ReplayTimelineItem, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 500
	}
	query := `
		SELECT id, replay_id, ts_ms, item_index, kind, pane_ref, title, level, message, url, method,
		       status_code, duration_ms, selector, text_value, trace_id, linked_event_id, linked_issue_id,
		       payload_ref, meta_json
		  FROM replay_timeline_items
		 WHERE manifest_id = ?`
	args := []any{manifestID}
	if filter.Pane != "" {
		query += ` AND pane_ref = ?`
		args = append(args, filter.Pane)
	}
	if filter.Kind != "" {
		query += ` AND kind = ?`
		args = append(args, filter.Kind)
	}
	if filter.StartMS > 0 {
		query += ` AND ts_ms >= ?`
		args = append(args, filter.StartMS)
	}
	if filter.EndMS > 0 {
		query += ` AND ts_ms <= ?`
		args = append(args, filter.EndMS)
	}
	if filter.EventID != "" {
		query += ` AND linked_event_id = ?`
		args = append(args, filter.EventID)
	}
	if filter.TraceID != "" {
		query += ` AND trace_id = ?`
		args = append(args, filter.TraceID)
	}
	if filter.IssueID != "" {
		query += ` AND linked_issue_id = ?`
		args = append(args, filter.IssueID)
	}
	if filter.Search != "" {
		query += ` AND (title LIKE ? OR message LIKE ? OR url LIKE ? OR text_value LIKE ?)`
		term := "%" + strings.TrimSpace(filter.Search) + "%"
		args = append(args, term, term, term, term)
	}
	query += ` ORDER BY ts_ms ASC, item_index ASC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list replay timeline: %w", err)
	}
	defer rows.Close()
	var items []store.ReplayTimelineItem
	for rows.Next() {
		var item store.ReplayTimelineItem
		var meta string
		if err := rows.Scan(&item.ID, &item.ReplayID, &item.TSMS, &item.ItemIndex, &item.Kind, &item.Pane, &item.Title, &item.Level,
			&item.Message, &item.URL, &item.Method, &item.StatusCode, &item.DurationMS, &item.Selector, &item.Text,
			&item.TraceID, &item.LinkedEventID, &item.LinkedIssueID, &item.PayloadRef, &meta); err != nil {
			return nil, err
		}
		if meta != "" {
			item.MetaJSON = json.RawMessage(meta)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanReplayManifest(row interface{ Scan(dest ...any) error }) (store.ReplayManifest, error) {
	var item store.ReplayManifest
	var startedAt, endedAt, createdAt, updatedAt string
	var userJSON, traceIDsJSON, linkedEventsJSON, linkedIssuesJSON string
	err := row.Scan(
		&item.ID, &item.EventRowID, &item.ProjectID, &item.ReplayID, &item.Platform, &item.Release, &item.Environment,
		&startedAt, &endedAt, &item.DurationMS, &item.RequestURL, &userJSON, &traceIDsJSON, &linkedEventsJSON, &linkedIssuesJSON,
		&item.AssetCount, &item.ConsoleCount, &item.NetworkCount, &item.ClickCount, &item.NavigationCount, &item.ErrorMarkerCount,
		&item.TimelineStartMS, &item.TimelineEndMS, &item.PrivacyPolicyVersion, &item.ProcessingStatus, &item.IngestError,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return store.ReplayManifest{}, err
	}
	item.StartedAt = parseTime(startedAt)
	item.EndedAt = parseTime(endedAt)
	item.CreatedAt = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)
	_ = json.Unmarshal([]byte(userJSON), &item.UserRef)
	_ = json.Unmarshal([]byte(traceIDsJSON), &item.TraceIDs)
	_ = json.Unmarshal([]byte(linkedEventsJSON), &item.LinkedEventIDs)
	_ = json.Unmarshal([]byte(linkedIssuesJSON), &item.LinkedIssueIDs)
	return item, nil
}

func scanReplayManifestRows(rows *sql.Rows) (store.ReplayManifest, error) {
	return scanReplayManifest(rows)
}
