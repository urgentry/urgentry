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

// ReleaseSession captures a single SDK session envelope item.
type ReleaseSession struct {
	ID          string            `json:"id"`
	ProjectID   string            `json:"projectId"`
	Release     string            `json:"release"`
	Environment string            `json:"environment,omitempty"`
	SessionID   string            `json:"sessionId,omitempty"`
	DistinctID  string            `json:"distinctId,omitempty"`
	Status      string            `json:"status"`
	Errors      int               `json:"errors"`
	StartedAt   time.Time         `json:"startedAt,omitempty"`
	Duration    float64           `json:"duration"`
	UserAgent   string            `json:"userAgent,omitempty"`
	Attrs       map[string]string `json:"attrs,omitempty"`
	Quantity    int               `json:"quantity"`
	DateCreated time.Time         `json:"dateCreated"`
}

// ReleaseHealthSummary aggregates release/session health for a release.
type ReleaseHealthSummary struct {
	ProjectID        string    `json:"projectId,omitempty"`
	ReleaseVersion   string    `json:"releaseVersion"`
	SessionCount     int       `json:"sessionCount"`
	ErroredSessions  int       `json:"erroredSessions"`
	CrashedSessions  int       `json:"crashedSessions"`
	AbnormalSessions int       `json:"abnormalSessions"`
	AffectedUsers    int       `json:"affectedUsers"`
	CrashFreeRate    float64   `json:"crashFreeRate"`
	LastSessionAt    time.Time `json:"lastSessionAt,omitempty"`
}

// ReleaseHealthStore persists release session items and produces health summaries.
type ReleaseHealthStore struct {
	db *sql.DB
}

// NewReleaseHealthStore creates a store backed by SQLite.
func NewReleaseHealthStore(db *sql.DB) *ReleaseHealthStore {
	return &ReleaseHealthStore{db: db}
}

// SaveSession records a single session item for later release health summaries.
func (s *ReleaseHealthStore) SaveSession(ctx context.Context, session *ReleaseSession) error {
	if session == nil {
		return nil
	}
	if session.ProjectID == "" || strings.TrimSpace(session.Release) == "" {
		return fmt.Errorf("session project_id and release are required")
	}
	if session.ID == "" {
		session.ID = id.New()
	}
	if session.Status == "" {
		session.Status = "ok"
	}
	if session.Quantity <= 0 {
		session.Quantity = 1
	}
	if session.DateCreated.IsZero() {
		session.DateCreated = time.Now().UTC()
	}
	if err := ensureReleaseForOwner(ctx, s.db, session.ProjectID, session.Release); err != nil {
		return fmt.Errorf("ensure release: %w", err)
	}
	attrsJSON := "{}"
	if len(session.Attrs) > 0 {
		data, err := json.Marshal(session.Attrs)
		if err != nil {
			return fmt.Errorf("marshal session attrs: %w", err)
		}
		attrsJSON = string(data)
	}

	var startedAt any
	if !session.StartedAt.IsZero() {
		startedAt = session.StartedAt.UTC().Format(time.RFC3339)
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO release_sessions
			(id, project_id, release_version, environment, session_id, distinct_id, status, errors, started_at, duration, user_agent, attrs_json, quantity, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID,
		session.ProjectID,
		session.Release,
		session.Environment,
		nullIfEmpty(session.SessionID),
		nullIfEmpty(session.DistinctID),
		session.Status,
		session.Errors,
		startedAt,
		session.Duration,
		nullIfEmpty(session.UserAgent),
		attrsJSON,
		session.Quantity,
		session.DateCreated.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert release session: %w", err)
	}
	return nil
}

// GetReleaseHealth returns aggregated health for a specific project release.
func (s *ReleaseHealthStore) GetReleaseHealth(ctx context.Context, projectID, releaseVersion string) (*ReleaseHealthSummary, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT project_id,
		        release_version,
		        COALESCE(SUM(quantity), 0),
		        COALESCE(SUM(CASE WHEN status = 'errored' THEN quantity ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN status = 'crashed' THEN quantity ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN status = 'abnormal' THEN quantity ELSE 0 END), 0),
		        COUNT(DISTINCT CASE WHEN distinct_id IS NOT NULL AND distinct_id != '' THEN distinct_id END),
		        MAX(created_at)
		 FROM release_sessions
		 WHERE project_id = ? AND release_version = ?
		 GROUP BY project_id, release_version`,
		projectID, releaseVersion,
	)

	var summary ReleaseHealthSummary
	var lastSessionAt sql.NullString
	if err := row.Scan(
		&summary.ProjectID,
		&summary.ReleaseVersion,
		&summary.SessionCount,
		&summary.ErroredSessions,
		&summary.CrashedSessions,
		&summary.AbnormalSessions,
		&summary.AffectedUsers,
		&lastSessionAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return &ReleaseHealthSummary{
				ProjectID:      projectID,
				ReleaseVersion: releaseVersion,
				CrashFreeRate:  100,
			}, nil
		}
		return nil, fmt.Errorf("query release health: %w", err)
	}
	summary.LastSessionAt = parseTime(nullStr(lastSessionAt))
	summary.CrashFreeRate = crashFreeRate(summary.SessionCount, summary.CrashedSessions, summary.AbnormalSessions)
	return &summary, nil
}

// ListReleaseSessions returns recent session items for a release.
func (s *ReleaseHealthStore) ListReleaseSessions(ctx context.Context, projectID, releaseVersion string, limit int) ([]ReleaseSession, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, release_version, environment, COALESCE(session_id, ''), COALESCE(distinct_id, ''), status, errors,
		        COALESCE(started_at, ''), duration, COALESCE(user_agent, ''), COALESCE(attrs_json, '{}'), quantity, created_at
		 FROM release_sessions
		 WHERE project_id = ? AND release_version = ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		projectID, releaseVersion, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list release sessions: %w", err)
	}
	defer rows.Close()

	var sessions []ReleaseSession
	for rows.Next() {
		var item ReleaseSession
		var attrsJSON, startedAt, createdAt string
		if err := rows.Scan(
			&item.ID,
			&item.ProjectID,
			&item.Release,
			&item.Environment,
			&item.SessionID,
			&item.DistinctID,
			&item.Status,
			&item.Errors,
			&startedAt,
			&item.Duration,
			&item.UserAgent,
			&attrsJSON,
			&item.Quantity,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan release session: %w", err)
		}
		item.StartedAt = parseTime(startedAt)
		item.DateCreated = parseTime(createdAt)
		if strings.TrimSpace(attrsJSON) != "" && attrsJSON != "{}" {
			_ = json.Unmarshal([]byte(attrsJSON), &item.Attrs)
		}
		sessions = append(sessions, item)
	}
	return sessions, rows.Err()
}

// ListOrganizationReleaseHealth returns aggregated health for the newest releases in an organization.
func (s *ReleaseHealthStore) ListOrganizationReleaseHealth(ctx context.Context, orgSlug string, limit int) ([]ReleaseHealthSummary, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.version,
		        COALESCE(SUM(rs.quantity), 0),
		        COALESCE(SUM(CASE WHEN rs.status = 'errored' THEN rs.quantity ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN rs.status = 'crashed' THEN rs.quantity ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN rs.status = 'abnormal' THEN rs.quantity ELSE 0 END), 0),
		        COUNT(DISTINCT CASE WHEN rs.distinct_id IS NOT NULL AND rs.distinct_id != '' THEN rs.distinct_id END),
		        MAX(rs.created_at)
		 FROM releases r
		 JOIN organizations o ON o.id = r.organization_id
		 LEFT JOIN projects p ON p.organization_id = o.id
		 LEFT JOIN release_sessions rs ON rs.project_id = p.id AND rs.release_version = r.version
		 WHERE o.slug = ?
		 GROUP BY r.version, r.created_at
		 ORDER BY r.created_at DESC
		 LIMIT ?`,
		orgSlug, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list org release health: %w", err)
	}
	defer rows.Close()

	var summaries []ReleaseHealthSummary
	for rows.Next() {
		var summary ReleaseHealthSummary
		var lastSessionAt sql.NullString
		if err := rows.Scan(
			&summary.ReleaseVersion,
			&summary.SessionCount,
			&summary.ErroredSessions,
			&summary.CrashedSessions,
			&summary.AbnormalSessions,
			&summary.AffectedUsers,
			&lastSessionAt,
		); err != nil {
			return nil, fmt.Errorf("scan org release health: %w", err)
		}
		summary.LastSessionAt = parseTime(nullStr(lastSessionAt))
		summary.CrashFreeRate = crashFreeRate(summary.SessionCount, summary.CrashedSessions, summary.AbnormalSessions)
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

func crashFreeRate(sessionCount, crashed, abnormal int) float64 {
	if sessionCount <= 0 {
		return 100
	}
	bad := crashed + abnormal
	if bad < 0 {
		bad = 0
	}
	if bad > sessionCount {
		bad = sessionCount
	}
	return (float64(sessionCount-bad) / float64(sessionCount)) * 100
}
