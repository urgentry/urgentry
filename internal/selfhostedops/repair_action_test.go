package selfhostedops

import (
	"strings"
	"testing"

	"urgentry/internal/postgrescontrol"
)

func TestExecuteRepairActionRecordsAudit(t *testing.T) {
	db, dsn := openMigratedMaintenanceDatabase(t, "repair_action")
	receipt, err := ExecuteRepairAction(t.Context(), dsn, RepairActionRequest{
		Surface: RepairSurfaceBackfills,
		Action:  RepairActionRestartBackfill,
		Target:  "run-1",
		Reason:  "stalled backfill after upgrade",
		Actor:   "ops-user",
		Source:  "cli",
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("ExecuteRepairAction() error = %v", err)
	}
	if receipt.Audit == nil || receipt.Audit.Action != "repair.backfills.restart_backfill" || receipt.Audit.Status != "requested" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if len(receipt.Safeguards) == 0 || len(receipt.Signals) == 0 {
		t.Fatalf("missing safeguards or signals: %+v", receipt)
	}
	items, err := postgrescontrol.NewOperatorAuditStore(db).List(t.Context(), "", 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0].Action != "repair.backfills.restart_backfill" || !strings.Contains(items[0].MetadataJSON, "stalled") {
		t.Fatalf("unexpected audit entries: %+v", items)
	}
}

func TestExecuteRepairActionRequiresConfirmAndValidSurfaceAction(t *testing.T) {
	_, dsn := openMigratedMaintenanceDatabase(t, "repair_action_invalid")
	_, err := ExecuteRepairAction(t.Context(), dsn, RepairActionRequest{
		Surface: RepairSurfaceBackfills,
		Action:  RepairActionRestartBackfill,
		Target:  "run-1",
		Reason:  "test",
	})
	if err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("confirm error = %v, want confirmation failure", err)
	}
	_, err = ExecuteRepairAction(t.Context(), dsn, RepairActionRequest{
		Surface: RepairSurfaceQuota,
		Action:  RepairActionRebuildReplay,
		Target:  "quota:org-1:query",
		Reason:  "test",
		Confirm: true,
	})
	if err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Fatalf("invalid action error = %v, want not valid failure", err)
	}
}

func TestExecutePITRActionRecordsAudit(t *testing.T) {
	db, dsn := openMigratedMaintenanceDatabase(t, "pitr_action")
	receipt, err := ExecutePITRAction(t.Context(), dsn, PITRActionRequest{
		Surface:    PostgresRecoverySurfaceControl,
		TargetType: "timestamp",
		Target:     "2026-04-10T14:30:00Z",
		Reason:     "operator-requested recovery drill",
		Actor:      "ops-user",
		Source:     "cli",
		Confirm:    true,
	})
	if err != nil {
		t.Fatalf("ExecutePITRAction() error = %v", err)
	}
	if receipt.Audit == nil || receipt.Audit.Action != "pitr.control.recovery_requested" || receipt.Audit.Status != "requested" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if len(receipt.Workflow) == 0 || len(receipt.Boundaries) == 0 {
		t.Fatalf("missing workflow or boundaries: %+v", receipt)
	}
	items, err := postgrescontrol.NewOperatorAuditStore(db).List(t.Context(), "", 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0].Action != "pitr.control.recovery_requested" || !strings.Contains(items[0].MetadataJSON, "timestamp") {
		t.Fatalf("unexpected audit entries: %+v", items)
	}
}

func TestExecutePITRActionRequiresValidTargetType(t *testing.T) {
	_, dsn := openMigratedMaintenanceDatabase(t, "pitr_action_invalid")
	_, err := ExecutePITRAction(t.Context(), dsn, PITRActionRequest{
		Surface:    PostgresRecoverySurfaceControl,
		TargetType: "lsn",
		Target:     "0/123",
		Reason:     "test",
		Confirm:    true,
	})
	if err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Fatalf("invalid target type error = %v, want not valid failure", err)
	}
}
