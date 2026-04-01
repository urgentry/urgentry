package postgrescontrol

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	sharedstore "urgentry/internal/store"
	"urgentry/pkg/id"
)

const installStateScope = "global"

type LifecycleStore struct {
	db *sql.DB
}

func NewLifecycleStore(db *sql.DB) *LifecycleStore {
	return &LifecycleStore{db: db}
}

func (s *LifecycleStore) GetInstallState(ctx context.Context) (*sharedstore.InstallState, error) {
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
		  WHERE scope = $1`,
		installStateScope,
	)
	var state sharedstore.InstallState
	var bootstrapCompletedAt sql.NullTime
	var maintenanceStartedAt sql.NullTime
	if err := row.Scan(
		&state.InstallID,
		&state.Region,
		&state.Environment,
		&state.Version,
		&state.BootstrapCompleted,
		&bootstrapCompletedAt,
		&state.MaintenanceMode,
		&state.MaintenanceReason,
		&maintenanceStartedAt,
		&state.DateCreated,
		&state.DateUpdated,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("load install state: %w", err)
	}
	if bootstrapCompletedAt.Valid {
		state.BootstrapCompletedAt = bootstrapCompletedAt.Time.UTC()
	}
	if maintenanceStartedAt.Valid {
		state.MaintenanceStartedAt = maintenanceStartedAt.Time.UTC()
	}
	return &state, nil
}

func (s *LifecycleStore) SyncInstallState(ctx context.Context, sync sharedstore.InstallStateSync) (*sharedstore.InstallState, error) {
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
	installID := id.New()
	bootstrapCompleted := false
	var bootstrapCompletedAt *time.Time
	maintenanceMode := false
	maintenanceReason := ""
	var maintenanceStartedAt *time.Time
	if current != nil {
		installID = current.InstallID
		bootstrapCompleted = current.BootstrapCompleted
		if !current.BootstrapCompletedAt.IsZero() {
			value := current.BootstrapCompletedAt.UTC()
			bootstrapCompletedAt = &value
		}
		maintenanceMode = current.MaintenanceMode
		maintenanceReason = current.MaintenanceReason
		if !current.MaintenanceStartedAt.IsZero() {
			value := current.MaintenanceStartedAt.UTC()
			maintenanceStartedAt = &value
		}
	}
	if strings.TrimSpace(installID) == "" {
		installID = id.New()
	}
	if sync.BootstrapCompleted != nil {
		bootstrapCompleted = *sync.BootstrapCompleted
		switch {
		case bootstrapCompleted && bootstrapCompletedAt == nil:
			value := sync.CapturedAt.UTC()
			bootstrapCompletedAt = &value
		case !bootstrapCompleted:
			bootstrapCompletedAt = nil
		}
	}
	createdAt := sync.CapturedAt.UTC()
	if current != nil && !current.DateCreated.IsZero() {
		createdAt = current.DateCreated.UTC()
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO install_state
			(scope, install_id, region, environment, version, bootstrap_completed, bootstrap_completed_at, maintenance_mode, maintenance_reason, maintenance_started_at, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
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
		bootstrapCompleted,
		bootstrapCompletedAt,
		maintenanceMode,
		maintenanceReason,
		maintenanceStartedAt,
		createdAt,
		sync.CapturedAt.UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("sync install state: %w", err)
	}
	return s.GetInstallState(ctx)
}

func (s *LifecycleStore) SetMaintenanceMode(ctx context.Context, enabled bool, reason string, changedAt time.Time) (*sharedstore.InstallState, error) {
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
	sync := sharedstore.InstallStateSync{CapturedAt: changedAt}
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
	var startedAt *time.Time
	if enabled {
		value := changedAt.UTC()
		startedAt = &value
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE install_state
		    SET maintenance_mode = $1,
		        maintenance_reason = $2,
		        maintenance_started_at = $3,
		        updated_at = $4
		  WHERE scope = $5`,
		enabled,
		strings.TrimSpace(reason),
		startedAt,
		changedAt.UTC(),
		installStateScope,
	)
	if err != nil {
		return nil, fmt.Errorf("set maintenance mode: %w", err)
	}
	return s.GetInstallState(ctx)
}
