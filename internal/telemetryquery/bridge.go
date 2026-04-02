package telemetryquery

import (
	"context"
	"database/sql"
	"fmt"

	"urgentry/internal/discover"
	"urgentry/internal/discoverharness"
	"urgentry/internal/store"
	"urgentry/internal/telemetrybridge"
)

/*
Bridge query flow

	Service facade
	  -> resolve scope / freshness
	  -> route by dataset
	     -> logs / transactions / traces
	     -> replays
	     -> profiles
	     -> discover bridge SQL
	  -> fall back to SQLite-backed readers only where explicitly allowed
*/

type bridgeService struct {
	sourceDB       *sql.DB
	bridgeDB       *sql.DB
	blobs          store.BlobStore
	issueSearch    IssueSearchStore
	contract       ExecutionContract
	sourceWeb      store.WebStore
	sourceDiscover discoverharness.Executor
	sourceTraces   TraceReadStore
	sourceReplays  store.ReplayReadStore
	sourceProfiles store.ProfileReadStore
	projector      *telemetrybridge.Projector
}

type Dependencies struct {
	Blobs       store.BlobStore
	IssueSearch IssueSearchStore
	Web         store.WebStore
	Discover    discoverharness.Executor
	Traces      TraceReadStore
	Replays     store.ReplayReadStore
	Profiles    store.ProfileReadStore
	Projector   *telemetrybridge.Projector
}

func NewService(sourceDB, bridgeDB *sql.DB, deps Dependencies) Service {
	if bridgeDB == nil {
		return newSQLiteServiceWithDependencies(deps)
	}
	issueSearch := deps.IssueSearch
	if issueSearch == nil {
		search, ok := deps.Web.(IssueSearchStore)
		if !ok {
			panic("telemetryquery: missing issue search dependency")
		}
		issueSearch = search
	}
	if deps.Web == nil || deps.Discover == nil || deps.Traces == nil || deps.Replays == nil || deps.Profiles == nil || deps.Projector == nil {
		panic("telemetryquery: missing bridge service dependencies")
	}
	return &bridgeService{
		sourceDB:       sourceDB,
		bridgeDB:       bridgeDB,
		blobs:          deps.Blobs,
		issueSearch:    issueSearch,
		contract:       DefaultExecutionContract(),
		sourceWeb:      deps.Web,
		sourceDiscover: deps.Discover,
		sourceTraces:   deps.Traces,
		sourceReplays:  deps.Replays,
		sourceProfiles: deps.Profiles,
		projector:      deps.Projector,
	}
}

func (s *bridgeService) SearchDiscoverIssues(ctx context.Context, orgSlug, filter, rawQuery string, limit int) ([]store.DiscoverIssue, error) {
	return s.issueSearch.SearchDiscoverIssues(ctx, orgSlug, filter, rawQuery, limit)
}

func (s *bridgeService) SearchDiscoverIssuesWithOptions(ctx context.Context, orgSlug string, opts store.DiscoverIssueSearchOptions) ([]store.DiscoverIssue, error) {
	if search, ok := s.issueSearch.(interface {
		SearchDiscoverIssuesWithOptions(context.Context, string, store.DiscoverIssueSearchOptions) ([]store.DiscoverIssue, error)
	}); ok {
		return search.SearchDiscoverIssuesWithOptions(ctx, orgSlug, opts)
	}
	return s.issueSearch.SearchDiscoverIssues(ctx, orgSlug, opts.Filter, opts.Query, opts.Limit)
}

func (s *bridgeService) ExecuteTable(ctx context.Context, query discover.Query) (discover.TableResult, error) {
	if query.Dataset == discover.DatasetLogs || query.Dataset == discover.DatasetTransactions {
		return s.executeBridgeTable(ctx, query)
	}
	return s.sourceDiscover.ExecuteTable(ctx, query)
}

func (s *bridgeService) ExecuteSeries(ctx context.Context, query discover.Query) (discover.SeriesResult, error) {
	if query.Dataset == discover.DatasetLogs || query.Dataset == discover.DatasetTransactions {
		return s.executeBridgeSeries(ctx, query)
	}
	return s.sourceDiscover.ExecuteSeries(ctx, query)
}

func (s *bridgeService) Explain(query discover.Query) (discover.ExplainPlan, error) {
	if query.Dataset == discover.DatasetLogs || query.Dataset == discover.DatasetTransactions {
		return s.explainBridge(context.Background(), query)
	}
	return s.sourceDiscover.Explain(query)
}

func (s *bridgeService) orgScope(ctx context.Context, orgSlug string) (telemetrybridge.Scope, error) {
	var scope telemetrybridge.Scope
	if err := s.sourceDB.QueryRowContext(ctx, `SELECT id FROM organizations WHERE slug = ?`, orgSlug).Scan(&scope.OrganizationID); err != nil {
		if err == sql.ErrNoRows {
			return scope, store.ErrNotFound
		}
		return scope, fmt.Errorf("resolve organization scope: %w", err)
	}
	return scope, nil
}

func (s *bridgeService) projectScope(ctx context.Context, projectID string) (telemetrybridge.Scope, error) {
	scope := telemetrybridge.Scope{ProjectID: projectID}
	if err := s.sourceDB.QueryRowContext(ctx, `SELECT organization_id FROM projects WHERE id = ?`, projectID).Scan(&scope.OrganizationID); err != nil {
		if err == sql.ErrNoRows {
			return scope, store.ErrNotFound
		}
		return scope, fmt.Errorf("resolve project scope: %w", err)
	}
	return scope, nil
}

func (s *bridgeService) projectSlugMap(ctx context.Context) (map[string]string, error) {
	rows, err := s.sourceDB.QueryContext(ctx, `SELECT id, slug FROM projects`)
	if err != nil {
		return nil, fmt.Errorf("list project slugs: %w", err)
	}
	defer rows.Close()
	items := map[string]string{}
	for rows.Next() {
		var id, slug string
		if err := rows.Scan(&id, &slug); err != nil {
			return nil, fmt.Errorf("scan project slug: %w", err)
		}
		items[id] = slug
	}
	return items, rows.Err()
}

func clamp(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
