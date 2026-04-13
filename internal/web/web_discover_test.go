package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"urgentry/internal/analyticsreport"
	"urgentry/internal/analyticsservice"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"urgentry/internal/telemetryquery"
	profilefixtures "urgentry/internal/testfixtures/profiles"
)

func TestDiscoverAndLogsPages(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()
	profiles := sqlite.NewProfileStore(db, store.NewMemoryBlobStore())

	insertGroup(t, db, "grp-discover-1", "ImportError: bad input", "main.go", "error", "unresolved")
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json)
		 VALUES
			('evt-discover-log-web-1', 'test-proj', 'evt-discover-log-web-1', NULL, '1.2.4', 'production', 'otlp', 'info', 'log', 'api worker started', 'api worker started', 'log.go', ?, '{"logger":"api"}', '{"logger":"api"}'),
			('evt-discover-txn-web-1', 'test-proj', 'evt-discover-txn-web-1', 'grp-discover-1', '1.2.4', 'production', 'go', 'error', 'transaction', 'checkout', 'checkout', 'txn.go', ?, '{}', '{}')`,
		now, now,
	); err != nil {
		t.Fatalf("insert discover rows: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO transactions
			(id, project_id, event_id, trace_id, span_id, parent_span_id, transaction_name, op, status, platform, environment, release, start_timestamp, end_timestamp, duration_ms, tags_json, measurements_json, payload_json, created_at)
		 VALUES
			('txn-web-1', 'test-proj', 'evt-discover-txn-web-1', '0123456789abcdef0123456789abcdef', '0123456789abcdef', '', 'checkout', 'http.server', 'ok', 'go', 'production', '1.2.4', ?, ?, 123.4, '{}', '{}', '{}', ?)`,
		time.Now().UTC().Add(-5*time.Minute).Format(time.RFC3339Nano),
		time.Now().UTC().Format(time.RFC3339Nano),
		time.Now().UTC().Add(-5*time.Minute).Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert discover transaction: %v", err)
	}
	profilefixtures.Save(t, profiles, "test-proj", profilefixtures.DBHeavy().Spec().
		WithIDs("evt-discover-profile-1", "profile-discover-1").
		WithRelease("1.2.4"))

	resp, err := http.Get(srv.URL + "/discover/?query=ImportError")
	if err != nil {
		t.Fatalf("GET /discover/: %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Discover") ||
		!strings.Contains(body, "ImportError") ||
		!strings.Contains(body, "Query Builder") ||
		!strings.Contains(body, `name="columns"`) ||
		!strings.Contains(body, `name="order_by"`) {
		t.Fatalf("unexpected discover body: %s", body)
	}

	resp, err = http.Get(srv.URL + "/discover/?scope=transactions&query=checkout")
	if err != nil {
		t.Fatalf("GET /discover/?scope=transactions: %v", err)
	}
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("transactions discover status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "/traces/0123456789abcdef0123456789abcdef/") ||
		!strings.Contains(body, "/profiles/profile-discover-1/") {
		t.Fatalf("unexpected transaction discover body: %s", body)
	}

	resp, err = http.Get(srv.URL + "/discover/?scope=transactions&visualization=table&aggregate=count,p95(duration.ms)&group_by=project,transaction&order_by=-p95&time_range=24h")
	if err != nil {
		t.Fatalf("GET expanded discover query: %v", err)
	}
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expanded discover status = %d, want 200", resp.StatusCode)
	}
	for _, snippet := range []string{"<th>project</th>", "<th>transaction</th>", "<th>count</th>", "<th>p95</th>", "checkout"} {
		if !strings.Contains(body, snippet) {
			t.Fatalf("expected expanded discover body to contain %q: %s", snippet, body)
		}
	}

	resp, err = http.Get(srv.URL + "/logs/?query=api")
	if err != nil {
		t.Fatalf("GET /logs/: %v", err)
	}
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "api worker started") {
		t.Fatalf("unexpected logs body: %s", body)
	}

	resp, err = http.Get(srv.URL + "/discover/?query=ImportError&export=json")
	if err != nil {
		t.Fatalf("GET discover export: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("discover export status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("discover export content-type = %q", ct)
	}
	body = getBody(t, resp)
	if !strings.Contains(body, `"viewType":"table"`) || !strings.Contains(body, `"issue.id"`) {
		t.Fatalf("unexpected discover export body: %s", body)
	}

	resp, err = http.Get(srv.URL + "/logs/?query=api&export=csv")
	if err != nil {
		t.Fatalf("GET logs export: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logs export status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/csv") {
		t.Fatalf("logs export content-type = %q", ct)
	}
	body = getBody(t, resp)
	if !strings.Contains(body, "event.id") || !strings.Contains(body, "evt-discover-log-web-1") {
		t.Fatalf("unexpected logs export body: %s", body)
	}
}

func TestDiscoverSavedQueryAndDashboardFlows(t *testing.T) {
	srv, db, sessionToken, csrf := setupAuthorizedTestServer(t)
	defer srv.Close()
	profiles := sqlite.NewProfileStore(db, store.NewMemoryBlobStore())

	insertGroup(t, db, "grp-discover-ui-1", "ImportError: bad input", "main.go", "error", "unresolved")
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json)
		 VALUES
			('evt-discover-ui-log-1', 'test-proj', 'evt-discover-ui-log-1', NULL, '1.2.4', 'production', 'otlp', 'info', 'log', 'api worker started', 'api worker started', 'log.go', ?, '{"logger":"api"}', '{"logger":"api"}'),
			('evt-discover-ui-txn-1', 'test-proj', 'evt-discover-ui-txn-1', 'grp-discover-ui-1', '1.2.4', 'production', 'go', 'error', 'transaction', 'checkout', 'checkout', 'txn.go', ?, '{}', '{}')`,
		now, now,
	); err != nil {
		t.Fatalf("insert discover ui rows: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO transactions
			(id, project_id, event_id, trace_id, span_id, parent_span_id, transaction_name, op, status, platform, environment, release, start_timestamp, end_timestamp, duration_ms, tags_json, measurements_json, payload_json, created_at)
		 VALUES
			('txn-ui-1', 'test-proj', 'evt-discover-ui-txn-1', '0123456789abcdef0123456789abcdef', '0123456789abcdef', '', 'checkout', 'http.server', 'internal_error', 'go', 'production', '1.2.4', ?, ?, 123.4, '{}', '{}', '{}', ?)`,
		time.Now().UTC().Add(-5*time.Minute).Format(time.RFC3339Nano),
		time.Now().UTC().Format(time.RFC3339Nano),
		time.Now().UTC().Add(-5*time.Minute).Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert discover ui transaction: %v", err)
	}
	profilefixtures.Save(t, profiles, "test-proj", profilefixtures.DBHeavy().Spec().
		WithIDs("evt-discover-ui-profile-1", "profile-discover-ui-1").
		WithRelease("1.2.4"))

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}

	resp := sessionRequest(t, client, http.MethodGet, srv.URL+"/discover/", sessionToken, "", "", nil)
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("discover page status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Starter Views") || !strings.Contains(body, "Slow endpoints") || !strings.Contains(body, "Top failing endpoints") {
		t.Fatalf("expected starter analytics views on discover page: %s", body)
	}
	if strings.Contains(body, "Noisy loggers") {
		t.Fatalf("discover page should not render log-only starter view: %s", body)
	}

	resp = sessionRequest(t, client, http.MethodGet, srv.URL+"/discover/starters/slow-endpoints/", sessionToken, "", "", nil)
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("slow endpoints status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Slow endpoints") || !strings.Contains(body, "checkout") || !strings.Contains(body, "123") {
		t.Fatalf("unexpected slow endpoints body: %s", body)
	}

	resp = sessionRequest(t, client, http.MethodGet, srv.URL+"/logs/", sessionToken, "", "", nil)
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logs page status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Starter Views") || !strings.Contains(body, "Noisy loggers") {
		t.Fatalf("expected noisy loggers starter on logs page: %s", body)
	}
	if strings.Contains(body, "Slow endpoints") || strings.Contains(body, "Top failing endpoints") {
		t.Fatalf("logs page should not render discover-only starter views: %s", body)
	}

	resp = sessionRequest(t, client, http.MethodGet, srv.URL+"/logs/starters/noisy-loggers/", sessionToken, "", "", nil)
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("noisy loggers status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Noisy loggers") || !strings.Contains(body, "api") {
		t.Fatalf("unexpected noisy loggers body: %s", body)
	}

	resp = sessionRequest(t, client, http.MethodGet, srv.URL+"/discover/starters/top-failing-endpoints/", sessionToken, "", "", nil)
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("failing endpoints status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Top failing endpoints") || !strings.Contains(body, "checkout") {
		t.Fatalf("unexpected failing endpoints body: %s", body)
	}

	saveForm := url.Values{
		"name":          {"Checkout traces"},
		"description":   {"Primary transaction triage query"},
		"favorite":      {"1"},
		"dataset":       {"transactions"},
		"query":         {"checkout"},
		"visualization": {"table"},
	}
	resp = sessionRequest(t, client, http.MethodPost, srv.URL+"/discover/save-query", sessionToken, csrf, "application/x-www-form-urlencoded", strings.NewReader(saveForm.Encode()))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save query status = %d, want 303", resp.StatusCode)
	}
	savedLocation := resp.Header.Get("Location")
	savedURL, err := url.Parse(savedLocation)
	if err != nil {
		t.Fatalf("parse save redirect: %v", err)
	}
	savedID := savedURL.Query().Get("saved")
	if savedID == "" {
		t.Fatalf("expected saved query id in redirect, got %q", savedLocation)
	}

	resp = sessionRequest(t, client, http.MethodGet, srv.URL+savedLocation, sessionToken, "", "", nil)
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("saved query page status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Checkout traces") || !strings.Contains(body, "Primary transaction triage query") || !strings.Contains(body, "Unfavorite") || !strings.Contains(body, "/traces/0123456789abcdef0123456789abcdef/") || !strings.Contains(body, "/profiles/profile-discover-ui-1/") {
		t.Fatalf("unexpected saved discover body: %s", body)
	}
	if !strings.Contains(body, "Query Explain") || !strings.Contains(body, "Estimated planner cost") {
		t.Fatalf("expected discover explain details in saved query body: %s", body)
	}

	reportForm := url.Values{
		"recipient": {"ops@example.com"},
		"cadence":   {"daily"},
	}
	resp = sessionRequest(t, client, http.MethodPost, srv.URL+"/discover/queries/"+savedID+"/reports", sessionToken, csrf, "application/x-www-form-urlencoded", strings.NewReader(reportForm.Encode()))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("saved query report status = %d, want 303", resp.StatusCode)
	}

	resp = sessionRequest(t, client, http.MethodGet, srv.URL+savedLocation+"&export=json", sessionToken, "", "", nil)
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("saved query export status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("saved query export content-type = %q", ct)
	}
	if !strings.Contains(body, `"trace.id"`) || !strings.Contains(body, `"profile"`) {
		t.Fatalf("unexpected saved query export body: %s", body)
	}

	resp = sessionRequest(t, client, http.MethodPost, srv.URL+"/discover/queries/"+savedID+"/snapshot", sessionToken, csrf, "application/x-www-form-urlencoded", strings.NewReader(""))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("saved query snapshot status = %d, want 303", resp.StatusCode)
	}
	savedSnapshotLocation := resp.Header.Get("Location")
	if !strings.Contains(savedSnapshotLocation, "/analytics/snapshots/") {
		t.Fatalf("unexpected saved snapshot redirect %q", savedSnapshotLocation)
	}
	resp, err = http.Get(srv.URL + savedSnapshotLocation)
	if err != nil {
		t.Fatalf("get saved snapshot: %v", err)
	}
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("saved snapshot status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Frozen analytics snapshot") || !strings.Contains(body, "Checkout traces") {
		t.Fatalf("unexpected saved snapshot body: %s", body)
	}
	resp, err = http.Get(srv.URL + savedSnapshotLocation + "?format=json")
	if err != nil {
		t.Fatalf("get saved snapshot export: %v", err)
	}
	body = getBody(t, resp)
	if !strings.Contains(body, `"profile"`) || !strings.Contains(body, `"trace.id"`) {
		t.Fatalf("unexpected saved snapshot export body: %s", body)
	}

	createDashboardForm := url.Values{
		"title":       {"Team overview"},
		"description": {"Primary triage board"},
		"visibility":  {"private"},
	}
	resp = sessionRequest(t, client, http.MethodPost, srv.URL+"/dashboards/", sessionToken, csrf, "application/x-www-form-urlencoded", strings.NewReader(createDashboardForm.Encode()))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create dashboard status = %d, want 303", resp.StatusCode)
	}
	dashboardLocation := resp.Header.Get("Location")
	if !strings.HasPrefix(dashboardLocation, "/dashboards/") {
		t.Fatalf("unexpected dashboard redirect %q", dashboardLocation)
	}
	dashboardID := strings.TrimSuffix(strings.TrimPrefix(dashboardLocation, "/dashboards/"), "/")

	savedWidgetForm := url.Values{
		"title":           {"Checkout table"},
		"saved_search_id": {savedID},
		"kind":            {"table"},
	}
	resp = sessionRequest(t, client, http.MethodPost, srv.URL+"/dashboards/"+dashboardID+"/widgets", sessionToken, csrf, "application/x-www-form-urlencoded", strings.NewReader(savedWidgetForm.Encode()))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create saved-search widget status = %d, want 303", resp.StatusCode)
	}

	statWidgetForm := url.Values{
		"title":               {"Issue count"},
		"dataset":             {"issues"},
		"query":               {"ImportError"},
		"kind":                {"stat"},
		"aggregate":           {"count"},
		"environment":         {"production"},
		"threshold_warning":   {"1"},
		"threshold_critical":  {"1"},
		"threshold_direction": {"above"},
	}
	resp = sessionRequest(t, client, http.MethodPost, srv.URL+"/dashboards/"+dashboardID+"/widgets", sessionToken, csrf, "application/x-www-form-urlencoded", strings.NewReader(statWidgetForm.Encode()))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create stat widget status = %d, want 303", resp.StatusCode)
	}

	seriesWidgetForm := url.Values{
		"title":       {"Transaction volume"},
		"dataset":     {"transactions"},
		"query":       {"checkout"},
		"kind":        {"series"},
		"aggregate":   {"count"},
		"time_range":  {"24h"},
		"rollup":      {"1h"},
		"environment": {"production"},
	}
	resp = sessionRequest(t, client, http.MethodPost, srv.URL+"/dashboards/"+dashboardID+"/widgets", sessionToken, csrf, "application/x-www-form-urlencoded", strings.NewReader(seriesWidgetForm.Encode()))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create series widget status = %d, want 303", resp.StatusCode)
	}

	resp = sessionRequest(t, client, http.MethodGet, srv.URL+"/dashboards/"+dashboardID+"/", sessionToken, "", "", nil)
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard detail status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Checkout table") || !strings.Contains(body, "Issue count") || !strings.Contains(body, "Transaction volume") || !strings.Contains(body, "profile-discover-ui-1") || !strings.Contains(body, "bucket") {
		t.Fatalf("unexpected dashboard detail body: %s", body)
	}
	if !strings.Contains(body, "Open widget") || !strings.Contains(body, "Saved query") || !strings.Contains(body, "Dashboard widget query") || !strings.Contains(body, "Export live CSV") || !strings.Contains(body, "Create frozen snapshot") {
		t.Fatalf("expected widget contract controls in dashboard detail: %s", body)
	}

	updateForm := url.Values{
		"title":              {"Team overview"},
		"description":        {"Primary triage board"},
		"visibility":         {"organization"},
		"refresh_seconds":    {"60"},
		"filter_environment": {"production"},
		"filter_release":     {"1.2.4"},
		"filter_transaction": {"checkout"},
		"annotations":        {"warning|Watch checkout rollouts\ncritical|Escalate if issue count spikes"},
	}
	resp = sessionRequest(t, client, http.MethodPost, srv.URL+"/dashboards/"+dashboardID+"/update", sessionToken, csrf, "application/x-www-form-urlencoded", strings.NewReader(updateForm.Encode()))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("update dashboard status = %d, want 303", resp.StatusCode)
	}

	duplicateForm := url.Values{"title": {"Team overview copy"}}
	resp = sessionRequest(t, client, http.MethodPost, srv.URL+"/dashboards/"+dashboardID+"/duplicate", sessionToken, csrf, "application/x-www-form-urlencoded", strings.NewReader(duplicateForm.Encode()))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("duplicate dashboard status = %d, want 303", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "/dashboards/") {
		t.Fatalf("unexpected duplicate redirect %q", resp.Header.Get("Location"))
	}

	var widgetID string
	if err := db.QueryRow(`SELECT id FROM dashboard_widgets WHERE dashboard_id = ? AND title = 'Checkout table'`, dashboardID).Scan(&widgetID); err != nil {
		t.Fatalf("lookup widget id: %v", err)
	}
	resp = sessionRequest(t, client, http.MethodGet, srv.URL+"/dashboards/"+dashboardID+"/widgets/"+widgetID+"/", sessionToken, "", "", nil)
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("widget detail status = %d, want 200", resp.StatusCode)
	}
	for _, snippet := range []string{
		"Widget Contract",
		"Saved query",
		"Query Explain",
		"Dashboard filters",
		"Back to dashboard",
		"trace.id",
		"profile",
	} {
		if !strings.Contains(body, snippet) {
			t.Fatalf("expected widget detail body to contain %q: %s", snippet, body)
		}
	}

	widgetReportForm := url.Values{
		"recipient": {"dashboards@example.com"},
		"cadence":   {"weekly"},
	}
	resp = sessionRequest(t, client, http.MethodPost, srv.URL+"/dashboards/"+dashboardID+"/widgets/"+widgetID+"/reports", sessionToken, csrf, "application/x-www-form-urlencoded", strings.NewReader(widgetReportForm.Encode()))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("widget report status = %d, want 303", resp.StatusCode)
	}
	resp = sessionRequest(t, client, http.MethodGet, srv.URL+"/dashboards/"+dashboardID+"/widgets/"+widgetID+"/", sessionToken, "", "", nil)
	body = getBody(t, resp)
	if !strings.Contains(body, "dashboards@example.com") || !strings.Contains(body, "Weekly") {
		t.Fatalf("expected widget report schedule in detail body: %s", body)
	}
	resp = sessionRequest(t, client, http.MethodGet, srv.URL+"/dashboards/"+dashboardID+"/widgets/"+widgetID+"/export?format=csv", sessionToken, "", "", nil)
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("widget export status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/csv") {
		t.Fatalf("widget export content-type = %q", ct)
	}
	if !strings.Contains(body, "trace.id") || !strings.Contains(body, "profile") {
		t.Fatalf("unexpected widget export body: %s", body)
	}
	resp = sessionRequest(t, client, http.MethodPost, srv.URL+"/dashboards/"+dashboardID+"/widgets/"+widgetID+"/snapshot", sessionToken, csrf, "application/x-www-form-urlencoded", strings.NewReader(""))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("widget snapshot status = %d, want 303", resp.StatusCode)
	}
	widgetSnapshotLocation := resp.Header.Get("Location")
	if !strings.Contains(widgetSnapshotLocation, "/analytics/snapshots/") {
		t.Fatalf("unexpected widget snapshot redirect %q", widgetSnapshotLocation)
	}
	resp, err = http.Get(srv.URL + widgetSnapshotLocation)
	if err != nil {
		t.Fatalf("get widget snapshot: %v", err)
	}
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("widget snapshot status = %d, want 200", resp.StatusCode)
	}
	for _, snippet := range []string{
		"Frozen analytics snapshot",
		"Snapshot contract",
		"Saved query",
		"transactions",
		"table",
		"env:production",
		"release:1.2.4",
		"transaction:checkout",
	} {
		if !strings.Contains(body, snippet) {
			t.Fatalf("expected widget snapshot body to contain %q: %s", snippet, body)
		}
	}
	resp, err = http.Get(srv.URL + widgetSnapshotLocation + "?format=csv")
	if err != nil {
		t.Fatalf("get widget snapshot export: %v", err)
	}
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("widget snapshot export status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "trace.id") || !strings.Contains(body, "profile") {
		t.Fatalf("unexpected widget snapshot export body: %s", body)
	}

	reportRunner := &analyticsreport.Runner{
		Schedules: analyticsservice.SQLiteServices(db).ReportSchedules,
		Freezer: &analyticsreport.Freezer{
			Analytics: analyticsservice.SQLiteServices(db),
			Queries:   telemetryquery.NewSQLiteService(db, store.NewMemoryBlobStore()),
		},
		Outbox:     sqlite.NewNotificationOutboxStore(db),
		Deliveries: sqlite.NewNotificationDeliveryStore(db),
		BaseURL:    srv.URL,
	}
	if err := reportRunner.RunDue(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	emails, err := sqlite.NewNotificationOutboxStore(db).ListRecent(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListRecent outbox: %v", err)
	}
	if len(emails) < 2 {
		t.Fatalf("expected scheduled report emails, got %+v", emails)
	}
	if !strings.Contains(emails[0].Body, srv.URL+"/analytics/snapshots/") && !strings.Contains(emails[1].Body, srv.URL+"/analytics/snapshots/") {
		t.Fatalf("expected absolute snapshot URL in report email: %+v", emails)
	}

	resp = sessionRequest(t, client, http.MethodGet, srv.URL+"/discover/queries/"+savedID+"/", sessionToken, "", "", nil)
	body = getBody(t, resp)
	if !strings.Contains(body, "ops@example.com") || !strings.Contains(body, "Open last snapshot") {
		t.Fatalf("expected saved query report state after scheduler run: %s", body)
	}

	resp = sessionRequest(t, client, http.MethodPost, srv.URL+"/dashboards/"+dashboardID+"/widgets/"+widgetID+"/delete", sessionToken, csrf, "application/x-www-form-urlencoded", strings.NewReader(""))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete widget status = %d, want 303", resp.StatusCode)
	}

	resp = sessionRequest(t, client, http.MethodGet, srv.URL+"/dashboards/"+dashboardID+"/", sessionToken, "", "", nil)
	body = getBody(t, resp)
	if strings.Contains(body, "Checkout table") {
		t.Fatalf("expected deleted widget to disappear: %s", body)
	}
	if !strings.Contains(body, `option value="organization" selected`) {
		t.Fatalf("expected updated sharing state in body: %s", body)
	}
	if !strings.Contains(body, "Refresh: 1 minute") || !strings.Contains(body, "env:production") || !strings.Contains(body, "release:1.2.4") || !strings.Contains(body, "transaction:checkout") {
		t.Fatalf("expected dashboard filters and refresh in body: %s", body)
	}
	if !strings.Contains(body, "Watch checkout rollouts") || !strings.Contains(body, "Escalate if issue count spikes") {
		t.Fatalf("expected dashboard annotations in body: %s", body)
	}
	if !strings.Contains(body, "Thresholds: warn &gt;= 1 · critical &gt;= 1") && !strings.Contains(body, "Thresholds: warn >= 1 · critical >= 1") {
		t.Fatalf("expected widget threshold summary in body: %s", body)
	}
	if !strings.Contains(body, "window.location.reload()") {
		t.Fatalf("expected auto-refresh script in body: %s", body)
	}

	resp = sessionRequest(t, client, http.MethodPost, srv.URL+"/dashboards/"+dashboardID+"/delete", sessionToken, csrf, "application/x-www-form-urlencoded", strings.NewReader(""))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete dashboard status = %d, want 303", resp.StatusCode)
	}

	resp = sessionRequest(t, client, http.MethodGet, srv.URL+"/dashboards/", sessionToken, "", "", nil)
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboards list status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Team overview copy") {
		t.Fatalf("expected duplicate dashboard in list: %s", body)
	}
	if strings.Contains(body, "Primary triage board</span>") && strings.Contains(body, "/dashboards/"+dashboardID+"/") {
		t.Fatalf("expected original dashboard to be deleted: %s", body)
	}
}

func TestAnalyticsPagesShowOnboardingGuides(t *testing.T) {
	srv, db, sessionToken, _ := setupAuthorizedTestServer(t)
	defer srv.Close()

	now := time.Now().UTC()
	if _, err := db.Exec(
		`INSERT INTO transactions
			(id, project_id, event_id, trace_id, span_id, parent_span_id, transaction_name, op, status, platform, environment, release, start_timestamp, end_timestamp, duration_ms, tags_json, measurements_json, payload_json, created_at)
		 VALUES
			('txn-onboarding-1', 'test-proj', 'evt-onboarding-1', 'feedfacefeedfacefeedfacefeedface', 'span-onboarding-1', '', 'checkout', 'http.server', 'ok', 'go', 'production', '1.0.0', ?, ?, 95.0, '{}', '{}', '{}', ?)`,
		now.Add(-time.Minute).Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano),
		now.Add(-time.Minute).Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert onboarding transaction: %v", err)
	}

	client := &http.Client{}
	cases := []struct {
		path    string
		snippet string
	}{
		{path: "/discover/", snippet: "Discover is the ad hoc analysis surface."},
		{path: "/logs/", snippet: "Logs gives you raw event context."},
		{path: "/dashboards/", snippet: "Dashboards are where repeat analysis should land."},
		{path: "/replays/", snippet: "Replays help you watch what happened before an error."},
		{path: "/profiles/", snippet: "Profiles show where time was spent inside a request."},
		{path: "/traces/feedfacefeedfacefeedfacefeedface/", snippet: "Trace detail ties transactions, spans, and profiles together."},
	}
	for _, tc := range cases {
		resp := sessionRequest(t, client, http.MethodGet, srv.URL+tc.path, sessionToken, "", "", nil)
		body := getBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", tc.path, resp.StatusCode)
		}
		if !strings.Contains(body, tc.snippet) {
			t.Fatalf("%s body missing %q: %s", tc.path, tc.snippet, body)
		}
	}

	resp := sessionRequest(t, client, http.MethodGet, srv.URL+"/discover/", sessionToken, "", "", nil)
	body := getBody(t, resp)
	for _, label := range []string{
		`aria-label="Open starter view: Slow endpoints"`,
		`aria-label="Open starter view: Top failing endpoints"`,
	} {
		if !strings.Contains(body, label) {
			t.Fatalf("discover body missing %q: %s", label, body)
		}
	}
}

func TestDiscoverPageShowsOrganizationSharedQueries(t *testing.T) {
	srv, db, sessionToken, csrf := setupAuthorizedTestServer(t)
	defer srv.Close()

	searches := sqlite.NewSearchStore(db)
	ctx := context.Background()
	shared, err := searches.Save(ctx, "other-user", "test-org", sqlite.SavedSearchVisibilityOrganization, "Shared checkout", "Shared team search", "checkout", "all", "", "last_seen", false)
	if err != nil {
		t.Fatalf("save shared search: %v", err)
	}
	if _, err := searches.Save(ctx, "other-user", "other-org", sqlite.SavedSearchVisibilityOrganization, "Other org only", "", "blocked", "all", "", "last_seen", false); err != nil {
		t.Fatalf("save cross-org shared search: %v", err)
	}
	if _, err := searches.Save(ctx, "other-user", "test-org", sqlite.SavedSearchVisibilityPrivate, "Private only", "", "blocked", "all", "", "last_seen", false); err != nil {
		t.Fatalf("save foreign private search: %v", err)
	}

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	favoriteForm := url.Values{
		"favorite":  {"1"},
		"return_to": {"/discover/"},
	}
	resp := sessionRequest(t, client, http.MethodPost, srv.URL+"/discover/queries/"+shared.ID+"/favorite", sessionToken, csrf, "application/x-www-form-urlencoded", strings.NewReader(favoriteForm.Encode()))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("favorite query status = %d, want 303", resp.StatusCode)
	}
	resp = sessionRequest(t, client, http.MethodGet, srv.URL+"/discover/", sessionToken, csrf, "", nil)
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("discover status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Shared checkout") || !strings.Contains(body, "Shared team search") || !strings.Contains(body, "shared") || !strings.Contains(body, "Unfavorite") {
		t.Fatalf("expected shared query marker in body: %s", body)
	}
	if strings.Contains(body, "Other org only") {
		t.Fatalf("unexpected cross-org shared query in body: %s", body)
	}
	if strings.Contains(body, "Private only") {
		t.Fatalf("unexpected foreign private query in body: %s", body)
	}
}

func TestDiscoverPageUsesAuthorizedOrgScope(t *testing.T) {
	srv, db, sessionToken, _ := setupAuthorizedTestServer(t)
	defer srv.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	insertGroup(t, db, "grp-member-1", "AllowedError: member org", "member.go", "error", "unresolved")
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name, created_at) VALUES ('other-org', 'other-org', 'Other Org', '2000-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed other org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status, created_at) VALUES ('other-proj', 'other-org', 'other-project', 'Other Project', 'go', 'active', '2000-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed other project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen, short_id)
		 VALUES ('grp-other-1', 'other-proj', 'urgentry-v1', 'grp-other-1', 'ForbiddenError: outsider org', 'other.go', 'error', 'unresolved', ?, ?, 1, 42)`,
		now, now,
	); err != nil {
		t.Fatalf("seed other group: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/discover/?scope=issues&query=Error", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "urgentry_session", Value: sessionToken})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /discover/: %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "AllowedError") {
		t.Fatalf("expected member-org issue in body: %s", body)
	}
	if strings.Contains(body, "ForbiddenError") {
		t.Fatalf("unexpected outsider-org issue in body: %s", body)
	}
}

func TestSavedQueryDetailAndCloneFlows(t *testing.T) {
	srv, db, sessionToken, csrf := setupAuthorizedTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-shared-query-1", "ImportError: bad input", "main.go", "error", "unresolved")

	searches := sqlite.NewSearchStore(db)
	shared, err := searches.Save(context.Background(), "other-user", "test-org", sqlite.SavedSearchVisibilityOrganization, "Shared triage", "Team-wide issue query", "ImportError", "all", "", "last_seen", false)
	if err != nil {
		t.Fatalf("save shared search: %v", err)
	}

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}

	resp := sessionRequest(t, client, http.MethodGet, srv.URL+"/discover/queries/"+shared.ID+"/", sessionToken, "", "", nil)
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d, want 200: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "Shared triage") || !strings.Contains(body, "Team-wide issue query") || !strings.Contains(body, "Create clone") || !strings.Contains(body, "Open query") {
		t.Fatalf("unexpected detail body: %s", body)
	}
	if strings.Contains(body, "Manage query") {
		t.Fatalf("shared query detail should not expose owner controls: %s", body)
	}

	apiResp := sessionRequest(t, client, http.MethodGet, srv.URL+"/api/ui/searches/"+shared.ID, sessionToken, "", "", nil)
	if apiResp.StatusCode != http.StatusOK {
		t.Fatalf("api detail status = %d, want 200", apiResp.StatusCode)
	}
	var detail savedSearchResponse
	if err := json.NewDecoder(apiResp.Body).Decode(&detail); err != nil {
		t.Fatalf("decode api detail: %v", err)
	}
	apiResp.Body.Close()
	if detail.DetailURL != "/discover/queries/"+shared.ID+"/" {
		t.Fatalf("detail url = %q", detail.DetailURL)
	}
	if !strings.Contains(detail.OpenURL, "/discover/?saved="+shared.ID) {
		t.Fatalf("open url = %q", detail.OpenURL)
	}

	cloneForm := url.Values{
		"name":       {"Shared triage copy"},
		"visibility": {"private"},
		"favorite":   {"1"},
	}
	resp = sessionRequest(t, client, http.MethodPost, srv.URL+"/discover/queries/"+shared.ID+"/clone", sessionToken, csrf, "application/x-www-form-urlencoded", strings.NewReader(cloneForm.Encode()))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("clone form status = %d, want 303", resp.StatusCode)
	}
	cloneLocation := resp.Header.Get("Location")
	if !strings.HasPrefix(cloneLocation, "/discover/queries/") {
		t.Fatalf("unexpected clone redirect %q", cloneLocation)
	}

	resp = sessionRequest(t, client, http.MethodGet, srv.URL+cloneLocation, sessionToken, "", "", nil)
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clone detail status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Shared triage copy") || !strings.Contains(body, "Unfavorite") {
		t.Fatalf("unexpected clone detail body: %s", body)
	}

	apiCloneBody := strings.NewReader(`{"name":"Shared triage API copy","visibility":"private","favorite":true}`)
	apiResp = sessionRequest(t, client, http.MethodPost, srv.URL+"/api/ui/searches/clone/"+shared.ID, sessionToken, csrf, "application/json", apiCloneBody)
	if apiResp.StatusCode != http.StatusCreated {
		t.Fatalf("api clone status = %d, want 201", apiResp.StatusCode)
	}
	var cloned savedSearchResponse
	if err := json.NewDecoder(apiResp.Body).Decode(&cloned); err != nil {
		t.Fatalf("decode api clone: %v", err)
	}
	apiResp.Body.Close()
	if cloned.Name != "Shared triage API copy" || !cloned.Favorite {
		t.Fatalf("unexpected api clone response: %+v", cloned)
	}

	var ownerUserID string
	if err := db.QueryRow(`SELECT id FROM users WHERE email = 'owner@example.com'`).Scan(&ownerUserID); err != nil {
		t.Fatalf("lookup owner user id: %v", err)
	}
	owned, err := searches.Save(context.Background(), ownerUserID, "test-org", sqlite.SavedSearchVisibilityPrivate, "Owner query", "Original owner query", "ImportError", "all", "", "last_seen", false)
	if err != nil {
		t.Fatalf("save owner search: %v", err)
	}

	resp = sessionRequest(t, client, http.MethodGet, srv.URL+"/discover/queries/"+owned.ID+"/", sessionToken, "", "", nil)
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner detail status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Manage query") {
		t.Fatalf("owner query detail should expose management controls: %s", body)
	}

	updateQueryForm := url.Values{
		"name":        {"Owner query v2"},
		"description": {"Pinned for triage"},
		"visibility":  {"organization"},
		"tags":        {"team, checkout"},
		"favorite":    {"1"},
	}
	resp = sessionRequest(t, client, http.MethodPost, srv.URL+"/discover/queries/"+owned.ID+"/update", sessionToken, csrf, "application/x-www-form-urlencoded", strings.NewReader(updateQueryForm.Encode()))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("update form status = %d, want 303", resp.StatusCode)
	}

	resp = sessionRequest(t, client, http.MethodGet, srv.URL+"/discover/queries/"+owned.ID+"/", sessionToken, "", "", nil)
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("updated owner detail status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Owner query v2") || !strings.Contains(body, "Pinned for triage") || !strings.Contains(body, "checkout, team") {
		t.Fatalf("unexpected updated owner detail body: %s", body)
	}

	apiUpdateBody := strings.NewReader(`{"name":"Owner query api","description":"Updated over API","visibility":"private","tags":["ops","latency"],"favorite":false}`)
	apiResp = sessionRequest(t, client, http.MethodPut, srv.URL+"/api/ui/searches/"+owned.ID, sessionToken, csrf, "application/json", apiUpdateBody)
	if apiResp.StatusCode != http.StatusOK {
		t.Fatalf("api update status = %d, want 200", apiResp.StatusCode)
	}
	if err := json.NewDecoder(apiResp.Body).Decode(&detail); err != nil {
		t.Fatalf("decode api update: %v", err)
	}
	apiResp.Body.Close()
	if detail.Name != "Owner query api" || detail.Favorite || len(detail.Tags) != 2 {
		t.Fatalf("unexpected api update response: %+v", detail)
	}

	resp = sessionRequest(t, client, http.MethodPost, srv.URL+"/discover/queries/"+owned.ID+"/delete", sessionToken, csrf, "application/x-www-form-urlencoded", strings.NewReader(url.Values{}.Encode()))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete form status = %d, want 303", resp.StatusCode)
	}
	resp = sessionRequest(t, client, http.MethodGet, srv.URL+"/discover/queries/"+owned.ID+"/", sessionToken, "", "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted owner detail status = %d, want 404", resp.StatusCode)
	}
}

func TestDiscoverPageReturnsQueryGuardRateLimit(t *testing.T) {
	srv, db, sessionToken, _ := setupAuthorizedTestServer(t)
	defer srv.Close()

	if _, err := db.Exec(
		`INSERT INTO query_guard_policies
			(organization_id, workload, max_cost_per_request, max_requests_per_window, max_cost_per_window, window_seconds)
		 VALUES ('test-org', ?, 500, 1, 500, 300)`,
		string(sqlite.QueryWorkloadDiscover),
	); err != nil {
		t.Fatalf("seed query guard policy: %v", err)
	}

	client := &http.Client{}
	newDiscoverRequest := func() *http.Request {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/discover/?query=hello", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.AddCookie(&http.Cookie{Name: "urgentry_session", Value: sessionToken})
		return req
	}

	first, err := client.Do(newDiscoverRequest())
	if err != nil {
		t.Fatalf("first /discover/: %v", err)
	}
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d, want 200", first.StatusCode)
	}

	second, err := client.Do(newDiscoverRequest())
	if err != nil {
		t.Fatalf("second /discover/: %v", err)
	}
	body := getBody(t, second)
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want 429", second.StatusCode)
	}
	if second.Header.Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on discover guardrail denial")
	}
	if !strings.Contains(body, "Query quota exhausted for the current window.") || !strings.Contains(body, "Retry after") {
		t.Fatalf("expected discover guard feedback in body: %s", body)
	}
}

func TestDiscoverStarterPageSharesDiscoverGuardRateLimit(t *testing.T) {
	srv, db, sessionToken, _ := setupAuthorizedTestServer(t)
	defer srv.Close()

	if _, err := db.Exec(
		`INSERT INTO query_guard_policies
			(organization_id, workload, max_cost_per_request, max_requests_per_window, max_cost_per_window, window_seconds)
		 VALUES ('test-org', ?, 500, 1, 500, 300)`,
		string(sqlite.QueryWorkloadDiscover),
	); err != nil {
		t.Fatalf("seed query guard policy: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO transactions
			(id, project_id, event_id, trace_id, span_id, parent_span_id, transaction_name, op, status, platform, environment, release, start_timestamp, end_timestamp, duration_ms, tags_json, measurements_json, payload_json, created_at)
		 VALUES
			('txn-guard-1', 'test-proj', 'evt-discover-guard-1', '0123456789abcdef0123456789abcdef', '0123456789abcdef', '', 'checkout', 'http.server', 'internal_error', 'go', 'production', '1.2.4', ?, ?, 123.4, '{}', '{}', '{}', ?)`,
		time.Now().UTC().Add(-5*time.Minute).Format(time.RFC3339Nano),
		time.Now().UTC().Format(time.RFC3339Nano),
		time.Now().UTC().Add(-5*time.Minute).Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("seed transactions: %v", err)
	}

	client := &http.Client{}
	first := sessionRequest(t, client, http.MethodGet, srv.URL+"/discover/?query=hello", sessionToken, "", "", nil)
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first discover status = %d, want 200", first.StatusCode)
	}

	second := sessionRequest(t, client, http.MethodGet, srv.URL+"/discover/starters/slow-endpoints/", sessionToken, "", "", nil)
	body := getBody(t, second)
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("starter status = %d, want 429", second.StatusCode)
	}
	if second.Header.Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on starter discover guardrail denial")
	}
	if !strings.Contains(body, "Query quota exhausted for the current window.") {
		t.Fatalf("expected discover starter guard feedback in body: %s", body)
	}
}

func TestDiscoverPageExplainsUnsupportedFields(t *testing.T) {
	srv, _, sessionToken, _ := setupAuthorizedTestServer(t)
	defer srv.Close()

	client := &http.Client{}
	resp := sessionRequest(t, client, http.MethodGet, srv.URL+"/discover/?dataset=logs&visualization=table&aggregate=count&group_by=transaction", sessionToken, "", "", nil)
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, `unknown field &#34;transaction&#34; for dataset &#34;logs&#34;`) && !strings.Contains(body, `unknown field "transaction" for dataset "logs"`) {
		t.Fatalf("expected unsupported field feedback in body: %s", body)
	}
}
