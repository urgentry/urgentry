package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	authpkg "urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/integration"
)

type externalIssueUpsertRequest struct {
	IssueID    json.RawMessage `json:"issueId"`
	WebURL     string          `json:"webUrl"`
	Project    string          `json:"project"`
	Identifier string          `json:"identifier"`
}

type externalIssueLinkResponse struct {
	ID          string `json:"id"`
	IssueID     string `json:"issueId"`
	ServiceType string `json:"serviceType"`
	DisplayName string `json:"displayName"`
	WebURL      string `json:"webUrl"`
}

// handleListExternalIssues handles GET /api/0/organizations/{org_slug}/issues/{issue_id}/external-issues/.
func handleListExternalIssues(store integration.ExternalIssueStore, authorize authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authorize(w, r) {
			return
		}
		items, err := store.ListByGroup(r.Context(), PathParam(r, "issue_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list external issues.")
			return
		}
		result := make([]externalIssueLinkResponse, 0, len(items))
		for _, item := range items {
			result = append(result, externalIssueLinkResponse{
				ID:          item.ID,
				IssueID:     item.GroupID,
				ServiceType: item.IntegrationID,
				DisplayName: item.Title,
				WebURL:      item.URL,
			})
		}
		httputil.WriteJSON(w, http.StatusOK, result)
	}
}

func handleUpsertInstallationExternalIssue(db *sql.DB, catalog controlplane.CatalogStore, authorizer *authpkg.Authorizer, installations integration.Store, issues integration.ExternalIssueStore, authorize authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authorize(w, r) {
			return
		}

		var body externalIssueUpsertRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		issueID := parseFlexibleID(body.IssueID)
		projectName := strings.TrimSpace(body.Project)
		identifier := strings.TrimSpace(body.Identifier)
		webURL := strings.TrimSpace(body.WebURL)
		if issueID == "" || projectName == "" || identifier == "" || webURL == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing required external issue fields.")
			return
		}
		if err := authorizer.AuthorizeIssue(r, issueID, authpkg.ScopeIssueWrite); err != nil {
			httputil.WriteError(w, http.StatusForbidden, "You do not have permission to perform this action.")
			return
		}

		_, orgSlug, err := projectScopeForGroup(r.Context(), db, issueID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve issue scope.")
			return
		}
		if orgSlug == "" {
			httputil.WriteError(w, http.StatusNotFound, "Issue not found.")
			return
		}
		org, err := catalog.GetOrganization(r.Context(), orgSlug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve organization.")
			return
		}
		if org == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}

		installationID := PathParam(r, "uuid")
		installation, err := installations.Get(r.Context(), installationID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load Sentry app installation.")
			return
		}
		if installation == nil || installation.OrganizationID != org.ID || strings.TrimSpace(installation.Status) != "active" {
			httputil.WriteError(w, http.StatusNotFound, "Sentry app installation not found.")
			return
		}

		displayName := projectName + "#" + identifier
		link := &integration.ExternalIssueLink{
			InstallationID: installation.ID,
			GroupID:        issueID,
			IntegrationID:  installation.IntegrationID,
			Key:            displayName,
			Title:          displayName,
			URL:            webURL,
		}
		if err := issues.Upsert(r.Context(), link); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create external issue.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, externalIssueLinkResponse{
			ID:          link.ID,
			IssueID:     issueID,
			ServiceType: installation.IntegrationID,
			DisplayName: displayName,
			WebURL:      webURL,
		})
	}
}

func handleDeleteInstallationExternalIssue(authorizer *authpkg.Authorizer, issues integration.ExternalIssueStore, authorize authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authorize(w, r) {
			return
		}
		installationID := PathParam(r, "uuid")
		externalIssueID := PathParam(r, "external_issue_id")
		link, err := issues.GetByInstallation(r.Context(), installationID, externalIssueID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load external issue.")
			return
		}
		if link == nil {
			httputil.WriteError(w, http.StatusNotFound, "External issue not found.")
			return
		}
		if err := authorizer.AuthorizeIssue(r, link.GroupID, authpkg.ScopeIssueWrite); err != nil {
			httputil.WriteError(w, http.StatusForbidden, "You do not have permission to perform this action.")
			return
		}
		if err := issues.Delete(r.Context(), installationID, externalIssueID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete external issue.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func parseFlexibleID(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return strings.TrimSpace(number.String())
	}
	return ""
}
