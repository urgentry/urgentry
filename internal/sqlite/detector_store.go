package sqlite

import (
	"context"
	"database/sql"
	"time"

	"urgentry/internal/store"
)

// DetectorStore is a SQLite-backed implementation of store.DetectorStore.
type DetectorStore struct {
	db *sql.DB
}

// NewDetectorStore creates a DetectorStore backed by the given database.
func NewDetectorStore(db *sql.DB) *DetectorStore {
	return &DetectorStore{db: db}
}

// CreateDetector inserts a new detector row.
func (s *DetectorStore) CreateDetector(ctx context.Context, d *store.Detector) error {
	if d.ID == "" {
		d.ID = generateID()
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	if d.State == "" {
		d.State = "active"
	}
	if d.ConfigJSON == "" {
		d.ConfigJSON = "{}"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO detectors (id, org_id, name, type, config_json, state, owner_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.OrgID, d.Name, d.Type, d.ConfigJSON, d.State, d.OwnerID,
		d.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

// ListDetectors returns all detectors for the given organization.
func (s *DetectorStore) ListDetectors(ctx context.Context, orgID string) ([]*store.Detector, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, name, type, config_json, state, owner_id, created_at
		 FROM detectors WHERE org_id = ? ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*store.Detector
	for rows.Next() {
		d, err := scanDetectorRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDetector returns a single detector by ID.
func (s *DetectorStore) GetDetector(ctx context.Context, id string) (*store.Detector, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, name, type, config_json, state, owner_id, created_at
		 FROM detectors WHERE id = ?`, id)
	var d store.Detector
	var createdAt string
	err := row.Scan(&d.ID, &d.OrgID, &d.Name, &d.Type, &d.ConfigJSON, &d.State, &d.OwnerID, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &d, nil
}

// UpdateDetector updates a detector row by ID.
func (s *DetectorStore) UpdateDetector(ctx context.Context, d *store.Detector) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE detectors SET name = ?, type = ?, config_json = ?, state = ?, owner_id = ?
		 WHERE id = ?`,
		d.Name, d.Type, d.ConfigJSON, d.State, d.OwnerID, d.ID)
	return err
}

// DeleteDetector removes a detector row by ID.
func (s *DetectorStore) DeleteDetector(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM detectors WHERE id = ?`, id)
	return err
}

// BulkUpdateDetectors updates the state of multiple detectors by ID within an org.
func (s *DetectorStore) BulkUpdateDetectors(ctx context.Context, orgID string, ids []string, state string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE detectors SET state = ? WHERE id = ? AND org_id = ?`,
			state, id, orgID); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// BulkDeleteDetectors deletes multiple detectors by ID within an org.
func (s *DetectorStore) BulkDeleteDetectors(ctx context.Context, orgID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM detectors WHERE id = ? AND org_id = ?`,
			id, orgID); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func scanDetectorRow(rows *sql.Rows) (*store.Detector, error) {
	var d store.Detector
	var createdAt string
	err := rows.Scan(&d.ID, &d.OrgID, &d.Name, &d.Type, &d.ConfigJSON, &d.State, &d.OwnerID, &createdAt)
	if err != nil {
		return nil, err
	}
	d.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &d, nil
}
