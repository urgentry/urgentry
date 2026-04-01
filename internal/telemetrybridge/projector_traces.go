package telemetrybridge

import (
	"context"
	"fmt"
)

func projectorTransactionsFamily() projectorFamily {
	return projectorFamily{
		sync: (*Projector).syncTransactions,
		count: func(p *Projector, ctx context.Context, scope Scope) (int, error) {
			return p.countSourceRows(ctx, FamilyTransactions, `SELECT COUNT(*) FROM transactions t JOIN projects p ON p.id = t.project_id WHERE p.organization_id = ? AND (? = '' OR t.project_id = ?)`, scope.OrganizationID, scope.ProjectID, scope.ProjectID)
		},
		clear: func(p *Projector, ctx context.Context, scope Scope) error {
			return p.deleteBridgeRows(ctx, FamilyTransactions, `DELETE FROM telemetry.transaction_facts WHERE `, scope)
		},
	}
}

func projectorSpansFamily() projectorFamily {
	return projectorFamily{
		sync: (*Projector).syncSpans,
		count: func(p *Projector, ctx context.Context, scope Scope) (int, error) {
			return p.countSourceRows(ctx, FamilySpans, `SELECT COUNT(*) FROM spans s JOIN projects p ON p.id = s.project_id WHERE p.organization_id = ? AND (? = '' OR s.project_id = ?)`, scope.OrganizationID, scope.ProjectID, scope.ProjectID)
		},
		clear: func(p *Projector, ctx context.Context, scope Scope) error {
			return p.deleteBridgeRows(ctx, FamilySpans, `DELETE FROM telemetry.span_facts WHERE `, scope)
		},
	}
}

func (p *Projector) syncTransactions(ctx context.Context, scope Scope, checkpoint int64) (StepResult, error) {
	rows, err := p.source.QueryContext(ctx, `
		SELECT t.rowid, t.id, p.organization_id, t.project_id, t.event_id, t.trace_id, t.span_id, COALESCE(t.parent_span_id, ''),
		       t.transaction_name, COALESCE(t.op, ''), COALESCE(t.status, ''), COALESCE(t.release, ''), COALESCE(t.environment, ''),
		       COALESCE(t.start_timestamp, ''), COALESCE(t.end_timestamp, ''), t.duration_ms,
		       COALESCE(t.measurements_json, '{}'), COALESCE(t.tags_json, '{}')
		  FROM transactions t
		  JOIN projects p ON p.id = t.project_id
		 WHERE p.organization_id = ?
		   AND (? = '' OR t.project_id = ?)
		   AND t.rowid > ?
		 ORDER BY t.rowid ASC
		 LIMIT ?`,
		scope.OrganizationID,
		scope.ProjectID, scope.ProjectID,
		checkpoint,
		p.batchSize,
	)
	if err != nil {
		return StepResult{}, fmt.Errorf("query transaction projector batch: %w", err)
	}
	defer rows.Close()

	return p.withBridgeBatch(ctx, scope, FamilyTransactions, func(exec bridgeExecer) (int, int64, error) {
		processed := 0
		last := checkpoint
		for rows.Next() {
			var rowID int64
			var (
				id, organizationID, projectID, eventID, traceID, spanID, parentSpanID string
				transactionName, op, status, release, environment                     string
				startedAt, finishedAt, measurementsJSON, tagsJSON                     string
				durationMS                                                            float64
			)
			if err := rows.Scan(&rowID, &id, &organizationID, &projectID, &eventID, &traceID, &spanID, &parentSpanID, &transactionName, &op, &status, &release, &environment, &startedAt, &finishedAt, &durationMS, &measurementsJSON, &tagsJSON); err != nil {
				return 0, 0, fmt.Errorf("scan transaction projector batch: %w", err)
			}
			if _, err := exec.ExecContext(ctx, `
			INSERT INTO telemetry.transaction_facts
				(id, organization_id, project_id, event_id, trace_id, span_id, parent_span_id, transaction_name, op, status, release, environment, started_at, finished_at, duration_ms, measurements_json, tags_json)
			VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,NULLIF($9,''),NULLIF($10,''),NULLIF($11,''),NULLIF($12,''),$13,$14,$15,$16::jsonb,$17::jsonb)
			ON CONFLICT (id) DO UPDATE SET
				trace_id = EXCLUDED.trace_id,
				span_id = EXCLUDED.span_id,
				parent_span_id = EXCLUDED.parent_span_id,
				transaction_name = EXCLUDED.transaction_name,
				op = EXCLUDED.op,
				status = EXCLUDED.status,
				release = EXCLUDED.release,
				environment = EXCLUDED.environment,
				started_at = EXCLUDED.started_at,
				finished_at = EXCLUDED.finished_at,
				duration_ms = EXCLUDED.duration_ms,
				measurements_json = EXCLUDED.measurements_json,
				tags_json = EXCLUDED.tags_json`,
				id, organizationID, projectID, eventID, traceID, spanID, parentSpanID, transactionName, op, status, release, environment, mustTimestamp(startedAt), mustTimestamp(finishedAt), durationMS, measurementsJSON, tagsJSON,
			); err != nil {
				return 0, 0, fmt.Errorf("upsert telemetry transaction fact: %w", err)
			}
			last = rowID
			processed++
		}
		if err := rows.Err(); err != nil {
			return 0, 0, fmt.Errorf("iterate transaction projector batch: %w", err)
		}
		return processed, last, nil
	})
}

func (p *Projector) syncSpans(ctx context.Context, scope Scope, checkpoint int64) (StepResult, error) {
	rows, err := p.source.QueryContext(ctx, `
		SELECT s.rowid, s.id, p.organization_id, s.project_id, s.transaction_event_id, s.trace_id, s.span_id, COALESCE(s.parent_span_id, ''),
		       COALESCE(s.op, ''), COALESCE(s.description, ''), COALESCE(s.status, ''), COALESCE(s.start_timestamp, ''),
		       COALESCE(s.end_timestamp, ''), s.duration_ms, COALESCE(s.tags_json, '{}'), COALESCE(s.data_json, '{}')
		  FROM spans s
		  JOIN projects p ON p.id = s.project_id
		 WHERE p.organization_id = ?
		   AND (? = '' OR s.project_id = ?)
		   AND s.rowid > ?
		 ORDER BY s.rowid ASC
		 LIMIT ?`,
		scope.OrganizationID,
		scope.ProjectID, scope.ProjectID,
		checkpoint,
		p.batchSize,
	)
	if err != nil {
		return StepResult{}, fmt.Errorf("query span projector batch: %w", err)
	}
	defer rows.Close()

	return p.withBridgeBatch(ctx, scope, FamilySpans, func(exec bridgeExecer) (int, int64, error) {
		processed := 0
		last := checkpoint
		for rows.Next() {
			var rowID int64
			var (
				id, organizationID, projectID, transactionEventID, traceID, spanID, parentSpanID string
				op, description, status, startedAt, finishedAt, tagsJSON, dataJSON               string
				durationMS                                                                       float64
			)
			if err := rows.Scan(&rowID, &id, &organizationID, &projectID, &transactionEventID, &traceID, &spanID, &parentSpanID, &op, &description, &status, &startedAt, &finishedAt, &durationMS, &tagsJSON, &dataJSON); err != nil {
				return 0, 0, fmt.Errorf("scan span projector batch: %w", err)
			}
			if _, err := exec.ExecContext(ctx, `
			INSERT INTO telemetry.span_facts
				(id, organization_id, project_id, transaction_event_id, trace_id, span_id, parent_span_id, op, description, status, started_at, finished_at, duration_ms, tags_json, data_json)
			VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),NULLIF($9,''),NULLIF($10,''),$11,$12,$13,$14::jsonb,$15::jsonb)
			ON CONFLICT (id) DO UPDATE SET
				trace_id = EXCLUDED.trace_id,
				span_id = EXCLUDED.span_id,
				parent_span_id = EXCLUDED.parent_span_id,
				op = EXCLUDED.op,
				description = EXCLUDED.description,
				status = EXCLUDED.status,
				started_at = EXCLUDED.started_at,
				finished_at = EXCLUDED.finished_at,
				duration_ms = EXCLUDED.duration_ms,
				tags_json = EXCLUDED.tags_json,
				data_json = EXCLUDED.data_json`,
				id, organizationID, projectID, transactionEventID, traceID, spanID, parentSpanID, op, description, status, mustTimestamp(startedAt), mustTimestamp(finishedAt), durationMS, tagsJSON, dataJSON,
			); err != nil {
				return 0, 0, fmt.Errorf("upsert telemetry span fact: %w", err)
			}
			last = rowID
			processed++
		}
		if err := rows.Err(); err != nil {
			return 0, 0, fmt.Errorf("iterate span projector batch: %w", err)
		}
		return processed, last, nil
	})
}
