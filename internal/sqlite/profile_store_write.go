package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/store"
)

func (s *ProfileStore) SaveEnvelopeProfile(ctx context.Context, projectID string, payload []byte) (string, error) {
	hint := guessProfileReceiptHint(payload)
	var parsed *normalizedProfile
	if normalized, err := normalizeProfilePayload(payload); err == nil {
		parsed = &normalized
		hint.ProfileID = firstNonEmptyText(parsed.Manifest.ProfileID, hint.ProfileID)
		hint.EventID = firstNonEmptyText(parsed.Manifest.EventID, hint.EventID, hint.ProfileID)
		hint.Transaction = firstNonEmptyText(parsed.Manifest.Transaction, hint.Transaction)
		hint.TraceID = firstNonEmptyText(parsed.Manifest.TraceID, hint.TraceID)
		hint.Release = firstNonEmptyText(parsed.Manifest.Release, hint.Release)
		hint.Environment = firstNonEmptyText(parsed.Manifest.Environment, hint.Environment)
		hint.Platform = firstNonEmptyText(parsed.Manifest.Platform, hint.Platform)
		hint.OccurredAt = firstNonZeroTime(parsed.Manifest.StartedAt, hint.OccurredAt)
	}
	hint.ProfileID = firstNonEmptyText(hint.ProfileID, hint.EventID, generateID())
	hint.EventID = firstNonEmptyText(hint.EventID, hint.ProfileID)
	if strings.TrimSpace(hint.Platform) == "" {
		hint.Platform = "profile"
	}
	if existing, err := s.lookupManifest(ctx, projectID, hint.ProfileID, hint.EventID); err == nil && existing != nil {
		samePayload, err := s.matchExistingProfilePayload(ctx, existing, payload)
		if err != nil {
			return "", err
		}
		if !samePayload {
			return "", fmt.Errorf("%w: profile %s already exists with different payload", errProfilePayloadConflict, existing.ProfileID)
		}
		return existing.ProfileID, nil
	}

	rawBlobKey := ""
	if s.blobs != nil {
		rawBlobKey = profileRawBlobKey(projectID, hint.ProfileID)
		if err := s.blobs.Put(ctx, rawBlobKey, payload); err != nil {
			return "", fmt.Errorf("store raw profile blob: %w", err)
		}
	}
	storedEvt, err := s.saveProfileReceipt(ctx, projectID, hint, rawBlobKey, payload)
	if err != nil {
		if rawBlobKey != "" && s.blobs != nil {
			_ = s.blobs.Delete(ctx, rawBlobKey)
		}
		return "", err
	}
	if storedEvt == nil {
		return "", store.ErrNotFound
	}
	storedEvt.NormalizedJSON = json.RawMessage(payload)
	if err := s.materializeStoredProfileEventParsed(ctx, storedEvt, payload, parsed); err != nil {
		return "", err
	}
	return hint.ProfileID, nil
}

func (s *ProfileStore) matchExistingProfilePayload(ctx context.Context, manifest *store.ProfileManifest, candidate []byte) (bool, error) {
	existing, err := s.loadStoredProfilePayload(ctx, manifest)
	if err != nil {
		return false, err
	}
	if len(existing) == 0 {
		return false, nil
	}
	existingCanonical, err := canonicalizeJSONObject(existing)
	if err != nil {
		return false, nil
	}
	candidateCanonical, err := canonicalizeJSONObject(candidate)
	if err != nil {
		return false, err
	}
	return string(existingCanonical) == string(candidateCanonical), nil
}

func (s *ProfileStore) loadStoredProfilePayload(ctx context.Context, manifest *store.ProfileManifest) ([]byte, error) {
	if manifest == nil {
		return nil, nil
	}
	if manifest.RawBlobKey != "" && s.blobs != nil {
		body, err := s.blobs.Get(ctx, manifest.RawBlobKey)
		if err == nil && len(body) > 0 {
			return body, nil
		}
	}
	var payload string
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(payload_json, '') FROM events WHERE id = ?`,
		manifest.EventRowID,
	).Scan(&payload)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("load existing profile payload: %w", err)
	}
	if strings.TrimSpace(payload) != "" {
		return []byte(payload), nil
	}
	return nil, nil
}

func (s *ProfileStore) saveProfileReceipt(ctx context.Context, projectID string, hint profileReceiptHint, rawBlobKey string, payload []byte) (*store.StoredEvent, error) {
	receipt := &store.StoredEvent{
		ID:             generateID(),
		ProjectID:      projectID,
		EventID:        hint.EventID,
		ReleaseID:      hint.Release,
		Environment:    hint.Environment,
		Platform:       hint.Platform,
		Level:          "info",
		EventType:      "profile",
		OccurredAt:     hint.OccurredAt,
		IngestedAt:     time.Now().UTC(),
		Message:        profileReceiptTitle(hint.Transaction),
		Title:          profileReceiptTitle(hint.Transaction),
		Culprit:        hint.Transaction,
		Tags:           profileReceiptTags(store.ProfileManifest{TraceID: hint.TraceID, Release: hint.Release, Environment: hint.Environment, Platform: hint.Platform}),
		NormalizedJSON: json.RawMessage(payload),
		PayloadKey:     rawBlobKey,
	}
	if receipt.OccurredAt.IsZero() {
		receipt.OccurredAt = receipt.IngestedAt
	}
	if err := NewEventStore(s.db).SaveEvent(ctx, receipt); err != nil {
		return nil, fmt.Errorf("save profile receipt: %w", err)
	}
	storedEvt, err := NewEventStore(s.db).GetEventByType(ctx, projectID, hint.ProfileID, "profile")
	if err != nil {
		return nil, fmt.Errorf("load profile receipt: %w", err)
	}
	if storedEvt == nil {
		return nil, store.ErrNotFound
	}
	if rawBlobKey != "" && storedEvt.PayloadKey == "" {
		if _, err := s.db.ExecContext(ctx, `UPDATE events SET payload_key = ? WHERE id = ?`, rawBlobKey, storedEvt.ID); err != nil {
			return nil, fmt.Errorf("update profile receipt payload key: %w", err)
		}
		storedEvt.PayloadKey = rawBlobKey
	}
	return storedEvt, nil
}
