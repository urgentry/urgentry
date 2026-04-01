package postgrescontrol

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"urgentry/internal/auth"
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

type auditLogger interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// AuthStore provides PostgreSQL-backed auth and token storage.
type AuthStore struct {
	db *sql.DB
}

var (
	_ auth.Store        = (*AuthStore)(nil)
	_ auth.TokenManager = (*AuthStore)(nil)
	_ auth.KeyStore     = (*AuthStore)(nil)
	_ auth.KeyToucher   = (*AuthStore)(nil)
)

// NewAuthStore creates a PostgreSQL-backed auth store.
func NewAuthStore(db *sql.DB) *AuthStore {
	return &AuthStore{db: db}
}

// EnsureBootstrapAccess creates the first local owner and PAT when the user table is empty.
func (s *AuthStore) EnsureBootstrapAccess(ctx context.Context, opts BootstrapOptions) (*BootstrapResult, error) {
	if opts.DefaultOrganizationID == "" {
		opts.DefaultOrganizationID = "default-org"
	}
	if opts.Email == "" {
		opts.Email = "admin@urgentry.local"
	}
	if opts.DisplayName == "" {
		opts.DisplayName = "Bootstrap Admin"
	}
	if opts.Password == "" {
		opts.Password = "urgentry-" + generateID()[:20]
	}
	if opts.PersonalAccessToken == "" {
		opts.PersonalAccessToken = rawToken("gpat")
	}

	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return nil, fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return &BootstrapResult{}, nil
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
		`INSERT INTO organizations (id, slug, name)
		 VALUES ($1, 'urgentry-org', 'Urgentry')
		 ON CONFLICT (id) DO NOTHING`,
		opts.DefaultOrganizationID,
	); err != nil {
		return nil, fmt.Errorf("ensure bootstrap org: %w", err)
	}

	now := time.Now().UTC()
	userID := generateID()
	email := strings.ToLower(strings.TrimSpace(opts.Email))
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (id, email, display_name, is_active, created_at, updated_at)
		 VALUES ($1, $2, $3, TRUE, $4, $4)`,
		userID, email, opts.DisplayName, now,
	); err != nil {
		return nil, fmt.Errorf("insert bootstrap user: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO user_password_credentials (user_id, password_hash, password_algo, password_updated_at)
		 VALUES ($1, $2, $3, $4)`,
		userID, string(passwordHash), passwordAlgorithmBcrypt, now,
	); err != nil {
		return nil, fmt.Errorf("insert bootstrap password: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO organization_members (id, organization_id, user_id, role, created_at)
		 VALUES ($1, $2, $3, 'owner', $4)`,
		generateID(), opts.DefaultOrganizationID, userID, now,
	); err != nil {
		return nil, fmt.Errorf("insert bootstrap membership: %w", err)
	}

	scopesJSON, err := marshalJSON([]string{auth.ScopeOrgAdmin})
	if err != nil {
		return nil, fmt.Errorf("marshal bootstrap pat scopes: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO personal_access_tokens
			(id, user_id, label, token_prefix, token_hash, scopes_json, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7)`,
		generateID(), userID, defaultPATLabel, tokenPrefix(opts.PersonalAccessToken),
		hashToken(opts.PersonalAccessToken), string(scopesJSON), now,
	); err != nil {
		return nil, fmt.Errorf("insert bootstrap pat: %w", err)
	}
	if err := insertAuditLog(ctx, tx, auditLog{
		CredentialType: "bootstrap",
		UserID:         userID,
		OrganizationID: opts.DefaultOrganizationID,
		Action:         "bootstrap.created",
	}); err != nil {
		return nil, err
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

// RotateBootstrapAccess replaces the bootstrap password and PAT for an existing bootstrap user.
func (s *AuthStore) RotateBootstrapAccess(ctx context.Context, email, password, rawPAT string) (*BootstrapResult, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, fmt.Errorf("bootstrap email is required")
	}
	if strings.TrimSpace(password) == "" {
		return nil, fmt.Errorf("bootstrap password is required")
	}
	if strings.TrimSpace(rawPAT) == "" {
		rawPAT = rawToken("gpat")
	}

	var userID string
	if err := s.db.QueryRowContext(ctx,
		`SELECT id FROM users WHERE lower(email) = lower($1) AND is_active = TRUE LIMIT 1`,
		email,
	).Scan(&userID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("bootstrap user %q not found", email)
		}
		return nil, fmt.Errorf("lookup bootstrap user: %w", err)
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash bootstrap password: %w", err)
	}
	scopesJSON, err := marshalJSON([]string{auth.ScopeOrgAdmin})
	if err != nil {
		return nil, fmt.Errorf("marshal bootstrap pat scopes: %w", err)
	}

	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin bootstrap rotation tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO user_password_credentials (user_id, password_hash, password_algo, password_updated_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (user_id) DO UPDATE SET
		     password_hash = EXCLUDED.password_hash,
		     password_algo = EXCLUDED.password_algo,
		     password_updated_at = EXCLUDED.password_updated_at`,
		userID, string(passwordHash), passwordAlgorithmBcrypt, now,
	); err != nil {
		return nil, fmt.Errorf("upsert bootstrap password: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE personal_access_tokens
		 SET revoked_at = $1
		 WHERE user_id = $2 AND label = $3 AND revoked_at IS NULL`,
		now, userID, defaultPATLabel,
	); err != nil {
		return nil, fmt.Errorf("revoke previous bootstrap pats: %w", err)
	}

	patID := generateID()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO personal_access_tokens
			(id, user_id, label, token_prefix, token_hash, scopes_json, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7)`,
		patID, userID, defaultPATLabel, tokenPrefix(rawPAT), hashToken(rawPAT), string(scopesJSON), now,
	); err != nil {
		return nil, fmt.Errorf("insert rotated bootstrap pat: %w", err)
	}

	if err := insertAuditLog(ctx, tx, auditLog{
		CredentialType: "bootstrap",
		CredentialID:   patID,
		UserID:         userID,
		Action:         "bootstrap.rotated",
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit bootstrap rotation tx: %w", err)
	}

	return &BootstrapResult{
		Email:    email,
		Password: password,
		PAT:      rawPAT,
	}, nil
}

// EnsureDefaultKey creates a default project key if none exists and returns a public key.
func EnsureDefaultKey(ctx context.Context, db *sql.DB) (string, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_keys`).Scan(&count); err != nil {
		return "", fmt.Errorf("count project keys: %w", err)
	}
	if count > 0 {
		var publicKey string
		if err := db.QueryRowContext(ctx, `SELECT public_key FROM project_keys ORDER BY created_at ASC LIMIT 1`).Scan(&publicKey); err != nil {
			return "", fmt.Errorf("load existing project key: %w", err)
		}
		return publicKey, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin default key tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO organizations (id, slug, name)
		 VALUES ('default-org', 'urgentry-org', 'Urgentry')
		 ON CONFLICT (id) DO NOTHING`,
	); err != nil {
		return "", fmt.Errorf("ensure default org: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO projects (id, organization_id, slug, name, platform, status)
		 VALUES ('default-project', 'default-org', 'default', 'Default Project', 'go', 'active')
		 ON CONFLICT (id) DO NOTHING`,
	); err != nil {
		return "", fmt.Errorf("ensure default project: %w", err)
	}

	publicKey := generateID()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO project_keys (id, project_id, public_key, status, label)
		 VALUES ($1, 'default-project', $2, 'active', 'Default Key')`,
		generateID(), publicKey,
	); err != nil {
		return "", fmt.Errorf("insert default project key: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit default key tx: %w", err)
	}
	return publicKey, nil
}

// AuthenticateUserPassword validates local credentials.
func (s *AuthStore) AuthenticateUserPassword(ctx context.Context, email, password string) (*auth.User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT u.id, u.email, u.display_name, c.password_hash
		 FROM users u
		 JOIN user_password_credentials c ON c.user_id = u.id
		 WHERE lower(u.email) = lower($1) AND u.is_active = TRUE`,
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
		`SELECT id, email, display_name FROM users WHERE id = $1 AND is_active = TRUE`,
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
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO user_sessions
			(id, user_id, session_token_hash, csrf_secret, ip_address, user_agent, created_at, last_seen_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $8)`,
		sessionID, user.ID, hashToken(raw), csrf, nullIfEmpty(ipAddress), nullIfEmpty(userAgent), now, now.Add(ttl),
	); err != nil {
		return "", nil, fmt.Errorf("insert session: %w", err)
	}
	if err := insertAuditLog(ctx, s.db, auditLog{
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
func (s *AuthStore) AuthenticateSession(ctx context.Context, raw string) (*auth.Principal, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT s.id, s.user_id, s.csrf_secret, s.expires_at, u.email, u.display_name
		 FROM user_sessions s
		 JOIN users u ON u.id = s.user_id
		 WHERE s.session_token_hash = $1 AND s.revoked_at IS NULL AND u.is_active = TRUE`,
		hashToken(raw),
	)

	var (
		principal auth.Principal
		user      auth.User
		expiresAt time.Time
	)
	if err := row.Scan(&principal.CredentialID, &user.ID, &principal.CSRFToken, &expiresAt, &user.Email, &user.DisplayName); err != nil {
		if err == sql.ErrNoRows {
			return nil, auth.ErrInvalidCredentials
		}
		return nil, fmt.Errorf("lookup session: %w", err)
	}
	if !expiresAt.IsZero() && time.Now().UTC().After(expiresAt.UTC()) {
		return nil, auth.ErrExpiredCredentials
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE user_sessions SET last_seen_at = $1 WHERE id = $2`,
		time.Now().UTC(), principal.CredentialID,
	); err != nil {
		return nil, fmt.Errorf("update session last_seen_at: %w", err)
	}

	principal.Kind = auth.CredentialSession
	principal.User = &user
	return &principal, nil
}

// RevokeSession revokes a stored session by ID.
func (s *AuthStore) RevokeSession(ctx context.Context, sessionID string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE user_sessions SET revoked_at = $1 WHERE id = $2`,
		time.Now().UTC(), sessionID,
	); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return insertAuditLog(ctx, s.db, auditLog{
		CredentialType: string(auth.CredentialSession),
		CredentialID:   sessionID,
		Action:         "session.revoked",
	})
}

// AuthenticatePAT validates a personal access token.
func (s *AuthStore) AuthenticatePAT(ctx context.Context, raw string) (*auth.Principal, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT p.id, p.user_id, p.scopes_json::text, p.expires_at, u.email, u.display_name
		 FROM personal_access_tokens p
		 JOIN users u ON u.id = p.user_id
		 WHERE p.token_hash = $1 AND p.revoked_at IS NULL AND u.is_active = TRUE`,
		hashToken(raw),
	)
	return s.scanUserTokenPrincipal(ctx, row, auth.CredentialPAT, "personal_access_tokens")
}

// AuthenticateAutomationToken validates a project automation token.
func (s *AuthStore) AuthenticateAutomationToken(ctx context.Context, raw string) (*auth.Principal, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, scopes_json::text, expires_at
		 FROM project_automation_tokens
		 WHERE token_hash = $1 AND revoked_at IS NULL`,
		hashToken(raw),
	)

	var (
		principal auth.Principal
		scopesRaw string
		expiresAt sql.NullTime
	)
	if err := row.Scan(&principal.CredentialID, &principal.ProjectID, &scopesRaw, &expiresAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, auth.ErrInvalidCredentials
		}
		return nil, fmt.Errorf("lookup automation token: %w", err)
	}
	if expiresAt.Valid && time.Now().UTC().After(expiresAt.Time.UTC()) {
		return nil, auth.ErrExpiredCredentials
	}
	scopes, err := parseScopes(scopesRaw)
	if err != nil {
		return nil, fmt.Errorf("parse automation token scopes: %w", err)
	}
	principal.Kind = auth.CredentialAutomationToken
	principal.Scopes = scopes
	if _, err := s.db.ExecContext(ctx,
		`UPDATE project_automation_tokens SET last_used_at = $1 WHERE id = $2`,
		time.Now().UTC(), principal.CredentialID,
	); err != nil {
		return nil, fmt.Errorf("update automation token last_used_at: %w", err)
	}
	return &principal, nil
}

func (s *AuthStore) scanUserTokenPrincipal(ctx context.Context, row *sql.Row, kind auth.CredentialKind, table string) (*auth.Principal, error) {
	var (
		principal auth.Principal
		user      auth.User
		scopesRaw string
		expiresAt sql.NullTime
	)
	if err := row.Scan(&principal.CredentialID, &user.ID, &scopesRaw, &expiresAt, &user.Email, &user.DisplayName); err != nil {
		if err == sql.ErrNoRows {
			return nil, auth.ErrInvalidCredentials
		}
		return nil, fmt.Errorf("lookup bearer token: %w", err)
	}
	if expiresAt.Valid && time.Now().UTC().After(expiresAt.Time.UTC()) {
		return nil, auth.ErrExpiredCredentials
	}
	scopes, err := parseScopes(scopesRaw)
	if err != nil {
		return nil, fmt.Errorf("parse bearer token scopes: %w", err)
	}
	principal.Kind = kind
	principal.User = &user
	principal.Scopes = scopes
	if _, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET last_used_at = $1 WHERE id = $2`, table),
		time.Now().UTC(), principal.CredentialID,
	); err != nil {
		return nil, fmt.Errorf("update %s last_used_at: %w", table, err)
	}
	return &principal, nil
}

func insertAuditLog(ctx context.Context, db auditLogger, entry auditLog) error {
	if _, err := db.ExecContext(ctx,
		`INSERT INTO auth_audit_logs
			(id, credential_type, credential_id, user_id, project_id, organization_id, action, request_path, request_method, ip_address, user_agent, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		generateID(), nullIfEmpty(entry.CredentialType), nullIfEmpty(entry.CredentialID), nullIfEmpty(entry.UserID),
		nullIfEmpty(entry.ProjectID), nullIfEmpty(entry.OrganizationID), entry.Action,
		nullIfEmpty(entry.RequestPath), nullIfEmpty(entry.RequestMethod), nullIfEmpty(entry.IPAddress),
		nullIfEmpty(entry.UserAgent), time.Now().UTC(),
	); err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}
	return nil
}

func parseScopes(raw string) (map[string]struct{}, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]struct{}{}, nil
	}
	var scopes []string
	if err := json.Unmarshal([]byte(raw), &scopes); err != nil {
		return nil, err
	}
	scopeSet := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		scopeSet[scope] = struct{}{}
	}
	return scopeSet, nil
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

