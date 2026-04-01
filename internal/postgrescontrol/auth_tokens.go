package postgrescontrol

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"urgentry/internal/auth"
)

// CreatePersonalAccessToken creates a PAT for a user and returns the raw token once.
func (s *AuthStore) CreatePersonalAccessToken(ctx context.Context, userID, label string, scopes []string, expiresAt *time.Time, raw string) (string, error) {
	if label == "" {
		label = defaultPATLabel
	}
	if raw == "" {
		raw = rawToken("gpat")
	}
	scopesJSON, err := marshalJSON(scopes)
	if err != nil {
		return "", fmt.Errorf("marshal personal access token scopes: %w", err)
	}
	tokenID := generateID()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO personal_access_tokens
			(id, user_id, label, token_prefix, token_hash, scopes_json, created_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8)`,
		tokenID, userID, label, tokenPrefix(raw), hashToken(raw), string(scopesJSON), time.Now().UTC(), optionalTime(expiresAt),
	); err != nil {
		return "", fmt.Errorf("insert personal access token: %w", err)
	}
	if err := insertAuditLog(ctx, s.db, auditLog{
		CredentialType: string(auth.CredentialPAT),
		CredentialID:   tokenID,
		UserID:         userID,
		Action:         "pat.created",
	}); err != nil {
		return "", err
	}
	return raw, nil
}

// ListPersonalAccessTokens lists a user's redacted PAT metadata.
func (s *AuthStore) ListPersonalAccessTokens(ctx context.Context, userID string) ([]auth.PersonalAccessTokenRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, label, token_prefix, scopes_json::text, created_at, last_used_at, expires_at, revoked_at
		 FROM personal_access_tokens
		 WHERE user_id = $1
		 ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list personal access tokens: %w", err)
	}
	defer rows.Close()

	var tokens []auth.PersonalAccessTokenRecord
	for rows.Next() {
		var (
			record     auth.PersonalAccessTokenRecord
			scopesRaw  string
			createdAt  time.Time
			lastUsedAt sql.NullTime
			expiresAt  sql.NullTime
			revokedAt  sql.NullTime
		)
		if err := rows.Scan(&record.ID, &record.Label, &record.TokenPrefix, &scopesRaw, &createdAt, &lastUsedAt, &expiresAt, &revokedAt); err != nil {
			return nil, fmt.Errorf("scan personal access token: %w", err)
		}
		record.CreatedAt = createdAt.UTC()
		record.LastUsedAt = nullTimePtr(lastUsedAt)
		record.ExpiresAt = nullTimePtr(expiresAt)
		record.RevokedAt = nullTimePtr(revokedAt)
		record.Scopes = scopesSlice(scopesRaw)
		tokens = append(tokens, record)
	}
	return tokens, rows.Err()
}

// RevokePersonalAccessToken revokes a PAT owned by the given user.
func (s *AuthStore) RevokePersonalAccessToken(ctx context.Context, tokenID, userID string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE personal_access_tokens
		 SET revoked_at = $1
		 WHERE id = $2 AND user_id = $3 AND revoked_at IS NULL`,
		time.Now().UTC(), tokenID, userID,
	)
	if err != nil {
		return fmt.Errorf("revoke personal access token: %w", err)
	}
	rows, err := result.RowsAffected()
	if err == nil && rows > 0 {
		return insertAuditLog(ctx, s.db, auditLog{
			CredentialType: string(auth.CredentialPAT),
			CredentialID:   tokenID,
			UserID:         userID,
			Action:         "pat.revoked",
		})
	}
	return nil
}

// CreateAutomationToken creates a project automation token and returns the raw token once.
func (s *AuthStore) CreateAutomationToken(ctx context.Context, projectID, label, createdByUserID string, scopes []string, expiresAt *time.Time, raw string) (string, error) {
	if label == "" {
		label = "Automation Token"
	}
	if raw == "" {
		raw = rawToken("gauto")
	}
	scopesJSON, err := marshalJSON(scopes)
	if err != nil {
		return "", fmt.Errorf("marshal automation token scopes: %w", err)
	}
	tokenID := generateID()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO project_automation_tokens
			(id, project_id, label, token_prefix, token_hash, scopes_json, created_by_user_id, created_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9)`,
		tokenID, projectID, label, tokenPrefix(raw), hashToken(raw), string(scopesJSON),
		nullIfEmpty(createdByUserID), time.Now().UTC(), optionalTime(expiresAt),
	); err != nil {
		return "", fmt.Errorf("insert automation token: %w", err)
	}
	if err := insertAuditLog(ctx, s.db, auditLog{
		CredentialType: string(auth.CredentialAutomationToken),
		CredentialID:   tokenID,
		UserID:         createdByUserID,
		ProjectID:      projectID,
		Action:         "automation_token.created",
	}); err != nil {
		return "", err
	}
	return raw, nil
}

// ListAutomationTokens lists project-scoped automation token metadata.
func (s *AuthStore) ListAutomationTokens(ctx context.Context, projectID string) ([]auth.AutomationTokenRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, label, token_prefix, scopes_json::text, created_by_user_id, created_at, last_used_at, expires_at, revoked_at
		 FROM project_automation_tokens
		 WHERE project_id = $1
		 ORDER BY created_at DESC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list automation tokens: %w", err)
	}
	defer rows.Close()

	var tokens []auth.AutomationTokenRecord
	for rows.Next() {
		var (
			record          auth.AutomationTokenRecord
			scopesRaw       string
			createdByUserID sql.NullString
			createdAt       time.Time
			lastUsedAt      sql.NullTime
			expiresAt       sql.NullTime
			revokedAt       sql.NullTime
		)
		if err := rows.Scan(&record.ID, &record.ProjectID, &record.Label, &record.TokenPrefix, &scopesRaw, &createdByUserID, &createdAt, &lastUsedAt, &expiresAt, &revokedAt); err != nil {
			return nil, fmt.Errorf("scan automation token: %w", err)
		}
		record.CreatedByUserID = nullStr(createdByUserID)
		record.CreatedAt = createdAt.UTC()
		record.LastUsedAt = nullTimePtr(lastUsedAt)
		record.ExpiresAt = nullTimePtr(expiresAt)
		record.RevokedAt = nullTimePtr(revokedAt)
		record.Scopes = scopesSlice(scopesRaw)
		tokens = append(tokens, record)
	}
	return tokens, rows.Err()
}

// RevokeAutomationToken revokes a project-scoped automation token.
func (s *AuthStore) RevokeAutomationToken(ctx context.Context, tokenID, projectID string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE project_automation_tokens
		 SET revoked_at = $1
		 WHERE id = $2 AND project_id = $3 AND revoked_at IS NULL`,
		time.Now().UTC(), tokenID, projectID,
	)
	if err != nil {
		return fmt.Errorf("revoke automation token: %w", err)
	}
	rows, err := result.RowsAffected()
	if err == nil && rows > 0 {
		return insertAuditLog(ctx, s.db, auditLog{
			CredentialType: string(auth.CredentialAutomationToken),
			CredentialID:   tokenID,
			ProjectID:      projectID,
			Action:         "automation_token.revoked",
		})
	}
	return nil
}
