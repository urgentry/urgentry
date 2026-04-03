package ingest

import (
	"net/http"

	"urgentry/internal/metrics"
	"urgentry/internal/pipeline"
	"urgentry/internal/trace"
)

// OTLPLogsHandler handles OTLP/HTTP JSON logs for a project.
func OTLPLogsHandler(pipe *pipeline.Pipeline, met *metrics.Metrics) http.Handler {
	return newOTLPHandler(pipe, met,
		"binary otlp logs are not supported",
		"invalid otlp log payload",
		trace.TranslateOTLPLogsJSON,
	)
}
