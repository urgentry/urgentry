package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

// Keep imports that are used by concurrent edits.
var (
	_ = fmt.Sprintf
	_ = strconv.Itoa
	_ = strings.TrimSpace
)

// handleListOrgEvents handles GET /api/0/organizations/{org_slug}/events/.
// Returns events across all projects in the organization (Discover events endpoint).
func handleListOrgEvents(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		query := strings.TrimSpace(r.URL.Query().Get("query"))
		sortField := strings.TrimSpace(r.URL.Query().Get("sort"))
		if sortField == "" {
			sortField = "-timestamp"
		}
		limit := discoverLimit(r, 100)

		rows, err := sqlite.ListOrgEvents(r.Context(), db, org, query, sortField, limit)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list organization events.")
			return
		}
		data := make([]OrgEventRow, 0, len(rows))
		for _, row := range rows {
			data = append(data, OrgEventRow{
				ID:          row.EventID,
				Title:       row.Title,
				Message:     row.Message,
				Level:       row.Level,
				Platform:    row.Platform,
				Culprit:     row.Culprit,
				ProjectName: row.ProjectName,
				Timestamp:   row.Timestamp,
				Tags:        apiEventTags(row.Tags),
			})
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"data": data})
	}
}

// handleListProjectEvents handles GET /api/0/projects/{org_slug}/{proj_slug}/events/.
func handleListProjectEvents(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")

		projectID, err := projectIDFromSlugs(r, db, org, proj)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve project.")
			return
		}
		if projectID == "" {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}
		events, err := listRecentEventsFromDB(r, db, projectID, 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list events.")
			return
		}
		page := Paginate(w, r, events)
		if page == nil {
			page = []*Event{}
		}
		httputil.WriteJSON(w, http.StatusOK, page)
	}
}

// handleGetProjectEvent handles GET /api/0/projects/{org_slug}/{proj_slug}/events/{event_id}/.
func handleGetProjectEvent(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")
		eventID := PathParam(r, "event_id")

		projectID, err := projectIDFromSlugs(r, db, org, proj)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve project.")
			return
		}
		if projectID == "" {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}
		evt, err := getEventFromDB(r, db, projectID, eventID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load event.")
			return
		}
		if evt == nil {
			httputil.WriteError(w, http.StatusNotFound, "Event not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, evt)
	}
}

// ---------------------------------------------------------------------------
// SQLite query helpers for events
// ---------------------------------------------------------------------------

func listRecentEventsFromDB(r *http.Request, db *sql.DB, projectID string, limit int) ([]*Event, error) {
	rows, err := sqlite.ListProjectEvents(r.Context(), db, projectID, limit)
	if err != nil {
		return nil, err
	}
	return apiEventsFromWebEvents(rows), nil
}

func getEventFromDB(r *http.Request, db *sql.DB, projectID, eventID string) (*Event, error) {
	row, err := sqlite.GetProjectEvent(r.Context(), db, projectID, eventID)
	if err == sql.ErrNoRows || row == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return apiEventFromWebEvent(*row), nil
}

// handleResolveEventID handles GET /api/0/organizations/{org_slug}/eventids/{event_id}/.
// Given an event_id, returns the org slug, project slug, group ID, and event details.
func handleResolveEventID(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		eventID := PathParam(r, "event_id")

		resolved, err := sqlite.ResolveEventID(r.Context(), db, orgSlug, eventID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve event ID.")
			return
		}
		if resolved == nil {
			httputil.WriteError(w, http.StatusNotFound, "Event not found.")
			return
		}
		evt := apiEventFromWebEvent(resolved.Event)
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"organizationSlug": resolved.OrgSlug,
			"projectSlug":      resolved.ProjectSlug,
			"groupId":          resolved.GroupID,
			"eventId":          resolved.EventID,
			"event":            evt,
		})
	}
}

// handleResolveShortID handles GET /api/0/organizations/{org_slug}/shortids/{short_id}/.
// Given a short ID like "GENTRY-42", returns the full issue.
func handleResolveShortID(db *sql.DB, issues controlplane.IssueWorkflowStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		raw := PathParam(r, "short_id")

		// Parse the short ID. Accept "GENTRY-42" or plain "42".
		numStr := raw
		if idx := strings.LastIndex(raw, "-"); idx >= 0 {
			numStr = raw[idx+1:]
		}
		shortID, err := strconv.Atoi(numStr)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid short ID format.")
			return
		}

		issue, projectSlug, err := sqlite.ResolveShortID(r.Context(), db, orgSlug, shortID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve short ID.")
			return
		}
		if issue == nil {
			httputil.WriteError(w, http.StatusNotFound, "Issue not found.")
			return
		}

		extras := loadIssueResponseExtras(r.Context(), db, issues, principalUserID(authPrincipalFromContext(r.Context())), []store.WebIssue{*issue})
		apiIssue := apiIssueFromWebIssueWithExtras(*issue, extras[issue.ID])
		apiIssue.ShortID = fmt.Sprintf("GENTRY-%d", issue.ShortID)
		apiIssue.ProjectRef, err = projectRefForIssue(r.Context(), db, issue.ID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve short ID.")
			return
		}
		if apiIssue.ProjectRef.Slug == "" {
			apiIssue.ProjectRef.Slug = projectSlug
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"organizationSlug": orgSlug,
			"projectSlug":      projectSlug,
			"group":            apiIssue,
		})
	}
}
