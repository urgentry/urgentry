package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"urgentry/internal/store"
)

type ReplayConfigStore struct {
	db *sql.DB
}

func NewReplayConfigStore(db *sql.DB) *ReplayConfigStore {
	return &ReplayConfigStore{db: db}
}

func DefaultReplayIngestPolicy() store.ReplayIngestPolicy {
	policy, err := canonicalReplayIngestPolicy(store.ReplayIngestPolicy{})
	if err != nil {
		return store.ReplayIngestPolicy{SampleRate: 1, MaxBytes: 10 << 20}
	}
	return policy
}

func (s *ReplayConfigStore) GetReplayIngestPolicy(ctx context.Context, projectID string) (store.ReplayIngestPolicy, error) {
	if s == nil || s.db == nil {
		return DefaultReplayIngestPolicy(), nil
	}
	var sampleRate float64
	var maxBytes int64
	var scrubFieldsJSON string
	var scrubSelectorsJSON string
	err := s.db.QueryRowContext(ctx,
		`SELECT sample_rate, max_bytes, COALESCE(scrub_fields_json, '[]'), COALESCE(scrub_selectors_json, '[]')
		 FROM project_replay_configs
		 WHERE project_id = ?`,
		projectID,
	).Scan(&sampleRate, &maxBytes, &scrubFieldsJSON, &scrubSelectorsJSON)
	if err == sql.ErrNoRows {
		return DefaultReplayIngestPolicy(), nil
	}
	if err != nil {
		return store.ReplayIngestPolicy{}, fmt.Errorf("load replay ingest policy: %w", err)
	}
	policy := store.ReplayIngestPolicy{
		SampleRate: sampleRate,
		MaxBytes:   maxBytes,
	}
	_ = json.Unmarshal([]byte(scrubFieldsJSON), &policy.ScrubFields)
	_ = json.Unmarshal([]byte(scrubSelectorsJSON), &policy.ScrubSelectors)
	canonical, err := canonicalReplayIngestPolicy(policy)
	if err != nil {
		return store.ReplayIngestPolicy{}, err
	}
	return canonical, nil
}

func upsertReplayIngestPolicy(ctx context.Context, tx *sql.Tx, projectID string, policy store.ReplayIngestPolicy) error {
	canonical, err := canonicalReplayIngestPolicy(policy)
	if err != nil {
		return err
	}
	scrubFieldsJSON, _ := json.Marshal(canonical.ScrubFields)
	scrubSelectorsJSON, _ := json.Marshal(canonical.ScrubSelectors)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = tx.ExecContext(ctx,
		`INSERT INTO project_replay_configs
			(project_id, sample_rate, max_bytes, scrub_fields_json, scrub_selectors_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(project_id) DO UPDATE SET
			sample_rate = excluded.sample_rate,
			max_bytes = excluded.max_bytes,
			scrub_fields_json = excluded.scrub_fields_json,
			scrub_selectors_json = excluded.scrub_selectors_json,
			updated_at = excluded.updated_at`,
		projectID,
		canonical.SampleRate,
		canonical.MaxBytes,
		string(scrubFieldsJSON),
		string(scrubSelectorsJSON),
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("upsert replay ingest policy: %w", err)
	}
	return nil
}

func canonicalReplayIngestPolicy(policy store.ReplayIngestPolicy) (store.ReplayIngestPolicy, error) {
	return store.CanonicalReplayIngestPolicy(policy)
}
