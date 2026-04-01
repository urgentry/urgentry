package web

import (
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
	Name           string
	ProcessCount   int64
	PublishCount   int64
	AvgProcessing  string // formatted ms
	P95Processing  string
	FailureCount   int64
	FailureRate    string // e.g. "2.3%"
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
			CASE
				WHEN SUM(CASE WHEN op = 'queue.process' THEN 1 ELSE 0 END) > 0
				THEN (SELECT s2.duration_ms FROM spans s2
					WHERE s2.op = 'queue.process'
					AND COALESCE(s2.description, '(unknown)') = COALESCE(description, '(unknown)')
					AND s2.start_timestamp >= ?
					ORDER BY s2.duration_ms DESC
					LIMIT 1 OFFSET (
						CAST(SUM(CASE WHEN op = 'queue.process' THEN 1 ELSE 0 END) * 0.05 AS INTEGER)
					))
				ELSE NULL
			END AS p95_processing,
			SUM(CASE WHEN op = 'queue.process' AND status != 'ok' AND status != '' THEN 1 ELSE 0 END) AS failure_count
		 FROM spans
		 WHERE op IN ('queue.process', 'queue.publish')
		   AND start_timestamp >= ?
		 GROUP BY queue_name
		 ORDER BY process_count DESC
		 LIMIT 50`,
		since, since,
	)
	if err != nil {
		data.Error = "Failed to query queue spans: " + err.Error()
		h.render(w, "performance-queues.html", data)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var qr queueRow
		var avgProcessing, p95Processing *float64
		if err := rows.Scan(&qr.Name, &qr.ProcessCount, &qr.PublishCount, &avgProcessing, &p95Processing, &qr.FailureCount); err != nil {
			continue
		}
		if avgProcessing != nil {
			qr.AvgProcessing = fmt.Sprintf("%.1f", *avgProcessing)
		}
		if p95Processing != nil {
			qr.P95Processing = fmt.Sprintf("%.1f", *p95Processing)
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
