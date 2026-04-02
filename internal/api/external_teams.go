package api

import (
	"net/http"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/store"
)

// externalTeamResponse is the JSON shape returned for a single external team mapping.
type externalTeamResponse struct {
	ID           string `json:"id"`
	TeamSlug     string `json:"teamSlug"`
	Provider     string `json:"provider"`
	ExternalID   string `json:"externalId"`
	ExternalName string `json:"externalName"`
	CreatedAt    string `json:"dateCreated"`
}

func toExternalTeamResponse(t *store.ExternalTeam) externalTeamResponse {
	return externalTeamResponse{
		ID:           t.ID,
		TeamSlug:     t.TeamSlug,
		Provider:     t.Provider,
		ExternalID:   t.ExternalID,
		ExternalName: t.ExternalName,
		CreatedAt:    t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

type createExternalTeamRequest struct {
	TeamSlug     string `json:"teamSlug"`
	Provider     string `json:"provider"`
	ExternalID   string `json:"externalId"`
	ExternalName string `json:"externalName"`
}

// handleListExternalTeams handles GET /api/0/organizations/{org_slug}/external-teams/.
func handleListExternalTeams(
	catalog controlplane.CatalogStore,
	externalTeams store.ExternalTeamStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
		if !ok {
			return
		}
		items, err := externalTeams.ListExternalTeams(r.Context(), org.ID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list external team mappings.")
			return
		}
		out := make([]externalTeamResponse, 0, len(items))
		for _, t := range items {
			out = append(out, toExternalTeamResponse(t))
		}
		httputil.WriteJSON(w, http.StatusOK, out)
	}
}

// handleCreateExternalTeam handles POST /api/0/organizations/{org_slug}/external-teams/.
func handleCreateExternalTeam(
	catalog controlplane.CatalogStore,
	externalTeams store.ExternalTeamStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
		if !ok {
			return
		}
		var body createExternalTeamRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if body.Provider == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing required field: provider")
			return
		}
		t := &store.ExternalTeam{
			OrgID:        org.ID,
			TeamSlug:     body.TeamSlug,
			Provider:     body.Provider,
			ExternalID:   body.ExternalID,
			ExternalName: body.ExternalName,
		}
		if err := externalTeams.CreateExternalTeam(r.Context(), t); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create external team mapping.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, toExternalTeamResponse(t))
	}
}

type updateExternalTeamRequest struct {
	TeamSlug     string `json:"teamSlug"`
	Provider     string `json:"provider"`
	ExternalID   string `json:"externalId"`
	ExternalName string `json:"externalName"`
}

// handleUpdateExternalTeam handles PUT /api/0/organizations/{org_slug}/external-teams/{id}/.
func handleUpdateExternalTeam(
	externalTeams store.ExternalTeamStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		extTeamID := PathParam(r, "id")
		if extTeamID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing external team ID.")
			return
		}
		var body updateExternalTeamRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		t := &store.ExternalTeam{
			ID:           extTeamID,
			TeamSlug:     body.TeamSlug,
			Provider:     body.Provider,
			ExternalID:   body.ExternalID,
			ExternalName: body.ExternalName,
		}
		if err := externalTeams.UpdateExternalTeam(r.Context(), t); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update external team mapping.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, toExternalTeamResponse(t))
	}
}

// handleDeleteExternalTeam handles DELETE /api/0/organizations/{org_slug}/external-teams/{id}/.
func handleDeleteExternalTeam(
	externalTeams store.ExternalTeamStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		extTeamID := PathParam(r, "id")
		if extTeamID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing external team ID.")
			return
		}
		if err := externalTeams.DeleteExternalTeam(r.Context(), extTeamID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete external team mapping.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
