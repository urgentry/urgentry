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
	columnar, ok := plan.CapabilityForTier(EngineTierAnalyticsColumnar)
	if !ok {
		t.Fatal("missing analytics columnar capability")
	}
	if columnar.Supported {
		t.Fatalf("Analytics columnar Supported = true, want false until proof exists")
	}
	if len(columnar.RequiredProof) == 0 {
		t.Fatalf("Analytics columnar required proof is empty: %+v", columnar)
	}
	supported := plan.SupportedTiers()
	if len(supported) != 2 || supported[0] != EngineTierBridgePostgres || supported[1] != EngineTierBridgeTimescale {
		t.Fatalf("SupportedTiers = %+v, want postgres and timescale bridge tiers", supported)
	}
}

func TestEnginePlanValidateRequiresUnsupportedProof(t *testing.T) {
	plan := DefaultEnginePlan()
	for idx := range plan.Capabilities {
		if plan.Capabilities[idx].Tier == EngineTierAnalyticsColumnar {
			plan.Capabilities[idx].RequiredProof = nil
		}
	}
	if err := plan.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want unsupported capability proof failure")
	}
}
