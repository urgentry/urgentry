package ingest

import (
	"bytes"
	"net/http"

	"urgentry/internal/httputil"
	"urgentry/internal/metrics"
	"urgentry/internal/pipeline"
	"urgentry/internal/securityreport"
)

const maxSecurityReportBodySize = 1 << 20 // 1 MB

// SecurityReportHandler handles browser security reports for a project.
func SecurityReportHandler(pipe *pipeline.Pipeline, met *metrics.Metrics) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			httputil.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		body := http.MaxBytesReader(w, r.Body, maxSecurityReportBodySize)
		payload := new(bytes.Buffer)
		if _, err := payload.ReadFrom(body); err != nil {
			if met != nil {
				met.RecordIngest(0, err)
			}
			httputil.WriteError(w, http.StatusBadRequest, "invalid security report body")
			return
		}
		items, err := securityreport.TranslateJSON(payload.Bytes())
		if err != nil {
			if met != nil {
				met.RecordIngest(payload.Len(), err)
			}
			httputil.WriteError(w, http.StatusBadRequest, "invalid security report payload")
			return
		}
		projectID := r.PathValue("project_id")
		if projectID == "" {
			projectID = "1"
		}
		for _, item := range items {
			if pipe == nil || !pipe.EnqueueNonBlocking(pipeline.Item{ProjectID: projectID, RawEvent: item}) {
				if met != nil {
					met.RecordIngest(payload.Len(), errQueueFull)
				}
				httputil.WriteError(w, http.StatusServiceUnavailable, "ingest queue is full, retry later")
				return
			}
		}
		if met != nil {
			met.RecordIngest(payload.Len(), nil)
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]int{"accepted": len(items)})
	})
}
