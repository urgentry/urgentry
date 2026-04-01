package hosted

import "fmt"

type SuspensionReason string

const (
	SuspensionReasonBillingDelinquency   SuspensionReason = "billing_delinquency"
	SuspensionReasonAbuseTraffic         SuspensionReason = "abuse_traffic"
	SuspensionReasonCompromisedAccount   SuspensionReason = "compromised_account"
	SuspensionReasonManualOperatorReview SuspensionReason = "manual_operator_review"
)

type LifecycleAction string

const (
	LifecycleActionSuspendAccount      LifecycleAction = "suspend_account"
	LifecycleActionRestoreAccount      LifecycleAction = "restore_account"
	LifecycleActionTransferOwnership   LifecycleAction = "transfer_ownership"
	LifecycleActionMarkPendingDeletion LifecycleAction = "mark_pending_deletion"
	LifecycleActionCancelDeletion      LifecycleAction = "cancel_pending_deletion"
)

type LifecycleRequest struct {
	AccountID           string             `json:"accountId"`
	OrganizationID      string             `json:"organizationId"`
	Action              LifecycleAction    `json:"action"`
	Reason              SuspensionReason   `json:"reason,omitempty"`
	CurrentAccount      AccountStatus      `json:"currentAccountStatus"`
	TargetAccount       AccountStatus      `json:"targetAccountStatus,omitempty"`
	CurrentOrganization OrganizationStatus `json:"currentOrganizationStatus"`
	TargetOrganization  OrganizationStatus `json:"targetOrganizationStatus,omitempty"`
	ReplacementOwnerID  string             `json:"replacementOwnerId,omitempty"`
}

type LifecyclePolicy struct {
	AccountTransitions      map[AccountStatus][]AccountStatus           `json:"accountTransitions"`
	OrganizationTransitions map[OrganizationStatus][]OrganizationStatus `json:"organizationTransitions"`
	RequiredCapabilities    map[LifecycleAction]SupportCapability       `json:"requiredCapabilities"`
}

func DefaultLifecyclePolicy() LifecyclePolicy {
	return LifecyclePolicy{
		AccountTransitions: map[AccountStatus][]AccountStatus{
			AccountStatusActive:    {AccountStatusReadOnly, AccountStatusSuspended},
			AccountStatusReadOnly:  {AccountStatusActive, AccountStatusSuspended},
			AccountStatusSuspended: {AccountStatusReadOnly, AccountStatusActive},
		},
		OrganizationTransitions: map[OrganizationStatus][]OrganizationStatus{
			OrganizationStatusActive:          {OrganizationStatusSuspended, OrganizationStatusPendingDeletion},
			OrganizationStatusSuspended:       {OrganizationStatusActive, OrganizationStatusPendingDeletion},
			OrganizationStatusPendingDeletion: {OrganizationStatusActive, OrganizationStatusSuspended},
		},
		RequiredCapabilities: map[LifecycleAction]SupportCapability{
			LifecycleActionSuspendAccount:      SupportCapabilitySuspendAccount,
			LifecycleActionRestoreAccount:      SupportCapabilityRestoreAccount,
			LifecycleActionTransferOwnership:   SupportCapabilityTransferOwnership,
			LifecycleActionMarkPendingDeletion: SupportCapabilityWriteTenant,
			LifecycleActionCancelDeletion:      SupportCapabilityWriteTenant,
		},
	}
}

func (p LifecyclePolicy) Validate() error {
	for status, next := range p.AccountTransitions {
		if !validAccountStatus(status) {
			return fmt.Errorf("invalid account status %q", status)
		}
		if len(next) == 0 {
			return fmt.Errorf("account status %q must allow at least one transition", status)
		}
		for _, target := range next {
			if !validAccountStatus(target) {
				return fmt.Errorf("invalid account transition target %q", target)
			}
		}
	}
	for status, next := range p.OrganizationTransitions {
		if !validOrganizationStatus(status) {
			return fmt.Errorf("invalid organization status %q", status)
		}
		if len(next) == 0 {
			return fmt.Errorf("organization status %q must allow at least one transition", status)
		}
		for _, target := range next {
			if !validOrganizationStatus(target) {
				return fmt.Errorf("invalid organization transition target %q", target)
			}
		}
	}
	for action, capability := range p.RequiredCapabilities {
		if !validLifecycleAction(action) {
			return fmt.Errorf("invalid lifecycle action %q", action)
		}
		if !validSupportCapability(capability) {
			return fmt.Errorf("invalid required capability %q for action %q", capability, action)
		}
	}
	return nil
}

func (p LifecyclePolicy) ValidateRequest(req LifecycleRequest) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if req.AccountID == "" {
		return fmt.Errorf("account id is required")
	}
	if req.OrganizationID == "" {
		return fmt.Errorf("organization id is required")
	}
	if !validLifecycleAction(req.Action) {
		return fmt.Errorf("invalid lifecycle action %q", req.Action)
	}
	switch req.Action {
	case LifecycleActionSuspendAccount:
		if !validSuspensionReason(req.Reason) {
			return fmt.Errorf("suspension reason is required for account suspension")
		}
		if !transitionAllowed(p.AccountTransitions, req.CurrentAccount, req.TargetAccount) {
			return fmt.Errorf("account transition %q -> %q is not allowed", req.CurrentAccount, req.TargetAccount)
		}
	case LifecycleActionRestoreAccount:
		if req.CurrentAccount != AccountStatusReadOnly && req.CurrentAccount != AccountStatusSuspended {
			return fmt.Errorf("restore requires a read_only or suspended account")
		}
		if !transitionAllowed(p.AccountTransitions, req.CurrentAccount, req.TargetAccount) {
			return fmt.Errorf("account transition %q -> %q is not allowed", req.CurrentAccount, req.TargetAccount)
		}
	case LifecycleActionTransferOwnership:
		if req.ReplacementOwnerID == "" {
			return fmt.Errorf("replacement owner id is required")
		}
	case LifecycleActionMarkPendingDeletion, LifecycleActionCancelDeletion:
		if !transitionAllowed(p.OrganizationTransitions, req.CurrentOrganization, req.TargetOrganization) {
			return fmt.Errorf("organization transition %q -> %q is not allowed", req.CurrentOrganization, req.TargetOrganization)
		}
	}
	return nil
}

func transitionAllowed[T comparable](allowed map[T][]T, current, target T) bool {
	next, ok := allowed[current]
	if !ok {
		return false
	}
	for _, item := range next {
		if item == target {
			return true
		}
	}
	return false
}

func validLifecycleAction(action LifecycleAction) bool {
	switch action {
	case LifecycleActionSuspendAccount,
		LifecycleActionRestoreAccount,
		LifecycleActionTransferOwnership,
		LifecycleActionMarkPendingDeletion,
		LifecycleActionCancelDeletion:
		return true
	default:
		return false
	}
}

func validSuspensionReason(reason SuspensionReason) bool {
	switch reason {
	case SuspensionReasonBillingDelinquency,
		SuspensionReasonAbuseTraffic,
		SuspensionReasonCompromisedAccount,
		SuspensionReasonManualOperatorReview:
		return true
	default:
		return false
	}
}
