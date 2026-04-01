package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"urgentry/internal/profilequery"
	"urgentry/internal/store"
)

func (s *ProfileStore) ListProfiles(ctx context.Context, projectID string, limit int) ([]store.ProfileManifest, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, event_row_id, project_id, COALESCE(event_id, ''), profile_id, COALESCE(trace_id, ''),
		        COALESCE(transaction_name, ''), COALESCE(release, ''), COALESCE(environment, ''),
		        COALESCE(platform, ''), COALESCE(profile_kind, ''), COALESCE(started_at, ''),
		        COALESCE(ended_at, ''), duration_ns, thread_count, sample_count, frame_count,
		        function_count, stack_count, processing_status, COALESCE(ingest_error, ''),
		        COALESCE(raw_blob_key, ''), COALESCE(created_at, '')
		 FROM profile_manifests
		 WHERE project_id = ?
		 ORDER BY COALESCE(started_at, created_at) DESC, created_at DESC
		 LIMIT ?`,
		projectID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list profile manifests: %w", err)
	}
	defer rows.Close()

	var manifests []store.ProfileManifest
	for rows.Next() {
		item, err := scanProfileManifestRows(rows)
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, item)
	}
	return manifests, rows.Err()
}

func (s *ProfileStore) GetProfile(ctx context.Context, projectID, profileID string) (*store.ProfileRecord, error) {
	if err := s.materializeProfile(ctx, projectID, profileID); err != nil {
		return nil, err
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT m.id, m.event_row_id, m.project_id, COALESCE(m.event_id, ''), m.profile_id, COALESCE(m.trace_id, ''),
		        COALESCE(m.transaction_name, ''), COALESCE(m.release, ''), COALESCE(m.environment, ''),
		        COALESCE(m.platform, ''), COALESCE(m.profile_kind, ''), COALESCE(m.started_at, ''),
		        COALESCE(m.ended_at, ''), m.duration_ns, m.thread_count, m.sample_count, m.frame_count,
		        m.function_count, m.stack_count, m.processing_status, COALESCE(m.ingest_error, ''),
		        COALESCE(m.raw_blob_key, ''), COALESCE(m.created_at, ''), COALESCE(e.payload_json, '')
		 FROM profile_manifests m
		 JOIN events e ON e.id = m.event_row_id
		 WHERE m.project_id = ? AND (m.profile_id = ? OR COALESCE(m.event_id, '') = ?)`,
		projectID, profileID, profileID,
	)
	record, err := scanProfileRecord(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, store.ErrNotFound
		}
		if err == store.ErrNotFound {
			return nil, err
		}
		return nil, fmt.Errorf("load profile record: %w", err)
	}
	if len(record.RawPayload) == 0 && record.Manifest.RawBlobKey != "" && s.blobs != nil {
		if body, err := s.blobs.Get(ctx, record.Manifest.RawBlobKey); err == nil {
			record.RawPayload = json.RawMessage(body)
		}
	}
	topFrames, err := s.loadTopProfileBreakdowns(ctx, record.Manifest.ID, true, 10)
	if err != nil {
		return nil, err
	}
	topFunctions, err := s.loadTopProfileBreakdowns(ctx, record.Manifest.ID, false, 10)
	if err != nil {
		return nil, err
	}
	record.TopFrames = topFrames
	record.TopFunctions = topFunctions
	return record, nil
}

func (s *ProfileStore) FindProfilesByTrace(ctx context.Context, projectID, traceID string, limit int) ([]store.ProfileReference, error) {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	return s.listProfileReferences(ctx, `
		SELECT m.project_id, COALESCE(m.event_id, ''), m.profile_id, COALESCE(m.trace_id, ''),
		       COALESCE(m.transaction_name, ''), COALESCE(m.release, ''), COALESCE(m.environment, ''),
		       COALESCE(m.platform, ''), m.duration_ns, m.sample_count, m.function_count,
		       COALESCE(m.started_at, m.created_at, ''),
		       COALESCE((
		           SELECT COALESCE(NULLIF(f.function_label, ''), f.frame_label, '')
		           FROM profile_samples ps
		           JOIN profile_stacks st ON st.id = ps.stack_id AND st.manifest_id = ps.manifest_id
		           JOIN profile_frames f ON f.id = st.leaf_frame_id
		           WHERE ps.manifest_id = m.id
		           GROUP BY COALESCE(NULLIF(f.function_label, ''), f.frame_label, '')
		           ORDER BY SUM(ps.weight) DESC, COALESCE(NULLIF(f.function_label, ''), f.frame_label, '') ASC
		           LIMIT 1
		       ), ''),
		       COALESCE((
		           SELECT SUM(ps.weight)
		           FROM profile_samples ps
		           JOIN profile_stacks st ON st.id = ps.stack_id AND st.manifest_id = ps.manifest_id
		           JOIN profile_frames f ON f.id = st.leaf_frame_id
		           WHERE ps.manifest_id = m.id
		           GROUP BY COALESCE(NULLIF(f.function_label, ''), f.frame_label, '')
		           ORDER BY SUM(ps.weight) DESC, COALESCE(NULLIF(f.function_label, ''), f.frame_label, '') ASC
		           LIMIT 1
		       ), 0)
		  FROM profile_manifests m
		 WHERE m.project_id = ?
		   AND m.processing_status = ?
		   AND COALESCE(m.trace_id, '') = ?
		 ORDER BY COALESCE(m.started_at, m.created_at) DESC, m.created_at DESC
		 LIMIT ?`,
		projectID,
		string(store.ProfileProcessingStatusCompleted),
		traceID,
		limit,
	)
}

func (s *ProfileStore) ListReleaseProfileHighlights(ctx context.Context, projectID, release string, limit int) ([]store.ProfileReference, error) {
	release = strings.TrimSpace(release)
	if release == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 6
	}
	return s.listProfileReferences(ctx, `
		SELECT m.project_id, COALESCE(m.event_id, ''), m.profile_id, COALESCE(m.trace_id, ''),
		       COALESCE(m.transaction_name, ''), COALESCE(m.release, ''), COALESCE(m.environment, ''),
		       COALESCE(m.platform, ''), m.duration_ns, m.sample_count, m.function_count,
		       COALESCE(m.started_at, m.created_at, ''),
		       COALESCE((
		           SELECT COALESCE(NULLIF(f.function_label, ''), f.frame_label, '')
		           FROM profile_samples ps
		           JOIN profile_stacks st ON st.id = ps.stack_id AND st.manifest_id = ps.manifest_id
		           JOIN profile_frames f ON f.id = st.leaf_frame_id
		           WHERE ps.manifest_id = m.id
		           GROUP BY COALESCE(NULLIF(f.function_label, ''), f.frame_label, '')
		           ORDER BY SUM(ps.weight) DESC, COALESCE(NULLIF(f.function_label, ''), f.frame_label, '') ASC
		           LIMIT 1
		       ), ''),
		       COALESCE((
		           SELECT SUM(ps.weight)
		           FROM profile_samples ps
		           JOIN profile_stacks st ON st.id = ps.stack_id AND st.manifest_id = ps.manifest_id
		           JOIN profile_frames f ON f.id = st.leaf_frame_id
		           WHERE ps.manifest_id = m.id
		           GROUP BY COALESCE(NULLIF(f.function_label, ''), f.frame_label, '')
		           ORDER BY SUM(ps.weight) DESC, COALESCE(NULLIF(f.function_label, ''), f.frame_label, '') ASC
		           LIMIT 1
		       ), 0)
		  FROM profile_manifests m
		 WHERE m.project_id = ?
		   AND m.processing_status = ?
		   AND COALESCE(m.release, '') = ?
		 ORDER BY m.duration_ns DESC, COALESCE(m.started_at, m.created_at) DESC, m.created_at DESC
		 LIMIT ?`,
		projectID,
		string(store.ProfileProcessingStatusCompleted),
		release,
		limit,
	)
}

func (s *ProfileStore) FindRelatedProfile(ctx context.Context, projectID, traceID, transaction, release string) (*store.ProfileReference, error) {
	if items, err := s.FindProfilesByTrace(ctx, projectID, traceID, 1); err != nil {
		return nil, err
	} else if len(items) > 0 {
		return &items[0], nil
	}
	transaction = strings.TrimSpace(transaction)
	release = strings.TrimSpace(release)
	if transaction != "" && release != "" {
		items, err := s.listProfileReferences(ctx, `
			SELECT m.project_id, COALESCE(m.event_id, ''), m.profile_id, COALESCE(m.trace_id, ''),
			       COALESCE(m.transaction_name, ''), COALESCE(m.release, ''), COALESCE(m.environment, ''),
			       COALESCE(m.platform, ''), m.duration_ns, m.sample_count, m.function_count,
			       COALESCE(m.started_at, m.created_at, ''),
			       COALESCE((
			           SELECT COALESCE(NULLIF(f.function_label, ''), f.frame_label, '')
			           FROM profile_samples ps
			           JOIN profile_stacks st ON st.id = ps.stack_id AND st.manifest_id = ps.manifest_id
			           JOIN profile_frames f ON f.id = st.leaf_frame_id
			           WHERE ps.manifest_id = m.id
			           GROUP BY COALESCE(NULLIF(f.function_label, ''), f.frame_label, '')
			           ORDER BY SUM(ps.weight) DESC, COALESCE(NULLIF(f.function_label, ''), f.frame_label, '') ASC
			           LIMIT 1
			       ), ''),
			       COALESCE((
			           SELECT SUM(ps.weight)
			           FROM profile_samples ps
			           JOIN profile_stacks st ON st.id = ps.stack_id AND st.manifest_id = ps.manifest_id
			           JOIN profile_frames f ON f.id = st.leaf_frame_id
			           WHERE ps.manifest_id = m.id
			           GROUP BY COALESCE(NULLIF(f.function_label, ''), f.frame_label, '')
			           ORDER BY SUM(ps.weight) DESC, COALESCE(NULLIF(f.function_label, ''), f.frame_label, '') ASC
			           LIMIT 1
			       ), 0)
			  FROM profile_manifests m
			 WHERE m.project_id = ?
			   AND m.processing_status = ?
			   AND COALESCE(m.transaction_name, '') = ?
			   AND COALESCE(m.release, '') = ?
			 ORDER BY COALESCE(m.started_at, m.created_at) DESC, m.created_at DESC
			 LIMIT 1`,
			projectID,
			string(store.ProfileProcessingStatusCompleted),
			transaction,
			release,
		)
		if err != nil {
			return nil, err
		}
		if len(items) > 0 {
			return &items[0], nil
		}
	}
	if release != "" {
		items, err := s.ListReleaseProfileHighlights(ctx, projectID, release, 1)
		if err != nil {
			return nil, err
		}
		if len(items) > 0 {
			return &items[0], nil
		}
	}
	if transaction == "" {
		return nil, nil
	}
	items, err := s.listProfileReferences(ctx, `
		SELECT m.project_id, COALESCE(m.event_id, ''), m.profile_id, COALESCE(m.trace_id, ''),
		       COALESCE(m.transaction_name, ''), COALESCE(m.release, ''), COALESCE(m.environment, ''),
		       COALESCE(m.platform, ''), m.duration_ns, m.sample_count, m.function_count,
		       COALESCE(m.started_at, m.created_at, ''),
		       COALESCE((
		           SELECT COALESCE(NULLIF(f.function_label, ''), f.frame_label, '')
		           FROM profile_samples ps
		           JOIN profile_stacks st ON st.id = ps.stack_id AND st.manifest_id = ps.manifest_id
		           JOIN profile_frames f ON f.id = st.leaf_frame_id
		           WHERE ps.manifest_id = m.id
		           GROUP BY COALESCE(NULLIF(f.function_label, ''), f.frame_label, '')
		           ORDER BY SUM(ps.weight) DESC, COALESCE(NULLIF(f.function_label, ''), f.frame_label, '') ASC
		           LIMIT 1
		       ), ''),
		       COALESCE((
		           SELECT SUM(ps.weight)
		           FROM profile_samples ps
		           JOIN profile_stacks st ON st.id = ps.stack_id AND st.manifest_id = ps.manifest_id
		           JOIN profile_frames f ON f.id = st.leaf_frame_id
		           WHERE ps.manifest_id = m.id
		           GROUP BY COALESCE(NULLIF(f.function_label, ''), f.frame_label, '')
		           ORDER BY SUM(ps.weight) DESC, COALESCE(NULLIF(f.function_label, ''), f.frame_label, '') ASC
		           LIMIT 1
		       ), 0)
		  FROM profile_manifests m
		 WHERE m.project_id = ?
		   AND m.processing_status = ?
		   AND COALESCE(m.transaction_name, '') = ?
		 ORDER BY COALESCE(m.started_at, m.created_at) DESC, m.created_at DESC
		 LIMIT 1`,
		projectID,
		string(store.ProfileProcessingStatusCompleted),
		transaction,
	)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	return &items[0], nil
}

func (s *ProfileStore) QueryTopDown(ctx context.Context, projectID string, filter store.ProfileQueryFilter) (*store.ProfileTree, error) {
	return s.queryProfileTree(ctx, projectID, filter, "top_down")
}

func (s *ProfileStore) QueryBottomUp(ctx context.Context, projectID string, filter store.ProfileQueryFilter) (*store.ProfileTree, error) {
	return s.queryProfileTree(ctx, projectID, filter, "bottom_up")
}

func (s *ProfileStore) QueryFlamegraph(ctx context.Context, projectID string, filter store.ProfileQueryFilter) (*store.ProfileTree, error) {
	return s.queryProfileTree(ctx, projectID, filter, "flamegraph")
}

func (s *ProfileStore) QueryHotPath(ctx context.Context, projectID string, filter store.ProfileQueryFilter) (*store.ProfileHotPath, error) {
	tree, err := s.queryProfileTree(ctx, projectID, filter, "top_down")
	if err != nil {
		return nil, err
	}
	return profilequery.BuildHotPath(tree), nil
}

func (s *ProfileStore) CompareProfiles(ctx context.Context, projectID string, filter store.ProfileComparisonFilter) (*store.ProfileComparison, error) {
	if strings.TrimSpace(filter.BaselineProfileID) == "" || strings.TrimSpace(filter.CandidateProfileID) == "" {
		return nil, fmt.Errorf("baseline and candidate profiles are required")
	}
	baselineManifest, err := s.resolveProfileManifest(ctx, projectID, store.ProfileQueryFilter{ProfileID: filter.BaselineProfileID})
	if err != nil {
		return nil, err
	}
	candidateManifest, err := s.resolveProfileManifest(ctx, projectID, store.ProfileQueryFilter{ProfileID: filter.CandidateProfileID})
	if err != nil {
		return nil, err
	}
	if err := profilequery.EnforceGuard(baselineManifest, 0); err != nil {
		return nil, err
	}
	if err := profilequery.EnforceGuard(candidateManifest, 0); err != nil {
		return nil, err
	}
	baselineThreadID, _, err := s.resolveProfileThread(ctx, baselineManifest.ID, filter.ThreadID)
	if err != nil {
		return nil, err
	}
	candidateThreadID, threadKey, err := s.resolveProfileThread(ctx, candidateManifest.ID, filter.ThreadID)
	if err != nil {
		return nil, err
	}
	baselineWeights, err := s.loadFunctionWeights(ctx, baselineManifest.ID, baselineThreadID)
	if err != nil {
		return nil, err
	}
	candidateWeights, err := s.loadFunctionWeights(ctx, candidateManifest.ID, candidateThreadID)
	if err != nil {
		return nil, err
	}
	baselineDurationNS, baselineSampleCount, err := s.loadProfileScopeTotals(ctx, baselineManifest, baselineThreadID)
	if err != nil {
		return nil, err
	}
	candidateDurationNS, candidateSampleCount, err := s.loadProfileScopeTotals(ctx, candidateManifest, candidateThreadID)
	if err != nil {
		return nil, err
	}
	return profilequery.BuildComparison(
		baselineManifest,
		candidateManifest,
		threadKey,
		baselineWeights,
		candidateWeights,
		baselineDurationNS,
		candidateDurationNS,
		baselineSampleCount,
		candidateSampleCount,
		filter,
	), nil
}

func (s *ProfileStore) queryProfileTree(ctx context.Context, projectID string, filter store.ProfileQueryFilter, mode string) (*store.ProfileTree, error) {
	manifest, err := s.resolveProfileManifest(ctx, projectID, filter)
	if err != nil {
		return nil, err
	}
	if err := profilequery.EnforceGuard(manifest, filter.MaxNodes); err != nil {
		return nil, err
	}
	threadRowID, threadKey, err := s.resolveProfileThread(ctx, manifest.ID, filter.ThreadID)
	if err != nil {
		return nil, err
	}
	stacks, err := s.loadProfileStackAggregates(ctx, manifest.ID, threadRowID)
	if err != nil {
		return nil, err
	}
	return profilequery.BuildTree(manifest.ProfileID, threadKey, mode, stacks, filter), nil
}

func (s *ProfileStore) listProfileReferences(ctx context.Context, query string, args ...any) ([]store.ProfileReference, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list profile references: %w", err)
	}
	defer rows.Close()

	var refs []store.ProfileReference
	for rows.Next() {
		var item store.ProfileReference
		var (
			eventID, traceID, transaction sql.NullString
			release, environment          sql.NullString
			platform, startedAt           sql.NullString
			topFunction                   sql.NullString
			topFunctionCount              sql.NullInt64
		)
		if err := rows.Scan(
			&item.ProjectID,
			&eventID,
			&item.ProfileID,
			&traceID,
			&transaction,
			&release,
			&environment,
			&platform,
			&item.DurationNS,
			&item.SampleCount,
			&item.FunctionCount,
			&startedAt,
			&topFunction,
			&topFunctionCount,
		); err != nil {
			return nil, fmt.Errorf("scan profile reference: %w", err)
		}
		item.EventID = nullStr(eventID)
		item.TraceID = nullStr(traceID)
		item.Transaction = nullStr(transaction)
		item.Release = nullStr(release)
		item.Environment = nullStr(environment)
		item.Platform = nullStr(platform)
		item.StartedAt = parseTime(nullStr(startedAt))
		item.TopFunction = nullStr(topFunction)
		if topFunctionCount.Valid {
			item.TopFunctionCnt = int(topFunctionCount.Int64)
		}
		refs = append(refs, item)
	}
	return refs, rows.Err()
}

func (s *ProfileStore) resolveProfileManifest(ctx context.Context, projectID string, filter store.ProfileQueryFilter) (*store.ProfileManifest, error) {
	query := `SELECT id, event_row_id, project_id, COALESCE(event_id, ''), profile_id, COALESCE(trace_id, ''),
		        COALESCE(transaction_name, ''), COALESCE(release, ''), COALESCE(environment, ''),
		        COALESCE(platform, ''), COALESCE(profile_kind, ''), COALESCE(started_at, ''),
		        COALESCE(ended_at, ''), duration_ns, thread_count, sample_count, frame_count,
		        function_count, stack_count, processing_status, COALESCE(ingest_error, ''),
		        COALESCE(raw_blob_key, ''), COALESCE(created_at, '')
		 FROM profile_manifests
		 WHERE project_id = ?`
	args := []any{projectID}
	if strings.TrimSpace(filter.ProfileID) != "" {
		query += ` AND profile_id = ?`
		args = append(args, strings.TrimSpace(filter.ProfileID))
	} else {
		query += ` AND processing_status = ?`
		args = append(args, store.ProfileProcessingStatusCompleted)
	}
	if strings.TrimSpace(filter.Transaction) != "" {
		query += ` AND transaction_name = ?`
		args = append(args, strings.TrimSpace(filter.Transaction))
	}
	if strings.TrimSpace(filter.Release) != "" {
		query += ` AND release = ?`
		args = append(args, strings.TrimSpace(filter.Release))
	}
	if strings.TrimSpace(filter.Environment) != "" {
		query += ` AND environment = ?`
		args = append(args, strings.TrimSpace(filter.Environment))
	}
	if !filter.StartedAfter.IsZero() {
		query += ` AND COALESCE(started_at, created_at) >= ?`
		args = append(args, formatOptionalTime(filter.StartedAfter))
	}
	if !filter.EndedBefore.IsZero() {
		query += ` AND COALESCE(ended_at, started_at, created_at) <= ?`
		args = append(args, formatOptionalTime(filter.EndedBefore))
	}
	query += ` ORDER BY COALESCE(started_at, created_at) DESC, created_at DESC LIMIT 1`
	item, err := scanProfileManifest(s.db.QueryRowContext(ctx, query, args...))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return item, nil
}

func (s *ProfileStore) loadProfileScopeTotals(ctx context.Context, manifest *store.ProfileManifest, threadRowID string) (int64, int, error) {
	if manifest == nil {
		return 0, 0, store.ErrNotFound
	}
	if strings.TrimSpace(threadRowID) == "" {
		return manifest.DurationNS, manifest.SampleCount, nil
	}
	var durationNS int64
	var sampleCount int
	if err := s.db.QueryRowContext(ctx,
		`SELECT duration_ns, sample_count
		 FROM profile_threads
		 WHERE id = ?`,
		threadRowID,
	).Scan(&durationNS, &sampleCount); err != nil {
		if err == sql.ErrNoRows {
			return 0, 0, store.ErrNotFound
		}
		return 0, 0, err
	}
	return durationNS, sampleCount, nil
}

func (s *ProfileStore) resolveProfileThread(ctx context.Context, manifestID, threadID string) (string, string, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", "", nil
	}
	var rowID string
	var threadKey string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, thread_key
		 FROM profile_threads
		 WHERE manifest_id = ? AND (thread_key = ? OR thread_name = ?)
		 LIMIT 1`,
		manifestID, threadID, threadID,
	).Scan(&rowID, &threadKey)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", store.ErrNotFound
		}
		return "", "", err
	}
	return rowID, threadKey, nil
}

func (s *ProfileStore) loadProfileStackAggregates(ctx context.Context, manifestID, threadRowID string) ([]profilequery.StackAggregate, error) {
	query := `SELECT s.stack_id, COUNT(*), SUM(s.weight), sf.position, f.id, COALESCE(f.frame_label, '')
		FROM profile_samples s
		JOIN profile_stack_frames sf ON sf.manifest_id = s.manifest_id AND sf.stack_id = s.stack_id
		JOIN profile_frames f ON f.id = sf.frame_id
		WHERE s.manifest_id = ?`
	args := []any{manifestID}
	if threadRowID != "" {
		query += ` AND s.thread_row_id = ?`
		args = append(args, threadRowID)
	}
	query += ` GROUP BY s.stack_id, sf.position, f.id, f.frame_label
		ORDER BY s.stack_id ASC, sf.position ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list profile stack aggregates: %w", err)
	}
	defer rows.Close()

	aggregates := map[string]*profilequery.StackAggregate{}
	var order []string
	for rows.Next() {
		var stackID, frameID, frameName string
		var sampleCount, weight, position int
		if err := rows.Scan(&stackID, &sampleCount, &weight, &position, &frameID, &frameName); err != nil {
			return nil, err
		}
		item := aggregates[stackID]
		if item == nil {
			item = &profilequery.StackAggregate{Weight: weight, SampleCount: sampleCount}
			aggregates[stackID] = item
			order = append(order, stackID)
		}
		item.Frames = append(item.Frames, profilequery.TreeFrame{ID: frameID, Name: frameName})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]profilequery.StackAggregate, 0, len(order))
	for _, stackID := range order {
		result = append(result, *aggregates[stackID])
	}
	return result, nil
}

func (s *ProfileStore) loadFunctionWeights(ctx context.Context, manifestID, threadRowID string) (map[string]int, error) {
	query := `SELECT COALESCE(f.function_label, ''), SUM(s.weight)
		FROM profile_samples s
		JOIN profile_stack_frames sf ON sf.manifest_id = s.manifest_id AND sf.stack_id = s.stack_id
		JOIN profile_frames f ON f.id = sf.frame_id
		WHERE s.manifest_id = ?`
	args := []any{manifestID}
	if threadRowID != "" {
		query += ` AND s.thread_row_id = ?`
		args = append(args, threadRowID)
	}
	query += ` GROUP BY f.function_label`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list profile function weights: %w", err)
	}
	defer rows.Close()
	result := map[string]int{}
	for rows.Next() {
		var name string
		var weight int
		if err := rows.Scan(&name, &weight); err != nil {
			return nil, err
		}
		if strings.TrimSpace(name) != "" {
			result[name] = weight
		}
	}
	return result, rows.Err()
}
