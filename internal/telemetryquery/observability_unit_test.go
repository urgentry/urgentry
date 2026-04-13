package telemetryquery

import "testing"

func TestBridgeObservabilityDefaultPassesValidation(t *testing.T) {
	t.Parallel()
	if err := DefaultBridgeObservability().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestBridgeObservabilityValidateRejectsEmptyBudgets(t *testing.T) {
	t.Parallel()
	if err := (BridgeObservability{}).Validate(); err == nil {
		t.Fatal("empty budgets should fail")
	}
}

func TestBridgeObservabilityValidateRejectsZeroLag(t *testing.T) {
	t.Parallel()

	obs := DefaultBridgeObservability()
	obs.Budgets[0].LagBudgetSeconds = 0
	if err := obs.Validate(); err == nil {
		t.Fatal("zero lag budget should fail")
	}
}

func TestBridgeObservabilityValidateRejectsBadLagOrder(t *testing.T) {
	t.Parallel()

	obs := DefaultBridgeObservability()
	obs.Budgets[0].WarnLagSeconds = obs.Budgets[0].LagBudgetSeconds - 1
	if err := obs.Validate(); err == nil {
		t.Fatal("warn < budget lag should fail")
	}
}

func TestBridgeObservabilityValidateRejectsBadCostOrder(t *testing.T) {
	t.Parallel()

	obs := DefaultBridgeObservability()
	obs.Budgets[0].WarnCostBudgetUnits = obs.Budgets[0].DailyCostBudgetUnits - 1
	if err := obs.Validate(); err == nil {
		t.Fatal("warn < budget cost should fail")
	}
}

func TestBridgeObservabilityEvaluateOK(t *testing.T) {
	t.Parallel()

	obs := DefaultBridgeObservability()
	assessment, err := obs.Evaluate(BridgeObservation{
		Surface:            BridgeSurfaceDiscover,
		LagSeconds:         60,
		DailyCostUnits:     500,
		ProjectedDailyCost: 500,
	})
	if err != nil {
		t.Fatalf("Evaluate error = %v", err)
	}
	if assessment.Health != BridgeHealthOK {
		t.Fatalf("Health = %q, want ok", assessment.Health)
	}
}

func TestBridgeObservabilityEvaluateWarn(t *testing.T) {
	t.Parallel()

	obs := DefaultBridgeObservability()
	assessment, err := obs.Evaluate(BridgeObservation{
		Surface:            BridgeSurfaceDiscover,
		LagSeconds:         300,
		DailyCostUnits:     500,
		ProjectedDailyCost: 500,
	})
	if err != nil {
		t.Fatalf("Evaluate error = %v", err)
	}
	if assessment.Health != BridgeHealthWarn {
		t.Fatalf("Health = %q, want warn", assessment.Health)
	}
}

func TestBridgeObservabilityEvaluatePage(t *testing.T) {
	t.Parallel()

	obs := DefaultBridgeObservability()
	assessment, err := obs.Evaluate(BridgeObservation{
		Surface:            BridgeSurfaceDiscover,
		LagSeconds:         600,
		DailyCostUnits:     500,
		ProjectedDailyCost: 500,
	})
	if err != nil {
		t.Fatalf("Evaluate error = %v", err)
	}
	if assessment.Health != BridgeHealthPage {
		t.Fatalf("Health = %q, want page", assessment.Health)
	}
}

func TestBridgeObservabilityEvaluatePageByCost(t *testing.T) {
	t.Parallel()

	obs := DefaultBridgeObservability()
	assessment, err := obs.Evaluate(BridgeObservation{
		Surface:            BridgeSurfaceDiscover,
		LagSeconds:         0,
		DailyCostUnits:     0,
		ProjectedDailyCost: 1800,
	})
	if err != nil {
		t.Fatalf("Evaluate error = %v", err)
	}
	if assessment.Health != BridgeHealthPage {
		t.Fatalf("Health = %q, want page (cost triggered)", assessment.Health)
	}
}

func TestBridgeObservabilityEvaluateWarnByCost(t *testing.T) {
	t.Parallel()

	obs := DefaultBridgeObservability()
	assessment, err := obs.Evaluate(BridgeObservation{
		Surface:            BridgeSurfaceDiscover,
		LagSeconds:         0,
		DailyCostUnits:     0,
		ProjectedDailyCost: 1400,
	})
	if err != nil {
		t.Fatalf("Evaluate error = %v", err)
	}
	if assessment.Health != BridgeHealthWarn {
		t.Fatalf("Health = %q, want warn (cost triggered)", assessment.Health)
	}
}

func TestBridgeObservabilityEvaluateRejectsNegativeLag(t *testing.T) {
	t.Parallel()

	obs := DefaultBridgeObservability()
	_, err := obs.Evaluate(BridgeObservation{
		Surface:    BridgeSurfaceDiscover,
		LagSeconds: -1,
	})
	if err == nil {
		t.Fatal("negative lag should fail")
	}
}

func TestBridgeObservabilityEvaluateRejectsUnknownSurface(t *testing.T) {
	t.Parallel()

	obs := DefaultBridgeObservability()
	_, err := obs.Evaluate(BridgeObservation{
		Surface: BridgeSurface("bogus"),
	})
	if err == nil {
		t.Fatal("unknown surface should fail")
	}
}

func TestValidBridgeHealth(t *testing.T) {
	t.Parallel()

	if !ValidBridgeHealth(BridgeHealthOK) {
		t.Fatal("ok should be valid")
	}
	if !ValidBridgeHealth(BridgeHealthWarn) {
		t.Fatal("warn should be valid")
	}
	if !ValidBridgeHealth(BridgeHealthPage) {
		t.Fatal("page should be valid")
	}
	if ValidBridgeHealth(BridgeHealth("bogus")) {
		t.Fatal("bogus should be invalid")
	}
}

func TestEnginePlanDefaultPassesValidation(t *testing.T) {
	t.Parallel()
	if err := DefaultEnginePlan().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestEnginePlanValidateRejectsEmptyTier(t *testing.T) {
	t.Parallel()

	plan := DefaultEnginePlan()
	plan.DefaultTier = ""
	if err := plan.Validate(); err == nil {
		t.Fatal("empty default tier should fail")
	}
}

func TestEnginePlanValidateRejectsEmptyGraduationTarget(t *testing.T) {
	t.Parallel()

	plan := DefaultEnginePlan()
	plan.GraduationTarget = ""
	if err := plan.Validate(); err == nil {
		t.Fatal("empty graduation target should fail")
	}
}

func TestEnginePlanValidateRejectsEmptyTiers(t *testing.T) {
	t.Parallel()

	plan := DefaultEnginePlan()
	plan.OptionalTiers = nil
	if err := plan.Validate(); err == nil {
		t.Fatal("empty optional tiers should fail")
	}
}

func TestEnginePlanValidateRejectsEmptyTriggers(t *testing.T) {
	t.Parallel()

	plan := DefaultEnginePlan()
	plan.Triggers = nil
	if err := plan.Validate(); err == nil {
		t.Fatal("empty triggers should fail")
	}
}

func TestEnginePlanValidateRejectsIncompleteTrigger(t *testing.T) {
	t.Parallel()

	plan := DefaultEnginePlan()
	plan.Triggers[0].Name = ""
	if err := plan.Validate(); err == nil {
		t.Fatal("incomplete trigger should fail")
	}
}

func TestBridgeSurfaceConstants(t *testing.T) {
	t.Parallel()

	if BridgeSurfaceDiscover != "discover" {
		t.Fatalf("BridgeSurfaceDiscover = %q", BridgeSurfaceDiscover)
	}
	if BridgeSurfaceLogs != "logs" {
		t.Fatalf("BridgeSurfaceLogs = %q", BridgeSurfaceLogs)
	}
	if BridgeSurfaceTraces != "traces" {
		t.Fatalf("BridgeSurfaceTraces = %q", BridgeSurfaceTraces)
	}
}

func TestQuerySurfaceConstants(t *testing.T) {
	t.Parallel()

	if QuerySurfaceDiscoverLogs != "discover_logs" {
		t.Fatalf("QuerySurfaceDiscoverLogs = %q", QuerySurfaceDiscoverLogs)
	}
	if QuerySurfaceProfiles != "profiles" {
		t.Fatalf("QuerySurfaceProfiles = %q", QuerySurfaceProfiles)
	}
}

func TestFreshnessModeConstants(t *testing.T) {
	t.Parallel()

	if FreshnessModeServeStale != "serve_stale" {
		t.Fatalf("FreshnessModeServeStale = %q", FreshnessModeServeStale)
	}
	if FreshnessModeFailClosed != "fail_closed" {
		t.Fatalf("FreshnessModeFailClosed = %q", FreshnessModeFailClosed)
	}
}
