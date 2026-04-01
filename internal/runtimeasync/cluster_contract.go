package runtimeasync

import "fmt"

type ClusterPrimitive string

const (
	ClusterPrimitiveIngestQuota ClusterPrimitive = "ingest_quota"
	ClusterPrimitiveQueryQuota  ClusterPrimitive = "query_quota"
	ClusterPrimitiveLease       ClusterPrimitive = "lease"
	ClusterPrimitiveHotCache    ClusterPrimitive = "hot_cache"
)

type FailureMode string

const (
	FailureModeFailClosed FailureMode = "fail_closed"
	FailureModeBestEffort FailureMode = "best_effort"
)

type PrimitiveContract struct {
	Primitive   ClusterPrimitive `json:"primitive"`
	Backend     string           `json:"backend"`
	ScopeKey    string           `json:"scopeKey"`
	FailureMode FailureMode      `json:"failureMode"`
	RepairPath  string           `json:"repairPath"`
}

type ClusterContract struct {
	Primitives []PrimitiveContract `json:"primitives"`
}

func DefaultClusterContract() ClusterContract {
	return ClusterContract{
		Primitives: []PrimitiveContract{
			{
				Primitive:   ClusterPrimitiveIngestQuota,
				Backend:     "valkey",
				ScopeKey:    "project_key + window",
				FailureMode: FailureModeFailClosed,
				RepairPath:  "inspect shared counters and clear poisoned windows only with scoped operator action",
			},
			{
				Primitive:   ClusterPrimitiveQueryQuota,
				Backend:     "valkey",
				ScopeKey:    "organization + workload + actor + window",
				FailureMode: FailureModeFailClosed,
				RepairPath:  "inspect quota windows, then clear only the affected org and workload state",
			},
			{
				Primitive:   ClusterPrimitiveLease,
				Backend:     "valkey",
				ScopeKey:    "lease family + holder",
				FailureMode: FailureModeFailClosed,
				RepairPath:  "force holder handoff only after the current holder and TTL are visible",
			},
			{
				Primitive:   ClusterPrimitiveHotCache,
				Backend:     "valkey",
				ScopeKey:    "query or auth scoped cache keys",
				FailureMode: FailureModeBestEffort,
				RepairPath:  "drop stale cache entries without mutating durable truth",
			},
		},
	}
}

func (c ClusterContract) Validate() error {
	if len(c.Primitives) != 4 {
		return fmt.Errorf("expected 4 cluster primitives, got %d", len(c.Primitives))
	}
	for _, item := range c.Primitives {
		if item.Primitive == "" || item.Backend == "" || item.ScopeKey == "" || item.RepairPath == "" {
			return fmt.Errorf("cluster primitive definitions must be complete")
		}
		if item.FailureMode != FailureModeFailClosed && item.FailureMode != FailureModeBestEffort {
			return fmt.Errorf("primitive %q has invalid failure mode %q", item.Primitive, item.FailureMode)
		}
	}
	return nil
}
