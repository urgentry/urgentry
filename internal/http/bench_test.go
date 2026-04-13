package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"urgentry/internal/config"
	"urgentry/internal/issue"
	"urgentry/internal/pipeline"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

func BenchmarkStoreEndpoint(b *testing.B) {
	handler, _, sentryKey := newBenchmarkStoreHandler(b)
	bodies := make([]string, b.N)
	for i := range bodies {
		bodies[i] = fmt.Sprintf(`{"event_id":"%032x","platform":"go","level":"error","exception":{"values":[{"type":"BenchmarkError","value":"store"}]}}`, i+1)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for _, body := range bodies {
		req := httptest.NewRequest(http.MethodPost, "/api/bench-proj/store/?sentry_key="+sentryKey, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
	}
}

func BenchmarkProjectIssuesEndpoint(b *testing.B) {
	handler, pat, _ := newBenchmarkHandler(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/0/projects/bench-org/bench-project/issues/", nil)
		req.Header.Set("Authorization", "Bearer "+pat)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
	}
}

type benchmarkFixture struct {
	db        *sql.DB
	dataDir   string
	pat       string
	sentryKey string
	authStore *sqlite.AuthStore
	keyStore  *sqlite.KeyStore
	jobStore  *sqlite.JobStore
}

func newBenchmarkFixture(b *testing.B) benchmarkFixture {
	b.Helper()

	previousLogger := log.Logger
	log.Logger = zerolog.New(io.Discard)
	b.Cleanup(func() { log.Logger = previousLogger })

	dataDir := b.TempDir()
	db, err := sqlite.Open(dataDir)
	if err != nil {
		b.Fatalf("sqlite.Open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })

	seedBenchmarkProject(b, db)

	authStore := sqlite.NewAuthStore(db)
	bootstrap, err := authStore.EnsureBootstrapAccess(context.Background(), sqlite.BootstrapOptions{
		DefaultOrganizationID: "bench-org-id",
		Email:                 "bench@example.com",
		DisplayName:           "Benchmark",
		Password:              "benchmark-password-123",
		PersonalAccessToken:   "gpat_benchmark_token",
	})
	if err != nil {
		b.Fatalf("EnsureBootstrapAccess: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO project_keys (id, project_id, public_key, secret_key, status, label) VALUES ('bench-key', 'bench-proj', 'bench-public-key', 'bench-secret-key', 'active', 'Benchmark')`); err != nil {
		b.Fatalf("seed project key: %v", err)
	}

	return benchmarkFixture{
		db:        db,
		dataDir:   dataDir,
		pat:       bootstrap.PAT,
		sentryKey: "bench-public-key",
		authStore: authStore,
		keyStore:  sqlite.NewKeyStore(db),
		jobStore:  sqlite.NewJobStore(db),
	}
}

func newBenchmarkHandler(b *testing.B) (http.Handler, string, string) {
	fx := newBenchmarkFixture(b)
	seedBenchmarkIssues(b, fx.db)

	events := sqlite.NewEventStore(fx.db)
	groups := sqlite.NewGroupStore(fx.db)
	blobs := store.NewMemoryBlobStore()
	processor := &issue.Processor{
		Events: events,
		Groups: groups,
		Blobs:  blobs,
	}
	pipe := pipeline.New(processor, 1, 1)
	pipe.Start(context.Background())
	b.Cleanup(pipe.Stop)
	nativeCrashes := sqlite.NewNativeCrashStore(fx.db, blobs, fx.jobStore, 100)

	return newBenchmarkServer(sqliteServerDeps(b, fx.db, fx.dataDir, fx.keyStore, fx.authStore, pipe, blobs, nativeCrashes)), fx.pat, fx.sentryKey
}

func newBenchmarkStoreHandler(b *testing.B) (http.Handler, string, string) {
	fx := newBenchmarkFixture(b)
	blobs := store.NewMemoryBlobStore()
	return newBenchmarkServer(sqliteServerDeps(b, fx.db, fx.dataDir, fx.keyStore, fx.authStore, nil, blobs, sqlite.NewNativeCrashStore(fx.db, blobs, fx.jobStore, 100))), fx.pat, fx.sentryKey
}

func newBenchmarkServer(deps Deps) http.Handler {
	return NewServer("all", config.Config{
		Env:               "benchmark",
		SessionCookieName: "urgentry_session",
		CSRFCookieName:    "urgentry_csrf",
	}, deps)
}

func seedBenchmarkProject(b *testing.B, db *sql.DB) {
	b.Helper()

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('bench-org-id', 'bench-org', 'Benchmark Org')`); err != nil {
		b.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('bench-proj', 'bench-org-id', 'bench-project', 'Benchmark Project', 'go', 'active')`); err != nil {
		b.Fatalf("seed project: %v", err)
	}
}

func seedBenchmarkIssues(b *testing.B, db *sql.DB) {
	b.Helper()

	baseTime := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 40; i++ {
		groupID := fmt.Sprintf("grp-http-%02d", i)
		title := "HTTP benchmark issue " + fmt.Sprintf("%02d", i)
		now := baseTime.Add(-time.Duration(i) * time.Minute).Format(time.RFC3339)
		if _, err := db.Exec(
			`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen, short_id)
			 VALUES (?, 'bench-proj', 'urgentry-v1', ?, ?, 'handler.go', 'error', 'unresolved', ?, ?, 4, ?)`,
			groupID, groupID, title, now, now, i+1,
		); err != nil {
			b.Fatalf("seed group: %v", err)
		}
		payloadJSON, _ := json.Marshal(map[string]any{"event_id": fmt.Sprintf("evt-http-%02d", i)})
		if _, err := db.Exec(
			`INSERT INTO events
				(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json, user_identifier, ingested_at)
			 VALUES
				(?, 'bench-proj', ?, ?, 'bench@1.0.0', 'production', 'go', 'error', 'error', ?, ?, 'handler.go', ?, '{"environment":"production"}', ?, ?, ?)`,
			fmt.Sprintf("evt-http-%02d-internal", i),
			fmt.Sprintf("evt-http-%02d", i),
			groupID,
			title,
			"HTTP benchmark message",
			now,
			string(payloadJSON),
			fmt.Sprintf("user-%02d", i),
			now,
		); err != nil {
			b.Fatalf("seed event: %v", err)
		}
	}
}
