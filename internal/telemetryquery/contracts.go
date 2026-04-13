package telemetryquery

import (
	"context"

	"urgentry/internal/discoverharness"
	"urgentry/internal/store"
)

type DiscoverReadStore interface {
	SearchDiscoverIssues(ctx context.Context, orgSlug, filter, rawQuery string, limit int) ([]store.DiscoverIssue, error)
	ListRecentLogs(ctx context.Context, orgSlug string, limit int) ([]store.DiscoverLog, error)
	SearchLogs(ctx context.Context, orgSlug, rawQuery string, limit int) ([]store.DiscoverLog, error)
	ListRecentTransactions(ctx context.Context, orgSlug string, limit int) ([]store.DiscoverTransaction, error)
	SearchTransactions(ctx context.Context, orgSlug, rawQuery string, limit int) ([]store.DiscoverTransaction, error)
}

type LogReadStore interface {
	ListRecentLogs(ctx context.Context, orgSlug string, limit int) ([]store.DiscoverLog, error)
	SearchLogs(ctx context.Context, orgSlug, rawQuery string, limit int) ([]store.DiscoverLog, error)
}

type IssueSearchStore interface {
	SearchDiscoverIssues(ctx context.Context, orgSlug, filter, rawQuery string, limit int) ([]store.DiscoverIssue, error)
}

type TraceReadStore interface {
	ListTransactions(ctx context.Context, projectID string, limit int) ([]*store.StoredTransaction, error)
	ListTransactionsByTrace(ctx context.Context, projectID, traceID string) ([]*store.StoredTransaction, error)
	ListTraceSpans(ctx context.Context, projectID, traceID string) ([]store.StoredSpan, error)
}

type ProjectEventReadStore interface {
	ListProjectEvents(ctx context.Context, projectID string, limit, offset int) ([]store.WebEvent, error)
}

type OrgReplayReadStore interface {
	ListOrgReplays(ctx context.Context, orgID string, limit int) ([]store.ReplayManifest, error)
}

type Service interface {
	DiscoverReadStore
	TraceReadStore
	ProjectEventReadStore
	store.ReplayReadStore
	OrgReplayReadStore
	store.ProfileReadStore
	discoverharness.Executor
}
