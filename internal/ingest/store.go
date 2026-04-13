package ingest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"urgentry/internal/httputil"
	"urgentry/internal/metrics"
	"urgentry/internal/pipeline"
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
		requestStarted := time.Now()
		var requestErr error
		defer func() {
			if met != nil {
				met.RecordStage(metrics.StageIngestRequest, time.Since(requestStarted), requestErr)
			}
		}()
		if r.Method != http.MethodPost {
			requestErr = fmt.Errorf("method not allowed")
			httputil.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		// Enforce body size limit.
		limited := http.MaxBytesReader(w, r.Body, maxStoreBodySize)
		body, err := io.ReadAll(limited)
		if err != nil {
			requestErr = err
			if met != nil {
				met.RecordIngest(0, err)
			}
			httputil.WriteError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}

		if len(body) == 0 {
			requestErr = errEmptyBody
			if met != nil {
				met.RecordIngest(0, errEmptyBody)
			}
			httputil.WriteError(w, http.StatusBadRequest, "empty body")
			return
		}

		eventID := extractJSONEventID(body)
		fastPath := eventID != ""
		// Validate JSON without materializing the whole payload into a map.
		if !fastPath && !json.Valid(body) {
			requestErr = errInvalidJSON
			if met != nil {
				met.RecordIngest(len(body), requestErr)
			}
			httputil.WriteError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		if !fastPath {
			body, eventID, err = canonicalizeStorePayload(body)
			if err != nil {
				requestErr = errInvalidJSON
				if met != nil {
					met.RecordIngest(len(body), requestErr)
				}
				httputil.WriteError(w, http.StatusBadRequest, "invalid JSON")
				return
			}
		}

		// Enqueue for async processing (normalize -> group -> persist).
		if pipe != nil {
			projectID := r.PathValue("project_id")
			if projectID == "" {
				projectID = "1"
			}
			enqueueStarted := time.Now()
			ok := pipe.EnqueueContext(r.Context(), pipeline.Item{
				ProjectID: projectID,
				RawEvent:  body,
			})
			if met != nil {
				enqueueErr := error(nil)
				if !ok {
					enqueueErr = errQueueFull
				}
				met.RecordStage(metrics.StageEnqueue, time.Since(enqueueStarted), enqueueErr)
			}
			if !ok {
				requestErr = errQueueFull
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

		writeStoreAccepted(w, eventID)
	})
}

var errEmptyBody = fmt.Errorf("empty body")
var errQueueFull = fmt.Errorf("queue full")
var errSpikeThrottled = fmt.Errorf("spike throttled")
var errInvalidJSON = fmt.Errorf("invalid JSON")

func writeStoreAccepted(w http.ResponseWriter, eventID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `{"id":"`)
	_, _ = io.WriteString(w, eventID)
	_, _ = io.WriteString(w, `"}`+"\n")
}
