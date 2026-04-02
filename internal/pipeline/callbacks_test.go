package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"urgentry/internal/alert"
	"urgentry/internal/issue"
	"urgentry/internal/notify"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	profilefixtures "urgentry/internal/testfixtures/profiles"
)

func TestNewAlertCallback_RecordsTinyModeEmail(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()

	alertStore := sqlite.NewAlertStore(db)
	historyStore := sqlite.NewAlertHistoryStore(db)
	outboxStore := sqlite.NewNotificationOutboxStore(db)
	deliveryStore := sqlite.NewNotificationDeliveryStore(db)
	notifier := notify.NewNotifier(outboxStore, deliveryStore)
	evaluator := &alert.Evaluator{Rules: alertStore}

	rule := &alert.Rule{
		ProjectID: "proj-1",
		Name:      "First Seen",
		Status:    "active",
		Conditions: []alert.Condition{{
			ID:   "sentry.rules.conditions.first_seen_event.FirstSeenEventCondition",
			Name: "First seen",
		}},
		Actions:   []alert.Action{{Type: "email", Target: "dev@example.com"}},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := alertStore.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	cb := NewAlertCallback(AlertDeps{
		Evaluator:    evaluator,
		Notifier:     notifier,
		HistoryStore: historyStore,
		AlertStore:   alertStore,
	})

	cb(ctx, "proj-1", issue.ProcessResult{
		EventID:    "evt-1",
		GroupID:    "grp-1",
		IsNewGroup: true,
		EventType:  alert.EventTypeError,
	})

	history, err := historyStore.ListRecent(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecent history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}

	emails, err := outboxStore.ListRecent(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecent outbox: %v", err)
	}
	if len(emails) != 1 {
		t.Fatalf("expected 1 email record, got %d", len(emails))
	}
	if emails[0].Recipient != "dev@example.com" || emails[0].ProjectID != "proj-1" {
		t.Fatalf("unexpected email record: %+v", emails[0])
	}
}

func TestNewAlertCallback_RecordsSlackDeliveryForSlowTransaction(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()

	alertStore := sqlite.NewAlertStore(db)
	historyStore := sqlite.NewAlertHistoryStore(db)
	deliveryStore := sqlite.NewNotificationDeliveryStore(db)
	profileStore := sqlite.NewProfileStore(db, store.NewMemoryBlobStore())
	notifier := notify.NewNotifier(nil, deliveryStore)
	notifier.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     make(http.Header),
		}, nil
	})}
	evaluator := &alert.Evaluator{Rules: alertStore}

	rule := &alert.Rule{
		ProjectID: "proj-1",
		Name:      "Slow checkout",
		Status:    "active",
		Conditions: []alert.Condition{
			alert.SlowTransactionCondition(750),
		},
		Actions:   []alert.Action{{Type: alert.ActionTypeSlack, Target: "https://hooks.slack.test/services/T000/B000/XYZ"}},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := alertStore.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	profilefixtures.Save(t, profileStore, "proj-1", profilefixtures.DBHeavy().Spec().
		WithIDs("evt-profile-alert-1", "profile-alert-1").
		WithTransaction("GET /checkout").
		WithTrace("trace-1").
		WithDuration(34000000))

	cb := NewAlertCallback(AlertDeps{
		Evaluator:    evaluator,
		Notifier:     notifier,
		HistoryStore: historyStore,
		AlertStore:   alertStore,
		Profiles:     profileStore,
	})

	cb(ctx, "proj-1", issue.ProcessResult{
		EventID:     "txn-1",
		EventType:   alert.EventTypeTransaction,
		TraceID:     "trace-1",
		Transaction: "GET /checkout",
		DurationMS:  925,
	})

	deliveries, err := deliveryStore.ListRecent(ctx, "proj-1", 10)
	if err != nil {
		t.Fatalf("ListRecent deliveries: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("expected 1 delivery, got %d", len(deliveries))
	}
	if deliveries[0].Kind != notify.DeliveryKindSlack || deliveries[0].Status != notify.DeliveryStatusDelivered {
		t.Fatalf("unexpected delivery: %+v", deliveries[0])
	}
	if !strings.Contains(deliveries[0].PayloadJSON, "profile-alert-1") || !strings.Contains(deliveries[0].PayloadJSON, "dbQuery") {
		t.Fatalf("expected profile-enriched slack payload, got %+v", deliveries[0])
	}
}

func TestNewAlertCallback_FiresServiceHooksForEventAndIssueCreation(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()

	client, payloads := newHookCaptureClient(t)

	hooks := sqlite.NewHookStore(db)
	hooks.HTTPClient = client
	for _, item := range [][]string{{"event.created"}, {"issue.created"}} {
		if err := hooks.Create(ctx, &sqlite.ServiceHook{
			ProjectID: "proj-1",
			URL:       "https://hooks.example.test/events",
			Events:    item,
		}); err != nil {
			t.Fatalf("Create hook %v: %v", item, err)
		}
	}

	cb := NewAlertCallback(AlertDeps{Hooks: hooks})
	cb(ctx, "proj-1", issue.ProcessResult{
		EventID:    "evt-hook-1",
		GroupID:    "grp-hook-1",
		IsNewGroup: true,
		EventType:  alert.EventTypeError,
		Status:     "error",
	})

	actions := collectedHookActions(payloads.snapshot())
	if _, ok := actions["event.created"]; !ok {
		t.Fatalf("expected event.created hook, got %#v", actions)
	}
	if _, ok := actions["issue.created"]; !ok {
		t.Fatalf("expected issue.created hook, got %#v", actions)
	}
	if got := actions["issue.created"]["data"].(map[string]any)["issue"].(map[string]any)["id"]; got != "grp-hook-1" {
		t.Fatalf("issue.created issue.id = %v, want grp-hook-1", got)
	}
}

func TestNewAlertCallback_FiresEventAlertServiceHook(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()

	client, payloads := newHookCaptureClient(t)

	hooks := sqlite.NewHookStore(db)
	hooks.HTTPClient = client
	if err := hooks.Create(ctx, &sqlite.ServiceHook{
		ProjectID: "proj-1",
		URL:       "https://hooks.example.test/alerts",
		Events:    []string{"event.alert"},
	}); err != nil {
		t.Fatalf("Create hook: %v", err)
	}

	alertStore := sqlite.NewAlertStore(db)
	rule := &alert.Rule{
		ProjectID: "proj-1",
		Name:      "Every event",
		Status:    "active",
		Conditions: []alert.Condition{{
			ID:   alert.ConditionEveryEvent,
			Name: "Every event",
		}},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := alertStore.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	cb := NewAlertCallback(AlertDeps{
		Evaluator: &alert.Evaluator{Rules: alertStore},
		Hooks:     hooks,
	})
	cb(ctx, "proj-1", issue.ProcessResult{
		EventID:   "evt-alert-hook-1",
		GroupID:   "grp-alert-hook-1",
		EventType: alert.EventTypeError,
		Status:    "error",
	})

	actions := collectedHookActions(payloads.snapshot())
	payload, ok := actions["event.alert"]
	if !ok {
		t.Fatalf("expected event.alert hook, got %#v", actions)
	}
	alertData := payload["data"].(map[string]any)["alert"].(map[string]any)
	if got := alertData["ruleId"]; got != rule.ID {
		t.Fatalf("alert.ruleId = %v, want %q", got, rule.ID)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

// openStoreTestDB opens a SQLite DB for pipeline tests.
func openStoreTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'org', 'Org')`); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('proj-1', 'org-1', 'proj', 'Project', 'go', 'active')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

type hookPayloads struct {
	mu    sync.Mutex
	items []map[string]any
}

func (h *hookPayloads) add(item map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.items = append(h.items, item)
}

func (h *hookPayloads) snapshot() []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]map[string]any, len(h.items))
	copy(out, h.items)
	return out
}

func newHookCaptureClient(t *testing.T) (*http.Client, *hookPayloads) {
	t.Helper()
	payloads := &hookPayloads{}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		defer r.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode hook payload: %v", err)
		}
		payloads.add(payload)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     make(http.Header),
		}, nil
	})}
	return client, payloads
}

func collectedHookActions(items []map[string]any) map[string]map[string]any {
	actions := make(map[string]map[string]any, len(items))
	for _, item := range items {
		action, _ := item["action"].(string)
		if action != "" {
			actions[action] = item
		}
	}
	return actions
}
