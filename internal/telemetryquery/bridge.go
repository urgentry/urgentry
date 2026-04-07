package telemetryquery

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

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

	// Cached lookups to avoid per-request DB queries.
	projectSlugsMu    sync.Mutex
	projectSlugsCache map[string]string
	orgScopeMu        sync.Mutex
	orgScopeCache     map[string]string // orgSlug -> orgID
	projectScopeMu    sync.Mutex
	projectScopeCache map[string]string // projectID -> orgID
	discoverCtxMu     sync.Mutex
	discoverCtxCache  map[string]bridgeDiscoverContext // organizationID -> cached project slug mappings
	readCacheMu       sync.Mutex
	replayCache       map[string]cachedReplayRecord
	profileCache      map[string]cachedProfileRecord
	traceTxnCache     map[string]cachedTransactions
	traceSpanCache    map[string]cachedSpans
	logsCache         map[string]cachedLogs
	tableCache        map[string]cachedTableResult
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
	s.orgScopeMu.Lock()
	if s.orgScopeCache != nil {
		if orgID, ok := s.orgScopeCache[orgSlug]; ok {
			s.orgScopeMu.Unlock()
			return telemetrybridge.Scope{OrganizationID: orgID}, nil
		}
	}
	s.orgScopeMu.Unlock()

	var scope telemetrybridge.Scope
	if err := s.sourceDB.QueryRowContext(ctx, `SELECT id FROM organizations WHERE slug = ?`, orgSlug).Scan(&scope.OrganizationID); err != nil {
		if err == sql.ErrNoRows {
			return scope, store.ErrNotFound
		}
		return scope, fmt.Errorf("resolve organization scope: %w", err)
	}

	s.orgScopeMu.Lock()
	if s.orgScopeCache == nil {
		s.orgScopeCache = make(map[string]string, 4)
	}
	s.orgScopeCache[orgSlug] = scope.OrganizationID
	s.orgScopeMu.Unlock()
	return scope, nil
}

func (s *bridgeService) projectScope(ctx context.Context, projectID string) (telemetrybridge.Scope, error) {
	scope := telemetrybridge.Scope{ProjectID: projectID}
	s.projectScopeMu.Lock()
	if s.projectScopeCache != nil {
		if orgID, ok := s.projectScopeCache[projectID]; ok {
			s.projectScopeMu.Unlock()
			scope.OrganizationID = orgID
			return scope, nil
		}
	}
	s.projectScopeMu.Unlock()
	if err := s.sourceDB.QueryRowContext(ctx, `SELECT organization_id FROM projects WHERE id = ?`, projectID).Scan(&scope.OrganizationID); err != nil {
		if err == sql.ErrNoRows {
			return scope, store.ErrNotFound
		}
		return scope, fmt.Errorf("resolve project scope: %w", err)
	}
	s.projectScopeMu.Lock()
	if s.projectScopeCache == nil {
		s.projectScopeCache = make(map[string]string, 8)
	}
	s.projectScopeCache[projectID] = scope.OrganizationID
	s.projectScopeMu.Unlock()
	return scope, nil
}

func (s *bridgeService) projectSlugMap(ctx context.Context) (map[string]string, error) {
	s.projectSlugsMu.Lock()
	if s.projectSlugsCache != nil {
		cached := s.projectSlugsCache
		s.projectSlugsMu.Unlock()
		return cached, nil
	}
	s.projectSlugsMu.Unlock()

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
	if err := rows.Err(); err != nil {
		return nil, err
	}

	s.projectSlugsMu.Lock()
	s.projectSlugsCache = items
	s.projectSlugsMu.Unlock()
	return items, nil
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

func (s *bridgeService) cachedDiscoverContext(organizationID string) (bridgeDiscoverContext, bool) {
	s.discoverCtxMu.Lock()
	defer s.discoverCtxMu.Unlock()
	if s.discoverCtxCache == nil {
		return bridgeDiscoverContext{}, false
	}
	state, ok := s.discoverCtxCache[organizationID]
	return state, ok
}

func (s *bridgeService) setCachedDiscoverContext(organizationID string, state bridgeDiscoverContext) {
	s.discoverCtxMu.Lock()
	defer s.discoverCtxMu.Unlock()
	if s.discoverCtxCache == nil {
		s.discoverCtxCache = make(map[string]bridgeDiscoverContext, 4)
	}
	s.discoverCtxCache[organizationID] = state
}
