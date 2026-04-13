package store

import (
	"context"
	"time"
)

// OrgDataForwarder is an organization-level data forwarder configuration.
type OrgDataForwarder struct {
	ID              string    `json:"id"`
	OrgID           string    `json:"orgId"`
	Type            string    `json:"type"` // "webhook", "s3", etc.
	Name            string    `json:"name"`
	URL             string    `json:"url"`
	CredentialsJSON string    `json:"credentials,omitempty"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"createdAt"`
}

// OrgForwarderStore persists and retrieves org-level data forwarder configurations.
type OrgForwarderStore interface {
	CreateOrgForwarder(ctx context.Context, f *OrgDataForwarder) error
	ListOrgForwarders(ctx context.Context, orgID string) ([]*OrgDataForwarder, error)
	UpdateOrgForwarder(ctx context.Context, f *OrgDataForwarder) error
	DeleteOrgForwarder(ctx context.Context, id string) error
}
