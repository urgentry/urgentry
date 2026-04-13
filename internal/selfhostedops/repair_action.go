package selfhostedops

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"urgentry/internal/store"
)

type RepairActionRequest struct {
	OrganizationID string
	ProjectID      string
	Surface        RepairSurface
	Action         RepairAction
	Target         string
	Reason         string
	Actor          string
	Source         string
	Confirm        bool
}

type RepairActionReceipt struct {
	Surface    RepairSurface          `json:"surface"`
	Action     RepairAction           `json:"action"`
	Target     string                 `json:"target"`
	Safeguards []string               `json:"safeguards"`
	Signals    []string               `json:"signals"`
	Audit      *OperatorActionReceipt `json:"audit"`
}

func ExecuteRepairAction(ctx context.Context, controlDSN string, req RepairActionRequest) (*RepairActionReceipt, error) {
	req.OrganizationID = strings.TrimSpace(req.OrganizationID)
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.Target = strings.TrimSpace(req.Target)
	req.Reason = strings.TrimSpace(req.Reason)
	req.Actor = strings.TrimSpace(req.Actor)
	req.Source = strings.TrimSpace(req.Source)
	if !req.Confirm {
		return nil, fmt.Errorf("repair action requires explicit confirmation")
	}
	if req.Surface == "" {
		return nil, fmt.Errorf("repair surface is required")
	}
	if req.Action == "" {
		return nil, fmt.Errorf("repair action is required")
	}
	if req.Target == "" {
		return nil, fmt.Errorf("repair target is required")
	}
	if req.Reason == "" {
		return nil, fmt.Errorf("repair reason is required")
	}
	surface, ok := DefaultRepairPack().lookupSurface(req.Surface)
	if !ok {
		return nil, fmt.Errorf("unknown repair surface %q", req.Surface)
	}
	if !slices.Contains(surface.Actions, req.Action) {
		return nil, fmt.Errorf("repair action %q is not valid for surface %q", req.Action, req.Surface)
	}
	metadata, err := json.Marshal(map[string]any{
		"surface":    req.Surface,
		"action":     req.Action,
		"target":     req.Target,
		"reason":     req.Reason,
		"signals":    surface.Signals,
		"safeguards": surface.Safeguards,
	})
	if err != nil {
		return nil, err
	}
	audit, err := RecordOperatorAction(ctx, controlDSN, store.OperatorAuditRecord{
		OrganizationID: req.OrganizationID,
		ProjectID:      req.ProjectID,
		Action:         fmt.Sprintf("repair.%s.%s", req.Surface, req.Action),
		Status:         "requested",
		Source:         firstNonEmptyLocal(req.Source, "cli"),
		Actor:          firstNonEmptyLocal(req.Actor, "system"),
		Detail:         req.Reason,
		MetadataJSON:   string(metadata),
	})
	if err != nil {
		return nil, err
	}
	return &RepairActionReceipt{
		Surface:    req.Surface,
		Action:     req.Action,
		Target:     req.Target,
		Safeguards: append([]string(nil), surface.Safeguards...),
		Signals:    append([]string(nil), surface.Signals...),
		Audit:      audit,
	}, nil
}

type PITRActionRequest struct {
	OrganizationID string
	ProjectID      string
	Surface        PostgresRecoverySurface
	TargetType     string
	Target         string
	Reason         string
	Actor          string
	Source         string
	Confirm        bool
}

type PITRActionReceipt struct {
	Surface    PostgresRecoverySurface `json:"surface"`
	TargetType string                  `json:"targetType"`
	Target     string                  `json:"target"`
	Workflow   []string                `json:"workflow"`
	Boundaries []string                `json:"boundaries"`
	Audit      *OperatorActionReceipt  `json:"audit"`
}

func ExecutePITRAction(ctx context.Context, controlDSN string, req PITRActionRequest) (*PITRActionReceipt, error) {
	req.OrganizationID = strings.TrimSpace(req.OrganizationID)
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.TargetType = strings.TrimSpace(req.TargetType)
	req.Target = strings.TrimSpace(req.Target)
	req.Reason = strings.TrimSpace(req.Reason)
	req.Actor = strings.TrimSpace(req.Actor)
	req.Source = strings.TrimSpace(req.Source)
	if !req.Confirm {
		return nil, fmt.Errorf("pitr action requires explicit confirmation")
	}
	if req.Surface == "" {
		return nil, fmt.Errorf("pitr surface is required")
	}
	if req.TargetType == "" {
		return nil, fmt.Errorf("pitr target type is required")
	}
	if req.Target == "" {
		return nil, fmt.Errorf("pitr target is required")
	}
	if req.Reason == "" {
		return nil, fmt.Errorf("pitr reason is required")
	}
	contract := DefaultPITRContract()
	var requirement *PITRRequirement
	for idx := range contract.Requirements {
		if contract.Requirements[idx].Surface == req.Surface {
			requirement = &contract.Requirements[idx]
			break
		}
	}
	if requirement == nil {
		return nil, fmt.Errorf("unknown pitr surface %q", req.Surface)
	}
	if !slices.Contains(requirement.RecoveryTargetTypes, req.TargetType) {
		return nil, fmt.Errorf("pitr target type %q is not valid for surface %q", req.TargetType, req.Surface)
	}
	metadata, err := json.Marshal(map[string]any{
		"surface":      req.Surface,
		"targetType":   req.TargetType,
		"target":       req.Target,
		"reason":       req.Reason,
		"requirements": requirement,
		"workflow":     contract.Workflow,
		"boundaries":   contract.Boundaries,
	})
	if err != nil {
		return nil, err
	}
	audit, err := RecordOperatorAction(ctx, controlDSN, store.OperatorAuditRecord{
		OrganizationID: req.OrganizationID,
		ProjectID:      req.ProjectID,
		Action:         fmt.Sprintf("pitr.%s.recovery_requested", req.Surface),
		Status:         "requested",
		Source:         firstNonEmptyLocal(req.Source, "cli"),
		Actor:          firstNonEmptyLocal(req.Actor, "system"),
		Detail:         req.Reason,
		MetadataJSON:   string(metadata),
	})
	if err != nil {
		return nil, err
	}
	return &PITRActionReceipt{
		Surface:    req.Surface,
		TargetType: req.TargetType,
		Target:     req.Target,
		Workflow:   append([]string(nil), contract.Workflow...),
		Boundaries: append([]string(nil), contract.Boundaries...),
		Audit:      audit,
	}, nil
}

func firstNonEmptyLocal(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
