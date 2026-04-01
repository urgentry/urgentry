package hosted

import "testing"

func TestPlaceProjectUsesDedicatedCellForDedicatedOrg(t *testing.T) {
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
		AccountID:  account.ID,
		Status:     OrganizationStatusActive,
		HomeRegion: "us",
		Isolation:  IsolationModeDedicatedCell,
	}
	project, err := model.PlaceProject(account, org, PlacementRequest{
		ProjectID:      "proj_1",
		OrganizationID: org.ID,
		Mode:           PlacementModePinned,
		Region:         "eu",
	})
	if err != nil {
		t.Fatalf("PlaceProject() error = %v", err)
	}
	if got, want := project.Cell, "eu-d1"; got != want {
		t.Fatalf("Cell = %q, want %q", got, want)
	}
	if got, want := project.Isolation, IsolationModeDedicatedCell; got != want {
		t.Fatalf("Isolation = %q, want %q", got, want)
	}
}

func TestPlaceProjectRejectsPinnedRegionOutsideResidency(t *testing.T) {
	model := DefaultTenancyModel()
	account := Account{
		ID:         "acct_1",
		Slug:       "acme",
		Plan:       PlanTeam,
		Status:     AccountStatusActive,
		HomeRegion: "us",
		Residency: ResidencyPolicy{
			Mode: ResidencyModeHomeOnly,
		},
	}
	org := Organization{
		ID:         "org_1",
		Slug:       "acme",
		AccountID:  account.ID,
		Status:     OrganizationStatusActive,
		HomeRegion: "us",
		Isolation:  IsolationModeSharedCell,
	}
	_, err := model.PlaceProject(account, org, PlacementRequest{
		ProjectID:      "proj_1",
		OrganizationID: org.ID,
		Mode:           PlacementModePinned,
		Region:         "eu",
	})
	if err == nil {
		t.Fatal("PlaceProject() error = nil, want residency failure")
	}
}

func TestPlaceProjectUsesAccountDefaultRegion(t *testing.T) {
	model := DefaultTenancyModel()
	account := Account{
		ID:         "acct_1",
		Slug:       "acme",
		Plan:       PlanTeam,
		Status:     AccountStatusActive,
		HomeRegion: "us",
		Residency: ResidencyPolicy{
			Mode: ResidencyModeHomeOnly,
		},
	}
	org := Organization{
		ID:         "org_1",
		Slug:       "acme",
		AccountID:  account.ID,
		Status:     OrganizationStatusActive,
		HomeRegion: "us",
		Isolation:  IsolationModeSharedCell,
	}
	project, err := model.PlaceProject(account, org, PlacementRequest{
		ProjectID:      "proj_1",
		OrganizationID: org.ID,
		Mode:           PlacementModeAccountDefault,
	})
	if err != nil {
		t.Fatalf("PlaceProject() error = %v", err)
	}
	if got, want := project.Region, "us"; got != want {
		t.Fatalf("Region = %q, want %q", got, want)
	}
	if got, want := project.Cell, "us-a"; got != want {
		t.Fatalf("Cell = %q, want %q", got, want)
	}
}
