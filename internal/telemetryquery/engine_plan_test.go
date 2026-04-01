package telemetryquery

import "testing"

func TestDefaultEnginePlanValidate(t *testing.T) {
	if err := DefaultEnginePlan().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDefaultEnginePlanGraduatesPastBridge(t *testing.T) {
	plan := DefaultEnginePlan()
	if got, want := plan.DefaultTier, EngineTierBridgePostgres; got != want {
		t.Fatalf("DefaultTier = %q, want %q", got, want)
	}
	if got, want := plan.GraduationTarget, EngineTierAnalyticsColumnar; got != want {
		t.Fatalf("GraduationTarget = %q, want %q", got, want)
	}
	if got, want := len(plan.Triggers), 4; got != want {
		t.Fatalf("len(Triggers) = %d, want %d", got, want)
	}
}
