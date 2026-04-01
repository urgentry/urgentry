package controlplane

import (
	"context"

	sharedstore "urgentry/internal/store"
)

type OwnershipStore interface {
	ListProjectRules(ctx context.Context, projectID string) ([]sharedstore.OwnershipRule, error)
	CreateRule(ctx context.Context, rule sharedstore.OwnershipRule) (*sharedstore.OwnershipRule, error)
	DeleteRule(ctx context.Context, projectID, ruleID string) error
	ResolveAssignee(ctx context.Context, projectID, title, culprit string, tags map[string]string) (string, error)
	ResolveOwnership(ctx context.Context, projectID, title, culprit string, tags map[string]string) (*sharedstore.OwnershipResolveResult, error)
}
