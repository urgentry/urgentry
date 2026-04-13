package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"urgentry/internal/alert"
	"urgentry/internal/discover"
	"urgentry/internal/issue"
	"urgentry/internal/notify"
	"urgentry/internal/store"
)

// openStoreTestDB opens a fresh SQLite database in a temp directory.
func openStoreTestDB(t testing.TB) *sql.DB {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// ---------------------------------------------------------------------------
// FeedbackStore
// ---------------------------------------------------------------------------

func TestFeedbackStore_SaveAndList(t *testing.T) {
	db := openStoreTestDB(t)
	fs := NewFeedbackStore(db)
	ctx := context.Background()

	// Save 3 feedback items.
	for i, name := range []string{"Alice", "Bob", "Carol"} {
		err := fs.SaveFeedback(ctx, "proj-1", "evt-"+name, name, name+"@example.com", "Comment from "+name)
		if err != nil {
			t.Fatalf("SaveFeedback %d: %v", i, err)
		}
	}

	// List them.
	items, err := fs.ListFeedback(ctx, "proj-1", 10)
	if err != nil {
		t.Fatalf("ListFeedback: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 feedback items, got %d", len(items))
	}

	// Should be ordered by created_at DESC — most recent first.
	// Since they were inserted in rapid succession, just verify all names are present.
	names := map[string]bool{}
	for _, f := range items {
		names[f.Name] = true
	}
	for _, name := range []string{"Alice", "Bob", "Carol"} {
		if !names[name] {
			t.Errorf("missing feedback from %s", name)
		}
	}

	// Different project returns empty.
	items, err = fs.ListFeedback(ctx, "proj-other", 10)
	if err != nil {
		t.Fatalf("ListFeedback other: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items for other project, got %d", len(items))
	}
}

// ---------------------------------------------------------------------------
// AlertStore
// ---------------------------------------------------------------------------

func TestAlertStore_CRUD(t *testing.T) {
	db := openStoreTestDB(t)
	as := NewAlertStore(db)
	ctx := context.Background()

	rule := &alert.Rule{
		ProjectID: "proj-1",
		Name:      "First Seen Alert",
		Status:    "active",
		RuleType:  "all",
		Conditions: []alert.Condition{
			{ID: "sentry.rules.conditions.first_seen_event.FirstSeenEventCondition", Name: "First seen"},
		},
		Actions: []alert.Action{
			{ID: "a1", Type: "email", Target: "dev@example.com"},
		},
		CreatedAt: time.Now().UTC(),
	}

	// Create.
	if err := as.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if rule.ID == "" {
		t.Fatal("rule ID should be set after create")
	}

	// Get.
	got, err := as.GetRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if got == nil {
		t.Fatal("GetRule returned nil")
	}
	if got.Name != "First Seen Alert" {
		t.Errorf("Name = %q, want 'First Seen Alert'", got.Name)
	}
	if len(got.Conditions) != 1 {
		t.Errorf("expected 1 condition, got %d", len(got.Conditions))
	}

	// List.
	rules, err := as.ListRules(ctx, "proj-1")
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	// Update.
	rule.Name = "Updated Alert"
	rule.Status = "disabled"
	if err := as.UpdateRule(ctx, rule); err != nil {
		t.Fatalf("UpdateRule: %v", err)
	}
	got, _ = as.GetRule(ctx, rule.ID)
	if got.Name != "Updated Alert" {
		t.Errorf("after update: Name = %q, want 'Updated Alert'", got.Name)
	}
	if got.Status != "disabled" {
		t.Errorf("after update: Status = %q, want 'disabled'", got.Status)
	}

	// Delete.
	if err := as.DeleteRule(ctx, rule.ID); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
	got, _ = as.GetRule(ctx, rule.ID)
	if got != nil {
		t.Error("expected nil after delete")
	}
}

// ---------------------------------------------------------------------------
// AlertHistoryStore
// ---------------------------------------------------------------------------

func TestAlertHistoryStore_RecordAndList(t *testing.T) {
	db := openStoreTestDB(t)
	hs := NewAlertHistoryStore(db)
	ctx := context.Background()

	// Record 3 triggers.
	for i := 0; i < 3; i++ {
		trigger := alert.TriggerEvent{
			RuleID:    "rule-1",
			GroupID:   "grp-1",
			EventID:   generateID(),
			Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Second),
		}
		if err := hs.Record(ctx, trigger); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}

	// List recent.
	history, err := hs.ListRecent(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("expected 3 history entries, got %d", len(history))
	}

	// Should be ordered by fired_at DESC.
	if history[0].FiredAt.Before(history[2].FiredAt) {
		t.Error("expected most recent first")
	}
}

// ---------------------------------------------------------------------------
// NotificationOutboxStore
// ---------------------------------------------------------------------------

func TestNotificationOutboxStore_RecordAndList(t *testing.T) {
	db := openStoreTestDB(t)
	store := NewNotificationOutboxStore(db)
	ctx := context.Background()

	notification := &notify.EmailNotification{
		ProjectID: "proj-1",
		RuleID:    "rule-1",
		GroupID:   "grp-1",
		EventID:   "evt-1",
		Recipient: "dev@example.com",
		Subject:   "Alert fired",
		Body:      "body",
	}
	if err := store.RecordEmail(ctx, notification); err != nil {
		t.Fatalf("RecordEmail: %v", err)
	}

	items, err := store.ListRecent(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(items))
	}
	if items[0].Recipient != "dev@example.com" || items[0].Status != "queued" {
		t.Fatalf("unexpected notification: %+v", items[0])
	}
}

// ---------------------------------------------------------------------------
// ReleaseStore
// ---------------------------------------------------------------------------

func TestReleaseStore_EnsureAndList(t *testing.T) {
	db := openStoreTestDB(t)

	// We need an org for releases.
	_, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`)
	if err != nil {
		t.Fatalf("insert org: %v", err)
	}

	rs := NewReleaseStore(db)
	ctx := context.Background()

	// Ensure creates a release.
	if err := rs.EnsureRelease(ctx, "org-1", "v1.0.0"); err != nil {
		t.Fatalf("EnsureRelease: %v", err)
	}

	// Idempotent: calling again should not error.
	if err := rs.EnsureRelease(ctx, "org-1", "v1.0.0"); err != nil {
		t.Fatalf("EnsureRelease idempotent: %v", err)
	}

	// Ensure a second release.
	if err := rs.EnsureRelease(ctx, "org-1", "v2.0.0"); err != nil {
		t.Fatalf("EnsureRelease v2: %v", err)
	}

	// List.
	releases, err := rs.ListReleases(ctx, "org-1", 10)
	if err != nil {
		t.Fatalf("ListReleases: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("expected 2 releases, got %d", len(releases))
	}

	// Verify versions.
	versions := map[string]bool{}
	for _, r := range releases {
		versions[r.Version] = true
	}
	if !versions["v1.0.0"] || !versions["v2.0.0"] {
		t.Errorf("missing expected versions: got %v", versions)
	}
}

func TestReleaseStore_GetReleaseRegression(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme'), ('org-2', 'other', 'Other')`); err != nil {
		t.Fatalf("insert orgs: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status, created_at) VALUES
		('proj-1', 'org-1', 'checkout', 'Checkout', 'go', 'active', ?),
		('proj-2', 'org-2', 'mirror', 'Mirror', 'go', 'active', ?)`,
		time.Now().UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert projects: %v", err)
	}

	rs := NewReleaseStore(db)
	if _, err := rs.CreateRelease(ctx, "acme", "checkout@1.0.0"); err != nil {
		t.Fatalf("CreateRelease previous: %v", err)
	}
	if _, err := rs.CreateRelease(ctx, "acme", "checkout@2.0.0"); err != nil {
		t.Fatalf("CreateRelease current: %v", err)
	}
	if _, err := rs.CreateRelease(ctx, "other", "checkout@2.0.0"); err != nil {
		t.Fatalf("CreateRelease other org: %v", err)
	}

	previousCreatedAt := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	currentCreatedAt := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`UPDATE releases SET created_at = ? WHERE organization_id = 'org-1' AND version = 'checkout@1.0.0'`, previousCreatedAt.Format(time.RFC3339)); err != nil {
		t.Fatalf("update previous created_at: %v", err)
	}
	if _, err := db.Exec(`UPDATE releases SET created_at = ? WHERE organization_id = 'org-1' AND version = 'checkout@2.0.0'`, currentCreatedAt.Format(time.RFC3339)); err != nil {
		t.Fatalf("update current created_at: %v", err)
	}

	deployFinishedAt := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	if _, err := rs.AddDeploy(ctx, "acme", "checkout@2.0.0", store.ReleaseDeploy{
		Environment:  "production",
		Name:         "deploy-200",
		DateFinished: deployFinishedAt,
	}); err != nil {
		t.Fatalf("AddDeploy: %v", err)
	}

	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json)
		 VALUES
			('evt-prev-1', 'proj-1', 'evt-prev-1', 'checkout@1.0.0', 'production', 'go', 'error', 'error', 'CheckoutError', 'boom', 'checkout.go', ?, '{}', '{}'),
			('evt-cur-1', 'proj-1', 'evt-cur-1', 'checkout@2.0.0', 'production', 'go', 'error', 'error', 'CheckoutError', 'boom', 'checkout.go', ?, '{}', '{}'),
			('evt-cur-2', 'proj-1', 'evt-cur-2', 'checkout@2.0.0', 'production', 'go', 'error', 'error', 'CheckoutError', 'boom', 'checkout.go', ?, '{}', '{}'),
			('evt-cur-3', 'proj-1', 'evt-cur-3', 'checkout@2.0.0', 'staging', 'go', 'error', 'error', 'CheckoutError', 'boom', 'checkout.go', ?, '{}', '{}'),
			('evt-cur-4', 'proj-1', 'evt-cur-4', 'checkout@2.0.0', 'production', 'go', 'error', 'error', 'CheckoutError', 'boom', 'checkout.go', ?, '{}', '{}'),
			('evt-other-1', 'proj-2', 'evt-other-1', 'checkout@2.0.0', 'production', 'go', 'error', 'error', 'CheckoutError', 'boom', 'checkout.go', ?, '{}', '{}')`,
		previousCreatedAt.Add(2*time.Hour).Format(time.RFC3339),
		deployFinishedAt.Add(-2*time.Hour).Format(time.RFC3339),
		deployFinishedAt.Add(1*time.Hour).Format(time.RFC3339),
		deployFinishedAt.Add(90*time.Minute).Format(time.RFC3339),
		deployFinishedAt.Add(2*time.Hour).Format(time.RFC3339),
		deployFinishedAt.Add(2*time.Hour).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	health := NewReleaseHealthStore(db)
	for _, session := range []*ReleaseSession{
		{ProjectID: "proj-1", Release: "checkout@1.0.0", Status: "ok", Quantity: 4, DistinctID: "user-a", DateCreated: previousCreatedAt.Add(30 * time.Minute)},
		{ProjectID: "proj-1", Release: "checkout@1.0.0", Status: "errored", Quantity: 1, DistinctID: "user-b", DateCreated: previousCreatedAt.Add(45 * time.Minute)},
		{ProjectID: "proj-1", Release: "checkout@2.0.0", Status: "ok", Quantity: 5, DistinctID: "user-a", DateCreated: currentCreatedAt.Add(30 * time.Minute)},
		{ProjectID: "proj-1", Release: "checkout@2.0.0", Status: "errored", Quantity: 2, DistinctID: "user-c", DateCreated: currentCreatedAt.Add(45 * time.Minute)},
		{ProjectID: "proj-1", Release: "checkout@2.0.0", Status: "crashed", Quantity: 1, DistinctID: "user-d", DateCreated: currentCreatedAt.Add(50 * time.Minute)},
	} {
		if err := health.SaveSession(ctx, session); err != nil {
			t.Fatalf("SaveSession(%s): %v", session.Release, err)
		}
	}

	traces := NewTraceStore(db)
	for _, txn := range []*store.StoredTransaction{
		{ProjectID: "proj-1", EventID: "txn-prev-1", TraceID: "trace-prev-1", SpanID: "span-prev-1", Transaction: "checkout", Environment: "production", ReleaseID: "checkout@1.0.0", StartTimestamp: previousCreatedAt.Add(4 * time.Hour), EndTimestamp: previousCreatedAt.Add(4*time.Hour + 200*time.Millisecond), DurationMS: 200},
		{ProjectID: "proj-1", EventID: "txn-prev-2", TraceID: "trace-prev-2", SpanID: "span-prev-2", Transaction: "checkout", Environment: "production", ReleaseID: "checkout@1.0.0", StartTimestamp: previousCreatedAt.Add(5 * time.Hour), EndTimestamp: previousCreatedAt.Add(5*time.Hour + 220*time.Millisecond), DurationMS: 220},
		{ProjectID: "proj-1", EventID: "txn-cur-1", TraceID: "trace-cur-1", SpanID: "span-cur-1", Transaction: "checkout", Environment: "production", ReleaseID: "checkout@2.0.0", StartTimestamp: deployFinishedAt.Add(-90 * time.Minute), EndTimestamp: deployFinishedAt.Add(-90*time.Minute + 160*time.Millisecond), DurationMS: 160},
		{ProjectID: "proj-1", EventID: "txn-cur-2", TraceID: "trace-cur-2", SpanID: "span-cur-2", Transaction: "checkout", Environment: "production", ReleaseID: "checkout@2.0.0", StartTimestamp: deployFinishedAt.Add(45 * time.Minute), EndTimestamp: deployFinishedAt.Add(45*time.Minute + 380*time.Millisecond), DurationMS: 380},
		{ProjectID: "proj-1", EventID: "txn-cur-3", TraceID: "trace-cur-3", SpanID: "span-cur-3", Transaction: "checkout", Environment: "production", ReleaseID: "checkout@2.0.0", StartTimestamp: deployFinishedAt.Add(90 * time.Minute), EndTimestamp: deployFinishedAt.Add(90*time.Minute + 420*time.Millisecond), DurationMS: 420},
		{ProjectID: "proj-1", EventID: "txn-cur-4", TraceID: "trace-cur-4", SpanID: "span-cur-4", Transaction: "search", Environment: "staging", ReleaseID: "checkout@2.0.0", StartTimestamp: deployFinishedAt.Add(2 * time.Hour), EndTimestamp: deployFinishedAt.Add(2*time.Hour + 110*time.Millisecond), DurationMS: 110},
	} {
		if err := traces.SaveTransaction(ctx, txn); err != nil {
			t.Fatalf("SaveTransaction(%s): %v", txn.EventID, err)
		}
	}

	summary, err := rs.GetReleaseRegression(ctx, "acme", "checkout@2.0.0")
	if err != nil {
		t.Fatalf("GetReleaseRegression: %v", err)
	}
	if summary == nil || summary.Previous == nil {
		t.Fatalf("expected previous release summary, got %+v", summary)
	}
	if summary.Current.EventCount != 4 {
		t.Fatalf("current event count = %d, want 4", summary.Current.EventCount)
	}
	if summary.Previous.Version != "checkout@1.0.0" {
		t.Fatalf("previous version = %q, want checkout@1.0.0", summary.Previous.Version)
	}
	if summary.EventDelta.Delta != 3 {
		t.Fatalf("event delta = %+v, want +3", summary.EventDelta)
	}
	if len(summary.EnvironmentMovements) == 0 || summary.EnvironmentMovements[0].Environment != "production" || summary.EnvironmentMovements[0].DeltaErrors != 2 {
		t.Fatalf("unexpected environment movements: %+v", summary.EnvironmentMovements)
	}
	if len(summary.TransactionMovements) == 0 || summary.TransactionMovements[0].Transaction != "checkout" || summary.TransactionMovements[0].DeltaP95 <= 0 {
		t.Fatalf("unexpected transaction movements: %+v", summary.TransactionMovements)
	}
	if summary.LatestDeployImpact == nil {
		t.Fatal("expected latest deploy impact")
	}
	if summary.LatestDeployImpact.ErrorsBefore != 1 || summary.LatestDeployImpact.ErrorsAfter != 2 {
		t.Fatalf("unexpected deploy error impact: %+v", summary.LatestDeployImpact)
	}
	if summary.LatestDeployImpact.P95After <= summary.LatestDeployImpact.P95Before {
		t.Fatalf("expected deploy latency regression, got %+v", summary.LatestDeployImpact)
	}
}

func TestReleaseStore_ProjectHasRelease(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status, created_at) VALUES
		('proj-1', 'org-1', 'checkout', 'Checkout', 'go', 'active', ?),
		('proj-2', 'org-1', 'mobile', 'Mobile', 'swift', 'active', ?)`,
		time.Now().UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert projects: %v", err)
	}

	rs := NewReleaseStore(db)
	if _, err := rs.CreateRelease(ctx, "acme", "checkout@1.0.0"); err != nil {
		t.Fatalf("CreateRelease event release: %v", err)
	}
	if _, err := rs.CreateRelease(ctx, "acme", "mobile@1.0.0"); err != nil {
		t.Fatalf("CreateRelease debug release: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO events
		(id, project_id, event_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json)
	 VALUES
		('evt-1', 'proj-1', 'evt-1', 'checkout@1.0.0', 'production', 'go', 'error', 'error', 'CheckoutError', 'boom', 'checkout/service.go', ?, '{}', '{}')`,
		now,
	); err != nil {
		t.Fatalf("insert release event: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO debug_files
		(id, project_id, release_version, uuid, code_id, name, object_key, size_bytes, checksum, created_at, kind, content_type)
	 VALUES
		('dbg-1', 'proj-2', 'mobile@1.0.0', 'UUID-1', '', 'App.dSYM.zip', 'debug/mobile', 1, '', ?, 'apple', 'application/zip')`,
		now,
	); err != nil {
		t.Fatalf("insert debug file: %v", err)
	}

	hasRelease, err := rs.ProjectHasRelease(ctx, "proj-1", "checkout@1.0.0")
	if err != nil {
		t.Fatalf("ProjectHasRelease project event: %v", err)
	}
	if !hasRelease {
		t.Fatal("expected project event release association")
	}

	hasRelease, err = rs.ProjectHasRelease(ctx, "proj-2", "mobile@1.0.0")
	if err != nil {
		t.Fatalf("ProjectHasRelease project debug file: %v", err)
	}
	if !hasRelease {
		t.Fatal("expected project debug-file release association")
	}

	hasRelease, err = rs.ProjectHasRelease(ctx, "proj-2", "checkout@1.0.0")
	if err != nil {
		t.Fatalf("ProjectHasRelease foreign project: %v", err)
	}
	if hasRelease {
		t.Fatal("expected missing project release association")
	}
}

// ---------------------------------------------------------------------------
// SourceMapStore
// ---------------------------------------------------------------------------

func TestSourceMapStore_UploadAndLookup(t *testing.T) {
	db := openStoreTestDB(t)
	blobs := store.NewMemoryBlobStore()
	sms := NewSourceMapStore(db, blobs)
	ctx := context.Background()

	data := []byte(`{"version":3,"sources":["app.js"]}`)

	// Upload.
	if err := sms.Upload(ctx, "proj-1", "v1.0.0", "app.js.map", data); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// Lookup by name.
	art, got, err := sms.LookupByName(ctx, "proj-1", "v1.0.0", "app.js.map")
	if err != nil {
		t.Fatalf("LookupByName: %v", err)
	}
	if art == nil {
		t.Fatal("LookupByName returned nil artifact")
	}
	if string(got) != string(data) {
		t.Errorf("data mismatch: got %q, want %q", string(got), string(data))
	}
	if art.Name != "app.js.map" {
		t.Errorf("artifact name = %q, want 'app.js.map'", art.Name)
	}

	// Lookup convenience.
	lookupData, err := sms.Lookup(ctx, "proj-1", "v1.0.0", "app.js.map")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if string(lookupData) != string(data) {
		t.Errorf("Lookup data mismatch")
	}

	// List by release.
	artifacts, err := sms.ListByRelease(ctx, "proj-1", "v1.0.0")
	if err != nil {
		t.Fatalf("ListByRelease: %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(artifacts))
	}

	// Delete.
	if err := sms.DeleteArtifact(ctx, art.ID); err != nil {
		t.Fatalf("DeleteArtifact: %v", err)
	}
	artifacts, _ = sms.ListByRelease(ctx, "proj-1", "v1.0.0")
	if len(artifacts) != 0 {
		t.Errorf("expected 0 artifacts after delete, got %d", len(artifacts))
	}
}

// ---------------------------------------------------------------------------
// SearchStore
// ---------------------------------------------------------------------------

func TestSearchStore_SaveAndList(t *testing.T) {
	db := openStoreTestDB(t)
	ss := NewSearchStore(db)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, email, display_name) VALUES ('user-1', 'user-1@example.com', 'User 1')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Save.
	saved, err := ss.Save(ctx, "user-1", "acme", SavedSearchVisibilityPrivate, "My Search", "Important issue query", "ValueError", "unresolved", "production", "last_seen", true)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.ID == "" {
		t.Fatal("saved search ID should be set")
	}
	if saved.Name != "My Search" {
		t.Errorf("Name = %q, want 'My Search'", saved.Name)
	}
	if saved.Description != "Important issue query" {
		t.Errorf("Description = %q, want saved description", saved.Description)
	}
	if !saved.Favorite {
		t.Fatal("saved query should be favorited for creator")
	}
	if saved.QueryVersion != discover.CurrentVersion {
		t.Fatalf("query version = %d, want %d", saved.QueryVersion, discover.CurrentVersion)
	}
	if saved.QueryDoc.Scope.Organization != "acme" {
		t.Fatalf("query scope org = %q, want acme", saved.QueryDoc.Scope.Organization)
	}

	// Save another.
	_, err = ss.Save(ctx, "user-1", "acme", SavedSearchVisibilityPrivate, "Another Search", "", "TypeError", "all", "", "events", false)
	if err != nil {
		t.Fatalf("Save second: %v", err)
	}

	// List.
	searches, err := ss.List(ctx, "user-1", "acme")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(searches) != 2 {
		t.Fatalf("expected 2 searches, got %d", len(searches))
	}
	if searches[0].QueryVersion != discover.CurrentVersion {
		t.Fatalf("listed query version = %d, want %d", searches[0].QueryVersion, discover.CurrentVersion)
	}
	if searches[0].QueryDoc.Dataset != discover.DatasetIssues {
		t.Fatalf("listed query dataset = %q, want issues", searches[0].QueryDoc.Dataset)
	}

	// Delete.
	if err := ss.Delete(ctx, "user-1", saved.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	searches, _ = ss.List(ctx, "user-1", "acme")
	if len(searches) != 1 {
		t.Errorf("expected 1 search after delete, got %d", len(searches))
	}
}

func TestSearchStore_ListUpgradesLegacyQuery(t *testing.T) {
	db := openStoreTestDB(t)
	ss := NewSearchStore(db)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO saved_searches (id, user_id, name, query, filter, environment, sort)
		 VALUES ('search-1', 'user-1', 'Legacy', 'release:backend@1.2.3 ImportError', 'unresolved', 'production', 'last_seen')`,
	); err != nil {
		t.Fatalf("insert legacy saved search: %v", err)
	}

	searches, err := ss.List(ctx, "user-1", "acme")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(searches) != 1 {
		t.Fatalf("expected 1 search, got %d", len(searches))
	}
	if searches[0].QueryVersion != discover.CurrentVersion {
		t.Fatalf("query version = %d, want %d", searches[0].QueryVersion, discover.CurrentVersion)
	}
	if searches[0].QueryDoc.Scope.Organization != "acme" {
		t.Fatalf("query scope org = %q, want acme", searches[0].QueryDoc.Scope.Organization)
	}
	if searches[0].QueryDoc.Where == nil {
		t.Fatal("legacy query did not upgrade into AST")
	}
}

func TestSearchStore_ListIncludesOrganizationSharedQueries(t *testing.T) {
	db := openStoreTestDB(t)
	ss := NewSearchStore(db)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name, created_at) VALUES
		('org-1', 'acme', 'Acme', ?),
		('org-2', 'other', 'Other', ?)`,
		time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed organizations: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, email, display_name) VALUES ('user-1', 'user-1@example.com', 'User 1')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := ss.Save(ctx, "user-1", "acme", SavedSearchVisibilityPrivate, "Mine", "", "ValueError", "all", "", "last_seen", false); err != nil {
		t.Fatalf("save private: %v", err)
	}
	shared, err := ss.Save(ctx, "user-2", "acme", SavedSearchVisibilityOrganization, "Shared", "Team dashboard baseline", "checkout", "all", "", "last_seen", false)
	if err != nil {
		t.Fatalf("save shared: %v", err)
	}
	if _, err := ss.Save(ctx, "user-3", "other", SavedSearchVisibilityOrganization, "Other org", "", "blocked", "all", "", "last_seen", false); err != nil {
		t.Fatalf("save other org shared: %v", err)
	}
	if err := ss.SetFavorite(ctx, "user-1", "acme", shared.ID, true); err != nil {
		t.Fatalf("SetFavorite: %v", err)
	}

	searches, err := ss.List(ctx, "user-1", "acme")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(searches) != 2 {
		t.Fatalf("expected 2 visible searches, got %d", len(searches))
	}
	if searches[0].ID != shared.ID || !searches[0].Favorite {
		t.Fatalf("first visible search = %+v, want favorite shared query first", searches[0])
	}
	if searches[1].UserID != "user-1" || searches[1].Visibility != SavedSearchVisibilityPrivate {
		t.Fatalf("second visible search = %+v, want owner private search second", searches[1])
	}
	got, err := ss.Get(ctx, "user-1", "acme", shared.ID)
	if err != nil {
		t.Fatalf("Get shared: %v", err)
	}
	if got == nil || got.ID != shared.ID {
		t.Fatalf("shared search lookup = %+v, want %s", got, shared.ID)
	}
	if got.Description != "Team dashboard baseline" {
		t.Fatalf("shared description = %q, want %q", got.Description, "Team dashboard baseline")
	}
	if !got.Favorite {
		t.Fatal("shared query should load as favorited for requesting user")
	}
}

func TestSearchStore_CloneVisibleQuery(t *testing.T) {
	db := openStoreTestDB(t)
	ss := NewSearchStore(db)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, email, display_name) VALUES ('user-1', 'user-1@example.com', 'User 1')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	shared, err := ss.Save(ctx, "other-user", "acme", SavedSearchVisibilityOrganization, "Shared", "Shared baseline", "checkout", "all", "production", "last_seen", false)
	if err != nil {
		t.Fatalf("save shared: %v", err)
	}
	updatedShared, err := ss.UpdateMetadata(ctx, "other-user", "acme", shared.ID, shared.Name, shared.Description, shared.Visibility, []string{"team", "Latency", "team"})
	if err != nil {
		t.Fatalf("UpdateMetadata shared tags: %v", err)
	}
	shared = updatedShared

	cloned, err := ss.Clone(ctx, "user-1", "acme", shared.ID, "", "", true)
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if cloned == nil {
		t.Fatal("Clone returned nil")
	}
	if cloned.Name != "Shared copy" {
		t.Fatalf("clone name = %q, want %q", cloned.Name, "Shared copy")
	}
	if cloned.Description != "Shared baseline" {
		t.Fatalf("clone description = %q, want %q", cloned.Description, "Shared baseline")
	}
	if cloned.Visibility != SavedSearchVisibilityPrivate {
		t.Fatalf("clone visibility = %q, want private", cloned.Visibility)
	}
	if !cloned.Favorite {
		t.Fatal("clone should be favorited when requested")
	}
	if len(cloned.Tags) != 2 || cloned.Tags[0] != "latency" || cloned.Tags[1] != "team" {
		t.Fatalf("clone tags = %#v, want [latency team]", cloned.Tags)
	}
}

func TestSearchStore_UpdateMetadataAndTags(t *testing.T) {
	db := openStoreTestDB(t)
	ss := NewSearchStore(db)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, email, display_name) VALUES ('user-1', 'user-1@example.com', 'User 1')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	saved, err := ss.Save(ctx, "user-1", "acme", SavedSearchVisibilityPrivate, "Original", "Old description", "ValueError", "all", "", "last_seen", false)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	updated, err := ss.UpdateMetadata(ctx, "user-1", "acme", saved.ID, "Renamed", "New description", SavedSearchVisibilityOrganization, []string{"  team ", "latency", "team"})
	if err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}
	if updated.Name != "Renamed" || updated.Description != "New description" {
		t.Fatalf("updated search = %+v", updated)
	}
	if updated.Visibility != SavedSearchVisibilityOrganization {
		t.Fatalf("visibility = %q, want organization", updated.Visibility)
	}
	if len(updated.Tags) != 2 || updated.Tags[0] != "latency" || updated.Tags[1] != "team" {
		t.Fatalf("tags = %#v, want [latency team]", updated.Tags)
	}
}

// ---------------------------------------------------------------------------
// KeyStore
// ---------------------------------------------------------------------------

func TestKeyStore_LookupAndDefault(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()

	// EnsureDefaultKey should create org + project + key.
	pubKey, err := EnsureDefaultKey(ctx, db)
	if err != nil {
		t.Fatalf("EnsureDefaultKey: %v", err)
	}
	if pubKey == "" {
		t.Fatal("expected non-empty public key")
	}

	// Calling again should return the same key.
	pubKey2, err := EnsureDefaultKey(ctx, db)
	if err != nil {
		t.Fatalf("EnsureDefaultKey second: %v", err)
	}
	if pubKey2 != pubKey {
		t.Errorf("second call returned different key: %q vs %q", pubKey2, pubKey)
	}

	// LookupKey should find the active key.
	ks := NewKeyStore(db)
	key, err := ks.LookupKey(ctx, pubKey)
	if err != nil {
		t.Fatalf("LookupKey: %v", err)
	}
	if key.PublicKey != pubKey {
		t.Errorf("PublicKey = %q, want %q", key.PublicKey, pubKey)
	}
	if key.Status != "active" {
		t.Errorf("Status = %q, want 'active'", key.Status)
	}
	if key.RateLimit != 0 {
		t.Errorf("RateLimit = %d, want 0", key.RateLimit)
	}

	_, err = db.Exec("UPDATE project_keys SET rate_limit = 17 WHERE public_key = ?", pubKey)
	if err != nil {
		t.Fatalf("set rate limit: %v", err)
	}
	key, err = ks.LookupKey(ctx, pubKey)
	if err != nil {
		t.Fatalf("LookupKey rate limit: %v", err)
	}
	if key.RateLimit != 17 {
		t.Errorf("RateLimit after update = %d, want 17", key.RateLimit)
	}

	// Disable the key and verify.
	_, err = db.Exec("UPDATE project_keys SET status = 'disabled' WHERE public_key = ?", pubKey)
	if err != nil {
		t.Fatalf("disable key: %v", err)
	}
	key, err = ks.LookupKey(ctx, pubKey)
	if err != nil {
		t.Fatalf("LookupKey disabled: %v", err)
	}
	if key.Status != "disabled" {
		t.Errorf("Status after disable = %q, want 'disabled'", key.Status)
	}
}

// ---------------------------------------------------------------------------
// GroupStore - Short ID
// ---------------------------------------------------------------------------

func TestGroupStore_ShortID(t *testing.T) {
	db := openStoreTestDB(t)
	gs := NewGroupStore(db)
	ctx := context.Background()
	now := time.Now().UTC()

	// Create 3 groups and verify sequential short_ids.
	for i := 0; i < 3; i++ {
		g := &issue.Group{
			ProjectID:       "proj-shortid",
			GroupingVersion: "urgentry-v1",
			GroupingKey:     generateID(),
			Title:           "Short ID Test",
			Level:           "error",
			FirstSeen:       now,
			LastSeen:        now,
			LastEventID:     generateID(),
		}
		if err := gs.UpsertGroup(ctx, g); err != nil {
			t.Fatalf("UpsertGroup %d: %v", i, err)
		}
	}

	// Query short_ids.
	rows, err := db.Query("SELECT short_id FROM groups WHERE project_id = 'proj-shortid' ORDER BY short_id")
	if err != nil {
		t.Fatalf("query short_ids: %v", err)
	}
	defer rows.Close()

	var shortIDs []int
	for rows.Next() {
		var sid int
		if err := rows.Scan(&sid); err != nil {
			t.Fatalf("scan short_id: %v", err)
		}
		shortIDs = append(shortIDs, sid)
	}

	if len(shortIDs) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(shortIDs))
	}

	// Verify they are sequential.
	for i := 1; i < len(shortIDs); i++ {
		if shortIDs[i] != shortIDs[i-1]+1 {
			t.Errorf("short_ids not sequential: %v", shortIDs)
			break
		}
	}
}
