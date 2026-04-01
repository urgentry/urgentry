package hosted

import (
	"fmt"
	"slices"
	"strings"
)

type PlacementMode string

const (
	PlacementModeAccountDefault      PlacementMode = "account_default"
	PlacementModeOrganizationDefault PlacementMode = "organization_default"
	PlacementModePinned              PlacementMode = "pinned"
)

var placementModeOrder = []PlacementMode{
	PlacementModeAccountDefault,
	PlacementModeOrganizationDefault,
	PlacementModePinned,
}

type PlacementRequest struct {
	ProjectID      string        `json:"projectId"`
	OrganizationID string        `json:"organizationId"`
	Mode           PlacementMode `json:"mode"`
	Region         string        `json:"region,omitempty"`
	Cell           string        `json:"cell,omitempty"`
}

func (m TenancyModel) PlaceProject(account Account, organization Organization, req PlacementRequest) (ProjectPlacement, error) {
	if err := m.ValidateAccount(account); err != nil {
		return ProjectPlacement{}, err
	}
	if strings.TrimSpace(organization.ID) == "" {
		return ProjectPlacement{}, fmt.Errorf("organization id is required")
	}
	if organization.AccountID != account.ID {
		return ProjectPlacement{}, fmt.Errorf("organization account %q does not match account %q", organization.AccountID, account.ID)
	}
	if organization.HomeRegion != account.HomeRegion {
		return ProjectPlacement{}, fmt.Errorf("organization home region %q must match account home region %q", organization.HomeRegion, account.HomeRegion)
	}
	if !validOrganizationStatus(organization.Status) {
		return ProjectPlacement{}, fmt.Errorf("invalid organization status %q", organization.Status)
	}
	if !validIsolationMode(organization.Isolation) {
		return ProjectPlacement{}, fmt.Errorf("invalid organization isolation %q", organization.Isolation)
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return ProjectPlacement{}, fmt.Errorf("project id is required")
	}
	if req.OrganizationID != organization.ID {
		return ProjectPlacement{}, fmt.Errorf("project organization %q does not match organization %q", req.OrganizationID, organization.ID)
	}
	if !slices.Contains(placementModeOrder, req.Mode) {
		return ProjectPlacement{}, fmt.Errorf("invalid placement mode %q", req.Mode)
	}
	region := placementRegion(account, organization, req)
	if !m.hasRegion(region) {
		return ProjectPlacement{}, fmt.Errorf("unknown project region %q", region)
	}
	if !regionAllowed(account, region) {
		return ProjectPlacement{}, fmt.Errorf("region %q is not allowed for account %q", region, account.ID)
	}
	cell, ok := m.pickPlacementCell(region, organization.Isolation, req.Cell)
	if !ok {
		return ProjectPlacement{}, fmt.Errorf("region %q does not have an active tenant cell for isolation %q", region, organization.Isolation)
	}
	placement := ProjectPlacement{
		ProjectID:      req.ProjectID,
		OrganizationID: organization.ID,
		Region:         region,
		Cell:           cell.Slug,
		Isolation:      placementIsolation(organization.Isolation, cell.DefaultIsolation),
	}
	if err := m.ValidateAssignment(account, organization, placement); err != nil {
		return ProjectPlacement{}, err
	}
	return placement, nil
}

func placementRegion(account Account, organization Organization, req PlacementRequest) string {
	switch req.Mode {
	case PlacementModeOrganizationDefault:
		return organization.HomeRegion
	case PlacementModePinned:
		return strings.TrimSpace(req.Region)
	default:
		return account.HomeRegion
	}
}

func (m TenancyModel) pickPlacementCell(region string, isolation IsolationMode, requestedCell string) (Cell, bool) {
	if requestedCell != "" {
		cell, ok := m.lookupCell(region, requestedCell)
		if !ok || cell.State != CellStateActive || !cell.AcceptsNewTenants {
			return Cell{}, false
		}
		if isolation == IsolationModeDedicatedCell && cell.DefaultIsolation != IsolationModeDedicatedCell {
			return Cell{}, false
		}
		return cell, true
	}
	for _, item := range m.Regions {
		if item.Slug != region {
			continue
		}
		for _, cell := range item.Cells {
			if cell.State != CellStateActive || !cell.AcceptsNewTenants {
				continue
			}
			if isolation == IsolationModeDedicatedCell && cell.DefaultIsolation != IsolationModeDedicatedCell {
				continue
			}
			return cell, true
		}
	}
	return Cell{}, false
}

func placementIsolation(orgIsolation, cellIsolation IsolationMode) IsolationMode {
	if orgIsolation == IsolationModeDedicatedCell || cellIsolation == IsolationModeDedicatedCell {
		return IsolationModeDedicatedCell
	}
	return IsolationModeSharedCell
}
