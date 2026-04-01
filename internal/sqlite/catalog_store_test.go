package sqlite

import (
	"context"
	"testing"
	"time"

	"urgentry/internal/store"
)

func TestCatalogStore_ReadWrite(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name, created_at) VALUES ('org-1', 'acme', 'Acme', ?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO teams (id, organization_id, slug, name, created_at) VALUES ('team-1', 'org-1', 'backend', 'Backend', ?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, email, display_name, is_active, created_at, updated_at) VALUES ('user-1', 'dev@example.com', 'Dev', 1, ?, ?)`, time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO organization_members (id, organization_id, user_id, role, created_at) VALUES ('mem-1', 'org-1', 'user-1', 'owner', ?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	cs := NewCatalogStore(db)

	orgs, err := cs.ListOrganizations(ctx)
	if err != nil {
		t.Fatalf("ListOrganizations: %v", err)
	}
	if len(orgs) != 1 || orgs[0].Slug != "acme" {
		t.Fatalf("unexpected orgs: %+v", orgs)
	}

	org, err := cs.GetOrganization(ctx, "acme")
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if org == nil || org.Name != "Acme" {
		t.Fatalf("unexpected organization: %+v", org)
	}

	teams, err := cs.ListTeams(ctx, "acme")
	if err != nil {
		t.Fatalf("ListTeams: %v", err)
	}
	if len(teams) != 1 || teams[0].Slug != "backend" {
		t.Fatalf("unexpected teams: %+v", teams)
	}

	project, err := cs.CreateProject(ctx, "acme", "backend", store.ProjectCreateInput{
		Name:     "Checkout",
		Slug:     "checkout",
		Platform: "go",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if project == nil || project.OrgSlug != "acme" || project.TeamSlug != "backend" {
		t.Fatalf("unexpected created project: %+v", project)
	}

	projects, err := cs.ListProjects(ctx, "acme")
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 || projects[0].Slug != "checkout" {
		t.Fatalf("unexpected projects: %+v", projects)
	}

	gotProject, err := cs.GetProject(ctx, "acme", "checkout")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if gotProject == nil || gotProject.ID != project.ID {
		t.Fatalf("unexpected project lookup: %+v", gotProject)
	}

	settings, err := cs.GetProjectSettings(ctx, "acme", "checkout")
	if err != nil {
		t.Fatalf("GetProjectSettings: %v", err)
	}
	if settings == nil || settings.Slug != "checkout" {
		t.Fatalf("unexpected settings: %+v", settings)
	}
	if len(settings.TelemetryPolicies) == 0 {
		t.Fatalf("expected telemetry policies in settings: %+v", settings)
	}
	if settings.ReplayPolicy.SampleRate != 1 || settings.ReplayPolicy.MaxBytes != 10<<20 {
		t.Fatalf("unexpected default replay policy: %+v", settings.ReplayPolicy)
	}

	updated, err := cs.UpdateProjectSettings(ctx, "acme", "checkout", store.ProjectSettingsUpdate{
		Name:                    "Checkout API",
		Platform:                "python",
		Status:                  "disabled",
		EventRetentionDays:      14,
		AttachmentRetentionDays: 7,
		DebugFileRetentionDays:  30,
		TelemetryPolicies: []store.TelemetryRetentionPolicy{
			{Surface: store.TelemetrySurfaceErrors, RetentionDays: 14, StorageTier: store.TelemetryStorageTierDelete},
			{Surface: store.TelemetrySurfaceReplays, RetentionDays: 7, StorageTier: store.TelemetryStorageTierArchive, ArchiveRetentionDays: 21},
			{Surface: store.TelemetrySurfaceDebugFiles, RetentionDays: 30, StorageTier: store.TelemetryStorageTierArchive, ArchiveRetentionDays: 60},
		},
		ReplayPolicy: store.ReplayIngestPolicy{
			SampleRate:     0.5,
			MaxBytes:       2048,
			ScrubFields:    []string{"email", "token"},
			ScrubSelectors: []string{".secret"},
		},
	})
	if err != nil {
		t.Fatalf("UpdateProjectSettings: %v", err)
	}
	if updated == nil || updated.Name != "Checkout API" || updated.Status != "disabled" || updated.EventRetentionDays != 14 {
		t.Fatalf("unexpected updated settings: %+v", updated)
	}
	if len(updated.TelemetryPolicies) == 0 {
		t.Fatalf("expected telemetry policies on updated settings: %+v", updated)
	}
	var replayPolicy *store.TelemetryRetentionPolicy
	for i := range updated.TelemetryPolicies {
		if updated.TelemetryPolicies[i].Surface == store.TelemetrySurfaceReplays {
			replayPolicy = &updated.TelemetryPolicies[i]
			break
		}
	}
	if replayPolicy == nil || replayPolicy.StorageTier != store.TelemetryStorageTierArchive || replayPolicy.ArchiveRetentionDays != 21 {
		t.Fatalf("unexpected replay policy: %+v", replayPolicy)
	}
	if len(updated.TelemetryPolicies) == 0 {
		t.Fatalf("expected updated telemetry policies: %+v", updated)
	}
	if updated.ReplayPolicy.SampleRate != 0.5 || updated.ReplayPolicy.MaxBytes != 2048 || len(updated.ReplayPolicy.ScrubFields) != 2 || len(updated.ReplayPolicy.ScrubSelectors) != 1 {
		t.Fatalf("unexpected replay ingest policy: %+v", updated.ReplayPolicy)
	}
	if _, err := cs.UpdateProjectSettings(ctx, "acme", "checkout", store.ProjectSettingsUpdate{
		Name:                    "Checkout API",
		Platform:                "python",
		Status:                  "active",
		EventRetentionDays:      14,
		AttachmentRetentionDays: 7,
		DebugFileRetentionDays:  30,
		ReplayPolicy: store.ReplayIngestPolicy{
			SampleRate: 2,
		},
	}); !store.IsInvalidReplayPolicy(err) {
		t.Fatalf("expected invalid replay policy error, got %v", err)
	}

	key, err := cs.CreateProjectKey(ctx, "acme", "checkout", "CI")
	if err != nil {
		t.Fatalf("CreateProjectKey: %v", err)
	}
	if key == nil || key.ProjectID != project.ID || key.Label != "CI" || key.PublicKey == "" {
		t.Fatalf("unexpected key: %+v", key)
	}

	keys, err := cs.ListProjectKeys(ctx, "acme", "checkout")
	if err != nil {
		t.Fatalf("ListProjectKeys: %v", err)
	}
	if len(keys) != 1 || keys[0].PublicKey != key.PublicKey {
		t.Fatalf("unexpected keys: %+v", keys)
	}
	allKeys, err := cs.ListAllProjectKeys(ctx)
	if err != nil {
		t.Fatalf("ListAllProjectKeys: %v", err)
	}
	if len(allKeys) != 1 || allKeys[0].PublicKey != key.PublicKey {
		t.Fatalf("unexpected all keys: %+v", allKeys)
	}

	if _, err := db.Exec(`INSERT INTO auth_audit_logs (id, credential_type, user_id, organization_id, action, request_path, request_method, created_at) VALUES ('log-1', 'session', 'user-1', 'org-1', 'project.updated', '/api/0/projects/acme/checkout/', 'PUT', ?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed audit log: %v", err)
	}
	logs, err := cs.ListOrganizationAuditLogs(ctx, "acme", 10)
	if err != nil {
		t.Fatalf("ListOrganizationAuditLogs: %v", err)
	}
	if len(logs) != 1 || logs[0].Action != "project.updated" || logs[0].UserEmail != "dev@example.com" {
		t.Fatalf("unexpected audit logs: %+v", logs)
	}
}

func TestReleaseStore_CreateRelease(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name, created_at) VALUES ('org-1', 'acme', 'Acme', ?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed org: %v", err)
	}

	store := NewReleaseStore(db)
	release, err := store.CreateRelease(ctx, "acme", "checkout@1.2.3")
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	if release == nil || release.Version != "checkout@1.2.3" || release.OrganizationID != "org-1" {
		t.Fatalf("unexpected release: %+v", release)
	}

	release, err = store.CreateRelease(ctx, "acme", "checkout@1.2.3")
	if err != nil {
		t.Fatalf("CreateRelease idempotent: %v", err)
	}
	if release == nil || release.Version != "checkout@1.2.3" {
		t.Fatalf("unexpected idempotent release: %+v", release)
	}
}

func TestReleaseStore_BindsResolvedNextReleaseIssues(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name, created_at) VALUES ('org-1', 'acme', 'Acme', ?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status, created_at) VALUES ('proj-1', 'org-1', 'checkout', 'Checkout', 'go', 'active', ?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO groups
			(id, project_id, grouping_version, grouping_key, title, culprit, level, status, resolution_substatus, resolved_in_release, first_seen, last_seen, times_seen)
		 VALUES
			('grp-1', 'proj-1', 'urgentry-v1', 'grp-1', 'CheckoutError', 'checkout.go', 'error', 'resolved', 'next_release', '', ?, ?, 1)`,
		time.Now().UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed group: %v", err)
	}

	release, err := NewReleaseStore(db).CreateRelease(ctx, "acme", "checkout@2.0.0")
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	if release == nil {
		t.Fatal("CreateRelease returned nil release")
	}

	var resolvedInRelease string
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(resolved_in_release, '') FROM groups WHERE id = 'grp-1'`).Scan(&resolvedInRelease); err != nil {
		t.Fatalf("load group: %v", err)
	}
	if resolvedInRelease != "checkout@2.0.0" {
		t.Fatalf("resolved_in_release = %q, want checkout@2.0.0", resolvedInRelease)
	}
}
