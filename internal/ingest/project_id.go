package ingest

import (
	"net/http"

	"urgentry/internal/auth"
)

func canonicalProjectID(r *http.Request) string {
	if key := auth.ProjectKeyFromContext(r.Context()); key != nil && key.ProjectID != "" {
		return key.ProjectID
	}
	if projectID := r.PathValue("project_id"); projectID != "" {
		return projectID
	}
	return "1"
}
