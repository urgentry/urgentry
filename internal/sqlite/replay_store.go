package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/store"
)

var _ store.ReplayIngestStore = (*ReplayStore)(nil)
var _ store.ReplayReadStore = (*ReplayStore)(nil)

// ReplayStore owns canonical replay manifests, asset references, and timeline
// indexes while keeping large replay bodies in blob storage.
type ReplayStore struct {
	db    *sql.DB
	blobs store.BlobStore
}

type replayReceiptHint struct {
	EventID              string
	ReplayID             string
	OccurredAt           time.Time
	Platform             string
	Release              string
	Environment          string
	RequestURL           string
	User                 store.ReplayUserRef
	TraceIDs             []string
	PrivacyPolicyVersion string
	PolicyError          string
}

type replayEnvelopePayload struct {
	EventID              string                     `json:"event_id"`
	ReplayID             string                     `json:"replay_id"`
	Timestamp            string                     `json:"timestamp"`
	Platform             string                     `json:"platform"`
	Release              string                     `json:"release"`
	Environment          string                     `json:"environment"`
	PrivacyPolicyVersion string                     `json:"privacy_policy_version"`
	PolicyDropReason     string                     `json:"policy_drop_reason"`
	Request              *replayRequestPayload      `json:"request"`
	User                 *replayUserPayload         `json:"user"`
	Tags                 map[string]any             `json:"tags"`
	Contexts             map[string]json.RawMessage `json:"contexts"`
}

type replayRequestPayload struct {
	URL string `json:"url"`
}

type replayUserPayload struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username"`
}

type replayRecordingEnvelope struct {
	Events []json.RawMessage `json:"events"`
}

// NewReplayStore creates a SQLite-backed replay store.
func NewReplayStore(db *sql.DB, blobs store.BlobStore) *ReplayStore {
	return &ReplayStore{db: db, blobs: blobs}
}

func parseReplayReceiptHint(payload []byte, fallbackEventID string) (replayReceiptHint, error) {
	var parsed replayEnvelopePayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return replayReceiptHint{
			EventID:  strings.TrimSpace(fallbackEventID),
			ReplayID: strings.TrimSpace(fallbackEventID),
		}, fmt.Errorf("parse replay payload: %w", err)
	}
	hint := replayReceiptHint{
		EventID:              firstNonEmptyText(parsed.EventID, fallbackEventID),
		ReplayID:             firstNonEmptyText(parsed.ReplayID, parsed.EventID, fallbackEventID),
		OccurredAt:           parseTimeAny(parsed.Timestamp),
		Platform:             strings.TrimSpace(parsed.Platform),
		Release:              strings.TrimSpace(parsed.Release),
		Environment:          strings.TrimSpace(parsed.Environment),
		PrivacyPolicyVersion: strings.TrimSpace(parsed.PrivacyPolicyVersion),
		PolicyError:          strings.TrimSpace(parsed.PolicyDropReason),
	}
	if parsed.Request != nil {
		hint.RequestURL = strings.TrimSpace(parsed.Request.URL)
	}
	if parsed.User != nil {
		hint.User = store.ReplayUserRef{
			ID:       strings.TrimSpace(parsed.User.ID),
			Email:    strings.TrimSpace(parsed.User.Email),
			Username: strings.TrimSpace(parsed.User.Username),
		}
	}
	hint.TraceIDs = replayTraceIDs(parsed.Tags, parsed.Contexts)
	return hint, nil
}

func replayTraceIDs(tags map[string]any, contexts map[string]json.RawMessage) []string {
	var traceIDs []string
	for _, key := range []string{"trace_id", "traceId"} {
		if value := strings.TrimSpace(stringFromAny(tags[key])); value != "" {
			traceIDs = append(traceIDs, value)
		}
	}
	if len(contexts) > 0 {
		for _, key := range []string{"trace", "otel"} {
			raw := contexts[key]
			if len(raw) == 0 {
				continue
			}
			var payload map[string]any
			if json.Unmarshal(raw, &payload) == nil {
				for _, field := range []string{"trace_id", "traceId"} {
					if value := strings.TrimSpace(stringFromAny(payload[field])); value != "" {
						traceIDs = append(traceIDs, value)
					}
				}
			}
		}
	}
	return uniqueReplayStrings(traceIDs)
}

func parseTimeAny(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return ts.UTC()
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts.UTC()
	}
	return time.Time{}
}

func uniqueReplayStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func intValue(raw any) int {
	if value, ok := intFromAny(raw); ok {
		return value
	}
	return 0
}

func int64Value(raw any) int64 {
	if value, ok := int64FromAny(raw); ok {
		return value
	}
	return 0
}

func int64FromAny(raw any) (int64, bool) {
	switch value := raw.(type) {
	case int:
		return int64(value), true
	case int64:
		return value, true
	case float64:
		return int64(value), true
	case json.Number:
		i, err := value.Int64()
		if err == nil {
			return i, true
		}
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err == nil {
			return i, true
		}
	}
	return 0, false
}

func stringFromAny(raw any) string {
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return value.String()
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case bool:
		if value {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func bytesTrimSpace(raw []byte) []byte {
	return []byte(strings.TrimSpace(string(raw)))
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
