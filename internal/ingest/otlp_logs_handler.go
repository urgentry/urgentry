package ingest

import (
	"net/http"

	"urgentry/internal/metrics"
	"urgentry/internal/pipeline"
	"urgentry/internal/trace"
)

// OTLPLogsHandler handles OTLP/HTTP JSON logs for a project.
func OTLPLogsHandler(pipe *pipeline.Pipeline, met *metrics.Metrics) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType := otlpResponseContentType(r.Header.Get("Content-Type"))
		if r.Method != http.MethodPost {
			writeOTLPStatus(w, contentType, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if contentType != otlpContentTypeJSON && contentType != otlpContentTypeProtobuf {
			writeOTLPStatus(w, otlpContentTypeJSON, http.StatusUnsupportedMediaType, "unsupported otlp content type")
			return
		}
		if contentType == otlpContentTypeProtobuf {
			writeOTLPStatus(w, contentType, http.StatusUnsupportedMediaType, "binary otlp logs are not supported")
			return
		}
		payload, err := readOTLPBody(w, r)
		if err != nil {
			if met != nil {
				met.RecordIngest(0, err)
			}
			writeOTLPStatus(w, contentType, http.StatusBadRequest, err.Error())
			return
		}
		items, err := trace.TranslateOTLPLogsJSON(payload)
		if err != nil {
			if met != nil {
				met.RecordIngest(len(payload), err)
			}
			writeOTLPStatus(w, contentType, http.StatusBadRequest, "invalid otlp log payload")
			return
		}
		projectID := r.PathValue("project_id")
		if projectID == "" {
			projectID = "1"
		}
		for _, item := range items {
			if pipe == nil || !pipe.EnqueueNonBlocking(pipeline.Item{ProjectID: projectID, RawEvent: item}) {
				if met != nil {
					met.RecordIngest(len(payload), errQueueFull)
				}
				w.Header().Set("Retry-After", "1")
				writeOTLPStatus(w, contentType, http.StatusServiceUnavailable, "ingest queue is full, retry later")
				return
			}
		}
		if met != nil {
			met.RecordIngest(len(payload), nil)
		}
		writeOTLPJSON(w, contentType, http.StatusOK, otlpTraceExportResponse{})
	})
}
