package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"urgentry/internal/auth"
	"urgentry/pkg/id"
)

const (
	defaultPATLabel         = "Bootstrap PAT"
	defaultSessionTokenTTL  = 30 * 24 * time.Hour
	passwordAlgorithmBcrypt = "bcrypt"
)

// BootstrapOptions control first-run access creation.
type BootstrapOptions struct {
	DefaultOrganizationID string
	Email                 string
	DisplayName           string
	Password              string
	PersonalAccessToken   string
}

// BootstrapResult reports created bootstrap credentials.
type BootstrapResult struct {
	Created  bool
	Email    string
	Password string
	PAT      string
}

// AuthStore handles local users, sessions, and bearer tokens.
type AuthStore struct {
	db *sql.DB
}

// NewAuthStore creates a SQLite-backed auth store.
func NewAuthStore(db *sql.DB) *AuthStore {
	return &AuthStore{db: db}
}

// EnsureBootstrapAccess creates the first local owner and PAT when the user table is empty.
func (s *AuthStore) EnsureBootstrapAccess(ctx context.Context, opts BootstrapOptions) (*BootstrapResult, error) {
	if opts.DefaultOrganizationID == "" {
		opts.DefaultOrganizationID = "default-org"
	}

	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return nil, fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return &BootstrapResult{}, nil
	}

	if opts.Email == "" {
		opts.Email = "admin@urgentry.local"
	}
	if opts.DisplayName == "" {
		opts.DisplayName = "Bootstrap Admin"
	}
	if opts.Password == "" {
		opts.Password = "urgentry-" + id.New()[:20]
	}
	if opts.PersonalAccessToken == "" {
		opts.PersonalAccessToken = rawToken("gpat")
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(opts.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash bootstrap password: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin bootstrap tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO organizations (id, slug, name) VALUES (?, 'urgentry-org', 'Urgentry')`,
		opts.DefaultOrganizationID,
	); err != nil {
		return nil, fmt.Errorf("ensure bootstrap org: %w", err)
	}

	userID := generateID()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (id, email, display_name, is_active, created_at, updated_at)
		 VALUES (?, ?, ?, 1, ?, ?)`,
		userID, strings.ToLower(strings.TrimSpace(opts.Email)), opts.DisplayName, now, now,
	); err != nil {
		return nil, fmt.Errorf("insert bootstrap user: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO user_password_credentials (user_id, password_hash, password_algo, password_updated_at)
		 VALUES (?, ?, ?, ?)`,
		userID, string(passwordHash), passwordAlgorithmBcrypt, now,
	); err != nil {
		return nil, fmt.Errorf("insert bootstrap password: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO organization_members (id, organization_id, user_id, role, created_at)
		 VALUES (?, ?, ?, 'owner', ?)`,
		generateID(), opts.DefaultOrganizationID, userID, now,
	); err != nil {
		return nil, fmt.Errorf("insert bootstrap membership: %w", err)
	}

	tokenHash := hashToken(opts.PersonalAccessToken)
	tokenPrefix := tokenPrefix(opts.PersonalAccessToken)
	scopesJSON, err := marshalJSON([]string{auth.ScopeOrgAdmin})
	if err != nil {
		return nil, fmt.Errorf("marshal bootstrap pat scopes: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO personal_access_tokens (id, user_id, label, token_prefix, token_hash, scopes_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		generateID(), userID, defaultPATLabel, tokenPrefix, tokenHash,
		string(scopesJSON), now,
	); err != nil {
		return nil, fmt.Errorf("insert bootstrap pat: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO auth_audit_logs (id, credential_type, credential_id, user_id, organization_id, action, created_at)
		 VALUES (?, 'bootstrap', NULL, ?, ?, 'bootstrap.created', ?)`,
		generateID(), userID, opts.DefaultOrganizationID, now,
	); err != nil {
		return nil, fmt.Errorf("insert bootstrap audit: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit bootstrap tx: %w", err)
	}

	return &BootstrapResult{
		Created:  true,
		Email:    opts.Email,
		Password: opts.Password,
		PAT:      opts.PersonalAccessToken,
	}, nil
}

// AuthenticateUserPassword validates local credentials.
func (s *AuthStore) AuthenticateUserPassword(ctx context.Context, email, password string) (*auth.User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT u.id, u.email, u.display_name, c.password_hash
		 FROM users u
		 JOIN user_password_credentials c ON c.user_id = u.id
		 WHERE lower(u.email) = lower(?) AND u.is_active = 1`,
		strings.TrimSpace(email),
	)

	var user auth.User
	var passwordHash string
	if err := row.Scan(&user.ID, &user.Email, &user.DisplayName, &passwordHash); err != nil {
		if err == sql.ErrNoRows {
			return nil, auth.ErrInvalidCredentials
		}
		return nil, fmt.Errorf("lookup user credentials: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return nil, auth.ErrInvalidCredentials
	}
	return &user, nil
}

// CreateSession creates a new opaque user session.
func (s *AuthStore) CreateSession(ctx context.Context, userID, userAgent, ipAddress string, ttl time.Duration) (string, *auth.Principal, error) {
	if ttl <= 0 {
		ttl = defaultSessionTokenTTL
	}

	row := s.db.QueryRowContext(ctx,
		`SELECT id, email, display_name FROM users WHERE id = ? AND is_active = 1`,
		userID,
	)
	var user auth.User
	if err := row.Scan(&user.ID, &user.Email, &user.DisplayName); err != nil {
		if err == sql.ErrNoRows {
			return "", nil, auth.ErrInvalidCredentials
		}
		return "", nil, fmt.Errorf("lookup user for session: %w", err)
	}

	sessionID := generateID()
	raw := rawToken("gsess")
	csrf := rawToken("csrf")
	now := time.Now().UTC()
	expiresAt := now.Add(ttl)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO user_sessions
			(id, user_id, session_token_hash, csrf_secret, ip_address, user_agent, created_at, last_seen_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, user.ID, hashToken(raw), csrf, nullable(ipAddress), nullable(userAgent),
		now.Format(time.RFC3339), now.Format(time.RFC3339), expiresAt.Format(time.RFC3339),
	); err != nil {
		return "", nil, fmt.Errorf("insert session: %w", err)
	}
	if err := s.insertAuditLog(ctx, auditLog{
		CredentialType: string(auth.CredentialSession),
		CredentialID:   sessionID,
		UserID:         user.ID,
		Action:         "session.created",
		IPAddress:      ipAddress,
		UserAgent:      userAgent,
	}); err != nil {
		return "", nil, err
	}

	return raw, &auth.Principal{
		Kind:         auth.CredentialSession,
		CredentialID: sessionID,
		User:         &user,
		CSRFToken:    csrf,
	}, nil
}

// AuthenticateSession validates an opaque session token cookie.
func (s *AuthStore) AuthenticateSession(ctx context.Context, rawToken string) (*auth.Principal, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT s.id, s.user_id, s.csrf_secret, s.expires_at, u.email, u.display_name
		 FROM user_sessions s
		 JOIN users u ON u.id = s.user_id
		 WHERE s.session_token_hash = ? AND s.revoked_at IS NULL AND u.is_active = 1`,
		hashToken(rawToken),
	)

	var principal auth.Principal
	var user auth.User
	var expiresAt string
	if err := row.Scan(&principal.CredentialID, &user.ID, &principal.CSRFToken, &expiresAt, &user.Email, &user.DisplayName); err != nil {
		if err == sql.ErrNoRows {
			return nil, auth.ErrInvalidCredentials
		}
		return nil, fmt.Errorf("lookup session: %w", err)
	}
	if expires := parseTime(expiresAt); !expires.IsZero() && time.Now().UTC().After(expires) {
		return nil, auth.ErrExpiredCredentials
	}
	_, _ = s.db.ExecContext(ctx,
		`UPDATE user_sessions SET last_seen_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), principal.CredentialID,
	)

	principal.Kind = auth.CredentialSession
	principal.User = &user
	return &principal, nil
}

// RevokeSession revokes a stored session by ID.
func (s *AuthStore) RevokeSession(ctx context.Context, sessionID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx,
		`UPDATE user_sessions SET revoked_at = ? WHERE id = ?`,
		now, sessionID,
	); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return s.insertAuditLog(ctx, auditLog{
		CredentialType: string(auth.CredentialSession),
		CredentialID:   sessionID,
		Action:         "session.revoked",
	})
}

// AuthenticatePAT validates a personal access token.
func (s *AuthStore) AuthenticatePAT(ctx context.Context, rawToken string) (*auth.Principal, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT p.id, p.user_id, p.scopes_json, p.expires_at, u.email, u.display_name
		 FROM personal_access_tokens p
		 JOIN users u ON u.id = p.user_id
		 WHERE p.token_hash = ? AND p.revoked_at IS NULL AND u.is_active = 1`,
		hashToken(rawToken),
	)
	return s.scanUserTokenPrincipal(ctx, row, auth.CredentialPAT, rawToken, "personal_access_tokens")
}

// AuthenticateAutomationToken validates a project automation token.
func (s *AuthStore) AuthenticateAutomationToken(ctx context.Context, rawToken string) (*auth.Principal, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT t.id, t.project_id, t.scopes_json, t.expires_at
		 FROM project_automation_tokens t
		 WHERE t.token_hash = ? AND t.revoked_at IS NULL`,
		hashToken(rawToken),
	)

	var principal auth.Principal
	var scopesJSON string
	var expiresAt sql.NullString
	if err := row.Scan(&principal.CredentialID, &principal.ProjectID, &scopesJSON, &expiresAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, auth.ErrInvalidCredentials
		}
		return nil, fmt.Errorf("lookup automation token: %w", err)
	}
	if expiresAt.Valid && time.Now().UTC().After(parseTime(expiresAt.String)) {
		return nil, auth.ErrExpiredCredentials
	}
	scopes, err := parseScopes(scopesJSON)
	if err != nil {
		return nil, fmt.Errorf("parse automation token scopes: %w", err)
	}
	principal.Kind = auth.CredentialAutomationToken
	principal.Scopes = scopes
	_, _ = s.db.ExecContext(ctx,
		`UPDATE project_automation_tokens SET last_used_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), principal.CredentialID,
	)
	return &principal, nil
}

// CreatePersonalAccessToken creates a PAT for a user and returns the raw token once.
func (s *AuthStore) CreatePersonalAccessToken(ctx context.Context, userID, label string, scopes []string, expiresAt *time.Time, raw string) (string, error) {
	if label == "" {
		label = defaultPATLabel
	}
	if raw == "" {
		raw = rawToken("gpat")
	}
	tokenID := generateID()
	now := time.Now().UTC()
	var expiresValue any
	if expiresAt != nil && !expiresAt.IsZero() {
		expiresValue = expiresAt.UTC().Format(time.RFC3339)
	}
	scopesJSON, err := marshalJSON(scopes)
	if err != nil {
		return "", fmt.Errorf("marshal personal access token scopes: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO personal_access_tokens
			(id, user_id, label, token_prefix, token_hash, scopes_json, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		tokenID, userID, label, tokenPrefix(raw), hashToken(raw), string(scopesJSON),
		now.Format(time.RFC3339), expiresValue,
	)
	if err != nil {
		return "", fmt.Errorf("insert personal access token: %w", err)
	}
	if err := s.insertAuditLog(ctx, auditLog{
		CredentialType: string(auth.CredentialPAT),
		CredentialID:   tokenID,
		UserID:         userID,
		Action:         "pat.created",
	}); err != nil {
		return "", err
	}
	return raw, nil
}

// CreateAutomationToken creates a project automation token and returns the raw token once.
func (s *AuthStore) CreateAutomationToken(ctx context.Context, projectID, label, createdByUserID string, scopes []string, expiresAt *time.Time, raw string) (string, error) {
	if label == "" {
		label = "Automation Token"
	}
	if raw == "" {
		raw = rawToken("gauto")
	}
	tokenID := generateID()
	now := time.Now().UTC()
	var expiresValue any
	if expiresAt != nil && !expiresAt.IsZero() {
		expiresValue = expiresAt.UTC().Format(time.RFC3339)
	}
	scopesJSON, err := marshalJSON(scopes)
	if err != nil {
		return "", fmt.Errorf("marshal automation token scopes: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO project_automation_tokens
			(id, project_id, label, token_prefix, token_hash, scopes_json, created_by_user_id, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tokenID, projectID, label, tokenPrefix(raw), hashToken(raw), string(scopesJSON),
		nullIfEmpty(createdByUserID), now.Format(time.RFC3339), expiresValue,
	)
	if err != nil {
		return "", fmt.Errorf("insert automation token: %w", err)
	}
	if err := s.insertAuditLog(ctx, auditLog{
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

// ListPersonalAccessTokens lists a user's redacted PAT metadata.
func (s *AuthStore) ListPersonalAccessTokens(ctx context.Context, userID string) ([]auth.PersonalAccessTokenRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, label, token_prefix, scopes_json, created_at, last_used_at, expires_at, revoked_at
		 FROM personal_access_tokens
		 WHERE user_id = ?
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
			scopesJSON string
			createdAt  string
			lastUsedAt sql.NullString
			expiresAt  sql.NullString
			revokedAt  sql.NullString
		)
		if err := rows.Scan(&record.ID, &record.Label, &record.TokenPrefix, &scopesJSON, &createdAt, &lastUsedAt, &expiresAt, &revokedAt); err != nil {
			return nil, fmt.Errorf("scan personal access token: %w", err)
		}
		record.CreatedAt = parseTime(createdAt)
		record.LastUsedAt = parseOptionalTime(lastUsedAt)
		record.ExpiresAt = parseOptionalTime(expiresAt)
		record.RevokedAt = parseOptionalTime(revokedAt)
		record.Scopes = scopesSlice(scopesJSON)
		tokens = append(tokens, record)
	}
	return tokens, rows.Err()
}

// RevokePersonalAccessToken revokes a PAT owned by the given user.
func (s *AuthStore) RevokePersonalAccessToken(ctx context.Context, tokenID, userID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx,
		`UPDATE personal_access_tokens
		 SET revoked_at = ?
		 WHERE id = ? AND user_id = ? AND revoked_at IS NULL`,
		now, tokenID, userID,
	)
	if err != nil {
		return fmt.Errorf("revoke personal access token: %w", err)
	}
	rows, err := result.RowsAffected()
	if err == nil && rows > 0 {
		return s.insertAuditLog(ctx, auditLog{
			CredentialType: string(auth.CredentialPAT),
			CredentialID:   tokenID,
			UserID:         userID,
			Action:         "pat.revoked",
		})
	}
	return nil
}

// ListAutomationTokens lists project-scoped automation token metadata.
func (s *AuthStore) ListAutomationTokens(ctx context.Context, projectID string) ([]auth.AutomationTokenRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, label, token_prefix, scopes_json, created_by_user_id, created_at, last_used_at, expires_at, revoked_at
		 FROM project_automation_tokens
		 WHERE project_id = ?
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
			scopesJSON      string
			createdAt       string
			createdByUserID sql.NullString
			lastUsedAt      sql.NullString
			expiresAt       sql.NullString
			revokedAt       sql.NullString
		)
		if err := rows.Scan(&record.ID, &record.ProjectID, &record.Label, &record.TokenPrefix, &scopesJSON, &createdByUserID, &createdAt, &lastUsedAt, &expiresAt, &revokedAt); err != nil {
			return nil, fmt.Errorf("scan automation token: %w", err)
		}
		record.CreatedByUserID = nullStr(createdByUserID)
		record.CreatedAt = parseTime(createdAt)
		record.LastUsedAt = parseOptionalTime(lastUsedAt)
		record.ExpiresAt = parseOptionalTime(expiresAt)
		record.RevokedAt = parseOptionalTime(revokedAt)
		record.Scopes = scopesSlice(scopesJSON)
		tokens = append(tokens, record)
	}
	return tokens, rows.Err()
}

// RevokeAutomationToken revokes a project-scoped automation token.
func (s *AuthStore) RevokeAutomationToken(ctx context.Context, tokenID, projectID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx,
		`UPDATE project_automation_tokens
		 SET revoked_at = ?
		 WHERE id = ? AND project_id = ? AND revoked_at IS NULL`,
		now, tokenID, projectID,
	)
	if err != nil {
		return fmt.Errorf("revoke automation token: %w", err)
	}
	rows, err := result.RowsAffected()
	if err == nil && rows > 0 {
		return s.insertAuditLog(ctx, auditLog{
			CredentialType: string(auth.CredentialAutomationToken),
			CredentialID:   tokenID,
			ProjectID:      projectID,
			Action:         "automation_token.revoked",
		})
	}
	return nil
}

// ResolveOrganizationBySlug resolves an organization resource.
func (s *AuthStore) ResolveOrganizationBySlug(ctx context.Context, slug string) (*auth.Organization, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, slug FROM organizations WHERE slug = ?`,
		slug,
	)
	var org auth.Organization
	if err := row.Scan(&org.ID, &org.Slug); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("resolve organization: %w", err)
	}
	return &org, nil
}

// ResolveProjectByID resolves a project resource by ID.
func (s *AuthStore) ResolveProjectByID(ctx context.Context, projectID string) (*auth.Project, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT p.id, p.slug, o.id, o.slug
		 FROM projects p
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE p.id = ?`,
		projectID,
	)
	return scanResolvedProject(row)
}

// ResolveProjectBySlug resolves a project resource by org/project slugs.
func (s *AuthStore) ResolveProjectBySlug(ctx context.Context, orgSlug, projectSlug string) (*auth.Project, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT p.id, p.slug, o.id, o.slug
		 FROM projects p
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE o.slug = ? AND p.slug = ?`,
		orgSlug, projectSlug,
	)
	return scanResolvedProject(row)
}

// ResolveIssueProject resolves the owning project for an issue/group.
func (s *AuthStore) ResolveIssueProject(ctx context.Context, issueID string) (*auth.Project, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT p.id, p.slug, o.id, o.slug
		 FROM groups g
		 JOIN projects p ON p.id = g.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE g.id = ?`,
		issueID,
	)
	return scanResolvedProject(row)
}

// ResolveEventProject resolves the owning project for an event.
func (s *AuthStore) ResolveEventProject(ctx context.Context, eventID string) (*auth.Project, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT p.id, p.slug, o.id, o.slug
		 FROM events e
		 JOIN projects p ON p.id = e.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE e.event_id = ?`,
		eventID,
	)
	return scanResolvedProject(row)
}

// LookupUserOrgRole returns the member role for a user in an organization.
func (s *AuthStore) LookupUserOrgRole(ctx context.Context, userID, organizationID string) (string, error) {
	var role string
	err := s.db.QueryRowContext(ctx,
		`SELECT role FROM organization_members WHERE user_id = ? AND organization_id = ?`,
		userID, organizationID,
	).Scan(&role)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("lookup organization role: %w", err)
	}
	return role, nil
}

// ListUserOrgRoles returns all organization memberships for a user.
func (s *AuthStore) ListUserOrgRoles(ctx context.Context, userID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT organization_id, role FROM organization_members WHERE user_id = ?`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list user org roles: %w", err)
	}
	defer rows.Close()

	roles := map[string]string{}
	for rows.Next() {
		var orgID, role string
		if err := rows.Scan(&orgID, &role); err != nil {
			return nil, fmt.Errorf("scan user org role: %w", err)
		}
		roles[orgID] = role
	}
	return roles, rows.Err()
}

// LookupUserProjectRole returns the project-level role for a user, or "" if none.
func (s *AuthStore) LookupUserProjectRole(ctx context.Context, userID, projectID string) (string, error) {
	var role string
	err := s.db.QueryRowContext(ctx,
		`SELECT role FROM project_memberships WHERE user_id = ? AND project_id = ?`,
		userID, projectID,
	).Scan(&role)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("lookup project role: %w", err)
	}
	return role, nil
}

// TouchProjectKey records usage of a project key.
func (s *AuthStore) TouchProjectKey(ctx context.Context, publicKey string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE project_keys SET last_used_at = ? WHERE public_key = ?`,
		time.Now().UTC().Format(time.RFC3339), publicKey,
	)
	if err != nil {
		return fmt.Errorf("touch project key: %w", err)
	}
	return nil
}

type auditLog struct {
	CredentialType string
	CredentialID   string
	UserID         string
	ProjectID      string
	OrganizationID string
	Action         string
	RequestPath    string
	RequestMethod  string
	IPAddress      string
	UserAgent      string
}

func (s *AuthStore) insertAuditLog(ctx context.Context, entry auditLog) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO auth_audit_logs
			(id, credential_type, credential_id, user_id, project_id, organization_id, action, request_path, request_method, ip_address, user_agent, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		generateID(), entry.CredentialType, nullIfEmpty(entry.CredentialID), nullIfEmpty(entry.UserID),
		nullIfEmpty(entry.ProjectID), nullIfEmpty(entry.OrganizationID), entry.Action,
		nullIfEmpty(entry.RequestPath), nullIfEmpty(entry.RequestMethod), nullIfEmpty(entry.IPAddress),
		nullIfEmpty(entry.UserAgent), time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}
	return nil
}

func (s *AuthStore) scanUserTokenPrincipal(ctx context.Context, row *sql.Row, kind auth.CredentialKind, rawToken string, table string) (*auth.Principal, error) {
	var principal auth.Principal
	var user auth.User
	var scopesJSON string
	var expiresAt sql.NullString
	if err := row.Scan(&principal.CredentialID, &user.ID, &scopesJSON, &expiresAt, &user.Email, &user.DisplayName); err != nil {
		if err == sql.ErrNoRows {
			return nil, auth.ErrInvalidCredentials
		}
		return nil, fmt.Errorf("lookup bearer token: %w", err)
	}
	if expiresAt.Valid && time.Now().UTC().After(parseTime(expiresAt.String)) {
		return nil, auth.ErrExpiredCredentials
	}
	scopes, err := parseScopes(scopesJSON)
	if err != nil {
		return nil, fmt.Errorf("parse bearer token scopes: %w", err)
	}
	principal.Kind = kind
	principal.User = &user
	principal.Scopes = scopes
	_, _ = s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET last_used_at = ? WHERE id = ?`, table),
		time.Now().UTC().Format(time.RFC3339), principal.CredentialID,
	)
	_ = rawToken
	return &principal, nil
}

func scanResolvedProject(row *sql.Row) (*auth.Project, error) {
	var project auth.Project
	if err := row.Scan(&project.ID, &project.Slug, &project.OrganizationID, &project.OrganizationSlug); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &project, nil
}

func parseScopes(raw string) (map[string]struct{}, error) {
	scopes := []string{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &scopes); err != nil {
			return nil, err
		}
	}
	scopeSet := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		scopeSet[scope] = struct{}{}
	}
	return scopeSet, nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func tokenPrefix(raw string) string {
	if idx := strings.IndexByte(raw, '_'); idx >= 0 {
		rest := raw[idx+1:]
		if next := strings.IndexByte(rest, '_'); next >= 0 {
			return raw[:idx+1+next]
		}
	}
	if len(raw) <= 18 {
		return raw
	}
	return raw[:18]
}

func rawToken(prefix string) string {
	return prefix + "_" + id.New()[:12] + "_" + id.New() + id.New()
}

func marshalJSON(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func nullable(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullIfEmpty(value string) any {
	return nullable(value)
}

func parseOptionalTime(value sql.NullString) *time.Time {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	parsed := parseTime(value.String)
	if parsed.IsZero() {
		return nil
	}
	return &parsed
}

func scopesSlice(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var scopes []string
	if err := json.Unmarshal([]byte(raw), &scopes); err != nil {
		return nil
	}
	return scopes
}
