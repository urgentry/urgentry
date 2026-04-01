package hosted

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

type UsageWindowKind string

const (
	UsageWindowMonthly UsageWindowKind = "monthly"
	UsageWindowDaily   UsageWindowKind = "daily"
)

var usageWindowOrder = []UsageWindowKind{
	UsageWindowMonthly,
	UsageWindowDaily,
}

type UsageLedgerPolicy struct {
	WindowByDimension map[UsageDimension]UsageWindowKind `json:"windowByDimension"`
}

type UsageLedgerPeriod struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type UsageLedgerEntry struct {
	AccountID      string              `json:"accountId"`
	AccountPlan    Plan                `json:"accountPlan"`
	Dimension      UsageDimension      `json:"dimension"`
	Surface        QuotaSurface        `json:"surface"`
	IdempotencyKey string              `json:"idempotencyKey"`
	WindowKind     UsageWindowKind     `json:"windowKind"`
	Window         UsageLedgerPeriod   `json:"window"`
	RecordedUnits  int64               `json:"recordedUnits"`
	BillableUnits  int64               `json:"billableUnits,omitempty"`
	GraceUnits     int64               `json:"graceUnits,omitempty"`
	BlockedUnits   int64               `json:"blockedUnits,omitempty"`
	Adjustment     UsageAdjustmentKind `json:"adjustment"`
	RecordedAt     time.Time           `json:"recordedAt"`
}

func DefaultUsageLedgerPolicy() UsageLedgerPolicy {
	return UsageLedgerPolicy{
		WindowByDimension: map[UsageDimension]UsageWindowKind{
			UsageMembers:               UsageWindowMonthly,
			UsageProjects:              UsageWindowMonthly,
			UsageMonthlyEvents:         UsageWindowMonthly,
			UsageDailyQueryUnits:       UsageWindowDaily,
			UsageMonthlyReplaySessions: UsageWindowMonthly,
			UsageMonthlyProfileSamples: UsageWindowMonthly,
			UsageStorageGiB:            UsageWindowMonthly,
			UsageMonthlyExportJobs:     UsageWindowMonthly,
			UsageMaxRetentionDays:      UsageWindowMonthly,
		},
	}
}

func (p UsageLedgerPolicy) Validate() error {
	if len(p.WindowByDimension) != len(usageDimensionOrder) {
		return fmt.Errorf("expected %d usage dimensions, got %d", len(usageDimensionOrder), len(p.WindowByDimension))
	}
	for _, dimension := range usageDimensionOrder {
		window, ok := p.WindowByDimension[dimension]
		if !ok {
			return fmt.Errorf("missing usage window for %q", dimension)
		}
		if !slices.Contains(usageWindowOrder, window) {
			return fmt.Errorf("dimension %q maps to unknown usage window %q", dimension, window)
		}
	}
	return nil
}

func (p UsageLedgerPolicy) BuildEntry(account Account, row UsageLedgerRow, idempotencyKey string) (UsageLedgerEntry, error) {
	if err := p.Validate(); err != nil {
		return UsageLedgerEntry{}, err
	}
	if strings.TrimSpace(account.ID) == "" {
		return UsageLedgerEntry{}, fmt.Errorf("account id is required")
	}
	if _, ok := DefaultCatalog().Lookup(account.Plan); !ok {
		return UsageLedgerEntry{}, fmt.Errorf("unknown account plan %q", account.Plan)
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return UsageLedgerEntry{}, fmt.Errorf("idempotency key is required")
	}
	if !slices.Contains(usageDimensionOrder, row.Dimension) {
		return UsageLedgerEntry{}, fmt.Errorf("unknown dimension %q", row.Dimension)
	}
	if !slices.Contains(quotaSurfaceOrder, row.Surface) {
		return UsageLedgerEntry{}, fmt.Errorf("unknown surface %q", row.Surface)
	}
	if DefaultQuotaPolicy().SurfaceDimensions[row.Surface] != row.Dimension {
		return UsageLedgerEntry{}, fmt.Errorf("surface %q does not map to dimension %q", row.Surface, row.Dimension)
	}
	if row.RecordedAt.IsZero() {
		return UsageLedgerEntry{}, fmt.Errorf("recorded time is required")
	}
	if row.RecordedUnits < 0 || row.BillableUnits < 0 || row.GraceUnits < 0 || row.BlockedUnits < 0 {
		return UsageLedgerEntry{}, fmt.Errorf("usage totals must be non-negative")
	}
	if !slices.Contains(usageAdjustmentOrder, row.Adjustment) {
		return UsageLedgerEntry{}, fmt.Errorf("unknown adjustment %q", row.Adjustment)
	}
	windowKind := p.WindowByDimension[row.Dimension]
	window := ledgerPeriod(windowKind, row.RecordedAt.UTC())
	return UsageLedgerEntry{
		AccountID:      account.ID,
		AccountPlan:    account.Plan,
		Dimension:      row.Dimension,
		Surface:        row.Surface,
		IdempotencyKey: strings.TrimSpace(idempotencyKey),
		WindowKind:     windowKind,
		Window:         window,
		RecordedUnits:  row.RecordedUnits,
		BillableUnits:  row.BillableUnits,
		GraceUnits:     row.GraceUnits,
		BlockedUnits:   row.BlockedUnits,
		Adjustment:     row.Adjustment,
		RecordedAt:     row.RecordedAt.UTC(),
	}, nil
}

func ledgerPeriod(kind UsageWindowKind, recordedAt time.Time) UsageLedgerPeriod {
	switch kind {
	case UsageWindowDaily:
		start := time.Date(recordedAt.Year(), recordedAt.Month(), recordedAt.Day(), 0, 0, 0, 0, time.UTC)
		return UsageLedgerPeriod{Start: start, End: start.Add(24 * time.Hour)}
	default:
		start := time.Date(recordedAt.Year(), recordedAt.Month(), 1, 0, 0, 0, 0, time.UTC)
		return UsageLedgerPeriod{Start: start, End: start.AddDate(0, 1, 0)}
	}
}
