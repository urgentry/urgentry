package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	sharedstore "urgentry/internal/store"
	"urgentry/pkg/id"
)

// Release represents a row from the releases table.
type Release struct {
	ID               string
	OrganizationID   string
	Version          string
	DateReleased     time.Time
	CreatedAt        time.Time
	EventCount       int // populated by queries that join with events
	SessionCount     int
	ErroredSessions  int
	CrashedSessions  int
	AbnormalSessions int
	AffectedUsers    int
	CrashFreeRate    float64
	LastSessionAt    time.Time
}

// ReleaseStore persists and queries release records in SQLite.
type ReleaseStore struct {
	db *sql.DB
}

// NewReleaseStore creates a ReleaseStore backed by the given database.
func NewReleaseStore(db *sql.DB) *ReleaseStore {
	return &ReleaseStore{db: db}
}

// EnsureRelease creates a release record if one doesn't already exist for the
// given (organization, version) pair. Uses INSERT OR IGNORE so it is safe to
// call repeatedly for the same version.
func (s *ReleaseStore) EnsureRelease(ctx context.Context, orgID, version string) error {
	return ensureReleaseForOwner(ctx, s.db, orgID, version)
}

// CreateRelease creates or returns a release by organization slug and version.
func (s *ReleaseStore) CreateRelease(ctx context.Context, orgSlug, version string) (*Release, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	var orgID string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM organizations WHERE slug = ?`, orgSlug).Scan(&orgID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO releases (id, organization_id, version, created_at)
		 VALUES (?, ?, ?, ?)`,
		id.New(), orgID, version, now,
	)
	if err != nil {
		return nil, err
	}
	if rows, rowsErr := res.RowsAffected(); rowsErr == nil && rows > 0 {
		if err := bindResolvedNextReleaseIssues(ctx, s.db, orgID, version); err != nil {
			return nil, err
		}
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT id, organization_id, version, date_released, created_at
		   FROM releases
		  WHERE organization_id = ? AND version = ?`,
		orgID, version,
	)
	var rel Release
	var dateReleased, createdAt sql.NullString
	if err := row.Scan(&rel.ID, &rel.OrganizationID, &rel.Version, &dateReleased, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rel.DateReleased = parseTime(nullStr(dateReleased))
	rel.CreatedAt = parseTime(nullStr(createdAt))
	return &rel, nil
}

// ListReleases returns releases for an organization ordered by creation date
// descending. Each release includes an event count from the events table.
func (s *ReleaseStore) ListReleases(ctx context.Context, orgID string, limit int) ([]Release, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, r.organization_id, r.version, r.date_released, r.created_at,
		        COALESCE((
		            SELECT COUNT(*)
		              FROM events ev
		              JOIN projects ep ON ep.id = ev.project_id
		             WHERE ep.organization_id = r.organization_id
		               AND COALESCE(ev.release, '') = r.version
		        ), 0),
		        COALESCE(SUM(rs.quantity), 0),
		        COALESCE(SUM(CASE WHEN rs.status = 'errored' THEN rs.quantity ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN rs.status = 'crashed' THEN rs.quantity ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN rs.status = 'abnormal' THEN rs.quantity ELSE 0 END), 0),
		        COUNT(DISTINCT CASE WHEN rs.distinct_id IS NOT NULL AND rs.distinct_id != '' THEN rs.distinct_id END),
		        MAX(rs.created_at)
		 FROM releases r
		 LEFT JOIN projects p ON p.organization_id = r.organization_id
		 LEFT JOIN release_sessions rs ON rs.project_id = p.id AND rs.release_version = r.version
		 WHERE r.organization_id = ?
		 GROUP BY r.id, r.organization_id, r.version, r.date_released, r.created_at
		 ORDER BY r.created_at DESC
		 LIMIT ?`,
		orgID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var releases []Release
	for rows.Next() {
		var rel Release
		var dateReleased, createdAt, lastSessionAt sql.NullString
		if err := rows.Scan(&rel.ID, &rel.OrganizationID, &rel.Version,
			&dateReleased, &createdAt, &rel.EventCount, &rel.SessionCount, &rel.ErroredSessions,
			&rel.CrashedSessions, &rel.AbnormalSessions, &rel.AffectedUsers, &lastSessionAt); err != nil {
			return nil, err
		}
		rel.DateReleased = parseTime(nullStr(dateReleased))
		rel.CreatedAt = parseTime(nullStr(createdAt))
		rel.LastSessionAt = parseTime(nullStr(lastSessionAt))
		if rel.SessionCount > 0 {
			bad := rel.CrashedSessions + rel.AbnormalSessions
			if bad < 0 {
				bad = 0
			}
			if bad > rel.SessionCount {
				bad = rel.SessionCount
			}
			rel.CrashFreeRate = (float64(rel.SessionCount-bad) / float64(rel.SessionCount)) * 100
		} else {
			rel.CrashFreeRate = 100
		}
		releases = append(releases, rel)
	}
	return releases, rows.Err()
}

// GetRelease retrieves a single release by organization and version.
func (s *ReleaseStore) GetRelease(ctx context.Context, orgID, version string) (*Release, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT r.id, r.organization_id, r.version, r.date_released, r.created_at,
		        COALESCE((
		            SELECT COUNT(*)
		              FROM events ev
		              JOIN projects ep ON ep.id = ev.project_id
		             WHERE ep.organization_id = r.organization_id
		               AND COALESCE(ev.release, '') = r.version
		        ), 0),
		        COALESCE(SUM(rs.quantity), 0),
		        COALESCE(SUM(CASE WHEN rs.status = 'errored' THEN rs.quantity ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN rs.status = 'crashed' THEN rs.quantity ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN rs.status = 'abnormal' THEN rs.quantity ELSE 0 END), 0),
		        COUNT(DISTINCT CASE WHEN rs.distinct_id IS NOT NULL AND rs.distinct_id != '' THEN rs.distinct_id END),
		        MAX(rs.created_at)
		 FROM releases r
		 LEFT JOIN projects p ON p.organization_id = r.organization_id
		 LEFT JOIN release_sessions rs ON rs.project_id = p.id AND rs.release_version = r.version
		 WHERE r.organization_id = ? AND r.version = ?`,
		orgID, version,
	)
	var rel Release
	var dateReleased, createdAt, lastSessionAt sql.NullString
	err := row.Scan(&rel.ID, &rel.OrganizationID, &rel.Version,
		&dateReleased, &createdAt, &rel.EventCount, &rel.SessionCount, &rel.ErroredSessions,
		&rel.CrashedSessions, &rel.AbnormalSessions, &rel.AffectedUsers, &lastSessionAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rel.DateReleased = parseTime(nullStr(dateReleased))
	rel.CreatedAt = parseTime(nullStr(createdAt))
	rel.LastSessionAt = parseTime(nullStr(lastSessionAt))
	if rel.SessionCount > 0 {
		bad := rel.CrashedSessions + rel.AbnormalSessions
		if bad < 0 {
			bad = 0
		}
		if bad > rel.SessionCount {
			bad = rel.SessionCount
		}
		rel.CrashFreeRate = (float64(rel.SessionCount-bad) / float64(rel.SessionCount)) * 100
	} else {
		rel.CrashFreeRate = 100
	}
	return &rel, nil
}

func (s *ReleaseStore) GetReleaseBySlug(ctx context.Context, orgSlug, version string) (*Release, error) {
	var orgID string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM organizations WHERE slug = ?`, orgSlug).Scan(&orgID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return s.GetRelease(ctx, orgID, version)
}

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
		`INSERT INTO release_deploys (id, release_id, environment, name, url, date_started, date_finished, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		deploy.ID, deploy.ReleaseID, strings.TrimSpace(deploy.Environment), strings.TrimSpace(deploy.Name), strings.TrimSpace(deploy.URL),
		timeOrNull(deploy.DateStarted), timeOrNull(deploy.DateFinished), deploy.DateCreated.Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	return &deploy, nil
}

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
		 WHERE release_id = ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		releaseID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []sharedstore.ReleaseDeploy
	for rows.Next() {
		var item sharedstore.ReleaseDeploy
		var environment, name, url, dateStarted, dateFinished, createdAt sql.NullString
		if err := rows.Scan(&item.ID, &item.ReleaseID, &environment, &name, &url, &dateStarted, &dateFinished, &createdAt); err != nil {
			return nil, err
		}
		item.ReleaseVersion = version
		item.Environment = nullStr(environment)
		item.Name = nullStr(name)
		item.URL = nullStr(url)
		item.DateStarted = parseTime(nullStr(dateStarted))
		item.DateFinished = parseTime(nullStr(dateFinished))
		item.DateCreated = parseTime(nullStr(createdAt))
		items = append(items, item)
	}
	return items, rows.Err()
}

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
		`INSERT INTO release_commits (id, release_id, commit_sha, repository, author_name, author_email, message, files_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(release_id, commit_sha) DO UPDATE SET
			repository = excluded.repository,
			author_name = excluded.author_name,
			author_email = excluded.author_email,
			message = excluded.message,
			files_json = excluded.files_json`,
		commit.ID, commit.ReleaseID, commit.CommitSHA, strings.TrimSpace(commit.Repository),
		strings.TrimSpace(commit.AuthorName), strings.TrimSpace(commit.AuthorEmail), strings.TrimSpace(commit.Message),
		string(filesJSON), commit.DateCreated.Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	return &commit, nil
}

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
		 WHERE release_id = ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		releaseID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []sharedstore.ReleaseCommit
	for rows.Next() {
		var item sharedstore.ReleaseCommit
		var repository, authorName, authorEmail, message, filesJSON, createdAt sql.NullString
		if err := rows.Scan(&item.ID, &item.ReleaseID, &item.CommitSHA, &repository, &authorName, &authorEmail, &message, &filesJSON, &createdAt); err != nil {
			return nil, err
		}
		item.ReleaseVersion = version
		item.Repository = nullStr(repository)
		item.AuthorName = nullStr(authorName)
		item.AuthorEmail = nullStr(authorEmail)
		item.Message = nullStr(message)
		item.DateCreated = parseTime(nullStr(createdAt))
		_ = json.Unmarshal([]byte(nullStr(filesJSON)), &item.Files)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *ReleaseStore) ListSuspects(ctx context.Context, orgSlug, version string, limit int) ([]sharedstore.ReleaseSuspect, error) {
	commits, err := s.ListCommits(ctx, orgSlug, version, 200)
	if err != nil || len(commits) == 0 {
		return nil, err
	}
	if limit <= 0 {
		limit = 25
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT g.id, COALESCE(g.short_id, 0), COALESCE(g.title, ''), COALESCE(g.culprit, ''), COALESCE(g.last_seen, '')
		 FROM groups g
		 JOIN projects p ON p.id = g.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 JOIN events e ON e.group_id = g.id
		 WHERE o.slug = ? AND COALESCE(e.release, '') = ?
		 ORDER BY g.last_seen DESC
		 LIMIT ?`,
		orgSlug, version, limit*4,
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
	var issues []releaseIssue
	for rows.Next() {
		var item releaseIssue
		var title, culprit, lastSeen sql.NullString
		var shortID sql.NullInt64
		if err := rows.Scan(&item.GroupID, &shortID, &title, &culprit, &lastSeen); err != nil {
			return nil, err
		}
		item.ShortID = int(shortID.Int64)
		item.Title = nullStr(title)
		item.Culprit = nullStr(culprit)
		item.LastSeen = parseTime(nullStr(lastSeen))
		issues = append(issues, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	suspects := make([]sharedstore.ReleaseSuspect, 0, limit)
	seen := map[string]struct{}{}
	for _, issue := range issues {
		for _, commit := range commits {
			matchedFile := matchCommitToIssue(commit, issue.Title, issue.Culprit)
			if matchedFile == "" {
				continue
			}
			key := issue.GroupID + ":" + commit.CommitSHA
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			suspects = append(suspects, sharedstore.ReleaseSuspect{
				GroupID:     issue.GroupID,
				ShortID:     issue.ShortID,
				Title:       issue.Title,
				Culprit:     issue.Culprit,
				LastSeen:    issue.LastSeen,
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
		 WHERE o.slug = ? AND r.version = ?`,
		orgSlug, version,
	).Scan(&releaseID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return releaseID, err
}

func timeOrNull(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Format(time.RFC3339)
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
