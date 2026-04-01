package ingest

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/attachment"
	"urgentry/internal/envelope"
	"urgentry/internal/middleware"
	"urgentry/internal/store"
	"urgentry/pkg/id"
)

type replayEnvelopeEvent struct {
	EventID     string            `json:"event_id"`
	ReplayID    string            `json:"replay_id"`
	Timestamp   string            `json:"timestamp"`
	Platform    string            `json:"platform"`
	Release     string            `json:"release"`
	Environment string            `json:"environment"`
	Request     *replayRequest    `json:"request"`
	User        *replayUser       `json:"user"`
	Tags        map[string]string `json:"tags"`
}

type replayRequest struct {
	URL string `json:"url"`
}

type replayUser struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username"`
}

type profileEnvelopeEvent struct {
	EventID     string                     `json:"event_id"`
	ProfileID   string                     `json:"profile_id"`
	Timestamp   string                     `json:"timestamp"`
	Platform    string                     `json:"platform"`
	Release     string                     `json:"release"`
	Environment string                     `json:"environment"`
	Transaction string                     `json:"transaction"`
	TraceID     string                     `json:"trace_id"`
	DurationNS  json.Number                `json:"duration_ns"`
	Tags        map[string]string          `json:"tags"`
	Context     map[string]any             `json:"context"`
	Profile     map[string]json.RawMessage `json:"profile"`
}

func saveReplayEvent(ctx context.Context, replays store.ReplayIngestStore, events store.EventStore, projectID, fallbackEventID string, payload []byte) string {
	l := middleware.LogFromCtx(ctx)
	if replays != nil {
		replayEventID, err := replays.SaveEnvelopeReplay(ctx, projectID, fallbackEventID, payload)
		if err != nil {
			l.Error().Err(err).Str("project_id", projectID).Msg("envelope: failed to save replay payload")
			return ""
		}
		return replayEventID
	}
	if events == nil {
		l.Debug().Str("project_id", projectID).Msg("envelope: replay_event received but no event store configured")
		return ""
	}

	var replay replayEnvelopeEvent
	if err := json.Unmarshal(payload, &replay); err != nil {
		l.Warn().Err(err).Str("project_id", projectID).Msg("envelope: failed to parse replay payload")
		return ""
	}

	eventID := normalizeEventIDField(firstNonEmpty(replay.EventID, replay.ReplayID))
	if eventID == "" {
		eventID = id.New()
	}
	occurredAt := parseSessionTime(replay.Timestamp)
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	tags := cloneTags(replay.Tags)
	if replay.ReplayID != "" {
		tags["replay_id"] = strings.TrimSpace(replay.ReplayID)
	}
	if replay.Release != "" {
		tags["release"] = strings.TrimSpace(replay.Release)
	}
	if replay.Environment != "" {
		tags["environment"] = strings.TrimSpace(replay.Environment)
	}
	title := "Session replay"
	culprit := ""
	if replay.Request != nil && strings.TrimSpace(replay.Request.URL) != "" {
		culprit = strings.TrimSpace(replay.Request.URL)
		title = fmt.Sprintf("Replay of %s", culprit)
	}
	userIdentifier := replayUserIdentifier(replay.User)
	if userIdentifier != "" {
		tags["user"] = userIdentifier
	}
	if err := events.SaveEvent(ctx, &store.StoredEvent{
		ID:             id.New(),
		ProjectID:      projectID,
		EventID:        eventID,
		ReleaseID:      strings.TrimSpace(replay.Release),
		Environment:    strings.TrimSpace(replay.Environment),
		Platform:       firstNonEmpty(strings.TrimSpace(replay.Platform), "javascript"),
		Level:          "info",
		EventType:      "replay",
		OccurredAt:     occurredAt,
		IngestedAt:     time.Now().UTC(),
		Message:        "Session replay",
		Title:          title,
		Culprit:        culprit,
		Tags:           tags,
		NormalizedJSON: json.RawMessage(payload),
		UserIdentifier: userIdentifier,
	}); err != nil {
		l.Error().Err(err).Str("project_id", projectID).Str("event_id", eventID).Msg("envelope: failed to save replay event")
		return ""
	}
	return eventID
}

func saveProfileEvent(ctx context.Context, profiles store.ProfileIngestStore, events store.EventStore, projectID string, payload []byte) string {
	l := middleware.LogFromCtx(ctx)
	if profiles != nil {
		profileID, err := profiles.SaveEnvelopeProfile(ctx, projectID, payload)
		if err != nil {
			l.Warn().Err(err).Str("project_id", projectID).Msg("envelope: failed to save canonical profile payload")
			return ""
		}
		return profileID
	}
	if events == nil {
		l.Debug().Str("project_id", projectID).Msg("envelope: profile received but no event store configured")
		return ""
	}

	var profile profileEnvelopeEvent
	if err := json.Unmarshal(payload, &profile); err != nil {
		l.Warn().Err(err).Str("project_id", projectID).Msg("envelope: failed to parse profile payload")
		return ""
	}

	eventID := normalizeEventIDField(firstNonEmpty(profile.EventID, profile.ProfileID))
	if eventID == "" {
		eventID = id.New()
	}
	occurredAt := parseSessionTime(profile.Timestamp)
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	tags := cloneTags(profile.Tags)
	if profile.TraceID != "" {
		tags["trace_id"] = strings.TrimSpace(profile.TraceID)
	}
	if profile.Release != "" {
		tags["release"] = strings.TrimSpace(profile.Release)
	}
	if profile.Environment != "" {
		tags["environment"] = strings.TrimSpace(profile.Environment)
	}
	durationValue := strings.TrimSpace(profile.DurationNS.String())
	if durationValue != "" {
		tags["duration_ns"] = durationValue
	}
	title := "Profile"
	if strings.TrimSpace(profile.Transaction) != "" {
		title = fmt.Sprintf("Profile for %s", strings.TrimSpace(profile.Transaction))
	}
	if err := events.SaveEvent(ctx, &store.StoredEvent{
		ID:             id.New(),
		ProjectID:      projectID,
		EventID:        eventID,
		ReleaseID:      strings.TrimSpace(profile.Release),
		Environment:    strings.TrimSpace(profile.Environment),
		Platform:       firstNonEmpty(strings.TrimSpace(profile.Platform), "profile"),
		Level:          "info",
		EventType:      "profile",
		OccurredAt:     occurredAt,
		IngestedAt:     time.Now().UTC(),
		Message:        title,
		Title:          title,
		Culprit:        strings.TrimSpace(profile.Transaction),
		Tags:           tags,
		NormalizedJSON: json.RawMessage(payload),
	}); err != nil {
		l.Error().Err(err).Str("project_id", projectID).Str("event_id", eventID).Msg("envelope: failed to save profile event")
		return ""
	}
	return eventID
}

func saveReplayAttachment(ctx context.Context, attachments attachment.Store, blobs store.BlobStore, projectID, eventID string, item envelope.Item) {
	if strings.TrimSpace(eventID) == "" {
		l := middleware.LogFromCtx(ctx)
		l.Debug().Str("project_id", projectID).Msg("envelope: replay attachment skipped because replay id is unavailable")
		return
	}
	clone := item
	clone.Header.Filename = replayAttachmentFilename(clone)
	if strings.TrimSpace(item.Header.Filename) != "" {
		saveReplayAsset(ctx, attachments, blobs, projectID, eventID, clone)
		return
	}
	saveReplayAsset(ctx, attachments, blobs, projectID, eventID, clone)
}

func replayAttachmentFilename(item envelope.Item) string {
	if strings.TrimSpace(item.Header.Filename) != "" {
		return strings.TrimSpace(item.Header.Filename)
	}
	switch item.Header.Type {
	case "replay_video":
		return "replay-video.bin"
	case "replay_recording_not_chunked":
		return "replay-recording-full.json"
	default:
		return "replay-recording.json"
	}
}

func envelopeItemEventID(item envelope.Item) string {
	switch item.Header.Type {
	case "event", "transaction", "replay_event", "profile":
	default:
		return ""
	}
	var partial struct {
		EventID   string `json:"event_id"`
		ReplayID  string `json:"replay_id"`
		ProfileID string `json:"profile_id"`
	}
	if err := json.Unmarshal(item.Payload, &partial); err != nil {
		return ""
	}
	return normalizeEventIDField(firstNonEmpty(partial.EventID, partial.ReplayID, partial.ProfileID))
}

func replayUserIdentifier(user *replayUser) string {
	if user == nil {
		return ""
	}
	return firstNonEmpty(strings.TrimSpace(user.ID), strings.TrimSpace(user.Email), strings.TrimSpace(user.Username))
}

func saveReplayAsset(ctx context.Context, as attachment.Store, bs store.BlobStore, projectID, eventID string, item envelope.Item) {
	l := middleware.LogFromCtx(ctx)

	filename := item.Header.Filename
	if filename == "" {
		filename = "unnamed"
	}
	if as == nil {
		saveAttachment(ctx, as, bs, projectID, eventID, item)
		return
	}
	att := &attachment.Attachment{
		ID:          replayAttachmentID(projectID, eventID, item.Header.Type, filename),
		EventID:     eventID,
		ProjectID:   projectID,
		Name:        filename,
		ContentType: item.Header.ContentType,
		Size:        int64(len(item.Payload)),
	}
	if err := as.SaveAttachment(ctx, att, item.Payload); err != nil {
		l.Error().Err(err).Str("project_id", projectID).Str("event_id", eventID).Str("name", filename).Msg("envelope: failed to save replay asset")
	}
}

func replayAttachmentID(projectID, eventID, itemType, filename string) string {
	sum := sha1.Sum([]byte(strings.Join([]string{
		strings.TrimSpace(projectID),
		strings.TrimSpace(eventID),
		strings.TrimSpace(itemType),
		strings.TrimSpace(filename),
	}, "\x00")))
	return fmt.Sprintf("replay-%x", sum[:8])
}

func cloneTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(tags))
	for key, value := range tags {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		cloned[key] = strings.TrimSpace(value)
	}
	return cloned
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
