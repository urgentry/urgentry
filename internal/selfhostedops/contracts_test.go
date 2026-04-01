package selfhostedops

import "testing"

func TestUpgradeContractDefaultPassesValidation(t *testing.T) {
	t.Parallel()
	if err := DefaultUpgradeContract().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestUpgradeContractValidateRejectsEmpty(t *testing.T) {
	t.Parallel()

	if err := (UpgradeContract{}).Validate(); err == nil {
		t.Fatal("empty contract should fail validation")
	}
}

func TestUpgradeContractValidateRejectsMissingCanaryStages(t *testing.T) {
	t.Parallel()

	contract := DefaultUpgradeContract()
	contract.CanaryStages = nil
	if err := contract.Validate(); err == nil {
		t.Fatal("missing canary stages should fail")
	}
}

func TestUpgradeContractValidateRejectsMissingRollbackSafeguards(t *testing.T) {
	t.Parallel()

	contract := DefaultUpgradeContract()
	contract.RollbackSafeguards = nil
	if err := contract.Validate(); err == nil {
		t.Fatal("missing rollback safeguards should fail")
	}
}

func TestUpgradeContractValidateRejectsNegativeSkew(t *testing.T) {
	t.Parallel()

	contract := DefaultUpgradeContract()
	contract.Rules[0].MaxVersionsBehind = -1
	if err := contract.Validate(); err == nil {
		t.Fatal("negative skew should fail")
	}
}

func TestUpgradeContractValidateRejectsEmptyNotes(t *testing.T) {
	t.Parallel()

	contract := DefaultUpgradeContract()
	contract.Rules[0].Notes = nil
	if err := contract.Validate(); err == nil {
		t.Fatal("empty notes should fail")
	}
}

func TestDistributionContractDefaultPassesValidation(t *testing.T) {
	t.Parallel()
	if err := DefaultDistributionContract().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDistributionContractValidateRejectsEmptyBundles(t *testing.T) {
	t.Parallel()
	if err := (DistributionContract{}).Validate(); err == nil {
		t.Fatal("empty bundles should fail")
	}
}

func TestDistributionContractValidateRejectsDuplicateKind(t *testing.T) {
	t.Parallel()

	contract := DefaultDistributionContract()
	contract.Bundles[1].Kind = contract.Bundles[0].Kind
	if err := contract.Validate(); err == nil {
		t.Fatal("duplicate kind should fail")
	}
}

func TestDistributionContractValidateRejectsEmptyArtifacts(t *testing.T) {
	t.Parallel()

	contract := DefaultDistributionContract()
	contract.Bundles[0].RequiredArtifacts = nil
	if err := contract.Validate(); err == nil {
		t.Fatal("empty artifacts should fail")
	}
}

func TestPITRContractDefaultPassesValidation(t *testing.T) {
	t.Parallel()
	if err := DefaultPITRContract().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestPITRContractValidateRejectsEmptyRequirements(t *testing.T) {
	t.Parallel()
	if err := (PITRContract{}).Validate(); err == nil {
		t.Fatal("empty requirements should fail")
	}
}

func TestPITRContractValidateRejectsNoWAL(t *testing.T) {
	t.Parallel()

	contract := DefaultPITRContract()
	contract.Requirements[0].RequiresWALArchive = false
	if err := contract.Validate(); err == nil {
		t.Fatal("no WAL requirement should fail")
	}
}

func TestPITRContractValidateRejectsEmptyWorkflow(t *testing.T) {
	t.Parallel()

	contract := DefaultPITRContract()
	contract.Workflow = nil
	if err := contract.Validate(); err == nil {
		t.Fatal("empty workflow should fail")
	}
}

func TestPITRContractValidateRejectsEmptyBoundaries(t *testing.T) {
	t.Parallel()

	contract := DefaultPITRContract()
	contract.Boundaries = nil
	if err := contract.Validate(); err == nil {
		t.Fatal("empty boundaries should fail")
	}
}

func TestRepairPackDefaultPassesValidation(t *testing.T) {
	t.Parallel()
	if err := DefaultRepairPack().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestRepairPackValidateRejectsEmptySurfaces(t *testing.T) {
	t.Parallel()
	if err := (RepairPack{}).Validate(); err == nil {
		t.Fatal("empty surfaces should fail")
	}
}

func TestRepairPackValidateRejectsMissingSurface(t *testing.T) {
	t.Parallel()

	pack := DefaultRepairPack()
	pack.Surfaces[0].Surface = "nonexistent"
	if err := pack.Validate(); err == nil {
		t.Fatal("missing surface should fail")
	}
}

func TestRepairPackValidateRejectsInvalidAction(t *testing.T) {
	t.Parallel()

	pack := DefaultRepairPack()
	pack.Surfaces[0].Actions = []RepairAction{"bogus_action"}
	if err := pack.Validate(); err == nil {
		t.Fatal("invalid action should fail")
	}
}

func TestScaleGateDefaultPassesValidation(t *testing.T) {
	t.Parallel()
	if err := DefaultScaleValidationGate().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestScaleValidationGateValidateRejectsEmpty(t *testing.T) {
	t.Parallel()
	if err := (ScaleValidationGate{}).Validate(); err == nil {
		t.Fatal("empty gate should fail")
	}
}

func TestScaleValidationGateValidateRejectsDuplicate(t *testing.T) {
	t.Parallel()

	gate := DefaultScaleValidationGate()
	gate.Checks[1].Check = gate.Checks[0].Check
	if err := gate.Validate(); err == nil {
		t.Fatal("duplicate check should fail")
	}
}

func TestScaleValidationGateValidateRejectsIncomplete(t *testing.T) {
	t.Parallel()

	gate := DefaultScaleValidationGate()
	gate.Checks[0].Command = ""
	if err := gate.Validate(); err == nil {
		t.Fatal("incomplete check should fail")
	}
}

func TestSLOPackDefaultPassesValidation(t *testing.T) {
	t.Parallel()
	if err := DefaultSLOPack().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestSLOPackValidateRejectsEmptyPlanes(t *testing.T) {
	t.Parallel()
	if err := (SLOPack{}).Validate(); err == nil {
		t.Fatal("empty planes should fail")
	}
}

func TestSLOPackValidateRejectsMissingPlane(t *testing.T) {
	t.Parallel()

	pack := DefaultSLOPack()
	pack.Planes[0].Plane = "nonexistent"
	if err := pack.Validate(); err == nil {
		t.Fatal("missing plane should fail")
	}
}

func TestSLOPackValidateRejectsInvalidAlertSeverity(t *testing.T) {
	t.Parallel()

	pack := DefaultSLOPack()
	pack.Planes[0].Alerts[0].Severity = "critical"
	if err := pack.Validate(); err == nil {
		t.Fatal("invalid alert severity should fail")
	}
}

func TestMigrationGateDefaultPassesValidation(t *testing.T) {
	t.Parallel()
	if err := DefaultMigrationCompatibilityGate().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestMigrationCompatibilityGateRejectsZeroLags(t *testing.T) {
	t.Parallel()

	gate := DefaultMigrationCompatibilityGate()
	gate.MaxBinaryLag = 0
	if err := gate.Validate(); err == nil {
		t.Fatal("zero lag should fail")
	}
}

func TestMigrationCompatibilityGateRejectsEmptyProofs(t *testing.T) {
	t.Parallel()

	gate := DefaultMigrationCompatibilityGate()
	gate.RequiredProofs = nil
	if err := gate.Validate(); err == nil {
		t.Fatal("empty proofs should fail")
	}
}

func TestMigrationCompatibilityGateEvaluate(t *testing.T) {
	t.Parallel()

	gate := DefaultMigrationCompatibilityGate()

	// Compatible: within limits
	report, err := gate.Evaluate(MigrationCompatibilityRequest{
		TargetBinaryVersion:   5,
		TargetControlSchema:   5,
		TargetTelemetrySchema: 5,
		OldestBinaryVersion:   4,
		OldestControlSchema:   4,
		OldestTelemetrySchema: 4,
	})
	if err != nil {
		t.Fatalf("Evaluate error = %v", err)
	}
	if !report.Compatible {
		t.Fatalf("expected compatible, violations = %v", report.Violations)
	}
	if !report.SupportsNMinusOne {
		t.Fatal("should support N-1")
	}

	// Incompatible: binary lag too large
	report, err = gate.Evaluate(MigrationCompatibilityRequest{
		TargetBinaryVersion:   10,
		TargetControlSchema:   10,
		TargetTelemetrySchema: 10,
		OldestBinaryVersion:   5,
		OldestControlSchema:   10,
		OldestTelemetrySchema: 10,
	})
	if err != nil {
		t.Fatalf("Evaluate error = %v", err)
	}
	if report.Compatible {
		t.Fatal("expected incompatible for large binary lag")
	}
	if report.BinaryLag != 5 {
		t.Fatalf("BinaryLag = %d, want 5", report.BinaryLag)
	}
}

func TestMigrationCompatibilityGateEvaluateRejectsNegativeTargets(t *testing.T) {
	t.Parallel()

	gate := DefaultMigrationCompatibilityGate()
	if _, err := gate.Evaluate(MigrationCompatibilityRequest{TargetBinaryVersion: -1}); err == nil {
		t.Fatal("negative target should fail")
	}
	if _, err := gate.Evaluate(MigrationCompatibilityRequest{OldestBinaryVersion: -1}); err == nil {
		t.Fatal("negative oldest should fail")
	}
}

func TestMigrationCompatibilityGateEvaluateRejectsOldestAheadOfTarget(t *testing.T) {
	t.Parallel()

	gate := DefaultMigrationCompatibilityGate()
	_, err := gate.Evaluate(MigrationCompatibilityRequest{
		TargetBinaryVersion:   3,
		TargetControlSchema:   3,
		TargetTelemetrySchema: 3,
		OldestBinaryVersion:   5,
		OldestControlSchema:   3,
		OldestTelemetrySchema: 3,
	})
	if err == nil {
		t.Fatal("oldest ahead of target should fail")
	}
}

func TestMigrationCompatibilityNMinusTwo(t *testing.T) {
	t.Parallel()

	gate := DefaultMigrationCompatibilityGate()
	report, err := gate.Evaluate(MigrationCompatibilityRequest{
		TargetBinaryVersion:   5,
		TargetControlSchema:   5,
		TargetTelemetrySchema: 5,
		OldestBinaryVersion:   3,
		OldestControlSchema:   3,
		OldestTelemetrySchema: 3,
	})
	if err != nil {
		t.Fatalf("Evaluate error = %v", err)
	}
	if !report.SupportsNMinusTwo {
		t.Fatal("expected N-2 support with lag=2")
	}
	if report.SupportsNMinusOne {
		t.Fatal("should NOT support N-1 with lag=2")
	}
}

func TestCheckMixedVersionClusterRejectsNoNodes(t *testing.T) {
	t.Parallel()

	_, err := CheckMixedVersionCluster(DefaultUpgradeContract(), ClusterVersionState{
		ControlSchemaVersion:   1,
		TelemetrySchemaVersion: 1,
		Nodes:                  nil,
	})
	if err == nil {
		t.Fatal("expected error for no api/ingest nodes")
	}
}

func TestCheckMixedVersionClusterRejectsNegativeSchemaVersion(t *testing.T) {
	t.Parallel()

	_, err := CheckMixedVersionCluster(DefaultUpgradeContract(), ClusterVersionState{
		ControlSchemaVersion:   -1,
		TelemetrySchemaVersion: 1,
		Nodes: []NodeVersion{
			{Role: ClusterRoleAPI, BinaryVersion: 1},
		},
	})
	if err == nil {
		t.Fatal("expected error for negative schema version")
	}
}

func TestCheckMixedVersionClusterDetectsSchedulerSkew(t *testing.T) {
	t.Parallel()

	report, err := CheckMixedVersionCluster(DefaultUpgradeContract(), ClusterVersionState{
		ControlSchemaVersion:   10,
		TelemetrySchemaVersion: 10,
		Nodes: []NodeVersion{
			{Role: ClusterRoleAPI, BinaryVersion: 10},
			{Role: ClusterRoleScheduler, BinaryVersion: 7},
		},
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if report.Compatible {
		t.Fatal("expected incompatible for scheduler skew")
	}
}

func TestCheckMixedVersionClusterNoWorkersOrSchedulers(t *testing.T) {
	t.Parallel()

	report, err := CheckMixedVersionCluster(DefaultUpgradeContract(), ClusterVersionState{
		ControlSchemaVersion:   10,
		TelemetrySchemaVersion: 10,
		Nodes: []NodeVersion{
			{Role: ClusterRoleAPI, BinaryVersion: 10},
			{Role: ClusterRoleIngest, BinaryVersion: 10},
		},
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !report.Compatible {
		t.Fatalf("expected compatible with no workers/schedulers, violations = %v", report.Violations)
	}
}
