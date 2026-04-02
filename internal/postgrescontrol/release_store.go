package postgrescontrol

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"urgentry/internal/sqlite"
	sharedstore "urgentry/internal/store"
	"urgentry/pkg/id"
)

// ReleaseStore persists release workflow data in the PostgreSQL control plane.
type ReleaseStore struct {
	db *sql.DB
}

// NewReleaseStore creates a release store backed by PostgreSQL.
func NewReleaseStore(db *sql.DB) *ReleaseStore {
	return &ReleaseStore{db: db}
}

// CreateRelease creates or returns a release by organization slug and version.
func (s *ReleaseStore) CreateRelease(ctx context.Context, orgSlug, version string) (*sqlite.Release, error) {
	orgSlug = strings.TrimSpace(orgSlug)
	version = strings.TrimSpace(version)
	if orgSlug == "" || version == "" {
		return nil, nil
	}

	var orgID string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM organizations WHERE slug = $1`, orgSlug).Scan(&orgID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO releases (id, organization_id, version, created_at)
		 VALUES ($1, $2, $3, now())
		 ON CONFLICT (organization_id, version) DO NOTHING`,
		id.New(), orgID, version,
	)
	if err != nil {
		return nil, err
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr == nil && rows > 0 {
		if err := bindResolvedNextReleaseIssues(ctx, s.db, orgID, version); err != nil {
			return nil, err
		}
	}
	return s.GetRelease(ctx, orgID, version)
}

// ListReleases lists releases for an organization.
func (s *ReleaseStore) ListReleases(ctx context.Context, orgID string, limit int) ([]sqlite.Release, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, releaseSummarySelectSQL+
		` WHERE r.organization_id = $1
		   ORDER BY r.created_at DESC
		   LIMIT $2`,
		orgID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]sqlite.Release, 0, limit)
	for rows.Next() {
		item, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetRelease retrieves one release by organization ID and version.
func (s *ReleaseStore) GetRelease(ctx context.Context, orgID, version string) (*sqlite.Release, error) {
	row := s.db.QueryRowContext(ctx, releaseSummarySelectSQL+` WHERE r.organization_id = $1 AND r.version = $2`, orgID, version)
	item, err := scanRelease(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

// GetReleaseBySlug retrieves one release by organization slug and version.
func (s *ReleaseStore) GetReleaseBySlug(ctx context.Context, orgSlug, version string) (*sqlite.Release, error) {
	var orgID string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM organizations WHERE slug = $1`, strings.TrimSpace(orgSlug)).Scan(&orgID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return s.GetRelease(ctx, orgID, version)
}

// DeleteRelease removes a release and its associated deploys and commits.
func (s *ReleaseStore) DeleteRelease(ctx context.Context, orgSlug, version string) error {
	releaseID, err := s.lookupReleaseID(ctx, orgSlug, version)
	if err != nil || releaseID == "" {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM release_deploys WHERE release_id = $1`, releaseID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM release_commits WHERE release_id = $1`, releaseID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM releases WHERE id = $1`, releaseID); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateRelease updates mutable release metadata: ref, url, date_released.
func (s *ReleaseStore) UpdateRelease(ctx context.Context, orgSlug, version string, ref, url *string, dateReleased *time.Time) (*sqlite.Release, error) {
	releaseID, err := s.lookupReleaseID(ctx, orgSlug, version)
	if err != nil {
		return nil, err
	}
	if releaseID == "" {
		return nil, nil
	}
	if ref != nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE releases SET ref = $1 WHERE id = $2`, strings.TrimSpace(*ref), releaseID); err != nil {
			return nil, err
		}
	}
	if url != nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE releases SET url = $1 WHERE id = $2`, strings.TrimSpace(*url), releaseID); err != nil {
			return nil, err
		}
	}
	if dateReleased != nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE releases SET date_released = $1 WHERE id = $2`, dateReleased.UTC(), releaseID); err != nil {
			return nil, err
		}
	}
	return s.GetReleaseBySlug(ctx, orgSlug, version)
}

// AddDeploy stores one deployment marker for a release.
func (s *ReleaseStore) AddDeploy(ctx context.Context, orgSlug, version string, deploy sharedstore.ReleaseDeploy) (*sharedstore.ReleaseDeploy, error) {
	releaseID, err := s.lookupReleaseID(ctx, orgSlug, version)
	if err != nil || releaseID == "" {
		return nil, err
	}
	if deploy.ID == "" {
		deploy.ID = id.New()
	}
	deploy.ReleaseID = releaseID
	deploy.ReleaseVersion = version
	if strings.TrimSpace(deploy.Environment) == "" {
		deploy.Environment = "production"
	}
	if deploy.DateCreated.IsZero() {
		deploy.DateCreated = time.Now().UTC()
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO release_deploys
			(id, release_id, environment, name, url, date_started, date_finished, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		deploy.ID, deploy.ReleaseID, strings.TrimSpace(deploy.Environment), strings.TrimSpace(deploy.Name), strings.TrimSpace(deploy.URL),
		nullableTime(deploy.DateStarted), nullableTime(deploy.DateFinished), deploy.DateCreated.UTC(),
	)
	if err != nil {
		return nil, err
	}
	return &deploy, nil
}

// ListDeploys lists recent deploys for a release.
func (s *ReleaseStore) ListDeploys(ctx context.Context, orgSlug, version string, limit int) ([]sharedstore.ReleaseDeploy, error) {
	releaseID, err := s.lookupReleaseID(ctx, orgSlug, version)
	if err != nil || releaseID == "" {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, release_id, environment, name, url, date_started, date_finished, created_at
		   FROM release_deploys
		  WHERE release_id = $1
		  ORDER BY created_at DESC
		  LIMIT $2`,
		releaseID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]sharedstore.ReleaseDeploy, 0, limit)
	for rows.Next() {
		var item sharedstore.ReleaseDeploy
		var environment, name, url sql.NullString
		var dateStarted, dateFinished, createdAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.ReleaseID, &environment, &name, &url, &dateStarted, &dateFinished, &createdAt); err != nil {
			return nil, err
		}
		item.ReleaseVersion = version
		item.Environment = nullString(environment)
		item.Name = nullString(name)
		item.URL = nullString(url)
		item.DateStarted = nullTime(dateStarted)
		item.DateFinished = nullTime(dateFinished)
		item.DateCreated = nullTime(createdAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

// AddCommit stores one release commit.
func (s *ReleaseStore) AddCommit(ctx context.Context, orgSlug, version string, commit sharedstore.ReleaseCommit) (*sharedstore.ReleaseCommit, error) {
	releaseID, err := s.lookupReleaseID(ctx, orgSlug, version)
	if err != nil || releaseID == "" {
		return nil, err
	}
	commit.CommitSHA = strings.TrimSpace(commit.CommitSHA)
	if commit.CommitSHA == "" {
		return nil, nil
	}
	if commit.ID == "" {
		commit.ID = id.New()
	}
	commit.ReleaseID = releaseID
	commit.ReleaseVersion = version
	if commit.DateCreated.IsZero() {
		commit.DateCreated = time.Now().UTC()
	}
	filesJSON, err := json.Marshal(commit.Files)
	if err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO release_commits
			(id, release_id, commit_sha, repository, author_name, author_email, message, files_json, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9)
		 ON CONFLICT (release_id, commit_sha) DO UPDATE SET
			repository = EXCLUDED.repository,
			author_name = EXCLUDED.author_name,
			author_email = EXCLUDED.author_email,
			message = EXCLUDED.message,
			files_json = EXCLUDED.files_json`,
		commit.ID, commit.ReleaseID, commit.CommitSHA, strings.TrimSpace(commit.Repository),
		strings.TrimSpace(commit.AuthorName), strings.TrimSpace(commit.AuthorEmail), strings.TrimSpace(commit.Message),
		string(filesJSON), commit.DateCreated.UTC(),
	)
	if err != nil {
		return nil, err
	}
	return &commit, nil
}

// ListCommits lists recent release commits.
func (s *ReleaseStore) ListCommits(ctx context.Context, orgSlug, version string, limit int) ([]sharedstore.ReleaseCommit, error) {
	releaseID, err := s.lookupReleaseID(ctx, orgSlug, version)
	if err != nil || releaseID == "" {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, release_id, commit_sha, repository, author_name, author_email, message, files_json, created_at
		   FROM release_commits
		  WHERE release_id = $1
		  ORDER BY created_at DESC
		  LIMIT $2`,
		releaseID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]sharedstore.ReleaseCommit, 0, limit)
	for rows.Next() {
		var item sharedstore.ReleaseCommit
		var repository, authorName, authorEmail, message sql.NullString
		var filesJSON []byte
		var createdAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.ReleaseID, &item.CommitSHA, &repository, &authorName, &authorEmail, &message, &filesJSON, &createdAt); err != nil {
			return nil, err
		}
		item.ReleaseVersion = version
		item.Repository = nullString(repository)
		item.AuthorName = nullString(authorName)
		item.AuthorEmail = nullString(authorEmail)
		item.Message = nullString(message)
		item.DateCreated = nullTime(createdAt)
		_ = json.Unmarshal(filesJSON, &item.Files)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *ReleaseStore) ProjectHasRelease(ctx context.Context, projectID, version string) (bool, error) {
	var match int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1
		 WHERE EXISTS (
			SELECT 1
			  FROM release_projects rp
			  JOIN releases r ON r.id = rp.release_id
			 WHERE rp.project_id = $1 AND r.version = $2
		 )
		    OR EXISTS (
			SELECT 1 FROM release_sessions WHERE project_id = $1 AND release = $2
		 )`,
		projectID, version,
	).Scan(&match)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return match == 1, err
}

// ListSuspects derives likely issue/commit matches for a release.
func (s *ReleaseStore) ListSuspects(ctx context.Context, orgSlug, version string, limit int) ([]sharedstore.ReleaseSuspect, error) {
	commits, err := s.ListCommits(ctx, orgSlug, version, 200)
	if err != nil || len(commits) == 0 {
		return nil, err
	}
	if limit <= 0 {
		limit = 25
	}

	releaseID, err := s.lookupReleaseID(ctx, orgSlug, version)
	if err != nil || releaseID == "" {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`WITH scoped_projects AS (
			SELECT project_id FROM release_projects WHERE release_id = $1
		)
		SELECT DISTINCT g.id, COALESCE(g.short_id, 0), COALESCE(g.title, ''), COALESCE(g.culprit, ''), g.last_seen
		  FROM groups g
		  JOIN projects p ON p.id = g.project_id
		  JOIN organizations o ON o.id = p.organization_id
		 WHERE o.slug = $2
		   AND (
		        COALESCE(g.resolved_in_release, '') = $3
		        OR EXISTS (SELECT 1 FROM scoped_projects sp WHERE sp.project_id = g.project_id)
		        OR NOT EXISTS (SELECT 1 FROM scoped_projects)
		   )
		 ORDER BY g.last_seen DESC NULLS LAST
		 LIMIT $4`,
		releaseID, strings.TrimSpace(orgSlug), strings.TrimSpace(version), limit*4,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type releaseIssue struct {
		GroupID  string
		ShortID  int
		Title    string
		Culprit  string
		LastSeen time.Time
	}
	issues := make([]releaseIssue, 0, limit*4)
	for rows.Next() {
		var item releaseIssue
		var shortID sql.NullInt64
		var title, culprit sql.NullString
		var lastSeen sql.NullTime
		if err := rows.Scan(&item.GroupID, &shortID, &title, &culprit, &lastSeen); err != nil {
			return nil, err
		}
		item.ShortID = int(shortID.Int64)
		item.Title = nullString(title)
		item.Culprit = nullString(culprit)
		item.LastSeen = nullTime(lastSeen)
		issues = append(issues, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	suspects := make([]sharedstore.ReleaseSuspect, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, item := range issues {
		for _, commit := range commits {
			matchedFile := matchCommitToIssue(commit, item.Title, item.Culprit)
			if matchedFile == "" {
				continue
			}
			key := item.GroupID + ":" + commit.CommitSHA
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			suspects = append(suspects, sharedstore.ReleaseSuspect{
				GroupID:     item.GroupID,
				ShortID:     item.ShortID,
				Title:       item.Title,
				Culprit:     item.Culprit,
				LastSeen:    item.LastSeen,
				CommitSHA:   commit.CommitSHA,
				Repository:  commit.Repository,
				AuthorName:  commit.AuthorName,
				Message:     commit.Message,
				MatchedFile: matchedFile,
				ReleaseID:   commit.ReleaseID,
				Release:     version,
			})
			if len(suspects) >= limit {
				return suspects, nil
			}
		}
	}
	return suspects, nil
}

func (s *ReleaseStore) lookupReleaseID(ctx context.Context, orgSlug, version string) (string, error) {
	var releaseID string
	err := s.db.QueryRowContext(ctx,
		`SELECT r.id
		   FROM releases r
		   JOIN organizations o ON o.id = r.organization_id
		  WHERE o.slug = $1 AND r.version = $2`,
		strings.TrimSpace(orgSlug), strings.TrimSpace(version),
	).Scan(&releaseID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return releaseID, err
}

func bindResolvedNextReleaseIssues(ctx context.Context, db *sql.DB, orgID, version string) error {
	orgID = strings.TrimSpace(orgID)
	version = strings.TrimSpace(version)
	if orgID == "" || version == "" {
		return nil
	}
	_, err := db.ExecContext(ctx,
		`UPDATE groups
		    SET resolved_in_release = $1,
		        updated_at = now()
		  WHERE status = 'resolved'
		    AND COALESCE(substatus, '') = 'next_release'
		    AND COALESCE(resolved_in_release, '') = ''
		    AND project_id IN (SELECT id FROM projects WHERE organization_id = $2)`,
		version, orgID,
	)
	return err
}

const releaseSummarySelectSQL = `SELECT r.id, r.organization_id, r.version, r.date_released, r.created_at,
       COALESCE((SELECT SUM(rp.new_groups) FROM release_projects rp WHERE rp.release_id = r.id), 0) AS event_count,
       COALESCE((SELECT COUNT(*) FROM release_sessions rs JOIN projects p ON p.id = rs.project_id WHERE p.organization_id = r.organization_id AND rs.release = r.version), 0) AS session_count,
       COALESCE((SELECT COUNT(*) FROM release_sessions rs JOIN projects p ON p.id = rs.project_id WHERE p.organization_id = r.organization_id AND rs.release = r.version AND rs.status = 'errored'), 0) AS errored_sessions,
       COALESCE((SELECT COUNT(*) FROM release_sessions rs JOIN projects p ON p.id = rs.project_id WHERE p.organization_id = r.organization_id AND rs.release = r.version AND rs.status = 'crashed'), 0) AS crashed_sessions,
       COALESCE((SELECT COUNT(*) FROM release_sessions rs JOIN projects p ON p.id = rs.project_id WHERE p.organization_id = r.organization_id AND rs.release = r.version AND rs.status = 'abnormal'), 0) AS abnormal_sessions,
       COALESCE((SELECT COUNT(DISTINCT rs.user_identifier) FROM release_sessions rs JOIN projects p ON p.id = rs.project_id WHERE p.organization_id = r.organization_id AND rs.release = r.version AND rs.user_identifier <> ''), 0) AS affected_users,
       (SELECT MAX(rs.timestamp) FROM release_sessions rs JOIN projects p ON p.id = rs.project_id WHERE p.organization_id = r.organization_id AND rs.release = r.version) AS last_session_at,
       COALESCE(r.ref, ''),
       COALESCE(r.url, '')
  FROM releases r`

func scanRelease(scanner rowScanner) (sqlite.Release, error) {
	var item sqlite.Release
	var dateReleased, createdAt, lastSessionAt sql.NullTime
	err := scanner.Scan(
		&item.ID, &item.OrganizationID, &item.Version, &dateReleased, &createdAt,
		&item.EventCount, &item.SessionCount, &item.ErroredSessions, &item.CrashedSessions, &item.AbnormalSessions, &item.AffectedUsers, &lastSessionAt,
		&item.Ref, &item.URL,
	)
	if err != nil {
		return item, err
	}
	item.DateReleased = nullTime(dateReleased)
	item.CreatedAt = nullTime(createdAt)
	item.LastSessionAt = nullTime(lastSessionAt)
	if item.SessionCount > 0 {
		bad := item.CrashedSessions + item.AbnormalSessions
		if bad < 0 {
			bad = 0
		}
		if bad > item.SessionCount {
			bad = item.SessionCount
		}
		item.CrashFreeRate = (float64(item.SessionCount-bad) / float64(item.SessionCount)) * 100
	} else {
		item.CrashFreeRate = 100
	}
	return item, nil
}

func matchCommitToIssue(commit sharedstore.ReleaseCommit, title, culprit string) string {
	text := strings.ToLower(title + " " + culprit)
	for _, file := range commit.Files {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		base := strings.ToLower(filepath.Base(file))
		if base != "" && strings.Contains(text, base) {
			return file
		}
		if strings.Contains(text, strings.ToLower(file)) {
			return file
		}
	}
	return ""
}
