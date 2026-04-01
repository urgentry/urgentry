package hosted

import (
	"fmt"
	"slices"
)

type DrillID string

const (
	DrillLockedOutOwnerRecovery    DrillID = "locked_out_owner_password_recovery"
	DrillPATAndSessionRevocation   DrillID = "lost_owner_pat_and_session_revocation"
	DrillOwnershipRecovery         DrillID = "orphaned_organization_ownership_recovery"
	DrillBillingRestore            DrillID = "billing_suspension_and_restore"
	DrillTemporarySupportAccess    DrillID = "temporary_support_access_session"
	DrillSupportAccessRevocation   DrillID = "support_session_revocation"
	DrillRedactedDiagnosticsExport DrillID = "redacted_diagnostics_export"
)

var drillOrder = []DrillID{
	DrillLockedOutOwnerRecovery,
	DrillPATAndSessionRevocation,
	DrillOwnershipRecovery,
	DrillBillingRestore,
	DrillTemporarySupportAccess,
	DrillSupportAccessRevocation,
	DrillRedactedDiagnosticsExport,
}

type ActorRole string

const (
	ActorCustomerOwner   ActorRole = "customer_owner"
	ActorSupportOperator ActorRole = "support_operator"
	ActorApprover        ActorRole = "approver"
	ActorAuditReviewer   ActorRole = "audit_reviewer"
)

var actorOrder = []ActorRole{
	ActorCustomerOwner,
	ActorSupportOperator,
	ActorApprover,
	ActorAuditReviewer,
}

type EvidenceField string

const (
	EvidenceDrillID         EvidenceField = "drill_id"
	EvidenceTicketID        EvidenceField = "ticket_or_incident_id"
	EvidenceAccountID       EvidenceField = "account_id"
	EvidenceOrganizationIDs EvidenceField = "organization_ids"
	EvidenceOperatorID      EvidenceField = "operator_id"
	EvidenceApproverID      EvidenceField = "approver_id"
	EvidenceWindow          EvidenceField = "start_and_end_timestamps"
	EvidenceReason          EvidenceField = "reason_code"
	EvidenceScopes          EvidenceField = "touched_scopes"
	EvidenceAuditRows       EvidenceField = "audit_log_rows"
	EvidenceRedactionProof  EvidenceField = "redaction_proof"
	EvidenceOutcome         EvidenceField = "customer_visible_outcome"
)

var evidenceOrder = []EvidenceField{
	EvidenceDrillID,
	EvidenceTicketID,
	EvidenceAccountID,
	EvidenceOrganizationIDs,
	EvidenceOperatorID,
	EvidenceApproverID,
	EvidenceWindow,
	EvidenceReason,
	EvidenceScopes,
	EvidenceAuditRows,
	EvidenceRedactionProof,
	EvidenceOutcome,
}

type DrillStep struct {
	Name   string    `json:"name"`
	Actor  ActorRole `json:"actor"`
	Action string    `json:"action"`
	Expect string    `json:"expect"`
}

type DrillSpec struct {
	ID             DrillID         `json:"id"`
	Title          string          `json:"title"`
	StartState     []string        `json:"startState"`
	RequiredActors []ActorRole     `json:"requiredActors"`
	Evidence       []EvidenceField `json:"evidence"`
	Steps          []DrillStep     `json:"steps"`
	PassCriteria   []string        `json:"passCriteria"`
}

func DefaultSupportDrills() []DrillSpec {
	evidence := append([]EvidenceField(nil), evidenceOrder...)
	actors := append([]ActorRole(nil), actorOrder...)
	return []DrillSpec{
		{
			ID:             DrillLockedOutOwnerRecovery,
			Title:          "Locked-out owner password recovery",
			StartState:     []string{"owner cannot sign in", "account is still active"},
			RequiredActors: append([]ActorRole(nil), actors...),
			Evidence:       append([]EvidenceField(nil), evidence...),
			Steps: []DrillStep{
				{"verify ownership", ActorSupportOperator, "verify the recovery request against the hosted recovery policy", "support has enough proof to proceed"},
				{"approve recovery", ActorApprover, "approve the reset or delegated recovery session", "approval is recorded before credentials change"},
				{"restore access", ActorSupportOperator, "issue the reset or recovery session", "the owner can rotate credentials"},
				{"audit review", ActorAuditReviewer, "review the audit rows and final account state", "the drill record is complete"},
			},
			PassCriteria: []string{
				"the owner signs in again",
				"stale sessions do not survive when policy requires revocation",
				"no other account or org state changes",
			},
		},
		{
			ID:             DrillPATAndSessionRevocation,
			Title:          "Lost owner PAT and session revocation",
			StartState:     []string{"owner believes a PAT or browser session leaked"},
			RequiredActors: append([]ActorRole(nil), actors...),
			Evidence:       append([]EvidenceField(nil), evidence...),
			Steps: []DrillStep{
				{"request emergency revoke", ActorCustomerOwner, "request revocation of PATs or sessions in scope", "support receives a bounded revoke request"},
				{"revoke credentials", ActorSupportOperator, "revoke the targeted PATs or all sessions in scope", "revoked credentials stop working"},
				{"approve exceptional scope", ActorApprover, "approve any org-wide revoke outside the default policy", "the wider revoke has a clear approval trail"},
				{"review ledger", ActorAuditReviewer, "check audit rows and replacement credential creation", "history remains intact"},
			},
			PassCriteria: []string{
				"revoked credentials stop working",
				"replacement credentials work",
				"the ledger shows the change without deleting history",
			},
		},
		{
			ID:             DrillOwnershipRecovery,
			Title:          "Orphaned organization ownership recovery",
			StartState:     []string{"the last active organization owner is gone or unreachable"},
			RequiredActors: append([]ActorRole(nil), actors...),
			Evidence:       append([]EvidenceField(nil), evidence...),
			Steps: []DrillStep{
				{"verify transfer request", ActorSupportOperator, "validate the recovery request and replacement owner identity", "support knows the transfer is eligible"},
				{"approve transfer", ActorApprover, "approve the ownership transfer", "support has explicit approval before mutation"},
				{"grant replacement owner", ActorSupportOperator, "grant owner access to the approved replacement user", "the org regains a working owner"},
				{"review outcome", ActorAuditReviewer, "confirm the old owner record survives in audit history", "the org never reaches an ownerless steady state"},
			},
			PassCriteria: []string{
				"one valid owner exists at the end",
				"the org never reaches an ownerless steady state",
				"the approval artifact and audit rows match",
			},
		},
		{
			ID:             DrillBillingRestore,
			Title:          "Billing suspension and restore",
			StartState:     []string{"the account moved from active to read_only or suspended"},
			RequiredActors: append([]ActorRole(nil), actors...),
			Evidence:       append([]EvidenceField(nil), evidence...),
			Steps: []DrillStep{
				{"inspect billing state", ActorSupportOperator, "inspect the billing and entitlement state for the account", "support sees the reason for the current mode"},
				{"approve restore", ActorApprover, "approve the restore to the intended billing mode", "the state change is authorized"},
				{"restore account mode", ActorSupportOperator, "reopen the account to the approved mode", "allowed write surfaces reopen and blocked surfaces stay blocked"},
				{"review state transition", ActorAuditReviewer, "review the old and new billing mode in audit history", "the state transition is reconstructable later"},
			},
			PassCriteria: []string{
				"the account ends in the approved mode",
				"blocked surfaces behave exactly as the billing state requires",
				"no plan limits silently reset",
			},
		},
		{
			ID:             DrillTemporarySupportAccess,
			Title:          "Temporary support access session",
			StartState:     []string{"support needs scoped access to inspect a tenant problem"},
			RequiredActors: append([]ActorRole(nil), actors...),
			Evidence:       append([]EvidenceField(nil), evidence...),
			Steps: []DrillStep{
				{"request scoped access", ActorSupportOperator, "request support access with a reason and expiry", "the request is bounded"},
				{"approve access", ActorApprover, "approve the support session scope and expiry", "support receives only the approved policy"},
				{"use support scope", ActorSupportOperator, "enter the tenant scope and perform the minimum action", "the action stays within scope"},
				{"review access trail", ActorAuditReviewer, "review request, approval, use, and expiry events", "the support session is fully reconstructable"},
			},
			PassCriteria: []string{
				"support access cannot exceed the approved org, project, or action scope",
				"expiry works without manual cleanup",
				"audit rows show request, approval, use, and revoke events",
			},
		},
		{
			ID:             DrillSupportAccessRevocation,
			Title:          "Support session revocation during live incident",
			StartState:     []string{"an active support session exists and must stop now"},
			RequiredActors: append([]ActorRole(nil), actors...),
			Evidence:       append([]EvidenceField(nil), evidence...),
			Steps: []DrillStep{
				{"request revoke", ActorSupportOperator, "request immediate revoke of the active support session", "the revoke request is recorded"},
				{"approve revoke", ActorApprover, "approve or acknowledge the emergency revoke", "the revoke has a recorded authority"},
				{"revoke session", ActorSupportOperator, "revoke the support session", "support access fails closed on the next request"},
				{"review tenant health", ActorAuditReviewer, "confirm customer traffic stayed healthy during the revoke", "the revoke did not disrupt normal tenant traffic"},
			},
			PassCriteria: []string{
				"revoked support access fails closed",
				"customer traffic keeps working",
				"the revoke event is visible in audit history",
			},
		},
		{
			ID:             DrillRedactedDiagnosticsExport,
			Title:          "Redacted diagnostics export",
			StartState:     []string{"support needs evidence from a tenant without exposing raw secrets"},
			RequiredActors: append([]ActorRole(nil), actors...),
			Evidence:       append([]EvidenceField(nil), evidence...),
			Steps: []DrillStep{
				{"request export", ActorSupportOperator, "request a diagnostics export for a bounded scope", "the requested scope is recorded"},
				{"approve export", ActorApprover, "approve the export when policy requires it", "sensitive export creation is gated"},
				{"review redaction", ActorSupportOperator, "check that tokens, secrets, and blocked payload fields are redacted", "the bundle is safe to share internally"},
				{"audit review", ActorAuditReviewer, "confirm who generated and accessed the export", "the evidence bundle is complete"},
			},
			PassCriteria: []string{
				"secrets, tokens, raw credentials, and blocked payload fields do not leak",
				"the export still includes enough state to debug the incident",
				"audit rows show who generated and accessed the bundle",
			},
		},
	}
}

func ValidateSupportDrills(drills []DrillSpec) error {
	if len(drills) != len(drillOrder) {
		return fmt.Errorf("expected %d drills, got %d", len(drillOrder), len(drills))
	}
	for i, id := range drillOrder {
		drill := drills[i]
		if drill.ID != id {
			return fmt.Errorf("drill %d id = %q, want %q", i, drill.ID, id)
		}
		if drill.Title == "" {
			return fmt.Errorf("%s title is required", drill.ID)
		}
		if len(drill.StartState) == 0 {
			return fmt.Errorf("%s start state is required", drill.ID)
		}
		if len(drill.Steps) < 3 {
			return fmt.Errorf("%s needs at least three steps", drill.ID)
		}
		if len(drill.PassCriteria) == 0 {
			return fmt.Errorf("%s pass criteria are required", drill.ID)
		}
		for _, actor := range actorOrder {
			if !slices.Contains(drill.RequiredActors, actor) {
				return fmt.Errorf("%s missing actor %q", drill.ID, actor)
			}
		}
		for _, field := range evidenceOrder {
			if !slices.Contains(drill.Evidence, field) {
				return fmt.Errorf("%s missing evidence field %q", drill.ID, field)
			}
		}
	}
	return nil
}
