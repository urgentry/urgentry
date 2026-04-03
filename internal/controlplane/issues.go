package controlplane

import (
	"context"

	sharedstore "urgentry/internal/store"
)

type IssueWorkflowStore interface {
	PatchIssue(ctx context.Context, id string, patch sharedstore.IssuePatch) error
	RecordIssueActivity(ctx context.Context, groupID, userID, kind, summary, details string) error
	ListIssueComments(ctx context.Context, groupID string, limit int) ([]sharedstore.IssueComment, error)
	AddIssueComment(ctx context.Context, groupID, userID, body string) (sharedstore.IssueComment, error)
	ListIssueActivity(ctx context.Context, groupID string, limit int) ([]sharedstore.IssueActivityEntry, error)
	MergeIssue(ctx context.Context, sourceGroupID, targetGroupID, actorUserID string) error
	UnmergeIssue(ctx context.Context, groupID, actorUserID string) error
	ToggleIssueBookmark(ctx context.Context, groupID, userID string, enabled bool) error
	ToggleIssueSubscription(ctx context.Context, groupID, userID string, enabled bool) error
	DeleteGroup(ctx context.Context, id string) error
	BulkDeleteGroups(ctx context.Context, ids []string) error
	BulkMutateGroups(ctx context.Context, ids []string, patch sharedstore.IssuePatch) error
}

type IssueReadStore interface {
	GetIssue(ctx context.Context, id string) (*sharedstore.WebIssue, error)
	SearchProjectIssues(ctx context.Context, projectID, filter, rawQuery string, limit int) ([]sharedstore.WebIssue, error)
	// SearchProjectIssuesPaged is like SearchProjectIssues but accepts an
	// explicit offset for DB-level pagination. Callers should request
	// limit+1 rows to detect whether a next page exists.
	SearchProjectIssuesPaged(ctx context.Context, projectID, filter, rawQuery string, limit, offset int) ([]sharedstore.WebIssue, error)
	SearchDiscoverIssues(ctx context.Context, orgSlug, filter, rawQuery string, limit int) ([]sharedstore.DiscoverIssue, error)
}
