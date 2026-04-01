package hosted

import (
	"fmt"
	"slices"
	"time"
)

type InvoiceStatus string

const (
	InvoiceStatusOpen      InvoiceStatus = "open"
	InvoiceStatusFinalized InvoiceStatus = "finalized"
	InvoiceStatusExported  InvoiceStatus = "exported"
	InvoiceStatusVoided    InvoiceStatus = "voided"
)

var invoiceStatusOrder = []InvoiceStatus{
	InvoiceStatusOpen,
	InvoiceStatusFinalized,
	InvoiceStatusExported,
	InvoiceStatusVoided,
}

type UsageAdjustmentKind string

const (
	UsageAdjustmentRecorded UsageAdjustmentKind = "recorded"
	UsageAdjustmentCredit   UsageAdjustmentKind = "credit"
	UsageAdjustmentManual   UsageAdjustmentKind = "manual"
)

var usageAdjustmentOrder = []UsageAdjustmentKind{
	UsageAdjustmentRecorded,
	UsageAdjustmentCredit,
	UsageAdjustmentManual,
}

type InvoicePeriod struct {
	Start  time.Time     `json:"start"`
	End    time.Time     `json:"end"`
	Status InvoiceStatus `json:"status"`
}

type UsageLedgerRow struct {
	Dimension     UsageDimension      `json:"dimension"`
	Surface       QuotaSurface        `json:"surface"`
	RecordedUnits int64               `json:"recordedUnits"`
	BillableUnits int64               `json:"billableUnits,omitempty"`
	GraceUnits    int64               `json:"graceUnits,omitempty"`
	BlockedUnits  int64               `json:"blockedUnits,omitempty"`
	Adjustment    UsageAdjustmentKind `json:"adjustment"`
	RecordedAt    time.Time           `json:"recordedAt"`
}

type BillingLine struct {
	Dimension             UsageDimension `json:"dimension"`
	OverageMode           OverageMode    `json:"overageMode"`
	IncludedUnits         int64          `json:"includedUnits"`
	RecordedUnits         int64          `json:"recordedUnits"`
	BillableUnits         int64          `json:"billableUnits,omitempty"`
	GraceUnits            int64          `json:"graceUnits,omitempty"`
	BlockedUnits          int64          `json:"blockedUnits,omitempty"`
	RequiresContractLimit bool           `json:"requiresContractLimit,omitempty"`
}

type BillingExport struct {
	AccountID   string        `json:"accountId"`
	AccountSlug string        `json:"accountSlug"`
	Plan        Plan          `json:"plan"`
	Status      AccountStatus `json:"status"`
	Period      InvoicePeriod `json:"period"`
	GeneratedAt time.Time     `json:"generatedAt"`
	Lines       []BillingLine `json:"lines"`
}

type BillingExportRequest struct {
	Account        Account                  `json:"account"`
	Period         InvoicePeriod            `json:"period"`
	Rows           []UsageLedgerRow         `json:"rows"`
	GeneratedAt    time.Time                `json:"generatedAt"`
	ContractLimits map[UsageDimension]Limit `json:"contractLimits,omitempty"`
}

func BuildBillingExport(catalog Catalog, req BillingExportRequest) (*BillingExport, error) {
	if err := catalog.Validate(); err != nil {
		return nil, err
	}
	if err := validateBillingRequest(catalog, req); err != nil {
		return nil, err
	}

	spec, _ := catalog.Lookup(req.Account.Plan)
	lines := make([]BillingLine, 0, len(usageDimensionOrder))
	totals := make(map[UsageDimension]*BillingLine, len(usageDimensionOrder))
	for _, dimension := range usageDimensionOrder {
		limit := spec.Limits[dimension]
		line := BillingLine{
			Dimension:     dimension,
			OverageMode:   limit.OverageMode,
			IncludedUnits: limit.Included,
		}
		if limit.OverageMode == OverageModeContract {
			if override, ok := req.ContractLimits[dimension]; ok {
				line.OverageMode = override.OverageMode
				line.IncludedUnits = override.Included
			} else {
				line.RequiresContractLimit = true
			}
		}
		lines = append(lines, line)
		totals[dimension] = &lines[len(lines)-1]
	}
	for _, row := range req.Rows {
		line := totals[row.Dimension]
		line.RecordedUnits += row.RecordedUnits
		line.BillableUnits += row.BillableUnits
		line.GraceUnits += row.GraceUnits
		line.BlockedUnits += row.BlockedUnits
	}
	return &BillingExport{
		AccountID:   req.Account.ID,
		AccountSlug: req.Account.Slug,
		Plan:        req.Account.Plan,
		Status:      req.Account.Status,
		Period:      req.Period,
		GeneratedAt: req.GeneratedAt.UTC(),
		Lines:       lines,
	}, nil
}

func validateBillingRequest(catalog Catalog, req BillingExportRequest) error {
	if req.GeneratedAt.IsZero() {
		return fmt.Errorf("generated time is required")
	}
	if stringsTrimmedEmpty(req.Account.ID) {
		return fmt.Errorf("account id is required")
	}
	if stringsTrimmedEmpty(req.Account.Slug) {
		return fmt.Errorf("account slug is required")
	}
	if _, ok := catalog.Lookup(req.Account.Plan); !ok {
		return fmt.Errorf("unknown plan %q", req.Account.Plan)
	}
	if !slices.Contains([]AccountStatus{
		AccountStatusActive,
		AccountStatusReadOnly,
		AccountStatusSuspended,
	}, req.Account.Status) {
		return fmt.Errorf("invalid account status %q", req.Account.Status)
	}
	if req.Period.Start.IsZero() || req.Period.End.IsZero() {
		return fmt.Errorf("invoice period start and end are required")
	}
	if !req.Period.Start.Before(req.Period.End) {
		return fmt.Errorf("invoice period start must be before end")
	}
	if !slices.Contains(invoiceStatusOrder, req.Period.Status) {
		return fmt.Errorf("invalid invoice status %q", req.Period.Status)
	}
	quota := DefaultQuotaPolicy()
	for dimension, limit := range req.ContractLimits {
		if !slices.Contains(usageDimensionOrder, dimension) {
			return fmt.Errorf("unknown contract limit dimension %q", dimension)
		}
		if err := validateQuotaLimit(limit); err != nil {
			return fmt.Errorf("contract limit %s: %w", dimension, err)
		}
	}
	for i, row := range req.Rows {
		if !slices.Contains(usageDimensionOrder, row.Dimension) {
			return fmt.Errorf("row %d has unknown dimension %q", i, row.Dimension)
		}
		if !slices.Contains(quotaSurfaceOrder, row.Surface) {
			return fmt.Errorf("row %d has unknown surface %q", i, row.Surface)
		}
		if quota.SurfaceDimensions[row.Surface] != row.Dimension {
			return fmt.Errorf("row %d surface %q does not match dimension %q", i, row.Surface, row.Dimension)
		}
		if row.RecordedUnits < 0 || row.BillableUnits < 0 || row.GraceUnits < 0 || row.BlockedUnits < 0 {
			return fmt.Errorf("row %d usage totals must be non-negative", i)
		}
		if !slices.Contains(usageAdjustmentOrder, row.Adjustment) {
			return fmt.Errorf("row %d has unknown adjustment %q", i, row.Adjustment)
		}
		if row.RecordedAt.IsZero() {
			return fmt.Errorf("row %d recorded time is required", i)
		}
		if row.RecordedAt.Before(req.Period.Start) || !row.RecordedAt.Before(req.Period.End) {
			return fmt.Errorf("row %d recorded time is outside the invoice period", i)
		}
	}
	return nil
}

func stringsTrimmedEmpty(v string) bool {
	for _, r := range v {
		switch r {
		case ' ', '\t', '\n', '\r':
			continue
		default:
			return false
		}
	}
	return true
}
