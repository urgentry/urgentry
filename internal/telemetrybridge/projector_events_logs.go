package telemetrybridge

import (
	"context"
	"fmt"
	"strings"
)

func projectorEventsFamily() projectorFamily {
	return projectorFamily{
		sync: (*Projector).syncEvents,
		count: func(p *Projector, ctx context.Context, scope Scope) (int, error) {
			return p.countSourceRows(ctx, FamilyEvents, `SELECT COUNT(*) FROM events e JOIN projects p ON p.id = e.project_id WHERE p.organization_id = ? AND (? = '' OR e.project_id = ?)`, scope.OrganizationID, scope.ProjectID, scope.ProjectID)
		},
		clear: func(p *Projector, ctx context.Context, scope Scope) error {
			return p.deleteBridgeRows(ctx, FamilyEvents, `DELETE FROM telemetry.event_facts WHERE `, scope)
		},
	}
}

func projectorLogsFamily() projectorFamily {
	return projectorFamily{
		sync: (*Projector).syncLogs,
		count: func(p *Projector, ctx context.Context, scope Scope) (int, error) {
			return p.countSourceRows(ctx, FamilyLogs, `SELECT COUNT(*) FROM events e JOIN projects p ON p.id = e.project_id WHERE p.organization_id = ? AND (? = '' OR e.project_id = ?) AND LOWER(COALESCE(e.event_type, 'error')) = 'log'`, scope.OrganizationID, scope.ProjectID, scope.ProjectID)
		},
		clear: func(p *Projector, ctx context.Context, scope Scope) error {
			return p.deleteBridgeRows(ctx, FamilyLogs, `DELETE FROM telemetry.log_facts WHERE `, scope)
		},
	}
}

func (p *Projector) syncEvents(ctx context.Context, scope Scope, checkpoint int64) (StepResult, error) {
	rows, err := p.source.QueryContext(ctx, `
		SELECT e.rowid, e.id, p.organization_id, e.project_id, COALESCE(e.group_id, ''), e.event_id,
		       COALESCE(e.event_type, 'error'), COALESCE(e.release, ''), COALESCE(e.environment, ''),
		       COALESCE(e.platform, ''), COALESCE(e.level, ''), COALESCE(e.title, ''), COALESCE(e.culprit, ''),
		       COALESCE(e.occurred_at, ''), COALESCE(e.ingested_at, ''), COALESCE(e.message, ''),
		       COALESCE(e.tags_json, '{}'), COALESCE(e.payload_key, '')
		  FROM events e
		  JOIN projects p ON p.id = e.project_id
		 WHERE p.organization_id = ?
		   AND (? = '' OR e.project_id = ?)
		   AND e.rowid > ?
		 ORDER BY e.rowid ASC
		 LIMIT ?`,
		scope.OrganizationID,
		scope.ProjectID, scope.ProjectID,
		checkpoint,
		p.batchSize,
	)
	if err != nil {
		return StepResult{}, fmt.Errorf("query event projector batch: %w", err)
	}
	defer rows.Close()

	return p.withBridgeBatch(ctx, scope, FamilyEvents, func(exec bridgeExecer) (int, int64, error) {
		processed := 0
		last := checkpoint
		for rows.Next() {
			var rowID int64
			var (
				id, organizationID, projectID, groupID, eventID, eventType string
				release, environment, platform, level, title, culprit      string
				occurredAt, ingestedAt, message, tagsJSON, payloadKey      string
			)
			if err := rows.Scan(&rowID, &id, &organizationID, &projectID, &groupID, &eventID, &eventType, &release, &environment, &platform, &level, &title, &culprit, &occurredAt, &ingestedAt, &message, &tagsJSON, &payloadKey); err != nil {
				return 0, 0, fmt.Errorf("scan event projector batch: %w", err)
			}
			searchText := strings.TrimSpace(strings.Join([]string{title, message, culprit}, " "))
			if _, err := exec.ExecContext(ctx, `
			INSERT INTO telemetry.event_facts
				(id, organization_id, project_id, group_id, event_id, event_type, release, environment, platform, level, title, culprit, occurred_at, ingested_at, search_text, tags_json, dimensions_json, payload_object_key)
			VALUES ($1,$2,$3,NULLIF($4,''),$5,$6,NULLIF($7,''),NULLIF($8,''),NULLIF($9,''),NULLIF($10,''),NULLIF($11,''),NULLIF($12,''),$13,$14,NULLIF($15,''),$16::jsonb,'{}'::jsonb,NULLIF($17,''))
			ON CONFLICT (id) DO UPDATE SET
				group_id = EXCLUDED.group_id,
				event_id = EXCLUDED.event_id,
				event_type = EXCLUDED.event_type,
				release = EXCLUDED.release,
				environment = EXCLUDED.environment,
				platform = EXCLUDED.platform,
				level = EXCLUDED.level,
				title = EXCLUDED.title,
				culprit = EXCLUDED.culprit,
				occurred_at = EXCLUDED.occurred_at,
				ingested_at = EXCLUDED.ingested_at,
				search_text = EXCLUDED.search_text,
				tags_json = EXCLUDED.tags_json,
				payload_object_key = EXCLUDED.payload_object_key`,
				id, organizationID, projectID, groupID, eventID, eventType, release, environment, platform, level, title, culprit, mustTimestamp(occurredAt), mustTimestamp(ingestedAt), searchText, tagsJSON, payloadKey,
			); err != nil {
				return 0, 0, fmt.Errorf("upsert telemetry event fact: %w", err)
			}
			last = rowID
			processed++
		}
		if err := rows.Err(); err != nil {
			return 0, 0, fmt.Errorf("iterate event projector batch: %w", err)
		}
		return processed, last, nil
	})
}

func (p *Projector) syncLogs(ctx context.Context, scope Scope, checkpoint int64) (StepResult, error) {
	rows, err := p.source.QueryContext(ctx, `
		SELECT e.rowid, e.id, p.organization_id, e.project_id, e.event_id,
		       COALESCE(json_extract(e.payload_json, '$.contexts.trace.trace_id'), ''),
		       COALESCE(json_extract(e.payload_json, '$.contexts.trace.span_id'), ''),
		       COALESCE(e.release, ''), COALESCE(e.environment, ''), COALESCE(e.platform, ''),
		       COALESCE(e.level, ''), COALESCE(json_extract(e.payload_json, '$.logger'), ''),
		       COALESCE(e.message, ''), COALESCE(e.occurred_at, ''), COALESCE(e.tags_json, '{}'),
		       COALESCE(e.payload_json, '{}')
		  FROM events e
		  JOIN projects p ON p.id = e.project_id
		 WHERE p.organization_id = ?
		   AND (? = '' OR e.project_id = ?)
		   AND LOWER(COALESCE(e.event_type, 'error')) = 'log'
		   AND e.rowid > ?
		 ORDER BY e.rowid ASC
		 LIMIT ?`,
		scope.OrganizationID,
		scope.ProjectID, scope.ProjectID,
		checkpoint,
		p.batchSize,
	)
	if err != nil {
		return StepResult{}, fmt.Errorf("query log projector batch: %w", err)
	}
	defer rows.Close()

	return p.withBridgeBatch(ctx, scope, FamilyLogs, func(exec bridgeExecer) (int, int64, error) {
		processed := 0
		last := checkpoint
		columnarFacts := make([]ColumnarLogFact, 0, p.batchSize)
		for rows.Next() {
			var rowID int64
			var (
				id, organizationID, projectID, eventID, traceID, spanID string
				release, environment, platform, level, logger, message  string
				timestampRaw, tagsJSON, payloadJSON                     string
			)
			if err := rows.Scan(&rowID, &id, &organizationID, &projectID, &eventID, &traceID, &spanID, &release, &environment, &platform, &level, &logger, &message, &timestampRaw, &tagsJSON, &payloadJSON); err != nil {
				return 0, 0, fmt.Errorf("scan log projector batch: %w", err)
			}
			searchText := strings.TrimSpace(strings.Join([]string{message, logger, traceID, spanID}, " "))
			if _, err := exec.ExecContext(ctx, `
			INSERT INTO telemetry.log_facts
				(id, organization_id, project_id, event_id, trace_id, span_id, release, environment, platform, level, logger, message, search_text, timestamp, attributes_json)
			VALUES ($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),NULLIF($9,''),NULLIF($10,''),NULLIF($11,''),NULLIF($12,''),NULLIF($13,''),$14,$15::jsonb)
			ON CONFLICT (id) DO UPDATE SET
				trace_id = EXCLUDED.trace_id,
				span_id = EXCLUDED.span_id,
				release = EXCLUDED.release,
				environment = EXCLUDED.environment,
				platform = EXCLUDED.platform,
				level = EXCLUDED.level,
				logger = EXCLUDED.logger,
				message = EXCLUDED.message,
				search_text = EXCLUDED.search_text,
				timestamp = EXCLUDED.timestamp,
				attributes_json = EXCLUDED.attributes_json`,
				id, organizationID, projectID, eventID, traceID, spanID, release, environment, platform, level, logger, message, searchText, mustTimestamp(timestampRaw), payloadJSON,
			); err != nil {
				return 0, 0, fmt.Errorf("upsert telemetry log fact: %w", err)
			}
			columnarFacts = append(columnarFacts, ColumnarLogFact{
				ID:             id,
				OrganizationID: organizationID,
				ProjectID:      projectID,
				EventID:        eventID,
				TraceID:        traceID,
				SpanID:         spanID,
				Release:        release,
				Environment:    environment,
				Platform:       platform,
				Level:          level,
				Logger:         logger,
				Message:        message,
				SearchText:     searchText,
				Timestamp:      mustTimestamp(timestampRaw),
				AttributesJSON: payloadJSON,
			})
			last = rowID
			processed++
		}
		if err := rows.Err(); err != nil {
			return 0, 0, fmt.Errorf("iterate log projector batch: %w", err)
		}
		if len(columnarFacts) > 0 && p.logSink != nil {
			if err := p.logSink.UpsertLogs(ctx, columnarFacts); err != nil {
				return 0, 0, fmt.Errorf("upsert columnar log facts: %w", err)
			}
		}
		return processed, last, nil
	})
}
