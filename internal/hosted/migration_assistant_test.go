package hosted

import (
	"testing"

	"urgentry/internal/migration"
)

func TestBuildMigrationAssistantPlan(t *testing.T) {
	signup, err := BuildSignupPlan(DefaultTenancyModel(), SignupRequest{
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
	plan, err := BuildMigrationAssistantPlan(signup, MigrationSourceSentryExport, migration.ImportPayload{
		Projects: []migration.ProjectImport{{Slug: "api", Name: "API"}},
		Releases: []migration.ReleaseImport{{Version: "1.2.3"}},
	})
	if err != nil {
		t.Fatalf("BuildMigrationAssistantPlan() error = %v", err)
	}
	if got, want := plan.DryRunPath, "/api/0/organizations/acme-org/import/?dry_run=1"; got != want {
		t.Fatalf("DryRunPath = %q, want %q", got, want)
	}
	if got, want := plan.Coverage.Projects, 1; got != want {
		t.Fatalf("Coverage.Projects = %d, want %d", got, want)
	}
}

func TestBuildMigrationAssistantPlanAddsArtifactAndKeyChecks(t *testing.T) {
	signup, err := BuildSignupPlan(DefaultTenancyModel(), SignupRequest{
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
	plan, err := BuildMigrationAssistantPlan(signup, MigrationSourceJSONUpload, migration.ImportPayload{
		Projects:    []migration.ProjectImport{{Slug: "api", Name: "API"}},
		ProjectKeys: []migration.ProjectKeyImport{{ProjectSlug: "api", PublicKey: "pub"}},
		Artifacts:   []migration.ArtifactImport{{ProjectSlug: "api", Name: "maps.js", Kind: "source_map"}},
	})
	if err != nil {
		t.Fatalf("BuildMigrationAssistantPlan() error = %v", err)
	}
	if !plan.RequiresArtifactVerify {
		t.Fatal("RequiresArtifactVerify = false, want true")
	}
	if !plan.RequiresProjectKeyReview {
		t.Fatal("RequiresProjectKeyReview = false, want true")
	}
	if got, want := plan.Phases[len(plan.Phases)-2], MigrationPhaseRotateKeys; got != want {
		t.Fatalf("second-to-last phase = %q, want %q", got, want)
	}
}

func TestBuildMigrationAssistantPlanRejectsBlankSignupMode(t *testing.T) {
	signup, err := BuildSignupPlan(DefaultTenancyModel(), SignupRequest{
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
	_, err = BuildMigrationAssistantPlan(signup, MigrationSourceSentryExport, migration.ImportPayload{
		Projects: []migration.ProjectImport{{Slug: "api", Name: "API"}},
	})
	if err == nil {
		t.Fatal("BuildMigrationAssistantPlan() error = nil, want import mode error")
	}
}

func TestBuildMigrationAssistantPlanRejectsEmptyPayload(t *testing.T) {
	signup, err := BuildSignupPlan(DefaultTenancyModel(), SignupRequest{
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
	_, err = BuildMigrationAssistantPlan(signup, MigrationSourceSentryExport, migration.ImportPayload{})
	if err == nil {
		t.Fatal("BuildMigrationAssistantPlan() error = nil, want empty payload error")
	}
}
