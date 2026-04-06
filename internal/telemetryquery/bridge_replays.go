package telemetryquery

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"urgentry/internal/discovershared"
	"urgentry/internal/sqlutil"
	"urgentry/internal/store"
	"urgentry/internal/telemetrybridge"
)

func (s *bridgeService) ListReplays(ctx context.Context, projectID string, limit int) ([]store.ReplayManifest, error) {
	scope, err := s.projectScope(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureSurfaceFresh(ctx, QuerySurfaceReplays, scope, telemetrybridge.FamilyReplays); err != nil {
		return nil, err
	}
	rows, err := s.bridgeDB.QueryContext(ctx, `
		SELECT id, project_id, replay_id, COALESCE(event_id, ''), COALESCE(trace_id, ''), COALESCE(release, ''),
		       COALESCE(environment, ''), started_at, finished_at, duration_ms, segment_count, error_count,
		       click_count, COALESCE(payload_json::text, '{}')
		  FROM telemetry.replay_manifests
		 WHERE project_id = $1
		 ORDER BY started_at DESC
		 LIMIT $2`,
		projectID, discovershared.Clamp(limit, 1, 100),
	)
	if err != nil {
		return nil, fmt.Errorf("list bridge replays: %w", err)
	}
	defer rows.Close()
	return scanBridgeReplayManifests(rows, limit)
}

func (s *bridgeService) ListOrgReplays(ctx context.Context, orgID string, limit int) ([]store.ReplayManifest, error) {
	scope := telemetrybridge.Scope{OrganizationID: orgID}
	if err := s.ensureSurfaceFresh(ctx, QuerySurfaceReplays, scope, telemetrybridge.FamilyReplays); err != nil {
		return nil, err
	}
	rows, err := s.bridgeDB.QueryContext(ctx, `
		SELECT id, project_id, replay_id, COALESCE(event_id, ''), COALESCE(trace_id, ''), COALESCE(release, ''),
		       COALESCE(environment, ''), started_at, finished_at, duration_ms, segment_count, error_count,
		       click_count, COALESCE(payload_json::text, '{}')
		  FROM telemetry.replay_manifests
		 WHERE organization_id = $1
		 ORDER BY started_at DESC
		 LIMIT $2`,
		orgID, discovershared.Clamp(limit, 1, 100),
	)
	if err != nil {
		return nil, fmt.Errorf("list bridge org replays: %w", err)
	}
	defer rows.Close()
	return scanBridgeReplayManifests(rows, limit)
}

func scanBridgeReplayManifests(rows *sql.Rows, limit int) ([]store.ReplayManifest, error) {
	items := make([]store.ReplayManifest, 0, discovershared.Clamp(limit, 1, 100))
	for rows.Next() {
		var (
			item                                                store.ReplayManifest
			eventID, traceID, release, environment, payloadJSON string
			durationMS                                          float64
			segmentCount, errorCount, clickCount                int
			startedAt, finishedAt                               sql.NullTime
		)
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.ReplayID, &eventID, &traceID, &release, &environment, &startedAt, &finishedAt, &durationMS, &segmentCount, &errorCount, &clickCount, &payloadJSON); err != nil {
			return nil, fmt.Errorf("scan bridge replay manifest: %w", err)
		}
		item.EventRowID = eventID
		item.Release = release
		item.Environment = environment
		if startedAt.Valid {
			item.StartedAt = startedAt.Time.UTC()
		}
		if finishedAt.Valid {
			item.EndedAt = finishedAt.Time.UTC()
		}
		item.DurationMS = int64(durationMS)
		item.AssetCount = segmentCount
		item.ErrorMarkerCount = errorCount
		item.ClickCount = clickCount
		applyReplayPayload(&item, []byte(payloadJSON))
		if traceID != "" && len(item.TraceIDs) == 0 {
			item.TraceIDs = []string{traceID}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *bridgeService) GetReplay(ctx context.Context, projectID, replayID string) (*store.ReplayRecord, error) {
	scope, err := s.projectScope(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureSurfaceFresh(ctx, QuerySurfaceReplays, scope, telemetrybridge.FamilyReplays, telemetrybridge.FamilyReplayTimeline); err != nil {
		return nil, err
	}
	row := s.bridgeDB.QueryRowContext(ctx, `
		SELECT id, project_id, replay_id, COALESCE(event_id, ''), COALESCE(trace_id, ''), COALESCE(release, ''),
		       COALESCE(environment, ''), started_at, finished_at, duration_ms, segment_count, error_count,
		       click_count, COALESCE(payload_json::text, '{}')
		  FROM telemetry.replay_manifests
		 WHERE project_id = $1 AND replay_id = $2`,
		projectID, replayID,
	)
	var (
		manifest                                            store.ReplayManifest
		eventID, traceID, release, environment, payloadJSON string
		durationMS                                          float64
		segmentCount, errorCount, clickCount                int
		startedAt, finishedAt                               sql.NullTime
	)
	if err := row.Scan(&manifest.ID, &manifest.ProjectID, &manifest.ReplayID, &eventID, &traceID, &release, &environment, &startedAt, &finishedAt, &durationMS, &segmentCount, &errorCount, &clickCount, &payloadJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("load bridge replay manifest: %w", err)
	}
	manifest.EventRowID = eventID
	manifest.Release = release
	manifest.Environment = environment
	if startedAt.Valid {
		manifest.StartedAt = startedAt.Time.UTC()
	}
	if finishedAt.Valid {
		manifest.EndedAt = finishedAt.Time.UTC()
	}
	manifest.DurationMS = int64(durationMS)
	manifest.AssetCount = segmentCount
	manifest.ErrorMarkerCount = errorCount
	manifest.ClickCount = clickCount
	applyReplayPayload(&manifest, []byte(payloadJSON))
	if traceID != "" && len(manifest.TraceIDs) == 0 {
		manifest.TraceIDs = []string{traceID}
	}
	timeline, err := s.listReplayTimeline(ctx, projectID, replayID, store.ReplayTimelineFilter{Limit: 500})
	if err != nil {
		return nil, err
	}
	assets, err := s.loadReplayAssets(ctx, manifest.EventRowID, replayID)
	if err != nil {
		return nil, err
	}
	return &store.ReplayRecord{Manifest: manifest, Timeline: timeline, Assets: assets}, nil
}

func (s *bridgeService) ListReplayTimeline(ctx context.Context, projectID, replayID string, filter store.ReplayTimelineFilter) ([]store.ReplayTimelineItem, error) {
	scope, err := s.projectScope(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureSurfaceFresh(ctx, QuerySurfaceReplays, scope, telemetrybridge.FamilyReplayTimeline); err != nil {
		return nil, err
	}
	return s.listReplayTimeline(ctx, projectID, replayID, filter)
}

func (s *bridgeService) listReplayTimeline(ctx context.Context, projectID, replayID string, filter store.ReplayTimelineFilter) ([]store.ReplayTimelineItem, error) {
	query := `
		SELECT id, replay_id, offset_ms, kind, COALESCE(lane, ''), COALESCE(payload_json::text, '{}')
		  FROM telemetry.replay_timeline_items
		 WHERE project_id = $1 AND replay_id = $2`
	args := []any{projectID, replayID}
	next := 3
	if filter.Pane != "" {
		query += fmt.Sprintf(` AND COALESCE(lane, '') = $%d`, next)
		args = append(args, filter.Pane)
		next++
	}
	if filter.Kind != "" {
		query += fmt.Sprintf(` AND kind = $%d`, next)
		args = append(args, filter.Kind)
		next++
	}
	if filter.StartMS > 0 {
		query += fmt.Sprintf(` AND offset_ms >= $%d`, next)
		args = append(args, filter.StartMS)
		next++
	}
	if filter.EndMS > 0 {
		query += fmt.Sprintf(` AND offset_ms <= $%d`, next)
		args = append(args, filter.EndMS)
		next++
	}
	if filter.EventID != "" {
		query += fmt.Sprintf(` AND COALESCE(payload_json->>'linked_event_id', '') = $%d`, next)
		args = append(args, filter.EventID)
		next++
	}
	if filter.TraceID != "" {
		query += fmt.Sprintf(` AND COALESCE(payload_json->>'trace_id', '') = $%d`, next)
		args = append(args, filter.TraceID)
		next++
	}
	if filter.IssueID != "" {
		query += fmt.Sprintf(` AND COALESCE(payload_json->>'linked_issue_id', '') = $%d`, next)
		args = append(args, filter.IssueID)
		next++
	}
	if filter.Search != "" {
		query += fmt.Sprintf(` AND (
			COALESCE(payload_json->>'title', '') ILIKE $%d OR
			COALESCE(payload_json->>'message', '') ILIKE $%d OR
			COALESCE(payload_json->>'url', '') ILIKE $%d OR
			COALESCE(payload_json->>'text', '') ILIKE $%d
		)`, next, next, next, next)
		args = append(args, "%"+strings.TrimSpace(filter.Search)+"%")
		next++
	}
	query += ` ORDER BY offset_ms ASC, id ASC LIMIT `
	query += fmt.Sprintf(`$%d`, next)
	args = append(args, discovershared.Clamp(filter.Limit, 1, 500))
	rows, err := s.bridgeDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list bridge replay timeline: %w", err)
	}
	defer rows.Close()
	var items []store.ReplayTimelineItem
	for rows.Next() {
		var (
			item     store.ReplayTimelineItem
			offsetMS int64
			payload  string
		)
		if err := rows.Scan(&item.ID, &item.ReplayID, &offsetMS, &item.Kind, &item.Pane, &payload); err != nil {
			return nil, fmt.Errorf("scan bridge replay timeline: %w", err)
		}
		item.TSMS = offsetMS
		applyReplayTimelinePayload(&item, []byte(payload))
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *bridgeService) loadReplayAssets(ctx context.Context, eventID, replayID string) ([]store.ReplayAssetRef, error) {
	if strings.TrimSpace(eventID) == "" {
		return nil, nil
	}
	rows, err := s.sourceDB.QueryContext(ctx, `
		SELECT id, name, COALESCE(content_type, ''), size_bytes, object_key, COALESCE(created_at, '')
		  FROM event_attachments
		 WHERE event_id = ?
		 ORDER BY created_at ASC, id ASC`,
		eventID,
	)
	if err != nil {
		return nil, fmt.Errorf("list replay assets: %w", err)
	}
	defer rows.Close()
	var items []store.ReplayAssetRef
	for rows.Next() {
		var item store.ReplayAssetRef
		var createdAt string
		if err := rows.Scan(&item.AttachmentID, &item.Name, &item.ContentType, &item.SizeBytes, &item.ObjectKey, &createdAt); err != nil {
			return nil, fmt.Errorf("scan replay asset: %w", err)
		}
		item.ID = item.AttachmentID
		item.ReplayID = replayID
		item.Kind = replayAssetKind(item.Name, item.ContentType)
		item.ChunkIndex = replayChunkIndex(item.Name)
		item.CreatedAt = sqlutil.ParseDBTime(createdAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func applyReplayPayload(item *store.ReplayManifest, raw []byte) {
	if item == nil || len(raw) == 0 {
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return
	}
	item.Platform = firstJSONString(payload["platform"])
	item.RequestURL = nestedJSONString(payload, "request", "url")
	item.PrivacyPolicyVersion = firstJSONString(payload["privacy_policy_version"])
	item.UserRef = store.ReplayUserRef{
		ID:       nestedJSONString(payload, "user", "id"),
		Email:    nestedJSONString(payload, "user", "email"),
		Username: nestedJSONString(payload, "user", "username"),
	}
	for _, traceID := range extractStringSlice(nestedJSONObject(payload, "contexts", "trace"), "trace_id") {
		if !slices.Contains(item.TraceIDs, traceID) {
			item.TraceIDs = append(item.TraceIDs, traceID)
		}
	}
	if traceID := nestedJSONString(payload, "contexts", "trace", "trace_id"); traceID != "" && !slices.Contains(item.TraceIDs, traceID) {
		item.TraceIDs = append(item.TraceIDs, traceID)
	}
}

func applyReplayTimelinePayload(item *store.ReplayTimelineItem, raw []byte) {
	if item == nil || len(raw) == 0 {
		return
	}
	var payload struct {
		Title         string          `json:"title"`
		Level         string          `json:"level"`
		Message       string          `json:"message"`
		URL           string          `json:"url"`
		Method        string          `json:"method"`
		StatusCode    int             `json:"status_code"`
		DurationMS    int64           `json:"duration_ms"`
		Selector      string          `json:"selector"`
		Text          string          `json:"text"`
		TraceID       string          `json:"trace_id"`
		LinkedEventID string          `json:"linked_event_id"`
		LinkedIssueID string          `json:"linked_issue_id"`
		PayloadRef    string          `json:"payload_ref"`
		Meta          json.RawMessage `json:"meta"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return
	}
	item.Title = payload.Title
	item.Level = payload.Level
	item.Message = payload.Message
	item.URL = payload.URL
	item.Method = payload.Method
	item.StatusCode = payload.StatusCode
	item.DurationMS = payload.DurationMS
	item.Selector = payload.Selector
	item.Text = payload.Text
	item.TraceID = payload.TraceID
	item.LinkedEventID = payload.LinkedEventID
	item.LinkedIssueID = payload.LinkedIssueID
	item.PayloadRef = payload.PayloadRef
	item.MetaJSON = payload.Meta
}

func firstJSONString(raw any) string {
	value, _ := raw.(string)
	return strings.TrimSpace(value)
}

func replayAssetKind(name, contentType string) string {
	lowerName := strings.ToLower(strings.TrimSpace(name))
	lowerType := strings.ToLower(strings.TrimSpace(contentType))
	switch {
	case strings.Contains(lowerName, "snapshot"):
		return "snapshot"
	case strings.Contains(lowerName, "video") || strings.Contains(lowerType, "video/"):
		return "video"
	case strings.Contains(lowerName, "recording") || strings.Contains(lowerName, ".rrweb") || strings.Contains(lowerType, "json"):
		return "recording"
	default:
		return "asset"
	}
}

func replayChunkIndex(name string) int {
	lower := strings.ToLower(name)
	for _, marker := range []string{"segment-", "chunk-", "part-"} {
		idx := strings.Index(lower, marker)
		if idx < 0 {
			continue
		}
		start := idx + len(marker)
		end := start
		for end < len(lower) && lower[end] >= '0' && lower[end] <= '9' {
			end++
		}
		if value, err := strconv.Atoi(lower[start:end]); err == nil {
			return value
		}
	}
	return 0
}

func nestedJSONObject(payload map[string]any, keys ...string) map[string]any {
	current := payload
	for _, key := range keys {
		next, _ := current[key].(map[string]any)
		if next == nil {
			return nil
		}
		current = next
	}
	return current
}

func nestedJSONString(payload map[string]any, keys ...string) string {
	if len(keys) == 0 {
		return ""
	}
	obj := payload
	for _, key := range keys[:len(keys)-1] {
		next, _ := obj[key].(map[string]any)
		if next == nil {
			return ""
		}
		obj = next
	}
	return firstJSONString(obj[keys[len(keys)-1]])
}

func extractStringSlice(payload map[string]any, key string) []string {
	if payload == nil {
		return nil
	}
	switch raw := payload[key].(type) {
	case []any:
		items := make([]string, 0, len(raw))
		for _, item := range raw {
			if value := firstJSONString(item); value != "" {
				items = append(items, value)
			}
		}
		return items
	case string:
		if value := strings.TrimSpace(raw); value != "" {
			return []string{value}
		}
	}
	return nil
}
