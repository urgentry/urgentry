package blob

import "testing"

func TestDefaultLifecycleContractValidate(t *testing.T) {
	if err := DefaultLifecycleContract().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDefaultLifecycleContractKeepsDebugArtifactsHot(t *testing.T) {
	contract := DefaultLifecycleContract()
	for _, rule := range contract.Rules {
		if rule.Surface == SurfaceDebug {
			if rule.PrimaryTier != ArchiveTierHot {
				t.Fatalf("debug artifact tier = %q, want %q", rule.PrimaryTier, ArchiveTierHot)
			}
			if rule.ColdArchiveAllowed {
				t.Fatal("debug artifacts should not allow cold archive by default")
			}
		}
	}
}
