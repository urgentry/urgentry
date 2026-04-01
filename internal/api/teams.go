package api

import (
	"net/http"
	"strings"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
)

// handleListTeams handles GET /api/0/organizations/{org_slug}/teams/.
func handleListTeams(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		orgRecord, err := catalog.GetOrganization(r.Context(), org)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load organization.")
			return
		}
		if orgRecord == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		teams, err := catalog.ListTeams(r.Context(), org)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list teams.")
			return
		}
		if teams == nil {
			teams = []Team{}
		}
		httputil.WriteJSON(w, http.StatusOK, teams)
	}
}

type createTeamRequest struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// handleCreateTeam handles POST /api/0/organizations/{org_slug}/teams/.
func handleCreateTeam(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}

		var body createTeamRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		body.Slug = strings.TrimSpace(body.Slug)
		body.Name = strings.TrimSpace(body.Name)
		if body.Slug == "" || body.Name == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Team slug and name are required.")
			return
		}

		org := PathParam(r, "org_slug")
		rec, err := admin.CreateTeam(r.Context(), org, body.Slug, body.Name)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create team.")
			return
		}
		if rec == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, &Team{ID: rec.ID, Slug: rec.Slug, Name: rec.Name, OrgID: rec.OrganizationID, DateCreated: rec.CreatedAt})
	}
}
