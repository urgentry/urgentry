package api

import (
	"net/http"

	"urgentry/internal/controlplane"
	"urgentry/internal/domain"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
)

// dataFilterResponse is the Sentry-compatible shape for a single data filter.
type dataFilterResponse struct {
	ID     string `json:"id"`
	Active bool   `json:"active"`
}

// wellKnownFilters are the standard Sentry data filter IDs.
var wellKnownFilters = []string{
	"browser-extensions",
	"legacy-browsers",
	"web-crawlers",
	"filtered-transaction",
}

// handleListDataFilters handles GET /api/0/projects/{org}/{proj}/filters/.
func handleListDataFilters(
	catalog controlplane.CatalogStore,
	filters *sqlite.InboundFilterStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		project, ok := getProjectFromCatalog(w, r, catalog, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
		if !ok {
			return
		}

		// Build map of active filters from the store.
		activeMap := map[string]bool{}
		if filters != nil {
			stored, err := filters.ListFilters(r.Context(), project.ID)
			if err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to list data filters.")
				return
			}
			for _, f := range stored {
				activeMap[filterTypeToID(f.Type)] = f.Active
			}
		}

		// Return the well-known filter list with active status.
		out := make([]dataFilterResponse, 0, len(wellKnownFilters))
		for _, fid := range wellKnownFilters {
			out = append(out, dataFilterResponse{
				ID:     fid,
				Active: activeMap[fid],
			})
		}
		httputil.WriteJSON(w, http.StatusOK, out)
	}
}

// updateFilterRequest is the JSON body for toggling a filter.
type updateFilterRequest struct {
	Active bool `json:"active"`
}

// handleUpdateDataFilter handles PUT /api/0/projects/{org}/{proj}/filters/{filter_id}/.
func handleUpdateDataFilter(
	catalog controlplane.CatalogStore,
	filters *sqlite.InboundFilterStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if filters == nil {
			httputil.WriteError(w, http.StatusServiceUnavailable, "Filter store unavailable.")
			return
		}
		project, ok := getProjectFromCatalog(w, r, catalog, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
		if !ok {
			return
		}

		filterID := PathParam(r, "filter_id")
		filterType := idToFilterType(filterID)
		if filterType == "" {
			httputil.WriteError(w, http.StatusNotFound, "Unknown filter.")
			return
		}

		var body updateFilterRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}

		// Find existing filter of this type for the project.
		stored, err := filters.ListFilters(r.Context(), project.ID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list data filters.")
			return
		}

		var existing *domain.InboundFilter
		for _, f := range stored {
			if f.Type == filterType {
				existing = f
				break
			}
		}

		if existing != nil {
			existing.Active = body.Active
			if err := filters.UpdateFilter(r.Context(), existing); err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to update filter.")
				return
			}
		} else {
			f := &domain.InboundFilter{
				ProjectID: project.ID,
				Type:      filterType,
				Active:    body.Active,
			}
			if err := filters.CreateFilter(r.Context(), f); err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to create filter.")
				return
			}
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// filterTypeToID maps domain.FilterType to the Sentry filter ID.
func filterTypeToID(ft domain.FilterType) string {
	switch ft {
	case domain.FilterLegacyBrowser:
		return "legacy-browsers"
	case domain.FilterCrawler:
		return "web-crawlers"
	case domain.FilterLocalhost:
		return "browser-extensions"
	case domain.FilterIPRange:
		return "filtered-transaction"
	default:
		return string(ft)
	}
}

// idToFilterType maps a Sentry filter ID to a domain.FilterType.
func idToFilterType(id string) domain.FilterType {
	switch id {
	case "legacy-browsers":
		return domain.FilterLegacyBrowser
	case "web-crawlers":
		return domain.FilterCrawler
	case "browser-extensions":
		return domain.FilterLocalhost
	case "filtered-transaction":
		return domain.FilterIPRange
	default:
		return ""
	}
}
