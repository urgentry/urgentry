package hosted

import "testing"

func TestDefaultRegionConfigPolicyValidate(t *testing.T) {
	if err := DefaultRegionConfigPolicy().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDefaultRegionConfigPolicyLayers(t *testing.T) {
	policy := DefaultRegionConfigPolicy()
	if got := policy.Layers[0].Scope; got != ConfigScopeEnvironment {
		t.Fatalf("first layer = %q, want %q", got, ConfigScopeEnvironment)
	}
	if got := policy.Layers[len(policy.Layers)-1].Scope; got != ConfigScopeCell {
		t.Fatalf("last layer = %q, want %q", got, ConfigScopeCell)
	}
}

func TestDefaultRegionConfigPolicyRotationKinds(t *testing.T) {
	policy := DefaultRegionConfigPolicy()
	dualRead := map[SecretKind]bool{}
	for _, kind := range policy.SecretRotation.DualReadKinds {
		dualRead[kind] = true
	}
	if !dualRead[SecretKindSessionSigning] {
		t.Fatal("session signing should support dual read")
	}
	if dualRead[SecretKindBootstrap] {
		t.Fatal("bootstrap should not support dual read")
	}
	restartRequired := map[SecretKind]bool{}
	for _, kind := range policy.SecretRotation.RestartRequired {
		restartRequired[kind] = true
	}
	if !restartRequired[SecretKindBootstrap] {
		t.Fatal("bootstrap should require restart")
	}
}
