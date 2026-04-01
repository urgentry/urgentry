package store

import (
	"context"
	"time"
)

// Workflow represents an automation workflow with triggers, conditions, and actions.
type Workflow struct {
	ID             string    `json:"id"`
	OrgID          string    `json:"orgId"`
	Name           string    `json:"name"`
	TriggersJSON   string    `json:"triggers"`
	ConditionsJSON string    `json:"conditions"`
	ActionsJSON    string    `json:"actions"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"createdAt"`
}

// WorkflowStore persists and retrieves workflow configurations.
type WorkflowStore interface {
	CreateWorkflow(ctx context.Context, w *Workflow) error
	ListWorkflows(ctx context.Context, orgID string) ([]*Workflow, error)
	GetWorkflow(ctx context.Context, id string) (*Workflow, error)
	UpdateWorkflow(ctx context.Context, w *Workflow) error
	DeleteWorkflow(ctx context.Context, id string) error
	BulkUpdateWorkflows(ctx context.Context, orgID string, ids []string, enabled bool) error
	BulkDeleteWorkflows(ctx context.Context, orgID string, ids []string) error
}
