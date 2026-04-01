package web

import (
	"context"
	"strings"

	sharedstore "urgentry/internal/store"
)

type dashboardLogRow struct {
	EventID     string
	Title       string
	Summary     string
	Level       string
	LevelColor  string
	Logger      string
	Environment string
	TimeAgo     string
	URL         string
	TraceURL    string
}

type dashboardTraceRow struct {
	EventID     string
	Transaction string
	Status      string
	Duration    string
	Release     string
	Environment string
	TimeAgo     string
	URL         string
}

func (h *Handler) dashboardRecentLogs(ctx context.Context, orgSlug, environment string, limit int) ([]dashboardLogRow, error) {
	if h.webStore == nil || strings.TrimSpace(orgSlug) == "" || limit <= 0 {
		return nil, nil
	}
	items, err := h.webStore.ListRecentLogs(ctx, orgSlug, limit*4)
	if err != nil {
		return nil, err
	}
	rows := make([]dashboardLogRow, 0, min(limit, len(items)))
	for _, item := range items {
		if strings.TrimSpace(environment) != "" && !strings.EqualFold(strings.TrimSpace(item.Environment), strings.TrimSpace(environment)) {
			continue
		}
		rows = append(rows, dashboardLogRow{
			EventID:     item.EventID,
			Title:       firstNonEmptyText(item.Title, item.Message, item.EventID),
			Summary:     firstNonEmptyText(item.Logger, item.Release, item.Message),
			Level:       firstNonEmptyText(item.Level, "info"),
			LevelColor:  levelColor(item.Level),
			Logger:      firstNonEmptyText(item.Logger, "logger"),
			Environment: firstNonEmptyText(item.Environment, "default"),
			TimeAgo:     timeAgo(item.Timestamp),
			URL:         "/events/" + item.EventID + "/",
			TraceURL:    dashboardTraceURL(item.TraceID),
		})
		if len(rows) >= limit {
			break
		}
	}
	return rows, nil
}

func (h *Handler) dashboardRecentTransactions(ctx context.Context, orgSlug, environment string, limit int) ([]dashboardTraceRow, string, string, string, error) {
	if h.webStore == nil || strings.TrimSpace(orgSlug) == "" || limit <= 0 {
		return nil, "Recent latency", "Unavailable", "No transactions yet", nil
	}
	items, err := h.webStore.ListRecentTransactions(ctx, orgSlug, limit*4)
	if err != nil {
		return nil, "", "", "", err
	}
	rows := make([]dashboardTraceRow, 0, min(limit, len(items)))
	var slowest *sharedstore.DiscoverTransaction
	for i := range items {
		item := items[i]
		if strings.TrimSpace(environment) != "" && !strings.EqualFold(strings.TrimSpace(item.Environment), strings.TrimSpace(environment)) {
			continue
		}
		if slowest == nil || item.DurationMS > slowest.DurationMS {
			slowest = &item
		}
		rows = append(rows, dashboardTraceRow{
			EventID:     item.EventID,
			Transaction: firstNonEmptyText(item.Transaction, item.TraceID, item.EventID),
			Status:      firstNonEmptyText(item.Status, "unknown"),
			Duration:    formatTraceDuration(item.DurationMS),
			Release:     firstNonEmptyText(item.Release, "unreleased"),
			Environment: firstNonEmptyText(item.Environment, "default"),
			TimeAgo:     timeAgo(firstNonZeroTime(item.EndTimestamp, item.StartTimestamp, item.Timestamp)),
			URL:         firstNonEmptyText(dashboardTraceURL(item.TraceID), "/events/"+item.EventID+"/"),
		})
		if len(rows) >= limit {
			break
		}
	}
	if slowest == nil {
		return rows, "Recent latency", "Unavailable", "No transactions yet", nil
	}
	return rows, "Recent latency", formatTraceDuration(slowest.DurationMS), firstNonEmptyText(strings.TrimSpace(slowest.Transaction), strings.TrimSpace(slowest.TraceID), "Latest transaction"), nil
}

func (h *Handler) dashboardRecentReleases(ctx context.Context, limit int) ([]releaseRow, error) {
	if h.webStore == nil || limit <= 0 {
		return nil, nil
	}
	items, err := h.listReleasesDB(ctx, limit)
	if err != nil {
		return nil, err
	}
	rows := make([]releaseRow, 0, len(items))
	for _, item := range items {
		rows = append(rows, releaseRow{
			Version:          item.version,
			CreatedAt:        timeAgo(item.createdAt),
			EventCount:       formatNumber(item.eventCount),
			SessionCount:     formatNumber(item.sessionCount),
			ErroredSessions:  formatNumber(item.erroredSessions),
			CrashedSessions:  formatNumber(item.crashedSessions),
			AbnormalSessions: formatNumber(item.abnormalSessions),
			AffectedUsers:    formatNumber(item.affectedUsers),
			CrashFreeRate:    formatPercent(item.crashFreeRate),
			LastSeen:         formatReleaseLastSeen(item.lastSessionAt),
		})
	}
	return rows, nil
}

func (h *Handler) dashboardRecentReplays(ctx context.Context, projectID, environment string, limit int) ([]replayRow, error) {
	if h.replays == nil || strings.TrimSpace(projectID) == "" || limit <= 0 {
		return nil, nil
	}
	items, err := h.replays.ListReplays(ctx, projectID, limit*4)
	if err != nil {
		return nil, err
	}
	rows := make([]replayRow, 0, min(limit, len(items)))
	for _, item := range items {
		if strings.TrimSpace(environment) != "" && !strings.EqualFold(strings.TrimSpace(item.Environment), strings.TrimSpace(environment)) {
			continue
		}
		rows = append(rows, replayRowFromManifest(item))
		if len(rows) >= limit {
			break
		}
	}
	return rows, nil
}

func (h *Handler) dashboardRecentProfiles(ctx context.Context, projectID, environment string, limit int) ([]profileRow, error) {
	if h.queries == nil || strings.TrimSpace(projectID) == "" || limit <= 0 {
		return nil, nil
	}
	items, err := h.queries.ListProfiles(ctx, projectID, limit*4)
	if err != nil {
		return nil, err
	}
	rows := make([]profileRow, 0, min(limit, len(items)))
	for _, item := range items {
		if strings.TrimSpace(environment) != "" && !strings.EqualFold(strings.TrimSpace(item.Environment), strings.TrimSpace(environment)) {
			continue
		}
		rows = append(rows, profileRowFromManifest(item))
		if len(rows) >= limit {
			break
		}
	}
	return rows, nil
}

func dashboardTraceURL(traceID string) string {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return ""
	}
	return "/traces/" + traceID + "/"
}
