package web

import (
	"encoding/json"
	"net/http"

	"urgentry/internal/auth"
	"urgentry/internal/httputil"
	"urgentry/pkg/id"
)

// ---------------------------------------------------------------------------
// Starred/Pinned Projects API
// ---------------------------------------------------------------------------

// handleToggleStarProject handles POST /api/0/projects/{org}/{proj}/star/.
// It toggles the star status for the current user and project.
func handleToggleStarProject(h *Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.db == nil {
			httputil.WriteError(w, http.StatusServiceUnavailable, "Database unavailable.")
			return
		}

		principal := auth.PrincipalFromContext(r.Context())
		if principal == nil || principal.User == nil || principal.User.ID == "" {
			httputil.WriteError(w, http.StatusUnauthorized, "Authentication required.")
			return
		}
		userID := principal.User.ID

		orgSlug := r.PathValue("org")
		projSlug := r.PathValue("proj")

		// Resolve project ID from org/project slugs.
		var projectID string
		err := h.db.QueryRowContext(r.Context(),
			`SELECT p.id FROM projects p
			 JOIN organizations o ON o.id = p.organization_id
			 WHERE o.slug = ? AND p.slug = ?`,
			orgSlug, projSlug,
		).Scan(&projectID)
		if err != nil {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}

		// Check if already starred.
		var count int
		err = h.db.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM user_starred_projects WHERE user_id = ? AND project_id = ?`,
			userID, projectID,
		).Scan(&count)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to check star status.")
			return
		}

		starred := false
		if count > 0 {
			// Unstar.
			_, err = h.db.ExecContext(r.Context(),
				`DELETE FROM user_starred_projects WHERE user_id = ? AND project_id = ?`,
				userID, projectID,
			)
		} else {
			// Star.
			_, err = h.db.ExecContext(r.Context(),
				`INSERT INTO user_starred_projects (id, user_id, project_id) VALUES (?, ?, ?)`,
				id.New(), userID, projectID,
			)
			starred = true
		}
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to toggle star.")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"starred":    starred,
			"project_id": projectID,
		})
	}
}
