package telemetrybridge

import (
	"context"
	"fmt"
)

type projectorFamily struct {
	sync  func(*Projector, context.Context, Scope, int64) (StepResult, error)
	count func(*Projector, context.Context, Scope) (int, error)
	clear func(*Projector, context.Context, Scope) error
}

var projectorFamilyOrder = []Family{
	FamilyEvents,
	FamilyLogs,
	FamilyTransactions,
	FamilySpans,
	FamilyOutcomes,
	FamilyReplays,
	FamilyReplayTimeline,
	FamilyProfiles,
}

func defaultProjectorFamilies() map[Family]projectorFamily {
	return map[Family]projectorFamily{
		FamilyEvents:         projectorEventsFamily(),
		FamilyLogs:           projectorLogsFamily(),
		FamilyTransactions:   projectorTransactionsFamily(),
		FamilySpans:          projectorSpansFamily(),
		FamilyOutcomes:       projectorOutcomesFamily(),
		FamilyReplays:        projectorReplaysFamily(),
		FamilyReplayTimeline: projectorReplayTimelineFamily(),
		FamilyProfiles:       projectorProfilesFamily(),
	}
}

func (p *Projector) familyDescriptor(family Family) (projectorFamily, error) {
	definition, ok := p.families[family]
	if !ok {
		return projectorFamily{}, fmt.Errorf("unsupported telemetry projector family %q", family)
	}
	return definition, nil
}

func (p *Projector) countFamily(ctx context.Context, scope Scope, family Family) (int, error) {
	definition, err := p.familyDescriptor(family)
	if err != nil {
		return 0, err
	}
	return definition.count(p, ctx, scope)
}

func (p *Projector) deleteFamily(ctx context.Context, scope Scope, family Family) error {
	definition, err := p.familyDescriptor(family)
	if err != nil {
		return err
	}
	return definition.clear(p, ctx, scope)
}

func (p *Projector) countSourceRows(ctx context.Context, family Family, query string, args ...any) (int, error) {
	var count int
	if err := p.source.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count telemetry projector family %s: %w", family, err)
	}
	return count, nil
}

func (p *Projector) deleteBridgeRows(ctx context.Context, family Family, baseQuery string, scope Scope) error {
	scopeClause, args := bridgeScopeArgs(scope)
	if _, err := p.bridge.ExecContext(ctx, baseQuery+scopeClause, args...); err != nil {
		return fmt.Errorf("delete telemetry family %s: %w", family, err)
	}
	return nil
}
