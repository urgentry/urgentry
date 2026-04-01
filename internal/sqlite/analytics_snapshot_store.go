package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const analyticsSnapshotTTL = 30 * 24 * time.Hour

var ErrAnalyticsSnapshotNotFound = errors.New("analytics snapshot not found")

type AnalyticsSnapshot struct {
	ID               string    `json:"id"`
	OrganizationSlug string    `json:"organizationSlug"`
	SourceType       string    `json:"sourceType"`
	SourceID         string    `json:"sourceId"`
	Title            string    `json:"title"`
	ShareToken       string    `json:"shareToken"`
	CreatedByUserID  string    `json:"createdByUserId"`
	CreatedAt        time.Time `json:"createdAt"`
	ExpiresAt        time.Time `json:"expiresAt"`
	Body             SnapshotBody
}

type SnapshotBody struct {
	ViewType      string     `json:"viewType"`
	Columns       []string   `json:"columns,omitempty"`
	Rows          [][]string `json:"rows,omitempty"`
	StatLabel     string     `json:"statLabel,omitempty"`
	StatValue     string     `json:"statValue,omitempty"`
	SourceLabel   string     `json:"sourceLabel,omitempty"`
	Dataset       string     `json:"dataset,omitempty"`
	Visualization string     `json:"visualization,omitempty"`
	Filters       []string   `json:"filters,omitempty"`
	CostLabel     string     `json:"costLabel,omitempty"`
	QueryJSON     string     `json:"queryJson,omitempty"`
}

type AnalyticsSnapshotStore struct {
	db *sql.DB
}

func NewAnalyticsSnapshotStore(db *sql.DB) *AnalyticsSnapshotStore {
	return &AnalyticsSnapshotStore{db: db}
}

func (s *AnalyticsSnapshotStore) Create(ctx context.Context, organizationSlug, userID, sourceType, sourceID, title string, body SnapshotBody) (*AnalyticsSnapshot, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("analytics snapshot store unavailable")
	}
	_ = s.DeleteExpired(ctx)
	now := time.Now().UTC()
	item := &AnalyticsSnapshot{
		ID:               generateID(),
		OrganizationSlug: organizationSlug,
		SourceType:       sourceType,
		SourceID:         sourceID,
		Title:            title,
		ShareToken:       generateID(),
		CreatedByUserID:  userID,
		CreatedAt:        now,
		ExpiresAt:        now.Add(analyticsSnapshotTTL),
		Body:             body,
	}
	bodyJSON, err := json.Marshal(item.Body)
	if err != nil {
		return nil, fmt.Errorf("marshal analytics snapshot body: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO analytics_snapshots
			(id, organization_slug, source_type, source_id, title, share_token, payload_json, created_by_user_id, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID,
		item.OrganizationSlug,
		item.SourceType,
		item.SourceID,
		item.Title,
		item.ShareToken,
		string(bodyJSON),
		item.CreatedByUserID,
		item.CreatedAt.Format(time.RFC3339),
		item.ExpiresAt.Format(time.RFC3339),
	); err != nil {
		return nil, fmt.Errorf("insert analytics snapshot: %w", err)
	}
	return item, nil
}

func (s *AnalyticsSnapshotStore) GetByShareToken(ctx context.Context, token string) (*AnalyticsSnapshot, error) {
	if s == nil || s.db == nil {
		return nil, ErrAnalyticsSnapshotNotFound
	}
	_ = s.DeleteExpired(ctx)
	var item AnalyticsSnapshot
	var payloadJSON, createdAt, expiresAt string
	if err := s.db.QueryRowContext(ctx,
		`SELECT id, organization_slug, source_type, source_id, title, share_token, payload_json, created_by_user_id, created_at, expires_at
		 FROM analytics_snapshots
		 WHERE share_token = ?`,
		token,
	).Scan(
		&item.ID,
		&item.OrganizationSlug,
		&item.SourceType,
		&item.SourceID,
		&item.Title,
		&item.ShareToken,
		&payloadJSON,
		&item.CreatedByUserID,
		&createdAt,
		&expiresAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAnalyticsSnapshotNotFound
		}
		return nil, fmt.Errorf("load analytics snapshot: %w", err)
	}
	if err := json.Unmarshal([]byte(payloadJSON), &item.Body); err != nil {
		return nil, fmt.Errorf("decode analytics snapshot body: %w", err)
	}
	item.CreatedAt = parseTime(createdAt)
	item.ExpiresAt = parseTime(expiresAt)
	if !item.ExpiresAt.IsZero() && time.Now().UTC().After(item.ExpiresAt) {
		return nil, ErrAnalyticsSnapshotNotFound
	}
	return &item, nil
}

func (s *AnalyticsSnapshotStore) DeleteExpired(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM analytics_snapshots WHERE expires_at != '' AND expires_at < ?`, time.Now().UTC().Format(time.RFC3339))
	return err
}
