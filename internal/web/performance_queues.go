package web

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"urgentry/internal/discovershared"
)

// ---------------------------------------------------------------------------
// Performance Queues Page
// ---------------------------------------------------------------------------

type performanceQueuesPageData struct {
	Title        string
	Nav          string
	Environment  string
	Environments []string
	Queues       []queueRow
	Error        string
}

type queueRow struct {
	Name          string
	ProcessCount  int64
	PublishCount  int64
	AvgProcessing string // formatted ms
	P95Processing string
	FailureCount  int64
	FailureRate   string // e.g. "2.3%"
}

func (h *Handler) performanceQueuesPage(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeWebUnavailable(w, r, "Web UI unavailable")
		return
	}

	data := performanceQueuesPageData{
		Title:        "Queue Performance",
		Nav:          "performance",
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(r.Context()),
	}

	timeRange := "24h"
	dur, err := discovershared.ParseDiscoverInterval(timeRange)
	if err != nil {
		data.Error = "Failed to parse time range."
		h.render(w, "performance-queues.html", data)
		return
	}
	since := time.Now().UTC().Add(-dur).Format(time.RFC3339)

	// Aggregate queue spans by description (queue name), split by op.
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT
			COALESCE(description, '(unknown)') AS queue_name,
			SUM(CASE WHEN op = 'queue.process' THEN 1 ELSE 0 END) AS process_count,
			SUM(CASE WHEN op = 'queue.publish' THEN 1 ELSE 0 END) AS publish_count,
			AVG(CASE WHEN op = 'queue.process' THEN duration_ms END) AS avg_processing,
			SUM(CASE WHEN op = 'queue.process' AND status != 'ok' AND status != '' THEN 1 ELSE 0 END) AS failure_count
		 FROM spans
		 WHERE op IN ('queue.process', 'queue.publish')
		   AND start_timestamp >= ?
		 GROUP BY queue_name
		 ORDER BY process_count DESC
		 LIMIT 50`,
		since,
	)
	if err != nil {
		data.Error = "Failed to query queue spans: " + err.Error()
		h.render(w, "performance-queues.html", data)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var qr queueRow
		var avgProcessing *float64
		if err := rows.Scan(&qr.Name, &qr.ProcessCount, &qr.PublishCount, &avgProcessing, &qr.FailureCount); err != nil {
			continue
		}
		if avgProcessing != nil {
			qr.AvgProcessing = fmt.Sprintf("%.1f", *avgProcessing)
		}
		if p95, p95Err := h.queueP95Processing(r.Context(), qr.Name, since); p95Err == nil && p95 != nil {
			qr.P95Processing = fmt.Sprintf("%.1f", *p95)
		}
		if qr.ProcessCount > 0 {
			rate := float64(qr.FailureCount) / float64(qr.ProcessCount) * 100
			qr.FailureRate = fmt.Sprintf("%.1f%%", rate)
		} else {
			qr.FailureRate = "-"
		}
		data.Queues = append(data.Queues, qr)
	}

	h.render(w, "performance-queues.html", data)
}

func (h *Handler) queueP95Processing(ctx context.Context, queueName, since string) (*float64, error) {
	if h.db == nil {
		return nil, nil
	}
	var count int64
	if err := h.db.QueryRowContext(ctx,
		`SELECT COUNT(*)
		 FROM spans
		 WHERE op = 'queue.process'
		   AND COALESCE(description, '(unknown)') = ?
		   AND start_timestamp >= ?`,
		queueName, since,
	).Scan(&count); err != nil || count == 0 {
		return nil, err
	}
	offset := int64(float64(count-1) * 0.05)
	if offset < 0 {
		offset = 0
	}
	var value float64
	if err := h.db.QueryRowContext(ctx,
		`SELECT duration_ms
		 FROM spans
		 WHERE op = 'queue.process'
		   AND COALESCE(description, '(unknown)') = ?
		   AND start_timestamp >= ?
		 ORDER BY duration_ms DESC
		 LIMIT 1 OFFSET ?`,
		queueName, since, offset,
	).Scan(&value); err != nil {
		return nil, err
	}
	return &value, nil
}
