package hosted

import "testing"

func TestDefaultTenancyModelValidate(t *testing.T) {
	if err := DefaultTenancyModel().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAccountRequiresRegionPinningEntitlement(t *testing.T) {
	model := DefaultTenancyModel()
	account := Account{
		ID:         "acct_1",
		Slug:       "acme",
		Plan:       PlanTeam,
		Status:     AccountStatusActive,
		HomeRegion: "us",
		Residency: ResidencyPolicy{
			Mode:           ResidencyModePinned,
			AllowedRegions: []string{"us", "eu"},
		},
	}
	if err := model.ValidateAccount(account); err == nil {
		t.Fatal("ValidateAccount() error = nil, want region pinning failure")
	}
}

func TestValidateAssignmentRejectsWrongCellRegion(t *testing.T) {
	model := DefaultTenancyModel()
	account := Account{
		ID:         "acct_1",
		Slug:       "acme",
		Plan:       PlanBusiness,
		Status:     AccountStatusActive,
		HomeRegion: "us",
		Residency: ResidencyPolicy{
			Mode:           ResidencyModePinned,
			AllowedRegions: []string{"us", "eu"},
		},
	}
	org := Organization{
		ID:         "org_1",
		Slug:       "acme",
		AccountID:  "acct_1",
		Status:     OrganizationStatusActive,
		HomeRegion: "us",
		Isolation:  IsolationModeSharedCell,
	}
	project := ProjectPlacement{
		ProjectID:      "proj_1",
		OrganizationID: "org_1",
		Region:         "eu",
		Cell:           "us-a",
		Isolation:      IsolationModeSharedCell,
	}
	if err := model.ValidateAssignment(account, org, project); err == nil {
		t.Fatal("ValidateAssignment() error = nil, want cell-region mismatch")
	}
}

func TestValidateAssignmentAllowsPinnedBusinessTenant(t *testing.T) {
	model := DefaultTenancyModel()
	account := Account{
		ID:         "acct_1",
		Slug:       "acme",
		Plan:       PlanBusiness,
		Status:     AccountStatusActive,
		HomeRegion: "us",
		Residency: ResidencyPolicy{
			Mode:           ResidencyModePinned,
			AllowedRegions: []string{"us", "eu"},
		},
	}
	org := Organization{
		ID:         "org_1",
		Slug:       "acme",
		AccountID:  "acct_1",
		Status:     OrganizationStatusActive,
		HomeRegion: "us",
		Isolation:  IsolationModeDedicatedCell,
	}
	project := ProjectPlacement{
		ProjectID:      "proj_1",
		OrganizationID: "org_1",
		Region:         "eu",
		Cell:           "eu-a",
		Isolation:      IsolationModeDedicatedCell,
	}
	if err := model.ValidateAssignment(account, org, project); err != nil {
		t.Fatalf("ValidateAssignment() error = %v", err)
	}
}
