package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"urgentry/internal/store"
)

func (s *RetentionStore) deleteTransactionsOlderThan(ctx context.Context, projectID string, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, event_id, COALESCE(payload_key, '')
		 FROM transactions
		 WHERE project_id = ? AND end_timestamp < ?`,
		projectID, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("list old traces: %w", err)
	}
	defer rows.Close()

	var ids []string
	var eventIDs []string
	for rows.Next() {
		var id, eventID, payloadKey string
		if err := rows.Scan(&id, &eventID, &payloadKey); err != nil {
			return 0, fmt.Errorf("scan old trace: %w", err)
		}
		ids = append(ids, id)
		eventIDs = append(eventIDs, eventID)
		if s.blobs != nil && payloadKey != "" {
			_ = s.blobs.Delete(ctx, payloadKey)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate old traces: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close old traces: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM spans WHERE project_id = ? AND transaction_event_id IN (`+placeholders(len(eventIDs))+`)`,
		append([]any{projectID}, stringArgs(eventIDs)...)...,
	); err != nil {
		return 0, fmt.Errorf("delete old spans: %w", err)
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM transactions WHERE id IN (`+placeholders(len(ids))+`)`,
		stringArgs(ids)...,
	)
	if err != nil {
		return 0, fmt.Errorf("delete old traces: %w", err)
	}
	return res.RowsAffected()
}

func (s *RetentionStore) archiveTransactionsOlderThan(ctx context.Context, projectID string, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, event_id, trace_id, span_id, COALESCE(parent_span_id, ''), transaction_name, COALESCE(op, ''), COALESCE(status, ''), COALESCE(platform, ''), COALESCE(environment, ''), COALESCE(release, ''),
		        start_timestamp, end_timestamp, duration_ms, COALESCE(tags_json, '{}'), COALESCE(measurements_json, '{}'), COALESCE(payload_json, '{}'), COALESCE(payload_key, ''), created_at
		 FROM transactions
		 WHERE project_id = ? AND end_timestamp < ?`,
		projectID, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("list traces to archive: %w", err)
	}
	defer rows.Close()

	var txns []archivedTransaction
	for rows.Next() {
		var item archivedTransaction
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.EventID, &item.TraceID, &item.SpanID, &item.ParentSpanID, &item.TransactionName, &item.Op, &item.Status, &item.Platform, &item.Environment, &item.Release, &item.StartTimestamp, &item.EndTimestamp, &item.DurationMS, &item.TagsJSON, &item.MeasurementsJSON, &item.PayloadJSON, &item.PayloadKey, &item.CreatedAt); err != nil {
			return 0, fmt.Errorf("scan trace archive candidate: %w", err)
		}
		txns = append(txns, item)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate trace archive candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close trace archive candidates: %w", err)
	}

	var archived int64
	for _, item := range txns {
		payload, _ := json.Marshal(item)
		if err := s.archiveRecord(ctx, projectID, store.TelemetrySurfaceTraces, "transaction", item.ID, item.PayloadKey, payload); err != nil {
			return 0, err
		}
		spans, err := s.loadSpansForTransaction(ctx, projectID, item.EventID)
		if err != nil {
			return 0, err
		}
		for _, span := range spans {
			payload, _ := json.Marshal(span)
			if err := s.archiveRecord(ctx, projectID, store.TelemetrySurfaceTraces, "span", span.ID, "", payload); err != nil {
				return 0, err
			}
			archived++
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM spans WHERE project_id = ? AND transaction_event_id = ?`, projectID, item.EventID); err != nil {
			return 0, fmt.Errorf("delete archived spans: %w", err)
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM transactions WHERE id = ?`, item.ID); err != nil {
			return 0, fmt.Errorf("delete archived transaction: %w", err)
		}
		archived++
	}
	return archived, nil
}

func (s *RetentionStore) loadSpansForTransaction(ctx context.Context, projectID, eventID string) ([]archivedSpan, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, transaction_event_id, trace_id, span_id, COALESCE(parent_span_id, ''), COALESCE(op, ''), COALESCE(description, ''), COALESCE(status, ''), start_timestamp, end_timestamp, duration_ms, COALESCE(tags_json, '{}'), COALESCE(data_json, '{}'), created_at
		 FROM spans
		 WHERE project_id = ? AND transaction_event_id = ?`,
		projectID, eventID,
	)
	if err != nil {
		return nil, fmt.Errorf("list spans for archive: %w", err)
	}
	defer rows.Close()
	var spans []archivedSpan
	for rows.Next() {
		var item archivedSpan
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.TransactionEventID, &item.TraceID, &item.SpanID, &item.ParentSpanID, &item.Op, &item.Description, &item.Status, &item.StartTimestamp, &item.EndTimestamp, &item.DurationMS, &item.TagsJSON, &item.DataJSON, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan span for archive: %w", err)
		}
		spans = append(spans, item)
	}
	return spans, rows.Err()
}

func (s *RetentionStore) deleteOutcomesOlderThan(ctx context.Context, projectID string, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour).Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM outcomes WHERE project_id = ? AND recorded_at < ?`,
		projectID, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("delete old outcomes: %w", err)
	}
	return res.RowsAffected()
}

func (s *RetentionStore) archiveOutcomesOlderThan(ctx context.Context, projectID string, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, COALESCE(event_id, ''), category, reason, quantity, source, COALESCE(release, ''), COALESCE(environment, ''), COALESCE(payload_json, '{}'), recorded_at, created_at
		 FROM outcomes
		 WHERE project_id = ? AND recorded_at < ?`,
		projectID, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("list outcomes to archive: %w", err)
	}
	defer rows.Close()

	var outcomes []archivedOutcome
	for rows.Next() {
		var item archivedOutcome
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.EventID, &item.Category, &item.Reason, &item.Quantity, &item.Source, &item.Release, &item.Environment, &item.PayloadJSON, &item.RecordedAt, &item.CreatedAt); err != nil {
			return 0, fmt.Errorf("scan outcome archive candidate: %w", err)
		}
		outcomes = append(outcomes, item)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate outcome archive candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close outcome archive candidates: %w", err)
	}

	var archived int64
	for _, item := range outcomes {
		payload, _ := json.Marshal(item)
		if err := s.archiveRecord(ctx, projectID, store.TelemetrySurfaceOutcomes, "outcome", item.ID, "", payload); err != nil {
			return 0, err
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM outcomes WHERE id = ?`, item.ID); err != nil {
			return 0, fmt.Errorf("delete archived outcome: %w", err)
		}
		archived++
	}
	return archived, nil
}
