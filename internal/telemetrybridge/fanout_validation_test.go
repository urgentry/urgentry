package telemetrybridge

import (
	"strings"
	"testing"
)

func TestFanoutContractValidateRejectsEmptyMode(t *testing.T) {
	t.Parallel()

	contract := DefaultFanoutContract()
	contract.Mode = ""
	if err := contract.Validate(); err == nil {
		t.Fatal("empty mode should fail")
	}
}

func TestFanoutContractValidateRejectsEmptyDeliveryGuarantee(t *testing.T) {
	t.Parallel()

	contract := DefaultFanoutContract()
	contract.DeliveryGuarantee = ""
	if err := contract.Validate(); err == nil {
		t.Fatal("empty delivery guarantee should fail")
	}
}

func TestFanoutContractValidateRejectsZeroLagBudget(t *testing.T) {
	t.Parallel()

	contract := DefaultFanoutContract()
	contract.LagBudgetSeconds = 0
	if err := contract.Validate(); err == nil {
		t.Fatal("zero lag budget should fail")
	}
}

func TestFanoutContractValidateRejectsNegativeLagBudget(t *testing.T) {
	t.Parallel()

	contract := DefaultFanoutContract()
	contract.LagBudgetSeconds = -1
	if err := contract.Validate(); err == nil {
		t.Fatal("negative lag budget should fail")
	}
}

func TestFanoutContractValidateRejectsEmptyIdempotencyKey(t *testing.T) {
	t.Parallel()

	contract := DefaultFanoutContract()
	contract.IdempotencyKey = ""
	if err := contract.Validate(); err == nil {
		t.Fatal("empty idempotency key should fail")
	}
}

func TestFanoutContractValidateRejectsEmptyRequiredEvents(t *testing.T) {
	t.Parallel()

	contract := DefaultFanoutContract()
	contract.RequiredEvents = nil
	if err := contract.Validate(); err == nil {
		t.Fatal("empty required events should fail")
	}
}

func TestFanoutContractValidateRejectsEmptyRebuildSteps(t *testing.T) {
	t.Parallel()

	contract := DefaultFanoutContract()
	contract.RebuildHandoffSteps = nil
	if err := contract.Validate(); err == nil {
		t.Fatal("empty rebuild handoff steps should fail")
	}
}

func TestDefaultFanoutContractHasExpectedDefaults(t *testing.T) {
	t.Parallel()

	contract := DefaultFanoutContract()
	if contract.Mode != FanoutModeOutbox {
		t.Fatalf("Mode = %q, want outbox", contract.Mode)
	}
	if contract.DeliveryGuarantee != DeliveryGuaranteeAtLeastOnce {
		t.Fatalf("DeliveryGuarantee = %q, want at_least_once", contract.DeliveryGuarantee)
	}
	if contract.LagBudgetSeconds != 120 {
		t.Fatalf("LagBudgetSeconds = %d, want 120", contract.LagBudgetSeconds)
	}
}

func TestFanoutModeConstants(t *testing.T) {
	t.Parallel()

	if FanoutModeOutbox != "outbox" {
		t.Fatalf("FanoutModeOutbox = %q", FanoutModeOutbox)
	}
	if FanoutModeCDC != "cdc" {
		t.Fatalf("FanoutModeCDC = %q", FanoutModeCDC)
	}
}

func TestExportContractValidateRejectsWrongSurfaceCount(t *testing.T) {
	t.Parallel()

	contract := DefaultExportContract()
	contract.Surfaces = contract.Surfaces[:2]
	if err := contract.Validate(); err == nil {
		t.Fatal("wrong surface count should fail")
	}
}

func TestExportContractValidateRejectsMissingSurface(t *testing.T) {
	t.Parallel()

	contract := DefaultExportContract()
	contract.Surfaces[0].Surface = ExportSurface("bogus")
	if err := contract.Validate(); err == nil {
		t.Fatal("missing surface should fail")
	}
}

func TestExportContractValidateRejectsEmptyModes(t *testing.T) {
	t.Parallel()

	contract := DefaultExportContract()
	contract.Surfaces[0].Modes = nil
	if err := contract.Validate(); err == nil {
		t.Fatal("empty modes should fail")
	}
}

func TestExportContractValidateRejectsInvalidMode(t *testing.T) {
	t.Parallel()

	contract := DefaultExportContract()
	contract.Surfaces[0].Modes = append(contract.Surfaces[0].Modes, ExportMode("bogus"))
	if err := contract.Validate(); err == nil {
		t.Fatal("invalid mode should fail")
	}
}

func TestExportContractValidateRejectsNoCursorCheckpoint(t *testing.T) {
	t.Parallel()

	contract := DefaultExportContract()
	contract.Surfaces[0].RequiresCursorCheckpoint = false
	if err := contract.Validate(); err == nil {
		t.Fatal("disabled cursor checkpoint should fail")
	}
}

func TestExportContractValidateRejectsNoIdempotencyKey(t *testing.T) {
	t.Parallel()

	contract := DefaultExportContract()
	contract.Surfaces[0].RequiresIdempotencyKey = false
	if err := contract.Validate(); err == nil {
		t.Fatal("disabled idempotency key should fail")
	}
}

func TestExportContractSupportsMethod(t *testing.T) {
	t.Parallel()

	contract := DefaultExportContract()

	// Events supports all three modes
	if !contract.Supports(ExportSurfaceEvents, ExportModeSnapshot) {
		t.Fatal("events should support snapshot")
	}
	if !contract.Supports(ExportSurfaceEvents, ExportModeShadowDualWrite) {
		t.Fatal("events should support shadow dual write")
	}
	if !contract.Supports(ExportSurfaceEvents, ExportModeCutoverDualWrite) {
		t.Fatal("events should support cutover dual write")
	}

	// Profile now supports cutover
	if !contract.Supports(ExportSurfaceProfile, ExportModeCutoverDualWrite) {
		t.Fatal("profile should support cutover dual write")
	}

	// Unknown surface
	if contract.Supports(ExportSurface("bogus"), ExportModeSnapshot) {
		t.Fatal("unknown surface should not support any mode")
	}
}

func TestExportModeConstants(t *testing.T) {
	t.Parallel()

	if ExportModeSnapshot != "snapshot_export" {
		t.Fatalf("ExportModeSnapshot = %q", ExportModeSnapshot)
	}
	if ExportModeShadowDualWrite != "shadow_dual_write" {
		t.Fatalf("ExportModeShadowDualWrite = %q", ExportModeShadowDualWrite)
	}
	if ExportModeCutoverDualWrite != "cutover_dual_write" {
		t.Fatalf("ExportModeCutoverDualWrite = %q", ExportModeCutoverDualWrite)
	}
}

func TestMigrationsPostgresHasCorrectCount(t *testing.T) {
	t.Parallel()

	postgres := Migrations(BackendPostgres)
	timescale := Migrations(BackendTimescale)

	if len(postgres) != len(baseMigrations) {
		t.Fatalf("postgres migrations = %d, want %d", len(postgres), len(baseMigrations))
	}
	if len(timescale) != len(baseMigrations)+len(timescaleMigrations) {
		t.Fatalf("timescale migrations = %d, want %d", len(timescale), len(baseMigrations)+len(timescaleMigrations))
	}
}

func TestMigrationsAreSequentiallyVersioned(t *testing.T) {
	t.Parallel()

	for _, backend := range []Backend{BackendPostgres, BackendTimescale} {
		migrations := Migrations(backend)
		for i, m := range migrations {
			if m.Version != i+1 {
				t.Fatalf("migration %d has version %d, expected %d (backend=%s)", i, m.Version, i+1, backend)
			}
			if strings.TrimSpace(m.Name) == "" {
				t.Fatalf("migration %d has empty name (backend=%s)", m.Version, backend)
			}
			if strings.TrimSpace(m.SQL) == "" {
				t.Fatalf("migration %d has empty SQL (backend=%s)", m.Version, backend)
			}
		}
	}
}
