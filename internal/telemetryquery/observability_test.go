package telemetryquery

import "testing"

func TestDefaultBridgeObservabilityValidate(t *testing.T) {
	if err := DefaultBridgeObservability().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestEvaluateBridgeObservabilityWarnsOnLag(t *testing.T) {
	assessment, err := DefaultBridgeObservability().Evaluate(BridgeObservation{
		Surface:            BridgeSurfaceDiscover,
		LagSeconds:         360,
		DailyCostUnits:     900,
		ProjectedDailyCost: 1200,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if got, want := assessment.Health, BridgeHealthWarn; got != want {
		t.Fatalf("Health = %q, want %q", got, want)
	}
}

func TestEvaluateBridgeObservabilityPagesOnCost(t *testing.T) {
	assessment, err := DefaultBridgeObservability().Evaluate(BridgeObservation{
		Surface:            BridgeSurfaceReplay,
		LagSeconds:         120,
		DailyCostUnits:     900,
		ProjectedDailyCost: 1600,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if got, want := assessment.Health, BridgeHealthPage; got != want {
		t.Fatalf("Health = %q, want %q", got, want)
	}
}
