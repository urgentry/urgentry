package pipeline

import (
	"context"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"urgentry/internal/alert"
	"urgentry/internal/issue"
	"urgentry/internal/metrics"
	"urgentry/internal/notify"
	sharedstore "urgentry/internal/store"
)

type AlertHistoryStore interface {
	Record(ctx context.Context, trigger alert.TriggerEvent) error
}

// AlertDeps holds the dependencies needed by the alert callback.
type AlertDeps struct {
	Evaluator        *alert.Evaluator
	Notifier         *notify.Notifier
	HistoryStore     AlertHistoryStore
	AlertStore       alert.RuleStore
	MetricAlertStore alert.MetricAlertRuleStore
	Profiles         sharedstore.ProfileReadStore
	Metrics          *metrics.Metrics
}

// DispatchAlertSignal evaluates one signal, records alert history, and sends
// notifications for every trigger returned by the evaluator.
func DispatchAlertSignal(ctx context.Context, deps AlertDeps, projectID string, signal alert.Signal) {
	if deps.Evaluator == nil {
		return
	}
	triggers, err := deps.Evaluator.EvaluateSignal(ctx, signal)
	if err != nil {
		log.Error().Err(err).Msg("alert evaluation failed")
		return
	}
	if deps.Profiles != nil {
		enrichAlertProfiles(ctx, deps.Profiles, projectID, signal, triggers)
	}
	dispatchAlertTriggers(ctx, deps, projectID, triggers)
}

// NewAlertCallback returns an AlertCallback that evaluates alert rules,
// records history, and dispatches notifications.
func NewAlertCallback(deps AlertDeps) AlertCallback {
	return func(ctx context.Context, projectID string, result issue.ProcessResult) {
		signal := alert.Signal{
			ProjectID:    projectID,
			GroupID:      result.GroupID,
			EventID:      result.EventID,
			EventType:    result.EventType,
			TraceID:      result.TraceID,
			Transaction:  result.Transaction,
			DurationMS:   result.DurationMS,
			Status:       result.Status,
			IsNewGroup:   result.IsNewGroup,
			IsRegression: result.IsRegression,
			Timestamp:    time.Now().UTC(),
		}
		DispatchAlertSignal(ctx, deps, projectID, signal)
	}
}

func enrichAlertProfiles(ctx context.Context, profiles sharedstore.ProfileReadStore, projectID string, signal alert.Signal, triggers []alert.TriggerEvent) {
	if len(triggers) == 0 || profiles == nil {
		return
	}
	item, err := profiles.FindRelatedProfile(ctx, projectID, signal.TraceID, signal.Transaction, signal.Release)
	if err != nil || item == nil {
		return
	}
	profile := &alert.ProfileContext{
		ProfileID:     item.ProfileID,
		URL:           "/profiles/" + item.ProfileID + "/",
		TraceID:       item.TraceID,
		Transaction:   item.Transaction,
		Release:       item.Release,
		DurationNS:    item.DurationNS,
		SampleCount:   item.SampleCount,
		FunctionCount: item.FunctionCount,
		TopFunction:   item.TopFunction,
	}
	for i := range triggers {
		if triggers[i].Profile == nil {
			triggers[i].Profile = alertProfileCopy(profile)
		}
	}
}

func alertProfileCopy(item *alert.ProfileContext) *alert.ProfileContext {
	if item == nil {
		return nil
	}
	copy := *item
	return &copy
}

func dispatchAlertTriggers(ctx context.Context, deps AlertDeps, projectID string, triggers []alert.TriggerEvent) {
	for _, t := range triggers {
		if deps.Metrics != nil {
			deps.Metrics.RecordAlert()
		}
		log.Info().
			Str("rule_id", t.RuleID).
			Str("group_id", t.GroupID).
			Str("event_id", t.EventID).
			Str("event_type", t.EventType).
			Msg("alert fired")
		if deps.HistoryStore != nil {
			if err := deps.HistoryStore.Record(ctx, t); err != nil {
				log.Error().Err(err).Str("rule_id", t.RuleID).Msg("failed to record alert history")
			}
		}

		// Dispatch notifications for each action on the rule.
		if deps.AlertStore == nil || deps.Notifier == nil {
			continue
		}
		rule, ruleErr := deps.AlertStore.GetRule(ctx, t.RuleID)
		if ruleErr != nil || rule == nil {
			continue
		}
		for _, action := range rule.Actions {
			switch action.Type {
			case alert.ActionTypeWebhook:
				if action.Target != "" {
					if wErr := deps.Notifier.NotifyWebhook(ctx, projectID, action.Target, t); wErr != nil {
						log.Error().Err(wErr).
							Str("rule_id", t.RuleID).
							Str("url", action.Target).
							Msg("webhook notification failed")
					} else {
						log.Info().
							Str("rule_id", t.RuleID).
							Str("url", action.Target).
							Msg("webhook notification sent")
					}
				}
			case alert.ActionTypeEmail:
				recipients := strings.Split(action.Target, ",")
				for _, recipient := range recipients {
					recipient = strings.TrimSpace(recipient)
					if recipient == "" {
						continue
					}
					if err := deps.Notifier.NotifyEmail(ctx, projectID, recipient, t); err != nil {
						log.Error().
							Err(err).
							Str("rule_id", t.RuleID).
							Str("recipient", recipient).
							Msg("email notification failed")
						continue
					}
					log.Info().
						Str("rule_id", t.RuleID).
						Str("recipient", recipient).
						Msg("email notification recorded")
				}
			case alert.ActionTypeSlack:
				if action.Target != "" {
					if sErr := deps.Notifier.NotifySlack(ctx, projectID, action.Target, t); sErr != nil {
						log.Error().Err(sErr).
							Str("rule_id", t.RuleID).
							Str("url", action.Target).
							Msg("slack notification failed")
					} else {
						log.Info().
							Str("rule_id", t.RuleID).
							Str("url", action.Target).
							Msg("slack notification sent")
					}
				}
			default:
				log.Warn().
					Str("rule_id", t.RuleID).
					Str("action_type", action.Type).
					Msg("unknown alert action type")
			}
		}
	}
}
