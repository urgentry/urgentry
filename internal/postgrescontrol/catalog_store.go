package postgrescontrol

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	sharedstore "urgentry/internal/store"
	"urgentry/pkg/id"
)

// CatalogStore exposes org/project/team/key/settings reads for the Postgres control plane.
type CatalogStore struct {
	db *sql.DB
}

// NewCatalogStore creates a Postgres-backed catalog store.
func NewCatalogStore(db *sql.DB) *CatalogStore {
	return &CatalogStore{db: db}
}

func (s *CatalogStore) ListOrganizations(ctx context.Context) ([]sharedstore.Organization, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, slug, name, created_at
		 FROM organizations
		 ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	defer rows.Close()

	var items []sharedstore.Organization
	for rows.Next() {
		var item sharedstore.Organization
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.DateCreated); err != nil {
			return nil, fmt.Errorf("scan organization: %w", err)
		}
		item.DateCreated = item.DateCreated.UTC()
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *CatalogStore) GetOrganization(ctx context.Context, slug string) (*sharedstore.Organization, error) {
	var item sharedstore.Organization
	err := s.db.QueryRowContext(ctx,
		`SELECT id, slug, name, created_at
		 FROM organizations
		 WHERE slug = $1`,
		strings.TrimSpace(slug),
	).Scan(&item.ID, &item.Slug, &item.Name, &item.DateCreated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get organization: %w", err)
	}
	item.DateCreated = item.DateCreated.UTC()
	return &item, nil
}

func (s *CatalogStore) ListProjects(ctx context.Context, orgSlug string) ([]sharedstore.Project, error) {
	query := `SELECT p.id, p.slug, p.name, COALESCE(p.platform, ''), COALESCE(p.status, 'active'), p.created_at, o.slug, COALESCE(t.slug, '')
	          FROM projects p
	          JOIN organizations o ON o.id = p.organization_id
	          LEFT JOIN teams t ON t.id = p.team_id`
	args := []any{}
	if orgSlug = strings.TrimSpace(orgSlug); orgSlug != "" {
		query += ` WHERE o.slug = $1`
		args = append(args, orgSlug)
	}
	query += ` ORDER BY p.created_at ASC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var items []sharedstore.Project
	for rows.Next() {
		var item sharedstore.Project
		if err := rows.Scan(
			&item.ID,
			&item.Slug,
			&item.Name,
			&item.Platform,
			&item.Status,
			&item.DateCreated,
			&item.OrgSlug,
			&item.TeamSlug,
		); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		item.DateCreated = item.DateCreated.UTC()
		if err := s.hydrateProjectRetention(ctx, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *CatalogStore) GetProject(ctx context.Context, orgSlug, projectSlug string) (*sharedstore.Project, error) {
	var item sharedstore.Project
	err := s.db.QueryRowContext(ctx,
		`SELECT p.id, p.slug, p.name, COALESCE(p.platform, ''), COALESCE(p.status, 'active'), p.created_at, o.slug, COALESCE(t.slug, '')
		 FROM projects p
		 JOIN organizations o ON o.id = p.organization_id
		 LEFT JOIN teams t ON t.id = p.team_id
		 WHERE o.slug = $1 AND p.slug = $2`,
		strings.TrimSpace(orgSlug),
		strings.TrimSpace(projectSlug),
	).Scan(
		&item.ID,
		&item.Slug,
		&item.Name,
		&item.Platform,
		&item.Status,
		&item.DateCreated,
		&item.OrgSlug,
		&item.TeamSlug,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}
	item.DateCreated = item.DateCreated.UTC()
	if err := s.hydrateProjectRetention(ctx, &item); err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *CatalogStore) ListTeams(ctx context.Context, orgSlug string) ([]sharedstore.Team, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.slug, t.name, t.organization_id, t.created_at
		 FROM teams t
		 JOIN organizations o ON o.id = t.organization_id
		 WHERE o.slug = $1
		 ORDER BY t.created_at ASC`,
		strings.TrimSpace(orgSlug),
	)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	defer rows.Close()

	var items []sharedstore.Team
	for rows.Next() {
		var item sharedstore.Team
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.OrgID, &item.DateCreated); err != nil {
			return nil, fmt.Errorf("scan team: %w", err)
		}
		item.DateCreated = item.DateCreated.UTC()
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *CatalogStore) ListProjectKeys(ctx context.Context, orgSlug, projectSlug string) ([]sharedstore.ProjectKeyMeta, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT k.id, k.project_id, k.label, k.public_key, COALESCE(k.secret_key, ''), k.status, k.created_at
		 FROM project_keys k
		 JOIN projects p ON p.id = k.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE o.slug = $1 AND p.slug = $2
		 ORDER BY k.created_at ASC`,
		strings.TrimSpace(orgSlug),
		strings.TrimSpace(projectSlug),
	)
	if err != nil {
		return nil, fmt.Errorf("list project keys: %w", err)
	}
	defer rows.Close()
	return scanProjectKeyRows(rows)
}

func (s *CatalogStore) ListAllProjectKeys(ctx context.Context) ([]sharedstore.ProjectKeyMeta, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, label, public_key, COALESCE(secret_key, ''), status, created_at
		 FROM project_keys
		 ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list all project keys: %w", err)
	}
	defer rows.Close()
	return scanProjectKeyRows(rows)
}

func (s *CatalogStore) CreateProject(ctx context.Context, orgSlug, teamSlug string, input sharedstore.ProjectCreateInput) (*sharedstore.Project, error) {
	slug := strings.TrimSpace(input.Slug)
	name := strings.TrimSpace(input.Name)
	platform := strings.TrimSpace(input.Platform)
	orgSlug = strings.TrimSpace(orgSlug)
	teamSlug = strings.TrimSpace(teamSlug)
	if slug == "" || name == "" || orgSlug == "" || teamSlug == "" {
		return nil, nil
	}

	var orgID string
	if err := s.db.QueryRowContext(ctx,
		`SELECT id FROM organizations WHERE slug = $1`,
		orgSlug,
	).Scan(&orgID); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("resolve organization for project create: %w", err)
	}

	var teamID string
	if err := s.db.QueryRowContext(ctx,
		`SELECT t.id
		 FROM teams t
		 JOIN organizations o ON o.id = t.organization_id
		 WHERE o.slug = $1 AND t.slug = $2`,
		orgSlug, teamSlug,
	).Scan(&teamID); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("resolve team for project create: %w", err)
	}

	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO projects (id, organization_id, team_id, slug, name, platform, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, 'active', $7, $7)`,
		id.New(), orgID, teamID, slug, name, platform, now,
	); err != nil {
		return nil, fmt.Errorf("insert project: %w", err)
	}
	return s.GetProject(ctx, orgSlug, slug)
}

func (s *CatalogStore) CreateProjectKey(ctx context.Context, orgSlug, projectSlug, label string) (*sharedstore.ProjectKeyMeta, error) {
	project, err := s.GetProject(ctx, orgSlug, projectSlug)
	if err != nil || project == nil {
		return nil, err
	}

	label = strings.TrimSpace(label)
	if label == "" {
		label = "Default"
	}
	item := sharedstore.ProjectKeyMeta{
		ID:          id.New(),
		ProjectID:   project.ID,
		Label:       label,
		PublicKey:   id.New(),
		SecretKey:   id.New(),
		Status:      "active",
		DateCreated: time.Now().UTC(),
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO project_keys (id, project_id, public_key, secret_key, status, label, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		item.ID, item.ProjectID, item.PublicKey, item.SecretKey, item.Status, item.Label, item.DateCreated,
	); err != nil {
		return nil, fmt.Errorf("insert project key: %w", err)
	}
	return &item, nil
}

func (s *CatalogStore) GetProjectSettings(ctx context.Context, orgSlug, projectSlug string) (*sharedstore.ProjectSettings, error) {
	project, err := s.GetProject(ctx, orgSlug, projectSlug)
	if err != nil || project == nil {
		return nil, err
	}

	var defaultEnvironment sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT default_environment FROM projects WHERE id = $1`,
		project.ID,
	).Scan(&defaultEnvironment); err != nil {
		return nil, fmt.Errorf("load project default environment: %w", err)
	}

	replayPolicy, err := s.getReplayIngestPolicy(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	telemetryPolicies, err := s.projectTelemetryPolicies(ctx, project.ID, project.EventRetentionDays, project.AttachRetentionDays, project.DebugRetentionDays)
	if err != nil {
		return nil, err
	}

	return &sharedstore.ProjectSettings{
		ID:                      project.ID,
		OrganizationSlug:        project.OrgSlug,
		Slug:                    project.Slug,
		Name:                    project.Name,
		Platform:                project.Platform,
		Status:                  project.Status,
		DefaultEnvironment:      nullStr(defaultEnvironment),
		EventRetentionDays:      project.EventRetentionDays,
		AttachmentRetentionDays: project.AttachRetentionDays,
		DebugFileRetentionDays:  project.DebugRetentionDays,
		TelemetryPolicies:       telemetryPolicies,
		ReplayPolicy:            replayPolicy,
		DateCreated:             project.DateCreated,
	}, nil
}

func (s *CatalogStore) UpdateProjectSettings(ctx context.Context, orgSlug, projectSlug string, update sharedstore.ProjectSettingsUpdate) (*sharedstore.ProjectSettings, error) {
	project, err := s.GetProject(ctx, orgSlug, projectSlug)
	if err != nil || project == nil {
		return nil, err
	}

	policies, err := sharedstore.CanonicalTelemetryPolicies(update.TelemetryPolicies, project.EventRetentionDays, project.AttachRetentionDays, project.DebugRetentionDays)
	if err != nil {
		return nil, err
	}
	for _, policy := range policies {
		switch policy.Surface {
		case sharedstore.TelemetrySurfaceErrors:
			update.EventRetentionDays = policy.RetentionDays
		case sharedstore.TelemetrySurfaceAttachments:
			update.AttachmentRetentionDays = policy.RetentionDays
		case sharedstore.TelemetrySurfaceDebugFiles:
			update.DebugFileRetentionDays = policy.RetentionDays
		}
	}

	name := strings.TrimSpace(update.Name)
	if name == "" {
		name = project.Name
	}
	platform := strings.TrimSpace(update.Platform)
	if platform == "" {
		platform = project.Platform
	}
	status := strings.TrimSpace(update.Status)
	if status == "" {
		status = project.Status
	}
	if status == "" {
		status = "active"
	}

	replayPolicy, err := sharedstore.CanonicalReplayIngestPolicy(update.ReplayPolicy)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin project settings tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`UPDATE projects
		 SET name = $1, platform = $2, status = $3, updated_at = $4
		 WHERE id = $5`,
		name, platform, status, time.Now().UTC(), project.ID,
	); err != nil {
		return nil, fmt.Errorf("update project settings: %w", err)
	}
	if err := upsertProjectTelemetryPolicies(ctx, tx, project.ID, policies); err != nil {
		return nil, err
	}
	if err := upsertReplayIngestPolicy(ctx, tx, project.ID, replayPolicy); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit project settings tx: %w", err)
	}

	return s.GetProjectSettings(ctx, orgSlug, projectSlug)
}

func (s *CatalogStore) ListOrganizationAuditLogs(ctx context.Context, orgSlug string, limit int) ([]sharedstore.AuditLogEntry, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT a.id,
		        a.credential_type,
		        COALESCE(NULLIF(a.credential_id, ''), ''),
		        COALESCE(NULLIF(a.user_id, ''), ''),
		        COALESCE(u.email, ''),
		        COALESCE(NULLIF(a.project_id, ''), ''),
		        COALESCE(p.slug, ''),
		        COALESCE(NULLIF(COALESCE(NULLIF(a.organization_id, ''), p.organization_id), ''), ''),
		        COALESCE(o.slug, ''),
		        a.action,
		        COALESCE(a.request_path, ''),
		        COALESCE(a.request_method, ''),
		        COALESCE(a.ip_address, ''),
		        COALESCE(a.user_agent, ''),
		        a.created_at
		 FROM auth_audit_logs a
		 LEFT JOIN users u ON u.id = NULLIF(a.user_id, '')
		 LEFT JOIN projects p ON p.id = NULLIF(a.project_id, '')
		 LEFT JOIN organizations o ON o.id = COALESCE(NULLIF(a.organization_id, ''), p.organization_id)
		 WHERE o.slug = $1
		 ORDER BY a.created_at DESC
		 LIMIT $2`,
		strings.TrimSpace(orgSlug), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list organization audit logs: %w", err)
	}
	defer rows.Close()

	var items []sharedstore.AuditLogEntry
	for rows.Next() {
		var item sharedstore.AuditLogEntry
		if err := rows.Scan(
			&item.ID,
			&item.CredentialType,
			&item.CredentialID,
			&item.UserID,
			&item.UserEmail,
			&item.ProjectID,
			&item.ProjectSlug,
			&item.OrganizationID,
			&item.OrganizationSlug,
			&item.Action,
			&item.RequestPath,
			&item.RequestMethod,
			&item.IPAddress,
			&item.UserAgent,
			&item.DateCreated,
		); err != nil {
			return nil, fmt.Errorf("scan organization audit log: %w", err)
		}
		item.DateCreated = item.DateCreated.UTC()
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *CatalogStore) hydrateProjectRetention(ctx context.Context, project *sharedstore.Project) error {
	if project == nil {
		return nil
	}
	policies, err := s.projectTelemetryPolicies(ctx, project.ID, 0, 0, 0)
	if err != nil {
		return err
	}
	applyProjectRetention(project, policies)
	return nil
}

func (s *CatalogStore) projectTelemetryPolicies(ctx context.Context, projectID string, eventDays, attachmentDays, debugDays int) ([]sharedstore.TelemetryRetentionPolicy, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT surface, retention_days, storage_tier, archive_retention_days
		 FROM telemetry_retention_policies
		 WHERE project_id = $1
		 ORDER BY surface ASC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list telemetry retention policies: %w", err)
	}
	defer rows.Close()

	var items []sharedstore.TelemetryRetentionPolicy
	for rows.Next() {
		var item sharedstore.TelemetryRetentionPolicy
		var surface string
		var tier string
		if err := rows.Scan(&surface, &item.RetentionDays, &tier, &item.ArchiveRetentionDays); err != nil {
			return nil, fmt.Errorf("scan telemetry retention policy: %w", err)
		}
		item.Surface = sharedstore.TelemetrySurface(surface)
		item.StorageTier = sharedstore.TelemetryStorageTier(tier)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sharedstore.CanonicalTelemetryPolicies(items, eventDays, attachmentDays, debugDays)
}

func scanProjectKeyRows(rows *sql.Rows) ([]sharedstore.ProjectKeyMeta, error) {
	var items []sharedstore.ProjectKeyMeta
	for rows.Next() {
		var item sharedstore.ProjectKeyMeta
		if err := rows.Scan(
			&item.ID,
			&item.ProjectID,
			&item.Label,
			&item.PublicKey,
			&item.SecretKey,
			&item.Status,
			&item.DateCreated,
		); err != nil {
			return nil, fmt.Errorf("scan project key: %w", err)
		}
		item.DateCreated = item.DateCreated.UTC()
		items = append(items, item)
	}
	return items, rows.Err()
}

func applyProjectRetention(project *sharedstore.Project, policies []sharedstore.TelemetryRetentionPolicy) {
	project.EventRetentionDays = 90
	project.AttachRetentionDays = 30
	project.DebugRetentionDays = 180
	for _, policy := range policies {
		switch policy.Surface {
		case sharedstore.TelemetrySurfaceErrors:
			project.EventRetentionDays = policy.RetentionDays
		case sharedstore.TelemetrySurfaceAttachments:
			project.AttachRetentionDays = policy.RetentionDays
		case sharedstore.TelemetrySurfaceDebugFiles:
			project.DebugRetentionDays = policy.RetentionDays
		}
	}
}

func (s *CatalogStore) getReplayIngestPolicy(ctx context.Context, projectID string) (sharedstore.ReplayIngestPolicy, error) {
	var (
		policy             sharedstore.ReplayIngestPolicy
		scrubFieldsJSON    []byte
		scrubSelectorsJSON []byte
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT sample_rate, max_bytes, scrub_fields_json, scrub_selectors_json
		 FROM project_replay_configs
		 WHERE project_id = $1`,
		projectID,
	).Scan(&policy.SampleRate, &policy.MaxBytes, &scrubFieldsJSON, &scrubSelectorsJSON)
	if err == sql.ErrNoRows {
		return defaultReplayIngestPolicy(), nil
	}
	if err != nil {
		return sharedstore.ReplayIngestPolicy{}, fmt.Errorf("load replay ingest policy: %w", err)
	}
	_ = json.Unmarshal(scrubFieldsJSON, &policy.ScrubFields)
	_ = json.Unmarshal(scrubSelectorsJSON, &policy.ScrubSelectors)
	return sharedstore.CanonicalReplayIngestPolicy(policy)
}

func upsertProjectTelemetryPolicies(ctx context.Context, tx *sql.Tx, projectID string, policies []sharedstore.TelemetryRetentionPolicy) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM telemetry_retention_policies WHERE project_id = $1`,
		projectID,
	); err != nil {
		return fmt.Errorf("clear telemetry retention policies: %w", err)
	}
	now := time.Now().UTC()
	for _, policy := range policies {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO telemetry_retention_policies
				(project_id, surface, retention_days, storage_tier, archive_retention_days, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $6)`,
			projectID,
			string(policy.Surface),
			policy.RetentionDays,
			string(policy.StorageTier),
			policy.ArchiveRetentionDays,
			now,
		); err != nil {
			return fmt.Errorf("insert telemetry retention policy: %w", err)
		}
	}
	return nil
}

func upsertReplayIngestPolicy(ctx context.Context, tx *sql.Tx, projectID string, policy sharedstore.ReplayIngestPolicy) error {
	scrubFieldsJSON, err := json.Marshal(policy.ScrubFields)
	if err != nil {
		return fmt.Errorf("marshal replay scrub fields: %w", err)
	}
	scrubSelectorsJSON, err := json.Marshal(policy.ScrubSelectors)
	if err != nil {
		return fmt.Errorf("marshal replay scrub selectors: %w", err)
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO project_replay_configs
			(project_id, sample_rate, max_bytes, scrub_fields_json, scrub_selectors_json, created_at, updated_at)
		 VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, $6, $6)
		 ON CONFLICT (project_id) DO UPDATE SET
			sample_rate = EXCLUDED.sample_rate,
			max_bytes = EXCLUDED.max_bytes,
			scrub_fields_json = EXCLUDED.scrub_fields_json,
			scrub_selectors_json = EXCLUDED.scrub_selectors_json,
			updated_at = EXCLUDED.updated_at`,
		projectID, policy.SampleRate, policy.MaxBytes, string(scrubFieldsJSON), string(scrubSelectorsJSON), now,
	); err != nil {
		return fmt.Errorf("upsert replay ingest policy: %w", err)
	}
	return nil
}

func defaultReplayIngestPolicy() sharedstore.ReplayIngestPolicy {
	policy, err := sharedstore.CanonicalReplayIngestPolicy(sharedstore.ReplayIngestPolicy{})
	if err != nil {
		return sharedstore.ReplayIngestPolicy{SampleRate: 1, MaxBytes: 10 << 20}
	}
	return policy
}
