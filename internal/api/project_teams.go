package api

import (
	"net/http"
	"time"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
)

// projectTeamResponse is the Sentry-compatible shape for a project's team.
type projectTeamResponse struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	DateCreated time.Time `json:"dateCreated"`
}

// handleListProjectTeams handles GET /api/0/projects/{org}/{proj}/teams/.
func handleListProjectTeams(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")

		teams, err := catalog.ListProjectTeams(r.Context(), org, proj)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list project teams.")
			return
		}
		if teams == nil {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}

		out := make([]projectTeamResponse, 0, len(teams))
		for _, t := range teams {
			out = append(out, projectTeamResponse{
				ID:          t.ID,
				Slug:        t.Slug,
				Name:        t.Name,
				DateCreated: t.DateCreated,
			})
		}
		httputil.WriteJSON(w, http.StatusOK, out)
	}
}

// handleAddProjectTeam handles POST /api/0/projects/{org}/{proj}/teams/{team_slug}/.
func handleAddProjectTeam(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")
		teamSlug := PathParam(r, "team_slug")

		team, err := catalog.AddProjectTeam(r.Context(), org, proj, teamSlug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to add team to project.")
			return
		}
		if team == nil {
			httputil.WriteError(w, http.StatusNotFound, "Project or team not found.")
			return
		}

		httputil.WriteJSON(w, http.StatusCreated, &projectTeamResponse{
			ID:          team.ID,
			Slug:        team.Slug,
			Name:        team.Name,
			DateCreated: team.DateCreated,
		})
	}
}

// handleRemoveProjectTeam handles DELETE /api/0/projects/{org}/{proj}/teams/{team_slug}/.
func handleRemoveProjectTeam(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")
		teamSlug := PathParam(r, "team_slug")

		removed, err := catalog.RemoveProjectTeam(r.Context(), org, proj, teamSlug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to remove team from project.")
			return
		}
		if !removed {
			httputil.WriteError(w, http.StatusNotFound, "Team not associated with project.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
