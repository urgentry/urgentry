package telemetryquery

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"urgentry/internal/discover"
	"urgentry/internal/discovershared"
	"urgentry/internal/telemetrybridge"
)

type bridgeDiscoverContext struct {
	organizationID   string
	projectSlugByID  map[string]string
	projectIDsBySlug map[string][]string
}

// bridgeSQLBuilder wraps discovershared.PostgresArgBuilder so methods
// on bridgeService can reach the inner args without a type assertion.
type bridgeSQLBuilder = discovershared.PostgresArgBuilder

func (s *bridgeService) executeBridgeTable(ctx context.Context, query discover.Query) (discover.TableResult, error) {
	query, cost, err := discover.ValidateQuery(query)
	if err != nil {
		return discover.TableResult{}, err
	}
	plan, state, err := s.explainBridgeQuery(ctx, query, "table")
	if err != nil {
		return discover.TableResult{}, err
	}
	scope := telemetrybridgeScopeForQuery(query, state.organizationID)
	if err := s.ensureSurfaceFresh(ctx, bridgeSurfaceForDataset(query.Dataset), scope, bridgeDiscoverFamily(query.Dataset)); err != nil {
		return discover.TableResult{}, err
	}
	if result, ok := s.cachedTable(query); ok {
		return result, nil
	}
	rows, err := s.fetchBridgeDiscoverRows(ctx, query, state, plan.ResultLimit)
	if err != nil {
		return discover.TableResult{}, err
	}
	result := discovershared.BuildTableResult(query, cost, rows)
	s.storeTable(query, result)
	return result, nil
}

func (s *bridgeService) executeBridgeSeries(ctx context.Context, query discover.Query) (discover.SeriesResult, error) {
	query, cost, err := discover.ValidateQuery(query)
	if err != nil {
		return discover.SeriesResult{}, err
	}
	plan, state, err := s.explainBridgeQuery(ctx, query, "series")
	if err != nil {
		return discover.SeriesResult{}, err
	}
	rows, err := s.fetchBridgeDiscoverRows(ctx, query, state, plan.ResultLimit)
	if err != nil {
		return discover.SeriesResult{}, err
	}
	return discovershared.BuildSeriesResult(query, cost, rows)
}

func (s *bridgeService) explainBridge(ctx context.Context, query discover.Query) (discover.ExplainPlan, error) {
	query, _, err := discover.ValidateQuery(query)
	if err != nil {
		return discover.ExplainPlan{}, err
	}
	plan, _, err := s.explainBridgeQuery(ctx, query, discovershared.ExplainMode(query))
	return plan, err
}

func (s *bridgeService) explainBridgeQuery(ctx context.Context, query discover.Query, mode string) (discover.ExplainPlan, bridgeDiscoverContext, error) {
	query, cost, err := discover.ValidateQuery(query)
	if err != nil {
		return discover.ExplainPlan{}, bridgeDiscoverContext{}, err
	}
	state, err := s.resolveBridgeDiscoverContext(ctx, query)
	if err != nil {
		return discover.ExplainPlan{}, bridgeDiscoverContext{}, err
	}
	sqlText, args, limit, err := buildBridgeDiscoverFetchSQL(query, state, discovershared.ScanLimit(query))
	if err != nil {
		return discover.ExplainPlan{}, bridgeDiscoverContext{}, err
	}
	return discover.ExplainPlan{
		Dataset:     query.Dataset,
		Mode:        mode,
		SQL:         sqlText,
		Args:        args,
		ResultLimit: limit,
		Cost:        cost,
	}, state, nil
}

func (s *bridgeService) resolveBridgeDiscoverContext(ctx context.Context, query discover.Query) (bridgeDiscoverContext, error) {
	state := bridgeDiscoverContext{
		projectSlugByID:  map[string]string{},
		projectIDsBySlug: map[string][]string{},
	}
	switch query.Scope.Kind {
	case discover.ScopeKindOrganization:
		scope, err := s.orgScope(ctx, query.Scope.Organization)
		if err != nil {
			return state, err
		}
		state.organizationID = scope.OrganizationID
	case discover.ScopeKindProject:
		scope, err := s.projectScope(ctx, query.Scope.ProjectID)
		if err != nil {
			return state, err
		}
		state.organizationID = scope.OrganizationID
	default:
		return state, fmt.Errorf("unsupported discover scope %q", query.Scope.Kind)
	}
	if cached, ok := s.cachedDiscoverContext(state.organizationID); ok {
		return cached, nil
	}
	rows, err := s.sourceDB.QueryContext(ctx, `SELECT id, slug FROM projects WHERE organization_id = ?`, state.organizationID)
	if err != nil {
		return state, fmt.Errorf("list discover project slugs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, slug string
		if err := rows.Scan(&id, &slug); err != nil {
			return state, fmt.Errorf("scan discover project slug: %w", err)
		}
		state.projectSlugByID[id] = slug
		state.projectIDsBySlug[slug] = append(state.projectIDsBySlug[slug], id)
	}
	if err := rows.Err(); err != nil {
		return state, err
	}
	s.setCachedDiscoverContext(state.organizationID, state)
	return state, nil
}

func (s *bridgeService) fetchBridgeDiscoverRows(ctx context.Context, query discover.Query, state bridgeDiscoverContext, limit int) ([]discover.TableRow, error) {
	scope := telemetrybridgeScopeForQuery(query, state.organizationID)
	if err := s.ensureSurfaceFresh(ctx, bridgeSurfaceForDataset(query.Dataset), scope, bridgeDiscoverFamily(query.Dataset)); err != nil {
		return nil, err
	}
	sqlText, args, _, err := buildBridgeDiscoverFetchSQL(query, state, limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.bridgeDB.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("query bridge discover rows: %w", err)
	}
	defer rows.Close()
	switch query.Dataset {
	case discover.DatasetLogs:
		return scanBridgeDiscoverLogRows(rows, state.projectSlugByID)
	case discover.DatasetTransactions:
		return scanBridgeDiscoverTransactionRows(rows, state.projectSlugByID)
	default:
		return nil, fmt.Errorf("unsupported bridge discover dataset %q", query.Dataset)
	}
}

func telemetrybridgeScopeForQuery(query discover.Query, organizationID string) telemetrybridge.Scope {
	scope := telemetrybridge.Scope{OrganizationID: organizationID}
	if query.Scope.Kind == discover.ScopeKindProject {
		scope.ProjectID = query.Scope.ProjectID
	}
	return scope
}

func bridgeDiscoverFamily(dataset discover.Dataset) telemetrybridge.Family {
	switch dataset {
	case discover.DatasetLogs:
		return telemetrybridge.FamilyLogs
	default:
		return telemetrybridge.FamilyTransactions
	}
}

func bridgeSurfaceForDataset(dataset discover.Dataset) QuerySurface {
	switch dataset {
	case discover.DatasetLogs:
		return QuerySurfaceDiscoverLogs
	default:
		return QuerySurfaceDiscoverTransactions
	}
}

func buildBridgeDiscoverFetchSQL(query discover.Query, state bridgeDiscoverContext, limit int) (string, []any, int, error) {
	limit = discovershared.DefaultFetchLimit(query.Limit, limit)
	switch query.Dataset {
	case discover.DatasetLogs:
		return buildBridgeLogsFetchSQL(query, state, limit)
	case discover.DatasetTransactions:
		return buildBridgeTransactionsFetchSQL(query, state, limit)
	default:
		return "", nil, 0, fmt.Errorf("unsupported bridge discover dataset %q", query.Dataset)
	}
}

func applyBridgeScopeClauses(query discover.Query, builder *bridgeSQLBuilder, projectColumn string, clauses *[]string) {
	switch query.Scope.Kind {
	case discover.ScopeKindProject:
		*clauses = append(*clauses, projectColumn+` = `+builder.Add(query.Scope.ProjectID))
	case discover.ScopeKindOrganization:
		if len(query.Scope.ProjectIDs) == 0 {
			return
		}
		*clauses = append(*clauses, projectColumn+` IN (`+builder.AddAll(query.Scope.ProjectIDs)+`)`)
	}
}

func bridgeProjectOverride(builder *bridgeSQLBuilder, state bridgeDiscoverContext, projectColumn string) discovershared.PredicateOverride {
	return func(pred discover.Predicate) (string, bool, error) {
		if !strings.EqualFold(strings.TrimSpace(pred.Field), "project") {
			return "", false, nil
		}
		sqlText, err := compileBridgeProjectPredicate(pred, builder, state, projectColumn)
		return sqlText, true, err
	}
}

func compileBridgeProjectPredicate(pred discover.Predicate, builder *bridgeSQLBuilder, state bridgeDiscoverContext, projectColumn string) (string, error) {
	resolve := func(match func(string) bool) []string {
		ids := make([]string, 0, len(state.projectIDsBySlug))
		for slug, projectIDs := range state.projectIDsBySlug {
			if match(slug) {
				ids = append(ids, projectIDs...)
			}
		}
		sort.Strings(ids)
		return ids
	}
	switch pred.Op {
	case "=":
		ids := append([]string(nil), state.projectIDsBySlug[pred.Value]...)
		if len(ids) == 0 {
			return "1=0", nil
		}
		return projectColumn + ` IN (` + builder.AddAll(ids) + `)`, nil
	case "!=":
		ids := append([]string(nil), state.projectIDsBySlug[pred.Value]...)
		if len(ids) == 0 {
			return "1=1", nil
		}
		return projectColumn + ` NOT IN (` + builder.AddAll(ids) + `)`, nil
	case "contains":
		needle := strings.ToLower(pred.Value)
		ids := resolve(func(slug string) bool { return strings.Contains(strings.ToLower(slug), needle) })
		if len(ids) == 0 {
			return "1=0", nil
		}
		return projectColumn + ` IN (` + builder.AddAll(ids) + `)`, nil
	case "prefix":
		prefix := strings.ToLower(pred.Value)
		ids := resolve(func(slug string) bool { return strings.HasPrefix(strings.ToLower(slug), prefix) })
		if len(ids) == 0 {
			return "1=0", nil
		}
		return projectColumn + ` IN (` + builder.AddAll(ids) + `)`, nil
	default:
		return "", fmt.Errorf("unsupported project predicate %q", pred.Op)
	}
}
