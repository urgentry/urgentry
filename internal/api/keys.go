package api

import (
	"fmt"

	"net/http"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
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
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
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
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme = forwarded
	}
	host := r.Host
	if host == "" {
		host = "localhost:8080"
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}

func apiProjectKeyFromMeta(r *http.Request, rec store.ProjectKeyMeta) *ProjectKey {
	return &ProjectKey{
		ID:        rec.ID,
		ProjectID: rec.ProjectID,
		Label:     rec.Label,
		Public:    rec.PublicKey,
		Secret:    rec.SecretKey,
		IsActive:  rec.Status == "" || rec.Status == "active",
		DSN: DSNURLs{
			Public: fmt.Sprintf("%s://%s@%s/%s", dsnScheme(baseURLFromRequest(r)), rec.PublicKey, dsnHost(baseURLFromRequest(r)), rec.ProjectID),
			Secret: fmt.Sprintf("%s://%s:%s@%s/%s", dsnScheme(baseURLFromRequest(r)), rec.PublicKey, rec.SecretKey, dsnHost(baseURLFromRequest(r)), rec.ProjectID),
		},
		DateCreated: rec.DateCreated,
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
