package store

import (
	"fmt"
	"strings"
	"time"
)

// Organization is the shared organization model used by SQLite, API, and web.
type Organization struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	DateCreated time.Time `json:"dateCreated"`
}

// Team is the shared team model used by SQLite, API, and web.
type Team struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	OrgID       string    `json:"orgId,omitempty"`
	DateCreated time.Time `json:"dateCreated"`
}

// Project is the shared project model used by SQLite, API, and web.
type Project struct {
	ID                  string    `json:"id"`
	Slug                string    `json:"slug"`
	Name                string    `json:"name"`
	OrgSlug             string    `json:"organization,omitempty"`
	Platform            string    `json:"platform,omitempty"`
	Status              string    `json:"status,omitempty"`
	EventRetentionDays  int       `json:"eventRetentionDays,omitempty"`
	AttachRetentionDays int       `json:"attachmentRetentionDays,omitempty"`
	DebugRetentionDays  int       `json:"debugFileRetentionDays,omitempty"`
	DateCreated         time.Time `json:"dateCreated"`
	TeamSlug            string    `json:"team,omitempty"`
}

// ProjectKeyMeta is the shared project-key record without request-specific DSN decoration.
type ProjectKeyMeta struct {
	ID          string
	ProjectID   string
	Label       string
	PublicKey   string
	SecretKey   string
	Status      string
	DateCreated time.Time
}

// ProjectCreateInput captures the writable fields needed to create a project.
type ProjectCreateInput struct {
	Name     string
	Slug     string
	Platform string
}

// ProjectSettings captures mutable project-level settings.
type ProjectSettings struct {
	ID                      string                     `json:"id"`
	OrganizationSlug        string                     `json:"organizationSlug,omitempty"`
	Slug                    string                     `json:"slug"`
	Name                    string                     `json:"name"`
	Platform                string                     `json:"platform,omitempty"`
	Status                  string                     `json:"status"`
	DefaultEnvironment      string                     `json:"defaultEnvironment,omitempty"`
	EventRetentionDays      int                        `json:"eventRetentionDays"`
	AttachmentRetentionDays int                        `json:"attachmentRetentionDays"`
	DebugFileRetentionDays  int                        `json:"debugFileRetentionDays"`
	TelemetryPolicies       []TelemetryRetentionPolicy `json:"telemetryPolicies,omitempty"`
	ReplayPolicy            ReplayIngestPolicy         `json:"replayPolicy"`
	DateCreated             time.Time                  `json:"dateCreated,omitempty"`
}

// ProjectSettingsUpdate is the shared patch shape for project settings.
type ProjectSettingsUpdate struct {
	Name                    string
	Platform                string
	Status                  string
	EventRetentionDays      int
	AttachmentRetentionDays int
	DebugFileRetentionDays  int
	TelemetryPolicies       []TelemetryRetentionPolicy
	ReplayPolicy            ReplayIngestPolicy
}

type TelemetrySurface string

const (
	TelemetrySurfaceErrors      TelemetrySurface = "errors"
	TelemetrySurfaceLogs        TelemetrySurface = "logs"
	TelemetrySurfaceReplays     TelemetrySurface = "replays"
	TelemetrySurfaceProfiles    TelemetrySurface = "profiles"
	TelemetrySurfaceTraces      TelemetrySurface = "traces"
	TelemetrySurfaceOutcomes    TelemetrySurface = "outcomes"
	TelemetrySurfaceAttachments TelemetrySurface = "attachments"
	TelemetrySurfaceDebugFiles  TelemetrySurface = "debug_files"
)

type TelemetryStorageTier string

const (
	TelemetryStorageTierHot     TelemetryStorageTier = "hot"
	TelemetryStorageTierArchive TelemetryStorageTier = "archive"
	TelemetryStorageTierDelete  TelemetryStorageTier = "delete"
)

// TelemetryRetentionPolicy is the per-project retention contract for one telemetry surface.
type TelemetryRetentionPolicy struct {
	Surface              TelemetrySurface     `json:"surface"`
	RetentionDays        int                  `json:"retentionDays"`
	StorageTier          TelemetryStorageTier `json:"storageTier"`
	ArchiveRetentionDays int                  `json:"archiveRetentionDays,omitempty"`
}

var telemetrySurfaceOrder = []TelemetrySurface{
	TelemetrySurfaceErrors,
	TelemetrySurfaceLogs,
	TelemetrySurfaceTraces,
	TelemetrySurfaceReplays,
	TelemetrySurfaceProfiles,
	TelemetrySurfaceOutcomes,
	TelemetrySurfaceAttachments,
	TelemetrySurfaceDebugFiles,
}

func TelemetrySurfaces() []TelemetrySurface {
	out := make([]TelemetrySurface, len(telemetrySurfaceOrder))
	copy(out, telemetrySurfaceOrder)
	return out
}

func CanonicalTelemetryPolicies(input []TelemetryRetentionPolicy, eventDays, attachmentDays, debugDays int) ([]TelemetryRetentionPolicy, error) {
	defaults := defaultTelemetryPolicies(eventDays, attachmentDays, debugDays)
	if len(input) == 0 {
		return defaults, nil
	}

	bySurface := make(map[TelemetrySurface]TelemetryRetentionPolicy, len(defaults))
	for _, item := range defaults {
		bySurface[item.Surface] = item
	}

	seen := make(map[TelemetrySurface]struct{}, len(input))
	for _, item := range input {
		surface := TelemetrySurface(strings.ToLower(strings.TrimSpace(string(item.Surface))))
		if !validTelemetrySurface(surface) {
			return nil, fmt.Errorf("unknown telemetry surface %q", item.Surface)
		}
		if _, ok := seen[surface]; ok {
			return nil, fmt.Errorf("duplicate telemetry surface %q", surface)
		}
		seen[surface] = struct{}{}

		tier := TelemetryStorageTier(strings.ToLower(strings.TrimSpace(string(item.StorageTier))))
		if tier == "" {
			tier = bySurface[surface].StorageTier
		}
		if !validTelemetryStorageTier(tier) {
			return nil, fmt.Errorf("invalid telemetry storage tier %q", item.StorageTier)
		}
		if tier == TelemetryStorageTierArchive && !SupportsArchiveTelemetrySurface(surface) {
			return nil, fmt.Errorf("archive tier is not supported for %s", surface)
		}
		if item.RetentionDays <= 0 {
			return nil, fmt.Errorf("retentionDays must be positive for %s", surface)
		}
		if item.ArchiveRetentionDays < 0 {
			return nil, fmt.Errorf("archiveRetentionDays must be non-negative for %s", surface)
		}
		archiveDays := item.ArchiveRetentionDays
		if tier == TelemetryStorageTierArchive && archiveDays == 0 {
			archiveDays = item.RetentionDays * 2
		}

		bySurface[surface] = TelemetryRetentionPolicy{
			Surface:              surface,
			RetentionDays:        item.RetentionDays,
			StorageTier:          tier,
			ArchiveRetentionDays: archiveDays,
		}
	}

	out := make([]TelemetryRetentionPolicy, 0, len(defaults))
	for _, surface := range telemetrySurfaceOrder {
		out = append(out, bySurface[surface])
	}
	return out, nil
}

func defaultTelemetryPolicies(eventDays, attachmentDays, debugDays int) []TelemetryRetentionPolicy {
	if eventDays <= 0 {
		eventDays = 90
	}
	if attachmentDays <= 0 {
		attachmentDays = 30
	}
	if debugDays <= 0 {
		debugDays = 180
	}
	return []TelemetryRetentionPolicy{
		{Surface: TelemetrySurfaceErrors, RetentionDays: eventDays, StorageTier: TelemetryStorageTierDelete},
		{Surface: TelemetrySurfaceLogs, RetentionDays: eventDays, StorageTier: TelemetryStorageTierDelete},
		{Surface: TelemetrySurfaceTraces, RetentionDays: eventDays, StorageTier: TelemetryStorageTierDelete},
		{Surface: TelemetrySurfaceReplays, RetentionDays: attachmentDays, StorageTier: TelemetryStorageTierDelete},
		{Surface: TelemetrySurfaceProfiles, RetentionDays: eventDays, StorageTier: TelemetryStorageTierDelete},
		{Surface: TelemetrySurfaceOutcomes, RetentionDays: eventDays, StorageTier: TelemetryStorageTierDelete},
		{Surface: TelemetrySurfaceAttachments, RetentionDays: attachmentDays, StorageTier: TelemetryStorageTierDelete},
		{Surface: TelemetrySurfaceDebugFiles, RetentionDays: debugDays, StorageTier: TelemetryStorageTierArchive, ArchiveRetentionDays: debugDays * 2},
	}
}

func validTelemetrySurface(surface TelemetrySurface) bool {
	for _, item := range telemetrySurfaceOrder {
		if item == surface {
			return true
		}
	}
	return false
}

func validTelemetryStorageTier(tier TelemetryStorageTier) bool {
	switch tier {
	case TelemetryStorageTierHot, TelemetryStorageTierArchive, TelemetryStorageTierDelete:
		return true
	default:
		return false
	}
}

func SupportsArchiveTelemetrySurface(surface TelemetrySurface) bool {
	return validTelemetrySurface(surface)
}

// AuditLogEntry is the shared redacted auth/admin activity record.
type AuditLogEntry struct {
	ID               string    `json:"id"`
	CredentialType   string    `json:"credentialType"`
	CredentialID     string    `json:"credentialId,omitempty"`
	UserID           string    `json:"userId,omitempty"`
	UserEmail        string    `json:"userEmail,omitempty"`
	ProjectID        string    `json:"projectId,omitempty"`
	ProjectSlug      string    `json:"projectSlug,omitempty"`
	OrganizationID   string    `json:"organizationId,omitempty"`
	OrganizationSlug string    `json:"organizationSlug,omitempty"`
	Action           string    `json:"action"`
	RequestPath      string    `json:"requestPath,omitempty"`
	RequestMethod    string    `json:"requestMethod,omitempty"`
	IPAddress        string    `json:"ipAddress,omitempty"`
	UserAgent        string    `json:"userAgent,omitempty"`
	DateCreated      time.Time `json:"dateCreated"`
}

// IssuePatch is the shared mutation shape for issue metadata updates.
type IssuePatch struct {
	Status              *string
	Assignee            *string
	Priority            *int
	ResolutionSubstatus *string
	ResolvedInRelease   *string
	MergedIntoGroupID   *string
}

// IssueComment is a persisted issue comment with lightweight user metadata.
type IssueComment struct {
	ID          string    `json:"id"`
	GroupID     string    `json:"groupId"`
	ProjectID   string    `json:"projectId"`
	UserID      string    `json:"userId,omitempty"`
	UserEmail   string    `json:"userEmail,omitempty"`
	UserName    string    `json:"userName,omitempty"`
	Body        string    `json:"body"`
	DateCreated time.Time `json:"dateCreated"`
}

// IssueActivityEntry is a persisted issue activity row.
type IssueActivityEntry struct {
	ID          string    `json:"id"`
	GroupID     string    `json:"groupId"`
	ProjectID   string    `json:"projectId"`
	UserID      string    `json:"userId,omitempty"`
	UserEmail   string    `json:"userEmail,omitempty"`
	UserName    string    `json:"userName,omitempty"`
	Kind        string    `json:"kind"`
	Summary     string    `json:"summary"`
	Details     string    `json:"details,omitempty"`
	DateCreated time.Time `json:"dateCreated"`
}

// IssueWorkflowState captures per-user state around a group.
type IssueWorkflowState struct {
	Bookmarked          bool   `json:"bookmarked"`
	Subscribed          bool   `json:"subscribed"`
	MergedIntoGroupID   string `json:"mergedIntoGroupId,omitempty"`
	ResolutionSubstatus string `json:"resolutionSubstatus,omitempty"`
	ResolvedInRelease   string `json:"resolvedInRelease,omitempty"`
}

// OwnershipRule routes matching issues to an assignee or owning team.
type OwnershipRule struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"projectId"`
	Name        string    `json:"name"`
	Pattern     string    `json:"pattern"`
	Assignee    string    `json:"assignee"`
	TeamSlug    string    `json:"teamSlug,omitempty"`
	NotifyTeam  bool      `json:"notifyTeam,omitempty"`
	DateCreated time.Time `json:"dateCreated"`
	DateUpdated time.Time `json:"dateUpdated"`
}

// OwnershipResolveResult carries both the assignee and any team routing metadata.
type OwnershipResolveResult struct {
	Assignee   string
	TeamSlug   string
	NotifyTeam bool
}

// ReleaseDeploy captures one release deployment marker.
type ReleaseDeploy struct {
	ID             string    `json:"id"`
	ReleaseID      string    `json:"releaseId"`
	ReleaseVersion string    `json:"releaseVersion,omitempty"`
	Environment    string    `json:"environment"`
	Name           string    `json:"name,omitempty"`
	URL            string    `json:"url,omitempty"`
	DateStarted    time.Time `json:"dateStarted,omitempty"`
	DateFinished   time.Time `json:"dateFinished,omitempty"`
	DateCreated    time.Time `json:"dateCreated"`
}

// ReleaseCommit captures one commit associated with a release.
type ReleaseCommit struct {
	ID             string    `json:"id"`
	ReleaseID      string    `json:"releaseId"`
	ReleaseVersion string    `json:"releaseVersion,omitempty"`
	CommitSHA      string    `json:"commitSha"`
	Repository     string    `json:"repository,omitempty"`
	AuthorName     string    `json:"authorName,omitempty"`
	AuthorEmail    string    `json:"authorEmail,omitempty"`
	Message        string    `json:"message,omitempty"`
	Files          []string  `json:"files,omitempty"`
	DateCreated    time.Time `json:"dateCreated"`
}

// ReleaseSuspect links a release commit to a likely affected issue.
type ReleaseSuspect struct {
	GroupID     string    `json:"groupId"`
	ShortID     int       `json:"shortId"`
	Title       string    `json:"title"`
	Culprit     string    `json:"culprit"`
	LastSeen    time.Time `json:"lastSeen"`
	CommitSHA   string    `json:"commitSha"`
	Repository  string    `json:"repository,omitempty"`
	AuthorName  string    `json:"authorName,omitempty"`
	Message     string    `json:"message,omitempty"`
	MatchedFile string    `json:"matchedFile,omitempty"`
	ReleaseID   string    `json:"releaseId,omitempty"`
	Release     string    `json:"release,omitempty"`
}

// ReleaseRegressionSnapshot captures top-line release metrics at one point in a comparison.
type ReleaseRegressionSnapshot struct {
	Version          string    `json:"version"`
	CreatedAt        time.Time `json:"createdAt,omitempty"`
	EventCount       int       `json:"eventCount"`
	SessionCount     int       `json:"sessionCount"`
	ErroredSessions  int       `json:"erroredSessions"`
	CrashedSessions  int       `json:"crashedSessions"`
	AbnormalSessions int       `json:"abnormalSessions"`
	AffectedUsers    int       `json:"affectedUsers"`
	CrashFreeRate    float64   `json:"crashFreeRate"`
	LastSessionAt    time.Time `json:"lastSessionAt,omitempty"`
}

// ReleaseCountDelta captures before/after counts for one metric.
type ReleaseCountDelta struct {
	Current  int `json:"current"`
	Previous int `json:"previous"`
	Delta    int `json:"delta"`
}

// ReleaseRateDelta captures before/after percentage-style metrics.
type ReleaseRateDelta struct {
	Current  float64 `json:"current"`
	Previous float64 `json:"previous"`
	Delta    float64 `json:"delta"`
}

// ReleaseEnvironmentRegression highlights where release-tagged error volume moved.
type ReleaseEnvironmentRegression struct {
	Environment    string `json:"environment"`
	CurrentErrors  int    `json:"currentErrors"`
	PreviousErrors int    `json:"previousErrors"`
	DeltaErrors    int    `json:"deltaErrors"`
}

// ReleaseTransactionRegression highlights where transaction volume and p95 latency moved.
type ReleaseTransactionRegression struct {
	Transaction   string  `json:"transaction"`
	CurrentP95    float64 `json:"currentP95"`
	PreviousP95   float64 `json:"previousP95"`
	DeltaP95      float64 `json:"deltaP95"`
	CurrentCount  int     `json:"currentCount"`
	PreviousCount int     `json:"previousCount"`
	DeltaCount    int     `json:"deltaCount"`
}

// ReleaseDeployImpact captures before/after telemetry around the newest deploy marker.
type ReleaseDeployImpact struct {
	Deploy             ReleaseDeploy `json:"deploy"`
	AnchorAt           time.Time     `json:"anchorAt,omitempty"`
	WindowHours        int           `json:"windowHours"`
	ErrorsBefore       int           `json:"errorsBefore"`
	ErrorsAfter        int           `json:"errorsAfter"`
	ErrorDelta         int           `json:"errorDelta"`
	TransactionsBefore int           `json:"transactionsBefore"`
	TransactionsAfter  int           `json:"transactionsAfter"`
	TransactionDelta   int           `json:"transactionDelta"`
	P95Before          float64       `json:"p95Before"`
	P95After           float64       `json:"p95After"`
	P95Delta           float64       `json:"p95Delta"`
}

// ReleaseRegressionSummary describes what changed between the current release and its baseline.
type ReleaseRegressionSummary struct {
	Current              ReleaseRegressionSnapshot      `json:"current"`
	Previous             *ReleaseRegressionSnapshot     `json:"previous,omitempty"`
	EventDelta           ReleaseCountDelta              `json:"eventDelta"`
	SessionDelta         ReleaseCountDelta              `json:"sessionDelta"`
	CrashFreeDelta       ReleaseRateDelta               `json:"crashFreeDelta"`
	EnvironmentMovements []ReleaseEnvironmentRegression `json:"environmentMovements,omitempty"`
	TransactionMovements []ReleaseTransactionRegression `json:"transactionMovements,omitempty"`
	LatestDeployImpact   *ReleaseDeployImpact           `json:"latestDeployImpact,omitempty"`
}
