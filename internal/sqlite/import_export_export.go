package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"

	"urgentry/internal/alert"
	"urgentry/internal/migration"
)

func exportOrganizationPayload(ctx context.Context, db *sql.DB, orgSlug string) (*migration.ImportPayload, error) {
	export, err := prepareOrganizationPayloadExport(ctx, db, orgSlug)
	if err != nil {
		return nil, err
	}
	return &migration.ImportPayload{
		Projects:    export.projects,
		Releases:    export.releases,
		Issues:      export.issues,
		Events:      export.events,
		ProjectKeys: export.projectKeys,
		AlertRules:  export.alertRules,
		Members:     export.members,
		Artifacts:   export.artifacts,
	}, nil
}

func prepareOrganizationPayloadExport(ctx context.Context, db *sql.DB, orgSlug string) (*organizationPayloadExport, error) {
	projects, err := exportProjectsFromDB(ctx, db, orgSlug)
	if err != nil {
		return nil, err
	}
	releases, err := exportReleasesFromDB(ctx, db, orgSlug)
	if err != nil {
		return nil, err
	}
	issues, err := exportIssuesFromDB(ctx, db, orgSlug)
	if err != nil {
		return nil, err
	}
	events, err := exportEventsFromDB(ctx, db, orgSlug)
	if err != nil {
		return nil, err
	}
	projectKeys, err := exportProjectKeysFromDB(ctx, db, orgSlug)
	if err != nil {
		return nil, err
	}
	alertRules, err := exportAlertRulesFromDB(ctx, db, orgSlug)
	if err != nil {
		return nil, err
	}
	members, err := exportMembersFromDB(ctx, db, orgSlug)
	if err != nil {
		return nil, err
	}
	artifacts, err := exportArtifactMetadataFromDB(ctx, db, orgSlug)
	if err != nil {
		return nil, err
	}
	return &organizationPayloadExport{
		projects:    projects,
		releases:    releases,
		issues:      issues,
		events:      events,
		projectKeys: projectKeys,
		alertRules:  alertRules,
		members:     members,
		artifacts:   artifacts,
	}, nil
}

func exportProjectsFromDB(ctx context.Context, db *sql.DB, orgSlug string) ([]migration.ProjectImport, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT p.id, p.name, p.slug, p.platform, COALESCE(t.slug, '')
		 FROM projects p
		 JOIN organizations o ON o.id = p.organization_id
		 LEFT JOIN teams t ON t.id = p.team_id
		 WHERE o.slug = ?
		 ORDER BY p.created_at ASC`,
		orgSlug,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []migration.ProjectImport
	for rows.Next() {
		var p migration.ProjectImport
		var platform, teamSlug sql.NullString
		if err := rows.Scan(&p.ID, &p.Name, &p.Slug, &platform, &teamSlug); err != nil {
			return nil, err
		}
		p.Platform = dbNullStr(platform)
		p.TeamSlug = dbNullStr(teamSlug)
		if p.TeamSlug != "" {
			p.Teams = []string{p.TeamSlug}
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func exportReleasesFromDB(ctx context.Context, db *sql.DB, orgSlug string) ([]migration.ReleaseImport, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT r.id, r.version, r.date_released
		 FROM releases r
		 JOIN organizations o ON o.id = r.organization_id
		 WHERE o.slug = ?
		 ORDER BY r.created_at ASC`,
		orgSlug,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var releases []migration.ReleaseImport
	for rows.Next() {
		var rel migration.ReleaseImport
		var dateReleased sql.NullString
		if err := rows.Scan(&rel.ID, &rel.Version, &dateReleased); err != nil {
			return nil, err
		}
		rel.DateReleased = dbNullStr(dateReleased)
		releases = append(releases, rel)
	}
	return releases, rows.Err()
}

func exportIssuesFromDB(ctx context.Context, db *sql.DB, orgSlug string) ([]migration.IssueImport, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT g.id, p.slug, g.title, g.culprit, g.level, g.status,
		        g.grouping_version, g.grouping_key, g.first_seen, g.last_seen,
		        g.times_seen, COALESCE(g.short_id, 0), g.assignee, COALESCE(g.priority, 2)
		 FROM groups g
		 JOIN projects p ON p.id = g.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE o.slug = ?
		 ORDER BY g.last_seen DESC`,
		orgSlug,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var issues []migration.IssueImport
	for rows.Next() {
		var item migration.IssueImport
		var culprit, level, status, groupingVersion, groupingKey, firstSeen, lastSeen, assignee sql.NullString
		var timesSeen, shortID, priority int
		if err := rows.Scan(&item.ID, &item.ProjectSlug, &item.Title, &culprit, &level, &status, &groupingVersion, &groupingKey, &firstSeen, &lastSeen, &timesSeen, &shortID, &assignee, &priority); err != nil {
			return nil, err
		}
		item.Culprit = dbNullStr(culprit)
		item.Level = dbNullStr(level)
		item.Status = dbNullStr(status)
		item.GroupingVersion = dbNullStr(groupingVersion)
		item.GroupingKey = dbNullStr(groupingKey)
		item.FirstSeen = dbNullStr(firstSeen)
		item.LastSeen = dbNullStr(lastSeen)
		item.TimesSeen = timesSeen
		item.ShortID = shortID
		item.Assignee = dbNullStr(assignee)
		item.Priority = priority
		issues = append(issues, item)
	}
	return issues, rows.Err()
}

func exportEventsFromDB(ctx context.Context, db *sql.DB, orgSlug string) ([]migration.EventImport, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT e.id, p.slug, e.event_id, e.group_id, COALESCE(e.event_type, 'error'), COALESCE(e.release, ''), COALESCE(e.environment, ''), COALESCE(e.platform, ''),
		        COALESCE(e.level, ''), COALESCE(e.title, ''), COALESCE(e.message, ''), COALESCE(e.culprit, ''),
		        COALESCE(e.occurred_at, ''), COALESCE(e.tags_json, '{}'), COALESCE(e.payload_json, ''), COALESCE(e.payload_key, ''), COALESCE(e.user_identifier, '')
		 FROM events e
		 JOIN projects p ON p.id = e.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE o.slug = ?
		 ORDER BY e.ingested_at ASC`,
		orgSlug,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []migration.EventImport
	for rows.Next() {
		var item migration.EventImport
		var eventType, release, environment, platform, level, title, message, culprit, occurredAt, tagsJSON, payloadJSON, payloadKey, userIdentifier sql.NullString
		if err := rows.Scan(&item.ID, &item.ProjectSlug, &item.EventID, &item.GroupID, &eventType, &release, &environment, &platform, &level, &title, &message, &culprit, &occurredAt, &tagsJSON, &payloadJSON, &payloadKey, &userIdentifier); err != nil {
			return nil, err
		}
		item.EventType = dbNullStr(eventType)
		item.Release = dbNullStr(release)
		item.Environment = dbNullStr(environment)
		item.Platform = dbNullStr(platform)
		item.Level = dbNullStr(level)
		item.Title = dbNullStr(title)
		item.Message = dbNullStr(message)
		item.Culprit = dbNullStr(culprit)
		item.OccurredAt = dbNullStr(occurredAt)
		item.PayloadJSON = dbNullStr(payloadJSON)
		item.PayloadKey = dbNullStr(payloadKey)
		item.UserIdentifier = dbNullStr(userIdentifier)
		item.Tags = nil
		if raw := dbNullStr(tagsJSON); raw != "" {
			var tags map[string]string
			if err := json.Unmarshal([]byte(raw), &tags); err == nil {
				item.Tags = tags
			}
		}
		events = append(events, item)
	}
	return events, rows.Err()
}

func exportProjectKeysFromDB(ctx context.Context, db *sql.DB, orgSlug string) ([]migration.ProjectKeyImport, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT k.id, p.slug, k.public_key, COALESCE(k.secret_key, ''), COALESCE(k.status, 'active'),
		        COALESCE(k.label, ''), COALESCE(k.rate_limit, 0), COALESCE(k.created_at, '')
		 FROM project_keys k
		 JOIN projects p ON p.id = k.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE o.slug = ?
		 ORDER BY k.created_at ASC`,
		orgSlug,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []migration.ProjectKeyImport
	for rows.Next() {
		var item migration.ProjectKeyImport
		var rateLimit int
		if err := rows.Scan(&item.ID, &item.ProjectSlug, &item.PublicKey, &item.SecretKey, &item.Status, &item.Label, &rateLimit, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.RateLimit = rateLimit
		keys = append(keys, item)
	}
	return keys, rows.Err()
}

func exportAlertRulesFromDB(ctx context.Context, db *sql.DB, orgSlug string) ([]migration.AlertRuleImport, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT r.id, p.slug, r.name, r.status, r.config_json, r.created_at
		 FROM alert_rules r
		 JOIN projects p ON p.id = r.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE o.slug = ?
		 ORDER BY r.created_at ASC`,
		orgSlug,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []migration.AlertRuleImport
	for rows.Next() {
		var item migration.AlertRuleImport
		var configJSON sql.NullString
		if err := rows.Scan(&item.ID, &item.ProjectSlug, &item.Name, &item.Status, &configJSON, &item.CreatedAt); err != nil {
			return nil, err
		}
		if cfg := dbNullStr(configJSON); cfg != "" && cfg != "{}" {
			var rule alert.Rule
			if err := json.Unmarshal([]byte(cfg), &rule); err == nil {
				item.RuleType = rule.RuleType
				item.Conditions = rule.Conditions
				item.Actions = rule.Actions
			}
		}
		rules = append(rules, item)
	}
	return rules, rows.Err()
}

func exportMembersFromDB(ctx context.Context, db *sql.DB, orgSlug string) ([]migration.MemberImport, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT u.id, u.email, u.display_name, u.is_active, m.role, u.created_at
		 FROM organization_members m
		 JOIN users u ON u.id = m.user_id
		 JOIN organizations o ON o.id = m.organization_id
		 WHERE o.slug = ?
		 ORDER BY m.created_at ASC`,
		orgSlug,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []migration.MemberImport
	for rows.Next() {
		var item migration.MemberImport
		var active int
		if err := rows.Scan(&item.ID, &item.Email, &item.DisplayName, &active, &item.Role, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.IsActive = active != 0
		members = append(members, item)
	}
	return members, rows.Err()
}

func exportArtifactMetadataFromDB(ctx context.Context, db *sql.DB, orgSlug string) ([]migration.ArtifactImport, error) {
	var artifacts []migration.ArtifactImport

	attachmentRows, err := db.QueryContext(ctx,
		`SELECT a.id, p.slug, a.event_id, a.name, COALESCE(a.content_type, ''), COALESCE(a.object_key, ''), a.size_bytes, a.created_at
		 FROM event_attachments a
		 JOIN projects p ON p.id = a.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE o.slug = ?
		 ORDER BY a.created_at ASC`,
		orgSlug,
	)
	if err != nil {
		return nil, err
	}
	defer attachmentRows.Close()
	for attachmentRows.Next() {
		var item migration.ArtifactImport
		if err := attachmentRows.Scan(&item.ID, &item.ProjectSlug, &item.EventID, &item.Name, &item.ContentType, &item.ObjectKey, &item.Size, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.Kind = "attachment"
		artifacts = append(artifacts, item)
	}
	if err := attachmentRows.Err(); err != nil {
		return nil, err
	}

	sourceMapRows, err := db.QueryContext(ctx,
		`SELECT a.id, p.slug, a.release_version, a.name, COALESCE(a.object_key, ''), a.size, COALESCE(a.checksum, ''), a.created_at
		 FROM artifacts a
		 JOIN projects p ON p.id = a.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE o.slug = ?
		 ORDER BY a.created_at ASC`,
		orgSlug,
	)
	if err != nil {
		return nil, err
	}
	defer sourceMapRows.Close()
	for sourceMapRows.Next() {
		var item migration.ArtifactImport
		if err := sourceMapRows.Scan(&item.ID, &item.ProjectSlug, &item.ReleaseVersion, &item.Name, &item.ObjectKey, &item.Size, &item.Checksum, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.Kind = "source_map"
		artifacts = append(artifacts, item)
	}
	if err := sourceMapRows.Err(); err != nil {
		return nil, err
	}

	proguardRows, err := db.QueryContext(ctx,
		`SELECT d.id, p.slug, d.release_version, d.kind, COALESCE(d.uuid, ''), COALESCE(d.code_id, ''), COALESCE(ns.build_id, ''), COALESCE(ns.module_name, ''), COALESCE(ns.architecture, ''), COALESCE(ns.platform, ''), d.name, COALESCE(d.content_type, ''), COALESCE(d.object_key, ''), d.size_bytes, COALESCE(d.checksum, ''), d.created_at
		 FROM debug_files d
		 JOIN projects p ON p.id = d.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 LEFT JOIN native_symbol_sources ns ON ns.id = (
		  SELECT id FROM native_symbol_sources ns2
		  WHERE ns2.debug_file_id = d.id
		  ORDER BY ns2.created_at DESC, ns2.id DESC
		  LIMIT 1
		 )
		 WHERE o.slug = ?
		 ORDER BY d.created_at ASC`,
		orgSlug,
	)
	if err != nil {
		return nil, err
	}
	defer proguardRows.Close()
	for proguardRows.Next() {
		var item migration.ArtifactImport
		if err := proguardRows.Scan(&item.ID, &item.ProjectSlug, &item.ReleaseVersion, &item.Kind, &item.UUID, &item.CodeID, &item.BuildID, &item.ModuleName, &item.Architecture, &item.Platform, &item.Name, &item.ContentType, &item.ObjectKey, &item.Size, &item.Checksum, &item.CreatedAt); err != nil {
			return nil, err
		}
		if strings.TrimSpace(item.Kind) == "" {
			item.Kind = "native"
		}
		artifacts = append(artifacts, item)
	}
	if err := proguardRows.Err(); err != nil {
		return nil, err
	}

	profileRows, err := db.QueryContext(ctx,
		`SELECT e.id, p.slug, e.event_id, COALESCE(e.payload_key, ''), 0, e.ingested_at
		 FROM events e
		 JOIN projects p ON p.id = e.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE o.slug = ? AND COALESCE(e.event_type, 'error') = 'profile' AND COALESCE(e.payload_key, '') != ''
		 ORDER BY e.ingested_at ASC`,
		orgSlug,
	)
	if err != nil {
		return nil, err
	}
	defer profileRows.Close()
	for profileRows.Next() {
		var item migration.ArtifactImport
		if err := profileRows.Scan(&item.ID, &item.ProjectSlug, &item.EventID, &item.ObjectKey, &item.Size, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.Kind = "profile_raw"
		item.Name = item.EventID + ".profile.json"
		item.ContentType = "application/json"
		artifacts = append(artifacts, item)
	}
	if err := profileRows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(artifacts, func(i, j int) bool {
		if artifacts[i].Kind == artifacts[j].Kind {
			return artifacts[i].Name < artifacts[j].Name
		}
		return artifacts[i].Kind < artifacts[j].Kind
	})
	return artifacts, nil
}
