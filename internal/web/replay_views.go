package web

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	sharedstore "urgentry/internal/store"
)

type replayPaneTab struct {
	Key    string
	Label  string
	Count  int
	URL    string
	Active bool
}

type replayTimelineView struct {
	ID               string
	Href             string
	Pane             string
	PaneLabel        string
	Kind             string
	KindLabel        string
	Title            string
	Summary          string
	Message          string
	URL              string
	Method           string
	Duration         string
	Selector         string
	Text             string
	TraceID          string
	TraceURL         string
	LinkedEventID    string
	LinkedEventURL   string
	LinkedIssueID    string
	LinkedIssueURL   string
	LinkedIssueTitle string
	LinkedIssueStatus string
	TimestampMS      int64
	TimeLabel        string
	Level            string
	StatusCode       int
	Selected         bool
	IsError          bool
}

func replayRowFromManifest(item sharedstore.ReplayManifest) replayRow {
	title := "Session replay"
	if strings.TrimSpace(item.RequestURL) != "" {
		title = "Replay of " + strings.TrimSpace(item.RequestURL)
	}
	return replayRow{
		ID:          item.ReplayID,
		Title:       title,
		URL:         item.RequestURL,
		User:        firstNonEmptyText(item.UserRef.ID, item.UserRef.Email, item.UserRef.Username),
		Platform:    item.Platform,
		Release:     item.Release,
		Environment: item.Environment,
		TimeAgo:     timeAgo(firstNonZeroTime(item.StartedAt, item.CreatedAt)),
		TimeClass:   timeAgoClass(firstNonZeroTime(item.StartedAt, item.CreatedAt)),
		Summary:     replayManifestSummaryLine(item),
	}
}

func replayManifestSummaryLine(item sharedstore.ReplayManifest) string {
	parts := make([]string, 0, 4)
	if user := firstNonEmptyText(item.UserRef.ID, item.UserRef.Email, item.UserRef.Username); user != "" {
		parts = append(parts, user)
	}
	if item.RequestURL != "" {
		parts = append(parts, item.RequestURL)
	}
	if item.ErrorMarkerCount > 0 {
		parts = append(parts, strconv.Itoa(item.ErrorMarkerCount)+" errors")
	}
	if item.DurationMS > 0 {
		parts = append(parts, formatReplayOffset(item.DurationMS))
	}
	if len(parts) == 0 {
		return "Session replay"
	}
	return strings.Join(parts, " · ")
}

func normalizeReplayPane(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "timeline":
		return "timeline"
	case "console":
		return "console"
	case "network":
		return "network"
	case "clicks":
		return "clicks"
	case "errors":
		return "errors"
	default:
		return ""
	}
}

func replayPaneLabel(pane string) string {
	switch pane {
	case "console":
		return "Console"
	case "network":
		return "Network"
	case "clicks":
		return "Clicks"
	case "errors":
		return "Errors"
	default:
		return "Timeline"
	}
}

func replayKindLabel(kind string) string {
	switch kind {
	case "console":
		return "Console"
	case "network":
		return "Network"
	case "click":
		return "Click"
	case "navigation":
		return "Navigation"
	case "error":
		return "Error"
	case "snapshot":
		return "Snapshot"
	default:
		return "Timeline"
	}
}

func replayStatusClass(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ready":
		return "status-ready"
	case "partial":
		return "status-partial"
	case "failed":
		return "status-failed"
	default:
		return "status-ignored"
	}
}

func formatReplayOffset(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	d := time.Duration(ms) * time.Millisecond
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	minutes := int(d / time.Minute)
	d -= time.Duration(minutes) * time.Minute
	seconds := int(d / time.Second)
	d -= time.Duration(seconds) * time.Second
	millis := int(d / time.Millisecond)
	if hours > 0 {
		return strconv.Itoa(hours) + ":" + leftPad2(minutes) + ":" + leftPad2(seconds) + "." + leftPad3(millis)
	}
	return leftPad2(minutes) + ":" + leftPad2(seconds) + "." + leftPad3(millis)
}

func leftPad2(value int) string {
	if value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}

func leftPad3(value int) string {
	switch {
	case value < 10:
		return "00" + strconv.Itoa(value)
	case value < 100:
		return "0" + strconv.Itoa(value)
	default:
		return strconv.Itoa(value)
	}
}

func replayTimelineRows(orgSlug, projectSlug, replayID string, items []sharedstore.ReplayTimelineItem, issues map[string]sharedstore.WebIssue) []replayTimelineView {
	rows := make([]replayTimelineView, 0, len(items))
	for _, item := range items {
		row := replayTimelineView{
			ID:             item.ID,
			Pane:           item.Pane,
			PaneLabel:      replayPaneLabel(item.Pane),
			Kind:           item.Kind,
			KindLabel:      replayKindLabel(item.Kind),
			Title:          firstNonEmptyText(item.Title, item.Message, replayKindLabel(item.Kind)),
			Summary:        replayTimelineSummary(item),
			Message:        item.Message,
			URL:            item.URL,
			Method:         item.Method,
			Selector:       item.Selector,
			Text:           item.Text,
			TraceID:        item.TraceID,
			LinkedEventID:  item.LinkedEventID,
			LinkedIssueID:  item.LinkedIssueID,
			TimestampMS:    item.TSMS,
			TimeLabel:      formatReplayOffset(item.TSMS),
			Level:          strings.ToLower(strings.TrimSpace(item.Level)),
			StatusCode:     item.StatusCode,
			IsError:        item.Kind == "error",
		}
		if item.DurationMS > 0 {
			row.Duration = formatReplayOffset(item.DurationMS)
		}
		if row.LinkedEventID != "" {
			row.LinkedEventURL = "/events/" + row.LinkedEventID + "/"
		}
		if row.TraceID != "" {
			row.TraceURL = "/traces/" + row.TraceID + "/"
		}
		if row.LinkedIssueID != "" {
			row.LinkedIssueURL = "/issues/" + row.LinkedIssueID + "/"
			if issue, ok := issues[row.LinkedIssueID]; ok {
				row.LinkedIssueTitle = issue.Title
				row.LinkedIssueStatus = issue.Status
			}
		}
		row.Href = replayDetailURL(replayID, "timeline", row.ID, row.TimestampMS)
		if orgSlug != "" && projectSlug != "" {
			row.Href = replayDetailURL(replayID, row.Pane, row.ID, row.TimestampMS)
			row.LinkedEventURL = strings.TrimSpace(row.LinkedEventURL)
			row.LinkedIssueURL = strings.TrimSpace(row.LinkedIssueURL)
			row.TraceURL = strings.TrimSpace(row.TraceURL)
		}
		rows = append(rows, row)
	}
	return rows
}

func replayTimelineSummary(item sharedstore.ReplayTimelineItem) string {
	switch item.Kind {
	case "network":
		parts := make([]string, 0, 3)
		if item.Method != "" {
			parts = append(parts, item.Method)
		}
		if item.URL != "" {
			parts = append(parts, item.URL)
		}
		if item.StatusCode > 0 {
			parts = append(parts, strconv.Itoa(item.StatusCode))
		}
		if len(parts) == 0 {
			return "Network request"
		}
		return strings.Join(parts, " · ")
	case "click":
		return firstNonEmptyText(item.Text, item.Selector, "Click")
	case "console":
		return firstNonEmptyText(item.Message, item.Title, "Console message")
	case "navigation":
		return firstNonEmptyText(item.URL, item.Title, "Navigation")
	case "error":
		return firstNonEmptyText(item.Message, item.LinkedIssueID, item.LinkedEventID, "Linked error")
	default:
		return firstNonEmptyText(item.Title, item.Message, "Replay event")
	}
}

func replayDetailURL(replayID, pane, anchor string, tsMS int64) string {
	query := url.Values{}
	query.Set("pane", normalizeReplayPane(pane))
	if query.Get("pane") == "" {
		query.Set("pane", "timeline")
	}
	if strings.TrimSpace(anchor) != "" {
		query.Set("anchor", strings.TrimSpace(anchor))
	}
	if tsMS > 0 {
		query.Set("ts", strconv.FormatInt(tsMS, 10))
	}
	target := "/replays/" + replayID + "/"
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	if strings.TrimSpace(anchor) != "" {
		target += "#item-" + strings.TrimSpace(anchor)
	}
	return target
}

func replayPaneTabs(replayID string, totalCount int, manifest sharedstore.ReplayManifest, selectedPane string, selectedTS int64, anchor string) []replayPaneTab {
	type paneCount struct {
		key   string
		label string
		count int
	}
	panes := []paneCount{
		{key: "timeline", label: "Timeline", count: totalCount},
		{key: "console", label: "Console", count: manifest.ConsoleCount},
		{key: "network", label: "Network", count: manifest.NetworkCount},
		{key: "clicks", label: "Clicks", count: manifest.ClickCount},
		{key: "errors", label: "Errors", count: manifest.ErrorMarkerCount},
	}
	tabs := make([]replayPaneTab, 0, len(panes))
	for _, pane := range panes {
		tabs = append(tabs, replayPaneTab{
			Key:    pane.key,
			Label:  pane.label,
			Count:  pane.count,
			URL:    replayDetailURL(replayID, pane.key, anchor, selectedTS),
			Active: pane.key == selectedPane,
		})
	}
	return tabs
}

func selectReplayTimeline(rows []replayTimelineView, requestedPane, anchor string, tsMS int64) (string, []replayTimelineView, *replayTimelineView, int) {
	if len(rows) == 0 {
		return "timeline", nil, nil, -1
	}
	selectedPane := normalizeReplayPane(requestedPane)
	var anchorRow *replayTimelineView
	for i := range rows {
		if rows[i].ID == strings.TrimSpace(anchor) {
			anchorRow = &rows[i]
			break
		}
	}
	if selectedPane == "" {
		if anchorRow != nil && anchorRow.Pane != "" {
			selectedPane = anchorRow.Pane
		} else {
			selectedPane = "timeline"
		}
	}
	visible := filterReplayTimeline(rows, selectedPane)
	if len(visible) == 0 && selectedPane != "timeline" {
		selectedPane = "timeline"
		visible = append([]replayTimelineView(nil), rows...)
	}
	selectedIndex := 0
	if strings.TrimSpace(anchor) != "" {
		for i := range visible {
			if visible[i].ID == strings.TrimSpace(anchor) {
				selectedIndex = i
				break
			}
		}
	}
	if tsMS > 0 && (strings.TrimSpace(anchor) == "" || visible[selectedIndex].ID != strings.TrimSpace(anchor)) {
		selectedIndex = nearestReplayTimelineIndex(visible, tsMS)
	} else if anchorRow != nil && strings.TrimSpace(anchor) != "" && visible[selectedIndex].ID != strings.TrimSpace(anchor) {
		selectedIndex = nearestReplayTimelineIndex(visible, anchorRow.TimestampMS)
	}
	visible[selectedIndex].Selected = true
	selected := visible[selectedIndex]
	return selectedPane, visible, &selected, selectedIndex
}

func filterReplayTimeline(rows []replayTimelineView, pane string) []replayTimelineView {
	if pane == "" || pane == "timeline" {
		return append([]replayTimelineView(nil), rows...)
	}
	filtered := make([]replayTimelineView, 0, len(rows))
	for _, row := range rows {
		if row.Pane == pane {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func nearestReplayTimelineIndex(rows []replayTimelineView, tsMS int64) int {
	bestIndex := 0
	bestDistance := absInt64(rows[0].TimestampMS - tsMS)
	for i := 1; i < len(rows); i++ {
		if distance := absInt64(rows[i].TimestampMS - tsMS); distance < bestDistance {
			bestIndex = i
			bestDistance = distance
		}
	}
	return bestIndex
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func replayAssetRows(orgSlug, projectSlug, replayID string, assets []sharedstore.ReplayAssetRef) []eventAttachmentRow {
	if len(assets) == 0 {
		return nil
	}
	rows := make([]eventAttachmentRow, 0, len(assets))
	for _, asset := range assets {
		downloadURL := ""
		if orgSlug != "" && projectSlug != "" {
			downloadURL = "/api/0/projects/" + orgSlug + "/" + projectSlug + "/replays/" + replayID + "/assets/" + asset.AttachmentID + "/"
		}
		rows = append(rows, eventAttachmentRow{
			ID:          asset.AttachmentID,
			Name:        asset.Name,
			ContentType: asset.ContentType,
			Size:        formatBytes(asset.SizeBytes),
			DownloadURL: downloadURL,
		})
	}
	return rows
}

func replayAssetKindsFromRefs(assets []sharedstore.ReplayAssetRef) []countRow {
	if len(assets) == 0 {
		return nil
	}
	counts := make(map[string]int, len(assets))
	for _, asset := range assets {
		kind := strings.TrimSpace(asset.Kind)
		if kind == "" {
			kind = replayAssetKind(asset.Name, asset.ContentType)
		}
		counts[kind]++
	}
	return sortCountRows(counts)
}

func replayAssetBytes(assets []sharedstore.ReplayAssetRef) int64 {
	var total int64
	for _, asset := range assets {
		total += asset.SizeBytes
	}
	return total
}

func replayVideoURL(assets []eventAttachmentRow) string {
	for _, asset := range assets {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(asset.ContentType)), "video/") {
			return asset.DownloadURL
		}
	}
	return ""
}
