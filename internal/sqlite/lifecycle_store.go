package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/store"
)

const installStateScope = "global"

type LifecycleStore struct {
	db *sql.DB
}

func NewLifecycleStore(db *sql.DB) *LifecycleStore {
	return &LifecycleStore{db: db}
}

func (s *LifecycleStore) GetInstallState(ctx context.Context) (*store.InstallState, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("lifecycle store is not configured")
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT install_id,
		        region,
		        environment,
		        version,
		        bootstrap_completed,
		        bootstrap_completed_at,
		        maintenance_mode,
		        maintenance_reason,
		        maintenance_started_at,
		        created_at,
		        updated_at
		   FROM install_state
		  WHERE scope = ?`,
		installStateScope,
	)
	var state store.InstallState
	var bootstrapCompleted int
	var maintenanceMode int
	var bootstrapCompletedAt string
	var maintenanceStartedAt string
	var createdAt string
	var updatedAt string
	if err := row.Scan(
		&state.InstallID,
		&state.Region,
		&state.Environment,
		&state.Version,
		&bootstrapCompleted,
		&bootstrapCompletedAt,
		&maintenanceMode,
		&state.MaintenanceReason,
		&maintenanceStartedAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("load install state: %w", err)
	}
	state.BootstrapCompleted = bootstrapCompleted > 0
	state.MaintenanceMode = maintenanceMode > 0
	state.BootstrapCompletedAt = parseTime(bootstrapCompletedAt)
	state.MaintenanceStartedAt = parseTime(maintenanceStartedAt)
	state.DateCreated = parseTime(createdAt)
	state.DateUpdated = parseTime(updatedAt)
	return &state, nil
}

func (s *LifecycleStore) SyncInstallState(ctx context.Context, sync store.InstallStateSync) (*store.InstallState, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("lifecycle store is not configured")
	}
	if sync.CapturedAt.IsZero() {
		sync.CapturedAt = time.Now().UTC()
	}
	current, err := s.GetInstallState(ctx)
	if err != nil {
		return nil, err
	}
	installID := generateID()
	bootstrapCompleted := false
	bootstrapCompletedAt := ""
	maintenanceMode := false
	maintenanceReason := ""
	maintenanceStartedAt := ""
	createdAt := sync.CapturedAt.UTC().Format(time.RFC3339)
	if current != nil {
		installID = current.InstallID
		bootstrapCompleted = current.BootstrapCompleted
		bootstrapCompletedAt = formatLifecycleTime(current.BootstrapCompletedAt)
		maintenanceMode = current.MaintenanceMode
		maintenanceReason = current.MaintenanceReason
		maintenanceStartedAt = formatLifecycleTime(current.MaintenanceStartedAt)
		createdAt = formatLifecycleTime(current.DateCreated)
		if createdAt == "" {
			createdAt = sync.CapturedAt.UTC().Format(time.RFC3339)
		}
	}
	if installID == "" {
		installID = generateID()
	}
	if sync.BootstrapCompleted != nil {
		bootstrapCompleted = *sync.BootstrapCompleted
		switch {
		case bootstrapCompleted && bootstrapCompletedAt == "":
			bootstrapCompletedAt = sync.CapturedAt.UTC().Format(time.RFC3339)
		case !bootstrapCompleted:
			bootstrapCompletedAt = ""
		}
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO install_state
			(scope, install_id, region, environment, version, bootstrap_completed, bootstrap_completed_at, maintenance_mode, maintenance_reason, maintenance_started_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(scope) DO UPDATE SET
			install_id = excluded.install_id,
			region = excluded.region,
			environment = excluded.environment,
			version = excluded.version,
			bootstrap_completed = excluded.bootstrap_completed,
			bootstrap_completed_at = excluded.bootstrap_completed_at,
			maintenance_mode = excluded.maintenance_mode,
			maintenance_reason = excluded.maintenance_reason,
			maintenance_started_at = excluded.maintenance_started_at,
			updated_at = excluded.updated_at`,
		installStateScope,
		installID,
		strings.TrimSpace(sync.Region),
		strings.TrimSpace(sync.Environment),
		strings.TrimSpace(sync.Version),
		lifecycleBoolToInt(bootstrapCompleted),
		bootstrapCompletedAt,
		lifecycleBoolToInt(maintenanceMode),
		maintenanceReason,
		maintenanceStartedAt,
		createdAt,
		sync.CapturedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("sync install state: %w", err)
	}
	return s.GetInstallState(ctx)
}

func (s *LifecycleStore) SetMaintenanceMode(ctx context.Context, enabled bool, reason string, changedAt time.Time) (*store.InstallState, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("lifecycle store is not configured")
	}
	if changedAt.IsZero() {
		changedAt = time.Now().UTC()
	}
	current, err := s.GetInstallState(ctx)
	if err != nil {
		return nil, err
	}
	sync := store.InstallStateSync{CapturedAt: changedAt}
	if current != nil {
		sync.Region = current.Region
		sync.Environment = current.Environment
		sync.Version = current.Version
		sync.BootstrapCompleted = &current.BootstrapCompleted
	} else {
		completed := false
		sync.BootstrapCompleted = &completed
	}
	if _, err := s.SyncInstallState(ctx, sync); err != nil {
		return nil, err
	}
	startedAt := ""
	if enabled {
		startedAt = changedAt.UTC().Format(time.RFC3339)
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE install_state
		    SET maintenance_mode = ?,
		        maintenance_reason = ?,
		        maintenance_started_at = ?,
		        updated_at = ?
		  WHERE scope = ?`,
		lifecycleBoolToInt(enabled),
		strings.TrimSpace(reason),
		startedAt,
		changedAt.UTC().Format(time.RFC3339),
		installStateScope,
	)
	if err != nil {
		return nil, fmt.Errorf("set maintenance mode: %w", err)
	}
	return s.GetInstallState(ctx)
}

func lifecycleBoolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func formatLifecycleTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
