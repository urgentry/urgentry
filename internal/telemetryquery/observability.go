package telemetryquery

import (
	"fmt"
	"slices"
)

type BridgeSurface string

const (
	BridgeSurfaceDiscover BridgeSurface = "discover"
	BridgeSurfaceLogs     BridgeSurface = "logs"
	BridgeSurfaceTraces   BridgeSurface = "traces"
	BridgeSurfaceReplay   BridgeSurface = "replay"
	BridgeSurfaceProfile  BridgeSurface = "profile"
)

var bridgeSurfaceOrder = []BridgeSurface{
	BridgeSurfaceDiscover,
	BridgeSurfaceLogs,
	BridgeSurfaceTraces,
	BridgeSurfaceReplay,
	BridgeSurfaceProfile,
}

type BridgeHealth string

const (
	BridgeHealthOK   BridgeHealth = "ok"
	BridgeHealthWarn BridgeHealth = "warn"
	BridgeHealthPage BridgeHealth = "page"
)

var bridgeHealthOrder = []BridgeHealth{
	BridgeHealthOK,
	BridgeHealthWarn,
	BridgeHealthPage,
}

type BridgeBudget struct {
	Surface              BridgeSurface `json:"surface"`
	LagBudgetSeconds     int           `json:"lagBudgetSeconds"`
	WarnLagSeconds       int           `json:"warnLagSeconds"`
	PageLagSeconds       int           `json:"pageLagSeconds"`
	DailyCostBudgetUnits int64         `json:"dailyCostBudgetUnits"`
	WarnCostBudgetUnits  int64         `json:"warnCostBudgetUnits"`
	PageCostBudgetUnits  int64         `json:"pageCostBudgetUnits"`
}

type BridgeObservability struct {
	Budgets []BridgeBudget `json:"budgets"`
}

type BridgeObservation struct {
	Surface            BridgeSurface `json:"surface"`
	LagSeconds         int           `json:"lagSeconds"`
	DailyCostUnits     int64         `json:"dailyCostUnits"`
	ProjectedDailyCost int64         `json:"projectedDailyCost"`
}

type BridgeAssessment struct {
	Surface BridgeSurface `json:"surface"`
	Health  BridgeHealth  `json:"health"`
	Reason  string        `json:"reason,omitempty"`
}

func DefaultBridgeObservability() BridgeObservability {
	return BridgeObservability{
		Budgets: []BridgeBudget{
			{Surface: BridgeSurfaceDiscover, LagBudgetSeconds: 120, WarnLagSeconds: 300, PageLagSeconds: 600, DailyCostBudgetUnits: 1000, WarnCostBudgetUnits: 1400, PageCostBudgetUnits: 1800},
			{Surface: BridgeSurfaceLogs, LagBudgetSeconds: 120, WarnLagSeconds: 300, PageLagSeconds: 600, DailyCostBudgetUnits: 1000, WarnCostBudgetUnits: 1400, PageCostBudgetUnits: 1800},
			{Surface: BridgeSurfaceTraces, LagBudgetSeconds: 120, WarnLagSeconds: 300, PageLagSeconds: 600, DailyCostBudgetUnits: 1000, WarnCostBudgetUnits: 1400, PageCostBudgetUnits: 1800},
			{Surface: BridgeSurfaceReplay, LagBudgetSeconds: 240, WarnLagSeconds: 600, PageLagSeconds: 1200, DailyCostBudgetUnits: 800, WarnCostBudgetUnits: 1100, PageCostBudgetUnits: 1500},
			{Surface: BridgeSurfaceProfile, LagBudgetSeconds: 240, WarnLagSeconds: 600, PageLagSeconds: 1200, DailyCostBudgetUnits: 800, WarnCostBudgetUnits: 1100, PageCostBudgetUnits: 1500},
		},
	}
}

func (o BridgeObservability) Validate() error {
	if len(o.Budgets) != len(bridgeSurfaceOrder) {
		return fmt.Errorf("expected %d bridge budgets, got %d", len(bridgeSurfaceOrder), len(o.Budgets))
	}
	for _, surface := range bridgeSurfaceOrder {
		item, ok := o.lookup(surface)
		if !ok {
			return fmt.Errorf("missing bridge budget for %q", surface)
		}
		if item.LagBudgetSeconds <= 0 || item.WarnLagSeconds <= 0 || item.PageLagSeconds <= 0 {
			return fmt.Errorf("surface %q must define positive lag thresholds", surface)
		}
		if item.LagBudgetSeconds > item.WarnLagSeconds || item.WarnLagSeconds > item.PageLagSeconds {
			return fmt.Errorf("surface %q has invalid lag threshold order", surface)
		}
		if item.DailyCostBudgetUnits <= 0 || item.WarnCostBudgetUnits <= 0 || item.PageCostBudgetUnits <= 0 {
			return fmt.Errorf("surface %q must define positive cost thresholds", surface)
		}
		if item.DailyCostBudgetUnits > item.WarnCostBudgetUnits || item.WarnCostBudgetUnits > item.PageCostBudgetUnits {
			return fmt.Errorf("surface %q has invalid cost threshold order", surface)
		}
	}
	return nil
}

func (o BridgeObservability) Evaluate(obs BridgeObservation) (BridgeAssessment, error) {
	if err := o.Validate(); err != nil {
		return BridgeAssessment{}, err
	}
	item, ok := o.lookup(obs.Surface)
	if !ok {
		return BridgeAssessment{}, fmt.Errorf("unknown bridge surface %q", obs.Surface)
	}
	if obs.LagSeconds < 0 || obs.DailyCostUnits < 0 || obs.ProjectedDailyCost < 0 {
		return BridgeAssessment{}, fmt.Errorf("lag and cost observations must be non-negative")
	}
	if obs.LagSeconds >= item.PageLagSeconds || obs.ProjectedDailyCost >= item.PageCostBudgetUnits {
		return BridgeAssessment{Surface: obs.Surface, Health: BridgeHealthPage, Reason: "bridge is outside the page threshold"}, nil
	}
	if obs.LagSeconds >= item.WarnLagSeconds || obs.ProjectedDailyCost >= item.WarnCostBudgetUnits {
		return BridgeAssessment{Surface: obs.Surface, Health: BridgeHealthWarn, Reason: "bridge is outside the warning threshold"}, nil
	}
	return BridgeAssessment{Surface: obs.Surface, Health: BridgeHealthOK}, nil
}

func (o BridgeObservability) lookup(surface BridgeSurface) (BridgeBudget, bool) {
	for _, item := range o.Budgets {
		if item.Surface == surface {
			return item, true
		}
	}
	return BridgeBudget{}, false
}

func ValidBridgeHealth(health BridgeHealth) bool {
	return slices.Contains(bridgeHealthOrder, health)
}
