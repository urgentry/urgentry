package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/normalize"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"github.com/rs/zerolog/log"
)

// handleListProjectIssues handles GET /api/0/projects/{org_slug}/{proj_slug}/issues/.
func handleListProjectIssues(catalog controlplane.CatalogStore, reads controlplane.IssueReadStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")

		project, err := catalog.GetProject(r.Context(), org, proj)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve project.")
			return
		}
		if project == nil {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}
		rows, err := reads.SearchProjectIssues(r.Context(), project.ID, r.URL.Query().Get("filter"), r.URL.Query().Get("query"), 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list issues.")
			return
		}
		issues := make([]Issue, 0, len(rows))
		for _, row := range rows {
			issue := apiIssueFromWebIssue(row)
			issue.ProjectRef = ProjectRef{ID: project.ID, Slug: project.Slug}
			issues = append(issues, issue)
		}
		page := Paginate(w, r, issues)
		if page == nil {
			page = []Issue{}
		}
		httputil.WriteJSON(w, http.StatusOK, page)
	}
}

// handleGetIssue handles GET /api/0/issues/{issue_id}/.
func handleGetIssue(reads controlplane.IssueReadStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		id := PathParam(r, "issue_id")

		row, err := reads.GetIssue(r.Context(), id)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load issue.")
			return
		}
		if row == nil {
			httputil.WriteError(w, http.StatusNotFound, "Issue not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, apiIssueFromWebIssue(*row))
	}
}

// updateIssueRequest is the JSON body for updating an issue.
type updateIssueRequest struct {
	Status              string `json:"status"`
	AssignTo            string `json:"assignedTo"`
	ResolutionSubstatus string `json:"resolutionSubstatus"`
	ResolvedInRelease   string `json:"resolvedInRelease"`
}

// handleUpdateIssue handles PUT /api/0/issues/{issue_id}/.
func handleUpdateIssue(reads controlplane.IssueReadStore, issues controlplane.IssueWorkflowStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		id := PathParam(r, "issue_id")

		var body updateIssueRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}

		validStatuses := map[string]bool{
			"resolved":   true,
			"unresolved": true,
			"ignored":    true,
		}
		if body.Status != "" && !validStatuses[body.Status] {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid status value.")
			return
		}

		patch := store.IssuePatch{}
		if body.Status != "" {
			patch.Status = &body.Status
		}
		if body.AssignTo != "" {
			assign := strings.TrimSpace(body.AssignTo)
			patch.Assignee = &assign
		}
		if body.ResolutionSubstatus != "" || body.ResolvedInRelease != "" || body.Status == "unresolved" || body.Status == "ignored" || body.Status == "resolved" {
			substatus := strings.TrimSpace(body.ResolutionSubstatus)
			release := strings.TrimSpace(body.ResolvedInRelease)
			patch.ResolutionSubstatus = &substatus
			patch.ResolvedInRelease = &release
			if body.Status == "unresolved" || body.Status == "ignored" || (body.Status == "resolved" && substatus == "") {
				empty := ""
				patch.ResolutionSubstatus = &empty
				patch.ResolvedInRelease = &empty
			}
		}
		if patch.Status == nil && patch.Assignee == nil && patch.ResolutionSubstatus == nil && patch.ResolvedInRelease == nil {
			httputil.WriteError(w, http.StatusBadRequest, "No issue changes requested.")
			return
		}
		if err := issues.PatchIssue(r.Context(), id, patch); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update issue.")
			return
		}
		if principal := authPrincipalFromContext(r.Context()); principal != nil && principal.User != nil && body.Status != "" {
			summary := map[string]string{
				"resolved":   "Marked as resolved",
				"unresolved": "Reopened issue",
				"ignored":    "Ignored issue",
			}[body.Status]
			if body.ResolutionSubstatus == "next_release" {
				summary = "Marked to resolve in the next release"
			}
			if err := issues.RecordIssueActivity(r.Context(), id, principal.User.ID, "status", summary, ""); err != nil {
				log.Warn().
					Err(err).
					Str("group_id", id).
					Str("user_id", principal.User.ID).
					Msg("failed to record issue activity")
			}
		}
		iss, err := reads.GetIssue(r.Context(), id)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load updated issue.")
			return
		}
		if iss == nil {
			httputil.WriteError(w, http.StatusNotFound, "Issue not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, apiIssueFromWebIssue(*iss))
	}
}

type createIssueCommentRequest struct {
	Body string `json:"body"`
}

func handleListIssueComments(issues controlplane.IssueWorkflowStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		items, err := issues.ListIssueComments(r.Context(), PathParam(r, "issue_id"), 100)
		if err != nil {
			if err == store.ErrNotFound {
				httputil.WriteError(w, http.StatusNotFound, "Issue not found.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list issue comments.")
			return
		}
		resp := make([]IssueComment, 0, len(items))
		for _, item := range items {
			resp = append(resp, issueCommentFromStore(item))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleCreateIssueComment(issues controlplane.IssueWorkflowStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		principal := authPrincipalFromContext(r.Context())
		if principal == nil || principal.User == nil {
			httputil.WriteError(w, http.StatusUnauthorized, "Authentication required.")
			return
		}
		var body createIssueCommentRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		comment, err := issues.AddIssueComment(r.Context(), PathParam(r, "issue_id"), principal.User.ID, strings.TrimSpace(body.Body))
		if err != nil {
			if err == store.ErrNotFound {
				httputil.WriteError(w, http.StatusNotFound, "Issue not found.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to save comment.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, issueCommentFromStore(comment))
	}
}

func handleListIssueActivity(issues controlplane.IssueWorkflowStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		items, err := issues.ListIssueActivity(r.Context(), PathParam(r, "issue_id"), 100)
		if err != nil {
			if err == store.ErrNotFound {
				httputil.WriteError(w, http.StatusNotFound, "Issue not found.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list issue activity.")
			return
		}
		resp := make([]IssueActivity, 0, len(items))
		for _, item := range items {
			resp = append(resp, issueActivityFromStore(item))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleMergeIssue(issues controlplane.IssueWorkflowStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		principal := authPrincipalFromContext(r.Context())
		if principal == nil || principal.User == nil {
			httputil.WriteError(w, http.StatusUnauthorized, "Authentication required.")
			return
		}
		var body struct {
			TargetIssueID string `json:"targetIssueId"`
		}
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if err := issues.MergeIssue(r.Context(), PathParam(r, "issue_id"), strings.TrimSpace(body.TargetIssueID), principal.User.ID); err != nil {
			if err == store.ErrNotFound {
				httputil.WriteError(w, http.StatusNotFound, "Issue not found.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to merge issue.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func handleUnmergeIssue(issues controlplane.IssueWorkflowStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		principal := authPrincipalFromContext(r.Context())
		if principal == nil || principal.User == nil {
			httputil.WriteError(w, http.StatusUnauthorized, "Authentication required.")
			return
		}
		if err := issues.UnmergeIssue(r.Context(), PathParam(r, "issue_id"), principal.User.ID); err != nil {
			if err == store.ErrNotFound {
				httputil.WriteError(w, http.StatusNotFound, "Issue not found.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to unmerge issue.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// handleListIssueEvents handles GET /api/0/issues/{issue_id}/events/.
func handleListIssueEvents(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		id := PathParam(r, "issue_id")

		events, err := listIssueEventsFromDB(r, db, id)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list issue events.")
			return
		}
		page := Paginate(w, r, events)
		if page == nil {
			page = []*Event{}
		}
		httputil.WriteJSON(w, http.StatusOK, page)
	}
}

// handleGetLatestIssueEvent handles GET /api/0/issues/{issue_id}/events/latest/.
func handleGetLatestIssueEvent(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		id := PathParam(r, "issue_id")
		evt, err := getLatestIssueEventFromDB(r, db, id)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load latest event.")
			return
		}
		if evt == nil {
			httputil.WriteError(w, http.StatusNotFound, "No events found for this issue.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, evt)
	}
}

func listIssueEventsFromDB(r *http.Request, db *sql.DB, groupID string) ([]*Event, error) {
	rows, err := sqlite.ListGroupEvents(r.Context(), db, groupID, 100)
	if err != nil {
		return nil, err
	}
	return apiEventsFromWebEvents(rows), nil
}

func getLatestIssueEventFromDB(r *http.Request, db *sql.DB, groupID string) (*Event, error) {
	row, err := sqlite.GetLatestGroupEvent(r.Context(), db, groupID)
	if err == sql.ErrNoRows || row == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return apiEventFromWebEvent(*row), nil
}

func apiIssueFromWebIssue(row store.WebIssue) Issue {
	shortID := row.ID
	if row.ShortID > 0 {
		shortID = fmt.Sprintf("GENTRY-%d", row.ShortID)
	}
	return Issue{
		ID:                  row.ID,
		ShortID:             shortID,
		Title:               row.Title,
		Culprit:             row.Culprit,
		Level:               row.Level,
		Status:              row.Status,
		ResolutionSubstatus: row.ResolutionSubstatus,
		ResolvedInRelease:   row.ResolvedInRelease,
		MergedIntoIssueID:   row.MergedIntoGroupID,
		FirstSeen:           row.FirstSeen,
		LastSeen:            row.LastSeen,
		Count:               int(row.Count),
	}
}

func apiEventsFromWebEvents(rows []store.WebEvent) []*Event {
	events := make([]*Event, 0, len(rows))
	for _, row := range rows {
		events = append(events, apiEventFromWebEvent(row))
	}
	return events
}

func apiEventFromWebEvent(row store.WebEvent) *Event {
	resolvedFrames, unresolvedFrames := normalize.CountNativeFrames(row.NormalizedJSON)
	return &Event{
		ID:               row.EventID,
		EventID:          row.EventID,
		IssueID:          row.GroupID,
		Title:            row.Title,
		Message:          row.Message,
		Level:            row.Level,
		Platform:         row.Platform,
		Culprit:          row.Culprit,
		ProcessingStatus: string(row.ProcessingStatus),
		IngestError:      row.IngestError,
		ResolvedFrames:   resolvedFrames,
		UnresolvedFrames: unresolvedFrames,
		DateCreated:      row.Timestamp,
		Tags:             row.Tags,
	}
}

func issueCommentFromStore(item store.IssueComment) IssueComment {
	return IssueComment{
		ID:          item.ID,
		IssueID:     item.GroupID,
		ProjectID:   item.ProjectID,
		UserID:      item.UserID,
		UserEmail:   item.UserEmail,
		UserName:    item.UserName,
		Body:        item.Body,
		DateCreated: item.DateCreated,
	}
}

func issueActivityFromStore(item store.IssueActivityEntry) IssueActivity {
	return IssueActivity{
		ID:          item.ID,
		IssueID:     item.GroupID,
		ProjectID:   item.ProjectID,
		UserID:      item.UserID,
		UserEmail:   item.UserEmail,
		UserName:    item.UserName,
		Kind:        item.Kind,
		Summary:     item.Summary,
		Details:     item.Details,
		DateCreated: item.DateCreated,
	}
}

func authPrincipalFromContext(ctx context.Context) *auth.Principal {
	return auth.PrincipalFromContext(ctx)
}
