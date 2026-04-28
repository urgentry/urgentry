package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"urgentry/internal/normalize"
	"urgentry/internal/store"
)

// ---------------------------------------------------------------------------
// Event Detail
// ---------------------------------------------------------------------------

type eventDetailData struct {
	Title           string
	Nav             string
	Environment     string   // selected environment ("" = all)
	Environments    []string // available environments for global nav
	Event           eventDetailView
	Frames          []stackFrame
	ExceptionGroups []exceptionGroup
	Breadcrumbs     []breadcrumb
	Attachments     []eventAttachmentRow
	Tags            []kvPair
	User            []kvPair
	Request         []kvPair
	ContextPanels   []contextPanel
	FeatureFlags    []featureFlag
	RawJSON         string
}

type eventAttachmentRow struct {
	ID          string
	Name        string
	ContentType string
	Size        string
	DownloadURL string
}

type eventDetailView struct {
	EventID          string
	ShortEventID     string
	IssueID          string
	Title            string
	Level            string
	ExceptionType    string
	ExceptionValue   string
	ProcessingStatus string
	IngestError      string
	ResolvedFrames   int
	UnresolvedFrames int
	TimeAgo          string
}

type stackFrame struct {
	File       string
	Function   string
	LineNo     int
	ColNo      int
	InApp      bool
	MappedFrom string // e.g. "mapped from app.min.js:1:45678" (empty if not source-mapped)
	CodeLines  []codeLine
}

type codeLine struct {
	Number    int
	Content   string
	Highlight bool
}

type breadcrumb struct {
	Level        string
	Time         string
	Category     string
	Message      string
	Type         string
	TimestampRel string            // e.g. "2.3s before", "150ms before"
	Data         map[string]string // flattened breadcrumb data
	CategoryCSS  string            // CSS class for category badge
}

type kvPair struct {
	Key   string
	Value string
}

func (h *Handler) eventDetailPage(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")

	if h.webStore == nil {
		http.Error(w, "Web UI unavailable", http.StatusServiceUnavailable)
		return
	}
	h.eventDetailFromDB(w, r, eventID)
}

func (h *Handler) eventDetailFromDB(w http.ResponseWriter, r *http.Request, eventID string) {
	wsEvent, err := h.webStore.GetEvent(r.Context(), eventID)
	if err != nil {
		http.Error(w, "Failed to load event.", http.StatusInternalServerError)
		return
	}
	if wsEvent == nil {
		http.NotFound(w, r)
		return
	}
	event := wsEvent

	short := event.EventID
	if len(short) > 8 {
		short = short[:8]
	}

	// Parse exception type/value from title
	excType := event.Title
	excValue := ""
	if idx := strings.Index(event.Title, ": "); idx > 0 {
		excType = event.Title[:idx]
		excValue = event.Title[idx+2:]
	}

	frames := generateFramesFromDB(event)
	excGroups := stackTraceFromPayload([]byte(event.NormalizedJSON))

	// Resolve source maps if a resolver and release tag are available.
	if h.sourceResolver != nil {
		release := event.Tags["release"]
		if release == "" {
			for _, p := range parseTagsFromNormalized(event.NormalizedJSON) {
				if p.Key == "release" {
					release = p.Value
					break
				}
			}
		}
		if release != "" {
			projectID, _ := h.webStore.DefaultProjectID(r.Context())
			excGroups = resolveSourceContext(r.Context(), h.sourceResolver, projectID, release, excGroups)
		}
	}

	// Apply code mappings to generate source links for stack frames.
	if h.codeMappings != nil {
		projectID, _ := h.webStore.DefaultProjectID(r.Context())
		if projectID != "" {
			if mappings, mapErr := h.codeMappings.ListCodeMappings(r.Context(), projectID); mapErr == nil {
				applyCodeMappings(excGroups, mappings)
			}
		}
	}

	breadcrumbs := parseBreadcrumbsWithTime(event.NormalizedJSON, event.Timestamp)

	// Tags — from the tags_json column first, then fall back to normalized JSON
	tags := make([]kvPair, 0, len(event.Tags))
	for k, v := range event.Tags {
		tags = append(tags, kvPair{Key: k, Value: v})
	}

	// Try extracting tags from normalized JSON if tags_json was empty
	if len(tags) == 0 && event.NormalizedJSON != "" {
		tags = parseTagsFromNormalized(event.NormalizedJSON)
	}

	user := parseUserFromNormalized(event.NormalizedJSON)
	reqInfo := parseRequestFromNormalized(event.NormalizedJSON)
	ctxPanels := parseContextPanels(event.NormalizedJSON)
	resolvedFrames, unresolvedFrames := normalize.CountNativeFrames(event.NormalizedJSON)

	data := eventDetailData{
		Title:        event.Title,
		Nav:          "issues",
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(r.Context()),
		Event: eventDetailView{
			EventID:          event.EventID,
			ShortEventID:     short,
			IssueID:          event.GroupID,
			Title:            event.Title,
			Level:            event.Level,
			ExceptionType:    excType,
			ExceptionValue:   excValue,
			ProcessingStatus: string(event.ProcessingStatus),
			IngestError:      event.IngestError,
			ResolvedFrames:   resolvedFrames,
			UnresolvedFrames: unresolvedFrames,
			TimeAgo:          timeAgo(event.Timestamp),
		},
		Frames:          frames,
		ExceptionGroups: excGroups,
		Breadcrumbs:     breadcrumbs,
		Tags:            tags,
		User:            user,
		Request:         reqInfo,
		ContextPanels:   ctxPanels,
		FeatureFlags:    parseFeatureFlags(event.NormalizedJSON),
		RawJSON:         prettyJSON(event.NormalizedJSON),
	}
	if attachments, attachErr := h.listEventAttachmentsDB(r.Context(), event.EventID); attachErr == nil {
		data.Attachments = attachments
	}

	h.render(w, "event-detail.html", data)
}
func generateFramesFromDB(event *store.WebEvent) []stackFrame {
	if event.NormalizedJSON != "" {
		if frames := parseFramesFromNormalized(event.NormalizedJSON); len(frames) > 0 {
			return frames
		}
	}
	return nil
}

func (h *Handler) listEventAttachmentsDB(ctx context.Context, eventID string) ([]eventAttachmentRow, error) {
	if h.webStore == nil {
		return nil, nil
	}
	items, err := h.webStore.ListEventAttachments(ctx, eventID)
	if err != nil {
		return nil, err
	}
	attachments := make([]eventAttachmentRow, 0, len(items))
	for _, item := range items {
		attachments = append(attachments, eventAttachmentRow{
			ID:          item.ID,
			Name:        item.Name,
			ContentType: item.ContentType,
			Size:        formatBytes(item.Size),
			DownloadURL: "/api/0/events/" + eventID + "/attachments/" + item.ID + "/",
		})
	}
	return attachments, nil
}

// parseFramesFromNormalized extracts real stack frames from the normalized event JSON.
func parseFramesFromNormalized(rawJSON string) []stackFrame {
	parsed := normalize.ParseFrames(rawJSON)
	if parsed == nil {
		return nil
	}
	frames := make([]stackFrame, len(parsed))
	for i, pf := range parsed {
		sf := stackFrame{
			File:       pf.File,
			Function:   pf.Function,
			LineNo:     pf.LineNo,
			ColNo:      pf.ColNo,
			InApp:      pf.InApp,
			MappedFrom: pf.MappedFrom,
		}
		for _, cl := range pf.CodeLines {
			sf.CodeLines = append(sf.CodeLines, codeLine{
				Number:    cl.Number,
				Content:   cl.Content,
				Highlight: cl.Highlight,
			})
		}
		frames[i] = sf
	}
	return frames
}

// parseUserFromNormalized extracts user data from the normalized event JSON.
func parseUserFromNormalized(rawJSON string) []kvPair {
	return kvPairsFromNormalize(normalize.ParseUser(rawJSON))
}

// parseRequestFromNormalized extracts request data from the normalized event JSON.
func parseRequestFromNormalized(rawJSON string) []kvPair {
	return kvPairsFromNormalize(normalize.ParseRequest(rawJSON))
}

// parseTagsFromNormalized extracts tags from the normalized event JSON.
func parseTagsFromNormalized(rawJSON string) []kvPair {
	return kvPairsFromNormalize(normalize.ParseNormalizedTags(rawJSON))
}

// parseBreadcrumbsFromNormalized extracts breadcrumbs from the normalized event JSON.
// When called without an event time, relative timestamps are omitted.
func parseBreadcrumbsFromNormalized(rawJSON string) []breadcrumb {
	return parseBreadcrumbsWithTime(rawJSON, time.Time{})
}

// parseBreadcrumbsWithTime extracts breadcrumbs from the normalized event JSON,
// computing relative timestamps against the event time.
func parseBreadcrumbsWithTime(rawJSON string, eventTime time.Time) []breadcrumb {
	if rawJSON == "" {
		return nil
	}
	bcs := parseBreadcrumbs([]byte(rawJSON), eventTime)
	// Attach CSS category class for template rendering.
	for i := range bcs {
		bcs[i].CategoryCSS = breadcrumbCategoryClass(bcs[i].Category)
	}
	return bcs
}

// kvPairsFromNormalize converts normalize.KVPair slices to web kvPair slices.
func kvPairsFromNormalize(nkvs []normalize.KVPair) []kvPair {
	if nkvs == nil {
		return nil
	}
	pairs := make([]kvPair, len(nkvs))
	for i, nkv := range nkvs {
		pairs[i] = kvPair{Key: nkv.Key, Value: nkv.Value}
	}
	return pairs
}

// buildHighlights extracts key metadata from a DB event for the highlights grid.
func buildHighlights(event *store.WebEvent) []kvPair {
	hl := []kvPair{
		{Key: "level", Value: event.Level},
		{Key: "handled", Value: "--"},
	}

	// Try to extract environment and release from tags or normalized JSON.
	env := event.Tags["environment"]
	rel := event.Tags["release"]

	if env == "" {
		if pairs := parseTagsFromNormalized(event.NormalizedJSON); len(pairs) > 0 {
			for _, p := range pairs {
				if p.Key == "environment" && env == "" {
					env = p.Value
				}
				if p.Key == "release" && rel == "" {
					rel = p.Value
				}
			}
		}
	}

	if rel != "" {
		hl = append(hl, kvPair{Key: "release", Value: rel})
	}
	if env != "" {
		hl = append(hl, kvPair{Key: "environment", Value: env})
	}
	if event.Culprit != "" {
		hl = append(hl, kvPair{Key: "transaction", Value: event.Culprit})
	}

	return hl
}

// buildActivity constructs an activity timeline for an issue.
func buildActivity(issue *store.WebIssue, rows []store.IssueActivityEntry) []activityItem {
	items := []activityItem{
		{Color: "info", Text: "Issue created", TimeAgo: timeAgo(issue.FirstSeen)},
	}
	if issue.LastSeen != issue.FirstSeen {
		items = append(items, activityItem{
			Color:   "warning",
			Text:    fmt.Sprintf("Last event received (%s total)", formatNumber(int(issue.Count))),
			TimeAgo: timeAgo(issue.LastSeen),
		})
	}
	if txt := issueResolutionActivity(issue); txt != "" {
		items = append(items, activityItem{
			Color:   "success",
			Text:    txt,
			TimeAgo: timeAgo(issue.LastSeen),
		})
	}
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		items = append(items, activityItem{
			Color:   activityColorForKind(row.Kind),
			Text:    activityTextForKind(row.Kind, row.Summary, row.Details),
			TimeAgo: timeAgo(row.DateCreated),
		})
	}
	return items
}

func issueResolutionActivity(issue *store.WebIssue) string {
	switch {
	case issue.ResolutionSubstatus == "next_release":
		return "Marked to resolve in the next release"
	case issue.Status == "resolved":
		return "Marked as resolved"
	case issue.Status == "ignored":
		return "Archived (ignored)"
	default:
		return ""
	}
}

func activityColorForKind(kind string) string {
	switch kind {
	case "comment":
		return "info"
	case "resolve", "reopen":
		return "success"
	case "ignore", "merge", "bookmark", "subscription":
		return "muted"
	case "native_reprocess":
		return "warning"
	default:
		return "info"
	}
}

func activityTextForKind(kind, summary, details string) string {
	switch kind {
	case "comment":
		if strings.TrimSpace(details) != "" {
			return "Comment: " + strings.TrimSpace(details)
		}
		return "Comment added"
	case "merge":
		return summary
	case "unmerge":
		return "Issue unmerged"
	case "native_reprocess":
		if strings.TrimSpace(summary) != "" {
			return summary
		}
		return "Native symbols reprocessed"
	default:
		if strings.TrimSpace(summary) != "" {
			return summary
		}
		return kind
	}
}
