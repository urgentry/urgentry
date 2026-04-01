package alert

import (
	"context"
	"testing"
	"time"
)

func TestEvaluate_FirstSeenCondition(t *testing.T) {
	store := NewMemoryRuleStore()
	ctx := context.Background()

	rule := &Rule{
		ID:        "rule-1",
		ProjectID: "proj-1",
		Name:      "First Seen Alert",
		Status:    "active",
		Conditions: []Condition{
			{ID: ConditionFirstSeen, Name: "First seen"},
		},
		Actions: []Action{
			{ID: "a1", Type: ActionTypeEmail},
		},
	}
	if err := store.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	eval := &Evaluator{Rules: store}

	// New group -> should trigger.
	triggers, err := eval.Evaluate(ctx, "proj-1", "grp-1", "evt-1", true, false)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(triggers) != 1 {
		t.Fatalf("expected 1 trigger, got %d", len(triggers))
	}
	if triggers[0].RuleID != "rule-1" {
		t.Errorf("RuleID = %q, want 'rule-1'", triggers[0].RuleID)
	}
	if triggers[0].GroupID != "grp-1" {
		t.Errorf("GroupID = %q, want 'grp-1'", triggers[0].GroupID)
	}
}

func TestEvaluate_FirstSeenCondition_NotNew(t *testing.T) {
	store := NewMemoryRuleStore()
	ctx := context.Background()

	rule := &Rule{
		ID:        "rule-2",
		ProjectID: "proj-1",
		Name:      "First Seen Alert",
		Status:    "active",
		Conditions: []Condition{
			{ID: ConditionFirstSeen, Name: "First seen"},
		},
	}
	if err := store.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	eval := &Evaluator{Rules: store}

	// Not a new group -> should NOT trigger.
	triggers, err := eval.Evaluate(ctx, "proj-1", "grp-1", "evt-1", false, false)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(triggers) != 0 {
		t.Fatalf("expected 0 triggers, got %d", len(triggers))
	}
}

func TestEvaluate_RegressionCondition(t *testing.T) {
	store := NewMemoryRuleStore()
	ctx := context.Background()

	rule := &Rule{
		ID:        "rule-3",
		ProjectID: "proj-1",
		Name:      "Regression Alert",
		Status:    "active",
		Conditions: []Condition{
			{ID: ConditionRegression, Name: "Regression"},
		},
	}
	if err := store.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	eval := &Evaluator{Rules: store}

	// Regression -> should trigger.
	triggers, err := eval.Evaluate(ctx, "proj-1", "grp-1", "evt-1", false, true)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(triggers) != 1 {
		t.Fatalf("expected 1 trigger, got %d", len(triggers))
	}
}

func TestEvaluate_EveryEventCondition(t *testing.T) {
	store := NewMemoryRuleStore()
	ctx := context.Background()

	rule := &Rule{
		ID:        "rule-4",
		ProjectID: "proj-1",
		Name:      "Every Event Alert",
		Status:    "active",
		Conditions: []Condition{
			{ID: ConditionEveryEvent, Name: "Every event"},
		},
	}
	if err := store.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	eval := &Evaluator{Rules: store}

	// Every event should always trigger regardless of isNew/isRegression.
	triggers, err := eval.Evaluate(ctx, "proj-1", "grp-1", "evt-1", false, false)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(triggers) != 1 {
		t.Fatalf("expected 1 trigger, got %d", len(triggers))
	}
}

func TestEvaluate_DisabledRule(t *testing.T) {
	store := NewMemoryRuleStore()
	ctx := context.Background()

	rule := &Rule{
		ID:        "rule-5",
		ProjectID: "proj-1",
		Name:      "Disabled Alert",
		Status:    "disabled",
		Conditions: []Condition{
			{ID: ConditionEveryEvent, Name: "Every event"},
		},
	}
	if err := store.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	eval := &Evaluator{Rules: store}

	// Disabled rule should not trigger even with EveryEvent condition.
	triggers, err := eval.Evaluate(ctx, "proj-1", "grp-1", "evt-1", true, true)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(triggers) != 0 {
		t.Fatalf("expected 0 triggers for disabled rule, got %d", len(triggers))
	}
}

func TestEvaluate_NoRules(t *testing.T) {
	store := NewMemoryRuleStore()
	ctx := context.Background()

	eval := &Evaluator{Rules: store}

	// Empty project -> no triggers.
	triggers, err := eval.Evaluate(ctx, "proj-empty", "grp-1", "evt-1", true, true)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(triggers) != 0 {
		t.Fatalf("expected 0 triggers for empty project, got %d", len(triggers))
	}
}

// ---------------------------------------------------------------------------
// Table-driven test combining all scenarios
// ---------------------------------------------------------------------------

func TestEvaluate_TableDriven(t *testing.T) {
	tests := []struct {
		name         string
		conditionID  string
		ruleStatus   string
		isNewGroup   bool
		isRegression bool
		wantTrigger  bool
	}{
		{"FirstSeen+New", ConditionFirstSeen, "active", true, false, true},
		{"FirstSeen+NotNew", ConditionFirstSeen, "active", false, false, false},
		{"Regression+Yes", ConditionRegression, "active", false, true, true},
		{"Regression+No", ConditionRegression, "active", false, false, false},
		{"EveryEvent", ConditionEveryEvent, "active", false, false, true},
		{"Disabled+EveryEvent", ConditionEveryEvent, "disabled", true, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryRuleStore()
			ctx := context.Background()

			rule := &Rule{
				ID:        "rule-table",
				ProjectID: "proj-table",
				Name:      tt.name,
				Status:    tt.ruleStatus,
				Conditions: []Condition{
					{ID: tt.conditionID},
				},
			}
			if err := store.CreateRule(ctx, rule); err != nil {
				t.Fatalf("CreateRule: %v", err)
			}

			eval := &Evaluator{Rules: store}
			triggers, err := eval.Evaluate(ctx, "proj-table", "grp-1", "evt-1", tt.isNewGroup, tt.isRegression)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}

			gotTrigger := len(triggers) > 0
			if gotTrigger != tt.wantTrigger {
				t.Errorf("trigger = %v, want %v", gotTrigger, tt.wantTrigger)
			}
		})
	}
}

func TestEvaluateSignal_SlowTransaction(t *testing.T) {
	store := NewMemoryRuleStore()
	ctx := context.Background()

	rule := &Rule{
		ID:         "rule-slow",
		ProjectID:  "proj-1",
		Name:       "Slow checkout",
		Status:     "active",
		Conditions: []Condition{SlowTransactionCondition(750)},
		Actions:    []Action{{Type: ActionTypeSlack}},
	}
	if err := store.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	eval := &Evaluator{Rules: store}
	triggers, err := eval.EvaluateSignal(ctx, Signal{
		ProjectID:   "proj-1",
		EventID:     "txn-1",
		EventType:   EventTypeTransaction,
		TraceID:     "trace-1",
		Transaction: "GET /checkout",
		DurationMS:  812,
	})
	if err != nil {
		t.Fatalf("EvaluateSignal: %v", err)
	}
	if len(triggers) != 1 {
		t.Fatalf("expected 1 trigger, got %d", len(triggers))
	}
	if triggers[0].EventType != EventTypeTransaction || triggers[0].TraceID != "trace-1" {
		t.Fatalf("unexpected trigger: %+v", triggers[0])
	}
}

func TestEvaluateSignal_EveryEventDoesNotMatchTransactions(t *testing.T) {
	store := NewMemoryRuleStore()
	ctx := context.Background()

	rule := &Rule{
		ID:         "rule-every",
		ProjectID:  "proj-1",
		Name:       "Every event",
		Status:     "active",
		Conditions: []Condition{{ID: ConditionEveryEvent}},
	}
	if err := store.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	eval := &Evaluator{Rules: store}
	triggers, err := eval.EvaluateSignal(ctx, Signal{
		ProjectID:  "proj-1",
		EventID:    "txn-1",
		EventType:  EventTypeTransaction,
		DurationMS: 1200,
	})
	if err != nil {
		t.Fatalf("EvaluateSignal: %v", err)
	}
	if len(triggers) != 0 {
		t.Fatalf("expected 0 triggers, got %d", len(triggers))
	}
}

func TestEvaluateSignal_SlowTransactionCondition(t *testing.T) {
	store := NewMemoryRuleStore()
	ctx := context.Background()

	rule := &Rule{
		ID:         "rule-slow",
		ProjectID:  "proj-1",
		Name:       "Slow transaction",
		Status:     "active",
		Conditions: []Condition{SlowTransactionCondition(750)},
	}
	if err := store.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	eval := &Evaluator{Rules: store}
	triggers, err := eval.EvaluateSignal(ctx, Signal{
		ProjectID:   "proj-1",
		EventID:     "txn-1",
		EventType:   EventTypeTransaction,
		TraceID:     "trace-1",
		Transaction: "GET /checkout",
		DurationMS:  1200,
		Timestamp:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EvaluateSignal: %v", err)
	}
	if len(triggers) != 1 {
		t.Fatalf("expected 1 trigger, got %d", len(triggers))
	}
	if triggers[0].Transaction != "GET /checkout" || triggers[0].TraceID != "trace-1" {
		t.Fatalf("unexpected trigger: %+v", triggers[0])
	}
}

func TestEvaluateSignal_FailedTransactionCondition(t *testing.T) {
	store := NewMemoryRuleStore()
	ctx := context.Background()

	rule := &Rule{
		ID:         "rule-failed",
		ProjectID:  "proj-1",
		Name:       "Failed transaction",
		Status:     "active",
		Conditions: []Condition{FailedTransactionCondition()},
	}
	if err := store.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	eval := &Evaluator{Rules: store}
	triggers, err := eval.EvaluateSignal(ctx, Signal{
		ProjectID:  "proj-1",
		EventID:    "txn-2",
		EventType:  EventTypeTransaction,
		Status:     "internal_error",
		DurationMS: 25,
		Timestamp:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EvaluateSignal: %v", err)
	}
	if len(triggers) != 1 {
		t.Fatalf("expected 1 trigger, got %d", len(triggers))
	}
}

func TestEvaluateSignal_MonitorMissedCondition(t *testing.T) {
	store := NewMemoryRuleStore()
	ctx := context.Background()

	rule := &Rule{
		ID:         "rule-monitor",
		ProjectID:  "proj-1",
		Name:       "Missed monitor",
		Status:     "active",
		Conditions: []Condition{MonitorMissedCondition()},
	}
	if err := store.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	eval := &Evaluator{Rules: store}
	triggers, err := eval.EvaluateSignal(ctx, Signal{
		ProjectID:   "proj-1",
		EventID:     "check-in-1",
		EventType:   EventTypeMonitor,
		MonitorSlug: "nightly-import",
		Status:      "missed",
		Timestamp:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EvaluateSignal: %v", err)
	}
	if len(triggers) != 1 {
		t.Fatalf("expected 1 trigger, got %d", len(triggers))
	}
	if triggers[0].MonitorSlug != "nightly-import" || triggers[0].EventType != EventTypeMonitor {
		t.Fatalf("unexpected trigger: %+v", triggers[0])
	}
}

func TestEvaluateSignal_ReleaseCrashFreeBelowCondition(t *testing.T) {
	store := NewMemoryRuleStore()
	ctx := context.Background()

	rule := &Rule{
		ID:         "rule-release",
		ProjectID:  "proj-1",
		Name:       "Release health",
		Status:     "active",
		Conditions: []Condition{ReleaseCrashFreeBelowCondition(95)},
	}
	if err := store.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	eval := &Evaluator{Rules: store}
	triggers, err := eval.EvaluateSignal(ctx, Signal{
		ProjectID:     "proj-1",
		EventID:       "ios@1.2.3",
		EventType:     EventTypeRelease,
		Release:       "ios@1.2.3",
		CrashFreeRate: 92.5,
		SessionCount:  200,
		AffectedUsers: 50,
		Timestamp:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EvaluateSignal: %v", err)
	}
	if len(triggers) != 1 {
		t.Fatalf("expected 1 trigger, got %d", len(triggers))
	}
	if triggers[0].Release != "ios@1.2.3" || triggers[0].CrashFreeRate != 92.5 {
		t.Fatalf("unexpected trigger: %+v", triggers[0])
	}
}
