package selfhostedops

import "testing"

func TestDefaultMigrationCompatibilityGateValidate(t *testing.T) {
	if err := DefaultMigrationCompatibilityGate().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestMigrationCompatibilityGateSupportsNMinusTwo(t *testing.T) {
	report, err := DefaultMigrationCompatibilityGate().Evaluate(MigrationCompatibilityRequest{
		TargetBinaryVersion:   10,
		TargetControlSchema:   10,
		TargetTelemetrySchema: 10,
		OldestBinaryVersion:   8,
		OldestControlSchema:   8,
		OldestTelemetrySchema: 8,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !report.Compatible || !report.SupportsNMinusTwo {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestMigrationCompatibilityGateRejectsBeyondNMinusTwo(t *testing.T) {
	report, err := DefaultMigrationCompatibilityGate().Evaluate(MigrationCompatibilityRequest{
		TargetBinaryVersion:   10,
		TargetControlSchema:   10,
		TargetTelemetrySchema: 10,
		OldestBinaryVersion:   7,
		OldestControlSchema:   7,
		OldestTelemetrySchema: 7,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if report.Compatible {
		t.Fatalf("Compatible = true, want false")
	}
}
