package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"urgentry/internal/alert"
	attachmentstore "urgentry/internal/attachment"
	memorystore "urgentry/internal/store"
)

func TestWebStoreReadPaths(t *testing.T) {
	ctx := context.Background()
	db := openSQLiteTestDB(t)
	blobs := memorystore.NewMemoryBlobStore()
	attachments := NewAttachmentStore(db, blobs)

	seedImportExportOrganization(t, db)
	seedImportExportRelease(t, db, "1.2.3")
	seedImportExportRelease(t, db, "1.2.4")
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO project_keys (id, project_id, public_key, status, label)
		 VALUES ('settings-key-1', 'test-proj-id', 'test-token-abc', 'active', 'Default')`,
	); err != nil {
		t.Fatalf("insert project key: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	seedWebIssue(t, db, "grp-1", "ImportError: boom", "worker.go", "error", "unresolved", now.Add(-2*time.Hour), now.Add(-time.Hour))
	seedWebIssue(t, db, "grp-2", "NilPointer: boom", "handler.go", "fatal", "resolved", now.Add(-90*time.Minute), now.Add(-30*time.Minute))
	seedWebEvent(t, db, "evt-1", "grp-1", "ImportError: boom", "1.2.3", "production", "user-1", now.Add(-2*time.Hour))
	seedWebEvent(t, db, "evt-2", "grp-1", "ImportError: boom", "1.2.4", "staging", "user-2", now.Add(-time.Hour))
	seedWebEvent(t, db, "evt-3", "grp-2", "NilPointer: boom", "1.2.4", "production", "user-3", now.Add(-30*time.Minute))
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json, user_identifier)
		 VALUES
			('log-1-internal', 'test-proj-id', 'log-1', NULL, '1.2.4', 'production', 'otlp', 'info', 'log', 'api worker started', 'api worker started', 'log.go', ?, '{"logger":"api"}', '{"logger":"api","contexts":{"trace":{"trace_id":"0123456789abcdef0123456789abcdef","span_id":"0123456789abcdef"}}}', 'user-4')`,
		now.Add(-10*time.Minute).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert log event: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO transactions
			(id, project_id, event_id, trace_id, span_id, parent_span_id, transaction_name, op, status, platform, environment, release, start_timestamp, end_timestamp, duration_ms, tags_json, measurements_json, payload_json, created_at)
		 VALUES
			('txn-1', 'test-proj-id', 'txn-event-1', '0123456789abcdef0123456789abcdef', '0123456789abcdef', '', 'checkout', 'http.server', 'ok', 'go', 'production', '1.2.4', ?, ?, 123.4, '{}', '{}', '{}', ?)`,
		now.Add(-5*time.Minute).Format(time.RFC3339Nano),
		now.Add(-5*time.Minute+123*time.Millisecond).Format(time.RFC3339Nano),
		now.Add(-5*time.Minute).Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert transaction: %v", err)
	}

	if err := attachments.SaveAttachment(ctx, &attachmentstore.Attachment{
		ID:          "att-1",
		EventID:     "evt-1",
		ProjectID:   "test-proj-id",
		Name:        "crash.txt",
		ContentType: "text/plain",
		CreatedAt:   now.Add(-25 * time.Minute),
	}, []byte("attachment payload")); err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO user_feedback (id, project_id, event_id, group_id, name, email, comments, created_at)
		 VALUES ('feedback-1', 'test-proj-id', 'evt-1', 'grp-1', 'Jane', 'jane@example.com', 'Something broke', ?)`,
		now.Add(-20*time.Minute).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert feedback: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO release_sessions
			(id, project_id, release_version, environment, session_id, distinct_id, status, errors, started_at, duration, user_agent, attrs_json, created_at, quantity)
		 VALUES
			('sess-1', 'test-proj-id', '1.2.4', 'production', 'session-1', 'user-1', 'ok', 0, ?, 1.2, 'agent', '{}', ?, 2),
			('sess-2', 'test-proj-id', '1.2.4', 'production', 'session-2', 'user-2', 'crashed', 1, ?, 2.0, 'agent', '{}', ?, 1)`,
		now.Add(-40*time.Minute).Format(time.RFC3339),
		now.Add(-40*time.Minute).Format(time.RFC3339),
		now.Add(-10*time.Minute).Format(time.RFC3339),
		now.Add(-10*time.Minute).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert release sessions: %v", err)
	}

	rule := &alert.Rule{
		ID:        "rule-1",
		ProjectID: "test-proj-id",
		Name:      "Slow transaction",
		Status:    "active",
		Conditions: []alert.Condition{
			alert.SlowTransactionCondition(750),
		},
		Actions: []alert.Action{
			{Type: alert.ActionTypeEmail, Target: "ops@example.com"},
			{Type: alert.ActionTypeWebhook, Target: "https://hooks.example.com/alert"},
		},
		CreatedAt: now.Add(-15 * time.Minute),
		UpdatedAt: now.Add(-15 * time.Minute),
	}
	if err := NewAlertStore(db).CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO auth_audit_logs
			(id, credential_type, user_id, organization_id, project_id, action, created_at)
		 VALUES ('audit-1', 'session', 'owner@example.com', 'test-org-id', 'test-proj-id', 'project.updated', ?)`,
		now.Add(-2*time.Minute).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert audit log: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO alert_history (id, rule_id, group_id, event_id, fired_at)
		 VALUES ('history-1', 'rule-1', 'grp-1', 'evt-2', ?)`,
		now.Add(-5*time.Minute).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert alert history: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO notification_deliveries
			(id, project_id, rule_id, group_id, event_id, kind, target, status, attempts, response_status, error, payload_json, created_at, last_attempt_at, delivered_at)
		 VALUES ('delivery-1', 'test-proj-id', 'rule-1', 'grp-1', 'evt-2', 'webhook', 'https://hooks.example.com/alert', 'delivered', 1, 200, '', '{}', ?, ?, ?)`,
		now.Add(-4*time.Minute).Format(time.RFC3339),
		now.Add(-4*time.Minute).Format(time.RFC3339),
		now.Add(-3*time.Minute).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert notification delivery: %v", err)
	}

	ws := NewWebStore(db)

	issues, total, err := ws.ListIssues(ctx, memorystore.IssueListOpts{Limit: 10, Sort: "last_seen"})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 2 || total != 2 {
		t.Fatalf("issues=%d total=%d, want 2/2", len(issues), total)
	}
	issue, err := ws.GetIssue(ctx, "grp-1")
	if err != nil || issue == nil || issue.Title != "ImportError: boom" {
		t.Fatalf("GetIssue = %+v, %v", issue, err)
	}
	events, err := ws.ListIssueEvents(ctx, "grp-1", 10)
	if err != nil || len(events) != 2 {
		t.Fatalf("ListIssueEvents = %d, %v", len(events), err)
	}
	recent, err := ws.ListRecentEvents(ctx, 10)
	if err != nil || len(recent) != 4 {
		t.Fatalf("ListRecentEvents = %d, %v", len(recent), err)
	}
	event, err := ws.GetEvent(ctx, "evt-1")
	if err != nil || event == nil || event.EventID != "evt-1" {
		t.Fatalf("GetEvent = %+v, %v", event, err)
	}
	offsetEvent, err := ws.GetEventAtOffset(ctx, "grp-1", 1)
	if err != nil || offsetEvent == nil || offsetEvent.EventID != "evt-1" {
		t.Fatalf("GetEventAtOffset = %+v, %v", offsetEvent, err)
	}
	if count, err := ws.CountEventsForGroup(ctx, "grp-1"); err != nil || count != 2 {
		t.Fatalf("CountEventsForGroup = %d, %v", count, err)
	}
	if count, err := ws.CountDistinctUsersForGroup(ctx, "grp-1"); err != nil || count != 2 {
		t.Fatalf("CountDistinctUsersForGroup = %d, %v", count, err)
	}
	if count, err := ws.CountEvents(ctx); err != nil || count != 4 {
		t.Fatalf("CountEvents = %d, %v", count, err)
	}
	if count, err := ws.CountGroups(ctx); err != nil || count != 2 {
		t.Fatalf("CountGroups = %d, %v", count, err)
	}
	if count, err := ws.CountGroupsByStatus(ctx, "unresolved"); err != nil || count != 1 {
		t.Fatalf("CountGroupsByStatus = %d, %v", count, err)
	}
	if count, err := ws.CountGroupsSince(ctx, now.Add(-3*time.Hour), ""); err != nil || count != 2 {
		t.Fatalf("CountGroupsSince = %d, %v", count, err)
	}
	if count, err := ws.CountGroupsForEnvironment(ctx, "production", ""); err != nil || count != 2 {
		t.Fatalf("CountGroupsForEnvironment = %d, %v", count, err)
	}
	if count, err := ws.CountSearchGroups(ctx, "all", "ImportError"); err != nil || count != 1 {
		t.Fatalf("CountSearchGroups = %d, %v", count, err)
	}
	if count, err := ws.CountSearchGroupsForEnvironment(ctx, "production", "unresolved", "ImportError"); err != nil || count != 1 {
		t.Fatalf("CountSearchGroupsForEnvironment = %d, %v", count, err)
	}
	if total, unresolved, resolved, ignored, err := ws.CountAllGroupsByStatus(ctx); err != nil || total != 2 || unresolved != 1 || resolved != 1 || ignored != 0 {
		t.Fatalf("CountAllGroupsByStatus = %d/%d/%d/%d err=%v", total, unresolved, resolved, ignored, err)
	}
	if total, unresolved, resolved, ignored, err := ws.CountAllGroupsForEnvironment(ctx, "production"); err != nil || total != 2 || unresolved != 1 || resolved != 1 || ignored != 0 {
		t.Fatalf("CountAllGroupsForEnvironment = %d/%d/%d/%d err=%v", total, unresolved, resolved, ignored, err)
	}
	if count, err := ws.CountEventsSince(ctx, now.Add(-3*time.Hour)); err != nil || count != 4 {
		t.Fatalf("CountEventsSince = %d, %v", count, err)
	}
	if count, err := ws.CountDistinctUsers(ctx); err != nil || count != 4 {
		t.Fatalf("CountDistinctUsers = %d, %v", count, err)
	}
	if count, err := ws.CountDistinctUsersSince(ctx, now.Add(-3*time.Hour)); err != nil || count != 4 {
		t.Fatalf("CountDistinctUsersSince = %d, %v", count, err)
	}
	summary, err := ws.DashboardSummary(ctx, now)
	if err != nil {
		t.Fatalf("DashboardSummary: %v", err)
	}
	if summary.TotalEvents != 4 || summary.UnresolvedGroups != 1 || summary.EventsCurrent != 4 || summary.EventsPrevious != 0 || summary.ErrorsCurrent != 1 || summary.ErrorsPrevious != 0 || summary.UsersTotal != 4 || summary.UsersCurrent != 4 || summary.UsersPrevious != 0 {
		t.Fatalf("DashboardSummary = %+v", summary)
	}
	burning, err := ws.ListBurningIssues(ctx, now, 5)
	if err != nil {
		t.Fatalf("ListBurningIssues: %v", err)
	}
	if len(burning) != 1 || burning[0].ID != "grp-1" || burning[0].Change != 100 {
		t.Fatalf("ListBurningIssues = %+v", burning)
	}
	environments, err := ws.ListEnvironments(ctx)
	if err != nil || len(environments) != 2 {
		t.Fatalf("ListEnvironments = %v, %v", environments, err)
	}
	if counts, err := ws.BatchUserCounts(ctx, []string{"grp-1", "grp-2"}); err != nil || counts["grp-1"] != 2 || counts["grp-2"] != 1 {
		t.Fatalf("BatchUserCounts = %+v, %v", counts, err)
	}
	sparklines, err := ws.BatchSparklines(ctx, []string{"grp-1", "grp-2"}, 4, 24*time.Hour)
	if err != nil || len(sparklines["grp-1"]) != 4 || len(sparklines["grp-2"]) != 4 {
		t.Fatalf("BatchSparklines = %+v, %v", sparklines, err)
	}
	if facets, err := ws.ListTagFacets(ctx, "grp-1"); err != nil || len(facets) == 0 {
		t.Fatalf("ListTagFacets = %+v, %v", facets, err)
	}
	if dist, err := ws.TagDistribution(ctx, "grp-1"); err != nil || len(dist) == 0 {
		t.Fatalf("TagDistribution = %+v, %v", dist, err)
	}
	if chart, err := ws.EventChartData(ctx, "grp-1", 7); err != nil || len(chart) != 7 {
		t.Fatalf("EventChartData = %+v, %v", chart, err)
	}
	if firstAt, err := ws.FirstEventAt(ctx); err != nil || firstAt == nil {
		t.Fatalf("FirstEventAt = %v, %v", firstAt, err)
	}
	if count, err := ws.CountErrorLevelEvents(ctx); err != nil || count != 3 {
		t.Fatalf("CountErrorLevelEvents = %d, %v", count, err)
	}
	if items, err := ws.SearchIssues(ctx, "event.type:error ImportError", 5); err != nil || len(items) != 1 || items[0].ID != "grp-1" {
		t.Fatalf("SearchIssues = %+v, %v", items, err)
	}
	if items, err := ws.SearchDiscoverIssues(ctx, "test-org", "all", "ImportError", 5); err != nil || len(items) != 1 || items[0].ProjectSlug != "test-project" {
		t.Fatalf("SearchDiscoverIssues = %+v, %v", items, err)
	}
	if logs, err := ws.SearchLogs(ctx, "test-org", "api worker", 5); err != nil || len(logs) != 1 || logs[0].EventID != "log-1" {
		t.Fatalf("SearchLogs = %+v, %v", logs, err)
	}
	if txns, err := ws.SearchTransactions(ctx, "test-org", "checkout", 5); err != nil || len(txns) != 1 || txns[0].EventID != "txn-event-1" {
		t.Fatalf("SearchTransactions = %+v, %v", txns, err)
	}
	if logs, err := ws.ListRecentLogs(ctx, "test-org", 5); err != nil || len(logs) != 1 || logs[0].ProjectSlug != "test-project" {
		t.Fatalf("ListRecentLogs = %+v, %v", logs, err)
	}
	if txns, err := ws.ListRecentTransactions(ctx, "test-org", 5); err != nil || len(txns) != 1 || txns[0].ProjectSlug != "test-project" {
		t.Fatalf("ListRecentTransactions = %+v, %v", txns, err)
	}
	first, latest, err := ws.IssueDiffBase(ctx, "grp-1")
	if err != nil || first == nil || latest == nil || first.Release != "1.2.3" || latest.Release != "1.2.4" {
		t.Fatalf("IssueDiffBase = %+v %+v %v", first, latest, err)
	}
	if attachments, err := ws.ListEventAttachments(ctx, "evt-1"); err != nil || len(attachments) != 1 {
		t.Fatalf("ListEventAttachments = %+v, %v", attachments, err)
	}
	if feedback, err := ws.ListFeedback(ctx, 10); err != nil || len(feedback) != 1 {
		t.Fatalf("ListFeedback = %+v, %v", feedback, err)
	}
	releases, err := ws.ListReleases(ctx, 10)
	if err != nil || len(releases) != 2 {
		t.Fatalf("ListReleases = %+v, %v", releases, err)
	}
	var release124 *memorystore.ReleaseRow
	for i := range releases {
		if releases[i].Version == "1.2.4" {
			release124 = &releases[i]
			break
		}
	}
	if release124 == nil || release124.SessionCount == 0 || release124.CrashFreeRate == 0 {
		t.Fatalf("ListReleases health = %+v", release124)
	}
	if projectID, err := ws.DefaultProjectID(ctx); err != nil || projectID != "test-proj-id" {
		t.Fatalf("DefaultProjectID = %q, %v", projectID, err)
	}
	if rules, err := ws.ListAlertRules(ctx, 10); err != nil || len(rules) != 1 || rules[0].Trigger != "slow_transaction" {
		t.Fatalf("ListAlertRules = %+v, %v", rules, err)
	}
	if history, err := ws.ListAlertHistory(ctx, 10); err != nil || len(history) != 1 || history[0].RuleID != "rule-1" {
		t.Fatalf("ListAlertHistory = %+v, %v", history, err)
	}
	if deliveries, err := ws.ListAlertDeliveries(ctx, 10); err != nil || len(deliveries) != 1 || deliveries[0].Status != "delivered" {
		t.Fatalf("ListAlertDeliveries = %+v, %v", deliveries, err)
	}
	settingsOverview, err := ws.SettingsOverview(ctx, 8)
	if err != nil {
		t.Fatalf("SettingsOverview: %v", err)
	}
	if settingsOverview.Project == nil || settingsOverview.Project.ID != "test-proj-id" {
		t.Fatalf("SettingsOverview.Project = %+v", settingsOverview.Project)
	}
	if settingsOverview.EventCount != 4 || settingsOverview.GroupCount != 2 {
		t.Fatalf("SettingsOverview counts = %d/%d, want 4/2", settingsOverview.EventCount, settingsOverview.GroupCount)
	}
	if len(settingsOverview.ProjectKeys) != 1 || settingsOverview.ProjectKeys[0].PublicKey != "test-token-abc" {
		t.Fatalf("SettingsOverview.ProjectKeys = %+v", settingsOverview.ProjectKeys)
	}
	if len(settingsOverview.AuditLogs) != 1 || settingsOverview.AuditLogs[0].Action != "project.updated" {
		t.Fatalf("SettingsOverview.AuditLogs = %+v", settingsOverview.AuditLogs)
	}
	alertsOverview, err := ws.AlertsOverview(ctx, 10, 10, 10)
	if err != nil {
		t.Fatalf("AlertsOverview: %v", err)
	}
	if alertsOverview.DefaultProjectID != "test-proj-id" {
		t.Fatalf("AlertsOverview.DefaultProjectID = %q", alertsOverview.DefaultProjectID)
	}
	if len(alertsOverview.Rules) != 1 || len(alertsOverview.History) != 1 || len(alertsOverview.Deliveries) != 1 {
		t.Fatalf("AlertsOverview = %+v", alertsOverview)
	}
}

func TestWebStoreBatchSparklinesUsesCapturedNow(t *testing.T) {
	ctx := context.Background()
	db := openSQLiteTestDB(t)
	now := time.Date(2026, time.January, 2, 12, 0, 0, 0, time.UTC)
	seedWebIssue(t, db, "grp-edge", "Edge case", "worker.go", "error", "unresolved", now, now)
	seedWebEvent(t, db, "evt-edge-1", "grp-edge", "Edge case", "1.0.0", "production", "user-1", now.Add(-24*time.Hour))
	seedWebEvent(t, db, "evt-edge-2", "grp-edge", "Edge case", "1.0.0", "production", "user-2", now.Add(-18*time.Hour+time.Second))

	ws := NewWebStore(db)
	sparklines, err := ws.batchSparklines(ctx, []string{"grp-edge"}, 4, 24*time.Hour, now)
	if err != nil {
		t.Fatalf("batchSparklines: %v", err)
	}

	got := sparklines["grp-edge"]
	want := []int{1, 1, 0, 0}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bucket %d = %d, want %d (full=%v)", i, got[i], want[i], got)
		}
	}
}

func seedWebIssue(t *testing.T, db *sql.DB, id, title, culprit, level, status string, firstSeen, lastSeen time.Time) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen, short_id, assignee, priority)
		 VALUES (?, 'test-proj-id', 'urgentry-v1', ?, ?, ?, ?, ?, ?, ?, 1, 1, 'owner@example.com', 1)`,
		id, id, title, culprit, level, status, firstSeen.Format(time.RFC3339), lastSeen.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert group %s: %v", id, err)
	}
}

func seedWebEvent(t *testing.T, db *sql.DB, eventID, groupID, title, release, environment, userID string, occurredAt time.Time) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"event_id": eventID, "message": title})
	if err != nil {
		t.Fatalf("Marshal payload: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json, user_identifier, ingested_at)
		 VALUES
			(?, 'test-proj-id', ?, ?, ?, ?, 'go', 'error', 'error', ?, ?, 'worker.go', ?, ?, ?, ?, ?)`,
		eventID+"-internal",
		eventID,
		groupID,
		release,
		environment,
		title,
		title+" message",
		occurredAt.Format(time.RFC3339),
		`{"environment":"`+environment+`","release":"`+release+`"}`,
		string(payload),
		userID,
		occurredAt.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert event %s: %v", eventID, err)
	}
}
