package ingest

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"urgentry/internal/envelope"
	"urgentry/pkg/id"
)

func canonicalizeStorePayload(body []byte) ([]byte, string, error) {
	if eventID := strings.TrimSpace(extractJSONEventID(body)); eventID != "" {
		return body, eventID, nil
	}
	root, err := decodeJSONObject(body)
	if err != nil {
		return nil, "", err
	}
	eventID := id.New()
	root["event_id"] = eventID
	body, err = json.Marshal(root)
	if err != nil {
		return nil, "", fmt.Errorf("marshal canonical store payload: %w", err)
	}
	return body, eventID, nil
}

func canonicalizeEnvelopeForIngest(env *envelope.Envelope) (string, error) {
	if env == nil {
		return "", fmt.Errorf("envelope is nil")
	}
	headerEventID := strings.TrimSpace(env.Header.EventID)
	headerUsed := false
	responseID := ""
	if headerEventID != "" {
		env.Header.EventID = headerEventID
		responseID = headerEventID
	}

	for idx := range env.Items {
		item := env.Items[idx]
		fallbackID := ""
		if !headerUsed && headerEventID != "" {
			fallbackID = headerEventID
		}
		switch item.Header.Type {
		case "event", "transaction":
			payload, eventID, err := canonicalizeEventPayload(item.Payload, fallbackID)
			if err != nil {
				return "", err
			}
			env.Items[idx].Payload = payload
			if responseID == "" {
				responseID = eventID
			}
			headerUsed = headerUsed || (headerEventID != "" && eventID == headerEventID)
		case "replay_event":
			payload, eventID, _, err := canonicalizeReplayPayload(item.Payload, fallbackID)
			if err != nil {
				return "", err
			}
			env.Items[idx].Payload = payload
			if responseID == "" {
				responseID = eventID
			}
			headerUsed = headerUsed || (headerEventID != "" && eventID == headerEventID)
		case "profile":
			payload, eventID, profileID, err := canonicalizeProfilePayload(item.Payload, fallbackID)
			if err != nil {
				return "", err
			}
			env.Items[idx].Payload = payload
			if responseID == "" {
				responseID = firstNonEmpty(profileID, eventID)
			}
			headerUsed = headerUsed || (headerEventID != "" && (eventID == headerEventID || profileID == headerEventID))
		}
	}

	if responseID == "" {
		responseID = id.New()
	}
	if headerEventID == "" {
		env.Header.EventID = responseID
	}
	return responseID, nil
}

func canonicalizeEventPayload(payload []byte, fallbackID string) ([]byte, string, error) {
	if eventID := strings.TrimSpace(extractJSONEventID(payload)); eventID != "" {
		return payload, eventID, nil
	}
	root, err := decodeJSONObject(payload)
	if err != nil {
		return nil, "", err
	}
	eventID := strings.TrimSpace(stringField(root, "event_id"))
	if eventID == "" {
		eventID = strings.TrimSpace(fallbackID)
	}
	if eventID == "" {
		eventID = id.New()
	}
	root["event_id"] = eventID
	body, err := json.Marshal(root)
	if err != nil {
		return nil, "", fmt.Errorf("marshal canonical event payload: %w", err)
	}
	return body, eventID, nil
}

func canonicalizeReplayPayload(payload []byte, fallbackID string) ([]byte, string, string, error) {
	root, err := decodeJSONObject(payload)
	if err != nil {
		return nil, "", "", err
	}
	eventID := firstNonEmpty(
		stringField(root, "event_id"),
		stringField(root, "replay_id"),
		strings.TrimSpace(fallbackID),
	)
	if eventID == "" {
		eventID = id.New()
	}
	replayID := firstNonEmpty(stringField(root, "replay_id"), eventID)
	root["event_id"] = eventID
	root["replay_id"] = replayID
	body, err := json.Marshal(root)
	if err != nil {
		return nil, "", "", fmt.Errorf("marshal canonical replay payload: %w", err)
	}
	return body, eventID, replayID, nil
}

func canonicalizeProfilePayload(payload []byte, fallbackID string) ([]byte, string, string, error) {
	root, err := decodeJSONObject(payload)
	if err != nil {
		return nil, "", "", err
	}
	profileID := firstNonEmpty(
		stringField(root, "profile_id"),
		stringField(root, "event_id"),
		strings.TrimSpace(fallbackID),
	)
	if profileID == "" {
		profileID = id.New()
	}
	eventID := firstNonEmpty(stringField(root, "event_id"), profileID)
	root["profile_id"] = profileID
	root["event_id"] = eventID
	body, err := json.Marshal(root)
	if err != nil {
		return nil, "", "", fmt.Errorf("marshal canonical profile payload: %w", err)
	}
	return body, eventID, profileID, nil
}

func decodeJSONObject(payload []byte) (map[string]any, error) {
	var root map[string]any
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, fmt.Errorf("parse json object: %w", err)
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}

func stringField(root map[string]any, key string) string {
	if root == nil {
		return ""
	}
	value, ok := root[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func stableEnvelopeAttachmentID(projectID, eventID string, item envelope.Item, itemIndex int) string {
	return stableHashID("att", projectID, eventID, item.Header.Type, item.Header.Filename, strconv.Itoa(itemIndex), payloadDigest(item.Payload))
}

func stableOutcomeID(projectID, eventID, source, category, reason string, payload []byte, itemIndex int) string {
	return stableHashID("out", projectID, eventID, source, category, reason, strconv.Itoa(itemIndex), payloadDigest(payload))
}

func stableHashID(prefix string, parts ...string) string {
	sum := sha1.Sum([]byte(strings.Join(parts, "\x00")))
	return prefix + "-" + hex.EncodeToString(sum[:8])
}

func payloadDigest(payload []byte) string {
	sum := sha1.Sum(payload)
	return hex.EncodeToString(sum[:8])
}

func extractJSONEventID(payload []byte) string {
	keyIdx := bytes.Index(payload, []byte(`"event_id"`))
	if keyIdx < 0 {
		return ""
	}
	cursor := keyIdx + len(`"event_id"`)
	for cursor < len(payload) && (payload[cursor] == ' ' || payload[cursor] == '\n' || payload[cursor] == '\r' || payload[cursor] == '\t') {
		cursor++
	}
	if cursor >= len(payload) || payload[cursor] != ':' {
		return ""
	}
	cursor++
	for cursor < len(payload) && (payload[cursor] == ' ' || payload[cursor] == '\n' || payload[cursor] == '\r' || payload[cursor] == '\t') {
		cursor++
	}
	if cursor >= len(payload) || payload[cursor] != '"' {
		return ""
	}
	cursor++
	start := cursor
	for cursor < len(payload) {
		if payload[cursor] == '"' && (cursor == start || payload[cursor-1] != '\\') {
			return strings.TrimSpace(string(payload[start:cursor]))
		}
		cursor++
	}
	return ""
}
