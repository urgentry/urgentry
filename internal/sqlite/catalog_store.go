package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"urgentry/internal/store"
	"urgentry/pkg/id"
)

// CatalogStore consolidates project/org/team/key/settings reads and writes.
type CatalogStore struct {
	db *sql.DB
}

// NewCatalogStore creates a CatalogStore backed by SQLite.
func NewCatalogStore(db *sql.DB) *CatalogStore {
	return &CatalogStore{db: db}
}

func (s *CatalogStore) ListOrganizations(ctx context.Context) ([]store.Organization, error) {
	return ListOrganizations(ctx, s.db)
}

func (s *CatalogStore) GetOrganization(ctx context.Context, slug string) (*store.Organization, error) {
	return GetOrganization(ctx, s.db, slug)
}

func (s *CatalogStore) ListProjects(ctx context.Context, orgSlug string) ([]store.Project, error) {
	return ListProjects(ctx, s.db, orgSlug)
}

func (s *CatalogStore) GetProject(ctx context.Context, orgSlug, projectSlug string) (*store.Project, error) {
	return GetProject(ctx, s.db, orgSlug, projectSlug)
}

func (s *CatalogStore) ListTeams(ctx context.Context, orgSlug string) ([]store.Team, error) {
	return ListTeams(ctx, s.db, orgSlug)
}

func (s *CatalogStore) ListProjectKeys(ctx context.Context, orgSlug, projectSlug string) ([]store.ProjectKeyMeta, error) {
	return ListProjectKeys(ctx, s.db, orgSlug, projectSlug)
}

func (s *CatalogStore) ListAllProjectKeys(ctx context.Context) ([]store.ProjectKeyMeta, error) {
	return ListAllProjectKeys(ctx, s.db)
}

func (s *CatalogStore) CreateProject(ctx context.Context, orgSlug, teamSlug string, input store.ProjectCreateInput) (*store.Project, error) {
	slug := strings.TrimSpace(input.Slug)
	name := strings.TrimSpace(input.Name)
	platform := strings.TrimSpace(input.Platform)
	if slug == "" || name == "" {
		return nil, nil
	}
	if teamSlug = strings.TrimSpace(teamSlug); teamSlug == "" {
		return nil, nil
	}

	org, err := s.GetOrganization(ctx, orgSlug)
	if err != nil || org == nil {
		return nil, err
	}
	teamExists := false
	teams, err := s.ListTeams(ctx, orgSlug)
	if err != nil {
		return nil, err
	}
	for _, team := range teams {
		if team.Slug == teamSlug {
			teamExists = true
			break
		}
	}
	if !teamExists {
		return nil, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO projects (id, organization_id, team_id, slug, name, platform, status, created_at)
		 VALUES (?, (SELECT id FROM organizations WHERE slug = ?), (SELECT id FROM teams WHERE slug = ? AND organization_id = (SELECT id FROM organizations WHERE slug = ?)), ?, ?, ?, 'active', ?)
		 RETURNING id`,
		id.New(), org.Slug, teamSlug, org.Slug, slug, name, platform, now,
	)
	var projectID string
	if err := row.Scan(&projectID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return s.GetProject(ctx, orgSlug, slug)
}

func (s *CatalogStore) CreateProjectKey(ctx context.Context, orgSlug, projectSlug, label string) (*store.ProjectKeyMeta, error) {
	project, err := s.GetProject(ctx, orgSlug, projectSlug)
	if err != nil || project == nil {
		return nil, err
	}
	now := time.Now().UTC()
	key := store.ProjectKeyMeta{
		ID:          id.New(),
		ProjectID:   project.ID,
		Label:       strings.TrimSpace(label),
		PublicKey:   id.New(),
		SecretKey:   id.New(),
		Status:      "active",
		DateCreated: now,
	}
	if key.Label == "" {
		key.Label = "Default"
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO project_keys (id, project_id, public_key, secret_key, status, label, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		key.ID, key.ProjectID, key.PublicKey, key.SecretKey, key.Status, key.Label, key.DateCreated.Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	return &key, nil
}

func (s *CatalogStore) GetProjectSettings(ctx context.Context, orgSlug, projectSlug string) (*store.ProjectSettings, error) {
	project, err := s.GetProject(ctx, orgSlug, projectSlug)
	if err != nil || project == nil {
		return nil, err
	}
	settings := projectSettingsFromProject(*project)
	settings.TelemetryPolicies, err = listProjectTelemetryPolicies(ctx, s.db, *project)
	if err != nil {
		return nil, err
	}
	settings.ReplayPolicy, err = NewReplayConfigStore(s.db).GetReplayIngestPolicy(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	return settings, nil
}

func (s *CatalogStore) UpdateProjectSettings(ctx context.Context, orgSlug, projectSlug string, update store.ProjectSettingsUpdate) (*store.ProjectSettings, error) {
	project, err := s.GetProject(ctx, orgSlug, projectSlug)
	if err != nil || project == nil {
		return nil, err
	}
	policies, err := mergeTelemetryPolicies(*project, update.TelemetryPolicies)
	if err != nil {
		return nil, err
	}
	for _, policy := range policies {
		switch policy.Surface {
		case store.TelemetrySurfaceErrors:
			update.EventRetentionDays = policy.RetentionDays
		case store.TelemetrySurfaceAttachments:
			update.AttachmentRetentionDays = policy.RetentionDays
		case store.TelemetrySurfaceDebugFiles:
			update.DebugFileRetentionDays = policy.RetentionDays
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx,
		`UPDATE projects
		 SET name = ?, platform = ?, status = ?, event_retention_days = ?, attachment_retention_days = ?, debug_file_retention_days = ?
		 WHERE id = ?`,
		strings.TrimSpace(update.Name),
		strings.TrimSpace(update.Platform),
		strings.TrimSpace(update.Status),
		update.EventRetentionDays,
		update.AttachmentRetentionDays,
		update.DebugFileRetentionDays,
		project.ID,
	); err != nil {
		return nil, err
	}
	updatedProject := *project
	updatedProject.EventRetentionDays = update.EventRetentionDays
	updatedProject.AttachRetentionDays = update.AttachmentRetentionDays
	updatedProject.DebugRetentionDays = update.DebugFileRetentionDays
	if err = upsertProjectTelemetryPolicies(ctx, tx, updatedProject, policies); err != nil {
		return nil, err
	}
	if err = upsertReplayIngestPolicy(ctx, tx, project.ID, update.ReplayPolicy); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetProjectSettings(ctx, orgSlug, projectSlug)
}

func (s *CatalogStore) ListOrganizationAuditLogs(ctx context.Context, orgSlug string, limit int) ([]store.AuditLogEntry, error) {
	return NewAuditStore(s.db).ListOrganizationAuditLogs(ctx, orgSlug, limit)
}

func projectSettingsFromProject(project store.Project) *store.ProjectSettings {
	return &store.ProjectSettings{
		ID:                      project.ID,
		OrganizationSlug:        project.OrgSlug,
		Slug:                    project.Slug,
		Name:                    project.Name,
		Platform:                project.Platform,
		Status:                  project.Status,
		EventRetentionDays:      project.EventRetentionDays,
		AttachmentRetentionDays: project.AttachRetentionDays,
		DebugFileRetentionDays:  project.DebugRetentionDays,
		ReplayPolicy:            DefaultReplayIngestPolicy(),
		DateCreated:             project.DateCreated,
	}
}

func listProjectTelemetryPolicies(ctx context.Context, db *sql.DB, project store.Project) ([]store.TelemetryRetentionPolicy, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT surface, retention_days, storage_tier, COALESCE(archive_retention_days, 0)
		   FROM telemetry_retention_policies
		  WHERE project_id = ?
		  ORDER BY updated_at DESC, surface ASC`,
		project.ID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var policies []store.TelemetryRetentionPolicy
	for rows.Next() {
		var policy store.TelemetryRetentionPolicy
		var surface, tier string
		if err := rows.Scan(&surface, &policy.RetentionDays, &tier, &policy.ArchiveRetentionDays); err != nil {
			return nil, err
		}
		policy.Surface = store.TelemetrySurface(surface)
		policy.StorageTier = store.TelemetryStorageTier(tier)
		policies = append(policies, policy)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return store.CanonicalTelemetryPolicies(policies, project.EventRetentionDays, project.AttachRetentionDays, project.DebugRetentionDays)
}

func mergeTelemetryPolicies(project store.Project, updates []store.TelemetryRetentionPolicy) ([]store.TelemetryRetentionPolicy, error) {
	return store.CanonicalTelemetryPolicies(updates, project.EventRetentionDays, project.AttachRetentionDays, project.DebugRetentionDays)
}

func upsertProjectTelemetryPolicies(ctx context.Context, tx *sql.Tx, project store.Project, policies []store.TelemetryRetentionPolicy) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM telemetry_retention_policies WHERE project_id = ?`, project.ID); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, policy := range policies {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO telemetry_retention_policies
				(project_id, surface, retention_days, storage_tier, archive_retention_days, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			project.ID,
			string(policy.Surface),
			policy.RetentionDays,
			string(policy.StorageTier),
			policy.ArchiveRetentionDays,
			now,
			now,
		); err != nil {
			return err
		}
	}
	return nil
}
