package store

import (
	"context"
	"time"
)

// ExternalTeam maps an internal team to an external IdP team identity.
type ExternalTeam struct {
	ID           string    `json:"id"`
	OrgID        string    `json:"orgId"`
	TeamSlug     string    `json:"teamSlug"`
	Provider     string    `json:"provider"` // "github", "okta", etc.
	ExternalID   string    `json:"externalId"`
	ExternalName string    `json:"externalName"`
	CreatedAt    time.Time `json:"createdAt"`
}

// ExternalTeamStore persists and retrieves external team mappings.
type ExternalTeamStore interface {
	CreateExternalTeam(ctx context.Context, t *ExternalTeam) error
	UpdateExternalTeam(ctx context.Context, t *ExternalTeam) error
	DeleteExternalTeam(ctx context.Context, id string) error
	ListExternalTeams(ctx context.Context, orgID string) ([]*ExternalTeam, error)
}
