package web

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"urgentry/internal/auth"
)

// ---------------------------------------------------------------------------
// Shared account settings data
// ---------------------------------------------------------------------------

type accountSettingsData struct {
	Title        string
	Nav          string
	Environment  string
	Environments []string
	ActiveTab    string // "account" | "security" | "notifications" | "api" | "close"
	UserEmail    string
	UserName     string
}

func (h *Handler) accountSettingsBase(r *http.Request, tab string) accountSettingsData {
	ctx := r.Context()
	data := accountSettingsData{
		Nav:          "settings",
		ActiveTab:    tab,
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(ctx),
	}
	if principal := auth.PrincipalFromContext(ctx); principal != nil && principal.User != nil {
		data.UserEmail = principal.User.Email
		data.UserName = principal.User.DisplayName
	}
	return data
}

// ---------------------------------------------------------------------------
// GET /settings/account/
// ---------------------------------------------------------------------------

type accountDetailsData struct {
	accountSettingsData
}

func (h *Handler) accountDetailsPage(w http.ResponseWriter, r *http.Request) {
	base := h.accountSettingsBase(r, "account")
	base.Title = "Account Details"

	data := accountDetailsData{accountSettingsData: base}
	h.render(w, "settings-account.html", data)
}

// ---------------------------------------------------------------------------
// GET /settings/account/security/
// ---------------------------------------------------------------------------

type accountSecuritySession struct {
	ID        string
	UserAgent string
	IPAddress string
	CreatedAt string
	LastSeen  string
	IsCurrent bool
}

type accountSecurityData struct {
	accountSettingsData
	Sessions []accountSecuritySession
}

func (h *Handler) accountSecurityPage(w http.ResponseWriter, r *http.Request) {
	base := h.accountSettingsBase(r, "security")
	base.Title = "Account Security"

	var sessions []accountSecuritySession
	ctx := r.Context()
	principal := auth.PrincipalFromContext(ctx)
	if h.db != nil && principal != nil && principal.User != nil {
		rows, err := listUserSessions(ctx, h.db, principal.User.ID)
		if err == nil {
			for _, row := range rows {
				sessions = append(sessions, accountSecuritySession{
					ID:        row.ID,
					UserAgent: row.UserAgent,
					IPAddress: row.IPAddress,
					CreatedAt: timeAgo(row.CreatedAt),
					LastSeen:  timeAgo(row.LastSeen),
					IsCurrent: row.ID == principal.CredentialID,
				})
			}
		}
	}

	data := accountSecurityData{
		accountSettingsData: base,
		Sessions:            sessions,
	}
	h.render(w, "settings-account-security.html", data)
}

// activeSessionRow is the raw DB row for a user session.
type activeSessionRow struct {
	ID        string
	UserAgent string
	IPAddress string
	CreatedAt time.Time
	LastSeen  time.Time
}

// listUserSessions queries non-revoked sessions for the given user.
func listUserSessions(ctx context.Context, db *sql.DB, userID string) ([]activeSessionRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, COALESCE(user_agent,''), COALESCE(ip_address,''), created_at, last_seen_at
		 FROM user_sessions
		 WHERE user_id = ? AND revoked_at IS NULL
		 ORDER BY last_seen_at DESC
		 LIMIT 20`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []activeSessionRow
	for rows.Next() {
		var row activeSessionRow
		var createdAt, lastSeen string
		if err := rows.Scan(&row.ID, &row.UserAgent, &row.IPAddress, &createdAt, &lastSeen); err != nil {
			return nil, err
		}
		row.CreatedAt = parseDBTime(createdAt)
		row.LastSeen = parseDBTime(lastSeen)
		result = append(result, row)
	}
	return result, rows.Err()
}

// ---------------------------------------------------------------------------
// POST /settings/account/security/revoke-session
// ---------------------------------------------------------------------------

func (h *Handler) revokeAccountSession(w http.ResponseWriter, r *http.Request) {
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeWebBadRequest(w, r, "Invalid form")
		return
	}
	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	if sessionID == "" {
		writeWebBadRequest(w, r, "session_id is required")
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		writeWebUnauthorized(w, r)
		return
	}
	// Prevent revoking the current session via this form; use /logout instead.
	if sessionID == principal.CredentialID {
		writeWebBadRequest(w, r, "Use /logout to end the current session")
		return
	}
	// Revoke only if the session belongs to this user.
	if h.db != nil {
		if _, err := h.db.ExecContext(r.Context(),
			`UPDATE user_sessions SET revoked_at = datetime('now') WHERE id = ? AND user_id = ? AND revoked_at IS NULL`,
			sessionID, principal.User.ID,
		); err != nil {
			writeWebInternal(w, r, "Failed to revoke session")
			return
		}
	}
	http.Redirect(w, r, "/settings/account/security/", http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// GET /settings/account/notifications/
// ---------------------------------------------------------------------------

type accountNotificationsData struct {
	accountSettingsData
}

func (h *Handler) accountNotificationsPage(w http.ResponseWriter, r *http.Request) {
	base := h.accountSettingsBase(r, "notifications")
	base.Title = "Notification Preferences"

	data := accountNotificationsData{accountSettingsData: base}
	h.render(w, "settings-account-notifications.html", data)
}

// ---------------------------------------------------------------------------
// GET /settings/account/api/
// ---------------------------------------------------------------------------

type accountAPIToken struct {
	ID          string
	Label       string
	TokenPrefix string
	Scopes      string
	CreatedAt   string
	LastUsedAt  string
	ExpiresAt   string
	IsRevoked   bool
}

type accountAPIData struct {
	accountSettingsData
	Tokens          []accountAPIToken
	NewTokenRaw     string // populated once after creation
	AvailableScopes []string
}

func (h *Handler) accountAPIPage(w http.ResponseWriter, r *http.Request) {
	base := h.accountSettingsBase(r, "api")
	base.Title = "API Tokens"

	data := accountAPIData{
		accountSettingsData: base,
		AvailableScopes:     availablePATScopes(),
	}

	if h.tokenManager != nil {
		if principal := auth.PrincipalFromContext(r.Context()); principal != nil && principal.User != nil {
			records, err := h.tokenManager.ListPersonalAccessTokens(r.Context(), principal.User.ID)
			if err == nil {
				for _, rec := range records {
					token := accountAPIToken{
						ID:          rec.ID,
						Label:       rec.Label,
						TokenPrefix: rec.TokenPrefix,
						Scopes:      strings.Join(rec.Scopes, ", "),
						CreatedAt:   rec.CreatedAt.Format("2006-01-02"),
						IsRevoked:   rec.RevokedAt != nil,
					}
					if rec.LastUsedAt != nil {
						token.LastUsedAt = timeAgo(*rec.LastUsedAt)
					}
					if rec.ExpiresAt != nil {
						token.ExpiresAt = rec.ExpiresAt.Format("2006-01-02")
					}
					data.Tokens = append(data.Tokens, token)
				}
			}
		}
	}

	h.render(w, "settings-account-api.html", data)
}

// POST /settings/account/api/create
func (h *Handler) createAccountAPIToken(w http.ResponseWriter, r *http.Request) {
	if h.tokenManager == nil {
		writeWebUnavailable(w, r, "Token management unavailable")
		return
	}
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeWebBadRequest(w, r, "Invalid form")
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		writeWebUnauthorized(w, r)
		return
	}

	label := strings.TrimSpace(r.FormValue("label"))
	if label == "" {
		writeWebBadRequest(w, r, "Token label is required")
		return
	}
	scopes := r.Form["scopes"]
	if len(scopes) == 0 {
		scopes = []string{auth.ScopeOrgRead}
	}

	rawToken, err := h.tokenManager.CreatePersonalAccessToken(r.Context(), principal.User.ID, label, scopes, nil, "")
	if err != nil {
		writeWebInternal(w, r, "Failed to create token")
		return
	}

	// Re-render the page with the newly created raw token shown once.
	base := h.accountSettingsBase(r, "api")
	base.Title = "API Tokens"
	data := accountAPIData{
		accountSettingsData: base,
		AvailableScopes:     availablePATScopes(),
		NewTokenRaw:         rawToken,
	}
	records, err := h.tokenManager.ListPersonalAccessTokens(r.Context(), principal.User.ID)
	if err == nil {
		for _, rec := range records {
			token := accountAPIToken{
				ID:          rec.ID,
				Label:       rec.Label,
				TokenPrefix: rec.TokenPrefix,
				Scopes:      strings.Join(rec.Scopes, ", "),
				CreatedAt:   rec.CreatedAt.Format("2006-01-02"),
				IsRevoked:   rec.RevokedAt != nil,
			}
			if rec.LastUsedAt != nil {
				token.LastUsedAt = timeAgo(*rec.LastUsedAt)
			}
			if rec.ExpiresAt != nil {
				token.ExpiresAt = rec.ExpiresAt.Format("2006-01-02")
			}
			data.Tokens = append(data.Tokens, token)
		}
	}
	h.render(w, "settings-account-api.html", data)
}

// POST /settings/account/api/{token_id}/revoke
func (h *Handler) revokeAccountAPIToken(w http.ResponseWriter, r *http.Request) {
	if h.tokenManager == nil {
		writeWebUnavailable(w, r, "Token management unavailable")
		return
	}
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		writeWebUnauthorized(w, r)
		return
	}
	tokenID := r.PathValue("token_id")
	if tokenID == "" {
		writeWebBadRequest(w, r, "token_id is required")
		return
	}
	if err := h.tokenManager.RevokePersonalAccessToken(r.Context(), tokenID, principal.User.ID); err != nil {
		writeWebInternal(w, r, "Failed to revoke token")
		return
	}
	http.Redirect(w, r, "/settings/account/api/", http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// GET /settings/account/close/
// ---------------------------------------------------------------------------

type accountCloseData struct {
	accountSettingsData
}

func (h *Handler) accountClosePage(w http.ResponseWriter, r *http.Request) {
	base := h.accountSettingsBase(r, "close")
	base.Title = "Close Account"

	data := accountCloseData{accountSettingsData: base}
	h.render(w, "settings-account-close.html", data)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func availablePATScopes() []string {
	return []string{
		auth.ScopeOrgRead,
		auth.ScopeOrgAdmin,
		auth.ScopeOrgQueryRead,
		auth.ScopeOrgQueryWrite,
		auth.ScopeProjectRead,
		auth.ScopeProjectWrite,
		auth.ScopeProjectKeysRead,
		auth.ScopeProjectKeysWrite,
		auth.ScopeProjectTokensRead,
		auth.ScopeProjectTokensWrite,
		auth.ScopeIssueWrite,
		auth.ScopeReleaseRead,
		auth.ScopeReleaseWrite,
	}
}
