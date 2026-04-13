package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"urgentry/internal/auth"
	"urgentry/internal/discover"
	"urgentry/internal/sqlite"
)

// ---------------------------------------------------------------------------
// Saved Search Endpoints
// ---------------------------------------------------------------------------

type savedSearchResponse struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	Favorite    bool     `json:"favorite"`
	Visibility  string   `json:"visibility"`
	Dataset     string   `json:"dataset"`
	Query       string   `json:"query"`
	Filter      string   `json:"filter"`
	Environment string   `json:"environment"`
	Sort        string   `json:"sort"`
	DetailURL   string   `json:"detailUrl"`
	OpenURL     string   `json:"openUrl"`
}

func mapSavedSearchResponse(item *sqlite.SavedSearch) savedSearchResponse {
	return savedSearchResponse{
		ID:          item.ID,
		Name:        item.Name,
		Description: item.Description,
		Tags:        append([]string(nil), item.Tags...),
		Favorite:    item.Favorite,
		Visibility:  string(item.Visibility),
		Dataset:     savedQueryDataset(*item),
		Query:       item.Query,
		Filter:      item.Filter,
		Environment: item.Environment,
		Sort:        item.Sort,
		DetailURL:   discoverSavedQueryDetailURL(*item),
		OpenURL:     discoverSavedQueryURL(savedQueryPath(savedQueryDataset(*item)), *item),
	}
}

func (h *Handler) saveSearch(w http.ResponseWriter, r *http.Request) {
	if h.searches == nil {
		http.Error(w, "saved searches not available", http.StatusServiceUnavailable)
		return
	}
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Query       string `json:"query"`
		Filter      string `json:"filter"`
		Environment string `json:"environment"`
		Sort        string `json:"sort"`
		Visibility  string `json:"visibility"`
		Favorite    bool   `json:"favorite"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	scope, err := h.defaultPageScope(r.Context())
	if err != nil {
		http.Error(w, "Failed to resolve default organization scope.", http.StatusInternalServerError)
		return
	}

	ss, err := h.searches.Save(r.Context(), principal.User.ID, scope.OrganizationSlug, sqlite.SavedSearchVisibility(req.Visibility), req.Name, req.Description, req.Query, req.Filter, req.Environment, req.Sort, req.Favorite)
	if err != nil {
		var validationErrs discover.ValidationErrors
		if errors.As(err, &validationErrs) {
			http.Error(w, validationErrs.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "save failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(mapSavedSearchResponse(ss)); err != nil {
		http.Error(w, "encode failed: "+err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) getSearch(w http.ResponseWriter, r *http.Request) {
	if h.searches == nil {
		http.Error(w, "saved searches not available", http.StatusServiceUnavailable)
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	scope, err := h.defaultPageScope(r.Context())
	if err != nil {
		http.Error(w, "Failed to resolve default organization scope.", http.StatusInternalServerError)
		return
	}
	ss, err := h.searches.Get(r.Context(), principal.User.ID, scope.OrganizationSlug, strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		http.Error(w, "lookup failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if ss == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(mapSavedSearchResponse(ss)); err != nil {
		http.Error(w, "encode failed: "+err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) cloneSearch(w http.ResponseWriter, r *http.Request) {
	if h.searches == nil {
		http.Error(w, "saved searches not available", http.StatusServiceUnavailable)
		return
	}
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	scope, err := h.defaultPageScope(r.Context())
	if err != nil {
		http.Error(w, "Failed to resolve default organization scope.", http.StatusInternalServerError)
		return
	}
	var req struct {
		Name       string `json:"name"`
		Visibility string `json:"visibility"`
		Favorite   bool   `json:"favorite"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	cloned, err := h.searches.Clone(r.Context(), principal.User.ID, scope.OrganizationSlug, strings.TrimSpace(r.PathValue("id")), req.Name, sqlite.SavedSearchVisibility(req.Visibility), req.Favorite)
	if err != nil {
		http.Error(w, "clone failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if cloned == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(mapSavedSearchResponse(cloned)); err != nil {
		http.Error(w, "encode failed: "+err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) updateSearch(w http.ResponseWriter, r *http.Request) {
	if h.searches == nil {
		http.Error(w, "saved searches not available", http.StatusServiceUnavailable)
		return
	}
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	scope, err := h.defaultPageScope(r.Context())
	if err != nil {
		http.Error(w, "Failed to resolve default organization scope.", http.StatusInternalServerError)
		return
	}
	var req struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Visibility  string   `json:"visibility"`
		Tags        []string `json:"tags"`
		Favorite    bool     `json:"favorite"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	item, err := h.searches.UpdateMetadata(r.Context(), principal.User.ID, scope.OrganizationSlug, strings.TrimSpace(r.PathValue("id")), req.Name, req.Description, sqlite.SavedSearchVisibility(req.Visibility), req.Tags)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "update failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.searches.SetFavorite(r.Context(), principal.User.ID, scope.OrganizationSlug, item.ID, req.Favorite); err != nil {
		http.Error(w, "favorite update failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	item, err = h.searches.Get(r.Context(), principal.User.ID, scope.OrganizationSlug, item.ID)
	if err != nil {
		http.Error(w, "reload failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(mapSavedSearchResponse(item)); err != nil {
		http.Error(w, "encode failed: "+err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) deleteSearch(w http.ResponseWriter, r *http.Request) {
	if h.searches == nil {
		http.Error(w, "saved searches not available", http.StatusServiceUnavailable)
		return
	}
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}

	if err := h.searches.Delete(r.Context(), principal.User.ID, id); err != nil {
		http.Error(w, "delete failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
