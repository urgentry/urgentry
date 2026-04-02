package api

import (
	"net/http"
	"testing"
	"time"

	"urgentry/internal/alert"
	"urgentry/internal/integration"
	"urgentry/internal/sqlite"
)

func TestAPIOrgMetricAlertRules_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	create := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/alert-rules/", pat, map[string]any{
		"name":          "Errors above 50",
		"projects":      []string{"test-project"},
		"aggregate":     "count()",
		"query":         "",
		"thresholdType": 0,
		"timeWindow":    5,
		"triggers": []map[string]any{
			{"label": "critical", "alertThreshold": 50, "actions": []any{}},
		},
	})
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", create.StatusCode)
	}
	var created alert.MetricAlertRule
	decodeBody(t, create, &created)
	if created.ProjectID != "test-proj-id" || created.Metric != "error_count" || created.TimeWindowSecs != 300 {
		t.Fatalf("unexpected create response: %+v", created)
	}

	list := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/alert-rules/", pat, nil)
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", list.StatusCode)
	}
	var listed []alert.MetricAlertRule
	decodeBody(t, list, &listed)
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("unexpected list response: %+v", listed)
	}

	get := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/alert-rules/"+created.ID+"/", pat, nil)
	if get.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200", get.StatusCode)
	}
	var fetched alert.MetricAlertRule
	decodeBody(t, get, &fetched)
	if fetched.ID != created.ID || fetched.Name != "Errors above 50" {
		t.Fatalf("unexpected get response: %+v", fetched)
	}

	update := authzJSONRequest(t, ts, http.MethodPut, "/api/0/organizations/test-org/alert-rules/"+created.ID+"/", pat, map[string]any{
		"name":          "Errors above 75",
		"projects":      []string{"test-project"},
		"aggregate":     "count()",
		"query":         "",
		"thresholdType": 0,
		"timeWindow":    5,
		"triggers": []map[string]any{
			{"label": "critical", "alertThreshold": 75, "actions": []any{}},
		},
	})
	if update.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, want 200", update.StatusCode)
	}
	var updated alert.MetricAlertRule
	decodeBody(t, update, &updated)
	if updated.Name != "Errors above 75" || updated.Threshold != 75 {
		t.Fatalf("unexpected update response: %+v", updated)
	}

	del := authzJSONRequest(t, ts, http.MethodDelete, "/api/0/organizations/test-org/alert-rules/"+created.ID+"/", pat, nil)
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", del.StatusCode)
	}
	del.Body.Close()
}

func TestAPIOrgMonitorCRUD_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	create := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/monitors/", pat, map[string]any{
		"name":    "Nightly Import",
		"project": "test-project",
		"config": map[string]any{
			"schedule": map[string]any{"type": "interval", "value": 5, "unit": "minute"},
			"timezone": "UTC",
		},
	})
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", create.StatusCode)
	}
	var created Monitor
	decodeBody(t, create, &created)
	if created.Project.Slug != "test-project" || created.Slug != "nightly-import" {
		t.Fatalf("unexpected create response: %+v", created)
	}

	get := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/monitors/nightly-import/", pat, nil)
	if get.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200", get.StatusCode)
	}
	var fetched Monitor
	decodeBody(t, get, &fetched)
	if fetched.ID != created.ID || fetched.Project.Slug != "test-project" {
		t.Fatalf("unexpected get response: %+v", fetched)
	}

	monitors := sqlite.NewMonitorStore(db)
	if _, err := monitors.SaveCheckIn(t.Context(), &sqlite.MonitorCheckIn{
		ProjectID:   "test-proj-id",
		CheckInID:   "org-check-in-1",
		MonitorSlug: "nightly-import",
		Status:      "ok",
		DateCreated: time.Now().UTC(),
	}, &sqlite.MonitorConfig{
		Schedule: sqlite.MonitorSchedule{Type: "interval", Value: 5, Unit: "minute"},
		Timezone: "UTC",
	}); err != nil {
		t.Fatalf("SaveCheckIn: %v", err)
	}

	checkins := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/monitors/nightly-import/checkins/", pat, nil)
	if checkins.StatusCode != http.StatusOK {
		t.Fatalf("checkins status = %d, want 200", checkins.StatusCode)
	}
	var listed []MonitorCheckIn
	decodeBody(t, checkins, &listed)
	if len(listed) != 1 || listed[0].CheckInID != "org-check-in-1" {
		t.Fatalf("unexpected checkins response: %+v", listed)
	}

	update := authzJSONRequest(t, ts, http.MethodPut, "/api/0/organizations/test-org/monitors/nightly-import/", pat, map[string]any{
		"project":  "test-project",
		"is_muted": true,
		"config": map[string]any{
			"schedule": map[string]any{"type": "interval", "value": 5, "unit": "minute"},
			"timezone": "UTC",
		},
	})
	if update.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, want 200", update.StatusCode)
	}
	var updated Monitor
	decodeBody(t, update, &updated)
	if updated.Status != "disabled" || !updated.IsMuted {
		t.Fatalf("unexpected update response: %+v", updated)
	}

	del := authzJSONRequest(t, ts, http.MethodDelete, "/api/0/organizations/test-org/monitors/nightly-import/", pat, nil)
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", del.StatusCode)
	}
	del.Body.Close()
}

func TestAPITeamExternalTeamAlias_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{
		ExternalTeams: sqlite.NewExternalTeamStore(db),
	})
	defer ts.Close()

	create := authzJSONRequest(t, ts, http.MethodPost, "/api/0/teams/test-org/backend/external-teams/", pat, map[string]any{
		"provider":      "github",
		"external_id":   "gh-team-1",
		"external_name": "Backend GitHub Team",
		"integration_id": 1,
	})
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", create.StatusCode)
	}
	var created externalTeamResponse
	decodeBody(t, create, &created)
	if created.TeamSlug != "backend" || created.ExternalID != "gh-team-1" {
		t.Fatalf("unexpected create response: %+v", created)
	}

	list := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/external-teams/", pat, nil)
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", list.StatusCode)
	}
	var listed []externalTeamResponse
	decodeBody(t, list, &listed)
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("unexpected list response: %+v", listed)
	}

	update := authzJSONRequest(t, ts, http.MethodPut, "/api/0/teams/test-org/backend/external-teams/"+created.ID+"/", pat, map[string]any{
		"provider":      "github",
		"external_id":   "gh-team-1",
		"external_name": "Backend GitHub Team Updated",
		"integration_id": 1,
	})
	if update.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, want 200", update.StatusCode)
	}
	var updated externalTeamResponse
	decodeBody(t, update, &updated)
	if updated.TeamSlug != "backend" || updated.ExternalName != "Backend GitHub Team Updated" {
		t.Fatalf("unexpected update response: %+v", updated)
	}

	del := authzJSONRequest(t, ts, http.MethodDelete, "/api/0/teams/test-org/backend/external-teams/"+created.ID+"/", pat, nil)
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", del.StatusCode)
	}
	del.Body.Close()
}

func TestAPISentryAppRoutes_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{
		IntegrationRegistry: integration.NewBuiltinRegistry(),
		IntegrationStore:    sqlite.NewIntegrationConfigStore(db),
	})
	defer ts.Close()

	list := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/sentry-apps/", pat, nil)
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", list.StatusCode)
	}
	var apps []sentryAppResponse
	decodeBody(t, list, &apps)
	if len(apps) == 0 {
		t.Fatal("expected non-empty sentry app list")
	}

	get := authzJSONRequest(t, ts, http.MethodGet, "/api/0/sentry-apps/webhook/", pat, nil)
	if get.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200", get.StatusCode)
	}
	var app sentryAppResponse
	decodeBody(t, get, &app)
	if app.ID != "webhook" || app.Name != "Webhook" {
		t.Fatalf("unexpected app response: %+v", app)
	}

	install := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/integrations/webhook/install", pat, map[string]any{
		"config": map[string]any{"url": "https://example.com/hook"},
	})
	if install.StatusCode != http.StatusCreated {
		t.Fatalf("install status = %d, want 201", install.StatusCode)
	}

	installations := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/sentry-app-installations/", pat, nil)
	if installations.StatusCode != http.StatusOK {
		t.Fatalf("installations status = %d, want 200", installations.StatusCode)
	}
	var items []sentryAppInstallationResponse
	decodeBody(t, installations, &items)
	if len(items) != 1 || items[0].App.ID != "webhook" || items[0].Status != "active" {
		t.Fatalf("unexpected installations response: %+v", items)
	}
}

func TestAPIOrgMetricAlertRules_ProjectValidation_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	resp := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/alert-rules/", pat, map[string]any{
		"name":          "Errors above 50",
		"projects":      []string{"missing-project"},
		"aggregate":     "count()",
		"query":         "",
		"thresholdType": 0,
		"timeWindow":    5,
		"triggers": []map[string]any{
			{"label": "critical", "alertThreshold": 50, "actions": []any{}},
		},
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPIOrgMonitorCreateRequiresProject_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	resp := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/monitors/", pat, map[string]any{
		"name": "Nightly Import",
		"config": map[string]any{
			"schedule": map[string]any{"type": "interval", "value": 5, "unit": "minute"},
		},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPIOrgMonitorUsesCatalogProjectRef_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	monitors := sqlite.NewMonitorStore(db)
	if _, err := monitors.UpsertMonitor(t.Context(), &sqlite.Monitor{
		ID:          "monitor-catalog-ref",
		ProjectID:   "test-proj-id",
		Slug:        "catalog-ref-check",
		Status:      "active",
		DateCreated: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertMonitor: %v", err)
	}

	resp := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/monitors/catalog-ref-check/", pat, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got Monitor
	decodeBody(t, resp, &got)
	if got.Project.ID != "test-proj-id" || got.Project.Slug != "test-project" {
		t.Fatalf("unexpected monitor project ref: %+v", got.Project)
	}
}
