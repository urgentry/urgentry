package api

import (
	"net/http"
	"strings"
	"time"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
)

// teamDetailResponse is the Sentry-compatible team detail shape.
type teamDetailResponse struct {
	ID           string         `json:"id"`
	Slug         string         `json:"slug"`
	Name         string         `json:"name"`
	DateCreated  time.Time      `json:"dateCreated"`
	IsMember     bool           `json:"isMember"`
	MemberCount  int            `json:"memberCount"`
	ProjectCount int            `json:"projectCount,omitempty"`
	Avatar       teamAvatar     `json:"avatar"`
	HasAccess    bool           `json:"hasAccess"`
	IsPending    bool           `json:"isPending"`
}

type teamAvatar struct {
	Type string `json:"type"`
}

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

// handleGetTeamDetail handles GET /api/0/teams/{org_slug}/{team_slug}/.
func handleGetTeamDetail(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		team := PathParam(r, "team_slug")

		rec, memberCount, projectCount, err := admin.GetTeam(r.Context(), org, team)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load team.")
			return
		}
		if rec == nil {
			httputil.WriteError(w, http.StatusNotFound, "Team not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, &teamDetailResponse{
			ID:           rec.ID,
			Slug:         rec.Slug,
			Name:         rec.Name,
			DateCreated:  rec.CreatedAt,
			IsMember:     true,
			MemberCount:  memberCount,
			ProjectCount: projectCount,
			Avatar:       teamAvatar{Type: "letter_avatar"},
			HasAccess:    true,
			IsPending:    false,
		})
	}
}

type updateTeamRequest struct {
	Name *string `json:"name"`
	Slug *string `json:"slug"`
}

// handleUpdateTeam handles PUT /api/0/teams/{org_slug}/{team_slug}/.
func handleUpdateTeam(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}

		var body updateTeamRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if body.Name == nil && body.Slug == nil {
			httputil.WriteError(w, http.StatusBadRequest, "At least one of name or slug is required.")
			return
		}

		org := PathParam(r, "org_slug")
		team := PathParam(r, "team_slug")

		rec, err := admin.UpdateTeam(r.Context(), org, team, body.Name, body.Slug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update team.")
			return
		}
		if rec == nil {
			httputil.WriteError(w, http.StatusNotFound, "Team not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, &teamDetailResponse{
			ID:          rec.ID,
			Slug:        rec.Slug,
			Name:        rec.Name,
			DateCreated: rec.CreatedAt,
			IsMember:    true,
			Avatar:      teamAvatar{Type: "letter_avatar"},
			HasAccess:   true,
			IsPending:   false,
		})
	}
}

// handleDeleteTeam handles DELETE /api/0/teams/{org_slug}/{team_slug}/.
func handleDeleteTeam(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		team := PathParam(r, "team_slug")

		deleted, err := admin.DeleteTeam(r.Context(), org, team)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete team.")
			return
		}
		if !deleted {
			httputil.WriteError(w, http.StatusNotFound, "Team not found.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
