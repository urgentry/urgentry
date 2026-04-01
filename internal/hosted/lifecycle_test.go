package hosted

import "testing"

func TestDefaultLifecyclePolicyValidate(t *testing.T) {
	if err := DefaultLifecyclePolicy().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestLifecyclePolicyRejectsRestoreFromActive(t *testing.T) {
	req := LifecycleRequest{
		AccountID:           "acct_1",
		OrganizationID:      "org_1",
		Action:              LifecycleActionRestoreAccount,
		CurrentAccount:      AccountStatusActive,
		TargetAccount:       AccountStatusActive,
		CurrentOrganization: OrganizationStatusActive,
	}
	if err := DefaultLifecyclePolicy().ValidateRequest(req); err == nil {
		t.Fatal("ValidateRequest() error = nil, want invalid restore")
	}
}

func TestLifecyclePolicyRequiresReplacementOwner(t *testing.T) {
	req := LifecycleRequest{
		AccountID:           "acct_1",
		OrganizationID:      "org_1",
		Action:              LifecycleActionTransferOwnership,
		CurrentAccount:      AccountStatusActive,
		CurrentOrganization: OrganizationStatusActive,
	}
	if err := DefaultLifecyclePolicy().ValidateRequest(req); err == nil {
		t.Fatal("ValidateRequest() error = nil, want missing replacement owner")
	}
}

func TestLifecyclePolicyAllowsAbuseSuspension(t *testing.T) {
	req := LifecycleRequest{
		AccountID:           "acct_1",
		OrganizationID:      "org_1",
		Action:              LifecycleActionSuspendAccount,
		Reason:              SuspensionReasonAbuseTraffic,
		CurrentAccount:      AccountStatusActive,
		TargetAccount:       AccountStatusSuspended,
		CurrentOrganization: OrganizationStatusActive,
	}
	if err := DefaultLifecyclePolicy().ValidateRequest(req); err != nil {
		t.Fatalf("ValidateRequest() error = %v", err)
	}
}
