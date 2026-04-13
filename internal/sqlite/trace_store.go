package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/store"
)

// TraceStore persists performance transactions and spans in SQLite.
type TraceStore struct {
	db *sql.DB
}

// NewTraceStore creates a SQLite-backed trace store.
func NewTraceStore(db *sql.DB) *TraceStore {
	return &TraceStore{db: db}
}

// SaveTransaction stores a transaction row and its child spans.
func (s *TraceStore) SaveTransaction(ctx context.Context, txn *store.StoredTransaction) error {
	if txn == nil {
		return nil
	}
	if txn.ProjectID == "" || txn.EventID == "" || txn.TraceID == "" || txn.SpanID == "" {
		return fmt.Errorf("transaction project_id, event_id, trace_id, and span_id are required")
	}
	if txn.ID == "" {
		txn.ID = generateID()
	}
	if txn.EndTimestamp.IsZero() {
		txn.EndTimestamp = time.Now().UTC()
	}
	if txn.StartTimestamp.IsZero() {
		txn.StartTimestamp = txn.EndTimestamp
	}
	if txn.DurationMS == 0 {
		txn.DurationMS = txn.EndTimestamp.Sub(txn.StartTimestamp).Seconds() * 1000
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	tagsJSON, _ := json.Marshal(txn.Tags)
	measurementsJSON, _ := json.Marshal(txn.Measurements)
	_, err = tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO transactions
			(id, project_id, event_id, trace_id, span_id, parent_span_id, transaction_name, op, status, platform,
			 environment, release, start_timestamp, end_timestamp, duration_ms, tags_json, measurements_json,
			 payload_json, payload_key, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		txn.ID,
		txn.ProjectID,
		txn.EventID,
		txn.TraceID,
		txn.SpanID,
		nullIfEmpty(txn.ParentSpanID),
		txn.Transaction,
		nullIfEmpty(txn.Op),
		nullIfEmpty(txn.Status),
		nullIfEmpty(txn.Platform),
		nullIfEmpty(txn.Environment),
		nullIfEmpty(txn.ReleaseID),
		txn.StartTimestamp.UTC().Format(time.RFC3339Nano),
		txn.EndTimestamp.UTC().Format(time.RFC3339Nano),
		txn.DurationMS,
		string(tagsJSON),
		string(measurementsJSON),
		string(txn.NormalizedJSON),
		nullIfEmpty(txn.PayloadKey),
		txn.EndTimestamp.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert transaction: %w", err)
	}

	if _, err = tx.ExecContext(ctx, `DELETE FROM spans WHERE project_id = ? AND transaction_event_id = ?`, txn.ProjectID, txn.EventID); err != nil {
		return fmt.Errorf("delete existing spans: %w", err)
	}
	for i := range txn.Spans {
		span := txn.Spans[i]
		if span.ID == "" {
			span.ID = generateID()
		}
		if span.ProjectID == "" {
			span.ProjectID = txn.ProjectID
		}
		if span.TransactionEventID == "" {
			span.TransactionEventID = txn.EventID
		}
		if span.DurationMS == 0 && !span.StartTimestamp.IsZero() && !span.EndTimestamp.IsZero() {
			span.DurationMS = span.EndTimestamp.Sub(span.StartTimestamp).Seconds() * 1000
		}
		txn.Spans[i] = span
		spanTagsJSON, _ := json.Marshal(span.Tags)
		dataJSON, _ := json.Marshal(span.Data)
		if _, err = tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO spans
				(id, project_id, transaction_event_id, trace_id, span_id, parent_span_id, op, description, status,
				 start_timestamp, end_timestamp, duration_ms, tags_json, data_json, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			span.ID,
			span.ProjectID,
			span.TransactionEventID,
			span.TraceID,
			span.SpanID,
			nullIfEmpty(span.ParentSpanID),
			nullIfEmpty(span.Op),
			nullIfEmpty(span.Description),
			nullIfEmpty(span.Status),
			span.StartTimestamp.UTC().Format(time.RFC3339Nano),
			span.EndTimestamp.UTC().Format(time.RFC3339Nano),
			span.DurationMS,
			string(spanTagsJSON),
			string(dataJSON),
			txn.EndTimestamp.UTC().Format(time.RFC3339Nano),
		); err != nil {
			return fmt.Errorf("insert span: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return err
	}
	return nil
}

// GetTransaction returns one transaction by event ID.
func (s *TraceStore) GetTransaction(ctx context.Context, projectID, eventID string) (*store.StoredTransaction, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, event_id, trace_id, span_id, COALESCE(parent_span_id, ''), transaction_name,
		        COALESCE(op, ''), COALESCE(status, ''), COALESCE(platform, ''), COALESCE(environment, ''),
		        COALESCE(release, ''), start_timestamp, end_timestamp, duration_ms,
		        COALESCE(tags_json, '{}'), COALESCE(measurements_json, '{}'), COALESCE(payload_json, '{}'),
		        COALESCE(payload_key, ''), created_at
		 FROM transactions
		 WHERE project_id = ? AND event_id = ?`,
		projectID, eventID,
	)
	item, err := s.scanTransaction(row)
	if err != nil {
		return nil, err
	}
	if err := s.loadTransactionSpans(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

// ListTransactions returns recent transactions for a project.
func (s *TraceStore) ListTransactions(ctx context.Context, projectID string, limit int) ([]*store.StoredTransaction, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, event_id, trace_id, span_id, COALESCE(parent_span_id, ''), transaction_name,
		        COALESCE(op, ''), COALESCE(status, ''), COALESCE(platform, ''), COALESCE(environment, ''),
		        COALESCE(release, ''), start_timestamp, end_timestamp, duration_ms,
		        COALESCE(tags_json, '{}'), COALESCE(measurements_json, '{}'), COALESCE(payload_json, '{}'),
		        COALESCE(payload_key, ''), created_at
		 FROM transactions
		 WHERE project_id = ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		projectID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
	}
	defer rows.Close()

	var items []*store.StoredTransaction
	for rows.Next() {
		item, err := s.scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ListTransactionsByTrace returns all transactions for a trace ordered by start time.
func (s *TraceStore) ListTransactionsByTrace(ctx context.Context, projectID, traceID string) ([]*store.StoredTransaction, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, event_id, trace_id, span_id, COALESCE(parent_span_id, ''), transaction_name,
		        COALESCE(op, ''), COALESCE(status, ''), COALESCE(platform, ''), COALESCE(environment, ''),
		        COALESCE(release, ''), start_timestamp, end_timestamp, duration_ms,
		        COALESCE(tags_json, '{}'), COALESCE(measurements_json, '{}'), COALESCE(payload_json, '{}'),
		        COALESCE(payload_key, ''), created_at
		 FROM transactions
		 WHERE project_id = ? AND trace_id = ?
		 ORDER BY start_timestamp ASC, span_id ASC`,
		projectID, traceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list transactions by trace: %w", err)
	}
	defer rows.Close()

	var items []*store.StoredTransaction
	for rows.Next() {
		item, err := s.scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ListTraceSpans returns all spans for a trace, ordered by start time.
func (s *TraceStore) ListTraceSpans(ctx context.Context, projectID, traceID string) ([]store.StoredSpan, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, transaction_event_id, trace_id, span_id, COALESCE(parent_span_id, ''),
		        COALESCE(op, ''), COALESCE(description, ''), COALESCE(status, ''), start_timestamp, end_timestamp,
		        duration_ms, COALESCE(tags_json, '{}'), COALESCE(data_json, '{}')
		 FROM spans
		 WHERE project_id = ? AND trace_id = ?
		 ORDER BY start_timestamp ASC, span_id ASC`,
		projectID, traceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list trace spans: %w", err)
	}
	defer rows.Close()

	var spans []store.StoredSpan
	for rows.Next() {
		var (
			span     store.StoredSpan
			tagsJSON string
			dataJSON string
			started  string
			ended    string
		)
		if err := rows.Scan(
			&span.ID,
			&span.ProjectID,
			&span.TransactionEventID,
			&span.TraceID,
			&span.SpanID,
			&span.ParentSpanID,
			&span.Op,
			&span.Description,
			&span.Status,
			&started,
			&ended,
			&span.DurationMS,
			&tagsJSON,
			&dataJSON,
		); err != nil {
			return nil, fmt.Errorf("scan span: %w", err)
		}
		span.StartTimestamp = parseNanoTime(started)
		span.EndTimestamp = parseNanoTime(ended)
		_ = json.Unmarshal([]byte(tagsJSON), &span.Tags)
		_ = json.Unmarshal([]byte(dataJSON), &span.Data)
		spans = append(spans, span)
	}
	return spans, rows.Err()
}

func (s *TraceStore) scanTransaction(row scanner) (*store.StoredTransaction, error) {
	var (
		item             store.StoredTransaction
		tagsJSON         string
		measurementsJSON string
		payloadJSON      string
		started          string
		ended            string
	)
	if err := row.Scan(
		&item.ID,
		&item.ProjectID,
		&item.EventID,
		&item.TraceID,
		&item.SpanID,
		&item.ParentSpanID,
		&item.Transaction,
		&item.Op,
		&item.Status,
		&item.Platform,
		&item.Environment,
		&item.ReleaseID,
		&started,
		&ended,
		&item.DurationMS,
		&tagsJSON,
		&measurementsJSON,
		&payloadJSON,
		&item.PayloadKey,
		new(string),
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("scan transaction: %w", err)
	}
	item.StartTimestamp = parseNanoTime(started)
	item.EndTimestamp = parseNanoTime(ended)
	item.NormalizedJSON = json.RawMessage(payloadJSON)
	_ = json.Unmarshal([]byte(tagsJSON), &item.Tags)
	_ = json.Unmarshal([]byte(measurementsJSON), &item.Measurements)
	return &item, nil
}

func (s *TraceStore) loadTransactionSpans(ctx context.Context, item *store.StoredTransaction) error {
	if item == nil || item.ProjectID == "" || item.TraceID == "" {
		return nil
	}
	spans, err := s.ListTraceSpans(ctx, item.ProjectID, item.TraceID)
	if err != nil {
		return err
	}
	item.Spans = spans
	return nil
}

func parseNanoTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return ts
	}
	return parseTime(raw)
}
