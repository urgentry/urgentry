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

func (s *CatalogStore) UpdateOrganization(ctx context.Context, slug string, update store.OrganizationUpdate) (*store.Organization, error) {
	return UpdateOrganization(ctx, s.db, slug, update)
}

func (s *CatalogStore) ListEnvironments(ctx context.Context, orgSlug string) ([]string, error) {
	return ListOrgEnvironments(ctx, s.db, orgSlug)
}

func (s *CatalogStore) ListProjects(ctx context.Context, orgSlug string) ([]store.Project, error) {
	return ListProjects(ctx, s.db, orgSlug)
}

func (s *CatalogStore) GetProject(ctx context.Context, orgSlug, projectSlug string) (*store.Project, error) {
	return GetProject(ctx, s.db, orgSlug, projectSlug)
}

func (s *CatalogStore) UpdateProject(ctx context.Context, orgSlug, projectSlug string, update store.ProjectUpdate) (*store.Project, error) {
	project, err := s.GetProject(ctx, orgSlug, projectSlug)
	if err != nil || project == nil {
		return nil, err
	}

	name := project.Name
	if update.Name != nil {
		name = strings.TrimSpace(*update.Name)
	}
	slug := project.Slug
	if update.Slug != nil {
		slug = strings.TrimSpace(*update.Slug)
	}
	platform := project.Platform
	if update.Platform != nil {
		platform = strings.TrimSpace(*update.Platform)
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE projects
		 SET slug = ?, name = ?, platform = ?
		 WHERE id = ?`,
		slug, name, platform, project.ID,
	); err != nil {
		return nil, err
	}
	return s.GetProject(ctx, orgSlug, slug)
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

func (s *CatalogStore) GetProjectKey(ctx context.Context, orgSlug, projectSlug, keyID string) (*store.ProjectKeyMeta, error) {
	return GetProjectKey(ctx, s.db, orgSlug, projectSlug, keyID)
}

func (s *CatalogStore) UpdateProjectKey(ctx context.Context, orgSlug, projectSlug, keyID string, update store.ProjectKeyUpdate) (*store.ProjectKeyMeta, error) {
	return UpdateProjectKey(ctx, s.db, orgSlug, projectSlug, keyID, update)
}

func (s *CatalogStore) DeleteProjectKey(ctx context.Context, orgSlug, projectSlug, keyID string) error {
	return DeleteProjectKey(ctx, s.db, orgSlug, projectSlug, keyID)
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

// DeleteProject removes a project and cascades to all dependent rows.
func (s *CatalogStore) DeleteProject(ctx context.Context, orgSlug, projectSlug string) error {
	project, err := s.GetProject(ctx, orgSlug, projectSlug)
	if err != nil {
		return err
	}
	if project == nil {
		return sql.ErrNoRows
	}
	pid := project.ID

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Cascade delete all project-scoped rows. Order matters for FK constraints:
	// children first, then the project row itself.
	cascadeTables := []string{
		"telemetry_retention_policies",
		"project_replay_configs",
		"replay_timeline_items",
		"replay_assets",
		"replay_manifests",
		"profile_samples",
		"profile_stack_frames",
		"profile_stacks",
		"profile_frames",
		"profile_threads",
		"profile_manifests",
		"native_crashes",
		"native_crash_images",
		"native_symbol_sources",
		"spans",
		"transactions",
		"monitor_checkins",
		"monitors",
		"outcomes",
		"release_sessions",
		"debug_files",
		"event_attachments",
		"artifacts",
		"issue_subscriptions",
		"issue_bookmarks",
		"issue_activity",
		"issue_comments",
		"ownership_rules",
		"events",
		"groups",
		"project_keys",
		"alert_history",
		"alert_rules",
		"user_feedback",
		"release_deploys",
		"release_commits",
		"releases",
		"notification_deliveries",
		"notification_outbox",
		"project_automation_tokens",
		"project_memberships",
		"data_forwarding_configs",
		"code_mappings",
		"sampling_rules",
		"uptime_check_results",
		"uptime_monitors",
		"metric_alert_rules",
		"anomaly_events",
		"inbound_filters",
		"project_environments",
		"project_teams",
	}
	for _, table := range cascadeTables {
		if _, execErr := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE project_id = ?", pid); execErr != nil {
			// Table may not exist in all deployments; ignore "no such table" errors.
			if !isNoSuchTable(execErr) {
				err = execErr
				return err
			}
		}
	}

	if _, err = tx.ExecContext(ctx, "DELETE FROM projects WHERE id = ?", pid); err != nil {
		return err
	}
	err = tx.Commit()
	return err
}

// ListProjectEnvironments returns environments observed for a project, merged
// with any explicit project_environments rows that carry visibility state.
func (s *CatalogStore) ListProjectEnvironments(ctx context.Context, orgSlug, projectSlug string) ([]store.ProjectEnvironment, error) {
	project, err := s.GetProject(ctx, orgSlug, projectSlug)
	if err != nil || project == nil {
		return nil, err
	}
	return ListProjectEnvironments(ctx, s.db, project.ID)
}

// GetProjectEnvironment returns a single project environment by name.
func (s *CatalogStore) GetProjectEnvironment(ctx context.Context, orgSlug, projectSlug, envName string) (*store.ProjectEnvironment, error) {
	project, err := s.GetProject(ctx, orgSlug, projectSlug)
	if err != nil || project == nil {
		return nil, err
	}
	return GetProjectEnvironment(ctx, s.db, project.ID, envName)
}

// UpdateProjectEnvironment toggles the isHidden flag for a project environment.
func (s *CatalogStore) UpdateProjectEnvironment(ctx context.Context, orgSlug, projectSlug, envName string, isHidden bool) (*store.ProjectEnvironment, error) {
	project, err := s.GetProject(ctx, orgSlug, projectSlug)
	if err != nil || project == nil {
		return nil, err
	}
	return UpdateProjectEnvironment(ctx, s.db, project.ID, envName, isHidden)
}

// ListProjectTeams returns all teams associated with a project.
func (s *CatalogStore) ListProjectTeams(ctx context.Context, orgSlug, projectSlug string) ([]store.Team, error) {
	project, err := s.GetProject(ctx, orgSlug, projectSlug)
	if err != nil || project == nil {
		return nil, err
	}
	return ListProjectTeams(ctx, s.db, project.ID)
}

// AddProjectTeam associates a team with a project.
func (s *CatalogStore) AddProjectTeam(ctx context.Context, orgSlug, projectSlug, teamSlug string) (*store.Team, error) {
	project, err := s.GetProject(ctx, orgSlug, projectSlug)
	if err != nil || project == nil {
		return nil, err
	}
	return AddProjectTeam(ctx, s.db, orgSlug, project.ID, teamSlug)
}

// RemoveProjectTeam removes a team association from a project.
func (s *CatalogStore) RemoveProjectTeam(ctx context.Context, orgSlug, projectSlug, teamSlug string) (bool, error) {
	project, err := s.GetProject(ctx, orgSlug, projectSlug)
	if err != nil || project == nil {
		return false, err
	}
	return RemoveProjectTeam(ctx, s.db, orgSlug, project.ID, teamSlug)
}

// isNoSuchTable checks if a SQLite error is a "no such table" error.
func isNoSuchTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

// TagValueRow is one distinct tag value with aggregate metadata.
type TagValueRow struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Name      string `json:"name"`
	Count     int    `json:"count"`
	LastSeen  string `json:"lastSeen"`
	FirstSeen string `json:"firstSeen"`
}

// ListTagValues returns distinct values for a tag key within a project.
func ListTagValues(ctx context.Context, db *sql.DB, projectID, tagKey string) ([]TagValueRow, error) {
	query := `SELECT
		json_extract(tags_json, ?) AS tag_val,
		COUNT(*) AS cnt,
		MAX(occurred_at) AS last_seen,
		MIN(occurred_at) AS first_seen
	FROM events
	WHERE project_id = ?
	  AND json_extract(tags_json, ?) IS NOT NULL
	  AND json_extract(tags_json, ?) != ''
	GROUP BY tag_val
	ORDER BY cnt DESC`

	path := "$." + tagKey
	rows, err := db.QueryContext(ctx, query, path, projectID, path, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TagValueRow
	for rows.Next() {
		var r TagValueRow
		if err := rows.Scan(&r.Value, &r.Count, &r.LastSeen, &r.FirstSeen); err != nil {
			return nil, err
		}
		r.Key = tagKey
		r.Name = r.Value
		out = append(out, r)
	}
	return out, rows.Err()
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
