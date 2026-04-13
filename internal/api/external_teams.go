package api

import (
	"net/http"
	"strings"

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
	TeamSlug           string `json:"teamSlug"`
	Provider           string `json:"provider"`
	ExternalID         string `json:"externalId"`
	ExternalIDSnake    string `json:"external_id"`
	ExternalName       string `json:"externalName"`
	ExternalNameSnake  string `json:"external_name"`
	IntegrationID      int    `json:"integrationId"`
	IntegrationIDSnake int    `json:"integration_id"`
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
			TeamSlug:     effectiveExternalTeamSlug(body.TeamSlug, ""),
			Provider:     body.Provider,
			ExternalID:   effectiveExternalID(body.ExternalID, body.ExternalIDSnake),
			ExternalName: effectiveExternalName(body.ExternalName, body.ExternalNameSnake),
		}
		if err := externalTeams.CreateExternalTeam(r.Context(), t); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create external team mapping.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, toExternalTeamResponse(t))
	}
}

type updateExternalTeamRequest struct {
	TeamSlug           string `json:"teamSlug"`
	Provider           string `json:"provider"`
	ExternalID         string `json:"externalId"`
	ExternalIDSnake    string `json:"external_id"`
	ExternalName       string `json:"externalName"`
	ExternalNameSnake  string `json:"external_name"`
	IntegrationID      int    `json:"integrationId"`
	IntegrationIDSnake int    `json:"integration_id"`
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
			TeamSlug:     effectiveExternalTeamSlug(body.TeamSlug, ""),
			Provider:     body.Provider,
			ExternalID:   effectiveExternalID(body.ExternalID, body.ExternalIDSnake),
			ExternalName: effectiveExternalName(body.ExternalName, body.ExternalNameSnake),
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

func handleCreateTeamExternalTeam(catalog controlplane.CatalogStore, externalTeams store.ExternalTeamStore, auth authFunc) http.HandlerFunc {
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
		if strings.TrimSpace(body.Provider) == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing required field: provider")
			return
		}
		t := &store.ExternalTeam{
			OrgID:        org.ID,
			TeamSlug:     effectiveExternalTeamSlug(body.TeamSlug, PathParam(r, "team_slug")),
			Provider:     strings.TrimSpace(body.Provider),
			ExternalID:   effectiveExternalID(body.ExternalID, body.ExternalIDSnake),
			ExternalName: effectiveExternalName(body.ExternalName, body.ExternalNameSnake),
		}
		if err := externalTeams.CreateExternalTeam(r.Context(), t); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create external team mapping.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, toExternalTeamResponse(t))
	}
}

func handleUpdateTeamExternalTeam(externalTeams store.ExternalTeamStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		extTeamID := PathParam(r, "external_team_id")
		if strings.TrimSpace(extTeamID) == "" {
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
			TeamSlug:     effectiveExternalTeamSlug(body.TeamSlug, PathParam(r, "team_slug")),
			Provider:     strings.TrimSpace(body.Provider),
			ExternalID:   effectiveExternalID(body.ExternalID, body.ExternalIDSnake),
			ExternalName: effectiveExternalName(body.ExternalName, body.ExternalNameSnake),
		}
		if err := externalTeams.UpdateExternalTeam(r.Context(), t); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update external team mapping.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, toExternalTeamResponse(t))
	}
}

func handleDeleteTeamExternalTeam(externalTeams store.ExternalTeamStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		extTeamID := PathParam(r, "external_team_id")
		if strings.TrimSpace(extTeamID) == "" {
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

func effectiveExternalTeamSlug(primary, fallback string) string {
	if slug := strings.TrimSpace(primary); slug != "" {
		return slug
	}
	return strings.TrimSpace(fallback)
}

func effectiveExternalID(primary, secondary string) string {
	if value := strings.TrimSpace(primary); value != "" {
		return value
	}
	return strings.TrimSpace(secondary)
}

func effectiveExternalName(primary, secondary string) string {
	if value := strings.TrimSpace(primary); value != "" {
		return value
	}
	return strings.TrimSpace(secondary)
}
