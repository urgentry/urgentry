package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
)

var (
	allowedPATScopes = map[string]struct{}{
		auth.ScopeOrgRead:               {},
		auth.ScopeOrgAdmin:              {},
		auth.ScopeOrgQueryRead:          {},
		auth.ScopeOrgQueryWrite:         {},
		auth.ScopeProjectRead:           {},
		auth.ScopeProjectWrite:          {},
		auth.ScopeProjectKeysRead:       {},
		auth.ScopeProjectKeysWrite:      {},
		auth.ScopeProjectTokensRead:     {},
		auth.ScopeProjectTokensWrite:    {},
		auth.ScopeProjectArtifactsWrite: {},
		auth.ScopeIssueWrite:            {},
		auth.ScopeReleaseRead:           {},
		auth.ScopeReleaseWrite:          {},
	}
	defaultPATScopes = []string{
		auth.ScopeOrgRead,
		auth.ScopeProjectRead,
		auth.ScopeReleaseRead,
	}
	allowedAutomationScopes = map[string]struct{}{
		auth.ScopeProjectArtifactsWrite: {},
	}
	defaultAutomationScopes = []string{
		auth.ScopeProjectArtifactsWrite,
	}
)

type createTokenRequest struct {
	Label     string   `json:"label"`
	Scopes    []string `json:"scopes"`
	ExpiresAt string   `json:"expiresAt"`
}

func handleListPersonalAccessTokens(tokenManager auth.TokenManager, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		principal := requireSessionPrincipal(w, r)
		if principal == nil {
			return
		}

		tokens, err := tokenManager.ListPersonalAccessTokens(r.Context(), principal.User.ID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list personal access tokens.")
			return
		}
		response := make([]PersonalAccessToken, 0, len(tokens))
		for _, token := range tokens {
			response = append(response, mapPAT(token))
		}
		httputil.WriteJSON(w, http.StatusOK, response)
	}
}

func handleCreatePersonalAccessToken(authz *auth.Authorizer, tokenManager auth.TokenManager, principalShadows *sqlite.PrincipalShadowStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		if !authz.ValidateCSRF(r) {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusForbidden,
				Code:   "csrf_failed",
				Detail: "CSRF validation failed.",
			})
			return
		}
		principal := requireSessionPrincipal(w, r)
		if principal == nil {
			return
		}

		var body createTokenRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONAPIError(w, err)
			return
		}
		scopes, err := normalizeScopes(body.Scopes, defaultPATScopes, allowedPATScopes)
		if err != nil {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusBadRequest,
				Code:   "invalid_token_scopes",
				Detail: err.Error(),
			})
			return
		}
		expiresAt, err := parseExpiry(body.ExpiresAt)
		if err != nil {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusBadRequest,
				Code:   "invalid_expires_at",
				Detail: "expiresAt must be RFC3339.",
			})
			return
		}
		if err := principalShadows.UpsertUser(r.Context(), principal.User); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to prepare authenticated identity.")
			return
		}

		raw, err := tokenManager.CreatePersonalAccessToken(r.Context(), principal.User.ID, strings.TrimSpace(body.Label), scopes, expiresAt, "")
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create personal access token.")
			return
		}

		tokens, err := tokenManager.ListPersonalAccessTokens(r.Context(), principal.User.ID)
		record, ok := findPATByPrefix(tokens, tokenPrefix(raw))
		if err != nil || !ok {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load created personal access token.")
			return
		}

		httputil.WriteJSON(w, http.StatusCreated, CreatedPersonalAccessToken{
			PersonalAccessToken: mapPAT(record),
			Token:               raw,
		})
	}
}

func handleRevokePersonalAccessToken(authz *auth.Authorizer, tokenManager auth.TokenManager, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		if !authz.ValidateCSRF(r) {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusForbidden,
				Code:   "csrf_failed",
				Detail: "CSRF validation failed.",
			})
			return
		}
		principal := requireSessionPrincipal(w, r)
		if principal == nil {
			return
		}
		if err := tokenManager.RevokePersonalAccessToken(r.Context(), PathParam(r, "token_id"), principal.User.ID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to revoke personal access token.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleListAutomationTokens(catalog controlplane.CatalogStore, tokenManager auth.TokenManager, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		if requireSessionPrincipal(w, r) == nil {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		tokens, err := tokenManager.ListAutomationTokens(r.Context(), projectID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list automation tokens.")
			return
		}
		response := make([]AutomationToken, 0, len(tokens))
		for _, token := range tokens {
			response = append(response, mapAutomationToken(token))
		}
		httputil.WriteJSON(w, http.StatusOK, response)
	}
}

func handleCreateAutomationToken(catalog controlplane.CatalogStore, authz *auth.Authorizer, tokenManager auth.TokenManager, principalShadows *sqlite.PrincipalShadowStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		if !authz.ValidateCSRF(r) {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusForbidden,
				Code:   "csrf_failed",
				Detail: "CSRF validation failed.",
			})
			return
		}
		principal := requireSessionPrincipal(w, r)
		if principal == nil {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}

		var body createTokenRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONAPIError(w, err)
			return
		}
		scopes, err := normalizeScopes(body.Scopes, defaultAutomationScopes, allowedAutomationScopes)
		if err != nil {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusBadRequest,
				Code:   "invalid_token_scopes",
				Detail: err.Error(),
			})
			return
		}
		expiresAt, err := parseExpiry(body.ExpiresAt)
		if err != nil {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusBadRequest,
				Code:   "invalid_expires_at",
				Detail: "expiresAt must be RFC3339.",
			})
			return
		}
		if err := principalShadows.UpsertUser(r.Context(), principal.User); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to prepare authenticated identity.")
			return
		}

		raw, err := tokenManager.CreateAutomationToken(r.Context(), projectID, strings.TrimSpace(body.Label), principal.User.ID, scopes, expiresAt, "")
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create automation token.")
			return
		}

		tokens, err := tokenManager.ListAutomationTokens(r.Context(), projectID)
		record, ok := findAutomationTokenByPrefix(tokens, tokenPrefix(raw))
		if err != nil || !ok {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load created automation token.")
			return
		}

		httputil.WriteJSON(w, http.StatusCreated, CreatedAutomationToken{
			AutomationToken: mapAutomationToken(record),
			Token:           raw,
		})
	}
}

func handleRevokeAutomationToken(catalog controlplane.CatalogStore, authz *auth.Authorizer, tokenManager auth.TokenManager, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		if !authz.ValidateCSRF(r) {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusForbidden,
				Code:   "csrf_failed",
				Detail: "CSRF validation failed.",
			})
			return
		}
		if requireSessionPrincipal(w, r) == nil {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		if err := tokenManager.RevokeAutomationToken(r.Context(), PathParam(r, "token_id"), projectID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to revoke automation token.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func requireSessionPrincipal(w http.ResponseWriter, r *http.Request) *auth.Principal {
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.Kind != auth.CredentialSession || principal.User == nil {
		httputil.WriteAPIError(w, httputil.APIError{
			Status: http.StatusForbidden,
			Code:   "interactive_session_required",
			Detail: "Interactive session required.",
		})
		return nil
	}
	return principal
}

func resolveProjectID(w http.ResponseWriter, r *http.Request, db *sql.DB) (string, bool) {
	projectID, err := projectIDFromSlugs(r, db, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
	if err != nil || projectID == "" {
		httputil.WriteError(w, http.StatusNotFound, "Project not found.")
		return "", false
	}
	return projectID, true
}

func normalizeScopes(requested, defaults []string, allowed map[string]struct{}) ([]string, error) {
	if len(requested) == 0 {
		requested = defaults
	}
	seen := make(map[string]struct{}, len(requested))
	scopes := make([]string, 0, len(requested))
	for _, scope := range requested {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if _, ok := allowed[scope]; !ok {
			return nil, fmt.Errorf("unsupported scope %q", scope)
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		scopes = append(scopes, scope)
	}
	if len(scopes) == 0 {
		return nil, fmt.Errorf("at least one supported scope is required")
	}
	return scopes, nil
}

func parseExpiry(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	expiresAt, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return &expiresAt, nil
}

func mapPAT(record auth.PersonalAccessTokenRecord) PersonalAccessToken {
	return PersonalAccessToken{
		ID:          record.ID,
		Label:       record.Label,
		TokenPrefix: record.TokenPrefix,
		Scopes:      record.Scopes,
		DateCreated: record.CreatedAt,
		LastUsed:    record.LastUsedAt,
		ExpiresAt:   record.ExpiresAt,
		RevokedAt:   record.RevokedAt,
	}
}

func mapAutomationToken(record auth.AutomationTokenRecord) AutomationToken {
	return AutomationToken{
		ID:              record.ID,
		ProjectID:       record.ProjectID,
		Label:           record.Label,
		TokenPrefix:     record.TokenPrefix,
		Scopes:          record.Scopes,
		CreatedByUserID: record.CreatedByUserID,
		DateCreated:     record.CreatedAt,
		LastUsed:        record.LastUsedAt,
		ExpiresAt:       record.ExpiresAt,
		RevokedAt:       record.RevokedAt,
	}
}

func findPATByPrefix(tokens []auth.PersonalAccessTokenRecord, prefix string) (auth.PersonalAccessTokenRecord, bool) {
	for _, token := range tokens {
		if token.TokenPrefix == prefix {
			return token, true
		}
	}
	return auth.PersonalAccessTokenRecord{}, false
}

func findAutomationTokenByPrefix(tokens []auth.AutomationTokenRecord, prefix string) (auth.AutomationTokenRecord, bool) {
	for _, token := range tokens {
		if token.TokenPrefix == prefix {
			return token, true
		}
	}
	return auth.AutomationTokenRecord{}, false
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
