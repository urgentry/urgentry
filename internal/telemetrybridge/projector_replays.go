package telemetrybridge

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

func projectorReplaysFamily() projectorFamily {
	return projectorFamily{
		sync: (*Projector).syncReplays,
		count: func(p *Projector, ctx context.Context, scope Scope) (int, error) {
			return p.countSourceRows(ctx, FamilyReplays, `SELECT COUNT(*) FROM replay_manifests rm JOIN projects p ON p.id = rm.project_id WHERE p.organization_id = ? AND (? = '' OR rm.project_id = ?)`, scope.OrganizationID, scope.ProjectID, scope.ProjectID)
		},
		clear: func(p *Projector, ctx context.Context, scope Scope) error {
			return p.deleteBridgeRows(ctx, FamilyReplays, `DELETE FROM telemetry.replay_manifests WHERE `, scope)
		},
	}
}

func projectorReplayTimelineFamily() projectorFamily {
	return projectorFamily{
		sync: (*Projector).syncReplayTimeline,
		count: func(p *Projector, ctx context.Context, scope Scope) (int, error) {
			return p.countSourceRows(ctx, FamilyReplayTimeline, `SELECT COUNT(*) FROM replay_timeline_items rti JOIN replay_manifests rm ON rm.id = rti.manifest_id JOIN projects p ON p.id = rm.project_id WHERE p.organization_id = ? AND (? = '' OR rm.project_id = ?)`, scope.OrganizationID, scope.ProjectID, scope.ProjectID)
		},
		clear: (*Projector).clearReplayTimeline,
	}
}

func (p *Projector) syncReplays(ctx context.Context, scope Scope, checkpoint int64) (StepResult, error) {
	rows, err := p.source.QueryContext(ctx, `
		SELECT rm.rowid, rm.id, p.organization_id, rm.project_id, rm.replay_id, COALESCE(e.event_id, ''),
		       COALESCE(json_extract(rm.trace_ids_json, '$[0]'), ''), COALESCE(rm.release, ''), COALESCE(rm.environment, ''),
		       COALESCE(rm.started_at, rm.created_at, ''), COALESCE(rm.ended_at, ''), rm.duration_ms, rm.asset_count,
		       rm.error_marker_count, rm.click_count, COALESCE(e.payload_json, '{}')
		  FROM replay_manifests rm
		  JOIN projects p ON p.id = rm.project_id
		  LEFT JOIN events e ON e.id = rm.event_row_id
		 WHERE p.organization_id = ?
		   AND (? = '' OR rm.project_id = ?)
		   AND rm.rowid > ?
		 ORDER BY rm.rowid ASC
		 LIMIT ?`,
		scope.OrganizationID,
		scope.ProjectID, scope.ProjectID,
		checkpoint,
		p.batchSize,
	)
	if err != nil {
		return StepResult{}, fmt.Errorf("query replay projector batch: %w", err)
	}
	defer rows.Close()

	return p.withBridgeBatch(ctx, scope, FamilyReplays, func(exec bridgeExecer) (int, int64, error) {
		processed := 0
		last := checkpoint
		for rows.Next() {
			var rowID int64
			var (
				id, organizationID, projectID, replayID, eventID, traceID string
				release, environment, startedAt, finishedAt, payloadJSON  string
			)
			var durationMS float64
			var segmentCount, errorCount, clickCount int
			if err := rows.Scan(&rowID, &id, &organizationID, &projectID, &replayID, &eventID, &traceID, &release, &environment, &startedAt, &finishedAt, &durationMS, &segmentCount, &errorCount, &clickCount, &payloadJSON); err != nil {
				return 0, 0, fmt.Errorf("scan replay projector batch: %w", err)
			}
			if _, err := exec.ExecContext(ctx, `
			INSERT INTO telemetry.replay_manifests
				(id, organization_id, project_id, replay_id, event_id, trace_id, release, environment, started_at, finished_at, duration_ms, segment_count, error_count, click_count, payload_json)
			VALUES ($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),$9,$10,$11,$12,$13,$14,$15::jsonb)
			ON CONFLICT (id) DO UPDATE SET
				replay_id = EXCLUDED.replay_id,
				event_id = EXCLUDED.event_id,
				trace_id = EXCLUDED.trace_id,
				release = EXCLUDED.release,
				environment = EXCLUDED.environment,
				started_at = EXCLUDED.started_at,
				finished_at = EXCLUDED.finished_at,
				duration_ms = EXCLUDED.duration_ms,
				segment_count = EXCLUDED.segment_count,
				error_count = EXCLUDED.error_count,
				click_count = EXCLUDED.click_count,
				payload_json = EXCLUDED.payload_json`,
				id, organizationID, projectID, replayID, eventID, traceID, release, environment, mustTimestamp(startedAt), nullTimestampArg(finishedAt), durationMS, segmentCount, errorCount, clickCount, payloadJSON,
			); err != nil {
				return 0, 0, fmt.Errorf("upsert telemetry replay manifest: %w", err)
			}
			last = rowID
			processed++
		}
		if err := rows.Err(); err != nil {
			return 0, 0, fmt.Errorf("iterate replay projector batch: %w", err)
		}
		return processed, last, nil
	})
}

func (p *Projector) syncReplayTimeline(ctx context.Context, scope Scope, checkpoint int64) (StepResult, error) {
	rows, err := p.source.QueryContext(ctx, `
		SELECT rti.rowid, rti.id, rm.id, rm.project_id, rm.replay_id, COALESCE(rm.started_at, rm.created_at, ''),
		       rti.kind, rti.ts_ms, COALESCE(rti.pane_ref, ''), COALESCE(rti.title, ''), COALESCE(rti.level, ''),
		       COALESCE(rti.message, ''), COALESCE(rti.url, ''), COALESCE(rti.method, ''), rti.status_code,
		       rti.duration_ms, COALESCE(rti.selector, ''), COALESCE(rti.text_value, ''), COALESCE(rti.trace_id, ''),
		       COALESCE(rti.linked_event_id, ''), COALESCE(rti.linked_issue_id, ''), COALESCE(rti.payload_ref, ''),
		       COALESCE(rti.meta_json, '{}')
		  FROM replay_timeline_items rti
		  JOIN replay_manifests rm ON rm.id = rti.manifest_id
		  JOIN projects p ON p.id = rm.project_id
		 WHERE p.organization_id = ?
		   AND (? = '' OR rm.project_id = ?)
		   AND rti.rowid > ?
		 ORDER BY rti.rowid ASC
		 LIMIT ?`,
		scope.OrganizationID,
		scope.ProjectID, scope.ProjectID,
		checkpoint,
		p.batchSize,
	)
	if err != nil {
		return StepResult{}, fmt.Errorf("query replay timeline projector batch: %w", err)
	}
	defer rows.Close()

	return p.withBridgeBatch(ctx, scope, FamilyReplayTimeline, func(exec bridgeExecer) (int, int64, error) {
		processed := 0
		last := checkpoint
		for rows.Next() {
			var rowID int64
			var (
				id, manifestID, projectID, replayID, startedAt, kind, lane, title, level, message, url, method string
				selector, textValue, traceID, linkedEventID, linkedIssueID, payloadRef, metaJSON               string
			)
			var tsMS, statusCode int64
			var durationMS float64
			if err := rows.Scan(&rowID, &id, &manifestID, &projectID, &replayID, &startedAt, &kind, &tsMS, &lane, &title, &level, &message, &url, &method, &statusCode, &durationMS, &selector, &textValue, &traceID, &linkedEventID, &linkedIssueID, &payloadRef, &metaJSON); err != nil {
				return 0, 0, fmt.Errorf("scan replay timeline projector batch: %w", err)
			}
			timestamp := mustTimestamp(startedAt).Add(time.Duration(tsMS) * time.Millisecond)
			payloadJSON, err := json.Marshal(map[string]any{
				"title":           title,
				"level":           level,
				"message":         message,
				"url":             url,
				"method":          method,
				"status_code":     statusCode,
				"duration_ms":     durationMS,
				"selector":        selector,
				"text":            textValue,
				"trace_id":        traceID,
				"linked_event_id": linkedEventID,
				"linked_issue_id": linkedIssueID,
				"payload_ref":     payloadRef,
				"meta":            json.RawMessage(metaJSON),
			})
			if err != nil {
				return 0, 0, fmt.Errorf("marshal replay timeline payload: %w", err)
			}
			if _, err := exec.ExecContext(ctx, `
			INSERT INTO telemetry.replay_timeline_items
				(id, replay_manifest_id, project_id, replay_id, kind, timestamp, offset_ms, lane, payload_json)
			VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9::jsonb)
			ON CONFLICT (id) DO UPDATE SET
				replay_manifest_id = EXCLUDED.replay_manifest_id,
				project_id = EXCLUDED.project_id,
				replay_id = EXCLUDED.replay_id,
				kind = EXCLUDED.kind,
				timestamp = EXCLUDED.timestamp,
				offset_ms = EXCLUDED.offset_ms,
				lane = EXCLUDED.lane,
				payload_json = EXCLUDED.payload_json`,
				id, manifestID, projectID, replayID, kind, timestamp, tsMS, lane, string(payloadJSON),
			); err != nil {
				return 0, 0, fmt.Errorf("upsert telemetry replay timeline item: %w", err)
			}
			last = rowID
			processed++
		}
		if err := rows.Err(); err != nil {
			return 0, 0, fmt.Errorf("iterate replay timeline projector batch: %w", err)
		}
		return processed, last, nil
	})
}

func (p *Projector) clearReplayTimeline(ctx context.Context, scope Scope) error {
	query := `DELETE FROM telemetry.replay_timeline_items WHERE project_id = $1`
	args := []any{scope.ProjectID}
	if scope.ProjectID == "" {
		query = `DELETE FROM telemetry.replay_timeline_items WHERE project_id IN (SELECT id FROM telemetry.replay_manifests WHERE organization_id = $1)`
		args = []any{scope.OrganizationID}
	}
	if _, err := p.bridge.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("delete telemetry family %s: %w", FamilyReplayTimeline, err)
	}
	return nil
}
