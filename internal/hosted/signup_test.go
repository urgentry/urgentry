package hosted

import "testing"

func TestBuildSignupPlanUsesTrialDefaults(t *testing.T) {
	plan, err := BuildSignupPlan(DefaultTenancyModel(), SignupRequest{
		AccountSlug:      "acme",
		OwnerEmail:       "owner@example.com",
		OrganizationSlug: "acme-org",
		TeamSlug:         "platform",
		ProjectSlug:      "api",
		Mode:             SignupModeBlank,
	})
	if err != nil {
		t.Fatalf("BuildSignupPlan() error = %v", err)
	}
	if got, want := plan.Account.Plan, PlanTeam; got != want {
		t.Fatalf("Account.Plan = %q, want %q", got, want)
	}
	if got, want := plan.Trial.Plan, PlanTeam; got != want {
		t.Fatalf("Trial.Plan = %q, want %q", got, want)
	}
	if got, want := plan.Project.Region, "us"; got != want {
		t.Fatalf("Project.Region = %q, want %q", got, want)
	}
}

func TestBuildSignupPlanUsesFirstActiveCell(t *testing.T) {
	model := DefaultTenancyModel()
	model.Regions[0].Cells[0].AcceptsNewTenants = false
	plan, err := BuildSignupPlan(model, SignupRequest{
		AccountSlug:      "acme",
		OwnerEmail:       "owner@example.com",
		OrganizationSlug: "acme-org",
		TeamSlug:         "platform",
		ProjectSlug:      "api",
		HomeRegion:       "us",
		Mode:             SignupModeBlank,
	})
	if err != nil {
		t.Fatalf("BuildSignupPlan() error = %v", err)
	}
	if got, want := plan.Project.Cell, "us-b"; got != want {
		t.Fatalf("Project.Cell = %q, want %q", got, want)
	}
}

func TestBuildSignupPlanAddsImportAssistant(t *testing.T) {
	plan, err := BuildSignupPlan(DefaultTenancyModel(), SignupRequest{
		AccountSlug:      "acme",
		OwnerEmail:       "owner@example.com",
		OrganizationSlug: "acme-org",
		TeamSlug:         "platform",
		ProjectSlug:      "api",
		Mode:             SignupModeImport,
	})
	if err != nil {
		t.Fatalf("BuildSignupPlan() error = %v", err)
	}
	if got, want := plan.ImportAssistantURL, "/onboarding/import/"; got != want {
		t.Fatalf("ImportAssistantURL = %q, want %q", got, want)
	}
	if got, want := plan.Steps[len(plan.Steps)-1], SignupStepStartImportAssist; got != want {
		t.Fatalf("last signup step = %q, want %q", got, want)
	}
}

func TestBuildSignupPlanRejectsMissingFields(t *testing.T) {
	_, err := BuildSignupPlan(DefaultTenancyModel(), SignupRequest{
		AccountSlug: "acme",
		Mode:        SignupModeBlank,
	})
	if err == nil {
		t.Fatal("BuildSignupPlan() error = nil, want validation error")
	}
}

func TestBuildSignupPlanRejectsRegionWithoutBootstrapCell(t *testing.T) {
	model := DefaultTenancyModel()
	model.Regions[0].Cells[0].AcceptsNewTenants = false
	model.Regions[0].Cells[1].AcceptsNewTenants = false
	_, err := BuildSignupPlan(model, SignupRequest{
		AccountSlug:      "acme",
		OwnerEmail:       "owner@example.com",
		OrganizationSlug: "acme-org",
		TeamSlug:         "platform",
		ProjectSlug:      "api",
		HomeRegion:       "us",
		Mode:             SignupModeBlank,
	})
	if err == nil {
		t.Fatal("BuildSignupPlan() error = nil, want bootstrap cell error")
	}
}
