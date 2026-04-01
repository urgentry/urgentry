package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// MonitorSchedule describes when a monitor is expected to run.
type MonitorSchedule struct {
	Type    string `json:"type,omitempty"`
	Value   int    `json:"value,omitempty"`
	Unit    string `json:"unit,omitempty"`
	Crontab string `json:"crontab,omitempty"`
}

// MonitorConfig captures scheduler-relevant monitor settings.
type MonitorConfig struct {
	Schedule      MonitorSchedule `json:"schedule"`
	CheckInMargin int             `json:"checkin_margin,omitempty"`
	MaxRuntime    int             `json:"max_runtime,omitempty"`
	Timezone      string          `json:"timezone,omitempty"`
}

// Monitor is the persisted monitor definition plus latest state.
type Monitor struct {
	ID            string        `json:"id"`
	ProjectID     string        `json:"projectId"`
	Slug          string        `json:"slug"`
	Status        string        `json:"status"`
	Environment   string        `json:"environment,omitempty"`
	Config        MonitorConfig `json:"config"`
	LastCheckInID string        `json:"lastCheckInId,omitempty"`
	LastStatus    string        `json:"lastStatus,omitempty"`
	LastCheckInAt time.Time     `json:"lastCheckInAt,omitempty"`
	NextCheckInAt time.Time     `json:"nextCheckInAt,omitempty"`
	DateCreated   time.Time     `json:"dateCreated"`
	UpdatedAt     time.Time     `json:"dateUpdated"`
}

// MonitorCheckIn is one monitor execution report.
type MonitorCheckIn struct {
	ID           string          `json:"id"`
	MonitorID    string          `json:"monitorId"`
	ProjectID    string          `json:"projectId"`
	CheckInID    string          `json:"checkInId"`
	MonitorSlug  string          `json:"monitorSlug"`
	Status       string          `json:"status"`
	Duration     float64         `json:"duration"`
	Release      string          `json:"release,omitempty"`
	Environment  string          `json:"environment,omitempty"`
	ScheduledFor time.Time       `json:"scheduledFor,omitempty"`
	PayloadJSON  json.RawMessage `json:"payload,omitempty"`
	DateCreated  time.Time       `json:"dateCreated"`
}

// MonitorStore persists monitors and check-ins in SQLite.
type MonitorStore struct {
	db *sql.DB
}

// NewMonitorStore creates a SQLite-backed monitor store.
func NewMonitorStore(db *sql.DB) *MonitorStore {
	return &MonitorStore{db: db}
}

// UpsertMonitor creates or updates a monitor definition.
func (s *MonitorStore) UpsertMonitor(ctx context.Context, monitor *Monitor) (*Monitor, error) {
	if monitor == nil {
		return nil, nil
	}
	if strings.TrimSpace(monitor.ProjectID) == "" {
		return nil, fmt.Errorf("monitor project_id is required")
	}
	if strings.TrimSpace(monitor.Slug) == "" {
		return nil, fmt.Errorf("monitor slug is required")
	}
	monitor.Status = normalizeMonitorStatus(monitor.Status)
	monitor.Environment = normalizeEnvironment(monitor.Environment)
	monitor.Config = normalizeMonitorConfig(monitor.Config)
	if monitor.Config.Timezone == "" {
		monitor.Config.Timezone = "UTC"
	}
	if monitor.ID == "" {
		monitor.ID = generateID()
	}
	now := time.Now().UTC()
	if monitor.DateCreated.IsZero() {
		monitor.DateCreated = now
	}
	monitor.UpdatedAt = now

	row := s.db.QueryRowContext(ctx,
		`INSERT INTO monitors
			(id, project_id, slug, status, environment, schedule_type, schedule_value, schedule_unit, schedule_crontab, checkin_margin, max_runtime, timezone, config_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(project_id, slug) DO UPDATE SET
			status = excluded.status,
			environment = excluded.environment,
			schedule_type = excluded.schedule_type,
			schedule_value = excluded.schedule_value,
			schedule_unit = excluded.schedule_unit,
			schedule_crontab = excluded.schedule_crontab,
			checkin_margin = excluded.checkin_margin,
			max_runtime = excluded.max_runtime,
			timezone = excluded.timezone,
			config_json = excluded.config_json,
			updated_at = excluded.updated_at
		 RETURNING id, project_id, slug, status, COALESCE(environment, ''), COALESCE(config_json, '{}'),
		        COALESCE(last_checkin_id, ''), COALESCE(last_status, ''), COALESCE(last_checkin_at, ''),
		        COALESCE(next_checkin_at, ''), created_at, updated_at`,
		monitor.ID,
		monitor.ProjectID,
		monitor.Slug,
		monitor.Status,
		nullIfEmpty(monitor.Environment),
		nullIfEmpty(monitor.Config.Schedule.Type),
		nullIfZero(monitor.Config.Schedule.Value),
		nullIfEmpty(monitor.Config.Schedule.Unit),
		nullIfEmpty(monitor.Config.Schedule.Crontab),
		monitor.Config.CheckInMargin,
		monitor.Config.MaxRuntime,
		firstNonEmpty(monitor.Config.Timezone, "UTC"),
		encodeMonitorConfig(monitor.Config),
		monitor.DateCreated.UTC().Format(time.RFC3339),
		monitor.UpdatedAt.UTC().Format(time.RFC3339),
	)
	created, err := scanMonitor(row)
	if err != nil {
		return nil, fmt.Errorf("upsert monitor: %w", err)
	}
	return &created, nil
}

// GetMonitor returns a monitor definition by project and slug.
func (s *MonitorStore) GetMonitor(ctx context.Context, projectID, slug string) (*Monitor, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, slug, status, COALESCE(environment, ''), COALESCE(config_json, '{}'),
		        COALESCE(last_checkin_id, ''), COALESCE(last_status, ''), COALESCE(last_checkin_at, ''),
		        COALESCE(next_checkin_at, ''), created_at, updated_at
		 FROM monitors
		 WHERE project_id = ? AND slug = ?`,
		projectID, slug,
	)
	item, err := scanMonitor(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get monitor: %w", err)
	}
	return &item, nil
}

// DeleteMonitor removes a monitor and its check-ins.
func (s *MonitorStore) DeleteMonitor(ctx context.Context, projectID, slug string) error {
	monitor, err := s.GetMonitor(ctx, projectID, slug)
	if err != nil {
		return err
	}
	if monitor == nil {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM monitor_checkins WHERE monitor_id = ?`, monitor.ID); err != nil {
		return fmt.Errorf("delete monitor check-ins: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`DELETE FROM monitors WHERE id = ?`,
		monitor.ID,
	)
	if err != nil {
		return fmt.Errorf("delete monitor: %w", err)
	}
	return nil
}

// SaveCheckIn records a monitor check-in and upserts its monitor definition.
func (s *MonitorStore) SaveCheckIn(ctx context.Context, checkIn *MonitorCheckIn, config *MonitorConfig) (*Monitor, error) {
	if checkIn == nil {
		return nil, nil
	}
	if strings.TrimSpace(checkIn.ProjectID) == "" {
		return nil, fmt.Errorf("check-in project_id is required")
	}
	if strings.TrimSpace(checkIn.MonitorSlug) == "" {
		return nil, fmt.Errorf("check-in monitor_slug is required")
	}
	checkIn.Status = normalizeCheckInStatus(checkIn.Status)
	if checkIn.CheckInID == "" {
		checkIn.CheckInID = generateID()
	}
	if checkIn.ID == "" {
		checkIn.ID = generateID()
	}
	if checkIn.DateCreated.IsZero() {
		checkIn.DateCreated = time.Now().UTC()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	monitor, err := s.ensureMonitor(ctx, tx, checkIn.ProjectID, checkIn.MonitorSlug)
	if err != nil {
		return nil, err
	}
	monitor.Environment = normalizeEnvironment(firstNonEmpty(checkIn.Environment, monitor.Environment))

	if config != nil {
		monitor.Config = normalizeMonitorConfig(*config)
	}
	if monitor.Config.Timezone == "" {
		monitor.Config.Timezone = "UTC"
	}

	nextCheckIn := computeNextCheckIn(checkIn.DateCreated, monitor.Config)
	payloadJSON := "{}"
	if len(checkIn.PayloadJSON) > 0 {
		payloadJSON = string(checkIn.PayloadJSON)
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO monitor_checkins
			(id, monitor_id, project_id, check_in_id, monitor_slug, status, duration, release, environment, scheduled_for, payload_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		checkIn.ID,
		monitor.ID,
		checkIn.ProjectID,
		checkIn.CheckInID,
		checkIn.MonitorSlug,
		checkIn.Status,
		checkIn.Duration,
		nullIfEmpty(checkIn.Release),
		nullIfEmpty(monitor.Environment),
		nullIfTime(checkIn.ScheduledFor),
		payloadJSON,
		checkIn.DateCreated.UTC().Format(time.RFC3339),
	); err != nil {
		return nil, fmt.Errorf("insert check-in: %w", err)
	}

	configJSON := encodeMonitorConfig(monitor.Config)
	monitor.LastCheckInID = checkIn.CheckInID
	monitor.LastStatus = checkIn.Status
	monitor.LastCheckInAt = checkIn.DateCreated
	monitor.NextCheckInAt = nextCheckIn
	monitor.UpdatedAt = time.Now().UTC()

	if _, err = tx.ExecContext(ctx,
		`UPDATE monitors
		 SET environment = ?,
		     schedule_type = ?,
		     schedule_value = ?,
		     schedule_unit = ?,
		     schedule_crontab = ?,
		     checkin_margin = ?,
		     max_runtime = ?,
		     timezone = ?,
		     config_json = ?,
		     last_checkin_id = ?,
		     last_status = ?,
		     last_checkin_at = ?,
		     next_checkin_at = ?,
		     updated_at = ?
		 WHERE id = ?`,
		nullIfEmpty(monitor.Environment),
		nullIfEmpty(monitor.Config.Schedule.Type),
		nullIfZero(monitor.Config.Schedule.Value),
		nullIfEmpty(monitor.Config.Schedule.Unit),
		nullIfEmpty(monitor.Config.Schedule.Crontab),
		monitor.Config.CheckInMargin,
		monitor.Config.MaxRuntime,
		firstNonEmpty(monitor.Config.Timezone, "UTC"),
		configJSON,
		nullIfEmpty(monitor.LastCheckInID),
		nullIfEmpty(monitor.LastStatus),
		nullIfTime(monitor.LastCheckInAt),
		nullIfTime(monitor.NextCheckInAt),
		monitor.UpdatedAt.UTC().Format(time.RFC3339),
		monitor.ID,
	); err != nil {
		return nil, fmt.Errorf("update monitor: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return monitor, nil
}

// ListMonitors returns project monitors ordered by recent updates.
func (s *MonitorStore) ListMonitors(ctx context.Context, projectID string, limit int) ([]Monitor, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, slug, status, COALESCE(environment, ''), COALESCE(config_json, '{}'),
		        COALESCE(last_checkin_id, ''), COALESCE(last_status, ''), COALESCE(last_checkin_at, ''),
		        COALESCE(next_checkin_at, ''), created_at, updated_at
		 FROM monitors
		 WHERE project_id = ?
		 ORDER BY updated_at DESC, slug ASC
		 LIMIT ?`,
		projectID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list monitors: %w", err)
	}
	defer rows.Close()

	var monitors []Monitor
	for rows.Next() {
		item, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		monitors = append(monitors, item)
	}
	return monitors, rows.Err()
}

// ListAllMonitors returns monitors across all projects.
func (s *MonitorStore) ListAllMonitors(ctx context.Context, limit int) ([]Monitor, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, slug, status, COALESCE(environment, ''), COALESCE(config_json, '{}'),
		        COALESCE(last_checkin_id, ''), COALESCE(last_status, ''), COALESCE(last_checkin_at, ''),
		        COALESCE(next_checkin_at, ''), created_at, updated_at
		 FROM monitors
		 ORDER BY updated_at DESC, slug ASC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list all monitors: %w", err)
	}
	defer rows.Close()

	var monitors []Monitor
	for rows.Next() {
		item, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		monitors = append(monitors, item)
	}
	return monitors, rows.Err()
}

// ListCheckIns returns recent check-ins for one project monitor slug.
func (s *MonitorStore) ListCheckIns(ctx context.Context, projectID, slug string, limit int) ([]MonitorCheckIn, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, monitor_id, project_id, check_in_id, monitor_slug, status, duration,
		        COALESCE(release, ''), COALESCE(environment, ''), COALESCE(scheduled_for, ''),
		        COALESCE(payload_json, '{}'), created_at
		 FROM monitor_checkins
		 WHERE project_id = ? AND monitor_slug = ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		projectID, slug, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list check-ins: %w", err)
	}
	defer rows.Close()

	var items []MonitorCheckIn
	for rows.Next() {
		var item MonitorCheckIn
		var payloadJSON, scheduledFor, createdAt string
		if err := rows.Scan(
			&item.ID,
			&item.MonitorID,
			&item.ProjectID,
			&item.CheckInID,
			&item.MonitorSlug,
			&item.Status,
			&item.Duration,
			&item.Release,
			&item.Environment,
			&scheduledFor,
			&payloadJSON,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan check-in: %w", err)
		}
		item.ScheduledFor = parseTime(scheduledFor)
		item.DateCreated = parseTime(createdAt)
		if strings.TrimSpace(payloadJSON) != "" && payloadJSON != "{}" {
			item.PayloadJSON = json.RawMessage(payloadJSON)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// MarkMissed inserts missed check-ins for monitors that are overdue.
func (s *MonitorStore) MarkMissed(ctx context.Context, now time.Time) ([]MonitorCheckIn, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, slug, status, COALESCE(environment, ''), COALESCE(config_json, '{}'),
		        COALESCE(last_checkin_id, ''), COALESCE(last_status, ''), COALESCE(last_checkin_at, ''),
		        COALESCE(next_checkin_at, ''), created_at, updated_at
		 FROM monitors
		 WHERE status = 'active' AND next_checkin_at IS NOT NULL AND next_checkin_at != '' AND next_checkin_at <= ?
		 ORDER BY next_checkin_at ASC`,
		now.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("query due monitors: %w", err)
	}
	defer rows.Close()

	var due []Monitor
	for rows.Next() {
		item, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		if item.NextCheckInAt.IsZero() {
			continue
		}
		due = append(due, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	missed := make([]MonitorCheckIn, 0, len(due))
	for _, monitor := range due {
		next := advancePast(monitor.NextCheckInAt, monitor.Config, now)
		checkInID := generateID()
		item := MonitorCheckIn{
			ID:           generateID(),
			MonitorID:    monitor.ID,
			ProjectID:    monitor.ProjectID,
			CheckInID:    checkInID,
			MonitorSlug:  monitor.Slug,
			Status:       "missed",
			Environment:  monitor.Environment,
			ScheduledFor: monitor.NextCheckInAt,
			DateCreated:  now.UTC(),
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO monitor_checkins
				(id, monitor_id, project_id, check_in_id, monitor_slug, status, duration, release, environment, scheduled_for, payload_json, created_at)
			 VALUES (?, ?, ?, ?, ?, 'missed', 0, NULL, ?, ?, '{}', ?)`,
			item.ID,
			monitor.ID,
			monitor.ProjectID,
			checkInID,
			monitor.Slug,
			nullIfEmpty(monitor.Environment),
			monitor.NextCheckInAt.UTC().Format(time.RFC3339),
			now.UTC().Format(time.RFC3339),
		); err != nil {
			return missed, fmt.Errorf("insert missed check-in: %w", err)
		}
		if _, err := s.db.ExecContext(ctx,
			`UPDATE monitors
			 SET last_checkin_id = ?,
			     last_status = 'missed',
			     last_checkin_at = ?,
			     next_checkin_at = ?,
			     updated_at = ?
			 WHERE id = ?`,
			checkInID,
			now.UTC().Format(time.RFC3339),
			nullIfTime(next),
			now.UTC().Format(time.RFC3339),
			monitor.ID,
		); err != nil {
			return missed, fmt.Errorf("update missed monitor: %w", err)
		}
		missed = append(missed, item)
	}
	return missed, nil
}

func (s *MonitorStore) ensureMonitor(ctx context.Context, tx *sql.Tx, projectID, slug string) (*Monitor, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT id, project_id, slug, status, COALESCE(environment, ''), COALESCE(config_json, '{}'),
		        COALESCE(last_checkin_id, ''), COALESCE(last_status, ''), COALESCE(last_checkin_at, ''),
		        COALESCE(next_checkin_at, ''), created_at, updated_at
		 FROM monitors
		 WHERE project_id = ? AND slug = ?`,
		projectID, slug,
	)

	item, err := scanMonitor(row)
	if err == nil {
		return &item, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("lookup monitor: %w", err)
	}

	now := time.Now().UTC()
	item = Monitor{
		ID:          generateID(),
		ProjectID:   projectID,
		Slug:        slug,
		Status:      "active",
		Config:      MonitorConfig{Timezone: "UTC"},
		DateCreated: now,
		UpdatedAt:   now,
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO monitors
			(id, project_id, slug, status, timezone, config_json, created_at, updated_at)
		 VALUES (?, ?, ?, 'active', 'UTC', '{}', ?, ?)`,
		item.ID,
		item.ProjectID,
		item.Slug,
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
	); err != nil {
		return nil, fmt.Errorf("insert monitor: %w", err)
	}
	return &item, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanMonitor(s scanner) (Monitor, error) {
	var (
		item       Monitor
		configJSON string
		lastAt     string
		nextAt     string
		createdAt  string
		updatedAt  string
	)
	if err := s.Scan(
		&item.ID,
		&item.ProjectID,
		&item.Slug,
		&item.Status,
		&item.Environment,
		&configJSON,
		&item.LastCheckInID,
		&item.LastStatus,
		&lastAt,
		&nextAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Monitor{}, err
	}
	item.LastCheckInAt = parseTime(lastAt)
	item.NextCheckInAt = parseTime(nextAt)
	item.DateCreated = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)
	item.Config = decodeMonitorConfig(configJSON)
	return item, nil
}

func encodeMonitorConfig(cfg MonitorConfig) string {
	cfg = normalizeMonitorConfig(cfg)
	data, err := json.Marshal(cfg)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func decodeMonitorConfig(raw string) MonitorConfig {
	cfg := MonitorConfig{Timezone: "UTC"}
	if strings.TrimSpace(raw) != "" && raw != "{}" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	return normalizeMonitorConfig(cfg)
}

func normalizeMonitorConfig(cfg MonitorConfig) MonitorConfig {
	cfg.Timezone = firstNonEmpty(strings.TrimSpace(cfg.Timezone), "UTC")
	cfg.Schedule.Type = strings.ToLower(strings.TrimSpace(cfg.Schedule.Type))
	cfg.Schedule.Unit = strings.ToLower(strings.TrimSpace(cfg.Schedule.Unit))
	cfg.Schedule.Crontab = strings.TrimSpace(cfg.Schedule.Crontab)
	if cfg.Schedule.Type == "" {
		switch {
		case cfg.Schedule.Crontab != "":
			cfg.Schedule.Type = "crontab"
		case cfg.Schedule.Value > 0:
			cfg.Schedule.Type = "interval"
		}
	}
	return cfg
}

func normalizeCheckInStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "ok":
		return "ok"
	case "in_progress":
		return "in_progress"
	case "error":
		return "error"
	case "missed":
		return "missed"
	default:
		return "error"
	}
}

func normalizeMonitorStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "active":
		return "active"
	case "disabled", "paused":
		return "disabled"
	default:
		return "active"
	}
}

func normalizeEnvironment(env string) string {
	if strings.TrimSpace(env) == "" {
		return "production"
	}
	return strings.TrimSpace(env)
}

func nullIfTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339)
}

func nullIfZero(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

// firstNonEmpty delegates to firstNonEmptyText (defined in sqlite_helpers.go).
func firstNonEmpty(values ...string) string {
	return firstNonEmptyText(values...)
}
