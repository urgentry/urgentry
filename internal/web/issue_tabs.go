package web

import (
	"fmt"
	"net/http"
	"strings"

	"urgentry/internal/store"
)

// ---------------------------------------------------------------------------
// Issue Detail Tab Data
// ---------------------------------------------------------------------------

// issueTabData is the shared base for all issue detail tab pages.
type issueTabData struct {
	Title        string
	Nav          string
	Environment  string
	Environments []string
	Issue        issueDetailView
	ActiveTab    string
	Tabs         []issueTab
}

type issueTab struct {
	Key    string
	Label  string
	URL    string
	Active bool
}

func issueTabs(issueID, activeTab string) []issueTab {
	defs := []struct{ key, label string }{
		{"details", "Details"},
		{"events", "Events"},
		{"activity", "Activity"},
		{"similar", "Similar Issues"},
		{"merged", "Merged Issues"},
		{"tags", "Tags"},
		{"replays", "Replays"},
	}
	tabs := make([]issueTab, 0, len(defs))
	for _, d := range defs {
		var url string
		if d.key == "details" {
			url = "/issues/" + issueID + "/"
		} else {
			url = "/issues/" + issueID + "/" + d.key + "/"
		}
		tabs = append(tabs, issueTab{
			Key:    d.key,
			Label:  d.label,
			URL:    url,
			Active: d.key == activeTab,
		})
	}
	return tabs
}

// issueEventsTabData holds data for the events tab.
type issueEventsTabData struct {
	issueTabData
	Events     []eventRow
	TotalCount int
}

// issueActivityTabData holds data for the activity tab.
type issueActivityTabData struct {
	issueTabData
	Activity []activityItem
}

// issueSimilarTabData holds data for the similar issues tab.
type issueSimilarTabData struct {
	issueTabData
	SimilarIssues []relatedIssueView
}

// issueMergedTabData holds data for the merged issues tab.
type issueMergedTabData struct {
	issueTabData
	MergedChildren []relatedIssueView
}

// issueTagsTabData holds data for the tags tab.
type issueTagsTabData struct {
	issueTabData
	TagFacets       []tagFacet
	TagDistribution []tagDistRow
}

// issueReplaysTabData holds data for the replays tab.
type issueReplaysTabData struct {
	issueTabData
	Replays []replayRow
}

// ---------------------------------------------------------------------------
// Shared: load minimal issue header for tab pages
// ---------------------------------------------------------------------------

func (h *Handler) loadIssueTabBase(r *http.Request, id, activeTab string) (issueTabData, bool) {
	ctx := r.Context()
	wsIssue, err := h.webStore.GetIssue(ctx, id)
	if err != nil || wsIssue == nil {
		return issueTabData{}, false
	}

	shortID := fmt.Sprintf("GENTRY-%d", wsIssue.ShortID)
	if wsIssue.ShortID == 0 {
		shortID = wsIssue.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
	}
	excType := wsIssue.Title
	excValue := ""
	if idx := strings.Index(wsIssue.Title, ": "); idx > 0 {
		excType = wsIssue.Title[:idx]
		excValue = wsIssue.Title[idx+2:]
	}
	userCount, _ := h.countDistinctUsersForGroupDB(ctx, id)

	base := issueTabData{
		Title:        wsIssue.Title,
		Nav:          "issues",
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(ctx),
		ActiveTab:    activeTab,
		Tabs:         issueTabs(id, activeTab),
		Issue: issueDetailView{
			ID:                  wsIssue.ID,
			ShortID:             shortID,
			Title:               wsIssue.Title,
			ExcType:             excType,
			ExcValue:            excValue,
			Status:              wsIssue.Status,
			StatusLabel:         issueStatusLabel(wsIssue.Status, wsIssue.ResolutionSubstatus, wsIssue.ResolvedInRelease),
			ResolutionSubstatus: wsIssue.ResolutionSubstatus,
			ResolvedInRelease:   wsIssue.ResolvedInRelease,
			ResolutionLabel:     issueResolutionLabel(wsIssue.ResolutionSubstatus, wsIssue.ResolvedInRelease),
			MergedIntoGroupID:   wsIssue.MergedIntoGroupID,
			Level:               wsIssue.Level,
			EventCount:          formatNumber(int(wsIssue.Count)),
			UserCount:           formatNumber(userCount),
			FirstSeen:           timeAgo(wsIssue.FirstSeen),
			LastSeen:            timeAgo(wsIssue.LastSeen),
			Assignee:            wsIssue.Assignee,
			Priority:            wsIssue.Priority,
			PriorityLabel:       priorityLabel(wsIssue.Priority),
		},
	}
	return base, true
}

// ---------------------------------------------------------------------------
// GET /issues/{id}/events/
// ---------------------------------------------------------------------------

func (h *Handler) issueEventsTab(w http.ResponseWriter, r *http.Request) {
	if h.webStore == nil {
		http.Error(w, "Web UI unavailable", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	base, ok := h.loadIssueTabBase(r, id, "events")
	if !ok {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	wsEvents, _ := h.webStore.ListIssueEvents(ctx, id, 100)
	totalCount, _ := h.webStore.CountEventsForGroup(ctx, id)

	evRows := make([]eventRow, 0, len(wsEvents))
	for _, e := range wsEvents {
		short := e.EventID
		if len(short) > 8 {
			short = short[:8]
		}
		evRows = append(evRows, eventRow{
			EventID:      e.EventID,
			ShortEventID: short,
			Title:        e.Title,
			Level:        e.Level,
			Platform:     e.Platform,
			TimeAgo:      timeAgo(e.Timestamp),
		})
	}

	data := issueEventsTabData{
		issueTabData: base,
		Events:       evRows,
		TotalCount:   totalCount,
	}
	h.render(w, "issue-events-tab.html", data)
}

// ---------------------------------------------------------------------------
// GET /issues/{id}/activity/
// ---------------------------------------------------------------------------

func (h *Handler) issueActivityTab(w http.ResponseWriter, r *http.Request) {
	if h.webStore == nil {
		http.Error(w, "Web UI unavailable", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	base, ok := h.loadIssueTabBase(r, id, "activity")
	if !ok {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	wsIssue, _ := h.webStore.GetIssue(ctx, id)
	var activityRows []store.IssueActivityEntry
	if rows, err := h.webStore.ListIssueActivity(ctx, id, 100); err == nil {
		activityRows = rows
	}

	var activity []activityItem
	if wsIssue != nil {
		activity = buildActivity(wsIssue, activityRows)
	}

	data := issueActivityTabData{
		issueTabData: base,
		Activity:     activity,
	}
	h.render(w, "issue-activity-tab.html", data)
}

// ---------------------------------------------------------------------------
// GET /issues/{id}/similar/
// ---------------------------------------------------------------------------

func (h *Handler) issueSimilarTab(w http.ResponseWriter, r *http.Request) {
	if h.webStore == nil {
		http.Error(w, "Web UI unavailable", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	base, ok := h.loadIssueTabBase(r, id, "similar")
	if !ok {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	var similarIssues []relatedIssueView
	if rows, err := h.webStore.ListSimilarIssues(ctx, id, 20); err == nil {
		similarIssues = relatedIssueViews(rows)
	}

	data := issueSimilarTabData{
		issueTabData:  base,
		SimilarIssues: similarIssues,
	}
	h.render(w, "issue-similar-tab.html", data)
}

// ---------------------------------------------------------------------------
// GET /issues/{id}/merged/
// ---------------------------------------------------------------------------

func (h *Handler) issueMergedTab(w http.ResponseWriter, r *http.Request) {
	if h.webStore == nil {
		http.Error(w, "Web UI unavailable", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	base, ok := h.loadIssueTabBase(r, id, "merged")
	if !ok {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	var mergedChildren []relatedIssueView
	if rows, err := h.webStore.ListMergedChildIssues(ctx, id, 50); err == nil {
		mergedChildren = relatedIssueViews(rows)
	}

	data := issueMergedTabData{
		issueTabData:   base,
		MergedChildren: mergedChildren,
	}
	h.render(w, "issue-merged-tab.html", data)
}

// ---------------------------------------------------------------------------
// GET /issues/{id}/tags/
// ---------------------------------------------------------------------------

func (h *Handler) issueTagsTab(w http.ResponseWriter, r *http.Request) {
	if h.webStore == nil {
		http.Error(w, "Web UI unavailable", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	base, ok := h.loadIssueTabBase(r, id, "tags")
	if !ok {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	wsTagFacets, _ := h.webStore.ListTagFacets(ctx, id)
	tagFacets := make([]tagFacet, len(wsTagFacets))
	for i, f := range wsTagFacets {
		tagFacets[i] = tagFacet{Key: f.Key, Value: f.Value, Count: f.Count}
	}

	wsTagDist, _ := h.webStore.TagDistribution(ctx, id)
	tagDist := make([]tagDistRow, len(wsTagDist))
	for i, td := range wsTagDist {
		tagDist[i] = tagDistRow{Key: td.Key, Value: td.Value, Percent: td.Percent, Hue: td.Color}
	}

	data := issueTagsTabData{
		issueTabData:    base,
		TagFacets:       tagFacets,
		TagDistribution: tagDist,
	}
	h.render(w, "issue-tags-tab.html", data)
}

// ---------------------------------------------------------------------------
// GET /issues/{id}/replays/
// ---------------------------------------------------------------------------

func (h *Handler) issueReplaysTab(w http.ResponseWriter, r *http.Request) {
	if h.webStore == nil {
		http.Error(w, "Web UI unavailable", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	base, ok := h.loadIssueTabBase(r, id, "replays")
	if !ok {
		http.NotFound(w, r)
		return
	}

	var rows []replayRow
	if h.replays != nil {
		ctx := r.Context()
		projectID, _ := h.webStore.DefaultProjectID(ctx)
		if projectID != "" {
			manifests, err := h.replays.ListReplays(ctx, projectID, 200)
			if err == nil {
				for _, m := range manifests {
					linked := false
					for _, issueID := range m.LinkedIssueIDs {
						if issueID == id {
							linked = true
							break
						}
					}
					if linked {
						rows = append(rows, replayRowFromManifest(m))
					}
				}
			}
		}
	}

	data := issueReplaysTabData{
		issueTabData: base,
		Replays:      rows,
	}
	h.render(w, "issue-replays-tab.html", data)
}

// ---------------------------------------------------------------------------
// GET /issues/errors/  — filtered to event_type=error
// GET /issues/warnings/ — filtered to level=warning
// ---------------------------------------------------------------------------

func (h *Handler) issueListErrorsPage(w http.ResponseWriter, r *http.Request) {
	if h.webStore == nil {
		http.Error(w, "Web UI unavailable", http.StatusServiceUnavailable)
		return
	}
	// Delegate to the standard list with a pre-applied query filter.
	r2 := r.Clone(r.Context())
	q := r2.URL.Query()
	if q.Get("query") == "" {
		q.Set("query", "event_type:error")
	}
	r2.URL.RawQuery = q.Encode()
	h.issueListFromDB(w, r2)
}

func (h *Handler) issueListWarningsPage(w http.ResponseWriter, r *http.Request) {
	if h.webStore == nil {
		http.Error(w, "Web UI unavailable", http.StatusServiceUnavailable)
		return
	}
	r2 := r.Clone(r.Context())
	q := r2.URL.Query()
	if q.Get("query") == "" {
		q.Set("query", "level:warning")
	}
	r2.URL.RawQuery = q.Encode()
	h.issueListFromDB(w, r2)
}
