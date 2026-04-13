package selfhostedops

import (
	"context"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/store"
	"github.com/rs/zerolog/log"
)

type MaintenanceStatus struct {
	Install    *store.InstallState `json:"install,omitempty"`
	WritesOpen bool                `json:"writesOpen"`
	DrainState string              `json:"drainState"`
	Steps      []string            `json:"steps"`
}

func LoadMaintenanceStatus(ctx context.Context, controlDSN string) (*MaintenanceStatus, error) {
	lifecycle, _, closeDB, err := openOperatorStores(ctx, controlDSN)
	if err != nil {
		return nil, err
	}
	defer closeDB()

	state, err := lifecycle.GetInstallState(ctx)
	if err != nil {
		return nil, err
	}
	return buildMaintenanceStatus(state), nil
}

func EnterMaintenance(ctx context.Context, controlDSN, reason, actor, source string, changedAt time.Time) (*MaintenanceStatus, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("maintenance reason is required")
	}
	lifecycle, audits, closeDB, err := openOperatorStores(ctx, controlDSN)
	if err != nil {
		return nil, err
	}
	defer closeDB()

	state, err := lifecycle.SetMaintenanceMode(ctx, true, reason, changedAt.UTC())
	if err != nil {
		return nil, err
	}
	if audits != nil {
		recordMaintenanceAudit(ctx, audits, store.OperatorAuditRecord{
			Action:       "maintenance.enabled",
			Status:       "succeeded",
			Source:       strings.TrimSpace(source),
			Actor:        strings.TrimSpace(actor),
			Detail:       reason,
			MetadataJSON: fmt.Sprintf(`{"maintenanceMode":true,"changedAt":"%s"}`, changedAt.UTC().Format(time.RFC3339)),
		})
	}
	return buildMaintenanceStatus(state), nil
}

func LeaveMaintenance(ctx context.Context, controlDSN, actor, source string, changedAt time.Time) (*MaintenanceStatus, error) {
	lifecycle, audits, closeDB, err := openOperatorStores(ctx, controlDSN)
	if err != nil {
		return nil, err
	}
	defer closeDB()

	state, err := lifecycle.SetMaintenanceMode(ctx, false, "", changedAt.UTC())
	if err != nil {
		return nil, err
	}
	if audits != nil {
		recordMaintenanceAudit(ctx, audits, store.OperatorAuditRecord{
			Action:       "maintenance.disabled",
			Status:       "succeeded",
			Source:       strings.TrimSpace(source),
			Actor:        strings.TrimSpace(actor),
			Detail:       "left maintenance mode",
			MetadataJSON: fmt.Sprintf(`{"maintenanceMode":false,"changedAt":"%s"}`, changedAt.UTC().Format(time.RFC3339)),
		})
	}
	return buildMaintenanceStatus(state), nil
}

func buildMaintenanceStatus(state *store.InstallState) *MaintenanceStatus {
	status := &MaintenanceStatus{
		Install:    state,
		WritesOpen: true,
		DrainState: "writes_open",
		Steps: []string{
			"Keep API, ingest, worker, and scheduler roles online while writes remain open.",
			"Before upgrades, restores, or schema changes, enter maintenance mode to freeze new writes.",
			"Wait for in-flight requests and async backlog to drain on /ops/ before stopping roles or mutating schemas.",
			"After the operator action completes, leave maintenance mode and rerun preflight, status, and smoke checks.",
		},
	}
	if state != nil && state.MaintenanceMode {
		status.WritesOpen = false
		status.DrainState = "draining"
		status.Steps = []string{
			"Reads stay online, but new writes and ingest are blocked with 503 maintenance responses.",
			"Watch queue backlog, retention, and backfill state on /ops/ until the install is quiet enough for the planned action.",
			"Once the drain is complete, stop or restart roles as needed for backup, restore, upgrade, or migration work.",
			"Leave maintenance mode after the action completes, then rerun preflight, status, and smoke validation.",
		}
	}
	return status
}

func recordMaintenanceAudit(ctx context.Context, audits store.OperatorAuditStore, record store.OperatorAuditRecord) {
	if audits == nil {
		return
	}
	if err := audits.Record(ctx, record); err != nil {
		log.Warn().
			Err(err).
			Str("action", record.Action).
			Str("source", record.Source).
			Str("actor", record.Actor).
			Msg("failed to record maintenance audit event")
	}
}
