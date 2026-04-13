package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/sqlutil"
	"urgentry/internal/store"
)

func (s *ProfileStore) materializeProfile(ctx context.Context, projectID, profileID string) error {
	existing, err := s.lookupManifest(ctx, projectID, profileID, profileID)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}
	evt, err := NewEventStore(s.db).GetEventByType(ctx, projectID, profileID, "profile")
	if err != nil {
		return err
	}
	if evt == nil {
		return store.ErrNotFound
	}
	return s.materializeStoredProfileEvent(ctx, evt, evt.NormalizedJSON)
}

func (s *ProfileStore) materializeStoredProfileEvent(ctx context.Context, evt *store.StoredEvent, rawPayload []byte) error {
	return s.materializeStoredProfileEventParsed(ctx, evt, rawPayload, nil)
}

func (s *ProfileStore) materializeStoredProfileEventParsed(ctx context.Context, evt *store.StoredEvent, rawPayload []byte, parsed *normalizedProfile) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := materializeProfileEventWithQuerierParsed(ctx, tx, s.blobs, evt, rawPayload, parsed); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func materializeProfileEventWithQuerier(ctx context.Context, db execQuerier, blobs store.BlobStore, evt *store.StoredEvent, rawPayload []byte) error {
	return materializeProfileEventWithQuerierParsed(ctx, db, blobs, evt, rawPayload, nil)
}

func materializeProfileEventWithQuerierParsed(ctx context.Context, db execQuerier, blobs store.BlobStore, evt *store.StoredEvent, rawPayload []byte, parsed *normalizedProfile) error {
	if evt == nil {
		return store.ErrNotFound
	}
	var raw []byte
	if evt.PayloadKey != "" && blobs != nil {
		body, err := blobs.Get(ctx, evt.PayloadKey)
		if err == nil && len(body) > 0 {
			raw = body
		}
	}
	if len(raw) == 0 {
		raw = rawPayload
	}
	if len(raw) == 0 {
		raw = evt.NormalizedJSON
	}

	if evt.PayloadKey == "" && len(raw) > 0 && blobs != nil {
		key := profileRawBlobKey(evt.ProjectID, guessProfileID(evt, raw))
		if err := blobs.Put(ctx, key, raw); err != nil {
			return fmt.Errorf("store rebuilt raw profile blob: %w", err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE events SET payload_key = ? WHERE id = ?`, key, evt.ID); err != nil {
			return fmt.Errorf("update rebuilt profile payload key: %w", err)
		}
		evt.PayloadKey = key
	}

	var normalized normalizedProfile
	if parsed != nil {
		normalized = *parsed
	} else {
		var err error
		normalized, err = normalizeProfilePayload(raw)
		if err != nil {
			return upsertFailedProfileManifestTx(ctx, db, evt, err.Error())
		}
	}
	normalized.Manifest.ProjectID = evt.ProjectID
	normalized.Manifest.EventRowID = evt.ID
	if normalized.Manifest.EventID == "" {
		normalized.Manifest.EventID = evt.EventID
	}
	if normalized.Manifest.ProfileID == "" {
		normalized.Manifest.ProfileID = evt.EventID
	}
	if normalized.Manifest.Transaction == "" {
		normalized.Manifest.Transaction = strings.TrimSpace(evt.Culprit)
	}
	if normalized.Manifest.Release == "" {
		normalized.Manifest.Release = strings.TrimSpace(evt.ReleaseID)
	}
	if normalized.Manifest.Environment == "" {
		normalized.Manifest.Environment = strings.TrimSpace(evt.Environment)
	}
	if normalized.Manifest.Platform == "" {
		normalized.Manifest.Platform = firstNonEmptyText(strings.TrimSpace(evt.Platform), "profile")
	}
	if normalized.Manifest.StartedAt.IsZero() {
		normalized.Manifest.StartedAt = evt.OccurredAt
	}
	if normalized.Manifest.RawBlobKey == "" {
		normalized.Manifest.RawBlobKey = evt.PayloadKey
	}
	normalized.Manifest.DateCreated = evt.OccurredAt
	if normalized.Manifest.DateCreated.IsZero() {
		normalized.Manifest.DateCreated = evt.IngestedAt
	}
	if normalized.Manifest.DateCreated.IsZero() {
		normalized.Manifest.DateCreated = time.Now().UTC()
	}

	manifestID, err := upsertProfileManifestTx(ctx, db, normalized.Manifest)
	if err != nil {
		return err
	}
	if err := clearProfileGraphTx(ctx, db, manifestID); err != nil {
		return err
	}
	if normalized.Manifest.ProcessingStatus == store.ProfileProcessingStatusCompleted {
		assignManifestID(manifestID, &normalized)
		if err := insertProfileGraphTx(ctx, db, normalized); err != nil {
			return err
		}
	}
	return nil
}

func upsertFailedProfileManifestTx(ctx context.Context, db execQuerier, evt *store.StoredEvent, reason string) error {
	manifest := store.ProfileManifest{
		ProjectID:        evt.ProjectID,
		EventRowID:       evt.ID,
		EventID:          evt.EventID,
		ProfileID:        evt.EventID,
		TraceID:          strings.TrimSpace(evt.Tags["trace_id"]),
		Transaction:      strings.TrimSpace(evt.Culprit),
		Release:          strings.TrimSpace(evt.ReleaseID),
		Environment:      strings.TrimSpace(evt.Environment),
		Platform:         firstNonEmptyText(strings.TrimSpace(evt.Platform), "profile"),
		ProfileKind:      "sampled",
		StartedAt:        evt.OccurredAt,
		ProcessingStatus: store.ProfileProcessingStatusFailed,
		IngestError:      strings.TrimSpace(reason),
		RawBlobKey:       strings.TrimSpace(evt.PayloadKey),
		DateCreated:      firstNonZeroTime(evt.OccurredAt, evt.IngestedAt, time.Now().UTC()),
	}
	manifestID, err := upsertProfileManifestTx(ctx, db, manifest)
	if err != nil {
		return err
	}
	if err := clearProfileGraphTx(ctx, db, manifestID); err != nil {
		return err
	}
	return nil
}

func (s *ProfileStore) lookupManifest(ctx context.Context, projectID, profileID, eventID string) (*store.ProfileManifest, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, event_row_id, project_id, COALESCE(event_id, ''), profile_id, COALESCE(trace_id, ''),
		        COALESCE(transaction_name, ''), COALESCE(release, ''), COALESCE(environment, ''),
		        COALESCE(platform, ''), COALESCE(profile_kind, ''), COALESCE(started_at, ''),
		        COALESCE(ended_at, ''), duration_ns, thread_count, sample_count, frame_count,
		        function_count, stack_count, processing_status, COALESCE(ingest_error, ''),
		        COALESCE(raw_blob_key, ''), COALESCE(created_at, '')
		 FROM profile_manifests
		 WHERE project_id = ? AND (profile_id = ? OR COALESCE(event_id, '') = ?)
		 LIMIT 1`,
		projectID, profileID, eventID,
	)
	item, err := scanProfileManifest(row)
	if err != nil {
		if err == sql.ErrNoRows || err == store.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return item, nil
}

func loadTopProfileBreakdowns(ctx context.Context, db *sql.DB, manifestID string, frameLabels bool, limit int) ([]store.ProfileBreakdown, error) {
	labelColumn := "f.function_label"
	if frameLabels {
		labelColumn = "f.frame_label"
	}
	rows, err := db.QueryContext(ctx,
		`SELECT `+labelColumn+`, SUM(s.weight)
		 FROM profile_samples s
		 JOIN profile_stacks st ON st.id = s.stack_id
		 JOIN profile_frames f ON f.id = st.leaf_frame_id
		 WHERE s.manifest_id = ?
		 GROUP BY `+labelColumn+`
		 ORDER BY SUM(s.weight) DESC, `+labelColumn+` ASC
		 LIMIT ?`,
		manifestID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list profile breakdowns: %w", err)
	}
	defer rows.Close()

	var items []store.ProfileBreakdown
	for rows.Next() {
		var item store.ProfileBreakdown
		if err := rows.Scan(&item.Name, &item.Count); err != nil {
			return nil, err
		}
		if strings.TrimSpace(item.Name) == "" {
			continue
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *ProfileStore) loadTopProfileBreakdowns(ctx context.Context, manifestID string, frameLabels bool, limit int) ([]store.ProfileBreakdown, error) {
	if limit <= 0 {
		limit = 10
	}
	return loadTopProfileBreakdowns(ctx, s.db, manifestID, frameLabels, limit)
}

func upsertProfileManifestTx(ctx context.Context, db execQuerier, manifest store.ProfileManifest) (string, error) {
	manifestID := strings.TrimSpace(manifest.ID)
	if manifestID == "" {
		_ = db.QueryRowContext(ctx,
			`SELECT id FROM profile_manifests WHERE project_id = ? AND profile_id = ?`,
			manifest.ProjectID, manifest.ProfileID,
		).Scan(&manifestID)
	}
	if manifestID == "" {
		manifestID = generateID()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	createdAt := manifest.DateCreated.UTC().Format(time.RFC3339)
	if manifest.DateCreated.IsZero() {
		createdAt = now
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO profile_manifests
			(id, event_row_id, project_id, event_id, profile_id, trace_id, transaction_name, release, environment,
			 platform, profile_kind, started_at, ended_at, duration_ns, thread_count, sample_count, frame_count,
			 function_count, stack_count, processing_status, ingest_error, raw_blob_key, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(project_id, profile_id) DO UPDATE SET
			event_row_id = excluded.event_row_id,
			event_id = excluded.event_id,
			trace_id = excluded.trace_id,
			transaction_name = excluded.transaction_name,
			release = excluded.release,
			environment = excluded.environment,
			platform = excluded.platform,
			profile_kind = excluded.profile_kind,
			started_at = excluded.started_at,
			ended_at = excluded.ended_at,
			duration_ns = excluded.duration_ns,
			thread_count = excluded.thread_count,
			sample_count = excluded.sample_count,
			frame_count = excluded.frame_count,
			function_count = excluded.function_count,
			stack_count = excluded.stack_count,
			processing_status = excluded.processing_status,
			ingest_error = excluded.ingest_error,
			raw_blob_key = excluded.raw_blob_key,
			updated_at = excluded.updated_at`,
		manifestID,
		manifest.EventRowID,
		manifest.ProjectID,
		manifest.EventID,
		manifest.ProfileID,
		nullIfEmpty(manifest.TraceID),
		nullIfEmpty(manifest.Transaction),
		nullIfEmpty(manifest.Release),
		nullIfEmpty(manifest.Environment),
		nullIfEmpty(manifest.Platform),
		nullIfEmpty(manifest.ProfileKind),
		nullIfEmpty(formatOptionalTime(manifest.StartedAt)),
		nullIfEmpty(formatOptionalTime(manifest.EndedAt)),
		manifest.DurationNS,
		manifest.ThreadCount,
		manifest.SampleCount,
		manifest.FrameCount,
		manifest.FunctionCount,
		manifest.StackCount,
		string(manifest.ProcessingStatus),
		nullIfEmpty(manifest.IngestError),
		nullIfEmpty(manifest.RawBlobKey),
		createdAt,
		now,
	)
	if err != nil {
		return "", fmt.Errorf("upsert profile manifest: %w", err)
	}
	return manifestID, nil
}

func clearProfileGraphTx(ctx context.Context, db execQuerier, manifestID string) error {
	for _, query := range []string{
		`DELETE FROM profile_samples WHERE manifest_id = ?`,
		`DELETE FROM profile_stack_frames WHERE manifest_id = ?`,
		`DELETE FROM profile_stacks WHERE manifest_id = ?`,
		`DELETE FROM profile_frames WHERE manifest_id = ?`,
		`DELETE FROM profile_threads WHERE manifest_id = ?`,
	} {
		if _, err := db.ExecContext(ctx, query, manifestID); err != nil {
			return err
		}
	}
	return nil
}

func insertProfileGraphTx(ctx context.Context, db execQuerier, result normalizedProfile) error {
	for _, item := range result.Threads {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO profile_threads
				(id, manifest_id, thread_key, thread_name, thread_role, is_main, sample_count, duration_ns)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID, item.ManifestID, item.ThreadKey, nullIfEmpty(item.ThreadName), nullIfEmpty(item.ThreadRole), boolToInt(item.IsMain), item.SampleCount, item.DurationNS,
		); err != nil {
			return fmt.Errorf("insert profile thread: %w", err)
		}
	}
	for _, item := range result.Frames {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO profile_frames
				(id, manifest_id, frame_key, frame_label, function_label, function_name, module_name, package_name, filename, lineno, in_app, image_ref)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID, item.ManifestID, item.FrameKey, item.FrameLabel, item.FunctionLabel, nullIfEmpty(item.FunctionName), nullIfEmpty(item.ModuleName),
			nullIfEmpty(item.PackageName), nullIfEmpty(item.Filename), item.Lineno, boolToInt(item.InApp), nullIfEmpty(item.ImageRef),
		); err != nil {
			return fmt.Errorf("insert profile frame: %w", err)
		}
	}
	for _, item := range result.Stacks {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO profile_stacks
				(id, manifest_id, stack_key, leaf_frame_id, root_frame_id, depth)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			item.ID, item.ManifestID, item.StackKey, item.LeafFrameID, item.RootFrameID, item.Depth,
		); err != nil {
			return fmt.Errorf("insert profile stack: %w", err)
		}
	}
	for _, item := range result.StackFrames {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO profile_stack_frames
				(manifest_id, stack_id, position, frame_id)
			 VALUES (?, ?, ?, ?)`,
			item.ManifestID, item.StackID, item.Position, item.FrameID,
		); err != nil {
			return fmt.Errorf("insert profile stack frame: %w", err)
		}
	}
	for _, item := range result.Samples {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO profile_samples
				(id, manifest_id, thread_row_id, stack_id, ts_ns, weight, wall_time_ns, queue_time_ns, cpu_time_ns, is_idle)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID, item.ManifestID, item.ThreadRowID, item.StackID, nullableInt64(item.TSNS), item.Weight, nullableInt64(item.WallTimeNS), nullableInt64(item.QueueTimeNS), nullableInt64(item.CPUTimeNS), boolToInt(item.IsIdle),
		); err != nil {
			return fmt.Errorf("insert profile sample: %w", err)
		}
	}
	return nil
}

func scanProfileManifest(row *sql.Row) (*store.ProfileManifest, error) {
	var createdAt, startedAt, endedAt sql.NullString
	var item store.ProfileManifest
	err := row.Scan(
		&item.ID,
		&item.EventRowID,
		&item.ProjectID,
		&item.EventID,
		&item.ProfileID,
		&item.TraceID,
		&item.Transaction,
		&item.Release,
		&item.Environment,
		&item.Platform,
		&item.ProfileKind,
		&startedAt,
		&endedAt,
		&item.DurationNS,
		&item.ThreadCount,
		&item.SampleCount,
		&item.FrameCount,
		&item.FunctionCount,
		&item.StackCount,
		&item.ProcessingStatus,
		&item.IngestError,
		&item.RawBlobKey,
		&createdAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	item.StartedAt = sqlutil.ParseDBTime(startedAt.String)
	item.EndedAt = sqlutil.ParseDBTime(endedAt.String)
	item.DateCreated = sqlutil.ParseDBTime(createdAt.String)
	return &item, nil
}

func scanProfileManifestRows(rows *sql.Rows) (store.ProfileManifest, error) {
	var createdAt, startedAt, endedAt sql.NullString
	var item store.ProfileManifest
	err := rows.Scan(
		&item.ID,
		&item.EventRowID,
		&item.ProjectID,
		&item.EventID,
		&item.ProfileID,
		&item.TraceID,
		&item.Transaction,
		&item.Release,
		&item.Environment,
		&item.Platform,
		&item.ProfileKind,
		&startedAt,
		&endedAt,
		&item.DurationNS,
		&item.ThreadCount,
		&item.SampleCount,
		&item.FrameCount,
		&item.FunctionCount,
		&item.StackCount,
		&item.ProcessingStatus,
		&item.IngestError,
		&item.RawBlobKey,
		&createdAt,
	)
	if err != nil {
		return store.ProfileManifest{}, err
	}
	item.StartedAt = sqlutil.ParseDBTime(startedAt.String)
	item.EndedAt = sqlutil.ParseDBTime(endedAt.String)
	item.DateCreated = sqlutil.ParseDBTime(createdAt.String)
	return item, nil
}

func scanProfileRecord(row *sql.Row) (*store.ProfileRecord, error) {
	var payload sql.NullString
	item, err := scanProfileManifestFromRecord(row, &payload)
	if err != nil {
		return nil, err
	}
	return &store.ProfileRecord{
		Manifest:   item,
		RawPayload: json.RawMessage(payload.String),
	}, nil
}

func scanProfileManifestFromRecord(row *sql.Row, payload *sql.NullString) (store.ProfileManifest, error) {
	var createdAt, startedAt, endedAt sql.NullString
	var item store.ProfileManifest
	err := row.Scan(
		&item.ID,
		&item.EventRowID,
		&item.ProjectID,
		&item.EventID,
		&item.ProfileID,
		&item.TraceID,
		&item.Transaction,
		&item.Release,
		&item.Environment,
		&item.Platform,
		&item.ProfileKind,
		&startedAt,
		&endedAt,
		&item.DurationNS,
		&item.ThreadCount,
		&item.SampleCount,
		&item.FrameCount,
		&item.FunctionCount,
		&item.StackCount,
		&item.ProcessingStatus,
		&item.IngestError,
		&item.RawBlobKey,
		&createdAt,
		payload,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return store.ProfileManifest{}, store.ErrNotFound
		}
		return store.ProfileManifest{}, err
	}
	item.StartedAt = sqlutil.ParseDBTime(startedAt.String)
	item.EndedAt = sqlutil.ParseDBTime(endedAt.String)
	item.DateCreated = sqlutil.ParseDBTime(createdAt.String)
	return item, nil
}
