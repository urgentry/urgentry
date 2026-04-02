package postgrescontrol

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/sqlite"
)

type MonitorSchedule = sqlite.MonitorSchedule
type MonitorConfig = sqlite.MonitorConfig
type Monitor = sqlite.Monitor
type MonitorCheckIn = sqlite.MonitorCheckIn

type monitorScanner interface {
	Scan(dest ...any) error
}

// MonitorStore persists monitors and check-ins in Postgres.
type MonitorStore struct {
	db *sql.DB
}

// NewMonitorStore creates a Postgres-backed monitor store.
func NewMonitorStore(db *sql.DB) *MonitorStore {
	return &MonitorStore{db: db}
}

// UpsertMonitor creates or updates a monitor definition.
func (s *MonitorStore) UpsertMonitor(ctx context.Context, monitor *Monitor) (*Monitor, error) {
	if monitor == nil {
		return nil, nil
	}
	monitor = canonicalMonitor(monitor)
	if monitor.ProjectID == "" {
		return nil, fmt.Errorf("monitor project_id is required")
	}
	if monitor.Slug == "" {
		return nil, fmt.Errorf("monitor slug is required")
	}

	if err := upsertMonitorRow(ctx, s.db, monitor); err != nil {
		return nil, err
	}
	return s.GetMonitor(ctx, monitor.ProjectID, monitor.Slug)
}

// GetMonitor returns a monitor definition by project and slug.
func (s *MonitorStore) GetMonitor(ctx context.Context, projectID, slug string) (*Monitor, error) {
	row := s.db.QueryRowContext(ctx, selectMonitorSQL+` WHERE m.project_id = $1 AND m.slug = $2`, strings.TrimSpace(projectID), strings.TrimSpace(slug))
	item, err := scanMonitor(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get monitor: %w", err)
	}
	return item, nil
}

// DeleteMonitor removes a monitor and its check-ins.
func (s *MonitorStore) DeleteMonitor(ctx context.Context, projectID, slug string) error {
	monitor, err := s.GetMonitor(ctx, projectID, slug)
	if err != nil || monitor == nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM monitor_checkins WHERE monitor_id = $1`, monitor.ID); err != nil {
		return fmt.Errorf("delete monitor check-ins: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM monitors WHERE id = $1`, monitor.ID); err != nil {
		return fmt.Errorf("delete monitor: %w", err)
	}
	return nil
}

// SaveCheckIn records a monitor check-in and ensures a backing monitor exists.
func (s *MonitorStore) SaveCheckIn(ctx context.Context, checkIn *MonitorCheckIn, config *sqlite.MonitorConfig) (*Monitor, error) {
	if checkIn == nil {
		return nil, nil
	}
	checkIn = canonicalCheckIn(checkIn)
	if checkIn.ProjectID == "" {
		return nil, fmt.Errorf("check-in project_id is required")
	}
	if checkIn.MonitorSlug == "" {
		return nil, fmt.Errorf("check-in monitor_slug is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	monitor, err := getMonitorTx(ctx, tx, checkIn.ProjectID, checkIn.MonitorSlug)
	if err != nil {
		return nil, err
	}
	if monitor == nil {
		monitor = canonicalMonitor(&Monitor{
			ProjectID:   checkIn.ProjectID,
			Slug:        checkIn.MonitorSlug,
			Status:      "active",
			Environment: checkIn.Environment,
		})
	}
	if config != nil {
		monitor.Config = canonicalMonitorConfig(*config)
	}
	if monitor.Environment == "" {
		monitor.Environment = canonicalEnvironment(checkIn.Environment)
	}
	if err := upsertMonitorRow(ctx, tx, monitor); err != nil {
		return nil, err
	}
	monitor, err = getMonitorTx(ctx, tx, checkIn.ProjectID, checkIn.MonitorSlug)
	if err != nil {
		return nil, err
	}
	if monitor == nil {
		return nil, fmt.Errorf("monitor disappeared during check-in upsert")
	}

	checkIn.MonitorID = monitor.ID
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO monitor_checkins
			(id, monitor_id, project_id, checkin_id, status, duration_ms, environment, occurred_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
		 ON CONFLICT (monitor_id, checkin_id) DO NOTHING`,
		checkIn.ID,
		monitor.ID,
		checkIn.ProjectID,
		checkIn.CheckInID,
		checkIn.Status,
		int64(checkIn.Duration),
		canonicalEnvironment(firstNonEmpty(checkIn.Environment, monitor.Environment)),
		checkIn.DateCreated.UTC(),
	); err != nil {
		return nil, fmt.Errorf("insert monitor check-in: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetMonitor(ctx, checkIn.ProjectID, checkIn.MonitorSlug)
}

// ListAllMonitors returns monitors across all projects ordered by recent activity.
func (s *MonitorStore) ListAllMonitors(ctx context.Context, limit int) ([]Monitor, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		selectMonitorSQL+` ORDER BY COALESCE(latest.occurred_at, m.updated_at) DESC, m.slug ASC LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list all monitors: %w", err)
	}
	defer rows.Close()

	items := make([]Monitor, 0, limit)
	for rows.Next() {
		item, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

// ListOrgMonitors returns monitors across one organization ordered by recent activity.
func (s *MonitorStore) ListOrgMonitors(ctx context.Context, orgID string, limit int) ([]Monitor, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		selectMonitorSQL+` JOIN projects p ON p.id = m.project_id WHERE p.organization_id = $1 ORDER BY COALESCE(latest.occurred_at, m.updated_at) DESC, m.slug ASC LIMIT $2`,
		strings.TrimSpace(orgID), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list organization monitors: %w", err)
	}
	defer rows.Close()

	items := make([]Monitor, 0, limit)
	for rows.Next() {
		item, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

// ListMonitors returns project monitors ordered by recent activity.
func (s *MonitorStore) ListMonitors(ctx context.Context, projectID string, limit int) ([]Monitor, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		selectMonitorSQL+` WHERE m.project_id = $1 ORDER BY COALESCE(latest.occurred_at, m.updated_at) DESC, m.slug ASC LIMIT $2`,
		strings.TrimSpace(projectID), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list monitors: %w", err)
	}
	defer rows.Close()

	items := make([]Monitor, 0, limit)
	for rows.Next() {
		item, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

// ListCheckIns returns recent check-ins for one monitor slug.
func (s *MonitorStore) ListCheckIns(ctx context.Context, projectID, slug string, limit int) ([]MonitorCheckIn, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT c.id, c.monitor_id, c.project_id, c.checkin_id, m.slug, c.status, c.duration_ms, c.environment, c.occurred_at, c.created_at
		   FROM monitor_checkins c
		   JOIN monitors m ON m.id = c.monitor_id
		  WHERE c.project_id = $1 AND m.slug = $2
		  ORDER BY c.occurred_at DESC, c.created_at DESC
		  LIMIT $3`,
		strings.TrimSpace(projectID), strings.TrimSpace(slug), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list monitor check-ins: %w", err)
	}
	defer rows.Close()

	items := make([]MonitorCheckIn, 0, limit)
	for rows.Next() {
		item := MonitorCheckIn{}
		var durationMS int64
		var occurredAt time.Time
		var createdAt time.Time
		if err := rows.Scan(
			&item.ID,
			&item.MonitorID,
			&item.ProjectID,
			&item.CheckInID,
			&item.MonitorSlug,
			&item.Status,
			&durationMS,
			&item.Environment,
			&occurredAt,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan monitor check-in: %w", err)
		}
		item.Duration = float64(durationMS)
		item.DateCreated = createdAt.UTC()
		items = append(items, item)
	}
	return items, rows.Err()
}

// MarkMissed inserts missed check-ins for active overdue monitors.
func (s *MonitorStore) MarkMissed(ctx context.Context, now time.Time) ([]MonitorCheckIn, error) {
	rows, err := s.db.QueryContext(ctx,
		selectMonitorSQL+` WHERE m.status = 'active' ORDER BY m.slug ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list due monitors: %w", err)
	}
	defer rows.Close()

	due := []*Monitor{}
	for rows.Next() {
		item, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		if item.NextCheckInAt.IsZero() || item.NextCheckInAt.After(now) {
			continue
		}
		due = append(due, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	missed := make([]MonitorCheckIn, 0, len(due))
	for _, monitor := range due {
		item := &MonitorCheckIn{
			ID:           generateID(),
			MonitorID:    monitor.ID,
			ProjectID:    monitor.ProjectID,
			CheckInID:    generateID(),
			MonitorSlug:  monitor.Slug,
			Status:       "missed",
			Duration:     0,
			Environment:  canonicalEnvironment(monitor.Environment),
			ScheduledFor: monitor.NextCheckInAt,
			DateCreated:  now.UTC(),
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO monitor_checkins
				(id, monitor_id, project_id, checkin_id, status, duration_ms, environment, occurred_at, created_at)
			 VALUES ($1, $2, $3, $4, 'missed', 0, $5, $6, $7)
			 ON CONFLICT (monitor_id, checkin_id) DO NOTHING`,
			item.ID, item.MonitorID, item.ProjectID, item.CheckInID, item.Environment, now.UTC(), now.UTC(),
		); err != nil {
			return missed, fmt.Errorf("insert missed check-in: %w", err)
		}
		missed = append(missed, *item)
	}
	return missed, nil
}

func getMonitorTx(ctx context.Context, tx *sql.Tx, projectID, slug string) (*Monitor, error) {
	row := tx.QueryRowContext(ctx, selectMonitorSQL+` WHERE m.project_id = $1 AND m.slug = $2`, strings.TrimSpace(projectID), strings.TrimSpace(slug))
	item, err := scanMonitor(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return item, nil
}

func upsertMonitorRow(ctx context.Context, exec monitorExec, monitor *Monitor) error {
	if monitor.ID == "" {
		monitor.ID = generateID()
	}
	now := time.Now().UTC()
	if monitor.DateCreated.IsZero() {
		monitor.DateCreated = now
	}
	monitor.UpdatedAt = now
	kind, value := encodeSchedule(monitor.Config)
	_, err := exec.ExecContext(ctx,
		`INSERT INTO monitors
			(id, project_id, slug, name, status, schedule_type, schedule_value, checkin_margin_seconds, max_runtime_seconds, timezone, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 ON CONFLICT (project_id, slug) DO UPDATE SET
		 	name = EXCLUDED.name,
		 	status = EXCLUDED.status,
		 	schedule_type = EXCLUDED.schedule_type,
		 	schedule_value = EXCLUDED.schedule_value,
		 	checkin_margin_seconds = EXCLUDED.checkin_margin_seconds,
		 	max_runtime_seconds = EXCLUDED.max_runtime_seconds,
		 	timezone = EXCLUDED.timezone,
		 	updated_at = EXCLUDED.updated_at`,
		monitor.ID,
		monitor.ProjectID,
		monitor.Slug,
		monitor.Slug,
		monitor.Status,
		kind,
		value,
		monitor.Config.CheckInMargin,
		monitor.Config.MaxRuntime,
		firstNonEmpty(monitor.Config.Timezone, "UTC"),
		monitor.DateCreated.UTC(),
		monitor.UpdatedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("upsert monitor: %w", err)
	}
	return nil
}

type monitorExec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

const selectMonitorSQL = `
SELECT m.id,
       m.project_id,
       m.slug,
       m.status,
       m.schedule_type,
       m.schedule_value,
       m.checkin_margin_seconds,
       m.max_runtime_seconds,
       m.timezone,
       m.created_at,
       m.updated_at,
       COALESCE(latest.checkin_id, ''),
       COALESCE(latest.status, ''),
       COALESCE(latest.environment, ''),
       latest.occurred_at
  FROM monitors m
  LEFT JOIN LATERAL (
    SELECT c.checkin_id, c.status, c.environment, c.occurred_at
      FROM monitor_checkins c
     WHERE c.monitor_id = m.id
     ORDER BY c.occurred_at DESC, c.created_at DESC
     LIMIT 1
  ) latest ON TRUE`

func scanMonitor(s monitorScanner) (*Monitor, error) {
	item := &Monitor{}
	var scheduleType, scheduleValue, timezone, lastCheckInID, lastStatus, environment string
	var occurredAt sql.NullTime
	if err := s.Scan(
		&item.ID,
		&item.ProjectID,
		&item.Slug,
		&item.Status,
		&scheduleType,
		&scheduleValue,
		&item.Config.CheckInMargin,
		&item.Config.MaxRuntime,
		&timezone,
		&item.DateCreated,
		&item.UpdatedAt,
		&lastCheckInID,
		&lastStatus,
		&environment,
		&occurredAt,
	); err != nil {
		return nil, err
	}
	item.DateCreated = item.DateCreated.UTC()
	item.UpdatedAt = item.UpdatedAt.UTC()
	item.Config = decodeMonitorConfig(scheduleType, scheduleValue, item.Config.CheckInMargin, item.Config.MaxRuntime, timezone)
	item.Environment = canonicalEnvironment(environment)
	item.LastCheckInID = strings.TrimSpace(lastCheckInID)
	item.LastStatus = strings.TrimSpace(lastStatus)
	if occurredAt.Valid {
		item.LastCheckInAt = occurredAt.Time.UTC()
		item.NextCheckInAt = computeNextCheckIn(item.LastCheckInAt, item.Config)
	}
	return item, nil
}

func canonicalMonitor(monitor *Monitor) *Monitor {
	if monitor == nil {
		return nil
	}
	copy := *monitor
	copy.ProjectID = strings.TrimSpace(copy.ProjectID)
	copy.Slug = strings.TrimSpace(copy.Slug)
	copy.Status = canonicalMonitorStatus(copy.Status)
	copy.Environment = canonicalEnvironment(copy.Environment)
	copy.Config = canonicalMonitorConfig(copy.Config)
	return &copy
}

func canonicalCheckIn(checkIn *MonitorCheckIn) *MonitorCheckIn {
	if checkIn == nil {
		return nil
	}
	copy := *checkIn
	copy.ProjectID = strings.TrimSpace(copy.ProjectID)
	copy.MonitorSlug = strings.TrimSpace(copy.MonitorSlug)
	copy.Status = canonicalCheckInStatus(copy.Status)
	copy.Environment = canonicalEnvironment(copy.Environment)
	if copy.CheckInID == "" {
		copy.CheckInID = generateID()
	}
	if copy.ID == "" {
		copy.ID = generateID()
	}
	if copy.DateCreated.IsZero() {
		copy.DateCreated = time.Now().UTC()
	}
	return &copy
}

func canonicalMonitorConfig(cfg MonitorConfig) MonitorConfig {
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

func canonicalMonitorStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "active":
		return "active"
	case "disabled", "paused":
		return "disabled"
	default:
		return "active"
	}
}

func canonicalCheckInStatus(status string) string {
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

func canonicalEnvironment(value string) string {
	if strings.TrimSpace(value) == "" {
		return "production"
	}
	return strings.TrimSpace(value)
}

func encodeSchedule(cfg MonitorConfig) (string, string) {
	cfg = canonicalMonitorConfig(cfg)
	switch cfg.Schedule.Type {
	case "interval":
		if cfg.Schedule.Value <= 0 {
			return "interval", ""
		}
		return "interval", fmt.Sprintf("%d %s", cfg.Schedule.Value, cfg.Schedule.Unit)
	case "crontab":
		return "crontab", cfg.Schedule.Crontab
	default:
		return "", ""
	}
}

func decodeMonitorConfig(kind, value string, checkInMargin, maxRuntime int, timezone string) MonitorConfig {
	cfg := MonitorConfig{
		CheckInMargin: checkInMargin,
		MaxRuntime:    maxRuntime,
		Timezone:      firstNonEmpty(strings.TrimSpace(timezone), "UTC"),
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	case "interval":
		cfg.Schedule.Type = "interval"
		parts := strings.Fields(strings.TrimSpace(value))
		if len(parts) >= 1 {
			cfg.Schedule.Value, _ = strconv.Atoi(parts[0])
		}
		if len(parts) >= 2 {
			cfg.Schedule.Unit = strings.ToLower(parts[1])
		}
	case "crontab":
		cfg.Schedule.Type = "crontab"
		cfg.Schedule.Crontab = strings.TrimSpace(value)
	}
	return canonicalMonitorConfig(cfg)
}

func computeNextCheckIn(base time.Time, cfg MonitorConfig) time.Time {
	cfg = canonicalMonitorConfig(cfg)
	if base.IsZero() {
		return time.Time{}
	}
	switch cfg.Schedule.Type {
	case "interval":
		return addInterval(base, cfg.Schedule.Value, cfg.Schedule.Unit)
	case "crontab":
		next, _ := nextCronOccurrence(base, cfg.Schedule.Crontab, cfg.Timezone)
		return next
	default:
		return time.Time{}
	}
}

func addInterval(base time.Time, value int, unit string) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "minute", "minutes":
		return base.Add(time.Duration(value) * time.Minute)
	case "hour", "hours":
		return base.Add(time.Duration(value) * time.Hour)
	case "day", "days":
		return base.AddDate(0, 0, value)
	case "week", "weeks":
		return base.AddDate(0, 0, value*7)
	case "month", "months":
		return base.AddDate(0, value, 0)
	default:
		return time.Time{}
	}
}

type cronField struct {
	any    bool
	values map[int]bool
}

func nextCronOccurrence(after time.Time, expr, timezone string) (time.Time, bool) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return time.Time{}, false
	}
	loc, err := time.LoadLocation(firstNonEmpty(strings.TrimSpace(timezone), "UTC"))
	if err != nil {
		loc = time.UTC
	}
	minute, ok := parseCronField(fields[0], 0, 59)
	if !ok {
		return time.Time{}, false
	}
	hour, ok := parseCronField(fields[1], 0, 23)
	if !ok {
		return time.Time{}, false
	}
	day, ok := parseCronField(fields[2], 1, 31)
	if !ok {
		return time.Time{}, false
	}
	month, ok := parseCronField(fields[3], 1, 12)
	if !ok {
		return time.Time{}, false
	}
	weekday, ok := parseCronField(fields[4], 0, 7)
	if !ok {
		return time.Time{}, false
	}

	current := after.In(loc).Truncate(time.Minute).Add(time.Minute)
	limit := current.AddDate(1, 0, 0)
	for !current.After(limit) {
		if !matchesCronField(month, int(current.Month())) {
			current = current.Add(time.Minute)
			continue
		}
		if !matchesCronField(hour, current.Hour()) || !matchesCronField(minute, current.Minute()) {
			current = current.Add(time.Minute)
			continue
		}
		dayMatch := matchesCronField(day, current.Day())
		weekdayValue := int(current.Weekday())
		weekdayMatch := matchesCronField(weekday, weekdayValue) || (weekdayValue == 0 && matchesCronField(weekday, 7))
		if !cronDayMatches(day, weekday, dayMatch, weekdayMatch) {
			current = current.Add(time.Minute)
			continue
		}
		return current.UTC(), true
	}
	return time.Time{}, false
}

func parseCronField(raw string, min, max int) (cronField, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "*" {
		return cronField{any: true}, true
	}
	field := cronField{values: map[int]bool{}}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, "*/"):
			step, err := strconv.Atoi(strings.TrimPrefix(part, "*/"))
			if err != nil || step <= 0 {
				return cronField{}, false
			}
			for value := min; value <= max; value += step {
				field.values[value] = true
			}
		default:
			value, err := strconv.Atoi(part)
			if err != nil || value < min || value > max {
				return cronField{}, false
			}
			field.values[value] = true
		}
	}
	return field, len(field.values) > 0
}

func matchesCronField(field cronField, value int) bool {
	if field.any {
		return true
	}
	return field.values[value]
}

func cronDayMatches(day, weekday cronField, dayMatch, weekdayMatch bool) bool {
	if day.any && weekday.any {
		return true
	}
	if day.any {
		return weekdayMatch
	}
	if weekday.any {
		return dayMatch
	}
	return dayMatch || weekdayMatch
}
