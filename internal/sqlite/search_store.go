package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/discover"
)

type SavedSearchVisibility string

const (
	SavedSearchVisibilityPrivate      SavedSearchVisibility = "private"
	SavedSearchVisibilityOrganization SavedSearchVisibility = "organization"
)

// SavedSearch represents a persisted filter+query combination.
type SavedSearch struct {
	ID               string
	UserID           string
	OrganizationSlug string
	Visibility       SavedSearchVisibility
	Name             string
	Description      string
	Tags             []string
	Favorite         bool
	Query            string
	Filter           string
	Environment      string
	Sort             string
	QueryVersion     int
	QueryDoc         discover.Query
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// SearchStore provides CRUD operations for saved searches.
type SearchStore struct {
	db *sql.DB
}

// NewSearchStore creates a SearchStore backed by the given database.
func NewSearchStore(db *sql.DB) *SearchStore {
	return &SearchStore{db: db}
}

// Save persists a new saved search for a user.
func (s *SearchStore) Save(ctx context.Context, userID, organizationSlug string, visibility SavedSearchVisibility, name, description, query, filter, env, sort string, favorite bool) (*SavedSearch, error) {
	if filter == "" {
		filter = "all"
	}
	if sort == "" {
		sort = "last_seen"
	}
	orgSlug := strings.TrimSpace(organizationSlug)
	if orgSlug == "" {
		var err error
		orgSlug, err = s.defaultOrganizationSlug(ctx)
		if err != nil {
			return nil, err
		}
	}
	queryDoc, _, err := discover.ParseLegacy(discover.LegacyInput{
		Dataset:      discover.DatasetIssues,
		Organization: orgSlug,
		Filter:       filter,
		Query:        query,
		Environment:  env,
		Limit:        50,
	})
	if err != nil {
		return nil, err
	}
	return s.SaveQuery(ctx, userID, orgSlug, visibility, name, description, query, filter, env, sort, favorite, queryDoc)
}

// SaveQuery persists a named AST-backed saved query for a user.
func (s *SearchStore) SaveQuery(ctx context.Context, userID, organizationSlug string, visibility SavedSearchVisibility, name, description, query, filter, env, sort string, favorite bool, queryDoc discover.Query) (*SavedSearch, error) {
	id := generateID()
	now := time.Now().UTC()
	if filter == "" {
		filter = "all"
	}
	if sort == "" {
		sort = "last_seen"
	}
	orgSlug, err := s.normalizeOrganizationSlug(ctx, organizationSlug, queryDoc)
	if err != nil {
		return nil, err
	}
	normalizedVisibility := normalizeSavedSearchVisibility(visibility)
	if queryDoc.Scope.Kind == discover.ScopeKindOrganization {
		queryDoc.Scope.Organization = orgSlug
	}
	queryJSON, err := discover.MarshalQuery(queryDoc)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO saved_searches (id, user_id, organization_slug, visibility, name, description, query, filter, environment, sort, query_version, query_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, userID, orgSlug, string(normalizedVisibility), name, description, query, filter, env, sort, discover.CurrentVersion, string(queryJSON), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
	); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if favorite {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO saved_search_favorites (saved_search_id, user_id, created_at) VALUES (?, ?, ?)`,
			id, userID, now.Format(time.RFC3339Nano),
		); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &SavedSearch{
		ID:               id,
		UserID:           userID,
		OrganizationSlug: orgSlug,
		Visibility:       normalizedVisibility,
		Name:             name,
		Description:      description,
		Tags:             nil,
		Favorite:         favorite,
		Query:            query,
		Filter:           filter,
		Environment:      env,
		Sort:             sort,
		QueryVersion:     discover.CurrentVersion,
		QueryDoc:         queryDoc,
		CreatedAt:        now,
		UpdatedAt:        now,
	}, nil
}

// List returns the user's private searches plus organization-shared searches in the same org.
func (s *SearchStore) List(ctx context.Context, userID, organizationSlug string) ([]SavedSearch, error) {
	defaultOrgSlug := s.mustDefaultOrganizationSlug(ctx)
	orgSlug := strings.TrimSpace(organizationSlug)
	if orgSlug == "" {
		orgSlug = defaultOrgSlug
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT s.id,
		        s.user_id,
		        COALESCE(s.organization_slug, ''),
		        COALESCE(s.visibility, ''),
		        s.name,
		        COALESCE(s.description, ''),
		        COALESCE((
		            SELECT GROUP_CONCAT(tag, ',')
		            FROM (
		                SELECT tag
		                FROM saved_search_tags
		                WHERE saved_search_id = s.id
		                ORDER BY LOWER(tag), tag
		            )
		        ), ''),
		        CASE WHEN f.user_id IS NULL THEN 0 ELSE 1 END,
		        s.query,
		        s.filter,
		        s.environment,
		        s.sort,
		        COALESCE(s.query_version, 0),
		        COALESCE(s.query_json, ''),
		        s.created_at,
		        COALESCE(s.updated_at, s.created_at)
		 FROM saved_searches s
		 LEFT JOIN saved_search_favorites f
		   ON f.saved_search_id = s.id AND f.user_id = ?
		 WHERE COALESCE(NULLIF(s.organization_slug, ''), ?) = ? AND (s.user_id = ? OR s.visibility = ?)
		 ORDER BY CASE WHEN f.user_id IS NULL THEN 1 ELSE 0 END,
		          CASE WHEN s.user_id = ? THEN 0 ELSE 1 END,
		          LOWER(s.name) ASC,
		          s.created_at DESC`,
		userID, defaultOrgSlug, orgSlug, userID, string(SavedSearchVisibilityOrganization), userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var searches []SavedSearch
	for rows.Next() {
		ss, err := scanSavedSearch(rows, defaultOrgSlug)
		if err != nil {
			return nil, err
		}
		searches = append(searches, ss)
	}
	return searches, rows.Err()
}

// Get returns one visible saved search by ID.
func (s *SearchStore) Get(ctx context.Context, userID, organizationSlug, id string) (*SavedSearch, error) {
	defaultOrgSlug := s.mustDefaultOrganizationSlug(ctx)
	orgSlug := strings.TrimSpace(organizationSlug)
	if orgSlug == "" {
		orgSlug = defaultOrgSlug
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT s.id,
		        s.user_id,
		        COALESCE(s.organization_slug, ''),
		        COALESCE(s.visibility, ''),
		        s.name,
		        COALESCE(s.description, ''),
		        COALESCE((
		            SELECT GROUP_CONCAT(tag, ',')
		            FROM (
		                SELECT tag
		                FROM saved_search_tags
		                WHERE saved_search_id = s.id
		                ORDER BY LOWER(tag), tag
		            )
		        ), ''),
		        CASE WHEN f.user_id IS NULL THEN 0 ELSE 1 END,
		        s.query,
		        s.filter,
		        s.environment,
		        s.sort,
		        COALESCE(s.query_version, 0),
		        COALESCE(s.query_json, ''),
		        s.created_at,
		        COALESCE(s.updated_at, s.created_at)
		 FROM saved_searches s
		 LEFT JOIN saved_search_favorites f
		   ON f.saved_search_id = s.id AND f.user_id = ?
		 WHERE s.id = ? AND COALESCE(NULLIF(s.organization_slug, ''), ?) = ? AND (s.user_id = ? OR s.visibility = ?)
		 LIMIT 1`,
		userID, id, defaultOrgSlug, orgSlug, userID, string(SavedSearchVisibilityOrganization),
	)
	item, err := scanSavedSearch(row, defaultOrgSlug)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

// Delete removes a saved search by ID for a user.
func (s *SearchStore) Delete(ctx context.Context, userID, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM saved_searches WHERE user_id = ? AND id = ?", userID, id)
	return err
}

// UpdateMetadata updates the caller-owned saved query metadata and tags.
func (s *SearchStore) UpdateMetadata(ctx context.Context, userID, organizationSlug, id, name, description string, visibility SavedSearchVisibility, tags []string) (*SavedSearch, error) {
	defaultOrgSlug := s.mustDefaultOrganizationSlug(ctx)
	orgSlug := strings.TrimSpace(organizationSlug)
	if orgSlug == "" {
		orgSlug = defaultOrgSlug
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx,
		`UPDATE saved_searches
		 SET name = ?, description = ?, visibility = ?, updated_at = ?
		 WHERE id = ? AND user_id = ? AND COALESCE(NULLIF(organization_slug, ''), ?) = ?`,
		strings.TrimSpace(name),
		strings.TrimSpace(description),
		string(normalizeSavedSearchVisibility(visibility)),
		now,
		id,
		userID,
		defaultOrgSlug,
		orgSlug,
	)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if affected == 0 {
		_ = tx.Rollback()
		return nil, sql.ErrNoRows
	}
	if err := replaceSavedSearchTags(ctx, tx, id, tags); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Get(ctx, userID, orgSlug, id)
}

// SetFavorite updates the caller's favorite state for a visible saved search.
func (s *SearchStore) SetFavorite(ctx context.Context, userID, organizationSlug, id string, favorite bool) error {
	defaultOrgSlug := s.mustDefaultOrganizationSlug(ctx)
	orgSlug := strings.TrimSpace(organizationSlug)
	if orgSlug == "" {
		orgSlug = defaultOrgSlug
	}
	var visibleID string
	if err := s.db.QueryRowContext(ctx,
		`SELECT id
		 FROM saved_searches
		 WHERE id = ? AND COALESCE(NULLIF(organization_slug, ''), ?) = ? AND (user_id = ? OR visibility = ?)
		 LIMIT 1`,
		id, defaultOrgSlug, orgSlug, userID, string(SavedSearchVisibilityOrganization),
	).Scan(&visibleID); err != nil {
		return err
	}
	if favorite {
		_, err := s.db.ExecContext(ctx,
			`INSERT OR IGNORE INTO saved_search_favorites (saved_search_id, user_id, created_at) VALUES (?, ?, ?)`,
			id, userID, time.Now().UTC().Format(time.RFC3339Nano),
		)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM saved_search_favorites WHERE saved_search_id = ? AND user_id = ?`, id, userID)
	return err
}

// Clone creates a caller-owned copy of a visible saved search.
func (s *SearchStore) Clone(ctx context.Context, userID, organizationSlug, id, name string, visibility SavedSearchVisibility, favorite bool) (*SavedSearch, error) {
	source, err := s.Get(ctx, userID, organizationSlug, id)
	if err != nil || source == nil {
		return source, err
	}
	cloneName := strings.TrimSpace(name)
	if cloneName == "" {
		cloneName = source.Name + " copy"
	}
	cloneVisibility := normalizeSavedSearchVisibility(visibility)
	if strings.TrimSpace(string(visibility)) == "" {
		cloneVisibility = SavedSearchVisibilityPrivate
	}
	cloned, err := s.SaveQuery(
		ctx,
		userID,
		source.OrganizationSlug,
		cloneVisibility,
		cloneName,
		source.Description,
		source.Query,
		source.Filter,
		source.Environment,
		source.Sort,
		favorite,
		source.QueryDoc,
	)
	if err != nil || cloned == nil || len(source.Tags) == 0 {
		return cloned, err
	}
	return s.UpdateMetadata(ctx, userID, source.OrganizationSlug, cloned.ID, cloned.Name, cloned.Description, cloned.Visibility, source.Tags)
}

func (s *SearchStore) defaultOrganizationSlug(ctx context.Context) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("search store is not configured")
	}
	var slug string
	if err := s.db.QueryRowContext(ctx, `SELECT slug FROM organizations ORDER BY created_at ASC, slug ASC LIMIT 1`).Scan(&slug); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("no organization available for saved query scope")
		}
		return "", err
	}
	return slug, nil
}

func (s *SearchStore) mustDefaultOrganizationSlug(ctx context.Context) string {
	slug, err := s.defaultOrganizationSlug(ctx)
	if err != nil {
		return "default-org"
	}
	return slug
}

type savedSearchScanner interface {
	Scan(dest ...any) error
}

func scanSavedSearch(scanner savedSearchScanner, defaultOrgSlug string) (SavedSearch, error) {
	var ss SavedSearch
	var userID, organizationSlug, visibility, name, description, tags, query, filter, env, sort, queryJSON, createdAt, updatedAt sql.NullString
	var queryVersion, favorite sql.NullInt64
	if err := scanner.Scan(&ss.ID, &userID, &organizationSlug, &visibility, &name, &description, &tags, &favorite, &query, &filter, &env, &sort, &queryVersion, &queryJSON, &createdAt, &updatedAt); err != nil {
		return SavedSearch{}, err
	}
	ss.UserID = nullStr(userID)
	ss.OrganizationSlug = nullStr(organizationSlug)
	if ss.OrganizationSlug == "" {
		ss.OrganizationSlug = defaultOrgSlug
	}
	ss.Visibility = normalizeSavedSearchVisibility(SavedSearchVisibility(nullStr(visibility)))
	ss.Name = nullStr(name)
	ss.Description = nullStr(description)
	ss.Tags = parseSavedSearchTags(nullStr(tags))
	ss.Favorite = favorite.Valid && favorite.Int64 != 0
	ss.Query = nullStr(query)
	ss.Filter = nullStr(filter)
	ss.Environment = nullStr(env)
	ss.Sort = nullStr(sort)
	if queryVersion.Valid {
		ss.QueryVersion = int(queryVersion.Int64)
	}
	if raw := nullStr(queryJSON); raw != "" {
		doc, _, err := discover.UnmarshalQuery([]byte(raw))
		if err != nil {
			return SavedSearch{}, fmt.Errorf("decode saved search %s query: %w", ss.ID, err)
		}
		ss.QueryDoc = doc
	} else {
		doc, _, err := discover.ParseLegacy(discover.LegacyInput{
			Dataset:      discover.DatasetIssues,
			Organization: ss.OrganizationSlug,
			Filter:       ss.Filter,
			Query:        ss.Query,
			Environment:  ss.Environment,
			Limit:        50,
		})
		if err != nil {
			return SavedSearch{}, fmt.Errorf("upgrade saved search %s: %w", ss.ID, err)
		}
		ss.QueryVersion = discover.CurrentVersion
		ss.QueryDoc = doc
	}
	if ca := nullStr(createdAt); ca != "" {
		ss.CreatedAt = parseTime(ca)
	}
	if ua := nullStr(updatedAt); ua != "" {
		ss.UpdatedAt = parseTime(ua)
	}
	return ss, nil
}

func normalizeSavedSearchVisibility(raw SavedSearchVisibility) SavedSearchVisibility {
	switch strings.ToLower(strings.TrimSpace(string(raw))) {
	case string(SavedSearchVisibilityOrganization):
		return SavedSearchVisibilityOrganization
	default:
		return SavedSearchVisibilityPrivate
	}
}

func (s *SearchStore) normalizeOrganizationSlug(ctx context.Context, organizationSlug string, queryDoc discover.Query) (string, error) {
	orgSlug := strings.TrimSpace(organizationSlug)
	if orgSlug == "" && queryDoc.Scope.Kind == discover.ScopeKindOrganization {
		orgSlug = strings.TrimSpace(queryDoc.Scope.Organization)
	}
	if orgSlug != "" {
		return orgSlug, nil
	}
	return s.defaultOrganizationSlug(ctx)
}

func normalizeSavedSearchTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tags))
	for _, raw := range tags {
		tag := strings.ToLower(strings.TrimSpace(raw))
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

func parseSavedSearchTags(raw string) []string {
	if raw == "" {
		return nil
	}
	return normalizeSavedSearchTags(strings.Split(raw, ","))
}

func replaceSavedSearchTags(ctx context.Context, tx *sql.Tx, id string, tags []string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `DELETE FROM saved_search_tags WHERE saved_search_id = ?`, id); err != nil {
		return err
	}
	for _, tag := range normalizeSavedSearchTags(tags) {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO saved_search_tags (saved_search_id, tag, created_at) VALUES (?, ?, ?)`,
			id, tag, now,
		); err != nil {
			return err
		}
	}
	return nil
}
