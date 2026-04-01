package notify

import (
	"context"
	"strings"
	"time"

	"urgentry/internal/alert"
)

// RoutingConditionField identifies what a routing condition matches against.
type RoutingConditionField string

const (
	ConditionFieldSeverity    RoutingConditionField = "severity"
	ConditionFieldEnvironment RoutingConditionField = "environment"
	ConditionFieldProject     RoutingConditionField = "project"
	ConditionFieldEventType   RoutingConditionField = "event_type"
)

// RoutingAction specifies what to do when a routing rule matches.
type RoutingAction struct {
	Kind   string `json:"kind"`   // "email", "webhook", "slack", "suppress"
	Target string `json:"target"` // recipient email, webhook URL, slack URL, or ""
}

// RoutingCondition is a single predicate within a routing rule.
type RoutingCondition struct {
	Field    RoutingConditionField `json:"field"`
	Operator string                `json:"operator"` // "equals", "not_equals", "contains"
	Value    string                `json:"value"`
}

// NotificationRoutingRule defines a rule that routes alert notifications
// based on conditions such as severity, environment, and project.
type NotificationRoutingRule struct {
	ID             string             `json:"id"`
	OrganizationID string             `json:"organizationId"`
	Name           string             `json:"name"`
	Priority       int                `json:"priority"` // lower = higher priority; evaluated in order
	Conditions     []RoutingCondition `json:"conditions"`
	Actions        []RoutingAction    `json:"actions"`
	Enabled        bool               `json:"enabled"`
	CreatedAt      time.Time          `json:"createdAt"`
	UpdatedAt      time.Time          `json:"updatedAt"`
}

// RoutingContext carries the fields the routing engine matches against.
type RoutingContext struct {
	ProjectID   string
	Environment string
	Severity    string // "fatal", "error", "warning", "info"
	EventType   string // alert.EventType* constants
}

// RoutingRuleStore persists notification routing rules.
type RoutingRuleStore interface {
	CreateRoutingRule(ctx context.Context, rule *NotificationRoutingRule) error
	GetRoutingRule(ctx context.Context, id string) (*NotificationRoutingRule, error)
	ListRoutingRules(ctx context.Context, orgID string) ([]*NotificationRoutingRule, error)
	UpdateRoutingRule(ctx context.Context, rule *NotificationRoutingRule) error
	DeleteRoutingRule(ctx context.Context, id string) error
}

// RoutingEngine evaluates routing rules against alert trigger events and
// returns the matching actions. When no routing rules match, it returns nil
// (meaning the caller should fall through to default delivery).
type RoutingEngine struct {
	Store RoutingRuleStore
}

// NewRoutingEngine creates a routing engine backed by the given store.
func NewRoutingEngine(store RoutingRuleStore) *RoutingEngine {
	return &RoutingEngine{Store: store}
}

// Evaluate returns the actions from the first matching enabled routing rule.
// Rules are evaluated in priority order (lowest priority number first).
// Returns nil actions and nil error when no rule matches.
func (e *RoutingEngine) Evaluate(ctx context.Context, orgID string, rctx RoutingContext) ([]RoutingAction, error) {
	rules, err := e.Store.ListRoutingRules(ctx, orgID)
	if err != nil {
		return nil, err
	}

	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if matchesAllConditions(rule.Conditions, rctx) {
			return rule.Actions, nil
		}
	}
	return nil, nil
}

// EvaluateAll returns the merged actions from all matching enabled routing
// rules. Useful when multiple routing rules should fire for the same event.
func (e *RoutingEngine) EvaluateAll(ctx context.Context, orgID string, rctx RoutingContext) ([]RoutingAction, error) {
	rules, err := e.Store.ListRoutingRules(ctx, orgID)
	if err != nil {
		return nil, err
	}

	var actions []RoutingAction
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if matchesAllConditions(rule.Conditions, rctx) {
			actions = append(actions, rule.Actions...)
		}
	}
	return actions, nil
}

// RouteAlert applies routing rules to an alert trigger event, returning
// delivery actions. If no routing rules match, it returns the alert rule's
// own actions as a fallback.
func (e *RoutingEngine) RouteAlert(ctx context.Context, orgID string, trigger alert.TriggerEvent, rctx RoutingContext, fallbackActions []alert.Action) ([]RoutingAction, error) {
	actions, err := e.Evaluate(ctx, orgID, rctx)
	if err != nil {
		return nil, err
	}
	if len(actions) > 0 {
		return actions, nil
	}

	// Fall back to the alert rule's own actions.
	var result []RoutingAction
	for _, a := range fallbackActions {
		result = append(result, RoutingAction{
			Kind:   a.Type,
			Target: a.Target,
		})
	}
	return result, nil
}

// matchesAllConditions returns true when every condition in the slice matches
// the routing context. An empty condition list matches everything.
func matchesAllConditions(conditions []RoutingCondition, rctx RoutingContext) bool {
	for _, c := range conditions {
		if !matchCondition(c, rctx) {
			return false
		}
	}
	return true
}

func matchCondition(c RoutingCondition, rctx RoutingContext) bool {
	actual := fieldValue(c.Field, rctx)
	expected := strings.TrimSpace(strings.ToLower(c.Value))
	actual = strings.TrimSpace(strings.ToLower(actual))

	switch strings.ToLower(c.Operator) {
	case "equals", "eq", "":
		return actual == expected
	case "not_equals", "neq":
		return actual != expected
	case "contains":
		return strings.Contains(actual, expected)
	default:
		return false
	}
}

func fieldValue(field RoutingConditionField, rctx RoutingContext) string {
	switch field {
	case ConditionFieldSeverity:
		return rctx.Severity
	case ConditionFieldEnvironment:
		return rctx.Environment
	case ConditionFieldProject:
		return rctx.ProjectID
	case ConditionFieldEventType:
		return rctx.EventType
	default:
		return ""
	}
}

// IsSuppressed returns true when any of the given actions is a "suppress"
// action, indicating the notification should be silenced.
func IsSuppressed(actions []RoutingAction) bool {
	for _, a := range actions {
		if strings.ToLower(a.Kind) == "suppress" {
			return true
		}
	}
	return false
}
