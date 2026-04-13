package alert

import (
	"context"
	"sync"
	"testing"
	"time"
)

// memoryMetricAlertStore is an in-memory MetricAlertRuleStore for testing.
type memoryMetricAlertStore struct {
	mu    sync.RWMutex
	rules map[string]*MetricAlertRule
}

func newMemoryMetricAlertStore() *memoryMetricAlertStore {
	return &memoryMetricAlertStore{rules: make(map[string]*MetricAlertRule)}
}

func (s *memoryMetricAlertStore) CreateMetricAlertRule(_ context.Context, r *MetricAlertRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules[r.ID] = r
	return nil
}

func (s *memoryMetricAlertStore) GetMetricAlertRule(_ context.Context, id string) (*MetricAlertRule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rules[id], nil
}

func (s *memoryMetricAlertStore) ListMetricAlertRules(_ context.Context, projectID string) ([]*MetricAlertRule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*MetricAlertRule
	for _, r := range s.rules {
		if r.ProjectID == projectID {
			result = append(result, r)
		}
	}
	return result, nil
}

func (s *memoryMetricAlertStore) ListAllActiveMetricAlertRules(_ context.Context) ([]*MetricAlertRule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*MetricAlertRule
	for _, r := range s.rules {
		if r.Status == "active" {
			result = append(result, r)
		}
	}
	return result, nil
}

func (s *memoryMetricAlertStore) UpdateMetricAlertRule(_ context.Context, r *MetricAlertRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules[r.ID] = r
	return nil
}

func (s *memoryMetricAlertStore) DeleteMetricAlertRule(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rules, id)
	return nil
}

// stubMetricQueryEngine returns pre-configured metric values for testing.
type stubMetricQueryEngine struct {
	eventCount       int64
	transactionCount int64
	p95              float64
	failureRate      float64
	customMetric     float64
	err              error
}

func (e *stubMetricQueryEngine) CountEvents(_ context.Context, _ string, _ time.Time, _ string) (int64, error) {
	return e.eventCount, e.err
}

func (e *stubMetricQueryEngine) CountTransactions(_ context.Context, _ string, _ time.Time, _ string) (int64, error) {
	return e.transactionCount, e.err
}

func (e *stubMetricQueryEngine) P95Latency(_ context.Context, _ string, _ time.Time, _ string) (float64, error) {
	return e.p95, e.err
}

func (e *stubMetricQueryEngine) FailureRate(_ context.Context, _ string, _ time.Time, _ string) (float64, error) {
	return e.failureRate, e.err
}

func (e *stubMetricQueryEngine) CustomMetricValue(_ context.Context, _ string, _ string, _ time.Time, _ string) (float64, error) {
	return e.customMetric, e.err
}

func TestMetricAlertEvaluator_ErrorCountAbove(t *testing.T) {
	store := newMemoryMetricAlertStore()
	ctx := context.Background()

	rule := &MetricAlertRule{
		ID:             "ma-1",
		ProjectID:      "proj-1",
		Name:           "High error rate",
		Metric:         MetricErrorCount,
		Threshold:      100,
		ThresholdType:  "above",
		TimeWindowSecs: 300,
		Status:         "active",
		State:          "ok",
		TriggerActions: []string{},
	}
	if err := store.CreateMetricAlertRule(ctx, rule); err != nil {
		t.Fatal(err)
	}

	eval := &MetricAlertEvaluator{
		Store:  store,
		Engine: &stubMetricQueryEngine{eventCount: 150},
	}

	transitions, err := eval.EvaluateAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 1 {
		t.Fatalf("expected 1 transition, got %d", len(transitions))
	}
	if transitions[0].FromState != "ok" || transitions[0].ToState != "triggered" {
		t.Errorf("transition = %s->%s, want ok->triggered", transitions[0].FromState, transitions[0].ToState)
	}
	if transitions[0].Value != 150 {
		t.Errorf("value = %f, want 150", transitions[0].Value)
	}
}

func TestMetricAlertEvaluator_ErrorCountNotBreached(t *testing.T) {
	store := newMemoryMetricAlertStore()
	ctx := context.Background()

	rule := &MetricAlertRule{
		ID:             "ma-2",
		ProjectID:      "proj-1",
		Name:           "High error rate",
		Metric:         MetricErrorCount,
		Threshold:      100,
		ThresholdType:  "above",
		TimeWindowSecs: 300,
		Status:         "active",
		State:          "ok",
		TriggerActions: []string{},
	}
	if err := store.CreateMetricAlertRule(ctx, rule); err != nil {
		t.Fatal(err)
	}

	eval := &MetricAlertEvaluator{
		Store:  store,
		Engine: &stubMetricQueryEngine{eventCount: 50},
	}

	transitions, err := eval.EvaluateAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 0 {
		t.Fatalf("expected 0 transitions, got %d", len(transitions))
	}
}

func TestMetricAlertEvaluator_TriggeredToResolved(t *testing.T) {
	store := newMemoryMetricAlertStore()
	ctx := context.Background()

	rule := &MetricAlertRule{
		ID:             "ma-3",
		ProjectID:      "proj-1",
		Name:           "P95 latency",
		Metric:         MetricP95Latency,
		Threshold:      500,
		ThresholdType:  "above",
		TimeWindowSecs: 600,
		Status:         "active",
		State:          "triggered",
		TriggerActions: []string{},
	}
	if err := store.CreateMetricAlertRule(ctx, rule); err != nil {
		t.Fatal(err)
	}

	eval := &MetricAlertEvaluator{
		Store:  store,
		Engine: &stubMetricQueryEngine{p95: 200},
	}

	transitions, err := eval.EvaluateAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 1 {
		t.Fatalf("expected 1 transition, got %d", len(transitions))
	}
	if transitions[0].FromState != "triggered" || transitions[0].ToState != "resolved" {
		t.Errorf("transition = %s->%s, want triggered->resolved", transitions[0].FromState, transitions[0].ToState)
	}
}

func TestMetricAlertEvaluator_ResolveThreshold(t *testing.T) {
	store := newMemoryMetricAlertStore()
	ctx := context.Background()

	rule := &MetricAlertRule{
		ID:               "ma-4",
		ProjectID:        "proj-1",
		Name:             "Error count with resolve",
		Metric:           MetricErrorCount,
		Threshold:        100,
		ThresholdType:    "above",
		ResolveThreshold: 50,
		TimeWindowSecs:   300,
		Status:           "active",
		State:            "triggered",
		TriggerActions:   []string{},
	}
	if err := store.CreateMetricAlertRule(ctx, rule); err != nil {
		t.Fatal(err)
	}

	// Value is 75: below trigger threshold (100) but above resolve threshold (50).
	// Should NOT resolve.
	eval := &MetricAlertEvaluator{
		Store:  store,
		Engine: &stubMetricQueryEngine{eventCount: 75},
	}

	transitions, err := eval.EvaluateAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 0 {
		t.Fatalf("expected 0 transitions (above resolve threshold), got %d", len(transitions))
	}

	// Value drops below resolve threshold: should resolve.
	eval.Engine = &stubMetricQueryEngine{eventCount: 30}
	transitions, err = eval.EvaluateAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 1 {
		t.Fatalf("expected 1 transition, got %d", len(transitions))
	}
	if transitions[0].ToState != "resolved" {
		t.Errorf("toState = %s, want resolved", transitions[0].ToState)
	}
}

func TestMetricAlertEvaluator_BelowThresholdType(t *testing.T) {
	store := newMemoryMetricAlertStore()
	ctx := context.Background()

	rule := &MetricAlertRule{
		ID:             "ma-5",
		ProjectID:      "proj-1",
		Name:           "Low transaction count",
		Metric:         MetricTransactionCount,
		Threshold:      10,
		ThresholdType:  "below",
		TimeWindowSecs: 300,
		Status:         "active",
		State:          "ok",
		TriggerActions: []string{},
	}
	if err := store.CreateMetricAlertRule(ctx, rule); err != nil {
		t.Fatal(err)
	}

	eval := &MetricAlertEvaluator{
		Store:  store,
		Engine: &stubMetricQueryEngine{transactionCount: 5},
	}

	transitions, err := eval.EvaluateAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 1 {
		t.Fatalf("expected 1 transition, got %d", len(transitions))
	}
	if transitions[0].ToState != "triggered" {
		t.Errorf("toState = %s, want triggered", transitions[0].ToState)
	}
}

func TestMetricAlertEvaluator_FailureRate(t *testing.T) {
	store := newMemoryMetricAlertStore()
	ctx := context.Background()

	rule := &MetricAlertRule{
		ID:             "ma-6",
		ProjectID:      "proj-1",
		Name:           "High failure rate",
		Metric:         MetricFailureRate,
		Threshold:      0.1,
		ThresholdType:  "above",
		TimeWindowSecs: 300,
		Status:         "active",
		State:          "ok",
		TriggerActions: []string{},
	}
	if err := store.CreateMetricAlertRule(ctx, rule); err != nil {
		t.Fatal(err)
	}

	eval := &MetricAlertEvaluator{
		Store:  store,
		Engine: &stubMetricQueryEngine{failureRate: 0.25},
	}

	transitions, err := eval.EvaluateAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 1 {
		t.Fatalf("expected 1 transition, got %d", len(transitions))
	}
	if transitions[0].ToState != "triggered" {
		t.Errorf("toState = %s, want triggered", transitions[0].ToState)
	}
}

func TestMetricAlertEvaluator_DisabledRuleSkipped(t *testing.T) {
	store := newMemoryMetricAlertStore()
	ctx := context.Background()

	rule := &MetricAlertRule{
		ID:             "ma-7",
		ProjectID:      "proj-1",
		Name:           "Disabled rule",
		Metric:         MetricErrorCount,
		Threshold:      10,
		ThresholdType:  "above",
		TimeWindowSecs: 300,
		Status:         "disabled",
		State:          "ok",
		TriggerActions: []string{},
	}
	if err := store.CreateMetricAlertRule(ctx, rule); err != nil {
		t.Fatal(err)
	}

	eval := &MetricAlertEvaluator{
		Store:  store,
		Engine: &stubMetricQueryEngine{eventCount: 999},
	}

	transitions, err := eval.EvaluateAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 0 {
		t.Fatalf("expected 0 transitions for disabled rule, got %d", len(transitions))
	}
}

func TestMetricAlertEvaluator_StaysTriggered(t *testing.T) {
	store := newMemoryMetricAlertStore()
	ctx := context.Background()

	rule := &MetricAlertRule{
		ID:             "ma-8",
		ProjectID:      "proj-1",
		Name:           "Stays triggered",
		Metric:         MetricErrorCount,
		Threshold:      100,
		ThresholdType:  "above",
		TimeWindowSecs: 300,
		Status:         "active",
		State:          "triggered",
		TriggerActions: []string{},
	}
	if err := store.CreateMetricAlertRule(ctx, rule); err != nil {
		t.Fatal(err)
	}

	// Still above threshold: should NOT transition.
	eval := &MetricAlertEvaluator{
		Store:  store,
		Engine: &stubMetricQueryEngine{eventCount: 200},
	}

	transitions, err := eval.EvaluateAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 0 {
		t.Fatalf("expected 0 transitions (still above threshold), got %d", len(transitions))
	}
}

func TestMetricAlertEvaluator_NilDeps(t *testing.T) {
	ctx := context.Background()

	// Nil store and engine should return nil without error.
	eval := &MetricAlertEvaluator{}
	transitions, err := eval.EvaluateAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 0 {
		t.Fatalf("expected 0 transitions, got %d", len(transitions))
	}
}

func TestMetricAlertEvaluator_MultipleRules(t *testing.T) {
	store := newMemoryMetricAlertStore()
	ctx := context.Background()

	// Rule 1: error count above 50 (will trigger with 100)
	if err := store.CreateMetricAlertRule(ctx, &MetricAlertRule{
		ID:             "ma-multi-1",
		ProjectID:      "proj-1",
		Name:           "High errors",
		Metric:         MetricErrorCount,
		Threshold:      50,
		ThresholdType:  "above",
		TimeWindowSecs: 300,
		Status:         "active",
		State:          "ok",
		TriggerActions: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	// Rule 2: failure rate above 0.5 (won't trigger with 0.25)
	if err := store.CreateMetricAlertRule(ctx, &MetricAlertRule{
		ID:             "ma-multi-2",
		ProjectID:      "proj-1",
		Name:           "Failure rate",
		Metric:         MetricFailureRate,
		Threshold:      0.5,
		ThresholdType:  "above",
		TimeWindowSecs: 300,
		Status:         "active",
		State:          "ok",
		TriggerActions: []string{},
	}); err != nil {
		t.Fatal(err)
	}

	eval := &MetricAlertEvaluator{
		Store: store,
		Engine: &stubMetricQueryEngine{
			eventCount:  100,
			failureRate: 0.25,
		},
	}

	transitions, err := eval.EvaluateAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 1 {
		t.Fatalf("expected 1 transition, got %d", len(transitions))
	}
	if transitions[0].Rule.ID != "ma-multi-1" {
		t.Errorf("triggered rule = %s, want ma-multi-1", transitions[0].Rule.ID)
	}
}

func TestThresholdBreached(t *testing.T) {
	tests := []struct {
		name      string
		value     float64
		threshold float64
		kind      string
		want      bool
	}{
		{"above: over", 150, 100, "above", true},
		{"above: under", 50, 100, "above", false},
		{"above: equal", 100, 100, "above", false},
		{"below: under", 5, 10, "below", true},
		{"below: over", 15, 10, "below", false},
		{"below: equal", 10, 10, "below", false},
		{"default is above", 150, 100, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := thresholdBreached(tt.value, tt.threshold, tt.kind)
			if got != tt.want {
				t.Errorf("thresholdBreached(%f, %f, %q) = %v, want %v", tt.value, tt.threshold, tt.kind, got, tt.want)
			}
		})
	}
}

func TestResolveWithExplicitThreshold_BelowType(t *testing.T) {
	rule := &MetricAlertRule{
		Threshold:        10,
		ThresholdType:    "below",
		ResolveThreshold: 15,
	}
	// Value is 20, above resolve threshold of 15 -> should resolve.
	if !shouldResolve(rule, 20, false) {
		t.Error("expected resolve when value (20) >= resolveThreshold (15) for below rule")
	}
	// Value is 12, below resolve threshold of 15 -> should NOT resolve.
	if shouldResolve(rule, 12, true) {
		t.Error("expected no resolve when value (12) < resolveThreshold (15) for below rule")
	}
}
