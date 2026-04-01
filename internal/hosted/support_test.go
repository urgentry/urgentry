package hosted

import "testing"

func TestDefaultSupportCatalogValidate(t *testing.T) {
	if err := DefaultSupportCatalog().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestSupportPolicyForPlan(t *testing.T) {
	policy, err := DefaultSupportCatalog().PolicyForPlan(DefaultCatalog(), PlanTeam)
	if err != nil {
		t.Fatalf("PolicyForPlan() error = %v", err)
	}
	if policy.Tier != SupportTierStandard {
		t.Fatalf("tier = %q, want %q", policy.Tier, SupportTierStandard)
	}
	if !containsSupportScope(policy.AllowedScopes, SupportScopeProject) {
		t.Fatal("team support policy should allow project scope")
	}
}

func TestValidateRequestRejectsUnsupportedStarterWrite(t *testing.T) {
	req := SupportRequest{
		AccountID:      "acct_1",
		OrganizationID: "org_1",
		ProjectID:      "proj_1",
		Scope:          SupportScopeProject,
		Capability:     SupportCapabilityWriteTenant,
		DurationMin:    15,
	}
	if err := DefaultSupportCatalog().ValidateRequest(DefaultCatalog(), PlanStarter, req); err == nil {
		t.Fatal("ValidateRequest() error = nil, want unsupported capability failure")
	}
}

func TestValidateRequestAllowsEnterpriseBreakGlass(t *testing.T) {
	req := SupportRequest{
		AccountID:      "acct_1",
		OrganizationID: "org_1",
		Scope:          SupportScopeOrganization,
		Capability:     SupportCapabilityReadTenant,
		DurationMin:    15,
		BreakGlass:     true,
	}
	if err := DefaultSupportCatalog().ValidateRequest(DefaultCatalog(), PlanEnterprise, req); err != nil {
		t.Fatalf("ValidateRequest() error = %v", err)
	}
}

func containsSupportScope(scopes []SupportScope, want SupportScope) bool {
	for _, scope := range scopes {
		if scope == want {
			return true
		}
	}
	return false
}
