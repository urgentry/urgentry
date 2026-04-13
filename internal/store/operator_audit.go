package store

import (
	"context"
	"time"
)

type OperatorAuditRecord struct {
	OrganizationID string
	ProjectID      string
	Action         string
	Status         string
	Source         string
	Actor          string
	Detail         string
	MetadataJSON   string
}

type OperatorAuditEntry struct {
	ID               string    `json:"id"`
	OrganizationID   string    `json:"organizationId,omitempty"`
	OrganizationSlug string    `json:"organizationSlug,omitempty"`
	ProjectID        string    `json:"projectId,omitempty"`
	ProjectSlug      string    `json:"projectSlug,omitempty"`
	Action           string    `json:"action"`
	Status           string    `json:"status"`
	Source           string    `json:"source,omitempty"`
	Actor            string    `json:"actor,omitempty"`
	Detail           string    `json:"detail,omitempty"`
	MetadataJSON     string    `json:"metadataJson,omitempty"`
	DateCreated      time.Time `json:"dateCreated"`
}

type OperatorAuditStore interface {
	Record(ctx context.Context, record OperatorAuditRecord) error
	List(ctx context.Context, orgSlug string, limit int) ([]OperatorAuditEntry, error)
}
