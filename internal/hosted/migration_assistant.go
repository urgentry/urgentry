package hosted

import (
	"fmt"
	"net/url"

	"urgentry/internal/migration"
)

type MigrationSourceKind string

const (
	MigrationSourceSentryExport MigrationSourceKind = "sentry_export"
	MigrationSourceUrgentryExport MigrationSourceKind = "urgentry_export"
	MigrationSourceJSONUpload   MigrationSourceKind = "json_upload"
)

var migrationSourceOrder = []MigrationSourceKind{
	MigrationSourceSentryExport,
	MigrationSourceUrgentryExport,
	MigrationSourceJSONUpload,
}

type MigrationPhase string

const (
	MigrationPhaseUploadPayload   MigrationPhase = "upload_payload"
	MigrationPhaseInspectPayload  MigrationPhase = "inspect_payload"
	MigrationPhaseMapProjects     MigrationPhase = "map_projects"
	MigrationPhaseDryRunImport    MigrationPhase = "dry_run_import"
	MigrationPhaseApplyImport     MigrationPhase = "apply_import"
	MigrationPhaseVerifyArtifacts MigrationPhase = "verify_artifacts"
	MigrationPhaseRotateKeys      MigrationPhase = "rotate_project_keys"
	MigrationPhaseFinishCutover   MigrationPhase = "finish_cutover"
)

type MigrationCoverage struct {
	Projects    int `json:"projects"`
	Releases    int `json:"releases"`
	Issues      int `json:"issues"`
	Events      int `json:"events"`
	ProjectKeys int `json:"projectKeys"`
	AlertRules  int `json:"alertRules"`
	Members     int `json:"members"`
	Artifacts   int `json:"artifacts"`
}

type MigrationAssistantPlan struct {
	AccountSlug              string              `json:"accountSlug"`
	OrganizationSlug         string              `json:"organizationSlug"`
	SourceKind               MigrationSourceKind `json:"sourceKind"`
	Coverage                 MigrationCoverage   `json:"coverage"`
	Phases                   []MigrationPhase    `json:"phases"`
	DryRunPath               string              `json:"dryRunPath"`
	ApplyPath                string              `json:"applyPath"`
	ExportVerificationPath   string              `json:"exportVerificationPath"`
	RequiresArtifactVerify   bool                `json:"requiresArtifactVerify"`
	RequiresProjectKeyReview bool                `json:"requiresProjectKeyReview"`
}

func BuildMigrationAssistantPlan(signup *SignupPlan, source MigrationSourceKind, payload migration.ImportPayload) (*MigrationAssistantPlan, error) {
	if signup == nil {
		return nil, fmt.Errorf("signup plan is required")
	}
	if signup.Mode != SignupModeImport {
		return nil, fmt.Errorf("signup plan must use import_existing mode")
	}
	if signup.Organization.Slug == "" {
		return nil, fmt.Errorf("signup organization slug is required")
	}
	if !containsMigrationSource(source) {
		return nil, fmt.Errorf("unknown migration source %q", source)
	}

	coverage := MigrationCoverage{
		Projects:    len(payload.Projects),
		Releases:    len(payload.Releases),
		Issues:      len(payload.Issues),
		Events:      len(payload.Events),
		ProjectKeys: len(payload.ProjectKeys),
		AlertRules:  len(payload.AlertRules),
		Members:     len(payload.Members),
		Artifacts:   len(payload.Artifacts),
	}
	if coverage.Projects == 0 {
		return nil, fmt.Errorf("migration assistant requires at least one project")
	}

	orgSlug := signup.Organization.Slug
	dryRunPath := "/api/0/organizations/" + url.PathEscape(orgSlug) + "/import/?dry_run=1"
	applyPath := "/api/0/organizations/" + url.PathEscape(orgSlug) + "/import/"
	plan := &MigrationAssistantPlan{
		AccountSlug:              signup.Account.Slug,
		OrganizationSlug:         orgSlug,
		SourceKind:               source,
		Coverage:                 coverage,
		Phases:                   []MigrationPhase{MigrationPhaseUploadPayload, MigrationPhaseInspectPayload, MigrationPhaseMapProjects, MigrationPhaseDryRunImport, MigrationPhaseApplyImport, MigrationPhaseFinishCutover},
		DryRunPath:               dryRunPath,
		ApplyPath:                applyPath,
		ExportVerificationPath:   "/api/0/organizations/" + url.PathEscape(orgSlug) + "/export/",
		RequiresArtifactVerify:   coverage.Artifacts > 0,
		RequiresProjectKeyReview: coverage.ProjectKeys > 0,
	}
	if plan.RequiresArtifactVerify {
		plan.Phases = insertMigrationPhase(plan.Phases, MigrationPhaseVerifyArtifacts, MigrationPhaseFinishCutover)
	}
	if plan.RequiresProjectKeyReview {
		plan.Phases = insertMigrationPhase(plan.Phases, MigrationPhaseRotateKeys, MigrationPhaseFinishCutover)
	}
	return plan, nil
}

func containsMigrationSource(source MigrationSourceKind) bool {
	for _, item := range migrationSourceOrder {
		if item == source {
			return true
		}
	}
	return false
}

func insertMigrationPhase(phases []MigrationPhase, phase, before MigrationPhase) []MigrationPhase {
	for i, item := range phases {
		if item == before {
			out := make([]MigrationPhase, 0, len(phases)+1)
			out = append(out, phases[:i]...)
			out = append(out, phase)
			out = append(out, phases[i:]...)
			return out
		}
	}
	return append(phases, phase)
}
