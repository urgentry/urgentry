package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"urgentry/internal/alert"
	"urgentry/internal/controlplane"
	"urgentry/internal/runtimeasync"
	"urgentry/internal/sqlite"

	"github.com/rs/zerolog/log"
)

const (
	schedulerLeaseName = "scheduler"
	schedulerLeaseTTL  = 30 * time.Second
	schedulerTick      = 10 * time.Second
)

// Scheduler requeues expired jobs and holds singleton maintenance leases.
type Scheduler struct {
	jobs           runtimeasync.Queue
	leases         runtimeasync.LeaseStore
	backfills      runtimeasync.KeyedEnqueuer
	reports        reportRunner
	metricEval     metricAlertRunner
	uptimePoller   *UptimePoller
	retention      *sqlite.RetentionStore
	monitors       controlplane.MonitorStore
	alerts         *AlertDeps
	holderID       string
}

type reportRunner interface {
	RunDue(ctx context.Context, now time.Time) error
}

type metricAlertRunner interface {
	EvaluateAll(ctx context.Context) ([]alert.MetricAlertTransition, error)
}

// NewScheduler creates a queue maintenance scheduler.
func NewScheduler(jobs runtimeasync.Queue, leases runtimeasync.LeaseStore, retention *sqlite.RetentionStore, monitors controlplane.MonitorStore, holderID string, alerts *AlertDeps) *Scheduler {
	return &Scheduler{
		jobs:      jobs,
		leases:    leases,
		retention: retention,
		monitors:  monitors,
		alerts:    alerts,
		holderID:  holderID,
	}
}

func (s *Scheduler) SetBackfillEnqueuer(enqueuer runtimeasync.KeyedEnqueuer) {
	s.backfills = enqueuer
}

func (s *Scheduler) SetReportRunner(runner reportRunner) {
	s.reports = runner
}

func (s *Scheduler) SetMetricAlertRunner(runner metricAlertRunner) {
	s.metricEval = runner
}

func (s *Scheduler) SetUptimePoller(poller *UptimePoller) {
	s.uptimePoller = poller
}

// Run executes scheduler maintenance until the context is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	if s == nil || s.jobs == nil || s.leases == nil {
		<-ctx.Done()
		return
	}

	ticker := time.NewTicker(schedulerTick)
	defer ticker.Stop()

	for {
		s.runOnce(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Scheduler) runOnce(ctx context.Context) {
	ok, err := s.leases.AcquireLease(ctx, schedulerLeaseName, s.holderID, schedulerLeaseTTL)
	if err != nil {
		log.Error().Err(err).Str("holder_id", s.holderID).Msg("scheduler: acquire lease failed")
		return
	}
	if !ok {
		return
	}
	requeued, err := s.jobs.RequeueExpiredProcessing(ctx)
	if err != nil {
		log.Error().Err(err).Str("holder_id", s.holderID).Msg("scheduler: requeue expired jobs failed")
		return
	}
	if requeued > 0 {
		log.Info().Int64("jobs", requeued).Msg("scheduler: requeued expired jobs")
	}
	if s.backfills != nil {
		if _, err := s.backfills.EnqueueKeyed(ctx, sqlite.JobKindBackfill, "", "backfill:tick", []byte("{}"), 1); err != nil {
			log.Error().Err(err).Str("holder_id", s.holderID).Msg("scheduler: enqueue backfill tick failed")
			return
		}
	}
	if s.retention != nil {
		report, err := s.retention.Apply(ctx)
		if err != nil {
			log.Error().Err(err).Str("holder_id", s.holderID).Msg("scheduler: retention sweep failed")
			return
		}
		if report.ErrorsDeleted > 0 || report.ErrorsArchived > 0 ||
			report.LogsDeleted > 0 || report.LogsArchived > 0 ||
			report.ReplaysDeleted > 0 || report.ReplaysArchived > 0 ||
			report.ProfilesDeleted > 0 || report.ProfilesArchived > 0 ||
			report.TracesDeleted > 0 || report.TracesArchived > 0 ||
			report.OutcomesDeleted > 0 || report.OutcomesArchived > 0 ||
			report.AttachmentsDeleted > 0 || report.AttachmentsArchived > 0 ||
			report.DebugFilesDeleted > 0 || report.DebugFilesArchived > 0 ||
			report.GroupsDeleted > 0 {
			log.Info().
				Int64("errors_deleted", report.ErrorsDeleted).
				Int64("errors_archived", report.ErrorsArchived).
				Int64("logs_deleted", report.LogsDeleted).
				Int64("logs_archived", report.LogsArchived).
				Int64("replays_deleted", report.ReplaysDeleted).
				Int64("replays_archived", report.ReplaysArchived).
				Int64("profiles_deleted", report.ProfilesDeleted).
				Int64("profiles_archived", report.ProfilesArchived).
				Int64("traces_deleted", report.TracesDeleted).
				Int64("traces_archived", report.TracesArchived).
				Int64("outcomes_deleted", report.OutcomesDeleted).
				Int64("outcomes_archived", report.OutcomesArchived).
				Int64("attachments_deleted", report.AttachmentsDeleted).
				Int64("attachments_archived", report.AttachmentsArchived).
				Int64("debug_files_deleted", report.DebugFilesDeleted).
				Int64("debug_files_archived", report.DebugFilesArchived).
				Int64("groups_deleted", report.GroupsDeleted).
				Msg("scheduler: retention sweep applied")
		}
	}
	if s.reports != nil {
		if err := s.reports.RunDue(ctx, time.Now().UTC()); err != nil {
			log.Error().Err(err).Str("holder_id", s.holderID).Msg("scheduler: run analytics reports failed")
			return
		}
	}
	if s.monitors != nil {
		missed, err := s.monitors.MarkMissed(ctx, time.Now().UTC())
		if err != nil {
			log.Error().Err(err).Str("holder_id", s.holderID).Msg("scheduler: mark missed monitors failed")
			return
		}
		if len(missed) > 0 {
			log.Info().Int("monitors", len(missed)).Msg("scheduler: marked missed check-ins")
			if s.alerts != nil && s.alerts.Evaluator != nil {
				for _, checkIn := range missed {
					DispatchAlertSignal(ctx, *s.alerts, checkIn.ProjectID, alert.Signal{
						ProjectID:   checkIn.ProjectID,
						EventID:     checkIn.CheckInID,
						EventType:   alert.EventTypeMonitor,
						MonitorSlug: checkIn.MonitorSlug,
						Status:      checkIn.Status,
						Timestamp:   checkIn.DateCreated,
					})
				}
			}
		}
	}
	if s.uptimePoller != nil {
		if err := s.uptimePoller.PollDue(ctx, time.Now().UTC()); err != nil {
			log.Error().Err(err).Str("holder_id", s.holderID).Msg("scheduler: uptime polling failed")
		}
	}
	if s.metricEval != nil {
		s.runMetricAlertEvaluation(ctx)
	}
}

func (s *Scheduler) runMetricAlertEvaluation(ctx context.Context) {
	transitions, err := s.metricEval.EvaluateAll(ctx)
	if err != nil {
		log.Error().Err(err).Str("holder_id", s.holderID).Msg("scheduler: metric alert evaluation failed")
		return
	}
	if len(transitions) == 0 {
		return
	}
	log.Info().Int("transitions", len(transitions)).Msg("scheduler: metric alert state transitions")

	if s.alerts == nil || s.alerts.Notifier == nil {
		return
	}
	for _, t := range transitions {
		if t.Rule == nil {
			continue
		}
		// Persist the state transition.
		t.Rule.State = t.ToState
		now := t.Timestamp
		if t.ToState == "triggered" {
			t.Rule.LastTriggeredAt = &now
		}
		t.Rule.UpdatedAt = now
		if s.alerts.MetricAlertStore != nil {
			if err := s.alerts.MetricAlertStore.UpdateMetricAlertRule(ctx, t.Rule); err != nil {
				log.Error().Err(err).Str("rule_id", t.Rule.ID).Msg("scheduler: failed to persist metric alert state")
			}
		}

		if t.ToState != "triggered" {
			continue
		}
		// Dispatch notifications for triggered rules.
		dispatchMetricAlertNotifications(ctx, s.alerts, t)
	}
}

func dispatchMetricAlertNotifications(ctx context.Context, deps *AlertDeps, t alert.MetricAlertTransition) {
	if deps == nil || deps.Notifier == nil || t.Rule == nil {
		return
	}
	trigger := alert.TriggerEvent{
		RuleID:    t.Rule.ID,
		EventType: "metric_alert",
		Status:    t.ToState,
		Timestamp: t.Timestamp,
	}
	if deps.Metrics != nil {
		deps.Metrics.RecordAlert()
	}
	log.Info().
		Str("rule_id", t.Rule.ID).
		Str("metric", t.Rule.Metric).
		Float64("value", t.Value).
		Float64("threshold", t.Rule.Threshold).
		Str("project_id", t.Rule.ProjectID).
		Msg("metric alert triggered")

	if deps.HistoryStore != nil {
		if err := deps.HistoryStore.Record(ctx, trigger); err != nil {
			log.Error().Err(err).Str("rule_id", t.Rule.ID).Msg("failed to record metric alert history")
		}
	}

	for _, actionJSON := range t.Rule.TriggerActions {
		kind, target := parseMetricAlertAction(actionJSON)
		if target == "" {
			continue
		}
		switch kind {
		case alert.ActionTypeWebhook:
			if err := deps.Notifier.NotifyWebhook(ctx, t.Rule.ProjectID, target, trigger); err != nil {
				log.Error().Err(err).Str("rule_id", t.Rule.ID).Str("url", target).Msg("metric alert webhook failed")
			}
		case alert.ActionTypeEmail:
			if err := deps.Notifier.NotifyEmail(ctx, t.Rule.ProjectID, target, trigger); err != nil {
				log.Error().Err(err).Str("rule_id", t.Rule.ID).Str("recipient", target).Msg("metric alert email failed")
			}
		case alert.ActionTypeSlack:
			if err := deps.Notifier.NotifySlack(ctx, t.Rule.ProjectID, target, trigger); err != nil {
				log.Error().Err(err).Str("rule_id", t.Rule.ID).Str("url", target).Msg("metric alert slack failed")
			}
		}
	}
}

// parseMetricAlertAction parses a trigger action string into a kind and
// target. Supported formats:
//   - "kind:target" (e.g. "email:admin@example.com", "webhook:https://...")
//   - JSON object: {"type":"email","target":"admin@example.com"}
func parseMetricAlertAction(action string) (kind, target string) {
	action = strings.TrimSpace(action)
	if action == "" {
		return "", ""
	}

	// Try JSON object format first.
	if strings.HasPrefix(action, "{") {
		var parsed struct {
			Type   string `json:"type"`
			Target string `json:"target"`
		}
		if err := json.Unmarshal([]byte(action), &parsed); err == nil && parsed.Target != "" {
			return strings.ToLower(strings.TrimSpace(parsed.Type)), strings.TrimSpace(parsed.Target)
		}
	}

	// Try "kind:target" format.
	if idx := strings.Index(action, ":"); idx > 0 {
		k := strings.ToLower(strings.TrimSpace(action[:idx]))
		t := strings.TrimSpace(action[idx+1:])
		switch k {
		case "email", "webhook", "slack":
			return k, t
		}
	}

	// Fallback: treat bare URL-like values as webhooks, emails as email.
	if strings.Contains(action, "@") {
		return alert.ActionTypeEmail, action
	}
	if strings.HasPrefix(action, "http://") || strings.HasPrefix(action, "https://") {
		return alert.ActionTypeWebhook, action
	}
	return "", ""
}
