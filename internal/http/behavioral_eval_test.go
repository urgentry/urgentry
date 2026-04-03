package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	gohttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"urgentry/internal/alert"
	"urgentry/internal/issue"
	"urgentry/internal/normalize"
	"urgentry/internal/pipeline"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// TestBehavioralTagExtraction verifies tags survive ingest and appear in
// the issue detail response.
func TestBehavioralTagExtraction(t *testing.T) {
	silenceEvalLog(t)
	db, dataDir := openEvalDB(t)
	seedEvalProject(t, db)
	ingestEvalEvent(t, db, `{
		"event_id": "tagtest00000000000000000000000001",
		"platform": "python", "level": "error",
		"message": "tag extraction test",
		"tags": {"browser": "Chrome", "os": "Linux"}
	}`)

	handler, pat := evalHTTPHandler(t, db, dataDir)
	issues := evalListIssues(t, handler, pat)
	if len(issues) == 0 {
		t.Fatal("no issues after ingest")
	}
	var first struct{ ID string }
	json.Unmarshal(issues[0], &first)

	rec := evalGET(t, handler, pat, "/api/0/issues/"+first.ID+"/")
	if !strings.Contains(rec.Body.String(), "browser") {
		t.Error("tag key 'browser' not found in issue detail")
	}
}

// TestBehavioralBreadcrumbPreservation verifies breadcrumbs survive ingest
// and appear in the event detail.
func TestBehavioralBreadcrumbPreservation(t *testing.T) {
	silenceEvalLog(t)
	db, dataDir := openEvalDB(t)
	seedEvalProject(t, db)
	ingestEvalEvent(t, db, `{
		"event_id": "bctest000000000000000000000000001",
		"platform": "python", "level": "error",
		"message": "breadcrumb test",
		"breadcrumbs": {"values": [
			{"type": "http", "category": "requests", "message": "GET /api/users"},
			{"type": "navigation", "category": "ui", "message": "clicked submit"}
		]}
	}`)

	handler, pat := evalHTTPHandler(t, db, dataDir)
	issues := evalListIssues(t, handler, pat)
	if len(issues) == 0 {
		t.Fatal("no issues after ingest")
	}
	var first struct{ ID string }
	json.Unmarshal(issues[0], &first)

	rec := evalGET(t, handler, pat, "/api/0/issues/"+first.ID+"/events/latest/")
	body := rec.Body.String()
	if !strings.Contains(body, "breadcrumbs") {
		t.Error("breadcrumbs entry not found in event detail")
	}
	if !strings.Contains(body, "GET /api/users") {
		t.Error("breadcrumb message not preserved")
	}
}

// TestBehavioralReleaseEnvironment verifies release+environment filtering works.
func TestBehavioralReleaseEnvironment(t *testing.T) {
	silenceEvalLog(t)
	db, dataDir := openEvalDB(t)
	seedEvalProject(t, db)
	ingestEvalEvent(t, db, `{
		"event_id": "relenv00000000000000000000000001",
		"platform": "python", "level": "error",
		"message": "release env test",
		"release": "backend@2.0.0", "environment": "staging"
	}`)

	handler, pat := evalHTTPHandler(t, db, dataDir)

	issues := evalGetJSON(t, handler, pat, "/api/0/projects/eval-org/eval-project/issues/?query=release:backend@2.0.0")
	if len(issues) == 0 {
		t.Error("release filter returned no issues")
	}

	issues2 := evalGetJSON(t, handler, pat, "/api/0/projects/eval-org/eval-project/issues/?query=environment:staging")
	if len(issues2) == 0 {
		t.Error("environment filter returned no issues")
	}
}

// TestBehavioralAlertTrigger verifies alert rules fire correctly.
func TestBehavioralAlertTrigger(t *testing.T) {
	silenceEvalLog(t)

	rules := alert.NewMemoryRuleStore()
	rule := &alert.Rule{
		ID:        "eval-rule-1",
		ProjectID: "eval-proj",
		Name:      "Eval Alert",
		Status:    "active",
		Conditions: []alert.Condition{{
			ID:   alert.ConditionFirstSeen,
			Name: "first_seen",
		}},
		Actions: []alert.Action{{
			ID:     "webhook",
			Type:   "webhook",
			Target: "https://example.com/hook",
		}},
		CreatedAt: time.Now().UTC(),
	}
	if err := rules.CreateRule(context.Background(), rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	evaluator := &alert.Evaluator{Rules: rules}
	triggers, err := evaluator.Evaluate(context.Background(), "eval-proj", "grp-1", "evt-1", true, false)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(triggers) == 0 {
		t.Fatal("alert should trigger on first_seen + isNew=true")
	}

	triggers2, err := evaluator.Evaluate(context.Background(), "eval-proj", "grp-2", "evt-2", false, false)
	if err != nil {
		t.Fatalf("Evaluate non-new: %v", err)
	}
	if len(triggers2) != 0 {
		t.Error("alert should NOT trigger when isNew=false")
	}
}

// --- Helpers ---

func silenceEvalLog(t *testing.T) {
	t.Helper()
	prev := log.Logger
	log.Logger = zerolog.New(io.Discard)
	t.Cleanup(func() { log.Logger = prev })
}

func openEvalDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dataDir := t.TempDir()
	db, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, dataDir
}

func seedEvalProject(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO organizations (id, slug, name) VALUES ('eval-org-id', 'eval-org', 'Eval Org')`,
		`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('eval-proj', 'eval-org-id', 'eval-project', 'Eval Project', 'python', 'active')`,
		`INSERT INTO project_keys (id, project_id, public_key, secret_key, status, label) VALUES ('eval-key', 'eval-proj', 'eval-public-key', 'eval-secret-key', 'active', 'Eval')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func ingestEvalEvent(t *testing.T, db *sql.DB, payload string) {
	t.Helper()
	events := sqlite.NewEventStore(db)
	groups := sqlite.NewGroupStore(db)
	blobs := store.NewMemoryBlobStore()
	processor := &issue.Processor{Events: events, Groups: groups, Blobs: blobs}
	pipe := pipeline.New(processor, 1, 1)
	pipe.Start(context.Background())
	evt, err := normalize.Normalize([]byte(payload))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	raw, _ := json.Marshal(evt)
	if !pipe.EnqueueNonBlocking(pipeline.Item{ProjectID: "eval-proj", RawEvent: raw}) {
		t.Fatal("enqueue failed")
	}
	time.Sleep(200 * time.Millisecond)
	pipe.Stop()
}

func evalHTTPHandler(t *testing.T, db *sql.DB, dataDir string) (gohttp.Handler, string) {
	t.Helper()
	authStore := sqlite.NewAuthStore(db)
	bootstrap, err := authStore.EnsureBootstrapAccess(context.Background(), sqlite.BootstrapOptions{
		DefaultOrganizationID: "eval-org-id",
		Email:                 "eval@example.com",
		DisplayName:           "Eval",
		Password:              "eval-password-123",
		PersonalAccessToken:   "gpat_eval_token",
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	keyStore := sqlite.NewKeyStore(db)
	blobs := store.NewMemoryBlobStore()
	nativeCrashes := sqlite.NewNativeCrashStore(db, blobs, sqlite.NewJobStore(db), 100)
	handler := newBenchmarkServer(sqliteServerDeps(t, db, dataDir, keyStore, authStore, nil, blobs, nativeCrashes))
	return handler, bootstrap.PAT
}

func evalGET(t *testing.T, handler gohttp.Handler, pat, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(gohttp.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+pat)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func evalListIssues(t *testing.T, handler gohttp.Handler, pat string) []json.RawMessage {
	t.Helper()
	return evalGetJSON(t, handler, pat, "/api/0/projects/eval-org/eval-project/issues/")
}

func evalGetJSON(t *testing.T, handler gohttp.Handler, pat, path string) []json.RawMessage {
	t.Helper()
	rec := evalGET(t, handler, pat, path)
	if rec.Code != gohttp.StatusOK {
		t.Fatalf("GET %s: status=%d body=%s", path, rec.Code, rec.Body.String())
	}
	var items []json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return items
}
