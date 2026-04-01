// Package store defines storage interfaces for events and raw payloads.
// WebStore abstracts all SQL queries needed by the web UI so that
// handlers never touch *sql.DB directly.
package store

import (
	"context"
	"time"
)

// WebStore provides read-only data access for the web UI layer.
// Implementations must return (value, error) — no silent error swallowing.
type WebStore interface {
	// Issue list
	ListIssues(ctx context.Context, opts IssueListOpts) ([]WebIssue, int, error)
	GetIssue(ctx context.Context, id string) (*WebIssue, error)
	GetIssues(ctx context.Context, ids []string) (map[string]WebIssue, error)

	// Events
	ListIssueEvents(ctx context.Context, groupID string, limit int) ([]WebEvent, error)
	ListRecentEvents(ctx context.Context, limit int) ([]WebEvent, error)
	GetEvent(ctx context.Context, eventID string) (*WebEvent, error)
	GetEventAtOffset(ctx context.Context, groupID string, offset int) (*WebEvent, error)
	CountEventsForGroup(ctx context.Context, groupID string) (int, error)
	CountDistinctUsersForGroup(ctx context.Context, groupID string) (int, error)

	// Dashboard
	DashboardSummary(ctx context.Context, now time.Time) (DashboardSummary, error)
	ListBurningIssues(ctx context.Context, now time.Time, limit int) ([]BurningIssueSummary, error)
	CountEvents(ctx context.Context) (int, error)
	CountGroups(ctx context.Context) (int, error)
	CountGroupsByStatus(ctx context.Context, status string) (int, error)
	CountAllGroupsByStatus(ctx context.Context) (total, unresolved, resolved, ignored int, err error)
	CountAllGroupsForEnvironment(ctx context.Context, env string) (total, unresolved, resolved, ignored int, err error)
	CountGroupsSince(ctx context.Context, since time.Time, status string) (int, error)
	CountGroupsForEnvironment(ctx context.Context, env, status string) (int, error)
	CountSearchGroups(ctx context.Context, filter, search string) (int, error)
	CountSearchGroupsForEnvironment(ctx context.Context, env, filter, search string) (int, error)
	CountEventsSince(ctx context.Context, since time.Time) (int, error)
	CountDistinctUsers(ctx context.Context) (int, error)
	CountDistinctUsersSince(ctx context.Context, since time.Time) (int, error)
	ListEnvironments(ctx context.Context) ([]string, error)

	// Batch helpers
	BatchUserCounts(ctx context.Context, groupIDs []string) (map[string]int, error)
	BatchSparklines(ctx context.Context, groupIDs []string, buckets int, window time.Duration) (map[string][]int, error)

	// Tags
	ListTagFacets(ctx context.Context, groupID string) ([]TagFacet, error)
	TagDistribution(ctx context.Context, groupID string) ([]TagDist, error)
	SearchIssues(ctx context.Context, rawQuery string, limit int) ([]WebIssue, error)
	SearchDiscoverIssues(ctx context.Context, orgSlug, filter, rawQuery string, limit int) ([]DiscoverIssue, error)
	ListRecentLogs(ctx context.Context, orgSlug string, limit int) ([]DiscoverLog, error)
	SearchLogs(ctx context.Context, orgSlug, rawQuery string, limit int) ([]DiscoverLog, error)
	ListRecentTransactions(ctx context.Context, orgSlug string, limit int) ([]DiscoverTransaction, error)
	SearchTransactions(ctx context.Context, orgSlug, rawQuery string, limit int) ([]DiscoverTransaction, error)

	// Chart
	EventChartData(ctx context.Context, groupID string, days int) ([]ChartPoint, error)

	// Beyond/dashboard/releases/feedback/alerts
	FirstEventAt(ctx context.Context) (*time.Time, error)
	CountErrorLevelEvents(ctx context.Context) (int, error)
	IssueDiffBase(ctx context.Context, groupID string) (*IssueDiffBase, *IssueDiffBase, error)
	ListEventAttachments(ctx context.Context, eventID string) ([]EventAttachment, error)
	ListIssueComments(ctx context.Context, groupID string, limit int) ([]IssueComment, error)
	ListIssueActivity(ctx context.Context, groupID string, limit int) ([]IssueActivityEntry, error)
	GetIssueWorkflowState(ctx context.Context, groupID, userID string) (IssueWorkflowState, error)
	ListSimilarIssues(ctx context.Context, groupID string, limit int) ([]WebIssue, error)
	ListMergedChildIssues(ctx context.Context, groupID string, limit int) ([]WebIssue, error)
	ListFeedback(ctx context.Context, limit int) ([]FeedbackRow, error)
	GetFeedback(ctx context.Context, id string) (*FeedbackRow, error)
	ListReleases(ctx context.Context, limit int) ([]ReleaseRow, error)
	DefaultProjectID(ctx context.Context) (string, error)
	ListAlertRules(ctx context.Context, limit int) ([]AlertRuleSummary, error)
	ListAlertHistory(ctx context.Context, limit int) ([]AlertHistoryEntry, error)
	ListAlertDeliveries(ctx context.Context, limit int) ([]AlertDeliveryEntry, error)
	SettingsOverview(ctx context.Context, auditLimit int) (SettingsOverview, error)
	AlertsOverview(ctx context.Context, ruleLimit, historyLimit, deliveryLimit int) (AlertsOverview, error)
}

// IssueListOpts controls filtering, searching, sorting, and pagination
// for issue list queries.
type IssueListOpts struct {
	Filter      string    // "all", "unresolved", "resolved", "ignored"
	Query       string    // free-text search across title/culprit
	Environment string    // empty = all environments
	Sort        string    // "last_seen", "first_seen", "events", "priority"
	Since       time.Time // zero value = no time bound
	Limit       int
	Offset      int
}

// WebIssue is the read model for issue list / detail pages.
type WebIssue struct {
	ID                  string
	Title               string
	Culprit             string
	Level               string
	Status              string
	ResolutionSubstatus string
	ResolvedInRelease   string
	MergedIntoGroupID   string
	FirstSeen           time.Time
	LastSeen            time.Time
	Count               int64
	ShortID             int
	Assignee            string
	Priority            int // 0=Critical, 1=High, 2=Medium (default), 3=Low
}

// WebEvent is the read model for event detail pages.
type WebEvent struct {
	EventID          string
	GroupID          string
	Title            string
	Message          string
	Level            string
	Platform         string
	Culprit          string
	Timestamp        time.Time
	Tags             map[string]string
	NormalizedJSON   string
	ProcessingStatus EventProcessingStatus
	IngestError      string
}

// TagFacet represents a tag key/value pair with its occurrence count.
type TagFacet struct {
	Key   string
	Value string
	Count int
}

// TagDist represents the top value for a tag key with its relative percentage.
type TagDist struct {
	Key     string
	Value   string
	Percent int
	Color   int // CSS hue for the bar color
}

// ChartPoint represents one bar in the event frequency chart.
type ChartPoint struct {
	Day    string
	Count  int
	Height int // percentage of max (0-100)
}

type IssueDiffBase struct {
	Level          string
	Release        string
	Environment    string
	UserIdentifier string
}

type DashboardSummary struct {
	TotalEvents      int
	UnresolvedGroups int
	EventsCurrent    int
	EventsPrevious   int
	ErrorsCurrent    int
	ErrorsPrevious   int
	UsersTotal       int
	UsersCurrent     int
	UsersPrevious    int
}

type BurningIssueSummary struct {
	ID     string
	Title  string
	Change int
}

type FeedbackRow struct {
	ID        string
	Name      string
	Email     string
	Comments  string
	EventID   string
	GroupID   string
	CreatedAt time.Time
}

type EventAttachment struct {
	ID          string
	Name        string
	ContentType string
	Size        int64
	CreatedAt   time.Time
}

type ReleaseRow struct {
	Version          string
	CreatedAt        time.Time
	EventCount       int
	SessionCount     int
	ErroredSessions  int
	CrashedSessions  int
	AbnormalSessions int
	AffectedUsers    int
	CrashFreeRate    float64
	LastSessionAt    time.Time
}

type AlertRuleSummary struct {
	ID           string
	ProjectID    string
	Name         string
	Status       string
	CreatedAt    time.Time
	Trigger      string
	TriggerLabel string
	ThresholdMS  string
	EmailTargets string
	WebhookURL   string
	SlackURL     string
	FireCount    int
}

type AlertHistoryEntry struct {
	ID       string
	RuleID   string
	RuleName string
	GroupID  string
	EventID  string
	FiredAt  time.Time
}

type AlertDeliveryEntry struct {
	ID             string
	ProjectID      string
	RuleID         string
	GroupID        string
	EventID        string
	Kind           string
	Target         string
	Status         string
	Attempts       int
	ResponseStatus *int
	Error          string
	CreatedAt      time.Time
	LastAttemptAt  *time.Time
	DeliveredAt    *time.Time
}

type SettingsOverview struct {
	Project           *Project
	ProjectKeys       []ProjectKeyMeta
	EventCount        int
	GroupCount        int
	AuditLogs         []AuditLogEntry
	TelemetryPolicies []TelemetryRetentionPolicy
}

type AlertsOverview struct {
	DefaultProjectID string
	Rules            []AlertRuleSummary
	History          []AlertHistoryEntry
	Deliveries       []AlertDeliveryEntry
}
