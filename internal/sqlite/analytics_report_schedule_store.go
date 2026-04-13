package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrAnalyticsReportScheduleNotFound = errors.New("analytics report schedule not found")

type AnalyticsReportCadence string

const (
	AnalyticsReportCadenceDaily  AnalyticsReportCadence = "daily"
	AnalyticsReportCadenceWeekly AnalyticsReportCadence = "weekly"
)

type AnalyticsReportSchedule struct {
	ID                string
	OrganizationSlug  string
	SourceType        string
	SourceID          string
	CreatedByUserID   string
	Recipient         string
	Cadence           AnalyticsReportCadence
	CreatedAt         time.Time
	UpdatedAt         time.Time
	LastAttemptAt     *time.Time
	LastRunAt         *time.Time
	NextRunAt         time.Time
	LastSnapshotToken string
	LastError         string
}

type AnalyticsReportScheduleStore struct {
	db *sql.DB
}

func NewAnalyticsReportScheduleStore(db *sql.DB) *AnalyticsReportScheduleStore {
	return &AnalyticsReportScheduleStore{db: db}
}

func (s *AnalyticsReportScheduleStore) Create(ctx context.Context, organizationSlug, sourceType, sourceID, userID, recipient string, cadence AnalyticsReportCadence) (*AnalyticsReportSchedule, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("analytics report schedules unavailable")
	}
	orgSlug := strings.TrimSpace(organizationSlug)
	sourceType = strings.TrimSpace(sourceType)
	sourceID = strings.TrimSpace(sourceID)
	userID = strings.TrimSpace(userID)
	recipient = strings.TrimSpace(recipient)
	if orgSlug == "" || sourceType == "" || sourceID == "" || userID == "" {
		return nil, fmt.Errorf("analytics report schedule source is required")
	}
	if recipient == "" || !strings.Contains(recipient, "@") {
		return nil, fmt.Errorf("report recipient must be a valid email address")
	}
	cadence = normalizeAnalyticsReportCadence(cadence)
	now := time.Now().UTC()
	item := &AnalyticsReportSchedule{
		ID:               generateID(),
		OrganizationSlug: orgSlug,
		SourceType:       sourceType,
		SourceID:         sourceID,
		CreatedByUserID:  userID,
		Recipient:        recipient,
		Cadence:          cadence,
		CreatedAt:        now,
		UpdatedAt:        now,
		NextRunAt:        now,
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO analytics_report_schedules
			(id, organization_slug, source_type, source_id, created_by_user_id, recipient, cadence, created_at, updated_at, next_run_at, last_snapshot_token, last_error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '')`,
		item.ID,
		item.OrganizationSlug,
		item.SourceType,
		item.SourceID,
		item.CreatedByUserID,
		item.Recipient,
		string(item.Cadence),
		item.CreatedAt.Format(time.RFC3339Nano),
		item.UpdatedAt.Format(time.RFC3339Nano),
		item.NextRunAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("insert analytics report schedule: %w", err)
	}
	return item, nil
}

func (s *AnalyticsReportScheduleStore) ListBySource(ctx context.Context, organizationSlug, sourceType, sourceID, userID string) ([]AnalyticsReportSchedule, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, organization_slug, source_type, source_id, created_by_user_id, recipient, cadence, created_at, updated_at,
		        last_attempt_at, last_run_at, next_run_at, last_snapshot_token, last_error
		   FROM analytics_report_schedules
		  WHERE organization_slug = ? AND source_type = ? AND source_id = ? AND created_by_user_id = ?
		  ORDER BY created_at DESC`,
		strings.TrimSpace(organizationSlug),
		strings.TrimSpace(sourceType),
		strings.TrimSpace(sourceID),
		strings.TrimSpace(userID),
	)
	if err != nil {
		return nil, fmt.Errorf("list analytics report schedules: %w", err)
	}
	defer rows.Close()
	var items []AnalyticsReportSchedule
	for rows.Next() {
		item, err := scanAnalyticsReportSchedule(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *AnalyticsReportScheduleStore) ListDue(ctx context.Context, now time.Time, limit int) ([]AnalyticsReportSchedule, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 25
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, organization_slug, source_type, source_id, created_by_user_id, recipient, cadence, created_at, updated_at,
		        last_attempt_at, last_run_at, next_run_at, last_snapshot_token, last_error
		   FROM analytics_report_schedules
		  WHERE next_run_at != '' AND next_run_at <= ?
		  ORDER BY next_run_at ASC, created_at ASC
		  LIMIT ?`,
		now.UTC().Format(time.RFC3339Nano),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list due analytics report schedules: %w", err)
	}
	defer rows.Close()
	var items []AnalyticsReportSchedule
	for rows.Next() {
		item, err := scanAnalyticsReportSchedule(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *AnalyticsReportScheduleStore) Delete(ctx context.Context, organizationSlug, userID, id string) error {
	if s == nil || s.db == nil {
		return ErrAnalyticsReportScheduleNotFound
	}
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM analytics_report_schedules
		  WHERE id = ? AND organization_slug = ? AND created_by_user_id = ?`,
		strings.TrimSpace(id),
		strings.TrimSpace(organizationSlug),
		strings.TrimSpace(userID),
	)
	if err != nil {
		return fmt.Errorf("delete analytics report schedule: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrAnalyticsReportScheduleNotFound
	}
	return nil
}

func (s *AnalyticsReportScheduleStore) MarkDelivered(ctx context.Context, id string, attemptedAt time.Time, cadence AnalyticsReportCadence, snapshotToken string) error {
	return s.updateRunState(ctx, id, attemptedAt, cadence, strings.TrimSpace(snapshotToken), "")
}

func (s *AnalyticsReportScheduleStore) MarkFailed(ctx context.Context, id string, attemptedAt time.Time, cadence AnalyticsReportCadence, errText string) error {
	return s.updateRunState(ctx, id, attemptedAt, cadence, "", strings.TrimSpace(errText))
}

func NextAnalyticsReportRun(now time.Time, cadence AnalyticsReportCadence) time.Time {
	now = now.UTC()
	switch normalizeAnalyticsReportCadence(cadence) {
	case AnalyticsReportCadenceWeekly:
		return now.Add(7 * 24 * time.Hour)
	default:
		return now.Add(24 * time.Hour)
	}
}

func normalizeAnalyticsReportCadence(cadence AnalyticsReportCadence) AnalyticsReportCadence {
	switch strings.ToLower(strings.TrimSpace(string(cadence))) {
	case string(AnalyticsReportCadenceWeekly):
		return AnalyticsReportCadenceWeekly
	default:
		return AnalyticsReportCadenceDaily
	}
}

func (s *AnalyticsReportScheduleStore) updateRunState(ctx context.Context, id string, attemptedAt time.Time, cadence AnalyticsReportCadence, snapshotToken, errText string) error {
	if s == nil || s.db == nil {
		return ErrAnalyticsReportScheduleNotFound
	}
	attemptedAt = attemptedAt.UTC()
	nextRunAt := NextAnalyticsReportRun(attemptedAt, cadence)
	lastRunAt := ""
	if strings.TrimSpace(errText) == "" {
		lastRunAt = attemptedAt.Format(time.RFC3339Nano)
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE analytics_report_schedules
		    SET updated_at = ?,
		        last_attempt_at = ?,
		        last_run_at = ?,
		        next_run_at = ?,
		        last_snapshot_token = ?,
		        last_error = ?
		  WHERE id = ?`,
		attemptedAt.Format(time.RFC3339Nano),
		attemptedAt.Format(time.RFC3339Nano),
		lastRunAt,
		nextRunAt.Format(time.RFC3339Nano),
		strings.TrimSpace(snapshotToken),
		strings.TrimSpace(errText),
		strings.TrimSpace(id),
	)
	if err != nil {
		return fmt.Errorf("update analytics report schedule: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrAnalyticsReportScheduleNotFound
	}
	return nil
}

func scanAnalyticsReportSchedule(scanner interface {
	Scan(dest ...any) error
}) (AnalyticsReportSchedule, error) {
	var item AnalyticsReportSchedule
	var cadence, createdAt, updatedAt, nextRunAt string
	var lastAttemptAt, lastRunAt sql.NullString
	if err := scanner.Scan(
		&item.ID,
		&item.OrganizationSlug,
		&item.SourceType,
		&item.SourceID,
		&item.CreatedByUserID,
		&item.Recipient,
		&cadence,
		&createdAt,
		&updatedAt,
		&lastAttemptAt,
		&lastRunAt,
		&nextRunAt,
		&item.LastSnapshotToken,
		&item.LastError,
	); err != nil {
		return AnalyticsReportSchedule{}, fmt.Errorf("scan analytics report schedule: %w", err)
	}
	item.Cadence = normalizeAnalyticsReportCadence(AnalyticsReportCadence(cadence))
	item.CreatedAt = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)
	item.NextRunAt = parseTime(nextRunAt)
	item.LastAttemptAt = parseOptionalTime(lastAttemptAt)
	item.LastRunAt = parseOptionalTime(lastRunAt)
	return item, nil
}
