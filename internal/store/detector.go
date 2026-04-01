package store

import (
	"context"
	"time"
)

// Detector represents a next-gen alerting detector (metric, issue, or uptime).
type Detector struct {
	ID         string    `json:"id"`
	OrgID      string    `json:"orgId"`
	Name       string    `json:"name"`
	Type       string    `json:"type"` // "metric", "issue", "uptime"
	ConfigJSON string    `json:"config"`
	State      string    `json:"state"` // "active", "disabled"
	OwnerID    string    `json:"ownerId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// DetectorStore persists and retrieves detector configurations.
type DetectorStore interface {
	CreateDetector(ctx context.Context, d *Detector) error
	ListDetectors(ctx context.Context, orgID string) ([]*Detector, error)
	GetDetector(ctx context.Context, id string) (*Detector, error)
	UpdateDetector(ctx context.Context, d *Detector) error
	DeleteDetector(ctx context.Context, id string) error
	BulkUpdateDetectors(ctx context.Context, orgID string, ids []string, state string) error
	BulkDeleteDetectors(ctx context.Context, orgID string, ids []string) error
}
