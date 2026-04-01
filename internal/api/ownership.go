package api

import (
	"net/http"
	"strings"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	sharedstore "urgentry/internal/store"
)

type ownershipRuleRequest struct {
	Name       string `json:"name"`
	Pattern    string `json:"pattern"`
	Assignee   string `json:"assignee"`
	TeamSlug   string `json:"teamSlug,omitempty"`
	NotifyTeam bool   `json:"notifyTeam,omitempty"`
}

func handleListOwnershipRules(catalog controlplane.CatalogStore, ownership controlplane.OwnershipStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		items, err := ownership.ListProjectRules(r.Context(), projectID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list ownership rules.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, items)
	}
}

func handleCreateOwnershipRule(catalog controlplane.CatalogStore, ownership controlplane.OwnershipStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		var body ownershipRuleRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		item, err := ownership.CreateRule(r.Context(), sharedstore.OwnershipRule{
			ProjectID:  projectID,
			Name:       strings.TrimSpace(body.Name),
			Pattern:    strings.TrimSpace(body.Pattern),
			Assignee:   strings.TrimSpace(body.Assignee),
			TeamSlug:   strings.TrimSpace(body.TeamSlug),
			NotifyTeam: body.NotifyTeam,
		})
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Failed to create ownership rule.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, item)
	}
}

func handleDeleteOwnershipRule(catalog controlplane.CatalogStore, ownership controlplane.OwnershipStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		if err := ownership.DeleteRule(r.Context(), projectID, PathParam(r, "rule_id")); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete ownership rule.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
