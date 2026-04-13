package ingest

import (
	"context"
	"encoding/json"
	"net/http"

	"urgentry/internal/metrics"
	"urgentry/internal/sqlite"
	"urgentry/internal/trace"
)

// OTLPMetricsHandler handles OTLP/HTTP JSON metrics for a project.
// It translates the payload into MetricBucketEvent records and writes them
// directly to the MetricBucketStore (bypassing the event pipeline, which
// routes events through the issue/group processor).
func OTLPMetricsHandler(store *sqlite.MetricBucketStore, met *metrics.Metrics) http.Handler {
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
			writeOTLPStatus(w, contentType, http.StatusUnsupportedMediaType, "binary otlp metrics are not supported")
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

		events, err := trace.TranslateOTLPMetricsJSON(payload)
		if err != nil {
			if met != nil {
				met.RecordIngest(len(payload), err)
			}
			writeOTLPStatus(w, contentType, http.StatusBadRequest, "invalid otlp metrics payload")
			return
		}

		projectID := r.PathValue("project_id")
		if projectID == "" {
			projectID = "1"
		}

		if len(events) > 0 && store != nil {
			buckets := make([]*sqlite.MetricBucket, 0, len(events))
			for _, raw := range events {
				var evt trace.MetricBucketEvent
				if err := json.Unmarshal(raw, &evt); err != nil {
					continue
				}
				buckets = append(buckets, &sqlite.MetricBucket{
					ProjectID: projectID,
					Name:      evt.Name,
					Type:      evt.Type,
					Value:     evt.Value,
					Unit:      evt.Unit,
					Tags:      evt.Tags,
					Timestamp: evt.Timestamp,
				})
			}
			if len(buckets) > 0 {
				if err := store.SaveMetricBuckets(context.Background(), buckets); err != nil {
					if met != nil {
						met.RecordIngest(len(payload), err)
					}
					writeOTLPStatus(w, contentType, http.StatusInternalServerError, "failed to save metrics")
					return
				}
			}
		}

		if met != nil {
			met.RecordIngest(len(payload), nil)
		}
		writeOTLPJSON(w, contentType, http.StatusOK, otlpTraceExportResponse{})
	})
}

