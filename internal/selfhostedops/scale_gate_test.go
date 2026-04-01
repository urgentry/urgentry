package selfhostedops

import "testing"

func TestDefaultScaleValidationGateValidate(t *testing.T) {
	if err := DefaultScaleValidationGate().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDefaultScaleValidationGateIncludesCriticalChecks(t *testing.T) {
	gate := DefaultScaleValidationGate()
	if got, want := len(gate.Checks), 7; got != want {
		t.Fatalf("len(Checks) = %d, want %d", got, want)
	}
	var hasUpgrade, hasFailover, hasSoak bool
	for _, item := range gate.Checks {
		switch item.Check {
		case GateCheckRollingUpgrade:
			hasUpgrade = true
		case GateCheckFailover:
			hasFailover = true
		case GateCheckSoak:
			hasSoak = true
		}
	}
	if !hasUpgrade || !hasFailover || !hasSoak {
		t.Fatalf("gate checks missing upgrade=%t failover=%t soak=%t", hasUpgrade, hasFailover, hasSoak)
	}
}
