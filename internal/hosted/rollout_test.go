package hosted

import "testing"

func TestDefaultRolloutContractValidate(t *testing.T) {
	if err := DefaultRolloutContract().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDefaultRolloutContractStages(t *testing.T) {
	contract := DefaultRolloutContract()
	if got := contract.Steps[0].Stage; got != RolloutStageFreezeRelease {
		t.Fatalf("first stage = %q, want %q", got, RolloutStageFreezeRelease)
	}
	canary := contract.Steps[4]
	if canary.Scope != RolloutScopeCell {
		t.Fatalf("canary scope = %q, want %q", canary.Scope, RolloutScopeCell)
	}
	if canary.TrafficMode != TrafficModeDrainWrites {
		t.Fatalf("canary traffic mode = %q, want %q", canary.TrafficMode, TrafficModeDrainWrites)
	}
}

func TestDefaultRolloutContractIncludesCriticalDrills(t *testing.T) {
	contract := DefaultRolloutContract()
	if len(contract.RequiredDrills) < 8 {
		t.Fatalf("required drills = %d, want at least 8", len(contract.RequiredDrills))
	}
	last := contract.Steps[len(contract.Steps)-1]
	if got := last.Stage; got != RolloutStageRemainingRegions {
		t.Fatalf("last stage = %q, want %q", got, RolloutStageRemainingRegions)
	}
	if got := last.AllowedRollback[len(last.AllowedRollback)-1]; got != RollbackClassSchemaRestore {
		t.Fatalf("last rollback class = %q, want %q", got, RollbackClassSchemaRestore)
	}
}
