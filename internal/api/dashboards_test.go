package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"urgentry/internal/auth"
	"urgentry/internal/sqlite"
)

func createDashboardTestUserPAT(t *testing.T, db *sql.DB, userID, email, displayName, role, token string, scopes []string) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO users (id, email, display_name, is_active, created_at, updated_at) VALUES (?, ?, ?, 1, ?, ?)`,
		userID, email, displayName, now, now,
	); err != nil {
		t.Fatalf("insert user %s: %v", email, err)
	}
	if _, err := db.Exec(
		`INSERT INTO organization_members (id, organization_id, user_id, role, created_at) VALUES (?, 'test-org-id', ?, ?, ?)`,
		"mem-"+userID, userID, role, now,
	); err != nil {
		t.Fatalf("insert org member %s: %v", email, err)
	}
	raw, err := sqlite.NewAuthStore(db).CreatePersonalAccessToken(context.Background(), userID, displayName+" PAT", scopes, nil, token)
	if err != nil {
		t.Fatalf("CreatePersonalAccessToken %s: %v", email, err)
	}
	return raw
}

func TestDashboardAPISharingAndWidgetCRUD(t *testing.T) {
	db := openTestSQLite(t)
	ts, ownerPAT := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	memberPAT := createDashboardTestUserPAT(t, db, "user-member-dashboard", "member-dashboard@example.com", "Member Dashboard", "member", "gpat_member_dashboard", []string{
		auth.ScopeOrgQueryRead,
		auth.ScopeOrgQueryWrite,
	})
	viewerPAT := createDashboardTestUserPAT(t, db, "user-viewer-dashboard", "viewer-dashboard@example.com", "Viewer Dashboard", "viewer", "gpat_viewer_dashboard", []string{
		auth.ScopeOrgQueryRead,
		auth.ScopeOrgQueryWrite,
	})

	memberSearch, err := sqlite.NewSearchStore(db).Save(context.Background(), "user-member-dashboard", "test-org", sqlite.SavedSearchVisibilityPrivate, "Hot issues", "", "ValueError", "all", "", "last_seen", false)
	if err != nil {
		t.Fatalf("Save member search: %v", err)
	}

	createOwner := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/dashboards/", ownerPAT, map[string]any{
		"title":      "Ops",
		"visibility": "private",
		"config": map[string]any{
			"refreshSeconds": 60,
			"filters": map[string]any{
				"environment": "production",
			},
			"annotations": []map[string]any{{
				"level": "warning",
				"text":  "Watch checkout",
			}},
		},
	})
	if createOwner.StatusCode != http.StatusCreated {
		t.Fatalf("owner create status = %d, want 201", createOwner.StatusCode)
	}
	var ownerDash Dashboard
	decodeBody(t, createOwner, &ownerDash)
	if !strings.Contains(string(ownerDash.Config), `"refreshSeconds":60`) || !strings.Contains(string(ownerDash.Config), `"environment":"production"`) {
		t.Fatalf("owner dashboard config = %s", string(ownerDash.Config))
	}

	memberList := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/dashboards/", memberPAT, nil)
	if memberList.StatusCode != http.StatusOK {
		t.Fatalf("member list status = %d, want 200", memberList.StatusCode)
	}
	var dashboards []Dashboard
	decodeBody(t, memberList, &dashboards)
	if len(dashboards) != 0 {
		t.Fatalf("member should not see owner private dashboard: %+v", dashboards)
	}

	viewerCreate := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/dashboards/", viewerPAT, map[string]any{
		"title":      "Viewer dashboard",
		"visibility": "private",
	})
	if viewerCreate.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer create status = %d, want 403", viewerCreate.StatusCode)
	}
	viewerCreate.Body.Close()

	shareOwner := authzJSONRequest(t, ts, http.MethodPut, "/api/0/organizations/test-org/dashboards/"+ownerDash.ID+"/", ownerPAT, map[string]any{
		"title":      "Ops",
		"visibility": "organization",
		"config": map[string]any{
			"refreshSeconds": 300,
			"filters": map[string]any{
				"environment": "production",
				"release":     "1.2.4",
			},
			"annotations": []map[string]any{{
				"level": "critical",
				"text":  "Release watch",
			}},
		},
	})
	if shareOwner.StatusCode != http.StatusOK {
		t.Fatalf("owner share status = %d, want 200", shareOwner.StatusCode)
	}
	decodeBody(t, shareOwner, &ownerDash)
	if !strings.Contains(string(ownerDash.Config), `"refreshSeconds":300`) || !strings.Contains(string(ownerDash.Config), `"release":"1.2.4"`) {
		t.Fatalf("shared owner dashboard config = %s", string(ownerDash.Config))
	}

	memberList = authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/dashboards/", memberPAT, nil)
	if memberList.StatusCode != http.StatusOK {
		t.Fatalf("member shared list status = %d, want 200", memberList.StatusCode)
	}
	decodeBody(t, memberList, &dashboards)
	if len(dashboards) != 1 || dashboards[0].ID != ownerDash.ID {
		t.Fatalf("member shared dashboards = %+v", dashboards)
	}

	memberWidgetDenied := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/dashboards/"+ownerDash.ID+"/widgets/", memberPAT, map[string]any{
		"title": "Should fail",
		"kind":  "table",
		"query": map[string]any{
			"version": 1,
			"dataset": "issues",
			"scope": map[string]any{
				"kind":         "organization",
				"organization": "test-org",
			},
			"where": map[string]any{
				"op":    "=",
				"field": "status",
				"value": "unresolved",
			},
			"limit": 10,
		},
	})
	if memberWidgetDenied.StatusCode != http.StatusForbidden {
		t.Fatalf("member owner-widget status = %d, want 403", memberWidgetDenied.StatusCode)
	}
	memberWidgetDenied.Body.Close()

	createMember := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/dashboards/", memberPAT, map[string]any{
		"title":      "Member dashboard",
		"visibility": "private",
	})
	if createMember.StatusCode != http.StatusCreated {
		t.Fatalf("member create status = %d, want 201", createMember.StatusCode)
	}
	var memberDash Dashboard
	decodeBody(t, createMember, &memberDash)

	invalidWidget := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/dashboards/"+memberDash.ID+"/widgets/", memberPAT, map[string]any{
		"title": "Broken series",
		"kind":  "series",
		"query": map[string]any{
			"version": 1,
			"dataset": "issues",
			"scope": map[string]any{
				"kind":         "organization",
				"organization": "test-org",
			},
			"limit": 10,
		},
	})
	if invalidWidget.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid widget status = %d, want 400", invalidWidget.StatusCode)
	}
	invalidWidget.Body.Close()

	createWidget := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/dashboards/"+memberDash.ID+"/widgets/", memberPAT, map[string]any{
		"title":         "Hot issues",
		"kind":          "table",
		"position":      1,
		"savedSearchId": memberSearch.ID,
		"config": map[string]any{
			"thresholds": map[string]any{
				"warning":   1,
				"critical":  2,
				"direction": "above",
			},
		},
	})
	if createWidget.StatusCode != http.StatusCreated {
		t.Fatalf("create widget status = %d, want 201", createWidget.StatusCode)
	}
	var widget DashboardWidget
	decodeBody(t, createWidget, &widget)
	if widget.QueryVersion != 1 || widget.Query.Dataset != "issues" {
		t.Fatalf("unexpected widget: %+v", widget)
	}
	if !strings.Contains(string(widget.Config), `"critical":2`) {
		t.Fatalf("widget config = %s", string(widget.Config))
	}

	getMember := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/dashboards/"+memberDash.ID+"/", memberPAT, nil)
	if getMember.StatusCode != http.StatusOK {
		t.Fatalf("get member dashboard status = %d, want 200", getMember.StatusCode)
	}
	decodeBody(t, getMember, &memberDash)
	if len(memberDash.Widgets) != 1 || memberDash.Widgets[0].ID != widget.ID {
		t.Fatalf("member dashboard widgets = %+v", memberDash.Widgets)
	}

	updateWidget := authzJSONRequest(t, ts, http.MethodPut, "/api/0/organizations/test-org/dashboards/"+memberDash.ID+"/widgets/"+widget.ID+"/", memberPAT, map[string]any{
		"title":    "Issue series",
		"kind":     "series",
		"position": 2,
		"config": map[string]any{
			"thresholds": map[string]any{
				"warning":   50,
				"critical":  10,
				"direction": "below",
			},
		},
		"query": map[string]any{
			"version": 1,
			"dataset": "issues",
			"scope": map[string]any{
				"kind":         "organization",
				"organization": "test-org",
			},
			"select": []map[string]any{{
				"alias": "count",
				"expr": map[string]any{
					"call": "count",
				},
			}},
			"time_range": map[string]any{
				"kind":  "relative",
				"value": "7d",
			},
			"rollup": map[string]any{
				"interval": "1d",
			},
			"limit": 20,
		},
	})
	if updateWidget.StatusCode != http.StatusOK {
		t.Fatalf("update widget status = %d, want 200", updateWidget.StatusCode)
	}
	decodeBody(t, updateWidget, &widget)
	if widget.Kind != "series" || widget.Position != 2 {
		t.Fatalf("updated widget = %+v", widget)
	}
	if !strings.Contains(string(widget.Config), `"direction":"below"`) {
		t.Fatalf("updated widget config = %s", string(widget.Config))
	}

	deleteDashboard := authzJSONRequest(t, ts, http.MethodDelete, "/api/0/organizations/test-org/dashboards/"+memberDash.ID+"/", memberPAT, nil)
	if deleteDashboard.StatusCode != http.StatusNoContent {
		t.Fatalf("delete dashboard status = %d, want 204", deleteDashboard.StatusCode)
	}
	deleteDashboard.Body.Close()

	getDeleted := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/dashboards/"+memberDash.ID+"/", memberPAT, nil)
	if getDeleted.StatusCode != http.StatusNotFound {
		t.Fatalf("get deleted status = %d, want 404", getDeleted.StatusCode)
	}
	getDeleted.Body.Close()
}
