package api

import (
	"net/http"
	"strings"
	"time"

	"urgentry/internal/analyticsservice"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
)

// SavedQuery is the Sentry-compatible API response shape for a discover saved query.
type SavedQuery struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Fields      []string  `json:"fields"`
	Query       string    `json:"query"`
	OrderBy     string    `json:"orderby"`
	DateCreated time.Time `json:"dateCreated"`
	DateUpdated time.Time `json:"dateUpdated"`
}

type savedQueryRequest struct {
	Name    string   `json:"name"`
	Fields  []string `json:"fields"`
	Query   string   `json:"query"`
	OrderBy string   `json:"orderby"`
}

func handleListDiscoverSavedQueries(searches analyticsservice.SearchStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		principal := requireUserPrincipal(w, r)
		if principal == nil {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		items, err := searches.List(r.Context(), principal.User.ID, orgSlug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list saved queries.")
			return
		}
		resp := make([]SavedQuery, 0, len(items))
		for _, item := range items {
			resp = append(resp, mapSavedSearchToQuery(item))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleCreateDiscoverSavedQuery(searches analyticsservice.SearchStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		principal := requireUserPrincipal(w, r)
		if principal == nil {
			return
		}
		var body savedQueryRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Name is required.")
			return
		}
		orgSlug := PathParam(r, "org_slug")
		query := strings.TrimSpace(body.Query)
		sort := strings.TrimSpace(body.OrderBy)
		if sort == "" {
			sort = "last_seen"
		}
		item, err := searches.Save(
			r.Context(),
			principal.User.ID,
			orgSlug,
			sqlite.SavedSearchVisibilityOrganization,
			strings.TrimSpace(body.Name),
			"",    // description
			query, // query
			"all", // filter
			"",    // environment
			sort,
			false, // favorite
		)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create saved query.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, mapSavedSearchToQuery(*item))
	}
}

func handleGetDiscoverSavedQuery(searches analyticsservice.SearchStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		principal := requireUserPrincipal(w, r)
		if principal == nil {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		queryID := PathParam(r, "query_id")
		item, err := searches.Get(r.Context(), principal.User.ID, orgSlug, queryID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load saved query.")
			return
		}
		if item == nil {
			httputil.WriteError(w, http.StatusNotFound, "Saved query not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapSavedSearchToQuery(*item))
	}
}

func handleUpdateDiscoverSavedQuery(searches analyticsservice.SearchStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		principal := requireUserPrincipal(w, r)
		if principal == nil {
			return
		}
		var body savedQueryRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		orgSlug := PathParam(r, "org_slug")
		queryID := PathParam(r, "query_id")
		name := strings.TrimSpace(body.Name)
		if name == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Name is required.")
			return
		}
		item, err := searches.UpdateMetadata(
			r.Context(),
			principal.User.ID,
			orgSlug,
			queryID,
			name,
			"", // description
			sqlite.SavedSearchVisibilityOrganization,
			nil, // tags
		)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update saved query.")
			return
		}
		if item == nil {
			httputil.WriteError(w, http.StatusNotFound, "Saved query not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapSavedSearchToQuery(*item))
	}
}

func handleDeleteDiscoverSavedQuery(searches analyticsservice.SearchStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		principal := requireUserPrincipal(w, r)
		if principal == nil {
			return
		}
		queryID := PathParam(r, "query_id")
		if err := searches.Delete(r.Context(), principal.User.ID, queryID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete saved query.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func mapSavedSearchToQuery(item sqlite.SavedSearch) SavedQuery {
	fields := savedQueryFields(item)
	return SavedQuery{
		ID:          item.ID,
		Name:        item.Name,
		Fields:      fields,
		Query:       item.Query,
		OrderBy:     item.Sort,
		DateCreated: item.CreatedAt,
		DateUpdated: item.UpdatedAt,
	}
}

func savedQueryFields(item sqlite.SavedSearch) []string {
	// Derive fields from the filter scope. This matches Sentry's discover
	// saved-query response shape where fields come from the column configuration.
	switch item.Filter {
	case "issues", "":
		return []string{"title", "events", "users", "project", "lastSeen"}
	case "transactions":
		return []string{"transaction", "project", "count()", "avg(duration)"}
	case "logs":
		return []string{"message", "level", "project", "timestamp"}
	default:
		return []string{"title", "events", "users", "project", "lastSeen"}
	}
}
