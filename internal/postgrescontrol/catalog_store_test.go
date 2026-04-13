package postgrescontrol

import (
	"database/sql"
	"testing"

	sharedstore "urgentry/internal/store"
)

func TestCatalogStoreListAndLookup(t *testing.T) {
	t.Parallel()

	store, db := newCatalogTestStore(t)
	seedCatalogReadData(t, db)

	orgs, err := store.ListOrganizations(t.Context())
	if err != nil {
		t.Fatalf("ListOrganizations() error = %v", err)
	}
	if len(orgs) != 2 || orgs[0].Slug != "acme" || orgs[1].Slug != "beta" {
		t.Fatalf("ListOrganizations() = %#v", orgs)
	}

	org, err := store.GetOrganization(t.Context(), "acme")
	if err != nil {
		t.Fatalf("GetOrganization() error = %v", err)
	}
	if org == nil || org.ID != "org-1" {
		t.Fatalf("GetOrganization() = %#v", org)
	}

	teams, err := store.ListTeams(t.Context(), "acme")
	if err != nil {
		t.Fatalf("ListTeams() error = %v", err)
	}
	if len(teams) != 2 || teams[0].Slug != "backend" || teams[1].Slug != "ops" {
		t.Fatalf("ListTeams() = %#v", teams)
	}

	projects, err := store.ListProjects(t.Context(), "acme")
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("ListProjects() len = %d, want 1", len(projects))
	}
	project := projects[0]
	if project.ID != "proj-1" || project.TeamSlug != "backend" || project.EventRetentionDays != 45 || project.AttachRetentionDays != 12 || project.DebugRetentionDays != 365 {
		t.Fatalf("ListProjects() project = %#v", project)
	}

	gotProject, err := store.GetProject(t.Context(), "acme", "api")
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	if gotProject == nil || gotProject.ID != "proj-1" || gotProject.OrgSlug != "acme" {
		t.Fatalf("GetProject() = %#v", gotProject)
	}

	projectKeys, err := store.ListProjectKeys(t.Context(), "acme", "api")
	if err != nil {
		t.Fatalf("ListProjectKeys() error = %v", err)
	}
	if len(projectKeys) != 2 || projectKeys[0].PublicKey != "pub-1" || projectKeys[1].PublicKey != "pub-2" {
		t.Fatalf("ListProjectKeys() = %#v", projectKeys)
	}

	allKeys, err := store.ListAllProjectKeys(t.Context())
	if err != nil {
		t.Fatalf("ListAllProjectKeys() error = %v", err)
	}
	if len(allKeys) != 3 {
		t.Fatalf("ListAllProjectKeys() len = %d, want 3", len(allKeys))
	}
}

func TestCatalogStoreCreateProjectAndKey(t *testing.T) {
	t.Parallel()

	store, db := newCatalogTestStore(t)
	if _, err := db.ExecContext(t.Context(), `
INSERT INTO organizations (id, slug, name, created_at, updated_at) VALUES ('org-1', 'acme', 'Acme', now(), now());
INSERT INTO teams (id, organization_id, slug, name, created_at, updated_at) VALUES ('team-1', 'org-1', 'backend', 'Backend', now(), now());
`); err != nil {
		t.Fatalf("seed create-project data: %v", err)
	}

	project, err := store.CreateProject(t.Context(), "acme", "backend", sharedstore.ProjectCreateInput{
		Name:     "Worker",
		Slug:     "worker",
		Platform: "python",
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	if project == nil || project.Slug != "worker" || project.TeamSlug != "backend" || project.EventRetentionDays != 90 || project.AttachRetentionDays != 30 || project.DebugRetentionDays != 180 {
		t.Fatalf("CreateProject() = %#v", project)
	}

	key, err := store.CreateProjectKey(t.Context(), "acme", "worker", "")
	if err != nil {
		t.Fatalf("CreateProjectKey() error = %v", err)
	}
	if key == nil || key.ProjectID != project.ID || key.Label != "Default" || key.PublicKey == "" || key.SecretKey == "" {
		t.Fatalf("CreateProjectKey() = %#v", key)
	}

	keys, err := store.ListProjectKeys(t.Context(), "acme", "worker")
	if err != nil {
		t.Fatalf("ListProjectKeys() error = %v", err)
	}
	if len(keys) != 1 || keys[0].ID != key.ID {
		t.Fatalf("ListProjectKeys() = %#v", keys)
	}
}

func TestCatalogStoreProjectSettingsAndAuditLogs(t *testing.T) {
	t.Parallel()

	store, db := newCatalogTestStore(t)
	seedCatalogReadData(t, db)

	settings, err := store.GetProjectSettings(t.Context(), "acme", "api")
	if err != nil {
		t.Fatalf("GetProjectSettings() error = %v", err)
	}
	if settings == nil || settings.DefaultEnvironment != "production" || settings.ReplayPolicy.SampleRate != 0.5 || settings.ReplayPolicy.MaxBytes != 2048 {
		t.Fatalf("GetProjectSettings() = %#v", settings)
	}
	if len(settings.TelemetryPolicies) != len(sharedstore.TelemetrySurfaces()) {
		t.Fatalf("GetProjectSettings() telemetry policies = %d", len(settings.TelemetryPolicies))
	}

	updated, err := store.UpdateProjectSettings(t.Context(), "acme", "api", sharedstore.ProjectSettingsUpdate{
		Name:                    "API Renamed",
		Platform:                "go-http",
		Status:                  "disabled",
		EventRetentionDays:      60,
		AttachmentRetentionDays: 20,
		DebugFileRetentionDays:  400,
		TelemetryPolicies: []sharedstore.TelemetryRetentionPolicy{
			{Surface: sharedstore.TelemetrySurfaceErrors, RetentionDays: 60, StorageTier: sharedstore.TelemetryStorageTierDelete},
			{Surface: sharedstore.TelemetrySurfaceAttachments, RetentionDays: 20, StorageTier: sharedstore.TelemetryStorageTierDelete},
			{Surface: sharedstore.TelemetrySurfaceDebugFiles, RetentionDays: 400, StorageTier: sharedstore.TelemetryStorageTierArchive, ArchiveRetentionDays: 900},
			{Surface: sharedstore.TelemetrySurfaceReplays, RetentionDays: 14, StorageTier: sharedstore.TelemetryStorageTierDelete},
		},
		ReplayPolicy: sharedstore.ReplayIngestPolicy{
			SampleRate:     0.75,
			MaxBytes:       4096,
			ScrubFields:    []string{"email", "password"},
			ScrubSelectors: []string{".secret"},
		},
	})
	if err != nil {
		t.Fatalf("UpdateProjectSettings() error = %v", err)
	}
	if updated == nil || updated.Name != "API Renamed" || updated.Platform != "go-http" || updated.Status != "disabled" {
		t.Fatalf("UpdateProjectSettings() = %#v", updated)
	}
	if updated.EventRetentionDays != 60 || updated.AttachmentRetentionDays != 20 || updated.DebugFileRetentionDays != 400 {
		t.Fatalf("UpdateProjectSettings() retentions = %#v", updated)
	}
	if updated.ReplayPolicy.SampleRate != 0.75 || updated.ReplayPolicy.MaxBytes != 4096 || len(updated.ReplayPolicy.ScrubFields) != 2 || len(updated.ReplayPolicy.ScrubSelectors) != 1 {
		t.Fatalf("UpdateProjectSettings() replay policy = %#v", updated.ReplayPolicy)
	}

	logs, err := store.ListOrganizationAuditLogs(t.Context(), "acme", 10)
	if err != nil {
		t.Fatalf("ListOrganizationAuditLogs() error = %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("ListOrganizationAuditLogs() len = %d, want 2", len(logs))
	}
	if logs[0].Action != "pat.created" || logs[0].UserEmail != "owner@example.com" || logs[0].ProjectSlug != "api" {
		t.Fatalf("ListOrganizationAuditLogs()[0] = %#v", logs[0])
	}
	if logs[1].Action != "bootstrap.created" || logs[1].OrganizationSlug != "acme" {
		t.Fatalf("ListOrganizationAuditLogs()[1] = %#v", logs[1])
	}
}

func newCatalogTestStore(t *testing.T) (*CatalogStore, *sql.DB) {
	t.Helper()

	db := openMigratedTestDatabase(t)
	return NewCatalogStore(db), db
}

func seedCatalogReadData(t *testing.T, db *sql.DB) {
	t.Helper()

	if _, err := db.ExecContext(t.Context(), `
INSERT INTO organizations (id, slug, name, created_at, updated_at) VALUES
	('org-1', 'acme', 'Acme', '2026-03-01T10:00:00Z', '2026-03-01T10:00:00Z'),
	('org-2', 'beta', 'Beta', '2026-03-02T10:00:00Z', '2026-03-02T10:00:00Z');
INSERT INTO teams (id, organization_id, slug, name, created_at, updated_at) VALUES
	('team-1', 'org-1', 'backend', 'Backend', '2026-03-01T11:00:00Z', '2026-03-01T11:00:00Z'),
	('team-2', 'org-1', 'ops', 'Ops', '2026-03-01T12:00:00Z', '2026-03-01T12:00:00Z');
INSERT INTO users (id, email, display_name, is_active, created_at, updated_at) VALUES
	('user-1', 'owner@example.com', 'Owner', TRUE, '2026-03-01T09:00:00Z', '2026-03-01T09:00:00Z');
INSERT INTO projects (id, organization_id, team_id, slug, name, platform, status, default_environment, created_at, updated_at) VALUES
	('proj-1', 'org-1', 'team-1', 'api', 'API', 'go', 'active', 'production', '2026-03-01T13:00:00Z', '2026-03-01T13:00:00Z'),
	('proj-2', 'org-2', NULL, 'web', 'Web', 'javascript', 'active', '', '2026-03-02T13:00:00Z', '2026-03-02T13:00:00Z');
INSERT INTO project_keys (id, project_id, public_key, secret_key, status, label, created_at) VALUES
	('key-1', 'proj-1', 'pub-1', 'sec-1', 'active', 'Default', '2026-03-01T14:00:00Z'),
	('key-2', 'proj-1', 'pub-2', 'sec-2', 'disabled', 'Secondary', '2026-03-01T15:00:00Z'),
	('key-3', 'proj-2', 'pub-3', 'sec-3', 'active', 'Beta', '2026-03-02T15:00:00Z');
INSERT INTO telemetry_retention_policies (project_id, surface, retention_days, storage_tier, archive_retention_days, created_at, updated_at) VALUES
	('proj-1', 'errors', 45, 'delete', 0, now(), now()),
	('proj-1', 'attachments', 12, 'delete', 0, now(), now()),
	('proj-1', 'debug_files', 365, 'archive', 730, now(), now());
INSERT INTO project_replay_configs (project_id, sample_rate, max_bytes, scrub_fields_json, scrub_selectors_json, created_at, updated_at) VALUES
	('proj-1', 0.5, 2048, '["email"]'::jsonb, '[".private"]'::jsonb, now(), now());
INSERT INTO auth_audit_logs (id, credential_type, credential_id, user_id, project_id, organization_id, action, request_path, request_method, ip_address, user_agent, created_at) VALUES
	('audit-1', 'bootstrap', '', 'user-1', '', 'org-1', 'bootstrap.created', '/bootstrap', 'POST', '127.0.0.1', 'seed', '2026-03-03T10:00:00Z'),
	('audit-2', 'personal_access_token', 'pat-1', 'user-1', 'proj-1', '', 'pat.created', '/api/0/projects/acme/api/keys/', 'POST', '127.0.0.1', 'seed', '2026-03-03T11:00:00Z'),
	('audit-3', 'personal_access_token', 'pat-2', 'user-1', 'proj-2', '', 'pat.created', '/api/0/projects/beta/web/keys/', 'POST', '127.0.0.1', 'seed', '2026-03-03T12:00:00Z');
`); err != nil {
		t.Fatalf("seed catalog read data: %v", err)
	}
}
