package postgrescontrol

import (
	"context"
	"testing"

	sharedstore "urgentry/internal/store"
)

func TestOwnershipStore_CreateResolveAndDelete(t *testing.T) {
	db, fx := seedControlFixture(t)
	store := NewOwnershipStore(db)
	ctx := context.Background()

	rule, err := store.CreateRule(ctx, sharedstore.OwnershipRule{
		ProjectID: fx.ProjectID,
		Name:      "checkout path",
		Pattern:   "path:checkout/service.go",
		Assignee:  "payments@team",
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if rule == nil || rule.ID == "" {
		t.Fatalf("unexpected created rule: %+v", rule)
	}

	rules, err := store.ListProjectRules(ctx, fx.ProjectID)
	if err != nil {
		t.Fatalf("ListProjectRules: %v", err)
	}
	if len(rules) != 1 || rules[0].Assignee != "payments@team" {
		t.Fatalf("unexpected rules: %+v", rules)
	}

	assignee, err := store.ResolveAssignee(ctx, fx.ProjectID, "checkout panic", "checkout/service.go in Handle", map[string]string{"env": "prod"})
	if err != nil {
		t.Fatalf("ResolveAssignee: %v", err)
	}
	if assignee != "payments@team" {
		t.Fatalf("ResolveAssignee = %q, want payments@team", assignee)
	}

	if err := store.DeleteRule(ctx, fx.ProjectID, rule.ID); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
	rules, err = store.ListProjectRules(ctx, fx.ProjectID)
	if err != nil {
		t.Fatalf("ListProjectRules after delete: %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules after delete, got %+v", rules)
	}
}
