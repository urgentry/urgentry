package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"urgentry/pkg/id"
)

// SymbolSource represents an external debug symbol repository (S3, GCS, HTTP).
type SymbolSource struct {
	ID              string          `json:"id"`
	ProjectID       string          `json:"projectId"`
	Type            string          `json:"type"` // "s3", "gcs", "http"
	Name            string          `json:"name"`
	Layout          json.RawMessage `json:"layout"`
	URL             string          `json:"url"`
	CredentialsJSON string          `json:"-"`
	CreatedAt       time.Time       `json:"dateCreated"`
}

// SymbolSourceLayout describes the directory layout of a symbol source.
type SymbolSourceLayout struct {
	Type   string `json:"type"`
	Casing string `json:"casing"`
}

// SymbolSourceResponse is the API response shape for a symbol source.
type SymbolSourceResponse struct {
	ID     string             `json:"id"`
	Type   string             `json:"type"`
	Name   string             `json:"name"`
	Layout SymbolSourceLayout `json:"layout"`
	URL    string             `json:"url"`
}

// ToResponse converts a SymbolSource to its API response shape.
func (s *SymbolSource) ToResponse() SymbolSourceResponse {
	var layout SymbolSourceLayout
	if len(s.Layout) > 0 {
		_ = json.Unmarshal(s.Layout, &layout)
	}
	if layout.Type == "" {
		layout.Type = "native"
	}
	if layout.Casing == "" {
		layout.Casing = "default"
	}
	return SymbolSourceResponse{
		ID:     s.ID,
		Type:   s.Type,
		Name:   s.Name,
		Layout: layout,
		URL:    s.URL,
	}
}

// SymbolSourceStore persists external debug symbol source configurations.
type SymbolSourceStore struct {
	db *sql.DB
}

// NewSymbolSourceStore creates a new SymbolSourceStore.
func NewSymbolSourceStore(db *sql.DB) *SymbolSourceStore {
	return &SymbolSourceStore{db: db}
}

// List returns all symbol sources for a project.
func (s *SymbolSourceStore) List(ctx context.Context, projectID string) ([]*SymbolSource, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, type, name, layout_json, url, credentials_json, created_at
		 FROM project_symbol_sources
		 WHERE project_id = ?
		 ORDER BY created_at DESC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list symbol sources: %w", err)
	}
	defer rows.Close()

	var result []*SymbolSource
	for rows.Next() {
		ss, err := scanSymbolSource(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, ss)
	}
	return result, rows.Err()
}

// Create inserts a new symbol source.
func (s *SymbolSourceStore) Create(ctx context.Context, ss *SymbolSource) (*SymbolSource, error) {
	if ss == nil {
		return nil, nil
	}
	if strings.TrimSpace(ss.ProjectID) == "" {
		return nil, fmt.Errorf("symbol source project_id is required")
	}
	if strings.TrimSpace(ss.Type) == "" {
		return nil, fmt.Errorf("symbol source type is required")
	}
	if strings.TrimSpace(ss.Name) == "" {
		return nil, fmt.Errorf("symbol source name is required")
	}
	if ss.ID == "" {
		ss.ID = id.New()
	}
	now := time.Now().UTC()
	if ss.CreatedAt.IsZero() {
		ss.CreatedAt = now
	}

	layoutJSON := "{}"
	if len(ss.Layout) > 0 {
		layoutJSON = string(ss.Layout)
	}
	credJSON := "{}"
	if ss.CredentialsJSON != "" {
		credJSON = ss.CredentialsJSON
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO project_symbol_sources
			(id, project_id, type, name, layout_json, url, credentials_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ss.ID, ss.ProjectID, ss.Type, ss.Name, layoutJSON, ss.URL, credJSON,
		ss.CreatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("insert symbol source: %w", err)
	}
	return ss, nil
}

// Update modifies an existing symbol source.
func (s *SymbolSourceStore) Update(ctx context.Context, ss *SymbolSource) (*SymbolSource, error) {
	if ss == nil || ss.ID == "" {
		return nil, fmt.Errorf("symbol source id is required")
	}

	layoutJSON := "{}"
	if len(ss.Layout) > 0 {
		layoutJSON = string(ss.Layout)
	}
	credJSON := "{}"
	if ss.CredentialsJSON != "" {
		credJSON = ss.CredentialsJSON
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE project_symbol_sources
		 SET type = ?, name = ?, layout_json = ?, url = ?, credentials_json = ?
		 WHERE id = ? AND project_id = ?`,
		ss.Type, ss.Name, layoutJSON, ss.URL, credJSON,
		ss.ID, ss.ProjectID,
	)
	if err != nil {
		return nil, fmt.Errorf("update symbol source: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, nil
	}
	return ss, nil
}

// Delete removes a symbol source by ID for a given project.
func (s *SymbolSourceStore) Delete(ctx context.Context, projectID, sourceID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM project_symbol_sources WHERE id = ? AND project_id = ?`,
		sourceID, projectID,
	)
	if err != nil {
		return fmt.Errorf("delete symbol source: %w", err)
	}
	return nil
}

type symbolSourceScanner interface {
	Scan(dest ...any) error
}

func scanSymbolSource(scanner symbolSourceScanner) (*SymbolSource, error) {
	var ss SymbolSource
	var layoutJSON, credJSON, createdAt sql.NullString
	if err := scanner.Scan(&ss.ID, &ss.ProjectID, &ss.Type, &ss.Name, &layoutJSON, &ss.URL, &credJSON, &createdAt); err != nil {
		return nil, fmt.Errorf("scan symbol source: %w", err)
	}
	if layoutJSON.Valid && layoutJSON.String != "" {
		ss.Layout = json.RawMessage(layoutJSON.String)
	}
	if credJSON.Valid {
		ss.CredentialsJSON = credJSON.String
	}
	if createdAt.Valid {
		ss.CreatedAt = parseTime(createdAt.String)
	}
	return &ss, nil
}
