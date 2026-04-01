package hosted

import "testing"

func TestDefaultSupportDrillsValidate(t *testing.T) {
	if err := ValidateSupportDrills(DefaultSupportDrills()); err != nil {
		t.Fatalf("ValidateSupportDrills() error = %v", err)
	}
}

func TestDefaultSupportDrillsCoverHighRiskScenarios(t *testing.T) {
	drills := DefaultSupportDrills()
	if got := drills[0].ID; got != DrillLockedOutOwnerRecovery {
		t.Fatalf("first drill = %q, want %q", got, DrillLockedOutOwnerRecovery)
	}
	if got := drills[len(drills)-1].ID; got != DrillRedactedDiagnosticsExport {
		t.Fatalf("last drill = %q, want %q", got, DrillRedactedDiagnosticsExport)
	}
}

func TestDefaultSupportDrillsRequireAuditEvidence(t *testing.T) {
	for _, drill := range DefaultSupportDrills() {
		if len(drill.Evidence) != len(evidenceOrder) {
			t.Fatalf("%s evidence count = %d, want %d", drill.ID, len(drill.Evidence), len(evidenceOrder))
		}
		if len(drill.RequiredActors) != len(actorOrder) {
			t.Fatalf("%s actor count = %d, want %d", drill.ID, len(drill.RequiredActors), len(actorOrder))
		}
	}
}
