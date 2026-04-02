package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"urgentry/internal/store"
)

var _ store.PreventStore = (*PreventStore)(nil)

// PreventStore persists Prevent repository-management records in SQLite.
type PreventStore struct {
	db *sql.DB
}

// NewPreventStore creates a PreventStore backed by SQLite.
func NewPreventStore(db *sql.DB) *PreventStore {
	return &PreventStore{db: db}
}

func (s *PreventStore) ListRepositories(ctx context.Context, orgSlug, ownerSlug string) ([]store.PreventRepository, error) {
	query := `
		SELECT r.id, r.organization_id, o.slug, COALESCE(r.owner_slug, ''), r.name, r.provider, COALESCE(r.url, ''),
		       COALESCE(r.external_slug, ''), r.status, COALESCE(r.default_branch, ''), COALESCE(r.sync_status, 'idle'),
		       r.last_synced_at, r.last_sync_started_at, COALESCE(r.last_sync_error, ''), r.created_at
		  FROM repositories r
		  JOIN organizations o ON o.id = r.organization_id`
	args := []any{}
	if orgSlug = strings.TrimSpace(orgSlug); orgSlug != "" {
		query += ` WHERE o.slug = ?`
		args = append(args, orgSlug)
	}
	if ownerSlug = strings.TrimSpace(ownerSlug); ownerSlug != "" {
		if len(args) == 0 {
			query += ` WHERE COALESCE(r.owner_slug, '') = ?`
		} else {
			query += ` AND COALESCE(r.owner_slug, '') = ?`
		}
		args = append(args, ownerSlug)
	}
	query += ` ORDER BY r.created_at ASC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []store.PreventRepository
	for rows.Next() {
		item, err := scanPreventRepository(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PreventStore) GetRepository(ctx context.Context, orgSlug, ownerSlug, repositoryName string) (*store.PreventRepository, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT r.id, r.organization_id, o.slug, COALESCE(r.owner_slug, ''), r.name, r.provider, COALESCE(r.url, ''),
		       COALESCE(r.external_slug, ''), r.status, COALESCE(r.default_branch, ''), COALESCE(r.sync_status, 'idle'),
		       r.last_synced_at, r.last_sync_started_at, COALESCE(r.last_sync_error, ''), r.created_at
		  FROM repositories r
		  JOIN organizations o ON o.id = r.organization_id
		 WHERE o.slug = ? AND COALESCE(r.owner_slug, '') = ? AND r.name = ?`,
		strings.TrimSpace(orgSlug), strings.TrimSpace(ownerSlug), strings.TrimSpace(repositoryName),
	)
	item, err := scanPreventRepository(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *PreventStore) ListRepositoryBranches(ctx context.Context, orgSlug, ownerSlug, repositoryName string) ([]store.PreventRepositoryBranch, error) {
	repo, err := s.repositoryByPath(ctx, orgSlug, ownerSlug, repositoryName)
	if err != nil || repo == nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, repository_id, name, is_default, status, last_synced_at, created_at
		  FROM prevent_repository_branches
		 WHERE repository_id = ?
		 ORDER BY created_at ASC`,
		repo.ID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []store.PreventRepositoryBranch
	for rows.Next() {
		var item store.PreventRepositoryBranch
		var isDefault int
		var lastSyncedAt sql.NullString
		var createdAt sql.NullString
		if err := rows.Scan(&item.ID, &item.RepositoryID, &item.Name, &isDefault, &item.Status, &lastSyncedAt, &createdAt); err != nil {
			return nil, err
		}
		item.IsDefault = isDefault != 0
		item.LastSyncedAt = parseOptionalTime(lastSyncedAt)
		if createdAt.Valid {
			item.DateCreated = parseTime(createdAt.String)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PreventStore) ListRepositoryTokens(ctx context.Context, orgSlug, ownerSlug, repositoryName string) ([]store.PreventRepositoryToken, error) {
	repo, err := s.repositoryByPath(ctx, orgSlug, ownerSlug, repositoryName)
	if err != nil || repo == nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, repository_id, label, token_prefix, token_hash, status, rotated_at, last_used_at, revoked_at, created_at
		  FROM prevent_repository_tokens
		 WHERE repository_id = ?
		 ORDER BY created_at ASC`,
		repo.ID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []store.PreventRepositoryToken
	for rows.Next() {
		var item store.PreventRepositoryToken
		var rotatedAt, lastUsedAt, revokedAt, createdAt sql.NullString
		if err := rows.Scan(&item.ID, &item.RepositoryID, &item.Label, &item.TokenPrefix, &item.TokenHash, &item.Status, &rotatedAt, &lastUsedAt, &revokedAt, &createdAt); err != nil {
			return nil, err
		}
		item.RotatedAt = parseOptionalTime(rotatedAt)
		item.LastUsedAt = parseOptionalTime(lastUsedAt)
		item.RevokedAt = parseOptionalTime(revokedAt)
		if createdAt.Valid {
			item.DateCreated = parseTime(createdAt.String)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PreventStore) RegenerateRepositoryToken(ctx context.Context, orgSlug, ownerSlug, repositoryName, tokenID string) (*store.PreventRepositoryToken, string, error) {
	repo, err := s.repositoryByPath(ctx, orgSlug, ownerSlug, repositoryName)
	if err != nil || repo == nil {
		return nil, "", err
	}

	var current store.PreventRepositoryToken
	var rotatedAt, lastUsedAt, revokedAt, createdAt sql.NullString
	if err := s.db.QueryRowContext(ctx, `
		SELECT id, repository_id, label, token_prefix, token_hash, status, rotated_at, last_used_at, revoked_at, created_at
		  FROM prevent_repository_tokens
		 WHERE repository_id = ? AND id = ?`,
		repo.ID, strings.TrimSpace(tokenID),
	).Scan(&current.ID, &current.RepositoryID, &current.Label, &current.TokenPrefix, &current.TokenHash, &current.Status, &rotatedAt, &lastUsedAt, &revokedAt, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, "", nil
		}
		return nil, "", err
	}

	raw := rawToken("gprevent")
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx, `
		UPDATE prevent_repository_tokens
		   SET token_prefix = ?,
		       token_hash = ?,
		       status = 'active',
		       rotated_at = ?,
		       last_used_at = NULL,
		       revoked_at = NULL
		 WHERE id = ?`,
		tokenPrefix(raw), hashToken(raw), now, current.ID,
	); err != nil {
		return nil, "", err
	}
	current.TokenPrefix = tokenPrefix(raw)
	current.TokenHash = hashToken(raw)
	current.Status = "active"
	rotatedAtTime := parseTime(now)
	current.RotatedAt = &rotatedAtTime
	current.LastUsedAt = nil
	current.RevokedAt = nil
	current.DateCreated = parseTime(createdAt.String)
	return &current, raw, nil
}

func (s *PreventStore) GetRepositorySyncStatus(ctx context.Context, orgSlug, ownerSlug, repositoryName string) (*store.PreventRepositorySyncStatus, error) {
	repo, err := s.repositoryByPath(ctx, orgSlug, ownerSlug, repositoryName)
	if err != nil || repo == nil {
		return nil, err
	}
	return &store.PreventRepositorySyncStatus{
		RepositoryID:      repo.ID,
		Status:            repo.SyncStatus,
		LastSyncedAt:      repo.LastSyncedAt,
		LastSyncStartedAt: repo.LastSyncStartedAt,
		LastSyncError:     repo.LastSyncError,
	}, nil
}

func (s *PreventStore) ListRepositoryTestSuites(ctx context.Context, orgSlug, ownerSlug, repositoryName string) ([]store.PreventRepositoryTestSuite, error) {
	repo, err := s.repositoryByPath(ctx, orgSlug, ownerSlug, repositoryName)
	if err != nil || repo == nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, repository_id, external_suite_id, name, status, last_run_at, created_at
		  FROM prevent_repository_test_suites
		 WHERE repository_id = ?
		 ORDER BY created_at ASC`,
		repo.ID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []store.PreventRepositoryTestSuite
	for rows.Next() {
		var item store.PreventRepositoryTestSuite
		var lastRunAt, createdAt sql.NullString
		if err := rows.Scan(&item.ID, &item.RepositoryID, &item.ExternalID, &item.Name, &item.Status, &lastRunAt, &createdAt); err != nil {
			return nil, err
		}
		item.LastRunAt = parseOptionalTime(lastRunAt)
		if createdAt.Valid {
			item.DateCreated = parseTime(createdAt.String)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PreventStore) ListRepositoryTestResults(ctx context.Context, orgSlug, ownerSlug, repositoryName string) ([]store.PreventRepositoryTestResult, error) {
	repo, err := s.repositoryByPath(ctx, orgSlug, ownerSlug, repositoryName)
	if err != nil || repo == nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, repository_id, suite_id, suite_name, branch_name, commit_sha, status, duration_ms, test_count, failure_count, skipped_count, created_at
		  FROM prevent_repository_test_results
		 WHERE repository_id = ?
		 ORDER BY created_at ASC`,
		repo.ID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []store.PreventRepositoryTestResult
	for rows.Next() {
		var item store.PreventRepositoryTestResult
		var suiteName, commitSHA, createdAt sql.NullString
		if err := rows.Scan(&item.ID, &item.RepositoryID, &item.SuiteID, &suiteName, &item.BranchName, &commitSHA, &item.Status, &item.DurationMS, &item.TestCount, &item.FailureCount, &item.SkippedCount, &createdAt); err != nil {
			return nil, err
		}
		item.SuiteName = suiteName.String
		item.CommitSHA = commitSHA.String
		if createdAt.Valid {
			item.DateCreated = parseTime(createdAt.String)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PreventStore) ListRepositoryTestAggregates(ctx context.Context, orgSlug, ownerSlug, repositoryName string) ([]store.PreventRepositoryTestAggregate, error) {
	repo, err := s.repositoryByPath(ctx, orgSlug, ownerSlug, repositoryName)
	if err != nil || repo == nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, repository_id, branch_name, total_runs, passing_runs, failing_runs, skipped_runs, avg_duration_ms, last_run_at, created_at
		  FROM prevent_repository_test_result_aggregates
		 WHERE repository_id = ?
		 ORDER BY created_at ASC`,
		repo.ID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []store.PreventRepositoryTestAggregate
	for rows.Next() {
		var item store.PreventRepositoryTestAggregate
		var lastRunAt, createdAt sql.NullString
		if err := rows.Scan(&item.ID, &item.RepositoryID, &item.BranchName, &item.TotalRuns, &item.PassingRuns, &item.FailingRuns, &item.SkippedRuns, &item.AvgDurationMS, &lastRunAt, &createdAt); err != nil {
			return nil, err
		}
		item.LastRunAt = parseOptionalTime(lastRunAt)
		if createdAt.Valid {
			item.DateCreated = parseTime(createdAt.String)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PreventStore) repositoryByPath(ctx context.Context, orgSlug, ownerSlug, repositoryName string) (*store.PreventRepository, error) {
	repo, err := s.GetRepository(ctx, orgSlug, ownerSlug, repositoryName)
	if err != nil || repo == nil {
		return repo, err
	}
	return repo, nil
}

func scanPreventRepository(row interface{ Scan(dest ...any) error }) (store.PreventRepository, error) {
	var item store.PreventRepository
	var lastSyncedAt, lastSyncStartedAt, createdAt sql.NullString
	if err := row.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.OrganizationSlug,
		&item.OwnerSlug,
		&item.Name,
		&item.Provider,
		&item.URL,
		&item.ExternalSlug,
		&item.Status,
		&item.DefaultBranch,
		&item.SyncStatus,
		&lastSyncedAt,
		&lastSyncStartedAt,
		&item.LastSyncError,
		&createdAt,
	); err != nil {
		return store.PreventRepository{}, err
	}
	item.LastSyncedAt = parseOptionalTime(lastSyncedAt)
	item.LastSyncStartedAt = parseOptionalTime(lastSyncStartedAt)
	item.DateCreated = parseTime(createdAt.String)
	return item, nil
}
