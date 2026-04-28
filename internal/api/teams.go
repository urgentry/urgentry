package api

import (
	"net/http"
	"strings"
	"time"

	authpkg "urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
)

// teamProjectResponse is the Sentry-compatible team project shape.
type teamProjectResponse struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Platform    string    `json:"platform,omitempty"`
	Status      string    `json:"status,omitempty"`
	DateCreated time.Time `json:"dateCreated"`
}

// handleListTeamProjects handles GET /api/0/teams/{org_slug}/{team_slug}/projects/.
func handleListTeamProjects(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		team := PathParam(r, "team_slug")

		projects, err := admin.ListTeamProjects(r.Context(), org, team)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list team projects.")
			return
		}
		out := make([]teamProjectResponse, 0, len(projects))
		for _, p := range projects {
			out = append(out, teamProjectResponse{
				ID:          p.ID,
				Slug:        p.Slug,
				Name:        p.Name,
				Platform:    p.Platform,
				Status:      p.Status,
				DateCreated: p.DateCreated,
			})
		}
		httputil.WriteJSON(w, http.StatusOK, out)
	}
}

// handleListUserTeams handles GET /api/0/organizations/{org_slug}/user-teams/.
func handleListUserTeams(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		principal := authpkg.PrincipalFromContext(r.Context())
		if principal == nil || principal.User == nil {
			httputil.WriteError(w, http.StatusUnauthorized, "Authentication required.")
			return
		}
		teams, err := admin.ListUserTeams(r.Context(), org, principal.User.ID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list user teams.")
			return
		}
		out := make([]teamDetailResponse, 0, len(teams))
		for _, t := range teams {
			out = append(out, teamDetailResponse{
				ID:          t.ID,
				Slug:        t.Slug,
				Name:        t.Name,
				DateCreated: t.CreatedAt,
				IsMember:    true,
				Avatar:      teamAvatar{Type: "letter_avatar"},
				HasAccess:   true,
				IsPending:   false,
			})
		}
		httputil.WriteJSON(w, http.StatusOK, out)
	}
}

// handleAddMemberToTeam handles POST /api/0/organizations/{org_slug}/members/{member_id}/teams/{team_slug}/.
func handleAddMemberToTeam(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		memberID := PathParam(r, "member_id")
		team := PathParam(r, "team_slug")

		rec, err := admin.AddMemberToTeamByMemberID(r.Context(), org, memberID, team)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to add member to team.")
			return
		}
		if rec == nil {
			httputil.WriteError(w, http.StatusNotFound, "Member, organization, or team not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, &Member{
			ID:             rec.ID,
			UserID:         rec.UserID,
			OrganizationID: rec.OrganizationID,
			TeamID:         rec.TeamID,
			Email:          rec.Email,
			Name:           rec.Name,
			Role:           rec.Role,
			DateCreated:    rec.CreatedAt,
		})
	}
}

// handleRemoveMemberFromTeam handles DELETE /api/0/organizations/{org_slug}/members/{member_id}/teams/{team_slug}/.
func handleRemoveMemberFromTeam(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		memberID := PathParam(r, "member_id")
		team := PathParam(r, "team_slug")

		ok, err := admin.RemoveMemberFromTeamByMemberID(r.Context(), org, memberID, team)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to remove member from team.")
			return
		}
		if !ok {
			httputil.WriteError(w, http.StatusNotFound, "Team membership not found.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// teamDetailResponse is the Sentry-compatible team detail shape.
type teamDetailResponse struct {
	ID           string                `json:"id"`
	Slug         string                `json:"slug"`
	Name         string                `json:"name"`
	DateCreated  time.Time             `json:"dateCreated"`
	IsMember     bool                  `json:"isMember"`
	MemberCount  int                   `json:"memberCount"`
	ProjectCount int                   `json:"projectCount,omitempty"`
	Avatar       teamAvatar            `json:"avatar"`
	HasAccess    bool                  `json:"hasAccess"`
	IsPending    bool                  `json:"isPending"`
	Organization *teamOrgEmbed         `json:"organization,omitempty"`
	Projects     []teamProjectResponse `json:"projects,omitempty"`
}

// teamOrgEmbed is a compact organization reference embedded in team detail.
type teamOrgEmbed struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
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
			writeDecodeJSONError(w, err)
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
func handleGetTeamDetail(catalog controlplane.CatalogStore, admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		team := PathParam(r, "team_slug")

		rec, memberCount, projectCount, err := admin.GetTeam(r.Context(), orgSlug, team)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load team.")
			return
		}
		if rec == nil {
			httputil.WriteError(w, http.StatusNotFound, "Team not found.")
			return
		}

		resp := &teamDetailResponse{
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
		}

		// Embed organization reference.
		if orgRec, err := catalog.GetOrganization(r.Context(), orgSlug); err == nil && orgRec != nil {
			resp.Organization = &teamOrgEmbed{
				ID:   orgRec.ID,
				Slug: orgRec.Slug,
				Name: orgRec.Name,
			}
		}

		// Embed projects associated with this team.
		if teamProjects, err := admin.ListTeamProjects(r.Context(), orgSlug, team); err == nil {
			projs := make([]teamProjectResponse, 0, len(teamProjects))
			for _, p := range teamProjects {
				projs = append(projs, teamProjectResponse{
					ID:          p.ID,
					Slug:        p.Slug,
					Name:        p.Name,
					Platform:    p.Platform,
					Status:      p.Status,
					DateCreated: p.DateCreated,
				})
			}
			resp.Projects = projs
		}

		httputil.WriteJSON(w, http.StatusOK, resp)
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
			writeDecodeJSONError(w, err)
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
