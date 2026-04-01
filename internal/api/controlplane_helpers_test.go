package api

import (
	"context"
	"net/http/httptest"
	"testing"

	"urgentry/internal/controlplane"
	"urgentry/internal/sqlite"
	sharedstore "urgentry/internal/store"
)

type testCatalog struct {
	org     *sharedstore.Organization
	project *sharedstore.Project
}

func (c testCatalog) ListOrganizations(context.Context) ([]sharedstore.Organization, error) {
	return nil, nil
}

func (c testCatalog) GetOrganization(context.Context, string) (*sharedstore.Organization, error) {
	return c.org, nil
}

func (c testCatalog) ListProjects(context.Context, string) ([]sharedstore.Project, error) {
	return nil, nil
}

func (c testCatalog) GetProject(context.Context, string, string) (*sharedstore.Project, error) {
	return c.project, nil
}

func (c testCatalog) ListTeams(context.Context, string) ([]sharedstore.Team, error) {
	return nil, nil
}

func (c testCatalog) ListProjectKeys(context.Context, string, string) ([]sharedstore.ProjectKeyMeta, error) {
	return nil, nil
}

func (c testCatalog) ListAllProjectKeys(context.Context) ([]sharedstore.ProjectKeyMeta, error) {
	return nil, nil
}

func (c testCatalog) CreateProject(context.Context, string, string, sharedstore.ProjectCreateInput) (*sharedstore.Project, error) {
	return nil, nil
}

func (c testCatalog) CreateProjectKey(context.Context, string, string, string) (*sharedstore.ProjectKeyMeta, error) {
	return nil, nil
}

func (c testCatalog) GetProjectKey(context.Context, string, string, string) (*sharedstore.ProjectKeyMeta, error) {
	return nil, nil
}

func (c testCatalog) UpdateProjectKey(context.Context, string, string, string, sharedstore.ProjectKeyUpdate) (*sharedstore.ProjectKeyMeta, error) {
	return nil, nil
}

func (c testCatalog) DeleteProjectKey(context.Context, string, string, string) error {
	return nil
}

func (c testCatalog) GetProjectSettings(context.Context, string, string) (*sharedstore.ProjectSettings, error) {
	return nil, nil
}

func (c testCatalog) UpdateProjectSettings(context.Context, string, string, sharedstore.ProjectSettingsUpdate) (*sharedstore.ProjectSettings, error) {
	return nil, nil
}

func (c testCatalog) ListOrganizationAuditLogs(context.Context, string, int) ([]sharedstore.AuditLogEntry, error) {
	return nil, nil
}

func (c testCatalog) UpdateOrganization(context.Context, string, sharedstore.OrganizationUpdate) (*sharedstore.Organization, error) {
	return nil, nil
}

func (c testCatalog) ListEnvironments(context.Context, string) ([]string, error) {
	return nil, nil
}

func (c testCatalog) ListProjectEnvironments(context.Context, string, string) ([]sharedstore.ProjectEnvironment, error) {
	return nil, nil
}

func (c testCatalog) GetProjectEnvironment(context.Context, string, string, string) (*sharedstore.ProjectEnvironment, error) {
	return nil, nil
}

func (c testCatalog) UpdateProjectEnvironment(_ context.Context, _, _, _ string, _ bool) (*sharedstore.ProjectEnvironment, error) {
	return nil, nil
}

func (c testCatalog) ListProjectTeams(context.Context, string, string) ([]sharedstore.Team, error) {
	return nil, nil
}

func (c testCatalog) AddProjectTeam(context.Context, string, string, string) (*sharedstore.Team, error) {
	return nil, nil
}

func (c testCatalog) RemoveProjectTeam(context.Context, string, string, string) (bool, error) {
	return false, nil
}

func (c testCatalog) DeleteProject(context.Context, string, string) error {
	return nil
}

var _ controlplane.CatalogStore = testCatalog{}

func TestProjectIDFromSlugsFallsBackToControlCatalog(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	req := httptest.NewRequest("GET", "/api/0/projects/acme/checkout/events/", nil)
	req = req.WithContext(context.WithValue(req.Context(), catalogContextKey{}, testCatalog{
		org:     &sharedstore.Organization{ID: "org-123", Slug: "acme", Name: "Acme"},
		project: &sharedstore.Project{ID: "proj-123", OrgSlug: "acme", Slug: "checkout", Name: "Checkout", Platform: "go", Status: "active"},
	}))

	projectID, err := projectIDFromSlugs(req, db, "acme", "checkout")
	if err != nil {
		t.Fatalf("projectIDFromSlugs() error = %v", err)
	}
	if projectID != "proj-123" {
		t.Fatalf("projectIDFromSlugs() = %q, want proj-123", projectID)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM projects WHERE id = 'proj-123'`).Scan(&count); err != nil {
		t.Fatalf("query shadow project: %v", err)
	}
	if count != 1 {
		t.Fatalf("shadow project count = %d, want 1", count)
	}
}

func TestGetOrganizationFromDBFallsBackToControlCatalog(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	req := httptest.NewRequest("GET", "/api/0/organizations/acme/", nil)
	req = req.WithContext(context.WithValue(req.Context(), catalogContextKey{}, testCatalog{
		org: &sharedstore.Organization{ID: "org-123", Slug: "acme", Name: "Acme"},
	}))

	org, err := getOrganizationFromDB(req, db, "acme")
	if err != nil {
		t.Fatalf("getOrganizationFromDB() error = %v", err)
	}
	if org == nil || org.ID != "org-123" {
		t.Fatalf("getOrganizationFromDB() = %#v, want org-123", org)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM organizations WHERE id = 'org-123'`).Scan(&count); err != nil {
		t.Fatalf("query shadow org: %v", err)
	}
	if count != 1 {
		t.Fatalf("shadow org count = %d, want 1", count)
	}
}

func TestResolveTraceScopeUsesControlCatalogWithoutSQLiteShadow(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	req := httptest.NewRequest("GET", "/api/0/projects/acme/checkout/transactions/", nil)
	req.SetPathValue("org_slug", "acme")
	req.SetPathValue("proj_slug", "checkout")
	req = req.WithContext(context.WithValue(req.Context(), catalogContextKey{}, testCatalog{
		org:     &sharedstore.Organization{ID: "org-123", Slug: "acme", Name: "Acme"},
		project: &sharedstore.Project{ID: "proj-123", OrgSlug: "acme", Slug: "checkout", Name: "Checkout", Platform: "go", Status: "active"},
	}))

	rec := httptest.NewRecorder()
	projectID, org, ok := resolveTraceScope(rec, req, db, "acme")
	if !ok {
		t.Fatalf("resolveTraceScope() ok = false, body=%s", rec.Body.String())
	}
	if projectID != "proj-123" {
		t.Fatalf("resolveTraceScope() projectID = %q, want proj-123", projectID)
	}
	if org == nil || org.ID != "org-123" {
		t.Fatalf("resolveTraceScope() org = %#v, want org-123", org)
	}

	var orgCount, projectCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM organizations`).Scan(&orgCount); err != nil {
		t.Fatalf("count organizations: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&projectCount); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if orgCount != 0 || projectCount != 0 {
		t.Fatalf("resolveTraceScope() wrote sqlite shadow rows: orgs=%d projects=%d", orgCount, projectCount)
	}
}
