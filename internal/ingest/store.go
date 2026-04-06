package ingest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"urgentry/internal/httputil"
	"urgentry/internal/metrics"
	"urgentry/internal/pipeline"
	"urgentry/pkg/id"
)

const maxStoreBodySize = 1 << 20 // 1 MB

// StoreHandler handles the legacy POST /api/{project_id}/store/ endpoint.
// If pipe is non-nil, accepted events are enqueued for async processing.
func StoreHandler(pipe *pipeline.Pipeline) http.Handler {
	return StoreHandlerWithMetrics(pipe, nil)
}

// StoreHandlerWithMetrics is like StoreHandler but records ingest metrics.
func StoreHandlerWithMetrics(pipe *pipeline.Pipeline, met *metrics.Metrics) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			httputil.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		// Enforce body size limit.
		limited := http.MaxBytesReader(w, r.Body, maxStoreBodySize)
		body, err := io.ReadAll(limited)
		if err != nil {
			if met != nil {
				met.RecordIngest(0, err)
			}
			httputil.WriteError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}

		if len(body) == 0 {
			if met != nil {
				met.RecordIngest(0, errEmptyBody)
			}
			httputil.WriteError(w, http.StatusBadRequest, "empty body")
			return
		}

		// Validate JSON without materializing the whole payload into a map.
		if !json.Valid(body) {
			if met != nil {
				met.RecordIngest(len(body), err)
			}
			httputil.WriteError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		// Extract or generate event_id.
		eventID := extractEventID(body)
		if eventID == "" {
			eventID = id.New()
		}

		// Enqueue for async processing (normalize -> group -> persist).
		if pipe != nil {
			projectID := r.PathValue("project_id")
			if projectID == "" {
				projectID = "1"
			}
			if ok := pipe.EnqueueContext(r.Context(), pipeline.Item{
				ProjectID: projectID,
				RawEvent:  body,
			}); !ok {
				if met != nil {
					met.RecordIngest(len(body), errQueueFull)
				}
				httputil.WriteError(w, http.StatusServiceUnavailable, "ingest queue is full, retry later")
				return
			}
		}

		// Record successful ingest.
		if met != nil {
			met.RecordIngest(len(body), nil)
		}

		httputil.WriteJSON(w, http.StatusOK, map[string]string{"id": eventID})
	})
}

var errEmptyBody = fmt.Errorf("empty body")
var errQueueFull = fmt.Errorf("queue full")
var errSpikeThrottled = fmt.Errorf("spike throttled")

// extractEventID pulls event_id from the raw payload. Returns "" if absent,
// malformed, or not a string.
func extractEventID(body []byte) string {
	var event struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		return ""
	}
	return event.EventID
}
