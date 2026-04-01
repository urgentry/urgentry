package store

import (
	"context"
	"time"
)

// ExternalUser maps a Sentry user to an external identity (e.g. GitHub, Slack).
type ExternalUser struct {
	ID           string    `json:"id"`
	OrgID        string    `json:"orgId"`
	UserID       string    `json:"userId"`
	Provider     string    `json:"provider"` // "github", "slack", etc.
	ExternalID   string    `json:"externalId"`
	ExternalName string    `json:"externalName"`
	CreatedAt    time.Time `json:"createdAt"`
}

// ExternalUserStore persists and retrieves external user mappings.
type ExternalUserStore interface {
	CreateExternalUser(ctx context.Context, u *ExternalUser) error
	UpdateExternalUser(ctx context.Context, u *ExternalUser) error
	DeleteExternalUser(ctx context.Context, id string) error
	ListExternalUsers(ctx context.Context, orgID string) ([]*ExternalUser, error)
}
