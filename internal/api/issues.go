package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

type issueWorkflowStateReader interface {
	GetIssueWorkflowState(ctx context.Context, groupID, userID string) (store.IssueWorkflowState, error)
}

type issueWorkflowBatchStateReader interface {
	BatchIssueWorkflowStates(ctx context.Context, groupIDs []string, userID string) (map[string]store.IssueWorkflowState, error)
}

type issueWorkflowCommentCounter interface {
	BatchIssueCommentCounts(ctx context.Context, groupIDs []string) (map[string]int, error)
}

type issueResponseExtras struct {
	AssignedTo   *IssueUser
	HasSeen      bool
	IsBookmarked bool
	IsPublic     bool
	IsSubscribed bool
	Priority     int
	Substatus    string
	Metadata     Metadata
	Type         string
	NumComments  int
	UserCount    int
	Stats        IssueStats
}

type eventResponseExtras struct {
	Entries      []EventEntry
	Contexts     Metadata
	SDK          Metadata
	User         Metadata
	Fingerprints []string
	Errors       []Metadata
	Packages     Metadata
	Measurements Metadata
}

type storedEventPayload struct {
	Request      Metadata          `json:"request,omitempty"`
	Exception    Metadata          `json:"exception,omitempty"`
	Breadcrumbs  Metadata          `json:"breadcrumbs,omitempty"`
	Contexts     Metadata          `json:"contexts,omitempty"`
	SDK          Metadata          `json:"sdk,omitempty"`
	User         Metadata          `json:"user,omitempty"`
	Fingerprint  []string          `json:"fingerprint,omitempty"`
	Errors       []Metadata        `json:"errors,omitempty"`
	Packages     Metadata          `json:"packages,omitempty"`
	Modules      map[string]string `json:"modules,omitempty"`
	Measurements Metadata          `json:"measurements,omitempty"`
	Dist         string            `json:"dist,omitempty"`
	Release      string            `json:"release,omitempty"`
}

// handleListProjectIssues handles GET /api/0/projects/{org_slug}/{proj_slug}/issues/.
func handleListProjectIssues(db *sql.DB, catalog controlplane.CatalogStore, reads controlplane.IssueReadStore, issues controlplane.IssueWorkflowStore, auth authFunc) http.HandlerFunc {
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
		// Parse pagination before querying the DB so we only fetch and
		// hydrate the rows that will be returned to the client.
		paging := ParsePagination(r)
		// Request one extra row to detect whether a next page exists
		// without a separate COUNT query.
		rows, err := reads.SearchProjectIssuesPaged(r.Context(), project.ID, r.URL.Query().Get("filter"), r.URL.Query().Get("query"), paging.Limit+1, paging.Offset)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list issues.")
			return
		}
		// Trim to the actual page and set Link headers.
		rows = SetPaginationHeaders(w, r, rows, paging)
		extras := loadIssueResponseExtras(r.Context(), db, issues, principalUserID(authPrincipalFromContext(r.Context())), rows)
		page := make([]Issue, 0, len(rows))
		for _, row := range rows {
			issue := apiIssueFromWebIssueWithExtras(row, extras[row.ID])
			issue.ProjectRef = apiProjectRefFromProject(project)
			finalizeIssueResponse(&issue, org)
			page = append(page, issue)
		}
		httputil.WriteJSON(w, http.StatusOK, page)
	}
}

// handleGetIssue handles GET /api/0/issues/{issue_id}/.
func handleGetIssue(db *sql.DB, reads controlplane.IssueReadStore, issues controlplane.IssueWorkflowStore, auth authFunc) http.HandlerFunc {
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
		issue, err := loadIssueResponse(r.Context(), db, issues, principalUserID(authPrincipalFromContext(r.Context())), *row)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load issue project.")
			return
		}
		enrichIssueDetail(r.Context(), db, issues, &issue, id)
		httputil.WriteJSON(w, http.StatusOK, issue)
	}
}

// enrichIssueDetail loads activity, tags, releases, and participant info for issue detail.
func enrichIssueDetail(ctx context.Context, db *sql.DB, issues controlplane.IssueWorkflowStore, issue *Issue, groupID string) {
	// Activity
	if activities, err := issues.ListIssueActivity(ctx, groupID, 100); err == nil {
		issue.Activity = make([]IssueActivitySummary, 0, len(activities))
		for _, a := range activities {
			issue.Activity = append(issue.Activity, IssueActivitySummary{
				ID:          a.ID,
				Type:        a.Kind,
				DateCreated: a.DateCreated,
				Data:        map[string]string{"summary": a.Summary},
			})
		}
	}

	// Tags (top tag facets from events)
	issue.Tags = loadIssueTagFacets(ctx, db, groupID)

	// First/last release
	issue.FirstRelease = loadIssueRelease(ctx, db, groupID, true)
	issue.LastRelease = loadIssueRelease(ctx, db, groupID, false)

	// User report count
	var reportCount int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_feedback WHERE group_id = ?`, groupID).Scan(&reportCount)
	issue.UserReportCount = reportCount
	if issue.Activity == nil {
		issue.Activity = []IssueActivitySummary{}
	}
	if issue.Tags == nil {
		issue.Tags = []IssueTagFacet{}
	}
	if issue.Participants == nil {
		issue.Participants = []IssueUser{}
	}
	if issue.SeenBy == nil {
		issue.SeenBy = []IssueUser{}
	}
}

func loadIssueTagFacets(ctx context.Context, db *sql.DB, groupID string) []IssueTagFacet {
	rows, err := db.QueryContext(ctx, `
		SELECT tags_json FROM events WHERE group_id = ? LIMIT 200`, groupID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	// Aggregate tag key → value → count across events.
	type valCount struct{ count int }
	facetCounts := map[string]map[string]*valCount{}
	for rows.Next() {
		var tagsJSON sql.NullString
		if err := rows.Scan(&tagsJSON); err != nil || !tagsJSON.Valid {
			continue
		}
		var tags map[string]string
		if err := json.Unmarshal([]byte(tagsJSON.String), &tags); err != nil {
			continue
		}
		for k, v := range tags {
			if facetCounts[k] == nil {
				facetCounts[k] = map[string]*valCount{}
			}
			vc, ok := facetCounts[k][v]
			if !ok {
				vc = &valCount{}
				facetCounts[k][v] = vc
			}
			vc.count++
		}
	}
	result := make([]IssueTagFacet, 0, len(facetCounts))
	for key, vals := range facetCounts {
		f := IssueTagFacet{Key: key, Name: key, TotalValues: len(vals)}
		for v, vc := range vals {
			if len(f.TopValues) < 5 {
				f.TopValues = append(f.TopValues, IssueTagVal{Value: v, Name: v, Count: vc.count})
			}
		}
		result = append(result, f)
	}
	return result
}

func loadIssueRelease(ctx context.Context, db *sql.DB, groupID string, first bool) *IssueRelease {
	order := "ASC"
	if !first {
		order = "DESC"
	}
	var version string
	var dateCreated time.Time
	err := db.QueryRowContext(ctx, `
		SELECT DISTINCT e.release, e.occurred_at
		FROM events e
		WHERE e.group_id = ? AND e.release != ''
		ORDER BY e.occurred_at `+order+`
		LIMIT 1`, groupID).Scan(&version, &dateCreated)
	if err != nil || version == "" {
		return nil
	}
	return &IssueRelease{Version: version, DateCreated: dateCreated}
}

// updateIssueRequest is the JSON body for updating an issue.
type updateIssueRequest struct {
	Status              string `json:"status"`
	AssignTo            string `json:"assignedTo"`
	ResolutionSubstatus string `json:"resolutionSubstatus"`
	ResolvedInRelease   string `json:"resolvedInRelease"`
	HasSeen             *bool  `json:"hasSeen"`
	IsBookmarked        *bool  `json:"isBookmarked"`
	IsPublic            *bool  `json:"isPublic"`
	IsSubscribed        *bool  `json:"isSubscribed"`
	Priority            *int   `json:"priority"`
	Substatus           string `json:"substatus"`
	Discard             *bool  `json:"discard"`
	Merge               *bool  `json:"merge"`
	MergeTarget         string `json:"mergeTarget"`
}

var validIssueStatuses = map[string]bool{
	"resolved":   true,
	"unresolved": true,
	"ignored":    true,
}

func validateIssueStatus(status string) bool {
	return status == "" || validIssueStatuses[status]
}

func issuePatchHasChanges(patch store.IssuePatch) bool {
	return patch.Status != nil ||
		patch.Assignee != nil ||
		patch.ResolutionSubstatus != nil ||
		patch.ResolvedInRelease != nil ||
		patch.Priority != nil
}

func buildUpdateIssuePatch(body updateIssueRequest) (store.IssuePatch, error) {
	patch := store.IssuePatch{}
	if !validateIssueStatus(body.Status) {
		return patch, errors.New("invalid status value")
	}
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
	if body.Substatus != "" {
		sub := strings.TrimSpace(body.Substatus)
		patch.ResolutionSubstatus = &sub
	}
	if body.Priority != nil {
		if *body.Priority < 0 || *body.Priority > 4 {
			return patch, errors.New("priority must be 0-4")
		}
		patch.Priority = body.Priority
	}
	return patch, nil
}

// handleUpdateIssue handles PUT /api/0/issues/{issue_id}/.
func handleUpdateIssue(db *sql.DB, reads controlplane.IssueReadStore, issues controlplane.IssueWorkflowStore, hooks *sqlite.HookStore, auth authFunc) http.HandlerFunc {
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
		// hasSeen is accepted silently — it's a per-user UI state marker.
		patch, patchErr := buildUpdateIssuePatch(body)
		if patchErr != nil {
			httputil.WriteError(w, http.StatusBadRequest, patchErr.Error())
			return
		}

		// Process bookmark/subscription/seen toggles.
		principal := authPrincipalFromContext(r.Context())
		userID := principalUserID(principal)

		hasSideEffects := body.IsBookmarked != nil || body.IsSubscribed != nil || body.Discard != nil || body.Merge != nil
		if !issuePatchHasChanges(patch) && !hasSideEffects {
			httputil.WriteError(w, http.StatusBadRequest, "No issue changes requested.")
			return
		}

		// Handle discard: mark issue as ignored and delete future events.
		if body.Discard != nil && *body.Discard {
			discardStatus := "ignored"
			patch.Status = &discardStatus
			if err := issues.DeleteGroup(r.Context(), id); err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to discard issue.")
				return
			}
			httputil.WriteJSON(w, http.StatusOK, map[string]any{"discard": true})
			return
		}

		// Handle merge: merge this issue into a target.
		if body.Merge != nil && *body.Merge && body.MergeTarget != "" {
			if err := issues.MergeIssue(r.Context(), id, body.MergeTarget, userID); err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to merge issue.")
				return
			}
			httputil.WriteJSON(w, http.StatusOK, map[string]any{"merge": map[string]string{"parent": body.MergeTarget, "children": id}})
			return
		}

		if body.IsBookmarked != nil && userID != "" {
			if err := issues.ToggleIssueBookmark(r.Context(), id, userID, *body.IsBookmarked); err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to toggle bookmark.")
				return
			}
		}
		if body.IsSubscribed != nil && userID != "" {
			if err := issues.ToggleIssueSubscription(r.Context(), id, userID, *body.IsSubscribed); err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to toggle subscription.")
				return
			}
		}
		var resolveTransitions []string
		if body.Status == "resolved" {
			resolveTransitions = issueIDsNeedingResolvedHook(r.Context(), reads, []string{id})
		}
		if err := issues.PatchIssue(r.Context(), id, patch); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update issue.")
			return
		}
		if principal != nil && principal.User != nil && body.Status != "" {
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
		if principal != nil && principal.User != nil && body.Priority != nil {
			summary := "Changed priority"
			if err := issues.RecordIssueActivity(r.Context(), id, principal.User.ID, "priority", summary, ""); err != nil {
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
		if len(resolveTransitions) > 0 {
			fireResolvedIssueHooks(r.Context(), hooks, reads, db, "", resolveTransitions)
		}
		issue, err := loadIssueResponse(r.Context(), db, issues, userID, *iss)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load issue project.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, issue)
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
			if err == store.ErrInvalidIssueWorkflow {
				httputil.WriteError(w, http.StatusBadRequest, "Invalid merge target.")
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
			if err == store.ErrInvalidIssueWorkflow {
				httputil.WriteError(w, http.StatusBadRequest, "Issue is not merged.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to unmerge issue.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// handleDeleteIssue handles DELETE /api/0/issues/{issue_id}/.
// Cascade-deletes events, attachments, and group assignments.
func handleDeleteIssue(issues controlplane.IssueWorkflowStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		id := PathParam(r, "issue_id")
		if err := issues.DeleteGroup(r.Context(), id); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete issue.")
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

func withOrgIssueScope(db *sql.DB, auth authFunc, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		ok, err := issueBelongsToOrganization(r.Context(), db, PathParam(r, "org_slug"), PathParam(r, "issue_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load issue.")
			return
		}
		if !ok {
			httputil.WriteError(w, http.StatusNotFound, "Issue not found.")
			return
		}
		next.ServeHTTP(w, r)
	}
}

func allowAllAuth(http.ResponseWriter, *http.Request) bool { return true }

func issueBelongsToOrganization(ctx context.Context, db *sql.DB, orgSlug, issueID string) (bool, error) {
	if db == nil {
		return false, nil
	}
	var exists int
	err := db.QueryRowContext(ctx, `
		SELECT 1
		FROM groups g
		JOIN projects p ON p.id = g.project_id
		JOIN organizations o ON o.id = p.organization_id
		WHERE g.id = ? AND o.slug = ?`,
		issueID, orgSlug,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// bulkMutateRequest is the JSON body for bulk-mutating org issues.
type bulkMutateRequest struct {
	Status        string `json:"status"`
	AssignedTo    string `json:"assignedTo"`
	HasSeen       *bool  `json:"hasSeen"`
	IsBookmarked  *bool  `json:"isBookmarked"`
	IsPublic      *bool  `json:"isPublic"`
	IsSubscribed  *bool  `json:"isSubscribed"`
	Merge         *bool  `json:"merge"`
	StatusDetails struct {
		InRelease        string `json:"inRelease"`
		InNextRelease    bool   `json:"inNextRelease"`
		InCommit         string `json:"inCommit"`
		IgnoreCount      int    `json:"ignoreCount"`
		IgnoreDuration   int    `json:"ignoreDuration"`
		IgnoreUserCount  int    `json:"ignoreUserCount"`
		IgnoreWindow     int    `json:"ignoreWindow"`
		IgnoreUserWindow int    `json:"ignoreUserWindow"`
	} `json:"statusDetails"`
}

const maxBulkIssueIDs = 200

func validateBulkIssueIDs(w http.ResponseWriter, ids []string) bool {
	if len(ids) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "Missing issue IDs.")
		return false
	}
	if len(ids) > maxBulkIssueIDs {
		httputil.WriteError(w, http.StatusBadRequest, "Too many issue IDs.")
		return false
	}
	return true
}

func applyBulkIssueMutation(w http.ResponseWriter, r *http.Request, db *sql.DB, reads controlplane.IssueReadStore, issues controlplane.IssueWorkflowStore, hooks *sqlite.HookStore, hookProjectID string) {
	ids := r.URL.Query()["id"]
	if !validateBulkIssueIDs(w, ids) {
		return
	}

	var body bulkMutateRequest
	if err := decodeJSON(r, &body); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
		return
	}

	if !validateIssueStatus(body.Status) {
		httputil.WriteError(w, http.StatusBadRequest, "Invalid status value.")
		return
	}

	patch := store.IssuePatch{}
	if body.Status != "" {
		patch.Status = &body.Status
	}
	if body.AssignedTo != "" {
		assign := strings.TrimSpace(body.AssignedTo)
		patch.Assignee = &assign
	}
	if body.Status == "resolved" || body.Status == "unresolved" || body.Status == "ignored" {
		empty := ""
		substatus := strings.TrimSpace(body.StatusDetails.InRelease)
		if body.StatusDetails.InNextRelease {
			substatus = "next_release"
		}
		patch.ResolutionSubstatus = &substatus
		patch.ResolvedInRelease = &empty
		if body.Status == "unresolved" || body.Status == "ignored" {
			patch.ResolutionSubstatus = &empty
		}
	}

	if !issuePatchHasChanges(patch) {
		httputil.WriteError(w, http.StatusBadRequest, "No changes requested.")
		return
	}

	resolveTransitions := []string(nil)
	if body.Status == "resolved" {
		resolveTransitions = issueIDsNeedingResolvedHook(r.Context(), reads, ids)
	}

	if err := issues.BulkMutateGroups(r.Context(), ids, patch); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "Failed to update issues.")
		return
	}
	if len(resolveTransitions) > 0 {
		fireResolvedIssueHooks(r.Context(), hooks, reads, db, hookProjectID, resolveTransitions)
	}

	resp := map[string]any{}
	if body.Status != "" {
		resp["status"] = body.Status
		resp["statusDetails"] = body.StatusDetails
	}
	httputil.WriteJSON(w, http.StatusOK, resp)
}

// handleBulkMutateOrgIssues handles PUT /api/0/organizations/{org_slug}/issues/.
func handleBulkMutateOrgIssues(db *sql.DB, reads controlplane.IssueReadStore, issues controlplane.IssueWorkflowStore, hooks *sqlite.HookStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		applyBulkIssueMutation(w, r, db, reads, issues, hooks, "")
	}
}

// handleBulkDeleteOrgIssues handles DELETE /api/0/organizations/{org_slug}/issues/.
func handleBulkDeleteOrgIssues(issues controlplane.IssueWorkflowStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		ids := r.URL.Query()["id"]
		if !validateBulkIssueIDs(w, ids) {
			return
		}
		if err := issues.BulkDeleteGroups(r.Context(), ids); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete issues.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleListIssueEvents handles GET /api/0/issues/{issue_id}/events/.
func handleListIssueEvents(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		id := PathParam(r, "issue_id")

		pg := ParsePagination(r)
		rows, err := sqlite.ListGroupEventsPaged(r.Context(), db, id, pg.Limit+1, pg.Offset)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list issue events.")
			return
		}
		events := apiEventsFromWebEvents(rows)
		page := SetPaginationHeaders(w, r, events, pg)
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
		projectID, orgSlug, err := projectScopeForGroup(r.Context(), db, id)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load latest event.")
			return
		}
		if err := enrichEventDetail(r, db, orgSlug, projectID, evt); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load latest event.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, evt)
	}
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

// handleGetIssueEvent handles GET /api/0/organizations/{org_slug}/issues/{issue_id}/events/{event_id}/.
func handleGetIssueEvent(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		groupID := PathParam(r, "issue_id")
		eventID := PathParam(r, "event_id")

		evt, err := sqlite.GetGroupEvent(r.Context(), db, groupID, eventID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load event.")
			return
		}
		if evt == nil {
			httputil.WriteError(w, http.StatusNotFound, "Event not found.")
			return
		}
		resp := apiEventFromWebEvent(*evt)
		projectID, err := projectIDForGroupEvent(r.Context(), db, groupID, eventID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load event.")
			return
		}
		if err := enrichEventDetail(r, db, PathParam(r, "org_slug"), projectID, resp); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load event.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func projectIDForGroupEvent(ctx context.Context, db *sql.DB, groupID, eventID string) (string, error) {
	if db == nil || groupID == "" || eventID == "" {
		return "", nil
	}
	var projectID string
	err := db.QueryRowContext(ctx,
		`SELECT project_id
		 FROM events
		 WHERE group_id = ? AND event_id = ?`,
		groupID, eventID,
	).Scan(&projectID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return projectID, nil
}

func projectScopeForGroup(ctx context.Context, db *sql.DB, groupID string) (string, string, error) {
	if db == nil || groupID == "" {
		return "", "", nil
	}
	var projectID, orgSlug string
	err := db.QueryRowContext(ctx,
		`SELECT p.id, o.slug
		 FROM groups g
		 JOIN projects p ON p.id = g.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE g.id = ?`,
		groupID,
	).Scan(&projectID, &orgSlug)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", nil
		}
		return "", "", err
	}
	return projectID, orgSlug, nil
}

// handleBulkMutateProjectIssues handles PUT /api/0/projects/{org}/{proj}/issues/.
func handleBulkMutateProjectIssues(catalog controlplane.CatalogStore, db *sql.DB, reads controlplane.IssueReadStore, issues controlplane.IssueWorkflowStore, hooks *sqlite.HookStore, auth authFunc) http.HandlerFunc {
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
		applyBulkIssueMutation(w, r, db, reads, issues, hooks, project.ID)
	}
}

func fireResolvedIssueHooks(ctx context.Context, hooks *sqlite.HookStore, reads controlplane.IssueReadStore, db *sql.DB, projectID string, issueIDs []string) {
	if hooks == nil || reads == nil || db == nil {
		return
	}
	for _, issueID := range issueIDs {
		iss, err := reads.GetIssue(ctx, issueID)
		if err != nil || iss == nil {
			log.Warn().Err(err).Str("group_id", issueID).Msg("failed to load issue for resolved hook")
			continue
		}
		if iss.Status != "resolved" {
			continue
		}
		resolvedProjectID := projectID
		if resolvedProjectID == "" {
			resolvedProjectID, err = projectIDForIssue(ctx, db, issueID)
			if err != nil {
				log.Warn().Err(err).Str("group_id", issueID).Msg("failed to resolve project for resolved hook")
				continue
			}
		}
		payload := map[string]any{
			"action": "issue.resolved",
			"data": map[string]any{
				"project": map[string]any{"id": resolvedProjectID},
				"issue": map[string]any{
					"id":                  iss.ID,
					"title":               iss.Title,
					"culprit":             iss.Culprit,
					"status":              iss.Status,
					"resolutionSubstatus": iss.ResolutionSubstatus,
					"resolvedInRelease":   iss.ResolvedInRelease,
				},
			},
		}
		if err := hooks.FireHooks(ctx, resolvedProjectID, "issue.resolved", payload); err != nil {
			log.Warn().Err(err).Str("group_id", issueID).Msg("failed to dispatch issue.resolved hooks")
		}
	}
}

func issueIDsNeedingResolvedHook(ctx context.Context, reads controlplane.IssueReadStore, issueIDs []string) []string {
	if reads == nil {
		return nil
	}
	out := make([]string, 0, len(issueIDs))
	for _, issueID := range issueIDs {
		iss, err := reads.GetIssue(ctx, issueID)
		if err != nil || iss == nil {
			continue
		}
		if iss.Status != "resolved" {
			out = append(out, issueID)
		}
	}
	return out
}

func projectIDForIssue(ctx context.Context, db *sql.DB, issueID string) (string, error) {
	var projectID string
	if err := db.QueryRowContext(ctx, `SELECT project_id FROM groups WHERE id = ?`, issueID).Scan(&projectID); err != nil {
		return "", err
	}
	return projectID, nil
}

// handleBulkDeleteProjectIssues handles DELETE /api/0/projects/{org}/{proj}/issues/.
func handleBulkDeleteProjectIssues(catalog controlplane.CatalogStore, issues controlplane.IssueWorkflowStore, auth authFunc) http.HandlerFunc {
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

		ids := r.URL.Query()["id"]
		if !validateBulkIssueIDs(w, ids) {
			return
		}
		if err := issues.BulkDeleteGroups(r.Context(), ids); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete issues.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleListIssueHashes handles GET /api/0/organizations/{org_slug}/issues/{issue_id}/hashes/.
func handleListIssueHashes(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		groupID := PathParam(r, "issue_id")

		hashes, err := sqlite.ListGroupHashes(r.Context(), db, groupID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list hashes.")
			return
		}
		if hashes == nil {
			httputil.WriteError(w, http.StatusNotFound, "Issue not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, hashes)
	}
}

// handleGetIssueTagDetail handles GET /api/0/organizations/{org_slug}/issues/{issue_id}/tags/{key}/.
func handleGetIssueTagDetail(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		groupID := PathParam(r, "issue_id")
		tagKey := PathParam(r, "key")

		detail, err := sqlite.GetIssueTagDetail(r.Context(), db, groupID, tagKey)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load tag detail.")
			return
		}
		if detail == nil {
			httputil.WriteError(w, http.StatusNotFound, "Tag not found for this issue.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, detail)
	}
}

// handleListIssueTagValues handles GET /api/0/organizations/{org_slug}/issues/{issue_id}/tags/{key}/values/.
func handleListIssueTagValues(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		groupID := PathParam(r, "issue_id")
		tagKey := PathParam(r, "key")

		values, err := sqlite.ListIssueTagValues(r.Context(), db, groupID, tagKey)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list tag values.")
			return
		}
		if values == nil {
			values = []sqlite.TagValueRow{}
		}
		httputil.WriteJSON(w, http.StatusOK, values)
	}
}

func authPrincipalFromContext(ctx context.Context) *auth.Principal {
	return auth.PrincipalFromContext(ctx)
}
