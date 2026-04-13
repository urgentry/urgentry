package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"urgentry/internal/alert"
	"urgentry/internal/attachment"
	"urgentry/internal/controlplane"
	"urgentry/internal/envelope"
	"urgentry/internal/httputil"
	"urgentry/internal/metrics"
	"urgentry/internal/middleware"
	"urgentry/internal/pipeline"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

const maxEnvelopeBodySize = 10 << 20 // 10 MB

var missingAttachmentStorageProjects sync.Map

// IngestDeps holds optional dependencies for the envelope handler.
// All fields may be nil (graceful degradation).
type IngestDeps struct {
	Pipeline        *pipeline.Pipeline
	AlertDeps       *pipeline.AlertDeps
	EventStore      store.EventStore
	ReplayStore     store.ReplayIngestStore
	ReplayPolicies  store.ReplayPolicyStore
	ProfileStore    store.ProfileIngestStore
	FeedbackStore   *sqlite.FeedbackStore
	AttachmentStore attachment.Store
	BlobStore       store.BlobStore
	DebugFiles      *sqlite.DebugFileStore
	NativeCrashes   *sqlite.NativeCrashStore
	SessionStore    *sqlite.ReleaseHealthStore
	OutcomeStore    *sqlite.OutcomeStore
	MonitorStore    controlplane.MonitorStore
	SamplingRules   *sqlite.SamplingRuleStore
	MetricBuckets   *sqlite.MetricBucketStore
	SpikeThrottle   *pipeline.SpikeThrottle
	Metrics         *metrics.Metrics
	WALMonitor      *WALMonitor
}

// EnvelopeHandler handles POST /api/{project_id}/envelope/.
// If pipe is non-nil, event items are enqueued for async processing.
func EnvelopeHandler(pipe *pipeline.Pipeline) http.Handler {
	return EnvelopeHandlerWithDeps(IngestDeps{Pipeline: pipe})
}

// EnvelopeHandlerWithDeps handles POST /api/{project_id}/envelope/ with
// full dependency injection for all item types.
func EnvelopeHandlerWithDeps(deps IngestDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := middleware.LogFromCtx(r.Context())
		requestStarted := time.Now()
		var requestErr error
		defer func() {
			if deps.Metrics != nil {
				deps.Metrics.RecordStage(metrics.StageIngestRequest, time.Since(requestStarted), requestErr)
			}
		}()
		writeErr := func(code int, msg string, err error) {
			requestErr = err
			httputil.WriteError(w, code, msg)
		}

		if r.Method != http.MethodPost {
			writeErr(http.StatusMethodNotAllowed, "method not allowed", fmt.Errorf("method not allowed"))
			return
		}

		// Circuit breaker: shed load when the SQLite WAL exceeds the size limit.
		if deps.WALMonitor.WALSizeExceeded() {
			w.Header().Set("Retry-After", "30")
			writeErr(http.StatusServiceUnavailable, "WAL size limit exceeded, retry later", fmt.Errorf("wal size limit exceeded"))
			return
		}

		limited := http.MaxBytesReader(w, r.Body, maxEnvelopeBodySize)
		body, err := io.ReadAll(limited)
		if err != nil {
			if deps.Metrics != nil {
				deps.Metrics.RecordIngest(0, err)
			}
			writeErr(http.StatusRequestEntityTooLarge, "request body too large", err)
			return
		}

		env, err := envelope.Parse(body)
		if err != nil {
			if deps.Metrics != nil {
				deps.Metrics.RecordIngest(len(body), err)
			}
			writeErr(http.StatusBadRequest, "invalid envelope: "+err.Error(), err)
			return
		}

		eventID, err := canonicalizeEnvelopeForIngest(env)
		if err != nil {
			if deps.Metrics != nil {
				deps.Metrics.RecordIngest(len(body), err)
			}
			writeErr(http.StatusBadRequest, "invalid envelope: "+err.Error(), err)
			return
		}

		projectID := r.PathValue("project_id")
		if projectID == "" {
			projectID = "1"
		}

		ctx := r.Context()

		// Phase 1: Attempt queue admission for event/transaction items
		// before persisting any side effects. If the queue rejects, we
		// return 503 without writing attachments, replays, sessions, etc.
		if deps.Pipeline != nil {
			for _, item := range env.Items {
				switch item.Header.Type {
				case "event", "transaction":
					// Apply server-side sampling for transactions.
					if item.Header.Type == "transaction" && deps.SamplingRules != nil {
						txEnv, txRel, txName := extractTransactionSamplingFields(item.Payload)
						admitted, evalErr := deps.SamplingRules.EvaluateSampling(ctx, projectID, txEnv, txRel, txName)
						if evalErr != nil {
							l.Warn().Err(evalErr).Str("project_id", projectID).Msg("envelope: sampling evaluation failed, admitting event")
						} else if !admitted {
							if deps.OutcomeStore != nil {
								_ = deps.OutcomeStore.SaveOutcome(ctx, &sqlite.Outcome{
									ProjectID:   projectID,
									EventID:     eventID,
									Category:    "transaction",
									Reason:      "sample_rate",
									Quantity:    1,
									Source:      "server_sampling",
									RecordedAt:  time.Now().UTC(),
									DateCreated: time.Now().UTC(),
								})
							}
							continue
						}
					}
					// Spike protection: throttle when volume exceeds baseline.
					if deps.SpikeThrottle != nil && !deps.SpikeThrottle.Allow(ctx, projectID) {
						if deps.Metrics != nil {
							deps.Metrics.RecordIngest(len(body), errSpikeThrottled)
						}
						writeErr(http.StatusTooManyRequests, "event rate exceeded, spike protection active", errSpikeThrottled)
						return
					}
					enqueueStarted := time.Now()
					ok := deps.Pipeline.EnqueueContext(ctx, pipeline.Item{
						ProjectID: projectID,
						RawEvent:  item.Payload,
					})
					if deps.Metrics != nil {
						enqueueErr := error(nil)
						if !ok {
							enqueueErr = errQueueFull
						}
						deps.Metrics.RecordStage(metrics.StageEnqueue, time.Since(enqueueStarted), enqueueErr)
					}
					if !ok {
						if deps.Metrics != nil {
							deps.Metrics.RecordIngest(len(body), errQueueFull)
						}
						writeErr(http.StatusServiceUnavailable, "ingest queue is full, retry later", errQueueFull)
						return
					}
				}
			}
		}

		if err := persistEnvelopeSideEffects(ctx, deps, env, projectID, eventID); err != nil {
			if deps.Metrics != nil {
				deps.Metrics.RecordIngest(len(body), err)
			}
			writeErr(http.StatusInternalServerError, err.Error(), err)
			return
		}

		// Record successful ingest.
		if deps.Metrics != nil {
			deps.Metrics.RecordIngest(len(body), nil)
		}

		httputil.WriteJSON(w, http.StatusOK, map[string]string{"id": eventID})
	})
}

type sessionPayload struct {
	SessionID   string            `json:"sid"`
	DistinctID  string            `json:"did"`
	Status      string            `json:"status"`
	Errors      int               `json:"errors"`
	Started     string            `json:"started"`
	Duration    float64           `json:"duration"`
	Release     string            `json:"release"`
	Environment string            `json:"environment"`
	Attrs       map[string]string `json:"attrs"`
	UserAgent   string            `json:"user_agent"`
}

type sessionAggregatesPayload struct {
	Release     string             `json:"release"`
	Environment string             `json:"environment"`
	Attrs       map[string]string  `json:"attrs"`
	Aggregates  []sessionAggregate `json:"aggregates"`
}

type sessionAggregate struct {
	Started  string `json:"started"`
	Exited   int    `json:"exited"`
	Errored  int    `json:"errored"`
	Abnormal int    `json:"abnormal"`
	Crashed  int    `json:"crashed"`
}

type clientReportPayload struct {
	Timestamp       string                       `json:"timestamp"`
	DiscardedEvents []clientReportDiscardedEvent `json:"discarded_events"`
}

type clientReportDiscardedEvent struct {
	Reason   string `json:"reason"`
	Category string `json:"category"`
	Quantity int    `json:"quantity"`
}

type checkInPayload struct {
	CheckInID     string                `json:"check_in_id"`
	MonitorSlug   string                `json:"monitor_slug"`
	Status        string                `json:"status"`
	Duration      float64               `json:"duration"`
	Release       string                `json:"release"`
	Environment   string                `json:"environment"`
	ScheduledFor  string                `json:"scheduled_for"`
	MonitorConfig *sqlite.MonitorConfig `json:"monitor_config"`
}

func saveSession(ctx context.Context, store *sqlite.ReleaseHealthStore, projectID string, payload []byte) {
	l := middleware.LogFromCtx(ctx)
	if store == nil {
		l.Debug().Str("project_id", projectID).Msg("envelope: session received but no release-health store configured")
		return
	}

	var session sessionPayload
	if err := json.Unmarshal(payload, &session); err != nil {
		l.Warn().Err(err).Str("project_id", projectID).Msg("envelope: failed to parse session payload")
		return
	}

	release, environment, userAgent := sessionAttrs(session.Release, session.Environment, session.UserAgent, session.Attrs)
	if release == "" {
		l.Debug().Str("project_id", projectID).Msg("envelope: session missing release, skipping")
		return
	}

	startedAt := parseSessionTime(session.Started)
	if err := store.SaveSession(ctx, &sqlite.ReleaseSession{
		ProjectID:   projectID,
		Release:     release,
		Environment: environment,
		SessionID:   strings.TrimSpace(session.SessionID),
		DistinctID:  strings.TrimSpace(session.DistinctID),
		Status:      strings.TrimSpace(session.Status),
		Errors:      session.Errors,
		StartedAt:   startedAt,
		Duration:    session.Duration,
		UserAgent:   userAgent,
		Attrs:       session.Attrs,
		Quantity:    1,
		DateCreated: time.Now().UTC(),
	}); err != nil {
		l.Error().Err(err).Str("project_id", projectID).Str("release", release).Msg("envelope: failed to save session")
	}
}

func saveSessionAggregates(ctx context.Context, store *sqlite.ReleaseHealthStore, alertDeps *pipeline.AlertDeps, projectID string, payload []byte) {
	l := middleware.LogFromCtx(ctx)
	if store == nil {
		l.Debug().Str("project_id", projectID).Msg("envelope: session aggregates received but no release-health store configured")
		return
	}

	var aggregates sessionAggregatesPayload
	if err := json.Unmarshal(payload, &aggregates); err != nil {
		l.Warn().Err(err).Str("project_id", projectID).Msg("envelope: failed to parse session aggregates payload")
		return
	}

	release, environment, userAgent := sessionAttrs(aggregates.Release, aggregates.Environment, "", aggregates.Attrs)
	if release == "" {
		l.Debug().Str("project_id", projectID).Msg("envelope: session aggregates missing release, skipping")
		return
	}

	for _, aggregate := range aggregates.Aggregates {
		startedAt := parseSessionTime(aggregate.Started)
		saveAggregateCount(ctx, store, sqlite.ReleaseSession{
			ProjectID:   projectID,
			Release:     release,
			Environment: environment,
			Status:      "exited",
			StartedAt:   startedAt,
			UserAgent:   userAgent,
			Attrs:       aggregates.Attrs,
			Quantity:    aggregate.Exited,
			DateCreated: time.Now().UTC(),
		})
		saveAggregateCount(ctx, store, sqlite.ReleaseSession{
			ProjectID:   projectID,
			Release:     release,
			Environment: environment,
			Status:      "errored",
			Errors:      1,
			StartedAt:   startedAt,
			UserAgent:   userAgent,
			Attrs:       aggregates.Attrs,
			Quantity:    aggregate.Errored,
			DateCreated: time.Now().UTC(),
		})
		saveAggregateCount(ctx, store, sqlite.ReleaseSession{
			ProjectID:   projectID,
			Release:     release,
			Environment: environment,
			Status:      "abnormal",
			StartedAt:   startedAt,
			UserAgent:   userAgent,
			Attrs:       aggregates.Attrs,
			Quantity:    aggregate.Abnormal,
			DateCreated: time.Now().UTC(),
		})
		saveAggregateCount(ctx, store, sqlite.ReleaseSession{
			ProjectID:   projectID,
			Release:     release,
			Environment: environment,
			Status:      "crashed",
			StartedAt:   startedAt,
			UserAgent:   userAgent,
			Attrs:       aggregates.Attrs,
			Quantity:    aggregate.Crashed,
			DateCreated: time.Now().UTC(),
		})
	}
	dispatchReleaseAlert(ctx, alertDeps, store, projectID, release, environment)
}

func saveAggregateCount(ctx context.Context, store *sqlite.ReleaseHealthStore, session sqlite.ReleaseSession) {
	if session.Quantity <= 0 {
		return
	}
	l := middleware.LogFromCtx(ctx)
	if err := store.SaveSession(ctx, &session); err != nil {
		l.Error().Err(err).Str("project_id", session.ProjectID).Str("release", session.Release).Str("status", session.Status).Msg("envelope: failed to save session aggregate")
	}
}

func dispatchReleaseAlert(ctx context.Context, deps *pipeline.AlertDeps, store *sqlite.ReleaseHealthStore, projectID, release, _ string) {
	if deps == nil || deps.Evaluator == nil || store == nil {
		return
	}
	summary, err := store.GetReleaseHealth(ctx, projectID, release)
	if err != nil || summary == nil {
		return
	}
	pipeline.DispatchAlertSignal(ctx, *deps, projectID, alert.Signal{
		ProjectID:     projectID,
		EventID:       release,
		EventType:     alert.EventTypeRelease,
		Release:       release,
		CrashFreeRate: summary.CrashFreeRate,
		SessionCount:  summary.SessionCount,
		AffectedUsers: summary.AffectedUsers,
		Timestamp:     time.Now().UTC(),
	})
}

func sessionAttrs(release, environment, userAgent string, attrs map[string]string) (string, string, string) {
	release = strings.TrimSpace(release)
	environment = strings.TrimSpace(environment)
	userAgent = strings.TrimSpace(userAgent)
	if len(attrs) == 0 {
		return release, environment, userAgent
	}
	if release == "" {
		release = strings.TrimSpace(attrs["release"])
	}
	if environment == "" {
		environment = strings.TrimSpace(attrs["environment"])
	}
	if userAgent == "" {
		userAgent = strings.TrimSpace(attrs["user_agent"])
	}
	return release, environment, userAgent
}

func parseSessionTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if startedAt, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return startedAt
	}
	if startedAt, err := time.Parse(time.RFC3339, raw); err == nil {
		return startedAt
	}
	return time.Time{}
}

func saveClientReport(ctx context.Context, store *sqlite.OutcomeStore, projectID, eventID string, payload []byte) {
	l := middleware.LogFromCtx(ctx)
	if store == nil {
		l.Debug().Str("project_id", projectID).Msg("envelope: client_report received but no outcome store configured")
		return
	}

	var report clientReportPayload
	if err := json.Unmarshal(payload, &report); err != nil {
		l.Warn().Err(err).Str("project_id", projectID).Msg("envelope: failed to parse client_report payload")
		return
	}

	recordedAt := parseSessionTime(report.Timestamp)
	for idx, discarded := range report.DiscardedEvents {
		if strings.TrimSpace(discarded.Category) == "" || strings.TrimSpace(discarded.Reason) == "" {
			continue
		}
		if err := store.SaveOutcome(ctx, &sqlite.Outcome{
			ID:          stableOutcomeID(projectID, eventID, "client_report", strings.TrimSpace(discarded.Category), strings.TrimSpace(discarded.Reason), payload, idx),
			ProjectID:   projectID,
			EventID:     eventID,
			Category:    strings.TrimSpace(discarded.Category),
			Reason:      strings.TrimSpace(discarded.Reason),
			Quantity:    discarded.Quantity,
			Source:      "client_report",
			PayloadJSON: json.RawMessage(payload),
			RecordedAt:  recordedAt,
			DateCreated: time.Now().UTC(),
		}); err != nil {
			l.Error().Err(err).Str("project_id", projectID).Msg("envelope: failed to save client_report outcome")
		}
	}
}

func saveCheckIn(ctx context.Context, store controlplane.MonitorStore, projectID string, payload []byte) {
	l := middleware.LogFromCtx(ctx)
	if store == nil {
		l.Debug().Str("project_id", projectID).Msg("envelope: check_in received but no monitor store configured")
		return
	}

	var checkIn checkInPayload
	if err := json.Unmarshal(payload, &checkIn); err != nil {
		l.Warn().Err(err).Str("project_id", projectID).Msg("envelope: failed to parse check_in payload")
		return
	}
	if _, err := store.SaveCheckIn(ctx, &sqlite.MonitorCheckIn{
		ProjectID:    projectID,
		CheckInID:    strings.TrimSpace(checkIn.CheckInID),
		MonitorSlug:  strings.TrimSpace(checkIn.MonitorSlug),
		Status:       strings.TrimSpace(checkIn.Status),
		Duration:     checkIn.Duration,
		Release:      strings.TrimSpace(checkIn.Release),
		Environment:  strings.TrimSpace(checkIn.Environment),
		ScheduledFor: parseSessionTime(checkIn.ScheduledFor),
		PayloadJSON:  json.RawMessage(payload),
		DateCreated:  time.Now().UTC(),
	}, checkIn.MonitorConfig); err != nil {
		l.Error().Err(err).Str("project_id", projectID).Str("monitor_slug", checkIn.MonitorSlug).Msg("envelope: failed to save check-in")
	}
}

// saveFeedback parses a user_report payload and persists it via the FeedbackStore.
func saveFeedback(ctx context.Context, fs *sqlite.FeedbackStore, projectID string, payload []byte) {
	l := middleware.LogFromCtx(ctx)

	if fs == nil {
		l.Debug().Str("project_id", projectID).Msg("envelope: user_report received but no feedback store configured")
		return
	}

	var report struct {
		EventID  string `json:"event_id"`
		Name     string `json:"name"`
		Email    string `json:"email"`
		Comments string `json:"comments"`
	}
	if err := json.Unmarshal(payload, &report); err != nil {
		l.Warn().Err(err).Str("project_id", projectID).Msg("envelope: failed to parse user_report payload")
		return
	}

	if err := fs.SaveFeedback(ctx, projectID, report.EventID, report.Name, report.Email, report.Comments); err != nil {
		l.Error().Err(err).Str("project_id", projectID).Msg("envelope: failed to save user feedback")
	}
}

// saveAttachment stores an attachment item in the attachment store or falls
// back to blob-only storage when the store is unavailable.
func saveAttachment(ctx context.Context, as attachment.Store, bs store.BlobStore, projectID, eventID string, item envelope.Item) {
	saveAttachmentWithID(ctx, as, bs, projectID, eventID, "", item)
}

func saveAttachmentWithID(ctx context.Context, as attachment.Store, bs store.BlobStore, projectID, eventID, attachmentID string, item envelope.Item) {
	l := middleware.LogFromCtx(ctx)

	filename := item.Header.Filename
	if filename == "" {
		filename = "unnamed"
	}

	if as == nil {
		if bs == nil {
			projectKey := strings.TrimSpace(projectID)
			if projectKey == "" {
				projectKey = "<unknown>"
			}
			if _, loaded := missingAttachmentStorageProjects.LoadOrStore(projectKey, struct{}{}); !loaded {
				l.Debug().Str("project_id", projectID).Msg("envelope: attachment received but no storage configured")
			}
			return
		}

		key := fmt.Sprintf("attachments/%s/%s/%s", projectID, eventID, filename)
		if err := bs.Put(ctx, key, item.Payload); err != nil {
			l.Error().Err(err).Str("project_id", projectID).Str("event_id", eventID).Str("key", key).Msg("envelope: failed to save attachment")
		}
		return
	}

	att := &attachment.Attachment{
		ID:          attachmentID,
		EventID:     eventID,
		ProjectID:   projectID,
		Name:        filename,
		ContentType: item.Header.ContentType,
		Size:        int64(len(item.Payload)),
	}
	if err := as.SaveAttachment(ctx, att, item.Payload); err != nil {
		l.Error().Err(err).Str("project_id", projectID).Str("event_id", eventID).Str("name", filename).Msg("envelope: failed to save attachment")
	}
}

func hasReplayEnvelopeItems(items []envelope.Item) bool {
	for _, item := range items {
		switch item.Header.Type {
		case "replay_event", "replay_recording", "replay_recording_not_chunked", "replay_video":
			return true
		}
	}
	return false
}

func envelopeReplayIDs(items []envelope.Item, fallback string) (eventID, replayID string) {
	for _, item := range items {
		if item.Header.Type == "replay_event" {
			return replayEnvelopeIDs(item.Payload, fallback)
		}
	}
	value := strings.TrimSpace(fallback)
	return value, value
}

// saveStatsdMetrics parses statsd-format metrics from a payload and saves them.
func saveStatsdMetrics(ctx context.Context, store *sqlite.MetricBucketStore, projectID string, payload []byte) {
	l := middleware.LogFromCtx(ctx)
	if store == nil {
		l.Debug().Str("project_id", projectID).Msg("envelope: statsd received but no metric bucket store configured")
		return
	}
	buckets := parseStatsdMetrics(projectID, payload)
	if len(buckets) == 0 {
		return
	}
	if err := store.SaveMetricBuckets(ctx, buckets); err != nil {
		l.Error().Err(err).Str("project_id", projectID).Int("count", len(buckets)).Msg("envelope: failed to save statsd metrics")
	}
}

// extractTransactionSamplingFields extracts environment, release, and
// transaction name from a raw transaction event payload for sampling evaluation.
func extractTransactionSamplingFields(payload []byte) (environment, release, transaction string) {
	var fields struct {
		Environment string `json:"environment"`
		Release     string `json:"release"`
		Transaction string `json:"transaction"`
	}
	if err := json.Unmarshal(payload, &fields); err != nil {
		return "", "", ""
	}
	return strings.TrimSpace(fields.Environment), strings.TrimSpace(fields.Release), strings.TrimSpace(fields.Transaction)
}
