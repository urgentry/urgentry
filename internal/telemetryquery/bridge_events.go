package telemetryquery

import (
	"context"
	"fmt"

	"urgentry/internal/discovershared"
	"urgentry/internal/sqlutil"
	"urgentry/internal/store"
	"urgentry/internal/telemetrybridge"
)

func (s *bridgeService) ListProjectEvents(ctx context.Context, projectID string, limit, offset int) ([]store.WebEvent, error) {
	scope, err := s.projectScope(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureSurfaceFresh(ctx, QuerySurfaceEvents, scope, telemetrybridge.FamilyEvents); err != nil {
		return nil, err
	}
	rows, err := s.bridgeDB.QueryContext(ctx, `
		SELECT event_id, COALESCE(group_id, ''), COALESCE(title, ''), COALESCE(title, ''), COALESCE(level, ''),
		       COALESCE(platform, ''), COALESCE(culprit, ''), occurred_at, COALESCE(tags_json::text, '{}')
		  FROM telemetry.event_facts
		 WHERE project_id = $1
		 ORDER BY ingested_at DESC, event_id DESC
		 LIMIT $2 OFFSET $3`,
		projectID,
		discovershared.Clamp(limit, 1, 100),
		intMax(offset, 0),
	)
	if err != nil {
		return nil, fmt.Errorf("list bridge project events: %w", err)
	}
	defer rows.Close()

	items := make([]store.WebEvent, 0, limit)
	for rows.Next() {
		var item store.WebEvent
		var groupID, title, message, level, platform, culprit, tagsJSON string
		if err := rows.Scan(&item.EventID, &groupID, &title, &message, &level, &platform, &culprit, &item.Timestamp, &tagsJSON); err != nil {
			return nil, fmt.Errorf("scan bridge project event: %w", err)
		}
		item.GroupID = groupID
		item.Title = title
		item.Message = message
		item.Level = level
		item.Platform = platform
		item.Culprit = culprit
		item.Tags = sqlutil.ParseTags(tagsJSON)
		item.ProcessingStatus = store.EventProcessingStatusCompleted
		items = append(items, item)
	}
	return items, rows.Err()
}

func intMax(a, b int) int {
	if a > b {
		return a
	}
	return b
}
