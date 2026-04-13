package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/sqlite"
	sharedstore "urgentry/internal/store"
)

type replaysData struct {
	Title   string
	Nav     string
	Environment  string   // selected environment ("" = all)
	Environments []string // available environments for global nav
	Guide   analyticsGuide
	Replays []replayRow
}

type replayRow struct {
	ID          string
	Title       string
	URL         string
	User        string
	Platform    string
	Release     string
	Environment string
	TimeAgo     string
	TimeClass   string
	Summary     string
}

type replayDetailData struct {
	Title            string
	Nav              string
	Environment  string   // selected environment ("" = all)
	Environments []string // available environments for global nav
	Replay           replayRow
	Attachments      []eventAttachmentRow
	AssetBytes       string
	AssetKinds       []countRow
	RawJSON          string
	StatusClass      string
	ProcessingStatus string
	IngestError      string
	Duration         string
	TimelineStart    string
	TimelineEnd      string
	PaneTabs         []replayPaneTab
	SelectedPane     string
	Selected         *replayTimelineView
	SelectedIndex    int
	Timeline         []replayTimelineView
	VideoURL         string
	TraceCount       int
	LinkedIssueCount int
}

type profilesData struct {
	Title    string
	Nav      string
	Environment  string   // selected environment ("" = all)
	Environments []string // available environments for global nav
	Guide    analyticsGuide
	Filters  profileListFilters
	Profiles []profileRow
}

type profileRow struct {
	ID          string
	Title       string
	Transaction string
	TraceID     string
	Platform    string
	Release     string
	Environment string
	Duration    string
	TimeAgo     string
	TimeClass   string
	Summary     string
}

type profileDetailData struct {
	Title            string
	Nav              string
	Environment  string   // selected environment ("" = all)
	Environments []string // available environments for global nav
	Profile          profileRow
	Summary          sharedstore.ProfileSummary
	TopFrames        []sharedstore.ProfileBreakdown
	TopFunctions     []sharedstore.ProfileBreakdown
	Links            []profileLink
	Controls         profileDetailControls
	TopDownRows      []profileTreeRow
	BottomUpRows     []profileTreeRow
	FlamegraphRows   []profileTreeRow
	HotPathRows      []profileHotPathRow
	HotPathTruncated bool
	Comparison       *profileComparisonData
	ComparisonError  string
	RawJSON          string
}

type countRow struct {
	Name  string
	Count int
}

func (h *Handler) replaysPage(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		http.Error(w, "Web UI unavailable", http.StatusServiceUnavailable)
		return
	}
	scope, ok := h.guardProjectQueryPage(w, r, sqlite.QueryWorkloadReplays, 100, "", false)
	if !ok {
		return
	}
	items, err := h.replays.ListReplays(r.Context(), scope.ProjectID, 100)
	if err != nil {
		http.Error(w, "Failed to load replays.", http.StatusInternalServerError)
		return
	}
	rows := make([]replayRow, 0, len(items))
	for _, item := range items {
		rows = append(rows, replayRowFromManifest(item))
	}
	h.render(w, "replays.html", replaysData{
		Title:        "Replays",
		Nav:          "replays",
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(r.Context()),
		Guide:   replaysGuide(),
		Replays: rows,
	})
}

func (h *Handler) replayDetailPage(w http.ResponseWriter, r *http.Request) {
	if h.db == nil || h.replays == nil {
		http.Error(w, "Web UI unavailable", http.StatusServiceUnavailable)
		return
	}
	replayID := r.PathValue("id")
	scope, ok := h.guardReplayQueryPage(w, r, replayID, 500, true)
	if !ok {
		return
	}
	record, err := h.replays.GetReplay(r.Context(), scope.ProjectID, replayID)
	if err != nil {
		if errors.Is(err, sharedstore.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "Failed to load replay.", http.StatusInternalServerError)
		return
	}
	issues, err := h.replayIssueLookup(r.Context(), record.Manifest.LinkedIssueIDs)
	if err != nil {
		http.Error(w, "Failed to load replay issue links.", http.StatusInternalServerError)
		return
	}
	allRows := replayTimelineRows(scope.OrganizationSlug, scope.ProjectSlug, replayID, record.Timeline, issues)
	selectedPane, visibleRows, selected, selectedIndex := selectReplayTimeline(allRows, r.URL.Query().Get("pane"), strings.TrimSpace(r.URL.Query().Get("anchor")), parseReplayTS(r))
	selectedTS := int64(0)
	selectedAnchor := ""
	if selected != nil {
		selectedTS = selected.TimestampMS
		selectedAnchor = selected.ID
		for i := range visibleRows {
			visibleRows[i].Href = replayDetailURL(replayID, selectedPane, visibleRows[i].ID, visibleRows[i].TimestampMS)
		}
	}
	attachments := replayAssetRows(scope.OrganizationSlug, scope.ProjectSlug, replayID, record.Assets)
	row := replayRowFromManifest(record.Manifest)
	h.render(w, "replay-detail.html", replayDetailData{
		Title:            row.Title,
		Nav:              "replays",
		Replay:           row,
		Attachments:      attachments,
		AssetBytes:       formatBytes(replayAssetBytes(record.Assets)),
		AssetKinds:       replayAssetKindsFromRefs(record.Assets),
		RawJSON:          prettyJSON(string(record.Payload)),
		StatusClass:      replayStatusClass(string(record.Manifest.ProcessingStatus)),
		ProcessingStatus: string(record.Manifest.ProcessingStatus),
		IngestError:      strings.TrimSpace(record.Manifest.IngestError),
		Duration:         formatReplayOffset(record.Manifest.DurationMS),
		TimelineStart:    formatReplayOffset(record.Manifest.TimelineStartMS),
		TimelineEnd:      formatReplayOffset(record.Manifest.TimelineEndMS),
		PaneTabs:         replayPaneTabs(replayID, len(allRows), record.Manifest, selectedPane, selectedTS, selectedAnchor),
		SelectedPane:     selectedPane,
		Selected:         selected,
		SelectedIndex:    selectedIndex,
		Timeline:         visibleRows,
		VideoURL:         replayVideoURL(attachments),
		TraceCount:       len(record.Manifest.TraceIDs),
		LinkedIssueCount: len(record.Manifest.LinkedIssueIDs),
	})
}

func (h *Handler) profilesPage(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		http.Error(w, "Web UI unavailable", http.StatusServiceUnavailable)
		return
	}
	filters := parseProfileListFilters(r)
	scope, ok := h.guardProjectQueryPage(w, r, sqlite.QueryWorkloadProfiles, 100, profileListQueryString(filters), false)
	if !ok {
		return
	}
	items, err := h.queries.ListProfiles(r.Context(), scope.ProjectID, 100)
	if err != nil {
		http.Error(w, "Failed to load profiles.", http.StatusInternalServerError)
		return
	}
	items = filterProfileManifests(items, filters)
	rows := make([]profileRow, 0, len(items))
	for _, item := range items {
		rows = append(rows, profileRowFromManifest(item))
	}
	h.render(w, "profiles.html", profilesData{
		Title:    "Profiles",
		Nav:      "profiles",
		Guide:    profilesGuide(),
		Filters:  filters,
		Profiles: rows,
	})
}

func (h *Handler) profileDetailPage(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		http.Error(w, "Web UI unavailable", http.StatusServiceUnavailable)
		return
	}
	filter, compareID := parseProfileDetailFilter(r, r.PathValue("id"))
	scope, ok := h.guardProjectQueryPage(w, r, sqlite.QueryWorkloadProfiles, profileDetailQueryLimit(filter, compareID), profileDetailQueryString(filter, compareID), false)
	if !ok {
		return
	}
	item, err := h.queries.GetProfile(r.Context(), scope.ProjectID, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sharedstore.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "Failed to load profile.", http.StatusInternalServerError)
		return
	}
	topDown, err := h.queries.QueryTopDown(r.Context(), scope.ProjectID, filter)
	if err != nil {
		http.Error(w, "Failed to load profile call tree.", http.StatusInternalServerError)
		return
	}
	bottomUp, err := h.queries.QueryBottomUp(r.Context(), scope.ProjectID, filter)
	if err != nil {
		http.Error(w, "Failed to load profile hotspots.", http.StatusInternalServerError)
		return
	}
	flamegraph, err := h.queries.QueryFlamegraph(r.Context(), scope.ProjectID, filter)
	if err != nil {
		http.Error(w, "Failed to load profile flamegraph.", http.StatusInternalServerError)
		return
	}
	hotPath, err := h.queries.QueryHotPath(r.Context(), scope.ProjectID, filter)
	if err != nil {
		http.Error(w, "Failed to load profile hot path.", http.StatusInternalServerError)
		return
	}
	recentProfiles, err := h.queries.ListProfiles(r.Context(), scope.ProjectID, 50)
	if err != nil {
		http.Error(w, "Failed to load profile comparison options.", http.StatusInternalServerError)
		return
	}
	issueID, err := lookupProfileIssueID(r.Context(), h.db, scope.ProjectID, item.Manifest.EventRowID, item.Manifest.TraceID)
	if err != nil {
		http.Error(w, "Failed to resolve related issue.", http.StatusInternalServerError)
		return
	}
	var comparison *profileComparisonData
	var comparisonError string
	if strings.TrimSpace(compareID) != "" && compareID != item.Manifest.ProfileID {
		rawComparison, err := h.queries.CompareProfiles(r.Context(), scope.ProjectID, sharedstore.ProfileComparisonFilter{
			BaselineProfileID:  item.Manifest.ProfileID,
			CandidateProfileID: compareID,
			ThreadID:           filter.ThreadID,
		})
		if err != nil {
			comparisonError = "Comparison unavailable for the selected profile."
		} else {
			comparison = mapProfileComparisonData(recentProfiles, compareID, rawComparison)
		}
	}
	row := profileRowFromManifest(item.Manifest)
	summary := mapProfileRecordSummary(item)
	h.render(w, "profile-detail.html", profileDetailData{
		Title:            row.Title,
		Nav:              "profiles",
		Profile:          row,
		Summary:          summary,
		TopFrames:        summary.TopFrames,
		TopFunctions:     summary.TopFunctions,
		Links:            buildProfileLinks(item.Manifest, issueID),
		Controls:         profileDetailControls{ThreadID: filter.ThreadID, Frame: filter.FrameFilter, MaxDepth: filter.MaxDepth, MaxNodes: filter.MaxNodes, CompareID: compareID, Threads: profileThreadOptions(item.Threads, filter.ThreadID), Comparands: profileCompareOptions(recentProfiles, item.Manifest.ProfileID, compareID)},
		TopDownRows:      flattenProfileTree(topDown),
		BottomUpRows:     flattenProfileTree(bottomUp),
		FlamegraphRows:   flattenProfileTree(flamegraph),
		HotPathRows:      mapProfileHotPathRows(hotPath),
		HotPathTruncated: hotPath != nil && hotPath.Truncated,
		Comparison:       comparison,
		ComparisonError:  comparisonError,
		RawJSON:          prettyJSON(string(item.RawPayload)),
	})
}

func (h *Handler) replayIssueLookup(ctx context.Context, issueIDs []string) (map[string]sharedstore.WebIssue, error) {
	if h.webStore == nil {
		return nil, nil
	}
	if state := pageRequestStateFromContext(ctx); state != nil {
		state.inc("replay_issue_lookup.batch")
	}
	return h.webStore.GetIssues(ctx, issueIDs)
}

func parseReplayTS(r *http.Request) int64 {
	raw := strings.TrimSpace(r.URL.Query().Get("ts"))
	if raw == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func profileRowFromManifest(item sharedstore.ProfileManifest) profileRow {
	summary := mapProfileManifestSummary(item)
	title := "Profile"
	if strings.TrimSpace(item.Transaction) != "" {
		title = "Profile for " + strings.TrimSpace(item.Transaction)
	}
	seenAt := item.StartedAt
	if seenAt.IsZero() {
		seenAt = item.DateCreated
	}
	row := profileRow{
		ID:          item.ProfileID,
		Title:       title,
		Transaction: item.Transaction,
		TraceID:     item.TraceID,
		Platform:    item.Platform,
		Release:     item.Release,
		Environment: item.Environment,
		Duration:    formatDurationNS(strconv.FormatInt(item.DurationNS, 10)),
		TimeAgo:     timeAgo(seenAt),
		TimeClass:   timeAgoClass(seenAt),
		Summary:     profileSummaryLine(summary),
	}
	return row
}

func mapProfileManifestSummary(item sharedstore.ProfileManifest) sharedstore.ProfileSummary {
	return sharedstore.ProfileSummary{
		Transaction:   item.Transaction,
		TraceID:       item.TraceID,
		Platform:      item.Platform,
		Release:       item.Release,
		Environment:   item.Environment,
		DurationNS:    strconv.FormatInt(item.DurationNS, 10),
		SampleCount:   item.SampleCount,
		FrameCount:    item.FrameCount,
		FunctionCount: item.FunctionCount,
	}
}

func mapProfileRecordSummary(item *sharedstore.ProfileRecord) sharedstore.ProfileSummary {
	summary := mapProfileManifestSummary(item.Manifest)
	summary.TopFrames = mapProfileWebBreakdowns(item.TopFrames)
	summary.TopFunctions = mapProfileWebBreakdowns(item.TopFunctions)
	return summary
}

func mapProfileWebBreakdowns(items []sharedstore.ProfileBreakdown) []sharedstore.ProfileBreakdown {
	result := make([]sharedstore.ProfileBreakdown, 0, len(items))
	for _, item := range items {
		result = append(result, sharedstore.ProfileBreakdown{Name: item.Name, Count: item.Count})
	}
	return result
}

func formatDurationNS(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	ns, err := time.ParseDuration(raw + "ns")
	if err != nil {
		return raw + " ns"
	}
	if ns >= time.Millisecond {
		return fmt.Sprintf("%.1f ms", float64(ns)/float64(time.Millisecond))
	}
	return fmt.Sprintf("%.1f µs", float64(ns)/float64(time.Microsecond))
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func replayAssetKind(name, contentType string) string {
	lowerName := strings.ToLower(name)
	lowerType := strings.ToLower(contentType)
	switch {
	case strings.Contains(lowerName, "video") || strings.Contains(lowerType, "video"):
		return "video"
	case strings.Contains(lowerName, "recording") || strings.Contains(lowerName, "replay") || strings.Contains(lowerType, "json"):
		return "recording"
	default:
		return "other"
	}
}

func profileSummaryLine(summary sharedstore.ProfileSummary) string {
	parts := make([]string, 0, 4)
	if summary.Transaction != "" {
		parts = append(parts, summary.Transaction)
	}
	if summary.DurationNS != "" {
		parts = append(parts, formatDurationNS(summary.DurationNS))
	}
	if summary.SampleCount > 0 {
		parts = append(parts, fmt.Sprintf("%d samples", summary.SampleCount))
	}
	if len(parts) == 0 {
		return "Profile"
	}
	return strings.Join(parts, " · ")
}

func sortCountRows(counts map[string]int) []countRow {
	if len(counts) == 0 {
		return nil
	}
	rows := make([]countRow, 0, len(counts))
	for name, count := range counts {
		rows = append(rows, countRow{Name: name, Count: count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count == rows[j].Count {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Count > rows[j].Count
	})
	return rows
}
