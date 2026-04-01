package telemetryquery

import (
	"context"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/telemetrybridge"
)

type BridgeStaleError struct {
	Surface QuerySurface
	Reasons []string
}

func (e *BridgeStaleError) Error() string {
	if e == nil {
		return ""
	}
	if len(e.Reasons) == 0 {
		return fmt.Sprintf("telemetry bridge unavailable for %s", e.Surface)
	}
	return fmt.Sprintf("telemetry bridge unavailable for %s: %s", e.Surface, strings.Join(e.Reasons, "; "))
}

func (s *bridgeService) ensureSurfaceFresh(ctx context.Context, surface QuerySurface, scope telemetrybridge.Scope, families ...telemetrybridge.Family) error {
	if s == nil || s.projector == nil {
		return nil
	}
	policy, err := s.contract.Surface(surface)
	if err != nil {
		return err
	}
	items, err := s.projector.AssessFreshness(ctx, scope, families...)
	if err != nil {
		return err
	}
	reasons := evaluateSurfaceFreshness(policy, items)
	if len(reasons) == 0 {
		return nil
	}
	return &BridgeStaleError{Surface: surface, Reasons: reasons}
}

func evaluateSurfaceFreshness(policy SurfaceExecution, items []telemetrybridge.FamilyFreshness) []string {
	staleBudget := time.Duration(policy.StaleBudgetSeconds) * time.Second
	failBudget := time.Duration(policy.FailClosedAfterSeconds) * time.Second
	var reasons []string
	for _, item := range items {
		if !item.Pending {
			continue
		}
		switch {
		case !item.CursorFound:
			reasons = append(reasons, fmt.Sprintf("%s has pending rows but no projection cursor; run a telemetry rebuild", item.Family))
		case item.LastError != "":
			reasons = append(reasons, fmt.Sprintf("%s projection is stalled after error %q", item.Family, item.LastError))
		case policy.FreshnessMode == FreshnessModeFailClosed && item.Lag > staleBudget:
			reasons = append(reasons, fmt.Sprintf("%s projection lag %s exceeds stale budget %s", item.Family, roundFreshnessDuration(item.Lag), staleBudget))
		case item.Lag > failBudget:
			reasons = append(reasons, fmt.Sprintf("%s projection lag %s exceeds fail-closed budget %s", item.Family, roundFreshnessDuration(item.Lag), failBudget))
		}
	}
	return reasons
}

func roundFreshnessDuration(value time.Duration) time.Duration {
	if value <= 0 {
		return 0
	}
	return value.Round(time.Second)
}
