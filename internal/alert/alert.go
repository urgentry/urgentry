// Package alert implements alert rules, conditions, and evaluation primitives
// shared by the SQLite stores, pipeline callbacks, API, and web UI.
package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	ConditionFirstSeen         = "sentry.rules.conditions.first_seen_event.FirstSeenEventCondition"
	ConditionRegression        = "sentry.rules.conditions.regression_event.RegressionEventCondition"
	ConditionEveryEvent        = "sentry.rules.conditions.every_event.EveryEventCondition"
	ConditionSlowTransaction   = "urgentry.rules.conditions.transaction_duration.TransactionDurationCondition"
	ConditionFailedTransaction = "urgentry.rules.conditions.transaction_status.TransactionStatusCondition"
	ConditionMonitorMissed     = "urgentry.rules.conditions.monitor_missed.MonitorMissedCondition"
	ConditionReleaseCrashFree  = "urgentry.rules.conditions.release_crash_free.ReleaseCrashFreeBelowCondition"
	ActionTypeEmail            = "email"
	ActionTypeWebhook          = "webhook"
	ActionTypeSlack            = "slack"
	EventTypeError             = "error"
	EventTypeTransaction       = "transaction"
	EventTypeMonitor           = "monitor"
	EventTypeRelease           = "release"
)

type Rule struct {
	ID         string          `json:"id"`
	ProjectID  string          `json:"projectId"`
	Name       string          `json:"name"`
	RuleType   string          `json:"actionMatch"` // "all", "any", "none"
	Status     string          `json:"status"`      // "active", "disabled"
	Conditions []Condition     `json:"conditions"`
	Actions    []Action        `json:"actions"`
	Config     json.RawMessage `json:"config,omitempty"`
	CreatedAt  time.Time       `json:"dateCreated"`
	UpdatedAt  time.Time       `json:"dateModified"`
}

type Condition struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Value any    `json:"value,omitempty"`
}

type Action struct {
	ID     string `json:"id"`
	Type   string `json:"type"` // "email", "webhook", "slack"
	Target string `json:"targetIdentifier,omitempty"`
	Config any    `json:"config,omitempty"`
}

type ProfileContext struct {
	ProfileID     string `json:"profileId"`
	URL           string `json:"url,omitempty"`
	TraceID       string `json:"traceId,omitempty"`
	Transaction   string `json:"transaction,omitempty"`
	Release       string `json:"release,omitempty"`
	DurationNS    int64  `json:"durationNs,omitempty"`
	SampleCount   int    `json:"sampleCount,omitempty"`
	FunctionCount int    `json:"functionCount,omitempty"`
	TopFunction   string `json:"topFunction,omitempty"`
}

// Signal captures the event context used to evaluate alert rules.
type Signal struct {
	ProjectID     string
	GroupID       string
	EventID       string
	EventType     string
	MonitorSlug   string
	Release       string
	TraceID       string
	Transaction   string
	DurationMS    float64
	CrashFreeRate float64
	SessionCount  int
	AffectedUsers int
	Status        string
	IsNewGroup    bool
	IsRegression  bool
	Profile       *ProfileContext
	Timestamp     time.Time
}

// TriggerEvent is emitted when an alert rule fires.
type TriggerEvent struct {
	RuleID        string
	GroupID       string
	EventID       string
	EventType     string
	MonitorSlug   string
	Release       string
	TraceID       string
	Transaction   string
	DurationMS    float64
	CrashFreeRate float64
	SessionCount  int
	AffectedUsers int
	Status        string
	Profile       *ProfileContext
	Timestamp     time.Time
}

type RuleStore interface {
	CreateRule(ctx context.Context, r *Rule) error
	GetRule(ctx context.Context, id string) (*Rule, error)
	ListRules(ctx context.Context, projectID string) ([]*Rule, error)
	UpdateRule(ctx context.Context, r *Rule) error
	DeleteRule(ctx context.Context, id string) error
}

// MetricAlertRule triggers on aggregate metric conditions (e.g. error_count
// above threshold within a time window).
type MetricAlertRule struct {
	ID               string     `json:"id"`
	ProjectID        string     `json:"projectId"`
	Name             string     `json:"name"`
	Metric           string     `json:"metric"`                     // "error_count", "transaction_count", "p95_latency", "failure_rate", "apdex", "custom_metric"
	CustomMetricName string     `json:"customMetricName,omitempty"` // bucket name when Metric == "custom_metric"
	Threshold        float64    `json:"threshold"`                  // trigger threshold
	ThresholdType    string     `json:"thresholdType"`     // "above", "below"
	TimeWindowSecs   int        `json:"timeWindowSecs"`    // evaluation window: 60, 300, 600, 900, 1800, 3600
	ResolveThreshold float64    `json:"resolveThreshold"`  // auto-resolve when metric crosses this (0 = no auto-resolve)
	Environment      string     `json:"environment"`       // filter by environment ("" = all)
	Status           string     `json:"status"`            // "active", "disabled"
	TriggerActions   []string   `json:"triggerActions"`    // JSON array of action configs (email, webhook, slack URLs)
	State            string     `json:"state"`             // "ok", "triggered", "resolved"
	LastTriggeredAt  *time.Time `json:"lastTriggeredAt"`   // last time this rule fired
	CreatedAt        time.Time  `json:"dateCreated"`
	UpdatedAt        time.Time  `json:"dateModified"`
}

// MetricAlertRuleStore persists metric alert rules.
type MetricAlertRuleStore interface {
	CreateMetricAlertRule(ctx context.Context, r *MetricAlertRule) error
	GetMetricAlertRule(ctx context.Context, id string) (*MetricAlertRule, error)
	ListMetricAlertRules(ctx context.Context, projectID string) ([]*MetricAlertRule, error)
	ListAllActiveMetricAlertRules(ctx context.Context) ([]*MetricAlertRule, error)
	UpdateMetricAlertRule(ctx context.Context, r *MetricAlertRule) error
	DeleteMetricAlertRule(ctx context.Context, id string) error
}

type MemoryRuleStore struct {
	mu    sync.RWMutex
	rules map[string]*Rule
}

func NewMemoryRuleStore() *MemoryRuleStore {
	return &MemoryRuleStore{rules: make(map[string]*Rule)}
}

func (s *MemoryRuleStore) CreateRule(_ context.Context, r *Rule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules[r.ID] = r
	return nil
}

func (s *MemoryRuleStore) GetRule(_ context.Context, id string) (*Rule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rules[id]
	if !ok {
		return nil, nil
	}
	return r, nil
}

func (s *MemoryRuleStore) ListRules(_ context.Context, projectID string) ([]*Rule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Rule
	for _, r := range s.rules {
		if r.ProjectID == projectID {
			result = append(result, r)
		}
	}
	return result, nil
}

func (s *MemoryRuleStore) UpdateRule(_ context.Context, r *Rule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules[r.ID] = r
	return nil
}

func (s *MemoryRuleStore) DeleteRule(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rules, id)
	return nil
}

// Evaluator checks alert rules against events.
type Evaluator struct {
	Rules RuleStore
}

// Evaluate checks all active rules for a project against a new group event.
// It checks both project-specific rules and global default rules (__default__).
func (e *Evaluator) Evaluate(ctx context.Context, projectID, groupID, eventID string, isNewGroup, isRegression bool) ([]TriggerEvent, error) {
	return e.EvaluateSignal(ctx, Signal{
		ProjectID:    projectID,
		GroupID:      groupID,
		EventID:      eventID,
		EventType:    EventTypeError,
		IsNewGroup:   isNewGroup,
		IsRegression: isRegression,
		Timestamp:    time.Now().UTC(),
	})
}

// EvaluateSignal checks all active rules for a concrete event signal. It
// supports both issue alerts and performance alerts.
func (e *Evaluator) EvaluateSignal(ctx context.Context, signal Signal) ([]TriggerEvent, error) {
	rules, err := e.Rules.ListRules(ctx, signal.ProjectID)
	if err != nil {
		return nil, err
	}

	// Also include global default rules.
	if signal.ProjectID != "__default__" {
		defaults, err := e.Rules.ListRules(ctx, "__default__")
		if err == nil {
			rules = append(rules, defaults...)
		}
	}

	var triggers []TriggerEvent
	for _, r := range rules {
		if r.Status != "active" {
			continue
		}
		if shouldTrigger(r, signal) {
			timestamp := signal.Timestamp
			if timestamp.IsZero() {
				timestamp = time.Now().UTC()
			}
			triggers = append(triggers, TriggerEvent{
				RuleID:        r.ID,
				GroupID:       signal.GroupID,
				EventID:       signal.EventID,
				EventType:     signal.EventType,
				MonitorSlug:   signal.MonitorSlug,
				Release:       signal.Release,
				TraceID:       signal.TraceID,
				Transaction:   signal.Transaction,
				DurationMS:    signal.DurationMS,
				CrashFreeRate: signal.CrashFreeRate,
				SessionCount:  signal.SessionCount,
				AffectedUsers: signal.AffectedUsers,
				Status:        signal.Status,
				Profile:       cloneProfileContext(signal.Profile),
				Timestamp:     timestamp,
			})
		}
	}
	return triggers, nil
}

func cloneProfileContext(item *ProfileContext) *ProfileContext {
	if item == nil {
		return nil
	}
	copy := *item
	return &copy
}

func shouldTrigger(r *Rule, signal Signal) bool {
	for _, c := range r.Conditions {
		switch c.ID {
		case ConditionFirstSeen:
			if signal.EventType == EventTypeError && signal.IsNewGroup {
				return true
			}
		case ConditionRegression:
			if signal.EventType == EventTypeError && signal.IsRegression {
				return true
			}
		case ConditionEveryEvent:
			if signal.EventType == EventTypeError {
				return true
			}
		case ConditionSlowTransaction:
			threshold, ok := conditionThresholdMS(c)
			if ok && signal.EventType == EventTypeTransaction && signal.DurationMS >= threshold {
				return true
			}
		case ConditionFailedTransaction:
			status := strings.TrimSpace(strings.ToLower(signal.Status))
			if signal.EventType == EventTypeTransaction && status != "" && status != "ok" {
				return true
			}
		case ConditionMonitorMissed:
			status := strings.TrimSpace(strings.ToLower(signal.Status))
			if signal.EventType == EventTypeMonitor && status == "missed" {
				return true
			}
		case ConditionReleaseCrashFree:
			threshold, ok := conditionThresholdMS(c)
			if ok && signal.EventType == EventTypeRelease && signal.CrashFreeRate > 0 && signal.CrashFreeRate < threshold {
				return true
			}
		}
	}
	return false
}

func conditionThresholdMS(condition Condition) (float64, bool) {
	switch value := condition.Value.(type) {
	case float64:
		return value, value > 0
	case int:
		return float64(value), value > 0
	case int64:
		return float64(value), value > 0
	case json.Number:
		parsed, err := value.Float64()
		return parsed, err == nil && parsed > 0
	case string:
		parsed := json.Number(value)
		floatValue, err := parsed.Float64()
		return floatValue, err == nil && floatValue > 0
	case map[string]any:
		raw, ok := value["threshold_ms"]
		if !ok {
			return 0, false
		}
		return conditionThresholdMS(Condition{Value: raw})
	default:
		return 0, false
	}
}

func SlowTransactionCondition(thresholdMS float64) Condition {
	return Condition{
		ID:    ConditionSlowTransaction,
		Name:  fmt.Sprintf("Slow transaction >= %.0fms", thresholdMS),
		Value: thresholdMS,
	}
}

func FailedTransactionCondition() Condition {
	return Condition{
		ID:   ConditionFailedTransaction,
		Name: "Failed transaction",
	}
}

func MonitorMissedCondition() Condition {
	return Condition{
		ID:   ConditionMonitorMissed,
		Name: "Monitor missed check-in",
	}
}

func ReleaseCrashFreeBelowCondition(threshold float64) Condition {
	return Condition{
		ID:    ConditionReleaseCrashFree,
		Name:  fmt.Sprintf("Crash-free rate below %.0f%%", threshold),
		Value: threshold,
	}
}
