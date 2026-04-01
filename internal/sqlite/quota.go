package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// QuotaUsage summarizes current event/transaction ingestion counts.
type QuotaUsage struct {
	ProjectID         string `json:"projectId"`
	ProjectSlug       string `json:"projectSlug,omitempty"`
	EventsIngested    int64  `json:"eventsIngested"`
	TransactionsCount int64  `json:"transactionsIngested"`
	EventsRejected    int64  `json:"eventsRejected"`
}

// QuotaRateLimit is a per-project rate limit override.
type QuotaRateLimit struct {
	ID                   string    `json:"id"`
	ProjectID            string    `json:"projectId"`
	MaxEventsPerHour     int       `json:"maxEventsPerHour"`
	MaxTransPerHour      int       `json:"maxTransactionsPerHour"`
	DateCreated          time.Time `json:"dateCreated"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

// QuotaStore aggregates quota data from outcomes and events.
type QuotaStore struct {
	db *sql.DB
}

// NewQuotaStore creates a new QuotaStore.
func NewQuotaStore(db *sql.DB) *QuotaStore {
	return &QuotaStore{db: db}
}

// GetUsage returns aggregated event and transaction counts for a project
// within the given time range.
func (s *QuotaStore) GetUsage(ctx context.Context, projectID string, since time.Time) (*QuotaUsage, error) {
	usage := &QuotaUsage{ProjectID: projectID}

	// Count ingested events.
	row := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE project_id = ? AND event_type = 'error' AND ingested_at >= ?`,
		projectID, since.UTC().Format(time.RFC3339),
	)
	if err := row.Scan(&usage.EventsIngested); err != nil {
		return nil, fmt.Errorf("count events: %w", err)
	}

	// Count ingested transactions.
	row = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM transactions WHERE project_id = ? AND created_at >= ?`,
		projectID, since.UTC().Format(time.RFC3339),
	)
	if err := row.Scan(&usage.TransactionsCount); err != nil {
		return nil, fmt.Errorf("count transactions: %w", err)
	}

	// Count rejected events from outcomes.
	row = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(quantity), 0) FROM outcomes WHERE project_id = ? AND recorded_at >= ?`,
		projectID, since.UTC().Format(time.RFC3339),
	)
	if err := row.Scan(&usage.EventsRejected); err != nil {
		return nil, fmt.Errorf("count rejected: %w", err)
	}

	return usage, nil
}

// GetAllProjectUsage returns usage summaries for all projects.
func (s *QuotaStore) GetAllProjectUsage(ctx context.Context, since time.Time) ([]QuotaUsage, error) {
	sinceStr := since.UTC().Format(time.RFC3339)

	rows, err := s.db.QueryContext(ctx,
		`SELECT p.id, p.slug,
			COALESCE((SELECT COUNT(*) FROM events e WHERE e.project_id = p.id AND e.event_type = 'error' AND e.ingested_at >= ?), 0),
			COALESCE((SELECT COUNT(*) FROM transactions t WHERE t.project_id = p.id AND t.created_at >= ?), 0),
			COALESCE((SELECT SUM(o.quantity) FROM outcomes o WHERE o.project_id = p.id AND o.recorded_at >= ?), 0)
		 FROM projects p
		 ORDER BY p.slug ASC`,
		sinceStr, sinceStr, sinceStr,
	)
	if err != nil {
		return nil, fmt.Errorf("list project usage: %w", err)
	}
	defer rows.Close()
	var out []QuotaUsage
	for rows.Next() {
		var u QuotaUsage
		if err := rows.Scan(&u.ProjectID, &u.ProjectSlug, &u.EventsIngested, &u.TransactionsCount, &u.EventsRejected); err != nil {
			return nil, fmt.Errorf("scan project usage: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpsertRateLimit creates or updates a per-project rate limit override.
func (s *QuotaStore) UpsertRateLimit(ctx context.Context, limit *QuotaRateLimit) (*QuotaRateLimit, error) {
	if limit == nil {
		return nil, nil
	}
	if strings.TrimSpace(limit.ProjectID) == "" {
		return nil, fmt.Errorf("rate limit project_id is required")
	}
	if limit.ID == "" {
		limit.ID = generateID()
	}
	now := time.Now().UTC()
	if limit.DateCreated.IsZero() {
		limit.DateCreated = now
	}
	limit.UpdatedAt = now

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO quota_rate_limits
			(id, project_id, max_events_per_hour, max_transactions_per_hour, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(project_id) DO UPDATE SET
			max_events_per_hour = excluded.max_events_per_hour,
			max_transactions_per_hour = excluded.max_transactions_per_hour,
			updated_at = excluded.updated_at`,
		limit.ID,
		limit.ProjectID,
		limit.MaxEventsPerHour,
		limit.MaxTransPerHour,
		limit.DateCreated.UTC().Format(time.RFC3339),
		limit.UpdatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("upsert quota rate limit: %w", err)
	}
	return limit, nil
}

// GetRateLimit returns the rate limit override for a project.
func (s *QuotaStore) GetRateLimit(ctx context.Context, projectID string) (*QuotaRateLimit, error) {
	var limit QuotaRateLimit
	var createdAt, updatedAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, max_events_per_hour, max_transactions_per_hour, created_at, updated_at
		 FROM quota_rate_limits WHERE project_id = ?`, projectID,
	).Scan(
		&limit.ID, &limit.ProjectID,
		&limit.MaxEventsPerHour, &limit.MaxTransPerHour,
		&createdAt, &updatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get quota rate limit: %w", err)
	}
	limit.DateCreated = parseTime(createdAt)
	limit.UpdatedAt = parseTime(updatedAt)
	return &limit, nil
}

// ListRateLimits returns all per-project rate limit overrides.
func (s *QuotaStore) ListRateLimits(ctx context.Context) ([]QuotaRateLimit, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, max_events_per_hour, max_transactions_per_hour, created_at, updated_at
		 FROM quota_rate_limits ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list quota rate limits: %w", err)
	}
	defer rows.Close()
	var out []QuotaRateLimit
	for rows.Next() {
		var limit QuotaRateLimit
		var createdAt, updatedAt string
		if err := rows.Scan(
			&limit.ID, &limit.ProjectID,
			&limit.MaxEventsPerHour, &limit.MaxTransPerHour,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan quota rate limit: %w", err)
		}
		limit.DateCreated = parseTime(createdAt)
		limit.UpdatedAt = parseTime(updatedAt)
		out = append(out, limit)
	}
	return out, rows.Err()
}

// DeleteRateLimit removes a per-project rate limit override.
func (s *QuotaStore) DeleteRateLimit(ctx context.Context, projectID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM quota_rate_limits WHERE project_id = ?`, projectID)
	if err != nil {
		return fmt.Errorf("delete quota rate limit: %w", err)
	}
	return nil
}
