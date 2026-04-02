package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/normalize"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

type issueWorkflowStateReader interface {
	GetIssueWorkflowState(ctx context.Context, groupID, userID string) (store.IssueWorkflowState, error)
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
		rows, err := reads.SearchProjectIssues(r.Context(), project.ID, r.URL.Query().Get("filter"), r.URL.Query().Get("query"), 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list issues.")
			return
		}
		extras := loadIssueResponseExtras(r.Context(), db, issues, principalUserID(authPrincipalFromContext(r.Context())), rows)
		issues := make([]Issue, 0, len(rows))
		for _, row := range rows {
			issue := apiIssueFromWebIssueWithExtras(row, extras[row.ID])
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
		extras := loadIssueResponseExtras(r.Context(), db, issues, principalUserID(authPrincipalFromContext(r.Context())), []store.WebIssue{*row})
		httputil.WriteJSON(w, http.StatusOK, apiIssueFromWebIssueWithExtras(*row, extras[row.ID]))
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
		var resolveTransitions []string
		if body.Status == "resolved" {
			resolveTransitions = issueIDsNeedingResolvedHook(r.Context(), reads, []string{id})
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
		if len(resolveTransitions) > 0 {
			fireResolvedIssueHooks(r.Context(), hooks, reads, db, "", resolveTransitions)
		}
		extras := loadIssueResponseExtras(r.Context(), db, issues, principalUserID(authPrincipalFromContext(r.Context())), []store.WebIssue{*iss})
		httputil.WriteJSON(w, http.StatusOK, apiIssueFromWebIssueWithExtras(*iss, extras[iss.ID]))
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

// handleBulkMutateOrgIssues handles PUT /api/0/organizations/{org_slug}/issues/.
func handleBulkMutateOrgIssues(db *sql.DB, reads controlplane.IssueReadStore, issues controlplane.IssueWorkflowStore, hooks *sqlite.HookStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		ids := r.URL.Query()["id"]
		if len(ids) == 0 {
			httputil.WriteError(w, http.StatusBadRequest, "Missing issue IDs.")
			return
		}

		var body bulkMutateRequest
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

		if patch.Status == nil && patch.Assignee == nil && patch.ResolutionSubstatus == nil {
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
			fireResolvedIssueHooks(r.Context(), hooks, reads, db, "", resolveTransitions)
		}

		resp := map[string]any{}
		if body.Status != "" {
			resp["status"] = body.Status
			resp["statusDetails"] = body.StatusDetails
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

// handleBulkDeleteOrgIssues handles DELETE /api/0/organizations/{org_slug}/issues/.
func handleBulkDeleteOrgIssues(issues controlplane.IssueWorkflowStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		ids := r.URL.Query()["id"]
		if len(ids) == 0 {
			httputil.WriteError(w, http.StatusBadRequest, "Missing issue IDs.")
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
	return apiIssueFromWebIssueWithExtras(row, defaultIssueResponseExtras(row))
}

func apiIssueFromWebIssueWithExtras(row store.WebIssue, extras issueResponseExtras) Issue {
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
		Type:                extras.Type,
		AssignedTo:          extras.AssignedTo,
		HasSeen:             extras.HasSeen,
		IsBookmarked:        extras.IsBookmarked,
		IsPublic:            extras.IsPublic,
		IsSubscribed:        extras.IsSubscribed,
		Priority:            extras.Priority,
		Substatus:           extras.Substatus,
		Metadata:            extras.Metadata,
		NumComments:         extras.NumComments,
		UserCount:           extras.UserCount,
		Stats:               extras.Stats,
		ResolutionSubstatus: row.ResolutionSubstatus,
		ResolvedInRelease:   row.ResolvedInRelease,
		MergedIntoIssueID:   row.MergedIntoGroupID,
		FirstSeen:           row.FirstSeen,
		LastSeen:            row.LastSeen,
		Count:               int(row.Count),
	}
}

func defaultIssueResponseExtras(row store.WebIssue) issueResponseExtras {
	return issueResponseExtras{
		AssignedTo:   apiIssueAssignee(row.Assignee),
		HasSeen:      true,
		IsBookmarked: false,
		IsPublic:     false,
		IsSubscribed: false,
		Priority:     row.Priority,
		Substatus:    row.ResolutionSubstatus,
		Metadata:     issueMetadata(row),
		Type:         "error",
		NumComments:  0,
		UserCount:    0,
		Stats:        IssueStats{Last24Hours: []int{}},
	}
}

func loadIssueResponseExtras(ctx context.Context, db *sql.DB, issues controlplane.IssueWorkflowStore, userID string, rows []store.WebIssue) map[string]issueResponseExtras {
	extras := make(map[string]issueResponseExtras, len(rows))
	groupIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		extras[row.ID] = defaultIssueResponseExtras(row)
		groupIDs = append(groupIDs, row.ID)
	}

	if db != nil && len(groupIDs) > 0 {
		webStore := sqlite.NewWebStore(db)
		if counts, err := webStore.BatchUserCounts(ctx, groupIDs); err == nil {
			for groupID, count := range counts {
				item := extras[groupID]
				item.UserCount = count
				extras[groupID] = item
			}
		}
		if sparklines, err := webStore.BatchSparklines(ctx, groupIDs, 24, 24*time.Hour); err == nil {
			for _, groupID := range groupIDs {
				item := extras[groupID]
				item.Stats = IssueStats{Last24Hours: append([]int(nil), sparklines[groupID]...)}
				if item.Stats.Last24Hours == nil {
					item.Stats.Last24Hours = []int{}
				}
				extras[groupID] = item
			}
		}
	}

	if issues == nil {
		return extras
	}

	var stateReader issueWorkflowStateReader
	if reader, ok := any(issues).(issueWorkflowStateReader); ok {
		stateReader = reader
	}
	for _, row := range rows {
		item := extras[row.ID]
		if comments, err := issues.ListIssueComments(ctx, row.ID, 100); err == nil {
			item.NumComments = len(comments)
		}
		if stateReader != nil {
			if state, err := stateReader.GetIssueWorkflowState(ctx, row.ID, userID); err == nil {
				item.IsBookmarked = state.Bookmarked
				item.IsSubscribed = state.Subscribed
				if item.Substatus == "" {
					item.Substatus = state.ResolutionSubstatus
				}
			}
		}
		extras[row.ID] = item
	}

	return extras
}

func apiIssueAssignee(value string) *IssueUser {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if slug, ok := strings.CutPrefix(value, "team:"); ok {
		slug = strings.TrimSpace(slug)
		if slug == "" {
			return nil
		}
		return &IssueUser{
			ID:       slug,
			Type:     "team",
			Name:     slug,
			Username: slug,
		}
	}
	user := &IssueUser{
		ID:   value,
		Type: "user",
		Name: value,
	}
	if strings.Contains(value, "@") {
		user.Email = value
		user.Username = strings.TrimSpace(strings.SplitN(value, "@", 2)[0])
		return user
	}
	user.Username = value
	return user
}

func issueMetadata(row store.WebIssue) Metadata {
	return issueMetadataFromParts(row.Title, row.Culprit)
}

func issueMetadataFromParts(title, culprit string) Metadata {
	meta := Metadata{}
	title = strings.TrimSpace(title)
	if prefix, value, ok := strings.Cut(title, ":"); ok {
		if prefix = strings.TrimSpace(prefix); prefix != "" {
			meta["type"] = prefix
		}
		if value = strings.TrimSpace(value); value != "" {
			meta["value"] = value
		}
	} else if title != "" {
		meta["value"] = title
	}
	if culprit = strings.TrimSpace(culprit); culprit != "" {
		meta["culprit"] = culprit
	}
	return meta
}

func apiEventsFromWebEvents(rows []store.WebEvent) []*Event {
	events := make([]*Event, 0, len(rows))
	for _, row := range rows {
		events = append(events, apiEventFromWebEvent(row))
	}
	return events
}

func apiEventTags(tags map[string]string) []EventTag {
	if len(tags) == 0 {
		return nil
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]EventTag, 0, len(keys))
	for _, key := range keys {
		items = append(items, EventTag{Key: key, Value: tags[key]})
	}
	return items
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
		Tags:             apiEventTags(row.Tags),
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
		httputil.WriteJSON(w, http.StatusOK, apiEventFromWebEvent(*evt))
	}
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

		ids := r.URL.Query()["id"]
		if len(ids) == 0 {
			httputil.WriteError(w, http.StatusBadRequest, "Missing issue IDs.")
			return
		}

		var body bulkMutateRequest
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

		if patch.Status == nil && patch.Assignee == nil && patch.ResolutionSubstatus == nil {
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
			fireResolvedIssueHooks(r.Context(), hooks, reads, db, project.ID, resolveTransitions)
		}

		resp := map[string]any{}
		if body.Status != "" {
			resp["status"] = body.Status
			resp["statusDetails"] = body.StatusDetails
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
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
		if len(ids) == 0 {
			httputil.WriteError(w, http.StatusBadRequest, "Missing issue IDs.")
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
