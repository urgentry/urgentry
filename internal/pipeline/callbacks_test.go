package pipeline

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"strings"
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
