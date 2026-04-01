package telemetrybridge

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/sqlutil"
)

/*
Projector flow

	SyncFamilies / StepFamilies
	  -> normalize requested families
	  -> load cursor for family + scope
	  -> dispatch to registered family projector
	  -> upsert bridge rows in batches
	  -> persist cursor / step progress

	Per-family SQL lives in projector_* files.
	Cursor, progress, and batch bookkeeping stay here.
*/

type Family string

const (
	FamilyEvents         Family = "events"
	FamilyLogs           Family = "logs"
	FamilyTransactions   Family = "transactions"
	FamilySpans          Family = "spans"
	FamilyOutcomes       Family = "outcomes"
	FamilyReplays        Family = "replays"
	FamilyReplayTimeline Family = "replay_timeline"
	FamilyProfiles       Family = "profiles"
)

type Scope struct {
	OrganizationID string
	ProjectID      string
}

type StepResult struct {
	Family    Family
	Processed int
	Done      bool
}

type bridgeExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type Projector struct {
	source    *sql.DB
	bridge    *sql.DB
	batchSize int
	cursorMap map[string]int64
	stepMap   map[string]int
	families  map[Family]projectorFamily
}

func NewProjector(source, bridge *sql.DB) *Projector {
	return &Projector{
		source:    source,
		bridge:    bridge,
		batchSize: 128,
		cursorMap: map[string]int64{},
		stepMap:   map[string]int{},
		families:  defaultProjectorFamilies(),
	}
}

func (p *Projector) SyncFamilies(ctx context.Context, scope Scope, families ...Family) error {
	for _, family := range normalizeFamilies(families) {
		for {
			result, err := p.syncFamilyBatch(ctx, scope, family)
			if err != nil {
				return err
			}
			if result.Done {
				break
			}
		}
	}
	return nil
}

func (p *Projector) StepFamilies(ctx context.Context, scope Scope, families ...Family) (StepResult, error) {
	normalized := normalizeFamilies(families)
	stepKey := scopeStepKey(scope, normalized)
	start := p.stepMap[stepKey]
	totalProcessed := 0
	var lastFamily Family
	for idx := start; idx < len(normalized); idx++ {
		family := normalized[idx]
		result, err := p.syncFamilyBatch(ctx, scope, family)
		if err != nil {
			return StepResult{}, err
		}
		totalProcessed += result.Processed
		if result.Processed > 0 {
			lastFamily = family
		}
		if !result.Done {
			p.stepMap[stepKey] = idx
			return StepResult{Family: family, Processed: totalProcessed, Done: false}, nil
		}
		p.stepMap[stepKey] = idx + 1
	}
	delete(p.stepMap, stepKey)
	return StepResult{Family: lastFamily, Processed: totalProcessed, Done: true}, nil
}

func (p *Projector) EstimateFamilies(ctx context.Context, scope Scope, families ...Family) (int, error) {
	total := 0
	for _, family := range normalizeFamilies(families) {
		count, err := p.countFamily(ctx, scope, family)
		if err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func (p *Projector) ResetScope(ctx context.Context, scope Scope, families ...Family) error {
	for _, family := range normalizeFamilies(families) {
		if err := p.deleteFamily(ctx, scope, family); err != nil {
			return err
		}
		if err := p.clearCursors(ctx, scope, family); err != nil {
			return err
		}
	}
	p.clearScopeProgress(scope)
	return nil
}

func normalizeFamilies(families []Family) []Family {
	if len(families) == 0 {
		return append([]Family(nil), projectorFamilyOrder...)
	}
	items := make([]Family, 0, len(families))
	seen := map[Family]struct{}{}
	for _, family := range families {
		if family == "" {
			continue
		}
		if _, ok := seen[family]; ok {
			continue
		}
		seen[family] = struct{}{}
		items = append(items, family)
	}
	return items
}

func (p *Projector) syncFamilyBatch(ctx context.Context, scope Scope, family Family) (StepResult, error) {
	if p == nil || p.source == nil || p.bridge == nil {
		return StepResult{Family: family, Done: true}, nil
	}
	checkpoint, err := p.cursor(ctx, scope, family)
	if err != nil {
		return StepResult{}, err
	}
	definition, err := p.familyDescriptor(family)
	if err != nil {
		return StepResult{}, err
	}
	return definition.sync(p, ctx, scope, checkpoint)
}

func bridgeScopeArgs(scope Scope) (string, []any) {
	if strings.TrimSpace(scope.ProjectID) != "" {
		return `project_id = $1`, []any{scope.ProjectID}
	}
	return `organization_id = $1`, []any{scope.OrganizationID}
}

func (p *Projector) cursor(ctx context.Context, scope Scope, family Family) (int64, error) {
	name := cursorName(scope, family)
	if checkpoint, ok := p.cursorMap[name]; ok {
		return checkpoint, nil
	}
	var checkpoint string
	err := p.bridge.QueryRowContext(ctx,
		`SELECT checkpoint
		   FROM telemetry.projector_cursors
		  WHERE name = $1`,
		name,
	).Scan(&checkpoint)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load telemetry projector cursor %s: %w", family, err)
	}
	value, err := strconv.ParseInt(strings.TrimSpace(checkpoint), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse telemetry projector cursor %s: %w", family, err)
	}
	p.cursorMap[name] = value
	return value, nil
}

func (p *Projector) setCursorWithExec(ctx context.Context, exec bridgeExecer, scope Scope, family Family, checkpoint int64) error {
	scopeKind, scopeID := scopeKindAndID(scope)
	_, err := exec.ExecContext(ctx, `
		INSERT INTO telemetry.projector_cursors
			(name, cursor_family, scope_kind, scope_id, checkpoint, metadata_json, updated_at)
		VALUES ($1,$2,$3,$4,$5,'{}'::jsonb, now())
		ON CONFLICT (name) DO UPDATE SET
			checkpoint = EXCLUDED.checkpoint,
			last_error = NULL,
			updated_at = now()`,
		cursorName(scope, family),
		string(family),
		scopeKind,
		scopeID,
		strconv.FormatInt(checkpoint, 10),
	)
	if err != nil {
		return fmt.Errorf("store telemetry projector cursor %s: %w", family, err)
	}
	return nil
}

func (p *Projector) clearCursors(ctx context.Context, scope Scope, family Family) error {
	if strings.TrimSpace(scope.ProjectID) != "" {
		name := cursorName(scope, family)
		if _, err := p.bridge.ExecContext(ctx, `DELETE FROM telemetry.projector_cursors WHERE name = $1`, name); err != nil {
			return fmt.Errorf("clear telemetry projector cursor %s: %w", family, err)
		}
		delete(p.cursorMap, name)
		return nil
	}
	projectIDs, err := p.projectIDsForOrganization(ctx, scope.OrganizationID)
	if err != nil {
		return err
	}
	args := make([]any, 0, len(projectIDs)+2)
	args = append(args, string(family), strings.TrimSpace(scope.OrganizationID))
	query := `DELETE FROM telemetry.projector_cursors WHERE cursor_family = $1 AND ((scope_kind = 'organization' AND scope_id = $2)`
	for i, projectID := range projectIDs {
		args = append(args, projectID)
		query += fmt.Sprintf(` OR (scope_kind = 'project' AND scope_id = $%d)`, i+3)
	}
	query += `)`
	if _, err := p.bridge.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("clear telemetry projector cursors %s: %w", family, err)
	}
	for key := range p.cursorMap {
		if strings.HasPrefix(key, string(family)+":organization:"+strings.TrimSpace(scope.OrganizationID)) || strings.HasPrefix(key, string(family)+":project:") {
			delete(p.cursorMap, key)
		}
	}
	return nil
}

func (p *Projector) withBridgeBatch(ctx context.Context, scope Scope, family Family, fn func(bridgeExecer) (int, int64, error)) (StepResult, error) {
	processed, last, err := fn(p.bridge)
	if err != nil {
		return StepResult{}, err
	}
	if processed == 0 {
		return StepResult{Family: family, Done: true}, nil
	}
	if err := p.setCursorWithExec(ctx, p.bridge, scope, family, last); err != nil {
		return StepResult{}, err
	}
	p.cursorMap[cursorName(scope, family)] = last
	return StepResult{Family: family, Processed: processed, Done: processed < p.batchSize}, nil
}

func (p *Projector) clearScopeProgress(scope Scope) {
	prefix := scopeCacheKey(scope) + "|"
	for key := range p.stepMap {
		if strings.HasPrefix(key, prefix) {
			delete(p.stepMap, key)
		}
	}
}

func scopeStepKey(scope Scope, families []Family) string {
	parts := make([]string, 0, len(families))
	for _, family := range families {
		parts = append(parts, string(family))
	}
	return scopeCacheKey(scope) + "|" + strings.Join(parts, ",")
}

func scopeCacheKey(scope Scope) string {
	scopeKind, scopeID := scopeKindAndID(scope)
	return scopeKind + ":" + scopeID
}

func (p *Projector) projectIDsForOrganization(ctx context.Context, organizationID string) ([]string, error) {
	rows, err := p.source.QueryContext(ctx, `SELECT id FROM projects WHERE organization_id = ?`, strings.TrimSpace(organizationID))
	if err != nil {
		return nil, fmt.Errorf("list projector scope projects: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan projector scope project: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projector scope projects: %w", err)
	}
	return ids, nil
}

func cursorName(scope Scope, family Family) string {
	scopeKind, scopeID := scopeKindAndID(scope)
	return string(family) + ":" + scopeKind + ":" + scopeID
}

func scopeKindAndID(scope Scope) (string, string) {
	if strings.TrimSpace(scope.ProjectID) != "" {
		return "project", strings.TrimSpace(scope.ProjectID)
	}
	return "organization", strings.TrimSpace(scope.OrganizationID)
}

func mustTimestamp(raw string) time.Time {
	return sqlutil.ParseDBTime(strings.TrimSpace(raw))
}

func nullTimestampArg(raw string) any {
	value := sqlutil.ParseDBTime(strings.TrimSpace(raw))
	if value.IsZero() {
		return nil
	}
	return value
}
