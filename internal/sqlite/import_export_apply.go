package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/alert"
	"urgentry/internal/migration"
	"urgentry/internal/store"
	"urgentry/pkg/id"
)

func importOrganizationPayload(ctx context.Context, db execQuerier, blobs store.BlobStore, orgID, orgSlug string, payload migration.ImportPayload, blobKeys *[]string) (*migration.ImportResult, error) {
	result := &migration.ImportResult{}
	projectIDs := make(map[string]string)

	for _, p := range payload.Projects {
		projectID, err := upsertProjectImport(ctx, db, orgID, orgSlug, p)
		if err != nil {
			return nil, fmt.Errorf("project %s: %w", p.Slug, err)
		}
		projectIDs[p.Slug] = projectID
		result.ProjectsImported++
	}

	for _, r := range payload.Releases {
		if err := upsertReleaseImport(ctx, db, orgID, r); err != nil {
			return nil, fmt.Errorf("release %s: %w", r.Version, err)
		}
		result.ReleasesImported++
	}

	for _, i := range payload.Issues {
		projectID, err := resolveImportedProjectID(ctx, db, projectIDs, orgID, i.ProjectSlug)
		if err != nil {
			return nil, fmt.Errorf("issue %s: %w", i.ID, err)
		}
		if err := upsertIssueImport(ctx, db, projectID, i); err != nil {
			return nil, fmt.Errorf("issue %s: %w", i.ID, err)
		}
		result.IssuesImported++
	}

	for _, a := range payload.Artifacts {
		projectID, err := resolveImportedProjectID(ctx, db, projectIDs, orgID, a.ProjectSlug)
		if err != nil {
			return nil, fmt.Errorf("artifact %s: %w", a.Name, err)
		}
		if err := upsertArtifactImport(ctx, db, blobs, projectID, a, blobKeys); err != nil {
			return nil, fmt.Errorf("artifact %s: %w", a.Name, err)
		}
		result.ArtifactsImported++
		result.ArtifactsVerified++
	}

	for _, e := range payload.Events {
		projectID, err := resolveImportedProjectID(ctx, db, projectIDs, orgID, e.ProjectSlug)
		if err != nil {
			return nil, fmt.Errorf("event %s: %w", e.EventID, err)
		}
		evt, err := upsertEventImport(ctx, db, projectID, e)
		if err != nil {
			return nil, fmt.Errorf("event %s: %w", e.EventID, err)
		}
		if evt != nil {
			if err := rebuildNativeCrashImagesWithQuerier(ctx, db, projectID, evt.EventID, evt.NormalizedJSON); err != nil {
				return nil, fmt.Errorf("event %s native catalog: %w", e.EventID, err)
			}
		}
		if evt != nil && evt.EventType == "profile" {
			if err := materializeProfileEventWithQuerier(ctx, db, blobs, evt, evt.NormalizedJSON); err != nil {
				return nil, fmt.Errorf("event %s profile: %w", e.EventID, err)
			}
		}
		result.EventsImported++
	}

	for _, k := range payload.ProjectKeys {
		projectID, err := resolveImportedProjectID(ctx, db, projectIDs, orgID, k.ProjectSlug)
		if err != nil {
			return nil, fmt.Errorf("project key %s: %w", k.PublicKey, err)
		}
		if err := upsertProjectKeyImport(ctx, db, projectID, k); err != nil {
			return nil, fmt.Errorf("project key %s: %w", k.PublicKey, err)
		}
		result.ProjectKeysImported++
	}

	for _, a := range payload.AlertRules {
		projectID, err := resolveImportedProjectID(ctx, db, projectIDs, orgID, a.ProjectSlug)
		if err != nil {
			return nil, fmt.Errorf("alert rule %s: %w", a.Name, err)
		}
		if err := upsertAlertRuleImport(ctx, db, projectID, a); err != nil {
			return nil, fmt.Errorf("alert rule %s: %w", a.Name, err)
		}
		result.AlertRulesImported++
	}

	for _, m := range payload.Members {
		if err := upsertMemberImport(ctx, db, orgID, m); err != nil {
			return nil, fmt.Errorf("member %s: %w", m.Email, err)
		}
		result.MembersImported++
	}

	return result, nil
}

func resolveImportedProjectID(ctx context.Context, db execQuerier, cached map[string]string, orgID, projectSlug string) (string, error) {
	if projectSlug == "" {
		return "", fmt.Errorf("project slug is required")
	}
	if id, ok := cached[projectSlug]; ok && id != "" {
		return id, nil
	}
	var projectID string
	err := db.QueryRowContext(ctx,
		`SELECT id FROM projects WHERE organization_id = ? AND slug = ?`,
		orgID, projectSlug,
	).Scan(&projectID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("project %s not found", projectSlug)
		}
		return "", err
	}
	cached[projectSlug] = projectID
	return projectID, nil
}

func upsertProjectImport(ctx context.Context, db execQuerier, orgID, orgSlug string, p migration.ProjectImport) (string, error) {
	slug := strings.TrimSpace(p.Slug)
	if slug == "" {
		slug = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(p.Name), " ", "-"))
	}
	if slug == "" {
		return "", fmt.Errorf("project slug is required")
	}
	teamID := ""
	if p.TeamSlug != "" {
		if err := db.QueryRowContext(ctx, `SELECT id FROM teams WHERE organization_id = ? AND slug = ?`, orgID, p.TeamSlug).Scan(&teamID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", validationErrorf("team %q not found for project %q", p.TeamSlug, slug)
			}
			return "", err
		}
	}
	projectID := strings.TrimSpace(p.ID)
	if projectID == "" {
		projectID = id.New()
	}
	row := db.QueryRowContext(ctx,
		`INSERT INTO projects (id, organization_id, team_id, slug, name, platform, status, created_at)
		 VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, 'active', ?)
		 ON CONFLICT(organization_id, slug) DO UPDATE SET
			name = excluded.name,
			platform = excluded.platform,
			team_id = COALESCE(excluded.team_id, projects.team_id)
		 RETURNING id`,
		projectID, orgID, teamID, slug, p.Name, p.Platform, time.Now().UTC().Format(time.RFC3339),
	)
	if err := row.Scan(&projectID); err != nil {
		return "", err
	}
	_ = orgSlug
	return projectID, nil
}

func upsertReleaseImport(ctx context.Context, db execQuerier, orgID string, r migration.ReleaseImport) error {
	version := strings.TrimSpace(r.Version)
	if version == "" {
		return fmt.Errorf("release version is required")
	}
	releaseID := strings.TrimSpace(r.ID)
	if releaseID == "" {
		releaseID = id.New()
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO releases (id, organization_id, version, date_released, created_at)
		 VALUES (?, ?, ?, NULLIF(?, ''), ?)
		 ON CONFLICT(organization_id, version) DO UPDATE SET
			date_released = excluded.date_released`,
		releaseID, orgID, version, r.DateReleased, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func upsertIssueImport(ctx context.Context, db execQuerier, projectID string, i migration.IssueImport) error {
	groupID := strings.TrimSpace(i.ID)
	if groupID == "" {
		groupID = id.New()
	}
	_, err := db.ExecContext(ctx,
		`INSERT OR REPLACE INTO groups
			(id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen, short_id, assignee, priority)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		groupID, projectID, nullOrDefault(i.GroupingVersion, "urgentry-v1"), nullOrDefault(i.GroupingKey, groupID),
		i.Title, i.Culprit, i.Level, nullOrDefault(i.Status, "unresolved"),
		i.FirstSeen, i.LastSeen, i.TimesSeen, nullableInt(i.ShortID), i.Assignee, nullableIntDefault(i.Priority, 2),
	)
	return err
}

func upsertEventImport(ctx context.Context, db execQuerier, projectID string, e migration.EventImport) (*store.StoredEvent, error) {
	rowID := strings.TrimSpace(e.ID)
	if rowID == "" {
		rowID = id.New()
	}
	tagsJSON := "{}"
	if len(e.Tags) > 0 {
		b, err := json.Marshal(e.Tags)
		if err != nil {
			return nil, err
		}
		tagsJSON = string(b)
	}
	payloadJSON := e.PayloadJSON
	if payloadJSON == "" {
		payloadJSON = "{}"
	}
	eventType := nullOrDefault(e.EventType, "error")
	_, err := db.ExecContext(ctx,
		`INSERT OR REPLACE INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, tags_json, payload_json, occurred_at, ingested_at, user_identifier, payload_key)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, COALESCE(NULLIF(?, ''), datetime('now')), ?, NULLIF(?, ''))`,
		rowID, projectID, e.EventID, e.GroupID, e.Release, e.Environment, e.Platform, e.Level, eventType, e.Title, e.Message, e.Culprit,
		tagsJSON, payloadJSON, e.OccurredAt, "", e.UserIdentifier, e.PayloadKey,
	)
	if err != nil {
		return nil, err
	}
	return &store.StoredEvent{
		ID:             rowID,
		ProjectID:      projectID,
		EventID:        e.EventID,
		GroupID:        e.GroupID,
		ReleaseID:      e.Release,
		Environment:    e.Environment,
		Platform:       e.Platform,
		Level:          e.Level,
		EventType:      eventType,
		OccurredAt:     parseOptionalTimeString(e.OccurredAt),
		IngestedAt:     time.Now().UTC(),
		Message:        e.Message,
		Title:          e.Title,
		Culprit:        e.Culprit,
		Tags:           e.Tags,
		NormalizedJSON: json.RawMessage(payloadJSON),
		PayloadKey:     e.PayloadKey,
		UserIdentifier: e.UserIdentifier,
	}, nil
}

func upsertProjectKeyImport(ctx context.Context, db execQuerier, projectID string, k migration.ProjectKeyImport) error {
	keyID := strings.TrimSpace(k.ID)
	if keyID == "" {
		keyID = id.New()
	}
	status := strings.TrimSpace(k.Status)
	if status == "" {
		status = "active"
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO project_keys
			(id, project_id, public_key, secret_key, status, label, rate_limit, created_at)
		 VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, NULLIF(?, 0), NULLIF(?, ''))
		 ON CONFLICT(public_key) DO UPDATE SET
			project_id = excluded.project_id,
			secret_key = excluded.secret_key,
			status = excluded.status,
			label = excluded.label,
			rate_limit = excluded.rate_limit`,
		keyID, projectID, k.PublicKey, k.SecretKey, status, k.Label, k.RateLimit, k.CreatedAt,
	)
	return err
}

func upsertAlertRuleImport(ctx context.Context, db execQuerier, projectID string, a migration.AlertRuleImport) error {
	ruleID := strings.TrimSpace(a.ID)
	if ruleID == "" {
		ruleID = id.New()
	}
	rule := alert.Rule{
		ID:         ruleID,
		ProjectID:  projectID,
		Name:       strings.TrimSpace(a.Name),
		RuleType:   strings.TrimSpace(a.RuleType),
		Status:     nullOrDefault(a.Status, "active"),
		Conditions: a.Conditions,
		Actions:    a.Actions,
		CreatedAt:  parseOptionalTimeString(a.CreatedAt),
		UpdatedAt:  parseOptionalTimeString(a.CreatedAt),
	}
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = time.Now().UTC()
		rule.UpdatedAt = rule.CreatedAt
	}
	configJSON, err := json.Marshal(rule)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO alert_rules (id, project_id, name, status, config_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
			project_id = excluded.project_id,
			name = excluded.name,
			status = excluded.status,
			config_json = excluded.config_json`,
		rule.ID, rule.ProjectID, rule.Name, rule.Status, string(configJSON), rule.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

func upsertMemberImport(ctx context.Context, db execQuerier, orgID string, m migration.MemberImport) error {
	if strings.TrimSpace(m.Email) == "" {
		return fmt.Errorf("email is required")
	}
	userID := strings.TrimSpace(m.ID)
	if userID == "" {
		userID = id.New()
	}
	isActive := 0
	if m.IsActive {
		isActive = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, email, display_name, is_active, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NULLIF(?, ''), ?)
		 ON CONFLICT(id) DO UPDATE SET
			email = excluded.email,
			display_name = excluded.display_name,
			is_active = excluded.is_active,
			updated_at = excluded.updated_at`,
		userID, strings.ToLower(strings.TrimSpace(m.Email)), strings.TrimSpace(m.DisplayName), isActive, m.CreatedAt, now,
	)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO organization_members (id, organization_id, user_id, role, created_at)
		 VALUES (?, ?, ?, ?, NULLIF(?, ''))
		 ON CONFLICT(organization_id, user_id) DO UPDATE SET role = excluded.role`,
		id.New(), orgID, userID, nullOrDefault(m.Role, "member"), m.CreatedAt,
	)
	return err
}
