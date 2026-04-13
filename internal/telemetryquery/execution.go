package telemetryquery

import (
	"fmt"
	"slices"
)

type QuerySurface string

const (
	QuerySurfaceEvents               QuerySurface = "events"
	QuerySurfaceDiscoverLogs         QuerySurface = "discover_logs"
	QuerySurfaceDiscoverTransactions QuerySurface = "discover_transactions"
	QuerySurfaceTraces               QuerySurface = "traces"
	QuerySurfaceReplays              QuerySurface = "replays"
	QuerySurfaceProfiles             QuerySurface = "profiles"
)

var querySurfaceOrder = []QuerySurface{
	QuerySurfaceEvents,
	QuerySurfaceDiscoverLogs,
	QuerySurfaceDiscoverTransactions,
	QuerySurfaceTraces,
	QuerySurfaceReplays,
	QuerySurfaceProfiles,
}

type FreshnessMode string

const (
	FreshnessModeServeStale FreshnessMode = "serve_stale"
	FreshnessModeFailClosed FreshnessMode = "fail_closed"
)

type RebuildMode string

const (
	RebuildModeExplicitOnly RebuildMode = "explicit_only"
)

type SurfaceExecution struct {
	Surface                QuerySurface  `json:"surface"`
	FreshnessMode          FreshnessMode `json:"freshnessMode"`
	StaleBudgetSeconds     int           `json:"staleBudgetSeconds"`
	FailClosedAfterSeconds int           `json:"failClosedAfterSeconds"`
	MaxOrgConcurrency      int           `json:"maxOrgConcurrency"`
	TimeoutSeconds         int           `json:"timeoutSeconds"`
	RebuildMode            RebuildMode   `json:"rebuildMode"`
}

type AdmissionPolicy struct {
	SharedQuotaBackend      string `json:"sharedQuotaBackend"`
	MaxNodeConcurrency      int    `json:"maxNodeConcurrency"`
	MaxClusterOrgHeavyQuery int    `json:"maxClusterOrgHeavyQuery"`
	RequireCancellation     bool   `json:"requireCancellation"`
}

type ExecutionContract struct {
	Surfaces        []SurfaceExecution `json:"surfaces"`
	Admission       AdmissionPolicy    `json:"admission"`
	RequiredSignals []string           `json:"requiredSignals"`
}

func DefaultExecutionContract() ExecutionContract {
	return ExecutionContract{
		Surfaces: []SurfaceExecution{
			{
				Surface:                QuerySurfaceEvents,
				FreshnessMode:          FreshnessModeServeStale,
				StaleBudgetSeconds:     120,
				FailClosedAfterSeconds: 600,
				MaxOrgConcurrency:      6,
				TimeoutSeconds:         5,
				RebuildMode:            RebuildModeExplicitOnly,
			},
			{
				Surface:                QuerySurfaceDiscoverLogs,
				FreshnessMode:          FreshnessModeServeStale,
				StaleBudgetSeconds:     120,
				FailClosedAfterSeconds: 600,
				MaxOrgConcurrency:      6,
				TimeoutSeconds:         5,
				RebuildMode:            RebuildModeExplicitOnly,
			},
			{
				Surface:                QuerySurfaceDiscoverTransactions,
				FreshnessMode:          FreshnessModeServeStale,
				StaleBudgetSeconds:     120,
				FailClosedAfterSeconds: 600,
				MaxOrgConcurrency:      6,
				TimeoutSeconds:         5,
				RebuildMode:            RebuildModeExplicitOnly,
			},
			{
				Surface:                QuerySurfaceTraces,
				FreshnessMode:          FreshnessModeServeStale,
				StaleBudgetSeconds:     120,
				FailClosedAfterSeconds: 600,
				MaxOrgConcurrency:      4,
				TimeoutSeconds:         5,
				RebuildMode:            RebuildModeExplicitOnly,
			},
			{
				Surface:                QuerySurfaceReplays,
				FreshnessMode:          FreshnessModeServeStale,
				StaleBudgetSeconds:     300,
				FailClosedAfterSeconds: 900,
				MaxOrgConcurrency:      3,
				TimeoutSeconds:         8,
				RebuildMode:            RebuildModeExplicitOnly,
			},
			{
				Surface:                QuerySurfaceProfiles,
				FreshnessMode:          FreshnessModeServeStale,
				StaleBudgetSeconds:     300,
				FailClosedAfterSeconds: 900,
				MaxOrgConcurrency:      2,
				TimeoutSeconds:         8,
				RebuildMode:            RebuildModeExplicitOnly,
			},
		},
		Admission: AdmissionPolicy{
			SharedQuotaBackend:      "valkey",
			MaxNodeConcurrency:      32,
			MaxClusterOrgHeavyQuery: 8,
			RequireCancellation:     true,
		},
		RequiredSignals: []string{
			"org query concurrency by surface",
			"cluster query queue depth",
			"bridge freshness by family",
			"query timeout and cancellation counts",
			"query guard deny and retry counts",
		},
	}
}

func (c ExecutionContract) Validate() error {
	if len(c.Surfaces) != len(querySurfaceOrder) {
		return fmt.Errorf("expected %d query surfaces, got %d", len(querySurfaceOrder), len(c.Surfaces))
	}
	for _, surface := range querySurfaceOrder {
		item, ok := c.lookupSurface(surface)
		if !ok {
			return fmt.Errorf("missing query surface %q", surface)
		}
		if item.StaleBudgetSeconds <= 0 {
			return fmt.Errorf("surface %q must define a positive stale budget", surface)
		}
		if item.FailClosedAfterSeconds <= item.StaleBudgetSeconds {
			return fmt.Errorf("surface %q fail-closed budget must exceed stale budget", surface)
		}
		if item.MaxOrgConcurrency <= 0 {
			return fmt.Errorf("surface %q must define positive org concurrency", surface)
		}
		if item.TimeoutSeconds <= 0 {
			return fmt.Errorf("surface %q must define a timeout", surface)
		}
		if item.RebuildMode != RebuildModeExplicitOnly {
			return fmt.Errorf("surface %q must disable inline rebuilds", surface)
		}
		if item.FreshnessMode != FreshnessModeServeStale && item.FreshnessMode != FreshnessModeFailClosed {
			return fmt.Errorf("surface %q has invalid freshness mode %q", surface, item.FreshnessMode)
		}
	}
	if c.Admission.SharedQuotaBackend == "" {
		return fmt.Errorf("shared quota backend is required")
	}
	if c.Admission.MaxNodeConcurrency <= 0 {
		return fmt.Errorf("max node concurrency must be positive")
	}
	if c.Admission.MaxClusterOrgHeavyQuery <= 0 {
		return fmt.Errorf("max cluster org concurrency must be positive")
	}
	if len(c.RequiredSignals) == 0 {
		return fmt.Errorf("required signals must not be empty")
	}
	return nil
}

func (c ExecutionContract) lookupSurface(surface QuerySurface) (SurfaceExecution, bool) {
	for _, item := range c.Surfaces {
		if item.Surface == surface {
			return item, true
		}
	}
	return SurfaceExecution{}, false
}

func SupportedQuerySurfaces() []QuerySurface {
	out := make([]QuerySurface, len(querySurfaceOrder))
	copy(out, querySurfaceOrder)
	return out
}

func (c ExecutionContract) Surface(surface QuerySurface) (SurfaceExecution, error) {
	item, ok := c.lookupSurface(surface)
	if !ok {
		return SurfaceExecution{}, fmt.Errorf("unknown query surface %q", surface)
	}
	return item, nil
}

func ValidQuerySurface(surface QuerySurface) bool {
	return slices.Contains(querySurfaceOrder, surface)
}
