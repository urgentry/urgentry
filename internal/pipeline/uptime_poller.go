package pipeline

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"urgentry/internal/alert"
	"urgentry/internal/outboundhttp"
	"urgentry/internal/sqlite"
)

// UptimePoller checks due uptime monitors by issuing HTTP requests.
type UptimePoller struct {
	store  *sqlite.UptimeMonitorStore
	alerts *AlertDeps
	client *http.Client
}

// NewUptimePoller creates a new UptimePoller.
func NewUptimePoller(store *sqlite.UptimeMonitorStore, alerts *AlertDeps) *UptimePoller {
	return &UptimePoller{
		store:  store,
		alerts: alerts,
		client: func() *http.Client {
			client := outboundhttp.NewClient(30*time.Second, nil)
			client.CheckRedirect = func(_ *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			}
			return client
		}(),
	}
}

// PollDue checks all uptime monitors that are due for a poll.
func (p *UptimePoller) PollDue(ctx context.Context, now time.Time) error {
	if p.store == nil {
		return nil
	}
	due, err := p.store.ListDueUptimeMonitors(ctx, now)
	if err != nil {
		return fmt.Errorf("list due uptime monitors: %w", err)
	}
	for _, monitor := range due {
		p.pollOne(ctx, &monitor)
	}
	return nil
}

func (p *UptimePoller) pollOne(ctx context.Context, monitor *sqlite.UptimeMonitor) {
	timeout := time.Duration(monitor.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if _, err := outboundhttp.ValidateTargetURL(monitor.URL); err != nil {
		p.recordError(ctx, monitor, 0, 0, fmt.Sprintf("disallowed target: %v", err))
		return
	}

	req, err := http.NewRequestWithContext(pollCtx, http.MethodGet, monitor.URL, nil)
	if err != nil {
		p.recordError(ctx, monitor, 0, 0, fmt.Sprintf("invalid url: %v", err))
		return
	}
	req.Header.Set("User-Agent", "Urgentry-Uptime/1.0")

	start := time.Now()
	resp, err := p.client.Do(req)
	latencyMS := float64(time.Since(start).Microseconds()) / 1000.0

	if err != nil {
		p.recordError(ctx, monitor, 0, latencyMS, fmt.Sprintf("request failed: %v", err))
		return
	}
	defer resp.Body.Close()

	statusCode := resp.StatusCode
	expected := monitor.ExpectedStatus
	if expected <= 0 {
		expected = 200
	}

	if statusCode < 200 || statusCode >= 300 || statusCode != expected {
		p.recordError(ctx, monitor, statusCode, latencyMS, fmt.Sprintf("expected %d, got %d", expected, statusCode))
		return
	}

	// Success
	result := &sqlite.UptimeCheckResult{
		UptimeMonitorID: monitor.ID,
		ProjectID:       monitor.ProjectID,
		StatusCode:      statusCode,
		LatencyMS:       latencyMS,
		Status:          "ok",
		DateCreated:     time.Now().UTC(),
	}
	if err := p.store.SaveCheckResult(ctx, result); err != nil {
		log.Error().Err(err).Str("monitor_id", monitor.ID).Msg("uptime: failed to save check result")
	}
}

func (p *UptimePoller) recordError(ctx context.Context, monitor *sqlite.UptimeMonitor, statusCode int, latencyMS float64, errMsg string) {
	result := &sqlite.UptimeCheckResult{
		UptimeMonitorID: monitor.ID,
		ProjectID:       monitor.ProjectID,
		StatusCode:      statusCode,
		LatencyMS:       latencyMS,
		Error:           errMsg,
		Status:          "error",
		DateCreated:     time.Now().UTC(),
	}
	if err := p.store.SaveCheckResult(ctx, result); err != nil {
		log.Error().Err(err).Str("monitor_id", monitor.ID).Msg("uptime: failed to save error result")
	}

	log.Warn().
		Str("monitor_id", monitor.ID).
		Str("url", monitor.URL).
		Int("status_code", statusCode).
		Str("error", errMsg).
		Msg("uptime: monitor check failed")

	// Fire alert signal on failure.
	if p.alerts != nil && p.alerts.Evaluator != nil {
		DispatchAlertSignal(ctx, *p.alerts, monitor.ProjectID, alert.Signal{
			ProjectID:   monitor.ProjectID,
			EventID:     result.ID,
			EventType:   alert.EventTypeMonitor,
			MonitorSlug: monitor.Name,
			Status:      "error",
			Timestamp:   result.DateCreated,
		})
	}
}
