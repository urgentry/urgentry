package postgrescontrol

import (
	"context"
	"testing"
	"time"

	"urgentry/internal/alert"
)

func TestAlertStore_CRUD(t *testing.T) {
	db, fx := seedControlFixture(t)
	store := NewAlertStore(db)
	ctx := context.Background()

	rule := &alert.Rule{
		ProjectID: fx.ProjectID,
		Name:      "First Seen Alert",
		Status:    "active",
		RuleType:  "all",
		Conditions: []alert.Condition{
			{ID: alert.ConditionFirstSeen, Name: "First seen"},
		},
		Actions: []alert.Action{
			{ID: "a1", Type: alert.ActionTypeEmail, Target: "dev@example.com"},
		},
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if rule.ID == "" {
		t.Fatal("expected created rule ID")
	}

	got, err := store.GetRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if got == nil || got.Name != rule.Name || len(got.Actions) != 1 {
		t.Fatalf("unexpected rule: %+v", got)
	}

	rules, err := store.ListRules(ctx, fx.ProjectID)
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	rule.Name = "Updated Alert"
	rule.Status = "disabled"
	rule.UpdatedAt = time.Now().UTC()
	if err := store.UpdateRule(ctx, rule); err != nil {
		t.Fatalf("UpdateRule: %v", err)
	}
	got, err = store.GetRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetRule after update: %v", err)
	}
	if got.Name != "Updated Alert" || got.Status != "disabled" {
		t.Fatalf("unexpected updated rule: %+v", got)
	}

	if err := store.DeleteRule(ctx, rule.ID); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
	got, err = store.GetRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetRule after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil rule after delete, got %+v", got)
	}
}
