package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"urgentry/internal/domain"
	"urgentry/pkg/id"
)

// InboundFilterStore persists inbound filter rules in SQLite.
type InboundFilterStore struct {
	db *sql.DB
}

// NewInboundFilterStore creates a store backed by the given database.
func NewInboundFilterStore(db *sql.DB) *InboundFilterStore {
	return &InboundFilterStore{db: db}
}

// CreateFilter inserts a new inbound filter.
func (s *InboundFilterStore) CreateFilter(ctx context.Context, f *domain.InboundFilter) error {
	if f.ID == "" {
		f.ID = id.New()
	}
	now := time.Now().UTC()
	if f.CreatedAt.IsZero() {
		f.CreatedAt = now
	}
	f.UpdatedAt = now

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO inbound_filters (id, project_id, type, active, pattern, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.ProjectID, string(f.Type), f.Active, f.Pattern,
		f.CreatedAt.Format(time.RFC3339), f.UpdatedAt.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert inbound filter: %w", err)
	}
	return nil
}

// GetFilter returns a single inbound filter by ID.
func (s *InboundFilterStore) GetFilter(ctx context.Context, filterID string) (*domain.InboundFilter, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, type, active, COALESCE(pattern, ''), created_at, updated_at
		 FROM inbound_filters WHERE id = ?`, filterID)

	f, err := scanInboundFilter(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("inbound filter not found: %s", filterID)
		}
		return nil, fmt.Errorf("get inbound filter: %w", err)
	}
	return f, nil
}

// ListFilters returns all inbound filters for a project.
func (s *InboundFilterStore) ListFilters(ctx context.Context, projectID string) ([]*domain.InboundFilter, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, type, active, COALESCE(pattern, ''), created_at, updated_at
		 FROM inbound_filters WHERE project_id = ? ORDER BY created_at`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list inbound filters: %w", err)
	}
	defer rows.Close()

	var filters []*domain.InboundFilter
	for rows.Next() {
		f, err := scanInboundFilterRows(rows)
		if err != nil {
			return nil, fmt.Errorf("scan inbound filter: %w", err)
		}
		filters = append(filters, f)
	}
	return filters, rows.Err()
}

// UpdateFilter updates an existing inbound filter.
func (s *InboundFilterStore) UpdateFilter(ctx context.Context, f *domain.InboundFilter) error {
	f.UpdatedAt = time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`UPDATE inbound_filters SET type = ?, active = ?, pattern = ?, updated_at = ?
		 WHERE id = ?`,
		string(f.Type), f.Active, f.Pattern, f.UpdatedAt.Format(time.RFC3339), f.ID,
	)
	if err != nil {
		return fmt.Errorf("update inbound filter: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("inbound filter not found: %s", f.ID)
	}
	return nil
}

// DeleteFilter removes an inbound filter by ID.
func (s *InboundFilterStore) DeleteFilter(ctx context.Context, filterID string) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM inbound_filters WHERE id = ?`, filterID)
	if err != nil {
		return fmt.Errorf("delete inbound filter: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("inbound filter not found: %s", filterID)
	}
	return nil
}

func scanInboundFilter(row *sql.Row) (*domain.InboundFilter, error) {
	var f domain.InboundFilter
	var filterType, createdAt, updatedAt string
	if err := row.Scan(&f.ID, &f.ProjectID, &filterType, &f.Active, &f.Pattern, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	f.Type = domain.FilterType(filterType)
	f.CreatedAt = parseTime(createdAt)
	f.UpdatedAt = parseTime(updatedAt)
	return &f, nil
}

func scanInboundFilterRows(rows *sql.Rows) (*domain.InboundFilter, error) {
	var f domain.InboundFilter
	var filterType, createdAt, updatedAt string
	if err := rows.Scan(&f.ID, &f.ProjectID, &filterType, &f.Active, &f.Pattern, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	f.Type = domain.FilterType(filterType)
	f.CreatedAt = parseTime(createdAt)
	f.UpdatedAt = parseTime(updatedAt)
	return &f, nil
}
