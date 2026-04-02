package api

import (
	"encoding/json"
	"time"

	sharedstore "urgentry/internal/store"
)

type Organization = sharedstore.Organization

type Team = sharedstore.Team

type Project = sharedstore.Project

// ProjectKey represents a DSN key for a project.
type ProjectKey struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Label       string        `json:"label"`
	ProjectID   string        `json:"projectId"`
	Public      string        `json:"public"`
	Secret      string        `json:"secret"`
	IsActive    bool          `json:"isActive"`
	RateLimit   *KeyRateLimit `json:"rateLimit"`
	DSN         DSNURLs       `json:"dsn"`
	DateCreated time.Time     `json:"dateCreated"`
}

// KeyRateLimit describes per-key rate limiting.
type KeyRateLimit struct {
	Window int `json:"window"`
	Count  int `json:"count"`
}

// DSNURLs holds the public and secret DSN strings.
type DSNURLs struct {
	Public      string `json:"public"`
	Secret      string `json:"secret"`
	CDN         string `json:"cdn"`
	Crons       string `json:"crons"`
	CSP         string `json:"csp"`
	Integration string `json:"integration"`
	Minidump    string `json:"minidump"`
	NEL         string `json:"nel"`
	OTLPLogs    string `json:"otlp_logs"`
	OTLPTraces  string `json:"otlp_traces"`
	PlayStation string `json:"playstation"`
	Security    string `json:"security"`
	Unreal      string `json:"unreal"`
}

// Issue represents a grouped set of events.
type Issue struct {
	ID                  string     `json:"id"`
	ShortID             string     `json:"shortId"`
	Title               string     `json:"title"`
	Culprit             string     `json:"culprit"`
	Level               string     `json:"level"`
	Status              string     `json:"status"`
	Type                string     `json:"type"`
	AssignedTo          *IssueUser `json:"assignedTo"`
	HasSeen             bool       `json:"hasSeen"`
	IsBookmarked        bool       `json:"isBookmarked"`
	IsPublic            bool       `json:"isPublic"`
	IsSubscribed        bool       `json:"isSubscribed"`
	Priority            int        `json:"priority"`
	Substatus           string     `json:"substatus"`
	Metadata            Metadata   `json:"metadata"`
	NumComments         int        `json:"numComments"`
	UserCount           int        `json:"userCount"`
	Stats               IssueStats `json:"stats"`
	ResolutionSubstatus string     `json:"resolutionSubstatus,omitempty"`
	ResolvedInRelease   string     `json:"resolvedInRelease,omitempty"`
	MergedIntoIssueID   string     `json:"mergedIntoIssueId,omitempty"`
	FirstSeen           time.Time  `json:"firstSeen"`
	LastSeen            time.Time  `json:"lastSeen"`
	Count               int        `json:"count"`
	ProjectRef          ProjectRef `json:"project"`
}

// IssueUser is the Sentry-compatible assignee object embedded in issue responses.
type IssueUser struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Email    string `json:"email,omitempty"`
	Username string `json:"username,omitempty"`
}

// Metadata is a generic object wrapper used by issue responses.
type Metadata map[string]any

// IssueStats carries the compact issue sparkline fields used by list/detail UIs.
type IssueStats struct {
	Last24Hours []int `json:"24h"`
}

// DiscoverIssue is the org-wide issue row used by discover/search endpoints.
type DiscoverIssue = sharedstore.DiscoverIssue

// DiscoverLog is the org-wide log row used by discover/search endpoints.
type DiscoverLog = sharedstore.DiscoverLog

// DiscoverTransaction is the org-wide transaction row used by discover/search endpoints.
type DiscoverTransaction = sharedstore.DiscoverTransaction

// DiscoverResponse groups issue, log, and transaction hits for a discover query.
type DiscoverResponse struct {
	Query        string                `json:"query"`
	Scope        string                `json:"scope"`
	Issues       []DiscoverIssue       `json:"issues"`
	Logs         []DiscoverLog         `json:"logs"`
	Transactions []DiscoverTransaction `json:"transactions"`
}

// ProjectRef is a minimal project reference embedded in issue responses.
type ProjectRef struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
}

// Event represents a single error event.
type Event struct {
	ID               string       `json:"id"`
	EventID          string       `json:"eventID"`
	ProjectID        string       `json:"projectID,omitempty"`
	IssueID          string       `json:"groupID"`
	Title            string       `json:"title"`
	Message          string       `json:"message"`
	Level            string       `json:"level"`
	Platform         string       `json:"platform"`
	Culprit          string       `json:"culprit"`
	ProcessingStatus string       `json:"processingStatus,omitempty"`
	IngestError      string       `json:"ingestError,omitempty"`
	ResolvedFrames   int          `json:"resolvedFrames,omitempty"`
	UnresolvedFrames int          `json:"unresolvedFrames,omitempty"`
	DateCreated      time.Time    `json:"dateCreated"`
	Tags             []EventTag   `json:"tags,omitempty"`
	Entries          []EventEntry `json:"entries,omitempty"`
	Contexts         Metadata     `json:"contexts,omitempty"`
	SDK              Metadata     `json:"sdk,omitempty"`
	User             Metadata     `json:"user,omitempty"`
	Fingerprints     []string     `json:"fingerprints,omitempty"`
	Errors           []Metadata   `json:"errors,omitempty"`
	Packages         Metadata     `json:"packages,omitempty"`
	Measurements     Metadata     `json:"measurements,omitempty"`
}

// EventTag is the Sentry-compatible event tag shape used in API responses.
type EventTag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// EventEntry is one Sentry-style event interface entry.
type EventEntry struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// IssueComment represents a stored comment on an issue.
type IssueComment struct {
	ID          string    `json:"id"`
	IssueID     string    `json:"issueId"`
	ProjectID   string    `json:"projectId"`
	UserID      string    `json:"userId,omitempty"`
	UserEmail   string    `json:"userEmail,omitempty"`
	UserName    string    `json:"userName,omitempty"`
	Body        string    `json:"body"`
	DateCreated time.Time `json:"dateCreated"`
}

// IssueActivity represents one issue timeline entry.
type IssueActivity struct {
	ID          string    `json:"id"`
	IssueID     string    `json:"issueId"`
	ProjectID   string    `json:"projectId"`
	UserID      string    `json:"userId,omitempty"`
	UserEmail   string    `json:"userEmail,omitempty"`
	UserName    string    `json:"userName,omitempty"`
	Kind        string    `json:"kind"`
	Summary     string    `json:"summary"`
	Details     string    `json:"details,omitempty"`
	DateCreated time.Time `json:"dateCreated"`
}

// Attachment represents an event attachment.
type Attachment struct {
	ID          string    `json:"id"`
	EventID     string    `json:"eventId"`
	ProjectID   string    `json:"projectId"`
	Name        string    `json:"name"`
	ContentType string    `json:"contentType,omitempty"`
	Size        int64     `json:"size"`
	DateCreated time.Time `json:"dateCreated"`
}

// Release represents a release version.
type Release struct {
	ID                       string     `json:"id"`
	OrgSlug                  string     `json:"-"`
	Version                  string     `json:"version"`
	ShortVersion             string     `json:"shortVersion"`
	Ref                      string     `json:"ref,omitempty"`
	URL                      string     `json:"url,omitempty"`
	DateCreated              time.Time  `json:"dateCreated"`
	DateReleased             *time.Time `json:"dateReleased,omitempty"`
	NewGroups                int        `json:"newGroups"`
	SessionCount             int        `json:"sessionCount,omitempty"`
	ErroredSessions          int        `json:"erroredSessions,omitempty"`
	CrashedSessions          int        `json:"crashedSessions,omitempty"`
	AbnormalSessions         int        `json:"abnormalSessions,omitempty"`
	AffectedUsers            int        `json:"affectedUsers,omitempty"`
	CrashFreeRate            float64    `json:"crashFreeRate,omitempty"`
	LastSessionSeenAt        *time.Time `json:"lastSessionSeenAt,omitempty"`
	NativeEventCount         int        `json:"nativeEventCount,omitempty"`
	NativePendingEvents      int        `json:"nativePendingEvents,omitempty"`
	NativeProcessingEvents   int        `json:"nativeProcessingEvents,omitempty"`
	NativeFailedEvents       int        `json:"nativeFailedEvents,omitempty"`
	NativeResolvedFrames     int        `json:"nativeResolvedFrames,omitempty"`
	NativeUnresolvedFrames   int        `json:"nativeUnresolvedFrames,omitempty"`
	NativeLastError          string     `json:"nativeLastError,omitempty"`
	NativeReprocessRunID     string     `json:"nativeReprocessRunId,omitempty"`
	NativeReprocessStatus    string     `json:"nativeReprocessStatus,omitempty"`
	NativeReprocessLastError string     `json:"nativeReprocessLastError,omitempty"`
	NativeReprocessUpdatedAt *time.Time `json:"nativeReprocessUpdatedAt,omitempty"`
}

// OrgEventRow is an event with project context for org-level event listing.
type OrgEventRow struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Message     string     `json:"message,omitempty"`
	Level       string     `json:"level,omitempty"`
	Platform    string     `json:"platform,omitempty"`
	Culprit     string     `json:"culprit,omitempty"`
	ProjectName string     `json:"project.name"`
	Timestamp   time.Time  `json:"timestamp"`
	Tags        []EventTag `json:"tags,omitempty"`
}

// SourceMapDebugResponse describes source map resolution debug info for an event.
type SourceMapDebugResponse struct {
	EventID    string                `json:"eventId"`
	Release    string                `json:"release,omitempty"`
	HasRelease bool                  `json:"hasRelease"`
	Errors     []SourceMapDebugError `json:"errors"`
}

// SourceMapDebugError describes one source map resolution issue.
type SourceMapDebugError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// ReleaseHealth describes session-backed health for a release.
type ReleaseHealth struct {
	ProjectID         string     `json:"projectId,omitempty"`
	Version           string     `json:"version"`
	SessionCount      int        `json:"sessionCount"`
	ErroredSessions   int        `json:"erroredSessions"`
	CrashedSessions   int        `json:"crashedSessions"`
	AbnormalSessions  int        `json:"abnormalSessions"`
	AffectedUsers     int        `json:"affectedUsers"`
	CrashFreeRate     float64    `json:"crashFreeRate"`
	LastSessionSeenAt *time.Time `json:"lastSessionSeenAt,omitempty"`
}

// ReleaseSession is a single SDK session payload captured for release health.
type ReleaseSession struct {
	ID          string            `json:"id"`
	ProjectID   string            `json:"projectId"`
	Version     string            `json:"version"`
	Environment string            `json:"environment,omitempty"`
	SessionID   string            `json:"sessionId,omitempty"`
	DistinctID  string            `json:"distinctId,omitempty"`
	Status      string            `json:"status"`
	Errors      int               `json:"errors"`
	StartedAt   *time.Time        `json:"startedAt,omitempty"`
	Duration    float64           `json:"duration"`
	UserAgent   string            `json:"userAgent,omitempty"`
	Attrs       map[string]string `json:"attrs,omitempty"`
	Quantity    int               `json:"quantity"`
	DateCreated time.Time         `json:"dateCreated"`
}

// Outcome represents one dropped-event accounting row.
type Outcome struct {
	ID          string          `json:"id"`
	ProjectID   string          `json:"projectId"`
	EventID     string          `json:"eventId,omitempty"`
	Category    string          `json:"category"`
	Reason      string          `json:"reason"`
	Quantity    int             `json:"quantity"`
	Source      string          `json:"source"`
	Release     string          `json:"release,omitempty"`
	Environment string          `json:"environment,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	RecordedAt  time.Time       `json:"recordedAt"`
	DateCreated time.Time       `json:"dateCreated"`
}

// Monitor describes one persisted cron monitor.
type Monitor struct {
	ID            string        `json:"id"`
	ProjectID     string        `json:"projectId"`
	Slug          string        `json:"slug"`
	Status        string        `json:"status"`
	Environment   string        `json:"environment,omitempty"`
	Config        MonitorConfig `json:"config"`
	LastCheckInID string        `json:"lastCheckInId,omitempty"`
	LastStatus    string        `json:"lastStatus,omitempty"`
	LastCheckInAt *time.Time    `json:"lastCheckInAt,omitempty"`
	NextCheckInAt *time.Time    `json:"nextCheckInAt,omitempty"`
	DateCreated   time.Time     `json:"dateCreated"`
	DateUpdated   time.Time     `json:"dateUpdated"`
}

// MonitorConfig mirrors monitor schedule settings exposed via the API.
type MonitorConfig struct {
	Schedule      MonitorSchedule `json:"schedule"`
	CheckInMargin int             `json:"checkin_margin,omitempty"`
	MaxRuntime    int             `json:"max_runtime,omitempty"`
	Timezone      string          `json:"timezone,omitempty"`
}

// MonitorSchedule describes the recurrence for a monitor.
type MonitorSchedule struct {
	Type    string `json:"type,omitempty"`
	Value   int    `json:"value,omitempty"`
	Unit    string `json:"unit,omitempty"`
	Crontab string `json:"crontab,omitempty"`
}

// MonitorCheckIn represents one check-in execution row.
type MonitorCheckIn struct {
	ID           string          `json:"id"`
	MonitorID    string          `json:"monitorId"`
	ProjectID    string          `json:"projectId"`
	CheckInID    string          `json:"checkInId"`
	MonitorSlug  string          `json:"monitorSlug"`
	Status       string          `json:"status"`
	Duration     float64         `json:"duration"`
	Release      string          `json:"release,omitempty"`
	Environment  string          `json:"environment,omitempty"`
	ScheduledFor *time.Time      `json:"scheduledFor,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	DateCreated  time.Time       `json:"dateCreated"`
}

// TransactionSummary is a persisted performance transaction.
type TransactionSummary struct {
	ID             string                 `json:"id"`
	ProjectID      string                 `json:"projectId"`
	EventID        string                 `json:"eventId"`
	TraceID        string                 `json:"traceId"`
	SpanID         string                 `json:"spanId"`
	ParentSpanID   string                 `json:"parentSpanId,omitempty"`
	Transaction    string                 `json:"transaction"`
	Op             string                 `json:"op,omitempty"`
	Status         string                 `json:"status,omitempty"`
	Platform       string                 `json:"platform,omitempty"`
	Environment    string                 `json:"environment,omitempty"`
	Release        string                 `json:"release,omitempty"`
	StartTimestamp *time.Time             `json:"startTimestamp,omitempty"`
	EndTimestamp   *time.Time             `json:"endTimestamp,omitempty"`
	DurationMS     float64                `json:"durationMs"`
	Measurements   map[string]Measurement `json:"measurements,omitempty"`
}

// Measurement is a single performance measurement value.
type Measurement struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit,omitempty"`
}

// TraceSpan is a persisted child span within a trace.
type TraceSpan struct {
	ID                 string            `json:"id"`
	ProjectID          string            `json:"projectId"`
	TransactionEventID string            `json:"transactionEventId"`
	TraceID            string            `json:"traceId"`
	SpanID             string            `json:"spanId"`
	ParentSpanID       string            `json:"parentSpanId,omitempty"`
	Op                 string            `json:"op,omitempty"`
	Description        string            `json:"description,omitempty"`
	Status             string            `json:"status,omitempty"`
	StartTimestamp     *time.Time        `json:"startTimestamp,omitempty"`
	EndTimestamp       *time.Time        `json:"endTimestamp,omitempty"`
	DurationMS         float64           `json:"durationMs"`
	Tags               map[string]string `json:"tags,omitempty"`
	Data               map[string]any    `json:"data,omitempty"`
}

// TraceDetail groups root transactions and child spans for a trace.
type TraceDetail struct {
	TraceID      string               `json:"traceId"`
	Transactions []TransactionSummary `json:"transactions"`
	Spans        []TraceSpan          `json:"spans"`
	Profiles     []Profile            `json:"profiles,omitempty"`
}

// Replay is one stored session replay metadata row.
type Replay struct {
	ID          string          `json:"id"`
	ProjectID   string          `json:"projectId"`
	ReplayID    string          `json:"replayId"`
	Title       string          `json:"title"`
	URL         string          `json:"url,omitempty"`
	User        string          `json:"user,omitempty"`
	Platform    string          `json:"platform,omitempty"`
	Release     string          `json:"release,omitempty"`
	Environment string          `json:"environment,omitempty"`
	DateCreated time.Time       `json:"dateCreated"`
	Summary     ReplaySummary   `json:"summary"`
	Attachments []Attachment    `json:"attachments,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
}

// ReplaySummary captures the useful session facts for one replay row.
type ReplaySummary struct {
	RequestURL  string         `json:"requestUrl,omitempty"`
	User        string         `json:"user,omitempty"`
	Platform    string         `json:"platform,omitempty"`
	Release     string         `json:"release,omitempty"`
	Environment string         `json:"environment,omitempty"`
	AssetCount  int            `json:"assetCount"`
	AssetBytes  int64          `json:"assetBytes"`
	AssetKinds  map[string]int `json:"assetKinds,omitempty"`
}

type ReplayPlaybackManifest struct {
	ID               string            `json:"id"`
	ProjectID        string            `json:"projectId"`
	ReplayID         string            `json:"replayId"`
	Platform         string            `json:"platform,omitempty"`
	Release          string            `json:"release,omitempty"`
	Environment      string            `json:"environment,omitempty"`
	RequestURL       string            `json:"requestUrl,omitempty"`
	User             string            `json:"user,omitempty"`
	ProcessingStatus string            `json:"processingStatus"`
	IngestError      string            `json:"ingestError,omitempty"`
	StartedAt        *time.Time        `json:"startedAt,omitempty"`
	EndedAt          *time.Time        `json:"endedAt,omitempty"`
	DurationMS       int64             `json:"durationMs"`
	TimelineStartMS  int64             `json:"timelineStartMs"`
	TimelineEndMS    int64             `json:"timelineEndMs"`
	Counts           ReplayEventCounts `json:"counts"`
	TraceIDs         []string          `json:"traceIds,omitempty"`
	LinkedEventIDs   []string          `json:"linkedEventIds,omitempty"`
	LinkedIssueIDs   []string          `json:"linkedIssueIds,omitempty"`
	Assets           []ReplayAssetRef  `json:"assets"`
	DateCreated      time.Time         `json:"dateCreated"`
}

type ReplayEventCounts struct {
	Assets     int `json:"assets"`
	Console    int `json:"console"`
	Network    int `json:"network"`
	Clicks     int `json:"clicks"`
	Navigation int `json:"navigation"`
	ErrorMarks int `json:"errorMarks"`
}

type ReplayAssetRef struct {
	ID           string    `json:"id"`
	AttachmentID string    `json:"attachmentId"`
	Kind         string    `json:"kind"`
	Name         string    `json:"name"`
	ContentType  string    `json:"contentType,omitempty"`
	SizeBytes    int64     `json:"sizeBytes"`
	ChunkIndex   int       `json:"chunkIndex"`
	DateCreated  time.Time `json:"dateCreated"`
	DownloadURL  string    `json:"downloadUrl,omitempty"`
}

type ReplayTimelineItem struct {
	ID            string          `json:"id"`
	Anchor        string          `json:"anchor"`
	ReplayID      string          `json:"replayId"`
	TimestampMS   int64           `json:"timestampMs"`
	Kind          string          `json:"kind"`
	Pane          string          `json:"pane"`
	Title         string          `json:"title,omitempty"`
	Level         string          `json:"level,omitempty"`
	Message       string          `json:"message,omitempty"`
	URL           string          `json:"url,omitempty"`
	Method        string          `json:"method,omitempty"`
	StatusCode    int             `json:"statusCode,omitempty"`
	DurationMS    int64           `json:"durationMs,omitempty"`
	Selector      string          `json:"selector,omitempty"`
	Text          string          `json:"text,omitempty"`
	TraceID       string          `json:"traceId,omitempty"`
	LinkedEventID string          `json:"linkedEventId,omitempty"`
	LinkedIssueID string          `json:"linkedIssueId,omitempty"`
	PayloadRef    string          `json:"payloadRef,omitempty"`
	Meta          json.RawMessage `json:"meta,omitempty"`
}

type ReplayTimelinePage struct {
	ReplayID    string               `json:"replayId"`
	Pane        string               `json:"pane,omitempty"`
	StartMS     int64                `json:"startMs"`
	EndMS       int64                `json:"endMs"`
	Limit       int                  `json:"limit"`
	HasMore     bool                 `json:"hasMore"`
	NextStartMS int64                `json:"nextStartMs,omitempty"`
	Items       []ReplayTimelineItem `json:"items"`
}

// Profile summarizes one stored application profile payload.
type Profile struct {
	ID          string          `json:"id"`
	ProjectID   string          `json:"projectId"`
	ProfileID   string          `json:"profileId"`
	Transaction string          `json:"transaction,omitempty"`
	TraceID     string          `json:"traceId,omitempty"`
	Platform    string          `json:"platform,omitempty"`
	Release     string          `json:"release,omitempty"`
	Environment string          `json:"environment,omitempty"`
	DurationNS  string          `json:"durationNs,omitempty"`
	DateCreated time.Time       `json:"dateCreated"`
	Summary     ProfileSummary  `json:"summary"`
	Payload     json.RawMessage `json:"payload,omitempty"`
}

type ProfileSummary = sharedstore.ProfileSummary
type ProfileBreakdown = sharedstore.ProfileBreakdown

type ProfileTree = sharedstore.ProfileTree
type ProfileTreeNode = sharedstore.ProfileTreeNode
type ProfileHotPath = sharedstore.ProfileHotPath
type ProfileHotPathFrame = sharedstore.ProfileHotPathFrame
type ProfileComparison = sharedstore.ProfileComparison
type ProfileComparisonDelta = sharedstore.ProfileComparisonDelta

type AuditLogEntry = sharedstore.AuditLogEntry

// DebugFile represents a generic native/mobile debug upload.
type DebugFile struct {
	ID                  string     `json:"id"`
	ProjectID           string     `json:"projectId"`
	ReleaseID           string     `json:"releaseId"`
	Kind                string     `json:"kind"`
	Name                string     `json:"name"`
	DebugID             string     `json:"debugId,omitempty"`
	CodeID              string     `json:"codeId,omitempty"`
	SymbolicationStatus string     `json:"symbolicationStatus,omitempty"`
	ReprocessRunID      string     `json:"reprocessRunId,omitempty"`
	ReprocessStatus     string     `json:"reprocessStatus,omitempty"`
	ReprocessLastError  string     `json:"reprocessLastError,omitempty"`
	DateReprocessed     *time.Time `json:"dateReprocessed,omitempty"`
	ContentType         string     `json:"contentType,omitempty"`
	Size                int64      `json:"size"`
	Checksum            string     `json:"sha1,omitempty"`
	DateCreated         time.Time  `json:"dateCreated"`
}

type ProjectSettings = sharedstore.ProjectSettings

type OwnershipRule = sharedstore.OwnershipRule

type ReleaseDeploy = sharedstore.ReleaseDeploy

type ReleaseCommit = sharedstore.ReleaseCommit

type ReleaseSuspect = sharedstore.ReleaseSuspect

// Member represents an organization member (minimal).
type Member struct {
	ID             string    `json:"id"`
	UserID         string    `json:"userId,omitempty"`
	OrganizationID string    `json:"organizationId,omitempty"`
	TeamID         string    `json:"teamId,omitempty"`
	Email          string    `json:"email"`
	Name           string    `json:"name"`
	Role           string    `json:"role"`
	DateCreated    time.Time `json:"dateCreated"`
}

// ProjectMember represents a project-level membership.
type ProjectMember struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"projectId"`
	UserID      string    `json:"userId"`
	Email       string    `json:"email"`
	Name        string    `json:"name"`
	Role        string    `json:"role"`
	DateCreated time.Time `json:"dateCreated"`
}

// Invite represents a pending organization invite.
type Invite struct {
	ID               string     `json:"id"`
	OrganizationID   string     `json:"organizationId"`
	OrganizationSlug string     `json:"organizationSlug"`
	TeamID           string     `json:"teamId,omitempty"`
	TeamSlug         string     `json:"teamSlug,omitempty"`
	Email            string     `json:"email"`
	Role             string     `json:"role"`
	Status           string     `json:"status"`
	TokenPrefix      string     `json:"tokenPrefix"`
	DateCreated      time.Time  `json:"dateCreated"`
	ExpiresAt        *time.Time `json:"expiresAt,omitempty"`
	AcceptedAt       *time.Time `json:"acceptedAt,omitempty"`
	AcceptedByUserID string     `json:"acceptedByUserId,omitempty"`
}

// CreatedInvite is returned once when an invite is minted.
type CreatedInvite struct {
	Invite
	Token string `json:"token"`
}

// PersonalAccessToken represents redacted PAT metadata.
type PersonalAccessToken struct {
	ID          string     `json:"id"`
	Label       string     `json:"label"`
	TokenPrefix string     `json:"tokenPrefix"`
	Scopes      []string   `json:"scopes"`
	DateCreated time.Time  `json:"dateCreated"`
	LastUsed    *time.Time `json:"lastUsed,omitempty"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	RevokedAt   *time.Time `json:"revokedAt,omitempty"`
}

// CreatedPersonalAccessToken is returned once when a PAT is minted.
type CreatedPersonalAccessToken struct {
	PersonalAccessToken
	Token string `json:"token"`
}

// AutomationToken represents redacted project automation token metadata.
type AutomationToken struct {
	ID              string     `json:"id"`
	ProjectID       string     `json:"projectId"`
	Label           string     `json:"label"`
	TokenPrefix     string     `json:"tokenPrefix"`
	Scopes          []string   `json:"scopes"`
	CreatedByUserID string     `json:"createdByUserId,omitempty"`
	DateCreated     time.Time  `json:"dateCreated"`
	LastUsed        *time.Time `json:"lastUsed,omitempty"`
	ExpiresAt       *time.Time `json:"expiresAt,omitempty"`
	RevokedAt       *time.Time `json:"revokedAt,omitempty"`
}

// CreatedAutomationToken is returned once when an automation token is minted.
type CreatedAutomationToken struct {
	AutomationToken
	Token string `json:"token"`
}

// BackfillRun is one durable backfill or reprocessing job.
type BackfillRun struct {
	ID                string     `json:"id"`
	Kind              string     `json:"kind"`
	Status            string     `json:"status"`
	OrganizationID    string     `json:"organizationId"`
	ProjectID         string     `json:"projectId,omitempty"`
	ReleaseVersion    string     `json:"releaseVersion,omitempty"`
	DebugFileID       string     `json:"debugFileId,omitempty"`
	StartedAfter      *time.Time `json:"startedAfter,omitempty"`
	EndedBefore       *time.Time `json:"endedBefore,omitempty"`
	TotalItems        int        `json:"totalItems"`
	ProcessedItems    int        `json:"processedItems"`
	UpdatedItems      int        `json:"updatedItems"`
	FailedItems       int        `json:"failedItems"`
	RequestedByUserID string     `json:"requestedByUserId,omitempty"`
	RequestedVia      string     `json:"requestedVia,omitempty"`
	WorkerID          string     `json:"workerId,omitempty"`
	LastError         string     `json:"lastError,omitempty"`
	DateCreated       time.Time  `json:"dateCreated"`
	DateStarted       *time.Time `json:"dateStarted,omitempty"`
	DateFinished      *time.Time `json:"dateFinished,omitempty"`
	DateUpdated       time.Time  `json:"dateUpdated"`
}
