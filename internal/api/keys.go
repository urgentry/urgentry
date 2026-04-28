package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/requestmeta"
	"urgentry/internal/store"
)

// handleListKeys handles GET /api/0/projects/{org_slug}/{proj_slug}/keys/.
func handleListKeys(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")
		recs, err := catalog.ListProjectKeys(r.Context(), org, proj)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list project keys.")
			return
		}
		keys := make([]*ProjectKey, 0, len(recs))
		for _, rec := range recs {
			keys = append(keys, apiProjectKeyFromMeta(r, rec))
		}
		if keys == nil {
			keys = []*ProjectKey{}
		}
		httputil.WriteJSON(w, http.StatusOK, keys)
	}
}

// createKeyRequest is the JSON body for creating a key.
type createKeyRequest struct {
	Label string `json:"label"`
}

// handleCreateKey handles POST /api/0/projects/{org_slug}/{proj_slug}/keys/.
func handleCreateKey(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")

		var body createKeyRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		label := body.Label
		if label == "" {
			label = "Default"
		}

		meta, err := catalog.CreateProjectKey(r.Context(), org, proj, label)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create project key.")
			return
		}
		if meta == nil {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, apiProjectKeyFromMeta(r, *meta))
	}
}

func baseURLFromRequest(r *http.Request) string {
	scheme := requestmeta.Scheme(r)
	host := requestmeta.Host(r)
	if host == "" {
		host = "localhost:8080"
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}

// handleGetKey handles GET /api/0/projects/{org_slug}/{proj_slug}/keys/{key_id}/.
func handleGetKey(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")
		keyID := PathParam(r, "key_id")
		rec, err := catalog.GetProjectKey(r.Context(), org, proj, keyID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to get project key.")
			return
		}
		if rec == nil {
			httputil.WriteError(w, http.StatusNotFound, "Project key not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, apiProjectKeyFromMeta(r, *rec))
	}
}

// updateKeyRequest is the JSON body for updating a key.
type updateKeyRequest struct {
	Name      string `json:"name"`
	IsActive  *bool  `json:"isActive"`
	RateLimit *struct {
		Count int `json:"count"`
	} `json:"rateLimit"`
}

// handleUpdateKey handles PUT /api/0/projects/{org_slug}/{proj_slug}/keys/{key_id}/.
func handleUpdateKey(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")
		keyID := PathParam(r, "key_id")

		var body updateKeyRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}

		update := store.ProjectKeyUpdate{
			Name:     body.Name,
			IsActive: body.IsActive,
		}
		if body.RateLimit != nil {
			update.RateLimit = &body.RateLimit.Count
		}

		rec, err := catalog.UpdateProjectKey(r.Context(), org, proj, keyID, update)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update project key.")
			return
		}
		if rec == nil {
			httputil.WriteError(w, http.StatusNotFound, "Project key not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, apiProjectKeyFromMeta(r, *rec))
	}
}

// handleDeleteKey handles DELETE /api/0/projects/{org_slug}/{proj_slug}/keys/{key_id}/.
func handleDeleteKey(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")
		keyID := PathParam(r, "key_id")

		err := catalog.DeleteProjectKey(r.Context(), org, proj, keyID)
		if err != nil {
			if err == sql.ErrNoRows {
				httputil.WriteError(w, http.StatusNotFound, "Project key not found.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete project key.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func apiProjectKeyFromMeta(r *http.Request, rec store.ProjectKeyMeta) *ProjectKey {
	var rl *KeyRateLimit
	if rec.RateLimit > 0 {
		rl = &KeyRateLimit{Window: 60, Count: rec.RateLimit}
	}
	dsnURLs := apiProjectKeyDSNURLs(r, rec)
	return &ProjectKey{
		ID:          rec.ID,
		Name:        rec.Label,
		Label:       rec.Label,
		ProjectID:   rec.ProjectID,
		Public:      rec.PublicKey,
		Secret:      rec.SecretKey,
		IsActive:    rec.Status == "" || rec.Status == "active",
		RateLimit:   rl,
		DSN:         dsnURLs,
		DateCreated: rec.DateCreated,
	}
}

func apiProjectKeyDSNURLs(r *http.Request, rec store.ProjectKeyMeta) DSNURLs {
	baseURL := strings.TrimSuffix(baseURLFromRequest(r), "/")
	publicDSN := fmt.Sprintf("%s://%s@%s/%s", dsnScheme(baseURL), rec.PublicKey, dsnHost(baseURL), rec.ProjectID)
	secretDSN := fmt.Sprintf("%s://%s:%s@%s/%s", dsnScheme(baseURL), rec.PublicKey, rec.SecretKey, dsnHost(baseURL), rec.ProjectID)
	apiPath := fmt.Sprintf("%s/api/%s", baseURL, rec.ProjectID)
	integrationPath := apiPath + "/integration/"
	return DSNURLs{
		Public:      publicDSN,
		Secret:      secretDSN,
		CDN:         fmt.Sprintf("%s/js-sdk-loader/%s.min.js", baseURL, rec.PublicKey),
		Crons:       fmt.Sprintf("%s/cron/___MONITOR_SLUG___/%s/", apiPath, rec.PublicKey),
		CSP:         fmt.Sprintf("%s/csp-report/?sentry_key=%s", apiPath, rec.PublicKey),
		Integration: integrationPath,
		Minidump:    fmt.Sprintf("%s/minidump/?sentry_key=%s", apiPath, rec.PublicKey),
		NEL:         fmt.Sprintf("%s/nel/?sentry_key=%s", apiPath, rec.PublicKey),
		OTLPLogs:    integrationPath + "otlp/v1/logs",
		OTLPTraces:  integrationPath + "otlp/v1/traces",
		PlayStation: fmt.Sprintf("%s/playstation/?sentry_key=%s", apiPath, rec.PublicKey),
		Security:    fmt.Sprintf("%s/security/?sentry_key=%s", apiPath, rec.PublicKey),
		Unreal:      fmt.Sprintf("%s/unreal/%s/", apiPath, rec.PublicKey),
	}
}

func dsnScheme(baseURL string) string {
	if len(baseURL) > 5 && baseURL[:5] == "https" {
		return "https"
	}
	return "http"
}

func dsnHost(baseURL string) string {
	host := baseURL
	for _, prefix := range []string{"https://", "http://"} {
		if len(host) > len(prefix) && host[:len(prefix)] == prefix {
			host = host[len(prefix):]
			break
		}
	}
	if len(host) > 0 && host[len(host)-1] == '/' {
		host = host[:len(host)-1]
	}
	return host
}
