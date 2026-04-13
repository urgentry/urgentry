package ingest

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"urgentry/internal/attachment"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

const replayPolicyFilteredValue = "[Filtered]"

func replayEnvelopeIDs(payload []byte, fallback string) (eventID, replayID string) {
	var partial struct {
		EventID  string `json:"event_id"`
		ReplayID string `json:"replay_id"`
	}
	if json.Unmarshal(payload, &partial) != nil {
		return strings.TrimSpace(fallback), strings.TrimSpace(fallback)
	}
	eventID = normalizeEventIDField(firstNonEmpty(strings.TrimSpace(partial.EventID), strings.TrimSpace(partial.ReplayID), strings.TrimSpace(fallback)))
	replayID = normalizeEventIDField(firstNonEmpty(strings.TrimSpace(partial.ReplayID), strings.TrimSpace(partial.EventID), strings.TrimSpace(fallback)))
	return eventID, replayID
}

func replayIncludedBySample(policy store.ReplayIngestPolicy, replayKey string) bool {
	if policy.SampleRate >= 1 {
		return true
	}
	if policy.SampleRate <= 0 {
		return false
	}
	sum := sha1.Sum([]byte(strings.TrimSpace(replayKey)))
	value := binary.BigEndian.Uint64(sum[:8])
	return float64(value)/float64(^uint64(0)) < policy.SampleRate
}

func annotateReplayReceiptPayload(payload []byte, policy store.ReplayIngestPolicy, dropReason string) []byte {
	var root map[string]any
	if json.Unmarshal(payload, &root) != nil {
		return payload
	}
	root = scrubReplayMap(root, policy, false)
	root["privacy_policy_version"] = policy.PolicyVersion()
	if strings.TrimSpace(dropReason) != "" {
		root["policy_drop_reason"] = strings.TrimSpace(dropReason)
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		return payload
	}
	return encoded
}

func scrubReplayRecordingPayload(payload []byte, policy store.ReplayIngestPolicy) []byte {
	var root any
	if json.Unmarshal(payload, &root) != nil {
		return payload
	}
	root = scrubReplayValue(root, policy, false)
	encoded, err := json.Marshal(root)
	if err != nil {
		return payload
	}
	return encoded
}

func scrubReplayValue(raw any, policy store.ReplayIngestPolicy, selectorMatched bool) any {
	switch value := raw.(type) {
	case map[string]any:
		return scrubReplayMap(value, policy, selectorMatched)
	case []any:
		items := make([]any, 0, len(value))
		for _, item := range value {
			items = append(items, scrubReplayValue(item, policy, selectorMatched))
		}
		return items
	default:
		return raw
	}
}

func scrubReplayMap(input map[string]any, policy store.ReplayIngestPolicy, inheritedSelectorMatch bool) map[string]any {
	if len(input) == 0 {
		return input
	}
	selectorMatched := inheritedSelectorMatch || replaySelectorMatched(input, policy.ScrubSelectors)
	out := make(map[string]any, len(input))
	for key, value := range input {
		lowerKey := strings.ToLower(strings.TrimSpace(key))
		switch {
		case replayStringInSet(lowerKey, policy.ScrubFields):
			out[key] = replayPolicyFilteredValue
		case lowerKey == "url":
			out[key] = scrubReplayURL(replayStringValue(value), policy.ScrubFields)
		case selectorMatched && replaySelectorSensitiveField(lowerKey):
			out[key] = replayPolicyFilteredValue
		default:
			out[key] = scrubReplayValue(value, policy, selectorMatched)
		}
	}
	return out
}

func replaySelectorMatched(payload map[string]any, selectors []string) bool {
	if len(selectors) == 0 {
		return false
	}
	for _, key := range []string{"selector", "target"} {
		value := strings.ToLower(strings.TrimSpace(replayStringValue(payload[key])))
		if value == "" {
			continue
		}
		for _, selector := range selectors {
			if strings.Contains(value, selector) {
				return true
			}
		}
	}
	return false
}

func replaySelectorSensitiveField(key string) bool {
	switch key {
	case "selector", "target", "text", "label", "value", "message":
		return true
	default:
		return false
	}
}

func replayStringInSet(value string, items []string) bool {
	for _, item := range items {
		if value == item {
			return true
		}
	}
	return false
}

func replayStringValue(raw any) string {
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return strings.TrimSpace(value.String())
	default:
		return ""
	}
}

func scrubReplayURL(raw string, scrubFields []string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(scrubFields) == 0 {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	query := parsed.Query()
	changed := false
	for _, field := range scrubFields {
		if _, ok := query[field]; ok {
			query.Set(field, replayPolicyFilteredValue)
			changed = true
		}
	}
	if !changed {
		return raw
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func replayProjectedAttachmentBytes(ctx context.Context, attachments attachment.Store, eventID, attachmentID string, payloadSize int64) (int64, error) {
	if attachments == nil || strings.TrimSpace(eventID) == "" {
		return payloadSize, nil
	}
	items, err := attachments.ListByEvent(ctx, eventID)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, item := range items {
		if item == nil {
			continue
		}
		total += item.Size
		if item.ID == attachmentID {
			total -= item.Size
		}
	}
	return total + payloadSize, nil
}

func saveReplayPolicyOutcome(ctx context.Context, outcomes *sqlite.OutcomeStore, projectID, eventID, reason string, policy store.ReplayIngestPolicy) {
	if outcomes == nil || strings.TrimSpace(reason) == "" {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"sampleRate": policy.SampleRate,
		"maxBytes":   policy.MaxBytes,
	})
	_ = outcomes.SaveOutcome(ctx, &sqlite.Outcome{
		ID:          stableOutcomeID(projectID, eventID, "replay_ingest_policy", "replay", reason, payload, 0),
		ProjectID:   projectID,
		EventID:     eventID,
		Category:    "replay",
		Reason:      reason,
		Quantity:    1,
		Source:      "replay_ingest_policy",
		PayloadJSON: payload,
		RecordedAt:  time.Now().UTC(),
		DateCreated: time.Now().UTC(),
	})
}
