//go:build integration

package compat

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// ---------------------------------------------------------------------------
// 1. TestCRUDProjects
// ---------------------------------------------------------------------------

func TestCRUDProjects(t *testing.T) {
	srv, orgSlug, _, projSlug := crudTestServer(t)
	base := srv.server.URL

	t.Run("ListAllProjects", func(t *testing.T) {
		resp := apiGet(t, base+"/api/0/projects/", srv.pat)
		requireStatus(t, resp, http.StatusOK)
		var projects []map[string]any
		readJSON(t, resp, &projects)
		if len(projects) == 0 {
			t.Fatal("expected at least one project")
		}
	})

	t.Run("GetProjectByOrgAndSlug", func(t *testing.T) {
		resp := apiGet(t, base+"/api/0/projects/"+orgSlug+"/"+projSlug+"/", srv.pat)
		requireStatus(t, resp, http.StatusOK)
		var project map[string]any
		readJSON(t, resp, &project)
		if project["slug"] != projSlug {
			t.Fatalf("expected slug %q, got %q", projSlug, project["slug"])
		}
	})

	t.Run("GetProjectSettings", func(t *testing.T) {
		resp := apiGet(t, base+"/api/0/projects/"+orgSlug+"/"+projSlug+"/settings/", srv.pat)
		requireStatus(t, resp, http.StatusOK)
		var settings map[string]any
		readJSON(t, resp, &settings)
		if settings["slug"] != projSlug {
			t.Fatalf("expected slug %q, got %q", projSlug, settings["slug"])
		}
	})

	t.Run("UpdateProjectSettings", func(t *testing.T) {
		resp := apiPut(t, base+"/api/0/projects/"+orgSlug+"/"+projSlug+"/settings/", srv.pat, map[string]any{
			"name":     "Renamed Project",
			"platform": "python",
			"status":   "active",
		})
		requireStatus(t, resp, http.StatusOK)
		var updated map[string]any
		readJSON(t, resp, &updated)
		if updated["name"] != "Renamed Project" {
			t.Fatalf("expected name %q, got %q", "Renamed Project", updated["name"])
		}
	})

	t.Run("GetProject404", func(t *testing.T) {
		resp := apiGet(t, base+"/api/0/projects/"+orgSlug+"/nonexistent/", srv.pat)
		requireStatus(t, resp, http.StatusNotFound)
		resp.Body.Close()
	})
}

// ---------------------------------------------------------------------------
// 2. TestCRUDOrganizations
// ---------------------------------------------------------------------------

func TestCRUDOrganizations(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	t.Cleanup(srv.close)
	base := srv.server.URL
	orgSlug := "urgentry-org"

	t.Run("ListOrganizations", func(t *testing.T) {
		resp := apiGet(t, base+"/api/0/organizations/", srv.pat)
		requireStatus(t, resp, http.StatusOK)
		var orgs []map[string]any
		readJSON(t, resp, &orgs)
		if len(orgs) == 0 {
			t.Fatal("expected at least one organization")
		}
		found := false
		for _, org := range orgs {
			if org["slug"] == orgSlug {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected org slug %q in list", orgSlug)
		}
	})

	t.Run("GetOrganization", func(t *testing.T) {
		resp := apiGet(t, base+"/api/0/organizations/"+orgSlug+"/", srv.pat)
		requireStatus(t, resp, http.StatusOK)
		var org map[string]any
		readJSON(t, resp, &org)
		if org["slug"] != orgSlug {
			t.Fatalf("expected slug %q, got %q", orgSlug, org["slug"])
		}
	})

	t.Run("GetOrganization404", func(t *testing.T) {
		resp := apiGet(t, base+"/api/0/organizations/nonexistent/", srv.pat)
		requireStatus(t, resp, http.StatusNotFound)
		resp.Body.Close()
	})
}

// ---------------------------------------------------------------------------
// 3. TestCRUDTeams
// ---------------------------------------------------------------------------

func TestCRUDTeams(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	t.Cleanup(srv.close)
	base := srv.server.URL
	orgSlug := "urgentry-org"

	t.Run("CreateTeam", func(t *testing.T) {
		resp := apiPost(t, base+"/api/0/organizations/"+orgSlug+"/teams/", srv.pat, map[string]string{
			"slug": "crud-team",
			"name": "CRUD Team",
		})
		requireStatus(t, resp, http.StatusCreated)
		var team map[string]any
		readJSON(t, resp, &team)
		if team["slug"] != "crud-team" {
			t.Fatalf("expected slug %q, got %q", "crud-team", team["slug"])
		}
	})

	t.Run("ListTeams", func(t *testing.T) {
		resp := apiGet(t, base+"/api/0/organizations/"+orgSlug+"/teams/", srv.pat)
		requireStatus(t, resp, http.StatusOK)
		var teams []map[string]any
		readJSON(t, resp, &teams)
		if len(teams) == 0 {
			t.Fatal("expected at least one team")
		}
		found := false
		for _, team := range teams {
			if team["slug"] == "crud-team" {
				found = true
			}
		}
		if !found {
			t.Fatal("created team not found in list")
		}
	})

	// No PUT /teams/ or DELETE /teams/ endpoints exist in the router.
	t.Run("UpdateTeam", func(t *testing.T) {
		t.Skip("no PUT teams endpoint")
	})

	t.Run("DeleteTeam", func(t *testing.T) {
		t.Skip("no DELETE teams endpoint")
	})
}

// ---------------------------------------------------------------------------
// 4. TestCRUDAlertRules
// ---------------------------------------------------------------------------

func TestCRUDAlertRules(t *testing.T) {
	srv, orgSlug, _, projSlug := crudTestServer(t)
	base := srv.server.URL
	projBase := fmt.Sprintf("%s/api/0/projects/%s/%s", base, orgSlug, projSlug)

	// Create an alert rule.
	var ruleID string
	t.Run("CreateAlertRule", func(t *testing.T) {
		resp := apiPost(t, projBase+"/alerts/", srv.pat, map[string]any{
			"name":        "Test Alert",
			"actionMatch": "all",
		})
		requireStatus(t, resp, http.StatusCreated)
		var rule map[string]any
		readJSON(t, resp, &rule)
		id, ok := rule["id"].(string)
		if !ok || id == "" {
			t.Fatal("expected rule id in response")
		}
		ruleID = id
		if rule["name"] != "Test Alert" {
			t.Fatalf("expected name %q, got %q", "Test Alert", rule["name"])
		}
	})

	t.Run("ListAlertRules", func(t *testing.T) {
		resp := apiGet(t, projBase+"/alerts/", srv.pat)
		requireStatus(t, resp, http.StatusOK)
		var rules []map[string]any
		readJSON(t, resp, &rules)
		if len(rules) == 0 {
			t.Fatal("expected at least one alert rule")
		}
	})

	t.Run("GetAlertRule", func(t *testing.T) {
		resp := apiGet(t, projBase+"/alerts/"+ruleID+"/", srv.pat)
		requireStatus(t, resp, http.StatusOK)
		var rule map[string]any
		readJSON(t, resp, &rule)
		if rule["id"] != ruleID {
			t.Fatalf("expected id %q, got %q", ruleID, rule["id"])
		}
	})

	t.Run("UpdateAlertRule", func(t *testing.T) {
		resp := apiPut(t, projBase+"/alerts/"+ruleID+"/", srv.pat, map[string]any{
			"name":        "Updated Alert",
			"actionMatch": "any",
		})
		requireStatus(t, resp, http.StatusOK)
		var rule map[string]any
		readJSON(t, resp, &rule)
		if rule["name"] != "Updated Alert" {
			t.Fatalf("expected name %q, got %q", "Updated Alert", rule["name"])
		}
	})

	t.Run("DeleteAlertRule", func(t *testing.T) {
		resp := apiDelete(t, projBase+"/alerts/"+ruleID+"/", srv.pat)
		requireStatus(t, resp, http.StatusNoContent)
		resp.Body.Close()

		// Verify it's gone.
		resp = apiGet(t, projBase+"/alerts/"+ruleID+"/", srv.pat)
		requireStatus(t, resp, http.StatusNotFound)
		resp.Body.Close()
	})
}

// ---------------------------------------------------------------------------
// 5. TestCRUDDashboards
// ---------------------------------------------------------------------------

func TestCRUDDashboards(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	t.Cleanup(srv.close)
	base := srv.server.URL
	orgSlug := "urgentry-org"
	dashBase := fmt.Sprintf("%s/api/0/organizations/%s/dashboards", base, orgSlug)

	var dashboardID string
	t.Run("CreateDashboard", func(t *testing.T) {
		resp := apiPost(t, dashBase+"/", srv.pat, map[string]any{
			"title":       "Test Dashboard",
			"description": "A test dashboard",
			"visibility":  "private",
		})
		requireStatus(t, resp, http.StatusCreated)
		var dash map[string]any
		readJSON(t, resp, &dash)
		id, ok := dash["id"].(string)
		if !ok || id == "" {
			t.Fatal("expected dashboard id in response")
		}
		dashboardID = id
		if dash["title"] != "Test Dashboard" {
			t.Fatalf("expected title %q, got %q", "Test Dashboard", dash["title"])
		}
	})

	t.Run("ListDashboards", func(t *testing.T) {
		resp := apiGet(t, dashBase+"/", srv.pat)
		requireStatus(t, resp, http.StatusOK)
		var dashboards []map[string]any
		readJSON(t, resp, &dashboards)
		if len(dashboards) == 0 {
			t.Fatal("expected at least one dashboard")
		}
	})

	t.Run("GetDashboard", func(t *testing.T) {
		resp := apiGet(t, dashBase+"/"+dashboardID+"/", srv.pat)
		requireStatus(t, resp, http.StatusOK)
		var dash map[string]any
		readJSON(t, resp, &dash)
		if dash["id"] != dashboardID {
			t.Fatalf("expected id %q, got %q", dashboardID, dash["id"])
		}
	})

	t.Run("UpdateDashboard", func(t *testing.T) {
		resp := apiPut(t, dashBase+"/"+dashboardID+"/", srv.pat, map[string]any{
			"title":       "Updated Dashboard",
			"description": "Updated description",
			"visibility":  "private",
		})
		requireStatus(t, resp, http.StatusOK)
		var dash map[string]any
		readJSON(t, resp, &dash)
		if dash["title"] != "Updated Dashboard" {
			t.Fatalf("expected title %q, got %q", "Updated Dashboard", dash["title"])
		}
	})

	t.Run("DeleteDashboard", func(t *testing.T) {
		resp := apiDelete(t, dashBase+"/"+dashboardID+"/", srv.pat)
		requireStatus(t, resp, http.StatusNoContent)
		resp.Body.Close()

		// Verify it's gone.
		resp = apiGet(t, dashBase+"/"+dashboardID+"/", srv.pat)
		requireStatus(t, resp, http.StatusNotFound)
		resp.Body.Close()
	})
}

// ---------------------------------------------------------------------------
// 6. TestCRUDReleases
// ---------------------------------------------------------------------------

func TestCRUDReleases(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	t.Cleanup(srv.close)
	base := srv.server.URL
	orgSlug := "urgentry-org"
	relBase := fmt.Sprintf("%s/api/0/organizations/%s/releases", base, orgSlug)

	t.Run("CreateRelease", func(t *testing.T) {
		resp := apiPost(t, relBase+"/", srv.pat, map[string]string{
			"version": "1.0.0",
		})
		requireStatus(t, resp, http.StatusCreated)
		var rel map[string]any
		readJSON(t, resp, &rel)
		if rel["version"] != "1.0.0" {
			t.Fatalf("expected version %q, got %q", "1.0.0", rel["version"])
		}
	})

	t.Run("ListReleases", func(t *testing.T) {
		resp := apiGet(t, relBase+"/", srv.pat)
		requireStatus(t, resp, http.StatusOK)
		var releases []map[string]any
		readJSON(t, resp, &releases)
		if len(releases) == 0 {
			t.Fatal("expected at least one release")
		}
		found := false
		for _, rel := range releases {
			if rel["version"] == "1.0.0" {
				found = true
			}
		}
		if !found {
			t.Fatal("created release not found in list")
		}
	})

	t.Run("GetRelease", func(t *testing.T) {
		resp := apiGet(t, relBase+"/1.0.0/", srv.pat)
		requireStatus(t, resp, http.StatusOK)
		var rel map[string]any
		readJSON(t, resp, &rel)
		if rel["version"] != "1.0.0" {
			t.Fatalf("expected version %q, got %q", "1.0.0", rel["version"])
		}
	})
}

// ---------------------------------------------------------------------------
// 7. TestCRUDSavedSearches
// ---------------------------------------------------------------------------

func TestCRUDSavedSearches(t *testing.T) {
	t.Skip("no saved searches API endpoints exist in the router")
}

// ---------------------------------------------------------------------------
// 8. TestCRUDProjectKeys
// ---------------------------------------------------------------------------

func TestCRUDProjectKeys(t *testing.T) {
	srv, orgSlug, _, projSlug := crudTestServer(t)
	base := srv.server.URL
	keyBase := fmt.Sprintf("%s/api/0/projects/%s/%s/keys", base, orgSlug, projSlug)

	t.Run("CreateProjectKey", func(t *testing.T) {
		resp := apiPost(t, keyBase+"/", srv.pat, map[string]string{
			"label": "CI Key",
		})
		requireStatus(t, resp, http.StatusCreated)
		var key map[string]any
		readJSON(t, resp, &key)
		if key["label"] != "CI Key" {
			t.Fatalf("expected label %q, got %q", "CI Key", key["label"])
		}
	})

	t.Run("ListProjectKeys", func(t *testing.T) {
		resp := apiGet(t, keyBase+"/", srv.pat)
		requireStatus(t, resp, http.StatusOK)
		var keys []map[string]any
		readJSON(t, resp, &keys)
		if len(keys) == 0 {
			t.Fatal("expected at least one project key after creation")
		}
		// Verify the key has DSN info.
		for _, key := range keys {
			dsn, ok := key["dsn"].(map[string]any)
			if !ok {
				t.Fatal("expected dsn object in key")
			}
			if dsn["public"] == nil || dsn["public"] == "" {
				t.Fatal("expected public DSN in key")
			}
		}
	})
}

// ---------------------------------------------------------------------------
// 9. TestAPIResponseFormat
// ---------------------------------------------------------------------------

func TestAPIResponseFormat(t *testing.T) {
	srv, orgSlug, _, projSlug := crudTestServer(t)
	base := srv.server.URL

	listEndpoints := []struct {
		name string
		url  string
	}{
		{"ListOrganizations", "/api/0/organizations/"},
		{"ListProjects", "/api/0/projects/"},
		{"ListOrgProjects", "/api/0/organizations/" + orgSlug + "/projects/"},
		{"ListTeams", "/api/0/organizations/" + orgSlug + "/teams/"},
		{"ListKeys", "/api/0/projects/" + orgSlug + "/" + projSlug + "/keys/"},
		{"ListReleases", "/api/0/organizations/" + orgSlug + "/releases/"},
		{"ListDashboards", "/api/0/organizations/" + orgSlug + "/dashboards/"},
	}

	for _, ep := range listEndpoints {
		t.Run("ListReturnsArray_"+ep.name, func(t *testing.T) {
			resp := apiGet(t, base+ep.url, srv.pat)
			requireStatus(t, resp, http.StatusOK)
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			trimmed := bytes.TrimSpace(body)
			if len(trimmed) == 0 || trimmed[0] != '[' {
				t.Fatalf("expected JSON array, got: %s", trimmed)
			}
		})
	}

	// Alert rules returns null when empty (acceptable JSON null), but array
	// after creation. Validate separately by creating one first.
	t.Run("ListReturnsArray_ListAlertRules", func(t *testing.T) {
		// Create an alert rule first so the list is non-empty.
		resp := apiPost(t, base+"/api/0/projects/"+orgSlug+"/"+projSlug+"/alerts/", srv.pat, map[string]any{
			"name":        "Format Check Alert",
			"actionMatch": "all",
		})
		requireStatus(t, resp, http.StatusCreated)
		resp.Body.Close()

		resp = apiGet(t, base+"/api/0/projects/"+orgSlug+"/"+projSlug+"/alerts/", srv.pat)
		requireStatus(t, resp, http.StatusOK)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		trimmed := bytes.TrimSpace(body)
		if len(trimmed) == 0 || trimmed[0] != '[' {
			t.Fatalf("expected JSON array, got: %s", trimmed)
		}
	})

	detailEndpoints := []struct {
		name string
		url  string
	}{
		{"GetOrganization", "/api/0/organizations/" + orgSlug + "/"},
		{"GetProject", "/api/0/projects/" + orgSlug + "/" + projSlug + "/"},
	}

	for _, ep := range detailEndpoints {
		t.Run("DetailReturnsObject_"+ep.name, func(t *testing.T) {
			resp := apiGet(t, base+ep.url, srv.pat)
			requireStatus(t, resp, http.StatusOK)
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			trimmed := bytes.TrimSpace(body)
			if len(trimmed) == 0 || trimmed[0] != '{' {
				t.Fatalf("expected JSON object, got: %s", trimmed)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 10. TestAPIPagination
// ---------------------------------------------------------------------------

func TestAPIPagination(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	t.Cleanup(srv.close)
	base := srv.server.URL
	orgSlug := "urgentry-org"

	// Create multiple releases to exercise pagination.
	for i := 0; i < 3; i++ {
		resp := apiPost(t, base+"/api/0/organizations/"+orgSlug+"/releases/", srv.pat, map[string]string{
			"version": fmt.Sprintf("pag-%d.0.0", i),
		})
		requireStatus(t, resp, http.StatusCreated)
		resp.Body.Close()
	}

	t.Run("LinkHeaderPresent", func(t *testing.T) {
		resp := apiGet(t, base+"/api/0/organizations/"+orgSlug+"/releases/", srv.pat)
		requireStatus(t, resp, http.StatusOK)
		resp.Body.Close()

		link := resp.Header.Get("Link")
		if link == "" {
			t.Fatal("expected Link header for pagination")
		}
		// The Link header should contain rel="previous" and rel="next".
		if !bytes.Contains([]byte(link), []byte(`rel="previous"`)) {
			t.Fatalf("Link header missing rel=previous: %s", link)
		}
		if !bytes.Contains([]byte(link), []byte(`rel="next"`)) {
			t.Fatalf("Link header missing rel=next: %s", link)
		}
	})

	t.Run("PaginationOnProjects", func(t *testing.T) {
		resp := apiGet(t, base+"/api/0/projects/", srv.pat)
		requireStatus(t, resp, http.StatusOK)
		resp.Body.Close()

		link := resp.Header.Get("Link")
		if link == "" {
			t.Fatal("expected Link header for pagination on projects list")
		}
	})

	t.Run("PaginationOnOrganizations", func(t *testing.T) {
		resp := apiGet(t, base+"/api/0/organizations/", srv.pat)
		requireStatus(t, resp, http.StatusOK)
		resp.Body.Close()

		link := resp.Header.Get("Link")
		if link == "" {
			t.Fatal("expected Link header for pagination on organizations list")
		}
	})
}
