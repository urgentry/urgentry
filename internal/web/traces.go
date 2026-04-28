package web

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/sqlite"
	"urgentry/internal/sqlutil"
)

type traceDetailData struct {
	Title        string
	Nav          string
	Environment  string   // selected environment ("" = all)
	Environments []string // available environments for global nav
	TraceID      string
	Guide        analyticsGuide
	Transactions []traceTransactionRow
	Spans        []traceSpanRow
	Profiles     []traceProfileRow
	Errors       []traceErrorRow
	Waterfall    waterfallData
}

type traceErrorRow struct {
	EventID string
	Title   string
	Level   string
	TimeAgo string
}

type traceTransactionRow struct {
	EventID     string
	Transaction string
	Op          string
	Status      string
	Release     string
	Environment string
	Duration    string
	TimeAgo     string
}

type traceSpanRow struct {
	SpanID       string
	ParentSpanID string
	Op           string
	Description  string
	Status       string
	Duration     string
}

type traceProfileRow struct {
	ID          string
	Transaction string
	Release     string
	Environment string
	Duration    string
	SampleCount string
	TopFunction string
	StartedAt   string
}

func (h *Handler) traceDetailPage(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		http.Error(w, "Web UI unavailable", http.StatusServiceUnavailable)
		return
	}
	traceID := strings.TrimSpace(r.PathValue("trace_id"))
	scope, ok := h.guardProjectQueryPage(w, r, sqlite.QueryWorkloadTransactions, 1, traceID, true)
	if !ok {
		return
	}
	transactions, err := h.queries.ListTransactionsByTrace(r.Context(), scope.ProjectID, traceID)
	if err != nil {
		http.Error(w, "Failed to load trace transactions.", http.StatusInternalServerError)
		return
	}
	spans, err := h.queries.ListTraceSpans(r.Context(), scope.ProjectID, traceID)
	if err != nil {
		http.Error(w, "Failed to load trace spans.", http.StatusInternalServerError)
		return
	}
	relatedProfiles, err := h.queries.FindProfilesByTrace(r.Context(), scope.ProjectID, traceID, 10)
	if err != nil {
		http.Error(w, "Failed to load related profiles.", http.StatusInternalServerError)
		return
	}
	linkedErrors, _ := listErrorsByTrace(r.Context(), h.db, scope.ProjectID, traceID, 20)

	if len(transactions) == 0 && len(spans) == 0 && len(relatedProfiles) == 0 {
		http.NotFound(w, r)
		return
	}

	data := traceDetailData{
		Title:        traceID,
		Nav:          "profiles",
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(r.Context()),
		TraceID:      traceID,
		Guide:        traceGuide(),
	}
	for _, item := range transactions {
		data.Transactions = append(data.Transactions, traceTransactionRow{
			EventID:     item.EventID,
			Transaction: item.Transaction,
			Op:          item.Op,
			Status:      item.Status,
			Release:     item.ReleaseID,
			Environment: item.Environment,
			Duration:    formatTraceDuration(item.DurationMS),
			TimeAgo:     timeAgo(firstNonZeroTime(item.EndTimestamp, item.StartTimestamp)),
		})
	}
	for _, item := range spans {
		data.Spans = append(data.Spans, traceSpanRow{
			SpanID:       item.SpanID,
			ParentSpanID: item.ParentSpanID,
			Op:           item.Op,
			Description:  item.Description,
			Status:       item.Status,
			Duration:     formatTraceDuration(item.DurationMS),
		})
	}
	for _, item := range relatedProfiles {
		data.Profiles = append(data.Profiles, traceProfileRow{
			ID:          item.ProfileID,
			Transaction: item.Transaction,
			Release:     item.Release,
			Environment: item.Environment,
			Duration:    formatProfileDuration(item.DurationNS),
			SampleCount: formatNumber(item.SampleCount),
			TopFunction: item.TopFunction,
			StartedAt:   timeAgo(item.StartedAt),
		})
	}
	for _, item := range linkedErrors {
		data.Errors = append(data.Errors, traceErrorRow{
			EventID: item.eventID,
			Title:   item.title,
			Level:   item.level,
			TimeAgo: timeAgo(item.occurredAt),
		})
	}
	data.Waterfall = buildWaterfall(transactions, spans)

	h.render(w, "trace-detail.html", data)
}

func formatTraceDuration(value float64) string {
	if value <= 0 {
		return "-"
	}
	return formatFloat(value, 0) + " ms"
}

func formatProfileDuration(value int64) string {
	if value <= 0 {
		return "-"
	}
	return formatFloat(float64(value)/1_000_000, 1) + " ms"
}

func formatFloat(value float64, digits int) string {
	return strconv.FormatFloat(value, 'f', digits, 64)
}

// linkedError is the internal row type returned by listErrorsByTrace.
type linkedError struct {
	eventID    string
	title      string
	level      string
	occurredAt time.Time
}

// listErrorsByTrace returns error/fatal events whose contexts.trace.trace_id
// matches the given traceID.
func listErrorsByTrace(ctx context.Context, db *sql.DB, projectID, traceID string, limit int) ([]linkedError, error) {
	if db == nil || traceID == "" {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx,
		`SELECT event_id, COALESCE(title, ''), COALESCE(level, 'error'), COALESCE(occurred_at, '')
		 FROM events
		 WHERE project_id = ?
		   AND COALESCE(json_extract(payload_json, '$.contexts.trace.trace_id'), '') = ?
		   AND COALESCE(event_type, 'error') IN ('error', 'default')
		 ORDER BY occurred_at DESC
		 LIMIT ?`,
		projectID, traceID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []linkedError
	for rows.Next() {
		var e linkedError
		var occurredAt sql.NullString
		if err := rows.Scan(&e.eventID, &e.title, &e.level, &occurredAt); err != nil {
			return nil, err
		}
		e.occurredAt = sqlutil.ParseDBTime(sqlutil.NullStr(occurredAt))
		out = append(out, e)
	}
	return out, rows.Err()
}
