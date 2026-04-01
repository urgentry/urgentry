package hosted

import (
	"fmt"
	"slices"
)

type QuotaSurface string

const (
	QuotaSurfaceIngestEvents  QuotaSurface = "ingest_events"
	QuotaSurfaceQuery         QuotaSurface = "query"
	QuotaSurfaceReplayIngest  QuotaSurface = "replay_ingest"
	QuotaSurfaceProfileIngest QuotaSurface = "profile_ingest"
	QuotaSurfaceExportJob     QuotaSurface = "export_job"
	QuotaSurfaceStorageWrite  QuotaSurface = "storage_write"
)

var quotaSurfaceOrder = []QuotaSurface{
	QuotaSurfaceIngestEvents,
	QuotaSurfaceQuery,
	QuotaSurfaceReplayIngest,
	QuotaSurfaceProfileIngest,
	QuotaSurfaceExportJob,
	QuotaSurfaceStorageWrite,
}

type QuotaPolicy struct {
	SurfaceDimensions map[QuotaSurface]UsageDimension `json:"surfaceDimensions"`
}

type QuotaRequest struct {
	Surface        QuotaSurface `json:"surface"`
	UsedUnits      int64        `json:"usedUnits"`
	RequestedUnits int64        `json:"requestedUnits"`
	GraceDaysUsed  int          `json:"graceDaysUsed,omitempty"`
	ContractLimit  *Limit       `json:"contractLimit,omitempty"`
}

type QuotaDecision struct {
	Surface               QuotaSurface   `json:"surface"`
	Dimension             UsageDimension `json:"dimension"`
	OverageMode           OverageMode    `json:"overageMode"`
	IncludedUnits         int64          `json:"includedUnits"`
	UsedUnits             int64          `json:"usedUnits"`
	RequestedUnits        int64          `json:"requestedUnits"`
	ProjectedUnits        int64          `json:"projectedUnits"`
	Allowed               bool           `json:"allowed"`
	BillableUnits         int64          `json:"billableUnits,omitempty"`
	GraceUnits            int64          `json:"graceUnits,omitempty"`
	GraceDaysRemaining    int            `json:"graceDaysRemaining,omitempty"`
	RequiresContractLimit bool           `json:"requiresContractLimit,omitempty"`
	Reason                string         `json:"reason,omitempty"`
}

func DefaultQuotaPolicy() QuotaPolicy {
	return QuotaPolicy{
		SurfaceDimensions: map[QuotaSurface]UsageDimension{
			QuotaSurfaceIngestEvents:  UsageMonthlyEvents,
			QuotaSurfaceQuery:         UsageDailyQueryUnits,
			QuotaSurfaceReplayIngest:  UsageMonthlyReplaySessions,
			QuotaSurfaceProfileIngest: UsageMonthlyProfileSamples,
			QuotaSurfaceExportJob:     UsageMonthlyExportJobs,
			QuotaSurfaceStorageWrite:  UsageStorageGiB,
		},
	}
}

func (p QuotaPolicy) Validate() error {
	if len(p.SurfaceDimensions) != len(quotaSurfaceOrder) {
		return fmt.Errorf("expected %d quota surfaces, got %d", len(quotaSurfaceOrder), len(p.SurfaceDimensions))
	}
	for _, surface := range quotaSurfaceOrder {
		dimension, ok := p.SurfaceDimensions[surface]
		if !ok {
			return fmt.Errorf("missing quota surface %q", surface)
		}
		if !slices.Contains(usageDimensionOrder, dimension) {
			return fmt.Errorf("surface %q maps to unknown usage dimension %q", surface, dimension)
		}
	}
	return nil
}

func (p QuotaPolicy) Evaluate(catalog Catalog, plan Plan, req QuotaRequest) (QuotaDecision, error) {
	if err := catalog.Validate(); err != nil {
		return QuotaDecision{}, err
	}
	if err := p.Validate(); err != nil {
		return QuotaDecision{}, err
	}
	if req.RequestedUnits <= 0 {
		return QuotaDecision{}, fmt.Errorf("requested units must be positive")
	}
	if req.UsedUnits < 0 {
		return QuotaDecision{}, fmt.Errorf("used units must be non-negative")
	}
	if req.GraceDaysUsed < 0 {
		return QuotaDecision{}, fmt.Errorf("grace days used must be non-negative")
	}

	spec, ok := catalog.Lookup(plan)
	if !ok {
		return QuotaDecision{}, fmt.Errorf("unknown plan %q", plan)
	}
	dimension, ok := p.SurfaceDimensions[req.Surface]
	if !ok {
		return QuotaDecision{}, fmt.Errorf("unknown quota surface %q", req.Surface)
	}

	limit := spec.Limits[dimension]
	decision := QuotaDecision{
		Surface:        req.Surface,
		Dimension:      dimension,
		OverageMode:    limit.OverageMode,
		IncludedUnits:  limit.Included,
		UsedUnits:      req.UsedUnits,
		RequestedUnits: req.RequestedUnits,
		ProjectedUnits: req.UsedUnits + req.RequestedUnits,
	}

	if limit.OverageMode == OverageModeContract {
		if req.ContractLimit == nil {
			decision.Allowed = true
			decision.RequiresContractLimit = true
			decision.Reason = "plan requires a customer-specific contract limit before hard enforcement"
			return decision, nil
		}
		if err := validateQuotaLimit(*req.ContractLimit); err != nil {
			return QuotaDecision{}, fmt.Errorf("invalid contract limit: %w", err)
		}
		limit = *req.ContractLimit
		decision.OverageMode = limit.OverageMode
		decision.IncludedUnits = limit.Included
	}

	overage := decision.ProjectedUnits - limit.Included
	if overage <= 0 {
		decision.Allowed = true
		return decision, nil
	}

	switch limit.OverageMode {
	case OverageModeBlock:
		decision.Reason = fmt.Sprintf("%s limit reached", dimension)
		return decision, nil
	case OverageModeMetered:
		decision.Allowed = true
		decision.BillableUnits = overage
		decision.Reason = fmt.Sprintf("%d %s units exceed the included allowance", overage, dimension)
		return decision, nil
	case OverageModeGraceThenBlock:
		if withinGrace(limit, overage, req.GraceDaysUsed) {
			decision.Allowed = true
			decision.GraceUnits = overage
			decision.GraceDaysRemaining = limit.GraceDays - req.GraceDaysUsed
			if decision.GraceDaysRemaining < 0 {
				decision.GraceDaysRemaining = 0
			}
			decision.Reason = fmt.Sprintf("%s is in a grace window", dimension)
			return decision, nil
		}
		decision.Reason = fmt.Sprintf("%s grace window exhausted", dimension)
		return decision, nil
	default:
		return QuotaDecision{}, fmt.Errorf("unsupported overage mode %q", limit.OverageMode)
	}
}

func validateQuotaLimit(limit Limit) error {
	if limit.Included <= 0 {
		return fmt.Errorf("included units must be positive")
	}
	switch limit.OverageMode {
	case OverageModeBlock, OverageModeMetered, OverageModeContract:
		if limit.GracePercent != 0 || limit.GraceDays != 0 {
			return fmt.Errorf("%s cannot define grace settings", limit.OverageMode)
		}
	case OverageModeGraceThenBlock:
		if limit.GracePercent <= 0 || limit.GraceDays <= 0 {
			return fmt.Errorf("grace_then_block requires positive grace settings")
		}
	default:
		return fmt.Errorf("unknown overage mode %q", limit.OverageMode)
	}
	return nil
}

func withinGrace(limit Limit, overage int64, graceDaysUsed int) bool {
	if limit.GracePercent <= 0 || limit.GraceDays <= 0 {
		return false
	}
	if graceDaysUsed >= limit.GraceDays {
		return false
	}
	return overagePercent(limit.Included, overage) <= limit.GracePercent
}

func overagePercent(included, overage int64) int {
	if included <= 0 || overage <= 0 {
		return 0
	}
	return int(((overage * 100) + included - 1) / included)
}
