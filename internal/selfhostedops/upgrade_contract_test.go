package selfhostedops

import "testing"

func TestDefaultUpgradeContractValidate(t *testing.T) {
	if err := DefaultUpgradeContract().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDefaultUpgradeContractProtectsSchemaRollback(t *testing.T) {
	contract := DefaultUpgradeContract()
	if got, want := len(contract.RollbackSafeguards) > 0, true; got != want {
		t.Fatalf("RollbackSafeguards empty = %t, want %t", !got, !want)
	}
	var appRuleFound bool
	for _, rule := range contract.Rules {
		if rule.Component == UpgradeComponentAppBundle {
			appRuleFound = true
			if rule.MaxVersionsBehind != 1 {
				t.Fatalf("app bundle max behind = %d, want 1", rule.MaxVersionsBehind)
			}
		}
	}
	if !appRuleFound {
		t.Fatal("missing app bundle upgrade rule")
	}
}
