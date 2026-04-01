package hosted

import (
	"fmt"
	"slices"
	"strings"
)

type AccountStatus string

const (
	AccountStatusActive    AccountStatus = "active"
	AccountStatusReadOnly  AccountStatus = "read_only"
	AccountStatusSuspended AccountStatus = "suspended"
)

type OrganizationStatus string

const (
	OrganizationStatusActive          OrganizationStatus = "active"
	OrganizationStatusSuspended       OrganizationStatus = "suspended"
	OrganizationStatusPendingDeletion OrganizationStatus = "pending_deletion"
)

type IsolationMode string

const (
	IsolationModeSharedCell    IsolationMode = "shared_cell"
	IsolationModeDedicatedCell IsolationMode = "dedicated_cell"
)

type ResidencyMode string

const (
	ResidencyModeHomeOnly ResidencyMode = "home_only"
	ResidencyModePinned   ResidencyMode = "pinned"
)

type CellState string

const (
	CellStateActive   CellState = "active"
	CellStateDraining CellState = "draining"
	CellStateDisabled CellState = "disabled"
)

type ResidencyPolicy struct {
	Mode           ResidencyMode `json:"mode"`
	AllowedRegions []string      `json:"allowedRegions,omitempty"`
}

type Region struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"displayName"`
	Cells       []Cell `json:"cells"`
}

type Cell struct {
	Slug              string        `json:"slug"`
	Region            string        `json:"region"`
	State             CellState     `json:"state"`
	AcceptsNewTenants bool          `json:"acceptsNewTenants"`
	DefaultIsolation  IsolationMode `json:"defaultIsolation"`
}

type Account struct {
	ID         string          `json:"id"`
	Slug       string          `json:"slug"`
	Plan       Plan            `json:"plan"`
	Status     AccountStatus   `json:"status"`
	HomeRegion string          `json:"homeRegion"`
	Residency  ResidencyPolicy `json:"residency"`
}

type Organization struct {
	ID         string             `json:"id"`
	Slug       string             `json:"slug"`
	AccountID  string             `json:"accountId"`
	Status     OrganizationStatus `json:"status"`
	HomeRegion string             `json:"homeRegion"`
	Isolation  IsolationMode      `json:"isolation"`
}

type ProjectPlacement struct {
	ProjectID      string        `json:"projectId"`
	OrganizationID string        `json:"organizationId"`
	Region         string        `json:"region"`
	Cell           string        `json:"cell"`
	Isolation      IsolationMode `json:"isolation"`
}

type TenancyModel struct {
	Catalog       Catalog  `json:"catalog"`
	DefaultRegion string   `json:"defaultRegion"`
	Regions       []Region `json:"regions"`
}

func DefaultTenancyModel() TenancyModel {
	return TenancyModel{
		Catalog:       DefaultCatalog(),
		DefaultRegion: "us",
		Regions: []Region{
			{
				Slug:        "us",
				DisplayName: "United States",
				Cells: []Cell{
					{Slug: "us-a", Region: "us", State: CellStateActive, AcceptsNewTenants: true, DefaultIsolation: IsolationModeSharedCell},
					{Slug: "us-b", Region: "us", State: CellStateActive, AcceptsNewTenants: true, DefaultIsolation: IsolationModeSharedCell},
					{Slug: "us-d1", Region: "us", State: CellStateActive, AcceptsNewTenants: true, DefaultIsolation: IsolationModeDedicatedCell},
				},
			},
			{
				Slug:        "eu",
				DisplayName: "Europe",
				Cells: []Cell{
					{Slug: "eu-a", Region: "eu", State: CellStateActive, AcceptsNewTenants: true, DefaultIsolation: IsolationModeSharedCell},
					{Slug: "eu-d1", Region: "eu", State: CellStateActive, AcceptsNewTenants: true, DefaultIsolation: IsolationModeDedicatedCell},
				},
			},
		},
	}
}

func (m TenancyModel) Validate() error {
	if err := m.Catalog.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(m.DefaultRegion) == "" {
		return fmt.Errorf("default region is required")
	}
	seenRegions := make(map[string]struct{}, len(m.Regions))
	hasDefault := false
	for _, region := range m.Regions {
		slug := strings.TrimSpace(region.Slug)
		if slug == "" {
			return fmt.Errorf("region slug is required")
		}
		if _, ok := seenRegions[slug]; ok {
			return fmt.Errorf("duplicate region %q", slug)
		}
		seenRegions[slug] = struct{}{}
		if slug == m.DefaultRegion {
			hasDefault = true
		}
		if len(region.Cells) == 0 {
			return fmt.Errorf("region %q must define at least one cell", slug)
		}
		seenCells := make(map[string]struct{}, len(region.Cells))
		accepting := false
		for _, cell := range region.Cells {
			if err := validateCell(slug, cell); err != nil {
				return err
			}
			if _, ok := seenCells[cell.Slug]; ok {
				return fmt.Errorf("duplicate cell %q in region %q", cell.Slug, slug)
			}
			seenCells[cell.Slug] = struct{}{}
			if cell.AcceptsNewTenants && cell.State == CellStateActive {
				accepting = true
			}
		}
		if !accepting {
			return fmt.Errorf("region %q must expose at least one active cell for new tenants", slug)
		}
	}
	if !hasDefault {
		return fmt.Errorf("default region %q is not defined", m.DefaultRegion)
	}
	return nil
}

func (m TenancyModel) ValidateAccount(account Account) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(account.ID) == "" {
		return fmt.Errorf("account id is required")
	}
	if strings.TrimSpace(account.Slug) == "" {
		return fmt.Errorf("account slug is required")
	}
	spec, ok := m.Catalog.Lookup(account.Plan)
	if !ok {
		return fmt.Errorf("unknown plan %q", account.Plan)
	}
	if !validAccountStatus(account.Status) {
		return fmt.Errorf("invalid account status %q", account.Status)
	}
	if !m.hasRegion(account.HomeRegion) {
		return fmt.Errorf("unknown home region %q", account.HomeRegion)
	}
	if err := validateResidency(account.Residency); err != nil {
		return err
	}
	switch account.Residency.Mode {
	case ResidencyModeHomeOnly:
		if len(account.Residency.AllowedRegions) != 0 {
			return fmt.Errorf("home_only residency does not accept allowed regions")
		}
	case ResidencyModePinned:
		if !spec.Features[FeatureRegionPinning] {
			return fmt.Errorf("plan %q does not allow region pinning", account.Plan)
		}
		if len(account.Residency.AllowedRegions) == 0 {
			return fmt.Errorf("pinned residency requires at least one allowed region")
		}
		if !slices.Contains(account.Residency.AllowedRegions, account.HomeRegion) {
			return fmt.Errorf("pinned residency must include the home region")
		}
		for _, region := range account.Residency.AllowedRegions {
			if !m.hasRegion(region) {
				return fmt.Errorf("unknown pinned region %q", region)
			}
		}
	default:
		return fmt.Errorf("invalid residency mode %q", account.Residency.Mode)
	}
	return nil
}

func (m TenancyModel) ValidateAssignment(account Account, organization Organization, project ProjectPlacement) error {
	if err := m.ValidateAccount(account); err != nil {
		return err
	}
	if strings.TrimSpace(organization.ID) == "" {
		return fmt.Errorf("organization id is required")
	}
	if organization.AccountID != account.ID {
		return fmt.Errorf("organization account %q does not match account %q", organization.AccountID, account.ID)
	}
	if !validOrganizationStatus(organization.Status) {
		return fmt.Errorf("invalid organization status %q", organization.Status)
	}
	if organization.HomeRegion != account.HomeRegion {
		return fmt.Errorf("organization home region %q must match account home region %q", organization.HomeRegion, account.HomeRegion)
	}
	if !validIsolationMode(organization.Isolation) {
		return fmt.Errorf("invalid organization isolation %q", organization.Isolation)
	}
	if strings.TrimSpace(project.ProjectID) == "" {
		return fmt.Errorf("project id is required")
	}
	if project.OrganizationID != organization.ID {
		return fmt.Errorf("project organization %q does not match organization %q", project.OrganizationID, organization.ID)
	}
	if !validIsolationMode(project.Isolation) {
		return fmt.Errorf("invalid project isolation %q", project.Isolation)
	}
	if !m.hasRegion(project.Region) {
		return fmt.Errorf("unknown project region %q", project.Region)
	}
	cell, ok := m.lookupCell(project.Region, project.Cell)
	if !ok {
		return fmt.Errorf("unknown cell %q in region %q", project.Cell, project.Region)
	}
	if cell.State != CellStateActive {
		return fmt.Errorf("cell %q is not active", project.Cell)
	}
	if !regionAllowed(account, project.Region) {
		return fmt.Errorf("region %q is not allowed for account %q", project.Region, account.ID)
	}
	if organization.Isolation == IsolationModeDedicatedCell && project.Isolation != IsolationModeDedicatedCell {
		return fmt.Errorf("project isolation must stay dedicated when organization isolation is dedicated")
	}
	return nil
}

func (m TenancyModel) hasRegion(region string) bool {
	for _, item := range m.Regions {
		if item.Slug == region {
			return true
		}
	}
	return false
}

func (m TenancyModel) lookupCell(regionSlug, cellSlug string) (Cell, bool) {
	for _, region := range m.Regions {
		if region.Slug != regionSlug {
			continue
		}
		for _, cell := range region.Cells {
			if cell.Slug == cellSlug {
				return cell, true
			}
		}
	}
	return Cell{}, false
}

func validateCell(regionSlug string, cell Cell) error {
	if strings.TrimSpace(cell.Slug) == "" {
		return fmt.Errorf("cell slug is required for region %q", regionSlug)
	}
	if cell.Region != regionSlug {
		return fmt.Errorf("cell %q references region %q, want %q", cell.Slug, cell.Region, regionSlug)
	}
	if !validCellState(cell.State) {
		return fmt.Errorf("invalid cell state %q", cell.State)
	}
	if !validIsolationMode(cell.DefaultIsolation) {
		return fmt.Errorf("invalid default isolation %q for cell %q", cell.DefaultIsolation, cell.Slug)
	}
	return nil
}

func validateResidency(policy ResidencyPolicy) error {
	switch policy.Mode {
	case ResidencyModeHomeOnly, ResidencyModePinned:
		return nil
	default:
		return fmt.Errorf("invalid residency mode %q", policy.Mode)
	}
}

func regionAllowed(account Account, region string) bool {
	switch account.Residency.Mode {
	case ResidencyModeHomeOnly:
		return region == account.HomeRegion
	case ResidencyModePinned:
		return slices.Contains(account.Residency.AllowedRegions, region)
	default:
		return false
	}
}

func validAccountStatus(status AccountStatus) bool {
	switch status {
	case AccountStatusActive, AccountStatusReadOnly, AccountStatusSuspended:
		return true
	default:
		return false
	}
}

func validOrganizationStatus(status OrganizationStatus) bool {
	switch status {
	case OrganizationStatusActive, OrganizationStatusSuspended, OrganizationStatusPendingDeletion:
		return true
	default:
		return false
	}
}

func validIsolationMode(mode IsolationMode) bool {
	switch mode {
	case IsolationModeSharedCell, IsolationModeDedicatedCell:
		return true
	default:
		return false
	}
}

func validCellState(state CellState) bool {
	switch state {
	case CellStateActive, CellStateDraining, CellStateDisabled:
		return true
	default:
		return false
	}
}
