package telemetryquery

import (
	"context"
	"database/sql"
	"errors"

	"urgentry/internal/discover"
	"urgentry/internal/discoverharness"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

type sqliteService struct {
	db       *sql.DB
	issues   IssueSearchStore
	web      store.WebStore
	traces   TraceReadStore
	replays  store.ReplayReadStore
	profiles store.ProfileReadStore
	discover discoverharness.Executor
}

type issueSearchStoreWithOptions interface {
	SearchDiscoverIssuesWithOptions(ctx context.Context, orgSlug string, opts store.DiscoverIssueSearchOptions) ([]store.DiscoverIssue, error)
}

func NewSQLiteService(db *sql.DB, blobs store.BlobStore) Service {
	return newSQLiteService(db, blobs, nil)
}

func newSQLiteService(db *sql.DB, blobs store.BlobStore, issues IssueSearchStore) Service {
	webStore := sqlite.NewWebStore(db)
	if issues == nil {
		issues = webStore
	}
	return &sqliteService{
		db:       db,
		issues:   issues,
		web:      webStore,
		traces:   sqlite.NewTraceStore(db),
		replays:  sqlite.NewReplayStore(db, blobs),
		profiles: sqlite.NewProfileStore(db, blobs),
		discover: sqlite.NewDiscoverEngine(db),
	}
}

func newSQLiteServiceWithDependencies(db *sql.DB, deps Dependencies) Service {
	if db == nil {
		panic("telemetryquery: missing sqlite source db")
	}
	if deps.Web == nil || deps.Discover == nil || deps.Traces == nil || deps.Replays == nil || deps.Profiles == nil {
		panic("telemetryquery: missing sqlite service dependencies")
	}
	issues := deps.IssueSearch
	if issues == nil {
		search, ok := deps.Web.(IssueSearchStore)
		if !ok {
			panic("telemetryquery: missing issue search dependency")
		}
		issues = search
	}
	return &sqliteService{
		db:       db,
		issues:   issues,
		web:      deps.Web,
		traces:   deps.Traces,
		replays:  deps.Replays,
		profiles: deps.Profiles,
		discover: deps.Discover,
	}
}

func (s *sqliteService) SearchDiscoverIssues(ctx context.Context, orgSlug, filter, rawQuery string, limit int) ([]store.DiscoverIssue, error) {
	return s.issues.SearchDiscoverIssues(ctx, orgSlug, filter, rawQuery, limit)
}

func (s *sqliteService) SearchDiscoverIssuesWithOptions(ctx context.Context, orgSlug string, opts store.DiscoverIssueSearchOptions) ([]store.DiscoverIssue, error) {
	if search, ok := s.issues.(issueSearchStoreWithOptions); ok {
		return search.SearchDiscoverIssuesWithOptions(ctx, orgSlug, opts)
	}
	return s.issues.SearchDiscoverIssues(ctx, orgSlug, opts.Filter, opts.Query, opts.Limit)
}

func (s *sqliteService) ListRecentLogs(ctx context.Context, orgSlug string, limit int) ([]store.DiscoverLog, error) {
	return s.web.ListRecentLogs(ctx, orgSlug, limit)
}

func (s *sqliteService) SearchLogs(ctx context.Context, orgSlug, rawQuery string, limit int) ([]store.DiscoverLog, error) {
	return s.web.SearchLogs(ctx, orgSlug, rawQuery, limit)
}

func (s *sqliteService) ListRecentTransactions(ctx context.Context, orgSlug string, limit int) ([]store.DiscoverTransaction, error) {
	return s.web.ListRecentTransactions(ctx, orgSlug, limit)
}

func (s *sqliteService) SearchTransactions(ctx context.Context, orgSlug, rawQuery string, limit int) ([]store.DiscoverTransaction, error) {
	return s.web.SearchTransactions(ctx, orgSlug, rawQuery, limit)
}

func (s *sqliteService) ExecuteTable(ctx context.Context, query discover.Query) (discover.TableResult, error) {
	return s.discover.ExecuteTable(ctx, query)
}

func (s *sqliteService) ExecuteSeries(ctx context.Context, query discover.Query) (discover.SeriesResult, error) {
	return s.discover.ExecuteSeries(ctx, query)
}

func (s *sqliteService) Explain(query discover.Query) (discover.ExplainPlan, error) {
	return s.discover.Explain(query)
}

func (s *sqliteService) ListTransactions(ctx context.Context, projectID string, limit int) ([]*store.StoredTransaction, error) {
	return s.traces.ListTransactions(ctx, projectID, limit)
}

func (s *sqliteService) ListProjectEvents(ctx context.Context, projectID string, limit, offset int) ([]store.WebEvent, error) {
	if s.db == nil {
		return nil, errors.New("telemetryquery: sqlite service db is unavailable")
	}
	return sqlite.ListProjectEventsPaged(ctx, s.db, projectID, limit, offset)
}

func (s *sqliteService) ListTransactionsByTrace(ctx context.Context, projectID, traceID string) ([]*store.StoredTransaction, error) {
	return s.traces.ListTransactionsByTrace(ctx, projectID, traceID)
}

func (s *sqliteService) ListTraceSpans(ctx context.Context, projectID, traceID string) ([]store.StoredSpan, error) {
	return s.traces.ListTraceSpans(ctx, projectID, traceID)
}

func (s *sqliteService) ListReplays(ctx context.Context, projectID string, limit int) ([]store.ReplayManifest, error) {
	return s.replays.ListReplays(ctx, projectID, limit)
}

func (s *sqliteService) ListOrgReplays(ctx context.Context, orgID string, limit int) ([]store.ReplayManifest, error) {
	replays, ok := s.replays.(OrgReplayReadStore)
	if !ok {
		return nil, errors.New("telemetryquery: replay store does not support organization replay reads")
	}
	return replays.ListOrgReplays(ctx, orgID, limit)
}

func (s *sqliteService) GetReplay(ctx context.Context, projectID, replayID string) (*store.ReplayRecord, error) {
	return s.replays.GetReplay(ctx, projectID, replayID)
}

func (s *sqliteService) ListReplayTimeline(ctx context.Context, projectID, replayID string, filter store.ReplayTimelineFilter) ([]store.ReplayTimelineItem, error) {
	return s.replays.ListReplayTimeline(ctx, projectID, replayID, filter)
}

func (s *sqliteService) ListProfiles(ctx context.Context, projectID string, limit int) ([]store.ProfileManifest, error) {
	return s.profiles.ListProfiles(ctx, projectID, limit)
}

func (s *sqliteService) GetProfile(ctx context.Context, projectID, profileID string) (*store.ProfileRecord, error) {
	return s.profiles.GetProfile(ctx, projectID, profileID)
}

func (s *sqliteService) FindProfilesByTrace(ctx context.Context, projectID, traceID string, limit int) ([]store.ProfileReference, error) {
	return s.profiles.FindProfilesByTrace(ctx, projectID, traceID, limit)
}

func (s *sqliteService) ListReleaseProfileHighlights(ctx context.Context, projectID, release string, limit int) ([]store.ProfileReference, error) {
	return s.profiles.ListReleaseProfileHighlights(ctx, projectID, release, limit)
}

func (s *sqliteService) FindRelatedProfile(ctx context.Context, projectID, traceID, transaction, release string) (*store.ProfileReference, error) {
	return s.profiles.FindRelatedProfile(ctx, projectID, traceID, transaction, release)
}

func (s *sqliteService) QueryTopDown(ctx context.Context, projectID string, filter store.ProfileQueryFilter) (*store.ProfileTree, error) {
	return s.profiles.QueryTopDown(ctx, projectID, filter)
}

func (s *sqliteService) QueryBottomUp(ctx context.Context, projectID string, filter store.ProfileQueryFilter) (*store.ProfileTree, error) {
	return s.profiles.QueryBottomUp(ctx, projectID, filter)
}

func (s *sqliteService) QueryFlamegraph(ctx context.Context, projectID string, filter store.ProfileQueryFilter) (*store.ProfileTree, error) {
	return s.profiles.QueryFlamegraph(ctx, projectID, filter)
}

func (s *sqliteService) QueryHotPath(ctx context.Context, projectID string, filter store.ProfileQueryFilter) (*store.ProfileHotPath, error) {
	return s.profiles.QueryHotPath(ctx, projectID, filter)
}

func (s *sqliteService) CompareProfiles(ctx context.Context, projectID string, filter store.ProfileComparisonFilter) (*store.ProfileComparison, error) {
	return s.profiles.CompareProfiles(ctx, projectID, filter)
}
