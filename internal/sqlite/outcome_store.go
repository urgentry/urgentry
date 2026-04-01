package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Outcome records a dropped-event accounting entry from a client report.
type Outcome struct {
	ID          string          `json:"id"`
	ProjectID   string          `json:"projectId"`
	EventID     string          `json:"eventId,omitempty"`
	Category    string          `json:"category"`
	Reason      string          `json:"reason"`
	Quantity    int             `json:"quantity"`
	Source      string          `json:"source"`
	Release     string          `json:"release,omitempty"`
	Environment string          `json:"environment,omitempty"`
	PayloadJSON json.RawMessage `json:"payload,omitempty"`
	RecordedAt  time.Time       `json:"recordedAt"`
	DateCreated time.Time       `json:"dateCreated"`
}

// OutcomeStore persists client-report outcomes in SQLite.
type OutcomeStore struct {
	db *sql.DB
}

// NewOutcomeStore creates a SQLite-backed outcome store.
func NewOutcomeStore(db *sql.DB) *OutcomeStore {
	return &OutcomeStore{db: db}
}

// SaveOutcome stores one dropped-event accounting row.
func (s *OutcomeStore) SaveOutcome(ctx context.Context, outcome *Outcome) error {
	if outcome == nil {
		return nil
	}
	if strings.TrimSpace(outcome.ProjectID) == "" {
		return fmt.Errorf("outcome project_id is required")
	}
	if strings.TrimSpace(outcome.Category) == "" {
		return fmt.Errorf("outcome category is required")
	}
	if strings.TrimSpace(outcome.Reason) == "" {
		return fmt.Errorf("outcome reason is required")
	}
	if outcome.ID == "" {
		outcome.ID = generateID()
	}
	if outcome.Quantity <= 0 {
		outcome.Quantity = 1
	}
	if outcome.Source == "" {
		outcome.Source = "client_report"
	}
	if outcome.RecordedAt.IsZero() {
		outcome.RecordedAt = time.Now().UTC()
	}
	if outcome.DateCreated.IsZero() {
		outcome.DateCreated = time.Now().UTC()
	}
	payloadJSON := "{}"
	if len(outcome.PayloadJSON) > 0 {
		payloadJSON = string(outcome.PayloadJSON)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO outcomes
			(id, project_id, event_id, category, reason, quantity, source, release, environment, payload_json, recorded_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		outcome.ID,
		outcome.ProjectID,
		nullIfEmpty(outcome.EventID),
		outcome.Category,
		outcome.Reason,
		outcome.Quantity,
		outcome.Source,
		nullIfEmpty(outcome.Release),
		nullIfEmpty(outcome.Environment),
		payloadJSON,
		outcome.RecordedAt.UTC().Format(time.RFC3339),
		outcome.DateCreated.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert outcome: %w", err)
	}
	return nil
}

// ListRecent returns recent outcomes for a project.
func (s *OutcomeStore) ListRecent(ctx context.Context, projectID string, limit int) ([]Outcome, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, COALESCE(event_id, ''), category, reason, quantity, source,
		        COALESCE(release, ''), COALESCE(environment, ''), COALESCE(payload_json, '{}'),
		        recorded_at, created_at
		 FROM outcomes
		 WHERE project_id = ?
		 ORDER BY recorded_at DESC, created_at DESC
		 LIMIT ?`,
		projectID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list outcomes: %w", err)
	}
	defer rows.Close()

	var outcomes []Outcome
	for rows.Next() {
		var item Outcome
		var payloadJSON, recordedAt, createdAt string
		if err := rows.Scan(
			&item.ID,
			&item.ProjectID,
			&item.EventID,
			&item.Category,
			&item.Reason,
			&item.Quantity,
			&item.Source,
			&item.Release,
			&item.Environment,
			&payloadJSON,
			&recordedAt,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan outcome: %w", err)
		}
		item.RecordedAt = parseTime(recordedAt)
		item.DateCreated = parseTime(createdAt)
		if strings.TrimSpace(payloadJSON) != "" && payloadJSON != "{}" {
			item.PayloadJSON = json.RawMessage(payloadJSON)
		}
		outcomes = append(outcomes, item)
	}
	return outcomes, rows.Err()
}
