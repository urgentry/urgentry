package sqlite

import (
	"context"
	"database/sql"

	"urgentry/internal/store"
)

// Compile-time interface check.
var _ store.WebStore = (*WebStore)(nil)

// WebStore implements store.WebStore backed by SQLite.
// All count methods return (int, error) with no silent error swallowing.
type WebStore struct {
	db *sql.DB
}

// NewWebStore creates a new WebStore.
func NewWebStore(db *sql.DB) *WebStore {
	return &WebStore{db: db}
}

func (s *WebStore) count(ctx context.Context, query string, args ...any) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}
