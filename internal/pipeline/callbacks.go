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

type ServiceHookDispatcher interface {
	FireHooks(ctx context.Context, projectID, action string, payload any) error
}

// AlertDeps holds the dependencies needed by the alert callback.
type AlertDeps struct {
	Evaluator        *alert.Evaluator
	Notifier         *notify.Notifier
	HistoryStore     AlertHistoryStore
	Hooks            ServiceHookDispatcher
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
		fireServiceHooks(ctx, deps, projectID, "event.created", eventHookPayload(projectID, result))
		if result.IsNewGroup && result.GroupID != "" {
			fireServiceHooks(ctx, deps, projectID, "issue.created", issueHookPayload(projectID, result, "unresolved"))
		}
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
		fireServiceHooks(ctx, deps, projectID, "event.alert", alertHookPayload(projectID, t))
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

func fireServiceHooks(ctx context.Context, deps AlertDeps, projectID, action string, payload any) {
	if deps.Hooks == nil {
		return
	}
	if err := deps.Hooks.FireHooks(ctx, projectID, action, payload); err != nil {
		log.Error().Err(err).Str("project_id", projectID).Str("action", action).Msg("service hook dispatch failed")
	}
}

func eventHookPayload(projectID string, result issue.ProcessResult) map[string]any {
	payload := map[string]any{
		"action": "event.created",
		"data": map[string]any{
			"project": map[string]any{"id": projectID},
			"event":   hookEventData(result),
		},
	}
	if result.GroupID != "" {
		payload["data"].(map[string]any)["issue"] = map[string]any{"id": result.GroupID}
	}
	return payload
}

func issueHookPayload(projectID string, result issue.ProcessResult, status string) map[string]any {
	data := map[string]any{
		"project": map[string]any{"id": projectID},
		"issue": map[string]any{
			"id":     result.GroupID,
			"status": status,
		},
	}
	if result.EventID != "" {
		data["event"] = hookEventData(result)
	}
	return map[string]any{
		"action": "issue.created",
		"data":   data,
	}
}

func alertHookPayload(projectID string, trigger alert.TriggerEvent) map[string]any {
	data := map[string]any{
		"project": map[string]any{"id": projectID},
		"event": map[string]any{
			"id":          trigger.EventID,
			"eventId":     trigger.EventID,
			"type":        trigger.EventType,
			"status":      trigger.Status,
			"traceId":     trigger.TraceID,
			"transaction": trigger.Transaction,
			"release":     trigger.Release,
			"timestamp":   trigger.Timestamp,
		},
		"alert": map[string]any{
			"ruleId": trigger.RuleID,
		},
	}
	if trigger.GroupID != "" {
		data["issue"] = map[string]any{"id": trigger.GroupID}
	}
	if trigger.DurationMS > 0 {
		data["event"].(map[string]any)["durationMs"] = trigger.DurationMS
	}
	if trigger.Profile != nil {
		data["profile"] = trigger.Profile
	}
	return map[string]any{
		"action": "event.alert",
		"data":   data,
	}
}

func hookEventData(result issue.ProcessResult) map[string]any {
	data := map[string]any{
		"id":      result.EventID,
		"eventId": result.EventID,
		"type":    result.EventType,
		"status":  result.Status,
	}
	if result.TraceID != "" {
		data["traceId"] = result.TraceID
	}
	if result.Transaction != "" {
		data["transaction"] = result.Transaction
	}
	if result.DurationMS > 0 {
		data["durationMs"] = result.DurationMS
	}
	return data
}
