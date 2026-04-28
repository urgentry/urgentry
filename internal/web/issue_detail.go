package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"urgentry/internal/auth"
	"urgentry/internal/normalize"
	"urgentry/internal/store"
)

// ---------------------------------------------------------------------------
// Issue Detail
// ---------------------------------------------------------------------------

type issueDetailData struct {
	Title           string
	Nav             string
	Environment     string   // selected environment ("" = all)
	Environments    []string // available environments for global nav
	Issue           issueDetailView
	Event           eventDetailView
	Frames          []stackFrame
	FrameGroups     []frameGroup
	Tags            []kvPair
	Breadcrumbs     []breadcrumb
	User            []kvPair
	Request         []kvPair
	Highlights      []kvPair
	Activity        []activityItem
	Comments        []issueCommentView
	Events          []eventRow
	SimilarIssues   []relatedIssueView
	MergedChildren  []relatedIssueView
	TagFacets       []tagFacet
	ChartPoints     []chartPoint
	TagDistribution []tagDistRow
	RawJSON         string
	HasEvent        bool
	// Event navigation
	EventOffset  int
	HasPrevEvent bool
	HasNextEvent bool
	TotalEvents  int
	// Beyond-Sentry: issue diff
	IssueDiff []IssueDiffEntry
	Workflow  issueWorkflowView
}

type tagFacet struct {
	Key   string
	Value string
	Count int
}

type issueDetailView struct {
	ID                  string
	ShortID             string
	Title               string
	ExcType             string
	ExcValue            string
	Status              string
	StatusLabel         string
	ResolutionSubstatus string
	ResolvedInRelease   string
	ResolutionLabel     string
	MergedIntoGroupID   string
	Level               string
	EventCount          string
	UserCount           string
	FirstSeen           string
	LastSeen            string
	Assignee            string
	Priority            int
	PriorityLabel       string
}

type issueWorkflowView struct {
	Bookmarked          bool
	Subscribed          bool
	MergedIntoGroupID   string
	ResolutionSubstatus string
	ResolvedInRelease   string
}

type issueCommentView struct {
	Author    string
	Body      string
	TimeAgo   string
	CreatedAt string
}

type relatedIssueView struct {
	ID        string
	ShortID   string
	Title     string
	Culprit   string
	Status    string
	StatusTag string
	LastSeen  string
}

type eventRow struct {
	EventID      string
	ShortEventID string
	Title        string
	Level        string
	Platform     string
	TimeAgo      string
}

func (h *Handler) issueDetailPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if h.webStore == nil {
		http.Error(w, "Web UI unavailable", http.StatusServiceUnavailable)
		return
	}
	h.issueDetailFromDB(w, r, id)
}

func (h *Handler) issueDetailFromDB(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	wsIssue, err := h.webStore.GetIssue(ctx, id)
	if err != nil {
		http.Error(w, "Failed to load issue.", http.StatusInternalServerError)
		return
	}
	if wsIssue == nil {
		http.NotFound(w, r)
		return
	}
	issue := wsIssue

	wsEvents, err := h.webStore.ListIssueEvents(ctx, id, 0)
	if err != nil {
		wsEvents = nil
	}

	wsTagFacets, _ := h.webStore.ListTagFacets(ctx, id)
	tagFacets := make([]tagFacet, len(wsTagFacets))
	for i, f := range wsTagFacets {
		tagFacets[i] = tagFacet{Key: f.Key, Value: f.Value, Count: f.Count}
	}

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

	shortID := fmt.Sprintf("GENTRY-%d", issue.ShortID)
	if issue.ShortID == 0 {
		shortID = issue.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
	}

	// Parse exception type/value from title.
	excType := issue.Title
	excValue := ""
	if idx := strings.Index(issue.Title, ": "); idx > 0 {
		excType = issue.Title[:idx]
		excValue = issue.Title[idx+2:]
	}

	userCount, _ := h.countDistinctUsersForGroupDB(ctx, id)
	principal := auth.PrincipalFromContext(ctx)
	var workflow issueWorkflowView
	if principal != nil && principal.User != nil {
		if state, stateErr := h.webStore.GetIssueWorkflowState(ctx, id, principal.User.ID); stateErr == nil {
			workflow = issueWorkflowView{
				Bookmarked:          state.Bookmarked,
				Subscribed:          state.Subscribed,
				MergedIntoGroupID:   state.MergedIntoGroupID,
				ResolutionSubstatus: state.ResolutionSubstatus,
				ResolvedInRelease:   state.ResolvedInRelease,
			}
		}
	}
	var comments []issueCommentView
	if rows, commentErr := h.webStore.ListIssueComments(ctx, id, 50); commentErr == nil {
		comments = make([]issueCommentView, 0, len(rows))
		for i := len(rows) - 1; i >= 0; i-- {
			row := rows[i]
			author := row.UserName
			if author == "" {
				author = row.UserEmail
			}
			if author == "" {
				author = "Anonymous"
			}
			comments = append(comments, issueCommentView{
				Author:    author,
				Body:      row.Body,
				TimeAgo:   timeAgo(row.DateCreated),
				CreatedAt: row.DateCreated.Format("2006-01-02 15:04"),
			})
		}
	}
	var activityRows []store.IssueActivityEntry
	if rows, activityErr := h.webStore.ListIssueActivity(ctx, id, 50); activityErr == nil {
		activityRows = rows
	}
	var similarIssues []relatedIssueView
	if rows, similarErr := h.webStore.ListSimilarIssues(ctx, id, 6); similarErr == nil {
		similarIssues = relatedIssueViews(rows)
	}
	var mergedChildren []relatedIssueView
	if rows, mergedErr := h.webStore.ListMergedChildIssues(ctx, id, 10); mergedErr == nil {
		mergedChildren = relatedIssueViews(rows)
	}

	data := issueDetailData{
		Title:        issue.Title,
		Nav:          "issues",
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(r.Context()),
		Issue: issueDetailView{
			ID:                  issue.ID,
			ShortID:             shortID,
			Title:               issue.Title,
			ExcType:             excType,
			ExcValue:            excValue,
			Status:              issue.Status,
			StatusLabel:         issueStatusLabel(issue.Status, issue.ResolutionSubstatus, issue.ResolvedInRelease),
			ResolutionSubstatus: issue.ResolutionSubstatus,
			ResolvedInRelease:   issue.ResolvedInRelease,
			ResolutionLabel:     issueResolutionLabel(issue.ResolutionSubstatus, issue.ResolvedInRelease),
			MergedIntoGroupID:   issue.MergedIntoGroupID,
			Level:               issue.Level,
			EventCount:          formatNumber(int(issue.Count)),
			UserCount:           formatNumber(userCount),
			FirstSeen:           timeAgo(issue.FirstSeen),
			LastSeen:            timeAgo(issue.LastSeen),
			Assignee:            issue.Assignee,
			Priority:            issue.Priority,
			PriorityLabel:       priorityLabel(issue.Priority),
		},
		Events:         evRows,
		SimilarIssues:  similarIssues,
		MergedChildren: mergedChildren,
		TagFacets:      tagFacets,
		Comments:       comments,
		Workflow:       workflow,
	}

	// Count total events for navigation.
	totalEvents, _ := h.webStore.CountEventsForGroup(ctx, id)
	data.TotalEvents = totalEvents

	// Load event based on ?event_offset=N or ?event= query param.
	eventOffset := 0
	if off, parseErr := strconv.Atoi(r.URL.Query().Get("event_offset")); parseErr == nil && off >= 0 {
		eventOffset = off
	}
	eventParam := r.URL.Query().Get("event")
	var wsEvent *store.WebEvent
	switch eventParam {
	case "first":
		// "first" = oldest = highest offset
		eventOffset = totalEvents - 1
		if eventOffset < 0 {
			eventOffset = 0
		}
		wsEvent, err = h.webStore.GetEventAtOffset(ctx, id, eventOffset)
	default:
		wsEvent, err = h.webStore.GetEventAtOffset(ctx, id, eventOffset)
	}
	data.EventOffset = eventOffset
	data.HasPrevEvent = eventOffset > 0
	data.HasNextEvent = eventOffset < totalEvents-1
	latestEvent := wsEvent
	if err == nil && latestEvent != nil {
		data.HasEvent = true
		short := latestEvent.EventID
		if len(short) > 8 {
			short = short[:8]
		}
		levType := excType
		levValue := excValue
		if idx := strings.Index(latestEvent.Title, ": "); idx > 0 {
			levType = latestEvent.Title[:idx]
			levValue = latestEvent.Title[idx+2:]
		}
		resolvedFrames, unresolvedFrames := normalize.CountNativeFrames(latestEvent.NormalizedJSON)

		data.Event = eventDetailView{
			EventID:          latestEvent.EventID,
			ShortEventID:     short,
			IssueID:          latestEvent.GroupID,
			Title:            latestEvent.Title,
			Level:            latestEvent.Level,
			ExceptionType:    levType,
			ExceptionValue:   levValue,
			ProcessingStatus: string(latestEvent.ProcessingStatus),
			IngestError:      latestEvent.IngestError,
			ResolvedFrames:   resolvedFrames,
			UnresolvedFrames: unresolvedFrames,
			TimeAgo:          timeAgo(latestEvent.Timestamp),
		}

		data.Frames = generateFramesFromDB(latestEvent)

		// Tags from normalized JSON or tags_json.
		tags := make([]kvPair, 0, len(latestEvent.Tags))
		for k, v := range latestEvent.Tags {
			tags = append(tags, kvPair{Key: k, Value: v})
		}
		if len(tags) == 0 && latestEvent.NormalizedJSON != "" {
			tags = parseTagsFromNormalized(latestEvent.NormalizedJSON)
		}
		data.Tags = tags

		// User from normalized JSON.
		data.User = parseUserFromNormalized(latestEvent.NormalizedJSON)

		// Request from normalized JSON.
		data.Request = parseRequestFromNormalized(latestEvent.NormalizedJSON)

		// Breadcrumbs from normalized JSON only.
		data.Breadcrumbs = parseBreadcrumbsFromNormalized(latestEvent.NormalizedJSON)

		// Highlights.
		data.Highlights = buildHighlights(latestEvent)

		// Raw JSON.
		data.RawJSON = prettyJSON(latestEvent.NormalizedJSON)
	}

	// Activity timeline.
	data.Activity = buildActivity(issue, activityRows)

	// Feature 1: Event chart data (last 30 days).
	wsChartPoints, err := h.webStore.EventChartData(ctx, id, 30)
	if err == nil && len(wsChartPoints) > 0 {
		cp := make([]chartPoint, len(wsChartPoints))
		for i, p := range wsChartPoints {
			cp[i] = chartPoint{Day: p.Day, Count: p.Count, Height: p.Height}
		}
		data.ChartPoints = cp
	}

	// Feature 2: Tag distribution bars.
	wsTagDist, _ := h.webStore.TagDistribution(ctx, id)
	tagDist := make([]tagDistRow, len(wsTagDist))
	for i, td := range wsTagDist {
		tagDist[i] = tagDistRow{Key: td.Key, Value: td.Value, Percent: td.Percent, Hue: td.Color}
	}
	data.TagDistribution = tagDist

	// Feature 3: Frame groups (collapse non-in-app frames).
	data.FrameGroups = groupFrames(data.Frames)

	// Beyond-Sentry: Issue diff (changes between first and latest event).
	data.IssueDiff = h.computeIssueDiff(ctx, id)

	h.render(w, "issue-detail.html", data)
}

func relatedIssueViews(rows []store.WebIssue) []relatedIssueView {
	items := make([]relatedIssueView, 0, len(rows))
	for _, row := range rows {
		items = append(items, relatedIssueView{
			ID:        row.ID,
			ShortID:   formatIssueShortID(row.ShortID, row.ID),
			Title:     row.Title,
			Culprit:   row.Culprit,
			Status:    row.Status,
			StatusTag: issueStatusLabel(row.Status, row.ResolutionSubstatus, row.ResolvedInRelease),
			LastSeen:  timeAgo(row.LastSeen),
		})
	}
	return items
}
