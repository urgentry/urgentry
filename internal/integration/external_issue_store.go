package integration

import (
	"context"
	"time"
)

// ExternalIssueLink stores an issue link created through an integration installation.
type ExternalIssueLink struct {
	ID             string    `json:"id"`
	InstallationID string    `json:"installationId"`
	GroupID        string    `json:"groupId"`
	IntegrationID  string    `json:"integrationId"`
	Key            string    `json:"key"`
	Title          string    `json:"title"`
	URL            string    `json:"url"`
	Description    string    `json:"description"`
	CreatedAt      time.Time `json:"createdAt"`
}

// ExternalIssueStore persists issue links created from Sentry App installations.
type ExternalIssueStore interface {
	GetByInstallation(ctx context.Context, installationID, externalIssueID string) (*ExternalIssueLink, error)
	ListByGroup(ctx context.Context, groupID string) ([]*ExternalIssueLink, error)
	Upsert(ctx context.Context, link *ExternalIssueLink) error
	Delete(ctx context.Context, installationID, externalIssueID string) error
}
