package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	sharedstore "urgentry/internal/store"
)

func BenchmarkWebStoreListIssues(b *testing.B) {
	ws, ctx, now := newBenchmarkWebStore(b)
	opts := sharedstore.IssueListOpts{
		Filter: "all",
		Sort:   "last_seen",
		Limit:  25,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		issues, total, err := ws.ListIssues(ctx, opts)
		if err != nil {
			b.Fatalf("ListIssues: %v", err)
		}
		if len(issues) == 0 || total == 0 {
			b.Fatalf("ListIssues returned no data at %s", now)
		}
	}
}

func BenchmarkWebStoreDashboardSummary(b *testing.B) {
	ws, ctx, now := newBenchmarkWebStore(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		summary, err := ws.DashboardSummary(ctx, now)
		if err != nil {
			b.Fatalf("DashboardSummary: %v", err)
		}
		if summary.TotalEvents == 0 {
			b.Fatal("DashboardSummary returned no events")
		}
	}
}

func newBenchmarkWebStore(b *testing.B) (*WebStore, context.Context, time.Time) {
	b.Helper()

	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('bench-org', 'bench-org', 'Benchmark Org')`); err != nil {
		b.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('bench-proj', 'bench-org', 'bench-project', 'Benchmark Project', 'go', 'active')`); err != nil {
		b.Fatalf("seed project: %v", err)
	}

	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 250; i++ {
		groupID := fmt.Sprintf("grp-bench-%03d", i)
		title := fmt.Sprintf("Benchmark issue %03d", i)
		firstSeen := now.Add(-time.Duration((i%48)+1) * time.Hour)
		lastSeen := firstSeen.Add(30 * time.Minute)
		status := "unresolved"
		if i%5 == 0 {
			status = "resolved"
		}
		seedWebIssueBenchmark(b, db, groupID, title, status, firstSeen, lastSeen)
		for j := 0; j < 6; j++ {
			eventID := fmt.Sprintf("evt-bench-%03d-%02d", i, j)
			occurredAt := now.Add(-time.Duration((i+j)%72) * time.Hour)
			environment := "production"
			if j%2 == 0 {
				environment = "staging"
			}
			seedWebEventBenchmark(b, db, eventID, groupID, title, environment, fmt.Sprintf("user-%03d", (i+j)%120), occurredAt)
		}
	}

	ws := NewWebStore(db)
	return ws, context.Background(), now
}

func seedWebIssueBenchmark(b *testing.B, db *sql.DB, id, title, status string, firstSeen, lastSeen time.Time) {
	b.Helper()
	if _, err := db.Exec(
		`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen, short_id, assignee, priority)
		 VALUES (?, 'bench-proj', 'urgentry-v1', ?, ?, 'worker.go', 'error', ?, ?, ?, 6, NULL, 'owner@example.com', 1)`,
		id, id, title, status, firstSeen.Format(time.RFC3339), lastSeen.Format(time.RFC3339),
	); err != nil {
		b.Fatalf("insert group %s: %v", id, err)
	}
	_, _ = db.Exec(`UPDATE groups SET short_id = (SELECT COALESCE(MAX(short_id), 0) + 1 FROM groups) WHERE id = ? AND short_id IS NULL`, id)
}

func seedWebEventBenchmark(b *testing.B, db *sql.DB, eventID, groupID, title, environment, userID string, occurredAt time.Time) {
	b.Helper()
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json, user_identifier, ingested_at)
		 VALUES
			(?, 'bench-proj', ?, ?, 'bench@1.0.0', ?, 'go', 'error', 'error', ?, ?, 'worker.go', ?, ?, ?, ?, ?)`,
		eventID+"-internal",
		eventID,
		groupID,
		environment,
		title,
		title+" message",
		occurredAt.Format(time.RFC3339),
		`{"environment":"`+environment+`","release":"bench@1.0.0"}`,
		fmt.Sprintf(`{"event_id":"%s"}`, eventID),
		userID,
		occurredAt.Format(time.RFC3339),
	); err != nil {
		b.Fatalf("insert event %s: %v", eventID, err)
	}
}
