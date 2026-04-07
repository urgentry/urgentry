package telemetrybridge

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type FamilyFreshness struct {
	Family      Family
	CursorFound bool
	Checkpoint  int64
	MaxRowID    int64
	Pending     bool
	UpdatedAt   time.Time
	Lag         time.Duration
	LastError   string
}

func (p *Projector) AssessFreshness(ctx context.Context, scope Scope, families ...Family) ([]FamilyFreshness, error) {
	if p == nil || p.source == nil || p.bridge == nil {
		return nil, nil
	}
	normalized := normalizeFamilies(families)
	items := make([]FamilyFreshness, 0, len(normalized))
	now := time.Now().UTC()
	for _, family := range normalized {
		item, err := p.assessFamilyFreshness(ctx, now, scope, family)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (p *Projector) assessFamilyFreshness(ctx context.Context, now time.Time, scope Scope, family Family) (FamilyFreshness, error) {
	if !isSupportedFamily(family) {
		return FamilyFreshness{}, fmt.Errorf("unsupported telemetry projector family %q", family)
	}
	state, err := p.loadCursorState(ctx, scope, family)
	if err != nil {
		return FamilyFreshness{}, err
	}
	item := FamilyFreshness{
		Family:      family,
		CursorFound: state.Found,
		Checkpoint:  state.Checkpoint,
		UpdatedAt:   state.UpdatedAt,
		LastError:   state.LastError,
	}
	if !state.Found {
		count, err := p.countFamily(ctx, scope, family)
		if err != nil {
			return FamilyFreshness{}, err
		}
		item.Pending = count > 0
	} else if !state.UpdatedAt.IsZero() {
		item.Lag = now.Sub(state.UpdatedAt.UTC())
		if item.Lag < 0 {
			item.Lag = 0
		}
		item.Pending = item.Lag > 0
	}
	return item, nil
}

func isSupportedFamily(f Family) bool {
	switch f {
	case FamilyEvents, FamilyLogs, FamilyTransactions, FamilySpans, FamilyOutcomes, FamilyReplays, FamilyReplayTimeline, FamilyProfiles:
		return true
	default:
		return false
	}
}

type cursorState struct {
	Found      bool
	Checkpoint int64
	UpdatedAt  time.Time
	LastError  string
}

func (p *Projector) loadCursorState(ctx context.Context, scope Scope, family Family) (cursorState, error) {
	name := cursorName(scope, family)
	if checkpoint, ok := p.cursorMap[name]; ok {
		var (
			updatedAt time.Time
			lastError string
		)
		err := p.bridge.QueryRowContext(ctx, `
			SELECT updated_at, COALESCE(last_error, '')
			  FROM telemetry.projector_cursors
			 WHERE name = $1`,
			name,
		).Scan(&updatedAt, &lastError)
		if err == sql.ErrNoRows {
			return cursorState{Found: true, Checkpoint: checkpoint}, nil
		}
		if err != nil {
			return cursorState{}, fmt.Errorf("load telemetry projector freshness %s: %w", family, err)
		}
		return cursorState{
			Found:      true,
			Checkpoint: checkpoint,
			UpdatedAt:  updatedAt.UTC(),
			LastError:  strings.TrimSpace(lastError),
		}, nil
	}
	var (
		checkpoint string
		updatedAt  time.Time
		lastError  string
	)
	err := p.bridge.QueryRowContext(ctx, `
		SELECT checkpoint, updated_at, COALESCE(last_error, '')
		  FROM telemetry.projector_cursors
		 WHERE name = $1`,
		name,
	).Scan(&checkpoint, &updatedAt, &lastError)
	if err == sql.ErrNoRows {
		return cursorState{}, nil
	}
	if err != nil {
		return cursorState{}, fmt.Errorf("load telemetry projector freshness %s: %w", family, err)
	}
	value, err := strconv.ParseInt(strings.TrimSpace(checkpoint), 10, 64)
	if err != nil {
		return cursorState{}, fmt.Errorf("parse telemetry projector freshness %s: %w", family, err)
	}
	p.cursorMap[name] = value
	return cursorState{
		Found:      true,
		Checkpoint: value,
		UpdatedAt:  updatedAt.UTC(),
		LastError:  strings.TrimSpace(lastError),
	}, nil
}
