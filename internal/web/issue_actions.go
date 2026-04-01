package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/auth"
	"urgentry/internal/store"
	"github.com/rs/zerolog/log"
)

// ---------------------------------------------------------------------------
// Update Issue Status (Resolve / Ignore / Reopen)
// ---------------------------------------------------------------------------

func (h *Handler) updateIssueStatus(w http.ResponseWriter, r *http.Request) {
	if h.authz != nil {
		if !h.authz.ValidateCSRF(r) {
			writeWebForbidden(w, r)
			return
		}
		if err := h.authz.AuthorizeIssue(r, r.PathValue("id"), auth.ScopeIssueWrite); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}

	id := r.PathValue("id")
	action := r.FormValue("action")
	ctx := r.Context()

	// Map action to status.
	var status string
	var resolutionSubstatus string
	var resolvedInRelease string
	switch action {
	case "resolve":
		status = "resolved"
	case "resolve_next":
		status = "resolved"
		resolutionSubstatus = "next_release"
	case "ignore":
		status = "ignored"
	case "reopen":
		status = "unresolved"
		resolutionSubstatus = ""
		resolvedInRelease = ""
	default:
		writeWebBadRequest(w, r, "Invalid action")
		return
	}

	// Update in DB if available — use h.db != nil instead of hasDBData()
	// so we can update groups even when the cached row-count check hasn't
	// flipped yet (e.g., first event just arrived).
	if h.issues != nil {
		patch := store.IssuePatch{Status: &status}
		if action == "resolve_next" {
			patch.ResolutionSubstatus = &resolutionSubstatus
			patch.ResolvedInRelease = &resolvedInRelease
		}
		if action == "resolve" || action == "ignore" || action == "reopen" {
			empty := ""
			patch.ResolutionSubstatus = &empty
			patch.ResolvedInRelease = &empty
		}
		empty := ""
		patch.MergedIntoGroupID = &empty
		if err := h.issues.PatchIssue(ctx, id, patch); err != nil {
			writeWebInternal(w, r, "Failed to update issue")
			return
		}
		if principal := auth.PrincipalFromContext(ctx); principal != nil && principal.User != nil {
			summary := map[string]string{
				"resolve":      "Marked as resolved",
				"resolve_next": "Marked to resolve in the next release",
				"ignore":       "Ignored issue",
				"reopen":       "Reopened issue",
			}[action]
			h.recordIssueActivityBestEffort(ctx, id, principal.User.ID, action, summary, "")
		}
	}

	// If HTMX request, tell HTMX to refresh in-place (smoother than a full redirect).
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Refresh", "true")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Non-HTMX: redirect back.
	referer := r.Referer()
	if referer == "" {
		referer = fmt.Sprintf("/issues/%s/", id)
	}
	http.Redirect(w, r, referer, http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// Assignee / Priority Handlers
// ---------------------------------------------------------------------------

func (h *Handler) updateIssueAssignee(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	assignee := strings.TrimSpace(r.FormValue("assignee"))
	ctx := r.Context()
	if h.authz != nil {
		if !h.authz.ValidateCSRF(r) {
			writeWebForbidden(w, r)
			return
		}
		if err := h.authz.AuthorizeIssue(r, id, auth.ScopeIssueWrite); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}

	if h.issues != nil {
		if err := h.issues.PatchIssue(ctx, id, store.IssuePatch{Assignee: &assignee}); err != nil {
			writeWebInternal(w, r, "Failed to update issue")
			return
		}
		if principal := auth.PrincipalFromContext(ctx); principal != nil && principal.User != nil {
			h.recordIssueActivityBestEffort(ctx, id, principal.User.ID, "assign", "Assigned issue", assignee)
		}
	}

	referer := r.Referer()
	if referer == "" {
		referer = fmt.Sprintf("/issues/%s/", id)
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", referer)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, referer, http.StatusSeeOther)
}

func (h *Handler) updateIssuePriority(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	priority, _ := strconv.Atoi(r.FormValue("priority"))
	if priority < 0 || priority > 3 {
		priority = 2
	}
	ctx := r.Context()
	if h.authz != nil {
		if !h.authz.ValidateCSRF(r) {
			writeWebForbidden(w, r)
			return
		}
		if err := h.authz.AuthorizeIssue(r, id, auth.ScopeIssueWrite); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}

	if h.issues != nil {
		if err := h.issues.PatchIssue(ctx, id, store.IssuePatch{Priority: &priority}); err != nil {
			writeWebInternal(w, r, "Failed to update issue")
			return
		}
		if principal := auth.PrincipalFromContext(ctx); principal != nil && principal.User != nil {
			h.recordIssueActivityBestEffort(ctx, id, principal.User.ID, "priority", "Priority changed", priorityLabel(priority))
		}
	}

	referer := r.Referer()
	if referer == "" {
		referer = fmt.Sprintf("/issues/%s/", id)
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", referer)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, referer, http.StatusSeeOther)
}

func (h *Handler) toggleIssueBookmark(w http.ResponseWriter, r *http.Request) {
	if h.authz != nil {
		if !h.authz.ValidateCSRF(r) {
			writeWebForbidden(w, r)
			return
		}
		if err := h.authz.AuthorizeIssue(r, r.PathValue("id"), auth.ScopeIssueWrite); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	if h.issues == nil {
		writeWebUnavailable(w, r, "Bookmarks unavailable")
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		writeWebUnauthorized(w, r)
		return
	}
	bookmark := strings.TrimSpace(r.FormValue("bookmark"))
	var enable bool
	switch bookmark {
	case "1", "true", "on":
		enable = true
	default:
		enable = false
	}
	if err := h.issues.ToggleIssueBookmark(r.Context(), r.PathValue("id"), principal.User.ID, enable); err != nil {
		writeWebInternal(w, r, "Failed to update bookmark")
		return
	}
	h.recordIssueActivityBestEffort(r.Context(), r.PathValue("id"), principal.User.ID, "bookmark", func() string {
		if enable {
			return "Bookmarked issue"
		}
		return "Bookmark removed"
	}(), "")
	issueRedirectOrRefresh(w, r, fmt.Sprintf("/issues/%s/", r.PathValue("id")))
}

func (h *Handler) toggleIssueSubscription(w http.ResponseWriter, r *http.Request) {
	if h.authz != nil {
		if !h.authz.ValidateCSRF(r) {
			writeWebForbidden(w, r)
			return
		}
		if err := h.authz.AuthorizeIssue(r, r.PathValue("id"), auth.ScopeIssueWrite); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	if h.issues == nil {
		writeWebUnavailable(w, r, "Subscriptions unavailable")
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		writeWebUnauthorized(w, r)
		return
	}
	subscribe := strings.TrimSpace(r.FormValue("subscribe"))
	var enable bool
	switch subscribe {
	case "1", "true", "on":
		enable = true
	default:
		enable = false
	}
	if err := h.issues.ToggleIssueSubscription(r.Context(), r.PathValue("id"), principal.User.ID, enable); err != nil {
		writeWebInternal(w, r, "Failed to update subscription")
		return
	}
	h.recordIssueActivityBestEffort(r.Context(), r.PathValue("id"), principal.User.ID, "subscription", func() string {
		if enable {
			return "Subscribed to issue"
		}
		return "Unsubscribed from issue"
	}(), "")
	issueRedirectOrRefresh(w, r, fmt.Sprintf("/issues/%s/", r.PathValue("id")))
}

func (h *Handler) addIssueComment(w http.ResponseWriter, r *http.Request) {
	if h.authz != nil {
		if !h.authz.ValidateCSRF(r) {
			writeWebForbidden(w, r)
			return
		}
		if err := h.authz.AuthorizeIssue(r, r.PathValue("id"), auth.ScopeIssueWrite); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	if h.issues == nil {
		writeWebUnavailable(w, r, "Comments unavailable")
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		writeWebUnauthorized(w, r)
		return
	}
	body := strings.TrimSpace(r.FormValue("body"))
	if body == "" {
		writeWebBadRequest(w, r, "Comment body is required")
		return
	}
	if _, err := h.issues.AddIssueComment(r.Context(), r.PathValue("id"), principal.User.ID, body); err != nil {
		writeWebInternal(w, r, "Failed to save comment")
		return
	}
	issueRedirectOrRefresh(w, r, fmt.Sprintf("/issues/%s/", r.PathValue("id")))
}

func (h *Handler) mergeIssue(w http.ResponseWriter, r *http.Request) {
	if h.authz != nil {
		if !h.authz.ValidateCSRF(r) {
			writeWebForbidden(w, r)
			return
		}
		if err := h.authz.AuthorizeIssue(r, r.PathValue("id"), auth.ScopeIssueWrite); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	if h.issues == nil {
		writeWebUnavailable(w, r, "Merge unavailable")
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		writeWebUnauthorized(w, r)
		return
	}
	target := strings.TrimSpace(r.FormValue("target_issue_id"))
	if target == "" {
		writeWebBadRequest(w, r, "Target issue is required")
		return
	}
	if err := h.issues.MergeIssue(r.Context(), r.PathValue("id"), target, principal.User.ID); err != nil {
		writeWebInternal(w, r, "Failed to merge issue")
		return
	}
	issueRedirectOrRefresh(w, r, fmt.Sprintf("/issues/%s/", r.PathValue("id")))
}

func (h *Handler) unmergeIssue(w http.ResponseWriter, r *http.Request) {
	if h.authz != nil {
		if !h.authz.ValidateCSRF(r) {
			writeWebForbidden(w, r)
			return
		}
		if err := h.authz.AuthorizeIssue(r, r.PathValue("id"), auth.ScopeIssueWrite); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	if h.issues == nil {
		writeWebUnavailable(w, r, "Merge unavailable")
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		writeWebUnauthorized(w, r)
		return
	}
	if err := h.issues.UnmergeIssue(r.Context(), r.PathValue("id"), principal.User.ID); err != nil {
		writeWebInternal(w, r, "Failed to unmerge issue")
		return
	}
	issueRedirectOrRefresh(w, r, fmt.Sprintf("/issues/%s/", r.PathValue("id")))
}

func (h *Handler) recordIssueActivityBestEffort(ctx context.Context, groupID, userID, kind, summary, details string) {
	if h.issues == nil {
		return
	}
	if err := h.issues.RecordIssueActivity(ctx, groupID, userID, kind, summary, details); err != nil {
		log.Warn().
			Err(err).
			Str("group_id", groupID).
			Str("user_id", userID).
			Str("kind", kind).
			Msg("failed to record issue activity")
	}
}

func issueRedirectOrRefresh(w http.ResponseWriter, r *http.Request, fallback string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Refresh", "true")
		w.WriteHeader(http.StatusOK)
		return
	}
	referer := r.Referer()
	if referer == "" {
		referer = fallback
	}
	http.Redirect(w, r, referer, http.StatusSeeOther)
}

// getSelectedSort reads the sort order from query param or cookie.
// Valid values: "last_seen", "first_seen", "events", "priority".
// Defaults to "last_seen".
func getSelectedSort(w http.ResponseWriter, r *http.Request) string {
	validSorts := map[string]bool{
		"last_seen": true, "first_seen": true, "events": true, "priority": true,
	}
	sort := r.URL.Query().Get("sort")
	if sort != "" {
		if !validSorts[sort] {
			sort = "last_seen"
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "urgentry_sort",
			Value:    sort,
			Path:     "/",
			MaxAge:   86400 * 30,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		return sort
	}
	if c, err := r.Cookie("urgentry_sort"); err == nil && c.Value != "" && validSorts[c.Value] {
		return c.Value
	}
	return "last_seen"
}

// getSelectedEnvironment reads the environment from query param or cookie.
// If set via query param, a cookie is set to persist the choice.
func getSelectedEnvironment(w http.ResponseWriter, r *http.Request) string {
	env := r.URL.Query().Get("environment")
	if env != "" {
		// Persist in cookie.
		http.SetCookie(w, &http.Cookie{
			Name:     "urgentry_environment",
			Value:    env,
			Path:     "/",
			MaxAge:   86400 * 30, // 30 days
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		if env == "all" {
			return ""
		}
		return env
	}
	// Fall back to cookie.
	if c, err := r.Cookie("urgentry_environment"); err == nil && c.Value != "" && c.Value != "all" {
		return c.Value
	}
	return ""
}

// readSelectedEnvironment reads the environment from the urgentry_env cookie
// without writing anything. Used by handlers that only need to read the
// current selection for template data.
func readSelectedEnvironment(r *http.Request) string {
	if c, err := r.Cookie("urgentry_env"); err == nil && c.Value != "" && c.Value != "all" {
		return c.Value
	}
	return ""
}

// TimeRangePreset describes one of the predefined time range options.
type TimeRangePreset struct {
	Value    string // cookie/query value, e.g. "24h"
	Label    string // human label, e.g. "Last 24 hours"
	Duration time.Duration
}

// timeRangePresets is the ordered set of available time range options.
var timeRangePresets = []TimeRangePreset{
	{"1h", "Last 1 hour", 1 * time.Hour},
	{"24h", "Last 24 hours", 24 * time.Hour},
	{"7d", "Last 7 days", 7 * 24 * time.Hour},
	{"14d", "Last 14 days", 14 * 24 * time.Hour},
	{"30d", "Last 30 days", 30 * 24 * time.Hour},
	{"90d", "Last 90 days", 90 * 24 * time.Hour},
}

// validTimeRange returns true if the given string is a known preset value.
func validTimeRange(v string) bool {
	for _, p := range timeRangePresets {
		if p.Value == v {
			return true
		}
	}
	return false
}

// timeRangeDuration returns the duration for a preset string, defaulting to 24h.
func timeRangeDuration(v string) time.Duration {
	for _, p := range timeRangePresets {
		if p.Value == v {
			return p.Duration
		}
	}
	return 24 * time.Hour
}

// getSelectedTimeRange reads the time range from query param or cookie.
// Returns the preset string (e.g. "24h") and the computed since time.
func getSelectedTimeRange(w http.ResponseWriter, r *http.Request) (string, time.Time) {
	tr := r.URL.Query().Get("timerange")
	if tr != "" {
		if !validTimeRange(tr) {
			tr = "24h"
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "urgentry_timerange",
			Value:    tr,
			Path:     "/",
			MaxAge:   86400 * 30,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		return tr, time.Now().Add(-timeRangeDuration(tr))
	}
	if c, err := r.Cookie("urgentry_timerange"); err == nil && c.Value != "" && validTimeRange(c.Value) {
		return c.Value, time.Now().Add(-timeRangeDuration(c.Value))
	}
	return "24h", time.Now().Add(-24 * time.Hour)
}

// setTimeRangeAction handles POST /settings/time-range.
// Sets the time range cookie and redirects back.
func (h *Handler) setTimeRangeAction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	code := strings.TrimSpace(r.PostForm.Get("range"))
	if !validTimeRange(code) {
		code = "24h"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "urgentry_timerange",
		Value:    code,
		Path:     "/",
		MaxAge:   86400 * 30,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	target := strings.TrimSpace(r.PostForm.Get("return_to"))
	if !strings.HasPrefix(target, "/") {
		if ref := r.Header.Get("Referer"); ref != "" {
			target = ref
		} else {
			target = "/"
		}
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
