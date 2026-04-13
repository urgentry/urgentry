package selfhostedops

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestDefaultDistributionContractArtifactsExist(t *testing.T) {
	appRoot := filepath.Join("..", "..")
	for _, bundle := range DefaultDistributionContract().Bundles {
		for _, artifact := range bundle.RequiredArtifacts {
			path := filepath.Join(appRoot, artifact)
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("%s artifact %s missing at %s: %v", bundle.Kind, artifact, path, err)
			}
		}
	}
}
