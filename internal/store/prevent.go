package store

import (
	"context"
	"time"
)

// PreventRepository represents one repository connected to a Prevent owner.
type PreventRepository struct {
	ID                string     `json:"id"`
	OrganizationID    string     `json:"organizationId"`
	OrganizationSlug  string     `json:"organizationSlug,omitempty"`
	OwnerSlug         string     `json:"ownerSlug"`
	Name              string     `json:"name"`
	Provider          string     `json:"provider"`
	URL               string     `json:"url,omitempty"`
	ExternalSlug      string     `json:"externalSlug,omitempty"`
	Status            string     `json:"status"`
	DefaultBranch     string     `json:"defaultBranch,omitempty"`
	TestAnalyticsEnabled bool    `json:"testAnalyticsEnabled"`
	SyncStatus        string     `json:"syncStatus"`
	LastSyncedAt      *time.Time `json:"lastSyncedAt,omitempty"`
	LastSyncStartedAt *time.Time `json:"lastSyncStartedAt,omitempty"`
	LastSyncError     string     `json:"lastSyncError,omitempty"`
	DateCreated       time.Time  `json:"dateCreated"`
}

// PreventRepositoryBranch captures one tracked branch for a repository.
type PreventRepositoryBranch struct {
	ID           string     `json:"id"`
	RepositoryID string     `json:"repositoryId"`
	Name         string     `json:"name"`
	IsDefault    bool       `json:"isDefault"`
	Status       string     `json:"status"`
	LastSyncedAt *time.Time `json:"lastSyncedAt,omitempty"`
	DateCreated  time.Time  `json:"dateCreated"`
}

// PreventRepositoryToken captures a repository token record.
type PreventRepositoryToken struct {
	ID           string     `json:"id"`
	RepositoryID string     `json:"repositoryId"`
	Label        string     `json:"label"`
	Token        string     `json:"token"`
	TokenPrefix  string     `json:"tokenPrefix"`
	TokenHash    string     `json:"tokenHash,omitempty"`
	Status       string     `json:"status"`
	RotatedAt    *time.Time `json:"rotatedAt,omitempty"`
	LastUsedAt   *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt    *time.Time `json:"revokedAt,omitempty"`
	DateCreated  time.Time  `json:"dateCreated"`
}

// PreventRepositorySyncStatus is the persisted sync view for a repository.
type PreventRepositorySyncStatus struct {
	RepositoryID      string     `json:"repositoryId"`
	Status            string     `json:"status"`
	LastSyncedAt      *time.Time `json:"lastSyncedAt,omitempty"`
	LastSyncStartedAt *time.Time `json:"lastSyncStartedAt,omitempty"`
	LastSyncError     string     `json:"lastSyncError,omitempty"`
}

// PreventRepositoryTestSuite captures one test suite row.
type PreventRepositoryTestSuite struct {
	ID           string     `json:"id"`
	RepositoryID string     `json:"repositoryId"`
	ExternalID   string     `json:"externalId"`
	Name         string     `json:"name"`
	Status       string     `json:"status"`
	LastRunAt    *time.Time `json:"lastRunAt,omitempty"`
	DateCreated  time.Time  `json:"dateCreated"`
}

// PreventRepositoryTestResult captures one repository test result row.
type PreventRepositoryTestResult struct {
	ID           string    `json:"id"`
	RepositoryID string    `json:"repositoryId"`
	SuiteID      string    `json:"suiteId"`
	SuiteName    string    `json:"suiteName,omitempty"`
	BranchName   string    `json:"branchName"`
	CommitSHA    string    `json:"commitSha,omitempty"`
	Status       string    `json:"status"`
	DurationMS   int64     `json:"durationMs"`
	TestCount    int       `json:"testCount"`
	FailureCount int       `json:"failureCount"`
	SkippedCount int       `json:"skippedCount"`
	DateCreated  time.Time `json:"dateCreated"`
}

// PreventRepositoryTestAggregate captures a rollup of test analytics for one branch.
type PreventRepositoryTestAggregate struct {
	ID            string     `json:"id"`
	RepositoryID  string     `json:"repositoryId"`
	BranchName    string     `json:"branchName"`
	TotalRuns     int        `json:"totalRuns"`
	PassingRuns   int        `json:"passingRuns"`
	FailingRuns   int        `json:"failingRuns"`
	SkippedRuns   int        `json:"skippedRuns"`
	AvgDurationMS int64      `json:"avgDurationMs"`
	LastRunAt     *time.Time `json:"lastRunAt,omitempty"`
	DateCreated   time.Time  `json:"dateCreated"`
}

// PreventStore provides backend-neutral repository-management reads and token rotation.
type PreventStore interface {
	ListRepositories(ctx context.Context, orgSlug, ownerSlug string) ([]PreventRepository, error)
	GetRepository(ctx context.Context, orgSlug, ownerSlug, repositoryName string) (*PreventRepository, error)
	GetOwnerSyncStatus(ctx context.Context, orgSlug, ownerSlug string) (bool, error)
	StartOwnerSync(ctx context.Context, orgSlug, ownerSlug string) (bool, error)
	ListRepositoryBranches(ctx context.Context, orgSlug, ownerSlug, repositoryName string) ([]PreventRepositoryBranch, error)
	ListRepositoryTokens(ctx context.Context, orgSlug, ownerSlug, repositoryName string) ([]PreventRepositoryToken, error)
	RegenerateRepositoryToken(ctx context.Context, orgSlug, ownerSlug, repositoryName, tokenID string) (*PreventRepositoryToken, string, error)
	GetRepositorySyncStatus(ctx context.Context, orgSlug, ownerSlug, repositoryName string) (*PreventRepositorySyncStatus, error)
	ListRepositoryTestSuites(ctx context.Context, orgSlug, ownerSlug, repositoryName string) ([]PreventRepositoryTestSuite, error)
	ListRepositoryTestResults(ctx context.Context, orgSlug, ownerSlug, repositoryName string) ([]PreventRepositoryTestResult, error)
	ListRepositoryTestAggregates(ctx context.Context, orgSlug, ownerSlug, repositoryName string) ([]PreventRepositoryTestAggregate, error)
}
