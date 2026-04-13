package ingest

import (
	"context"
	"fmt"
	"strings"

	"urgentry/internal/envelope"
	"urgentry/internal/middleware"
	"urgentry/internal/sqlite"
)

func persistEnvelopeSideEffects(ctx context.Context, deps IngestDeps, env *envelope.Envelope, projectID, baseEventID string) error {
	if env == nil {
		return nil
	}
	replayIndexes := map[string]struct{}{}
	replayPolicy := sqlite.DefaultReplayIngestPolicy()
	replayPolicyLoaded := false
	replayAllowed := true
	replayDropReason := ""
	activeReplayEventID := baseEventID
	replayReceiptPayload := []byte(nil)

	if hasReplayEnvelopeItems(env.Items) {
		activeReplayEventID, _ = envelopeReplayIDs(env.Items, baseEventID)
		if deps.ReplayPolicies != nil {
			policy, err := deps.ReplayPolicies.GetReplayIngestPolicy(ctx, projectID)
			if err != nil {
				return fmt.Errorf("load replay ingest policy: %w", err)
			}
			replayPolicy = policy
			replayPolicyLoaded = true
			replayAllowed = replayIncludedBySample(replayPolicy, activeReplayEventID)
			if !replayAllowed {
				saveReplayPolicyOutcome(ctx, deps.OutcomeStore, projectID, activeReplayEventID, "sample_rate", replayPolicy)
			}
		}
	}

	for idx, item := range env.Items {
		switch item.Header.Type {
		case "event", "transaction":
		case "user_report":
			saveFeedback(ctx, deps.FeedbackStore, projectID, item.Payload)
		case "attachment":
			saveAttachmentWithID(ctx, deps.AttachmentStore, deps.BlobStore, projectID, baseEventID, stableEnvelopeAttachmentID(projectID, baseEventID, item, idx), item)
		case "replay_event":
			if replayPolicyLoaded && !replayAllowed {
				continue
			}
			payload := item.Payload
			if replayPolicyLoaded {
				replayReceiptPayload = append(replayReceiptPayload[:0], item.Payload...)
				payload = annotateReplayReceiptPayload(replayReceiptPayload, replayPolicy, replayDropReason)
			}
			if replayEventID := saveReplayEvent(ctx, deps.ReplayStore, deps.EventStore, projectID, activeReplayEventID, payload); replayEventID != "" {
				activeReplayEventID = replayEventID
				replayIndexes[replayEventID] = struct{}{}
			}
		case "replay_recording", "replay_recording_not_chunked", "replay_video":
			if replayPolicyLoaded && !replayAllowed {
				continue
			}
			payload := item.Payload
			if replayPolicyLoaded && item.Header.Type != "replay_video" {
				payload = scrubReplayRecordingPayload(payload, replayPolicy)
			}
			if replayPolicyLoaded && replayDropReason == "" {
				projectedBytes, err := replayProjectedAttachmentBytes(
					ctx,
					deps.AttachmentStore,
					activeReplayEventID,
					replayAttachmentID(projectID, activeReplayEventID, item.Header.Type, replayAttachmentFilename(item)),
					int64(len(payload)),
				)
				if err != nil {
					return fmt.Errorf("enforce replay ingest policy: %w", err)
				}
				if projectedBytes > replayPolicy.MaxBytes {
					replayDropReason = "replay attachment exceeds max_bytes policy"
					saveReplayPolicyOutcome(ctx, deps.OutcomeStore, projectID, activeReplayEventID, "max_bytes", replayPolicy)
				}
			}
			if replayDropReason != "" {
				continue
			}
			clone := item
			clone.Payload = payload
			saveReplayAttachment(ctx, deps.AttachmentStore, deps.BlobStore, projectID, activeReplayEventID, clone)
			if strings.TrimSpace(activeReplayEventID) != "" {
				replayIndexes[activeReplayEventID] = struct{}{}
			}
		case "profile":
			saveProfileEvent(ctx, deps.ProfileStore, deps.EventStore, projectID, item.Payload)
		case "client_report":
			saveClientReport(ctx, deps.OutcomeStore, projectID, baseEventID, item.Payload)
		case "session":
			saveSession(ctx, deps.SessionStore, projectID, item.Payload)
		case "sessions":
			saveSessionAggregates(ctx, deps.SessionStore, deps.AlertDeps, projectID, item.Payload)
		case "check_in":
			saveCheckIn(ctx, deps.MonitorStore, projectID, item.Payload)
		case "statsd", "metric_buckets":
			saveStatsdMetrics(ctx, deps.MetricBuckets, projectID, item.Payload)
		default:
			logger := middleware.LogFromCtx(ctx)
			logger.Warn().Str("project_id", projectID).Str("type", item.Header.Type).Msg("envelope: unknown side-effect item type, skipping")
		}
	}

	if replayPolicyLoaded && replayAllowed && replayDropReason != "" && len(replayReceiptPayload) > 0 {
		if replayEventID := saveReplayEvent(ctx, deps.ReplayStore, deps.EventStore, projectID, activeReplayEventID, annotateReplayReceiptPayload(replayReceiptPayload, replayPolicy, replayDropReason)); replayEventID != "" {
			replayIndexes[replayEventID] = struct{}{}
		}
	}
	if deps.ReplayStore != nil {
		for replayID := range replayIndexes {
			if err := deps.ReplayStore.IndexReplay(ctx, projectID, replayID); err != nil {
				return fmt.Errorf("index replay: %w", err)
			}
		}
	}
	return nil
}
