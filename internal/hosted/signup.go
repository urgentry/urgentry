package hosted

import (
	"fmt"
	"slices"
	"strings"
)

type SignupMode string

const (
	SignupModeBlank  SignupMode = "blank"
	SignupModeImport SignupMode = "import_existing"
)

var signupModeOrder = []SignupMode{
	SignupModeBlank,
	SignupModeImport,
}

type SignupStep string

const (
	SignupStepCreateAccount     SignupStep = "create_account"
	SignupStepCreateOwner       SignupStep = "create_owner"
	SignupStepStartTrial        SignupStep = "start_trial"
	SignupStepCreateOrg         SignupStep = "create_organization"
	SignupStepCreateTeam        SignupStep = "create_team"
	SignupStepCreateProject     SignupStep = "create_project"
	SignupStepMintProjectKey    SignupStep = "mint_project_key"
	SignupStepMintProjectDSN    SignupStep = "mint_project_dsn"
	SignupStepMintOwnerPAT      SignupStep = "mint_owner_pat"
	SignupStepReturnBootstrap   SignupStep = "return_bootstrap_pack"
	SignupStepStartImportAssist SignupStep = "start_import_assistant"
)

type BootstrapCredential string

const (
	BootstrapCredentialOwnerPAT   BootstrapCredential = "owner_pat"
	BootstrapCredentialProjectKey BootstrapCredential = "project_key"
	BootstrapCredentialProjectDSN BootstrapCredential = "project_dsn"
)

type SignupRequest struct {
	AccountSlug      string     `json:"accountSlug"`
	OwnerEmail       string     `json:"ownerEmail"`
	OrganizationSlug string     `json:"organizationSlug"`
	TeamSlug         string     `json:"teamSlug"`
	ProjectSlug      string     `json:"projectSlug"`
	HomeRegion       string     `json:"homeRegion,omitempty"`
	Mode             SignupMode `json:"mode"`
}

type SignupPlan struct {
	Account            Account               `json:"account"`
	Organization       Organization          `json:"organization"`
	FirstTeamSlug      string                `json:"firstTeamSlug"`
	Project            ProjectPlacement      `json:"project"`
	Trial              TrialBehavior         `json:"trial"`
	Mode               SignupMode            `json:"mode"`
	Steps              []SignupStep          `json:"steps"`
	BootstrapOutputs   []BootstrapCredential `json:"bootstrapOutputs"`
	ImportAssistantURL string                `json:"importAssistantUrl,omitempty"`
}

func BuildSignupPlan(model TenancyModel, req SignupRequest) (*SignupPlan, error) {
	if err := model.Validate(); err != nil {
		return nil, err
	}
	if err := validateSignupRequest(req); err != nil {
		return nil, err
	}
	region := req.HomeRegion
	if strings.TrimSpace(region) == "" {
		region = model.DefaultRegion
	}
	cell, ok := firstSignupCell(model, region)
	if !ok {
		return nil, fmt.Errorf("region %q does not have an active bootstrap cell", region)
	}

	account := Account{
		ID:         "acct:" + req.AccountSlug,
		Slug:       req.AccountSlug,
		Plan:       model.Catalog.Trial.Plan,
		Status:     AccountStatusActive,
		HomeRegion: region,
		Residency: ResidencyPolicy{
			Mode: ResidencyModeHomeOnly,
		},
	}
	org := Organization{
		ID:         "org:" + req.OrganizationSlug,
		Slug:       req.OrganizationSlug,
		AccountID:  account.ID,
		Status:     OrganizationStatusActive,
		HomeRegion: region,
		Isolation:  IsolationModeSharedCell,
	}
	project, err := model.PlaceProject(account, org, PlacementRequest{
		ProjectID:      "proj:" + req.ProjectSlug,
		OrganizationID: org.ID,
		Mode:           PlacementModeAccountDefault,
		Cell:           cell.Slug,
	})
	if err != nil {
		return nil, err
	}

	plan := &SignupPlan{
		Account:          account,
		Organization:     org,
		FirstTeamSlug:    req.TeamSlug,
		Project:          project,
		Trial:            model.Catalog.Trial,
		Mode:             req.Mode,
		Steps:            signupSteps(req.Mode),
		BootstrapOutputs: []BootstrapCredential{BootstrapCredentialOwnerPAT, BootstrapCredentialProjectKey, BootstrapCredentialProjectDSN},
	}
	if req.Mode == SignupModeImport {
		plan.ImportAssistantURL = "/onboarding/import/"
	}
	return plan, nil
}

func validateSignupRequest(req SignupRequest) error {
	if strings.TrimSpace(req.AccountSlug) == "" {
		return fmt.Errorf("account slug is required")
	}
	if strings.TrimSpace(req.OwnerEmail) == "" {
		return fmt.Errorf("owner email is required")
	}
	if strings.TrimSpace(req.OrganizationSlug) == "" {
		return fmt.Errorf("organization slug is required")
	}
	if strings.TrimSpace(req.TeamSlug) == "" {
		return fmt.Errorf("team slug is required")
	}
	if strings.TrimSpace(req.ProjectSlug) == "" {
		return fmt.Errorf("project slug is required")
	}
	if !strings.Contains(req.OwnerEmail, "@") {
		return fmt.Errorf("owner email must contain @")
	}
	if !slices.Contains(signupModeOrder, req.Mode) {
		return fmt.Errorf("invalid signup mode %q", req.Mode)
	}
	return nil
}

func firstSignupCell(model TenancyModel, region string) (Cell, bool) {
	for _, item := range model.Regions {
		if item.Slug != region {
			continue
		}
		for _, cell := range item.Cells {
			if cell.State == CellStateActive && cell.AcceptsNewTenants && cell.DefaultIsolation == IsolationModeSharedCell {
				return cell, true
			}
		}
	}
	return Cell{}, false
}

func signupSteps(mode SignupMode) []SignupStep {
	steps := []SignupStep{
		SignupStepCreateAccount,
		SignupStepCreateOwner,
		SignupStepStartTrial,
		SignupStepCreateOrg,
		SignupStepCreateTeam,
		SignupStepCreateProject,
		SignupStepMintProjectKey,
		SignupStepMintProjectDSN,
		SignupStepMintOwnerPAT,
		SignupStepReturnBootstrap,
	}
	if mode == SignupModeImport {
		steps = append(steps, SignupStepStartImportAssist)
	}
	return steps
}
