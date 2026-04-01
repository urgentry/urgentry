package selfhostedops

import "testing"

func TestDefaultDistributionContractValidate(t *testing.T) {
	if err := DefaultDistributionContract().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDefaultDistributionContractIncludesHelmPath(t *testing.T) {
	contract := DefaultDistributionContract()
	var hasHelm bool
	for _, bundle := range contract.Bundles {
		if bundle.Kind == DistributionHelm {
			hasHelm = true
			if bundle.SecretSource != SecretSourceExternalSecrets {
				t.Fatalf("helm secret source = %q, want %q", bundle.SecretSource, SecretSourceExternalSecrets)
			}
		}
	}
	if !hasHelm {
		t.Fatal("helm bundle missing")
	}
}
