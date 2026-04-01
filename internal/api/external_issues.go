package api

import (
	"database/sql"
	"net/http"
	"time"

	"urgentry/internal/httputil"
)

// externalIssueResponse is the JSON shape for an external issue link.
type externalIssueResponse struct {
	ID            string    `json:"id"`
	GroupID       string    `json:"groupId"`
	IntegrationID string    `json:"integrationId"`
	Key           string    `json:"key"`
	Title         string    `json:"title"`
	URL           string    `json:"url"`
	Description   string    `json:"description"`
	DateCreated   time.Time `json:"dateCreated"`
}

// handleListExternalIssues handles GET /api/0/organizations/{org_slug}/issues/{issue_id}/external-issues/.
// Returns external issue links (Jira, GitHub, etc.) for the given issue.
func handleListExternalIssues(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		issueID := PathParam(r, "issue_id")

		rows, err := db.QueryContext(r.Context(),
			`SELECT id, group_id, integration_id, key, title, url, description, created_at
			 FROM group_external_issues
			 WHERE group_id = ?
			 ORDER BY created_at DESC`,
			issueID,
		)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list external issues.")
			return
		}
		defer rows.Close()

		result := make([]externalIssueResponse, 0)
		for rows.Next() {
			var ei externalIssueResponse
			var createdAt sql.NullString
			if err := rows.Scan(&ei.ID, &ei.GroupID, &ei.IntegrationID, &ei.Key, &ei.Title, &ei.URL, &ei.Description, &createdAt); err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to scan external issue.")
				return
			}
			if createdAt.Valid {
				ei.DateCreated, _ = time.Parse(time.RFC3339, createdAt.String)
			}
			result = append(result, ei)
		}
		if err := rows.Err(); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list external issues.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, result)
	}
}
