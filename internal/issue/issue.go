// Package issue provides the issue/group model, storage interfaces, and
// the processor that ties normalization, grouping, and storage together.
package issue

import (
	"context"
	"time"
)

// Group is the core issue entity. Events are grouped into issues by
// their grouping key, which is computed from the event's fingerprint,
// stack trace, exception type, or message.
type Group struct {
	ID                  string
	ProjectID           string
	GroupingVersion     string
	GroupingKey         string
	Title               string
	Culprit             string
	Level               string
	Assignee            string
	Status              string // "unresolved", "resolved", "ignored", "merged"
	ResolutionSubstatus string
	ResolvedInRelease   string
	MergedIntoGroupID   string
	FirstSeen           time.Time
	LastSeen            time.Time
	TimesSeen           int64
	LastEventID         string
}

// GroupStore manages issue groups.
type GroupStore interface {
	UpsertGroup(ctx context.Context, g *Group) error
	GetGroup(ctx context.Context, id string) (*Group, error)
	GetGroupByKey(ctx context.Context, projectID, version, key string) (*Group, error)
	ListGroups(ctx context.Context, projectID string, opts ListOpts) ([]*Group, error)
	UpdateStatus(ctx context.Context, id string, status string) error
	UpdateAssignee(ctx context.Context, id string, assignee string) error
}

// ListOpts controls filtering, pagination, and sorting for group queries.
type ListOpts struct {
	Limit       int
	Cursor      string
	Sort        string // "last_seen_desc" (default), "last_seen_asc", "first_seen_desc", "first_seen_asc", "times_seen_desc"
	Status      string
	Release     string
	Environment string
}
