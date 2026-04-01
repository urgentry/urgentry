package hosted

import (
	"fmt"
	"slices"
	"strings"
)

type SupportScope string

const (
	SupportScopeAccount      SupportScope = "account"
	SupportScopeOrganization SupportScope = "organization"
	SupportScopeProject      SupportScope = "project"
)

type SupportCapability string

const (
	SupportCapabilityReadTenant        SupportCapability = "read_tenant"
	SupportCapabilityExportDiagnostics SupportCapability = "export_diagnostics"
	SupportCapabilityRevokeCredentials SupportCapability = "revoke_credentials"
	SupportCapabilityWriteTenant       SupportCapability = "write_tenant"
	SupportCapabilityTransferOwnership SupportCapability = "transfer_ownership"
	SupportCapabilitySuspendAccount    SupportCapability = "suspend_account"
	SupportCapabilityRestoreAccount    SupportCapability = "restore_account"
)

type ApprovalMode string

const (
	ApprovalModeSingle     ApprovalMode = "single"
	ApprovalModeDual       ApprovalMode = "dual"
	ApprovalModeBreakGlass ApprovalMode = "break_glass"
)

type SupportSessionStatus string

const (
	SupportSessionStatusRequested SupportSessionStatus = "requested"
	SupportSessionStatusApproved  SupportSessionStatus = "approved"
	SupportSessionStatusActive    SupportSessionStatus = "active"
	SupportSessionStatusRevoked   SupportSessionStatus = "revoked"
	SupportSessionStatusExpired   SupportSessionStatus = "expired"
)

type SupportPolicy struct {
	Tier                SupportTier                        `json:"tier"`
	AllowedScopes       []SupportScope                     `json:"allowedScopes"`
	MaxSessionMinutes   int                                `json:"maxSessionMinutes"`
	BreakGlassMinutes   int                                `json:"breakGlassMinutes,omitempty"`
	CapabilityApprovals map[SupportCapability]ApprovalMode `json:"capabilityApprovals"`
}

type SupportCatalog struct {
	Policies map[SupportTier]SupportPolicy `json:"policies"`
}

type SupportRequest struct {
	AccountID      string            `json:"accountId"`
	OrganizationID string            `json:"organizationId,omitempty"`
	ProjectID      string            `json:"projectId,omitempty"`
	Scope          SupportScope      `json:"scope"`
	Capability     SupportCapability `json:"capability"`
	DurationMin    int               `json:"durationMinutes"`
	BreakGlass     bool              `json:"breakGlass"`
}

func DefaultSupportCatalog() SupportCatalog {
	return SupportCatalog{
		Policies: map[SupportTier]SupportPolicy{
			SupportTierCommunity: {
				Tier:              SupportTierCommunity,
				AllowedScopes:     []SupportScope{SupportScopeOrganization, SupportScopeProject},
				MaxSessionMinutes: 30,
				CapabilityApprovals: map[SupportCapability]ApprovalMode{
					SupportCapabilityExportDiagnostics: ApprovalModeDual,
				},
			},
			SupportTierStandard: {
				Tier:              SupportTierStandard,
				AllowedScopes:     []SupportScope{SupportScopeOrganization, SupportScopeProject},
				MaxSessionMinutes: 60,
				CapabilityApprovals: map[SupportCapability]ApprovalMode{
					SupportCapabilityReadTenant:        ApprovalModeSingle,
					SupportCapabilityExportDiagnostics: ApprovalModeSingle,
					SupportCapabilityRevokeCredentials: ApprovalModeDual,
				},
			},
			SupportTierPriority: {
				Tier:              SupportTierPriority,
				AllowedScopes:     []SupportScope{SupportScopeAccount, SupportScopeOrganization, SupportScopeProject},
				MaxSessionMinutes: 120,
				CapabilityApprovals: map[SupportCapability]ApprovalMode{
					SupportCapabilityReadTenant:        ApprovalModeSingle,
					SupportCapabilityExportDiagnostics: ApprovalModeSingle,
					SupportCapabilityRevokeCredentials: ApprovalModeDual,
					SupportCapabilityTransferOwnership: ApprovalModeDual,
					SupportCapabilityWriteTenant:       ApprovalModeDual,
					SupportCapabilitySuspendAccount:    ApprovalModeDual,
					SupportCapabilityRestoreAccount:    ApprovalModeDual,
				},
			},
			SupportTierDedicated: {
				Tier:              SupportTierDedicated,
				AllowedScopes:     []SupportScope{SupportScopeAccount, SupportScopeOrganization, SupportScopeProject},
				MaxSessionMinutes: 240,
				BreakGlassMinutes: 15,
				CapabilityApprovals: map[SupportCapability]ApprovalMode{
					SupportCapabilityReadTenant:        ApprovalModeSingle,
					SupportCapabilityExportDiagnostics: ApprovalModeSingle,
					SupportCapabilityRevokeCredentials: ApprovalModeDual,
					SupportCapabilityTransferOwnership: ApprovalModeDual,
					SupportCapabilityWriteTenant:       ApprovalModeDual,
					SupportCapabilitySuspendAccount:    ApprovalModeDual,
					SupportCapabilityRestoreAccount:    ApprovalModeDual,
				},
			},
		},
	}
}

func (c SupportCatalog) Validate() error {
	for _, tier := range []SupportTier{
		SupportTierCommunity,
		SupportTierStandard,
		SupportTierPriority,
		SupportTierDedicated,
	} {
		policy, ok := c.Policies[tier]
		if !ok {
			return fmt.Errorf("missing support policy for tier %q", tier)
		}
		if err := validateSupportPolicy(policy); err != nil {
			return err
		}
	}
	return nil
}

func (c SupportCatalog) PolicyForPlan(plans Catalog, plan Plan) (SupportPolicy, error) {
	if err := plans.Validate(); err != nil {
		return SupportPolicy{}, err
	}
	if err := c.Validate(); err != nil {
		return SupportPolicy{}, err
	}
	spec, ok := plans.Lookup(plan)
	if !ok {
		return SupportPolicy{}, fmt.Errorf("unknown plan %q", plan)
	}
	policy, ok := c.Policies[spec.SupportTier]
	if !ok {
		return SupportPolicy{}, fmt.Errorf("missing support policy for tier %q", spec.SupportTier)
	}
	return policy, nil
}

func (c SupportCatalog) ValidateRequest(plans Catalog, plan Plan, req SupportRequest) error {
	policy, err := c.PolicyForPlan(plans, plan)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.AccountID) == "" {
		return fmt.Errorf("account id is required")
	}
	if !slices.Contains(policy.AllowedScopes, req.Scope) {
		return fmt.Errorf("scope %q is not allowed for tier %q", req.Scope, policy.Tier)
	}
	approval, ok := policy.CapabilityApprovals[req.Capability]
	if !ok {
		return fmt.Errorf("capability %q is not allowed for tier %q", req.Capability, policy.Tier)
	}
	if req.DurationMin <= 0 {
		return fmt.Errorf("duration must be positive")
	}
	switch req.Scope {
	case SupportScopeAccount:
	case SupportScopeOrganization:
		if strings.TrimSpace(req.OrganizationID) == "" {
			return fmt.Errorf("organization id is required for organization scope")
		}
	case SupportScopeProject:
		if strings.TrimSpace(req.OrganizationID) == "" {
			return fmt.Errorf("organization id is required for project scope")
		}
		if strings.TrimSpace(req.ProjectID) == "" {
			return fmt.Errorf("project id is required for project scope")
		}
	default:
		return fmt.Errorf("invalid support scope %q", req.Scope)
	}
	if req.BreakGlass {
		if policy.BreakGlassMinutes == 0 {
			return fmt.Errorf("tier %q does not allow break-glass access", policy.Tier)
		}
		if req.DurationMin > policy.BreakGlassMinutes {
			return fmt.Errorf("break-glass duration exceeds %d minutes", policy.BreakGlassMinutes)
		}
		return nil
	}
	if approval == ApprovalModeBreakGlass {
		return fmt.Errorf("capability %q requires break-glass access", req.Capability)
	}
	if req.DurationMin > policy.MaxSessionMinutes {
		return fmt.Errorf("duration exceeds %d minutes", policy.MaxSessionMinutes)
	}
	return nil
}

func validateSupportPolicy(policy SupportPolicy) error {
	if policy.Tier == "" {
		return fmt.Errorf("support tier is required")
	}
	if policy.MaxSessionMinutes <= 0 {
		return fmt.Errorf("support tier %q must define a positive max session duration", policy.Tier)
	}
	if len(policy.AllowedScopes) == 0 {
		return fmt.Errorf("support tier %q must allow at least one scope", policy.Tier)
	}
	for _, scope := range policy.AllowedScopes {
		if !validSupportScope(scope) {
			return fmt.Errorf("invalid support scope %q for tier %q", scope, policy.Tier)
		}
	}
	if len(policy.CapabilityApprovals) == 0 {
		return fmt.Errorf("support tier %q must define at least one capability", policy.Tier)
	}
	for capability, approval := range policy.CapabilityApprovals {
		if !validSupportCapability(capability) {
			return fmt.Errorf("invalid support capability %q for tier %q", capability, policy.Tier)
		}
		if !validApprovalMode(approval) {
			return fmt.Errorf("invalid approval mode %q for tier %q", approval, policy.Tier)
		}
	}
	if policy.BreakGlassMinutes < 0 {
		return fmt.Errorf("support tier %q cannot define a negative break-glass duration", policy.Tier)
	}
	if policy.BreakGlassMinutes > 0 && policy.BreakGlassMinutes >= policy.MaxSessionMinutes {
		return fmt.Errorf("support tier %q must keep break-glass sessions shorter than normal sessions", policy.Tier)
	}
	return nil
}

func validSupportScope(scope SupportScope) bool {
	switch scope {
	case SupportScopeAccount, SupportScopeOrganization, SupportScopeProject:
		return true
	default:
		return false
	}
}

func validSupportCapability(capability SupportCapability) bool {
	switch capability {
	case SupportCapabilityReadTenant,
		SupportCapabilityExportDiagnostics,
		SupportCapabilityRevokeCredentials,
		SupportCapabilityWriteTenant,
		SupportCapabilityTransferOwnership,
		SupportCapabilitySuspendAccount,
		SupportCapabilityRestoreAccount:
		return true
	default:
		return false
	}
}

func validApprovalMode(mode ApprovalMode) bool {
	switch mode {
	case ApprovalModeSingle, ApprovalModeDual, ApprovalModeBreakGlass:
		return true
	default:
		return false
	}
}
