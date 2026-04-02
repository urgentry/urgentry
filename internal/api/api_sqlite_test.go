package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"urgentry/internal/analyticsservice"
	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"urgentry/internal/telemetryquery"
	profilefixtures "urgentry/internal/testfixtures/profiles"
)

// ---------------------------------------------------------------------------
// Helpers for SQLite-backed API tests
// ---------------------------------------------------------------------------

func openTestSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedSQLiteAuth(t *testing.T, db *sql.DB) {
	t.Helper()
	// Seed org, team, project, and a public ingest key for mixed API/ingest fixtures.
	if _, err := db.Exec(`INSERT OR IGNORE INTO organizations (id, slug, name) VALUES ('test-org-id', 'test-org', 'Test Org')`); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO teams (id, organization_id, slug, name) VALUES ('test-team-id', 'test-org-id', 'backend', 'Backend Team')`); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO projects (id, organization_id, slug, name, platform, status) VALUES ('test-proj-id', 'test-org-id', 'test-project', 'Test Project', 'go', 'active')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO project_keys (id, project_id, public_key, status, label) VALUES ('key-test', 'test-proj-id', 'test-public-key', 'active', 'Test Key')`); err != nil {
		t.Fatalf("seed project key: %v", err)
	}
}

func saveProfileQueryFixture(t *testing.T, profiles *sqlite.ProfileStore, projectID string, spec profilefixtures.EnvelopeSpec) {
	t.Helper()
	profilefixtures.Save(t, profiles, projectID, spec)
}

func sqliteAuthorizedDependencies(t *testing.T, db *sql.DB, deps Dependencies) Dependencies {
	t.Helper()

	authStore := sqlite.NewAuthStore(db)
	if _, err := authStore.EnsureBootstrapAccess(t.Context(), sqlite.BootstrapOptions{
		DefaultOrganizationID: "test-org-id",
		Email:                 "owner@example.com",
		DisplayName:           "Owner",
		Password:              "test-password-123",
		PersonalAccessToken:   "gpat_test_admin_token",
	}); err != nil {
		t.Fatalf("EnsureBootstrapAccess: %v", err)
	}

	if deps.BlobStore == nil {
		deps.BlobStore = store.NewMemoryBlobStore()
	}
	if deps.Control.Catalog == nil {
		deps.Control = controlplane.SQLiteServices(db)
	}
	if deps.QueryGuard == nil {
		deps.QueryGuard = sqlite.NewQueryGuardStore(db)
	}
	if deps.PrincipalShadows == nil {
		deps.PrincipalShadows = sqlite.NewPrincipalShadowStore(db)
	}
	if deps.OperatorAudits == nil {
		deps.OperatorAudits = sqlite.NewOperatorAuditStore(db)
	}
	if deps.Operators == nil {
		deps.Operators = sqlite.NewOperatorStore(db, store.OperatorRuntime{Role: "test", Env: "test"}, nil, deps.OperatorAudits, func(context.Context) (int, error) {
			return 0, nil
		})
	}
	if deps.Analytics.Dashboards == nil {
		deps.Analytics = analyticsservice.SQLiteServices(db)
	}
	if deps.Backfills == nil {
		deps.Backfills = sqlite.NewBackfillStore(db)
	}
	if deps.Audits == nil {
		deps.Audits = sqlite.NewAuditStore(db)
	}
	if deps.ReleaseHealth == nil {
		deps.ReleaseHealth = sqlite.NewReleaseHealthStore(db)
	}
	if deps.DebugFiles == nil {
		deps.DebugFiles = sqlite.NewDebugFileStore(db, deps.BlobStore)
	}
	if deps.Outcomes == nil {
		deps.Outcomes = sqlite.NewOutcomeStore(db)
	}
	if deps.Hooks == nil {
		deps.Hooks = sqlite.NewHookStore(db)
	}
	if deps.Retention == nil {
		deps.Retention = sqlite.NewRetentionStore(db, deps.BlobStore)
	}
	if deps.NativeControl == nil {
		deps.NativeControl = sqlite.NewNativeControlStore(db, deps.BlobStore, deps.OperatorAudits)
	}
	if deps.ImportExport == nil {
		deps.ImportExport = sqlite.NewImportExportStore(db, deps.Attachments, deps.ProGuardStore, deps.SourceMapStore, deps.BlobStore)
	}
	if deps.Queries == nil {
		deps.Queries = telemetryquery.NewSQLiteService(db, deps.BlobStore)
	}
	deps.DB = db
	deps.Auth = auth.NewAuthorizer(authStore, "urgentry_session", "urgentry_csrf", 30*24*time.Hour)
	deps.TokenManager = authStore
	return deps
}

func newSQLiteTestServer(t *testing.T, db *sql.DB) *httptest.Server {
	t.Helper()

	seedSQLiteAuth(t, db)

	router := NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{}))
	return httptest.NewServer(router)
}

func insertSQLiteGroup(t *testing.T, db *sql.DB, id, title, culprit, level, status string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen)
		 VALUES (?, 'test-proj-id', 'urgentry-v1', ?, ?, ?, ?, ?, ?, ?, 1)`,
		id, id, title, culprit, level, status, now, now,
	)
	if err != nil {
		t.Fatalf("insert group %s: %v", id, err)
	}
}

func insertSQLiteEvent(t *testing.T, db *sql.DB, eventID, groupID, title, level string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	tags := `{"environment":"production"}`
	_, err := db.Exec(
		`INSERT INTO events (id, project_id, event_id, group_id, level, title, message, platform, culprit, occurred_at, tags_json)
		 VALUES (?, 'test-proj-id', ?, ?, ?, ?, 'test message', 'go', 'main.go', ?, ?)`,
		eventID+"-internal", eventID, groupID, level, title, now, tags,
	)
	if err != nil {
		t.Fatalf("insert event %s: %v", eventID, err)
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestAPIListOutcomes_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)

	outcomes := sqlite.NewOutcomeStore(db)
	if err := outcomes.SaveOutcome(t.Context(), &sqlite.Outcome{
		ProjectID:   "test-proj-id",
		EventID:     "evt-1",
		Category:    "error",
		Reason:      "sample_rate",
		Quantity:    3,
		RecordedAt:  time.Now().UTC(),
		DateCreated: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveOutcome: %v", err)
	}

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/test-org/test-project/outcomes/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	defer resp.Body.Close()

	var items []Outcome
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatalf("decode outcomes: %v", err)
	}
	if len(items) != 1 || items[0].Reason != "sample_rate" || items[0].Quantity != 3 {
		t.Fatalf("unexpected outcomes: %+v", items)
	}
}

func TestAPIListMonitorsAndCheckIns_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)

	monitors := sqlite.NewMonitorStore(db)
	if _, err := monitors.SaveCheckIn(t.Context(), &sqlite.MonitorCheckIn{
		ProjectID:   "test-proj-id",
		CheckInID:   "check-in-1",
		MonitorSlug: "nightly-import",
		Status:      "ok",
		DateCreated: time.Now().UTC(),
	}, &sqlite.MonitorConfig{
		Schedule: sqlite.MonitorSchedule{Type: "interval", Value: 5, Unit: "minute"},
		Timezone: "UTC",
	}); err != nil {
		t.Fatalf("SaveCheckIn: %v", err)
	}

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/test-org/test-project/monitors/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("monitor list status = %d, want 200", resp.StatusCode)
	}
	var monitorItems []Monitor
	decodeBody(t, resp, &monitorItems)
	if len(monitorItems) != 1 || monitorItems[0].Slug != "nightly-import" {
		t.Fatalf("unexpected monitors: %+v", monitorItems)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/monitors/nightly-import/check-ins/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("check-ins status = %d, want 200", resp.StatusCode)
	}
	var checkIns []MonitorCheckIn
	decodeBody(t, resp, &checkIns)
	if len(checkIns) != 1 || checkIns[0].CheckInID != "check-in-1" {
		t.Fatalf("unexpected check-ins: %+v", checkIns)
	}
}

func TestAPIOwnershipAndReleaseWorkflow_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)

	if _, err := sqlite.NewReleaseStore(db).CreateRelease(t.Context(), "test-org", "backend@1.2.3"); err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	insertSQLiteGroup(t, db, "grp-release-1", "CheckoutError", "payments.go in charge", "error", "unresolved")
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json)
		 VALUES
			('evt-release-1', 'test-proj-id', 'evt-release-1', 'grp-release-1', 'backend@1.2.3', 'production', 'go', 'error', 'error', 'CheckoutError', 'boom', 'payments.go in charge', ?, '{}', '{}')`,
		now,
	); err != nil {
		t.Fatalf("insert release event: %v", err)
	}

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authPost(t, ts, "/api/0/projects/test-org/test-project/ownership/", ownershipRuleRequest{
		Name:     "Payments",
		Pattern:  "path:payments.go",
		Assignee: "payments@team",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("ownership create status = %d, want 201", resp.StatusCode)
	}
	var rule OwnershipRule
	decodeBody(t, resp, &rule)
	if rule.Assignee != "payments@team" {
		t.Fatalf("unexpected ownership rule: %+v", rule)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/ownership/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ownership list status = %d, want 200", resp.StatusCode)
	}
	var rules []OwnershipRule
	decodeBody(t, resp, &rules)
	if len(rules) != 1 || rules[0].Pattern != "path:payments.go" {
		t.Fatalf("unexpected ownership rules: %+v", rules)
	}

	resp = authPut(t, ts, "/api/0/projects/test-org/test-project/ownership/", ownershipRuleRequest{
		Name:     "Checkout",
		Pattern:  "path:internal/checkout/service.go",
		Assignee: "checkout@team",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("ownership put status = %d, want 201", resp.StatusCode)
	}
	var putRule OwnershipRule
	decodeBody(t, resp, &putRule)
	if putRule.Assignee != "checkout@team" {
		t.Fatalf("unexpected ownership rule from put: %+v", putRule)
	}

	resp = authPost(t, ts, "/api/0/organizations/test-org/releases/backend@1.2.3/deploys/", releaseDeployRequest{
		Environment: "production",
		Name:        "deploy-123",
		URL:         "https://deploys.example.com/123",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("release deploy create status = %d, want 201", resp.StatusCode)
	}

	resp = authPost(t, ts, "/api/0/organizations/test-org/releases/backend@1.2.3/commits/", releaseCommitRequest{
		CommitSHA:   "abc123def456",
		Repository:  "github.com/acme/backend",
		AuthorName:  "Ada",
		AuthorEmail: "ada@example.com",
		Message:     "Fix checkout crash",
		Files:       []string{"payments.go", "internal/checkout/service.go"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("release commit create status = %d, want 201", resp.StatusCode)
	}

	resp = authGet(t, ts, "/api/0/organizations/test-org/releases/backend@1.2.3/deploys/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("release deploy list status = %d, want 200", resp.StatusCode)
	}
	var deploys []ReleaseDeploy
	decodeBody(t, resp, &deploys)
	if len(deploys) != 1 || deploys[0].Environment != "production" {
		t.Fatalf("unexpected deploys: %+v", deploys)
	}

	resp = authGet(t, ts, "/api/0/organizations/test-org/releases/backend@1.2.3/commits/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("release commit list status = %d, want 200", resp.StatusCode)
	}
	var commits []ReleaseCommit
	decodeBody(t, resp, &commits)
	if len(commits) != 1 || commits[0].CommitSHA != "abc123def456" {
		t.Fatalf("unexpected commits: %+v", commits)
	}

	resp = authGet(t, ts, "/api/0/organizations/test-org/releases/backend@1.2.3/suspects/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("release suspect list status = %d, want 200", resp.StatusCode)
	}
	var suspects []ReleaseSuspect
	decodeBody(t, resp, &suspects)
	if len(suspects) != 1 || suspects[0].MatchedFile != "payments.go" {
		t.Fatalf("unexpected suspects: %+v", suspects)
	}
}

func TestAPIListTransactionsAndTrace_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)

	traces := sqlite.NewTraceStore(db)
	profiles := sqlite.NewProfileStore(db, store.NewMemoryBlobStore())
	now := time.Now().UTC()
	if err := traces.SaveTransaction(t.Context(), &store.StoredTransaction{
		ProjectID:      "test-proj-id",
		EventID:        "txn-evt-1",
		TraceID:        "trace-1",
		SpanID:         "root-1",
		Transaction:    "GET /items",
		Op:             "http.server",
		Status:         "ok",
		Platform:       "javascript",
		StartTimestamp: now.Add(-time.Second),
		EndTimestamp:   now,
		DurationMS:     1000,
		Spans: []store.StoredSpan{{
			ProjectID:          "test-proj-id",
			TransactionEventID: "txn-evt-1",
			TraceID:            "trace-1",
			SpanID:             "db-1",
			ParentSpanID:       "root-1",
			Op:                 "db",
			Description:        "SELECT 1",
			StartTimestamp:     now.Add(-500 * time.Millisecond),
			EndTimestamp:       now.Add(-250 * time.Millisecond),
			DurationMS:         250,
		}},
	}); err != nil {
		t.Fatalf("SaveTransaction: %v", err)
	}
	profilefixtures.Save(t, profiles, "test-proj-id", profilefixtures.DBHeavy().Spec().
		WithIDs("evt-trace-profile-1", "trace-profile-1").
		WithTransaction("GET /items").
		WithTrace("trace-1").
		WithDuration(22000000))
	for i := 0; i < 110; i++ {
		eventID := fmt.Sprintf("txn-bulk-%03d", i)
		traceID := fmt.Sprintf("trace-bulk-%03d", i)
		started := now.Add(time.Duration(i+1) * time.Second)
		if err := traces.SaveTransaction(t.Context(), &store.StoredTransaction{
			ProjectID:      "test-proj-id",
			EventID:        eventID,
			TraceID:        traceID,
			SpanID:         fmt.Sprintf("root-%03d", i),
			Transaction:    fmt.Sprintf("GET /bulk/%03d", i),
			Op:             "http.server",
			Status:         "ok",
			Platform:       "javascript",
			StartTimestamp: started,
			EndTimestamp:   started.Add(250 * time.Millisecond),
			DurationMS:     250,
		}); err != nil {
			t.Fatalf("SaveTransaction bulk %d: %v", i, err)
		}
	}

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/test-org/test-project/transactions/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("transactions status = %d, want 200", resp.StatusCode)
	}
	var items []TransactionSummary
	decodeBody(t, resp, &items)
	if len(items) != 100 || items[0].EventID != "txn-bulk-109" {
		t.Fatalf("unexpected transactions: %+v", items)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/transactions/?limit=4")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("transactions(limit) status = %d, want 200", resp.StatusCode)
	}
	decodeBody(t, resp, &items)
	if len(items) != 4 || items[0].EventID != "txn-bulk-109" || items[3].EventID != "txn-bulk-106" {
		t.Fatalf("unexpected limited transactions: %+v", items)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/traces/trace-1/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("trace status = %d, want 200", resp.StatusCode)
	}
	var detail TraceDetail
	decodeBody(t, resp, &detail)
	if detail.TraceID != "trace-1" || len(detail.Spans) != 1 || len(detail.Transactions) != 1 || len(detail.Profiles) != 1 {
		t.Fatalf("unexpected trace detail: %+v", detail)
	}
	if detail.Transactions[0].EventID != "txn-evt-1" {
		t.Fatalf("unexpected trace transaction: %+v", detail.Transactions)
	}
	if detail.Profiles[0].ProfileID != "trace-profile-1" || len(detail.Profiles[0].Summary.TopFunctions) != 1 || detail.Profiles[0].Summary.TopFunctions[0].Name != "dbQuery" {
		t.Fatalf("unexpected trace profiles: %+v", detail.Profiles)
	}
}

// Suppress unused import warning for json.
var _ = json.Compact
