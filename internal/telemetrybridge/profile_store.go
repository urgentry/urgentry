package telemetrybridge

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"urgentry/internal/profilequery"
	"urgentry/internal/store"
)

var _ store.ProfileReadStore = (*ProfileReadStore)(nil)

type ProfileReadStore struct {
	db    *sql.DB
	blobs store.BlobStore
}

func NewProfileReadStore(db *sql.DB, blobs store.BlobStore) *ProfileReadStore {
	return &ProfileReadStore{db: db, blobs: blobs}
}

func (s *ProfileReadStore) ListProfiles(ctx context.Context, projectID string, limit int) ([]store.ProfileManifest, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, profile_id, COALESCE(event_id, ''), COALESCE(trace_id, ''),
		       COALESCE(transaction_name, ''), COALESCE(release, ''), COALESCE(environment, ''),
		       COALESCE(platform, ''), COALESCE(profile_kind, ''), started_at, ended_at, duration_ns,
		       thread_count, sample_count, frame_count, function_count, stack_count,
		       COALESCE(processing_status, 'completed'), COALESCE(ingest_error, ''),
		       COALESCE(raw_blob_key, ''), created_at, COALESCE(payload_json::text, '')
		  FROM telemetry.profile_manifests
		 WHERE project_id = $1
		 ORDER BY COALESCE(started_at, created_at) DESC, created_at DESC
		 LIMIT $2`,
		projectID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list bridge profiles: %w", err)
	}
	defer rows.Close()

	items := make([]store.ProfileManifest, 0, limit)
	for rows.Next() {
		item, _, err := scanBridgeProfileManifest(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *ProfileReadStore) GetProfile(ctx context.Context, projectID, profileID string) (*store.ProfileRecord, error) {
	record, err := s.loadProfileRecord(ctx, `
		SELECT id, project_id, profile_id, COALESCE(event_id, ''), COALESCE(trace_id, ''),
		       COALESCE(transaction_name, ''), COALESCE(release, ''), COALESCE(environment, ''),
		       COALESCE(platform, ''), COALESCE(profile_kind, ''), started_at, ended_at, duration_ns,
		       thread_count, sample_count, frame_count, function_count, stack_count,
		       COALESCE(processing_status, 'completed'), COALESCE(ingest_error, ''),
		       COALESCE(raw_blob_key, ''), created_at, COALESCE(payload_json::text, '')
		  FROM telemetry.profile_manifests
		 WHERE project_id = $1 AND (profile_id = $2 OR COALESCE(event_id, '') = $2)
		 LIMIT 1`,
		projectID, profileID,
	)
	if err != nil {
		if err == sql.ErrNoRows || err == store.ErrNotFound {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return record, nil
}

func (s *ProfileReadStore) FindProfilesByTrace(ctx context.Context, projectID, traceID string, limit int) ([]store.ProfileReference, error) {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	return s.listProfileReferences(ctx, `
		SELECT id, project_id, profile_id, COALESCE(event_id, ''), COALESCE(trace_id, ''),
		       COALESCE(transaction_name, ''), COALESCE(release, ''), COALESCE(environment, ''),
		       COALESCE(platform, ''), COALESCE(profile_kind, ''), started_at, ended_at, duration_ns,
		       thread_count, sample_count, frame_count, function_count, stack_count,
		       COALESCE(processing_status, 'completed'), COALESCE(ingest_error, ''),
		       COALESCE(raw_blob_key, ''), created_at, COALESCE(payload_json::text, '')
		  FROM telemetry.profile_manifests
		 WHERE project_id = $1
		   AND COALESCE(processing_status, 'completed') = $2
		   AND COALESCE(trace_id, '') = $3
		 ORDER BY COALESCE(started_at, created_at) DESC, created_at DESC
		 LIMIT $4`,
		projectID, string(store.ProfileProcessingStatusCompleted), traceID, limit,
	)
}

func (s *ProfileReadStore) ListReleaseProfileHighlights(ctx context.Context, projectID, release string, limit int) ([]store.ProfileReference, error) {
	release = strings.TrimSpace(release)
	if release == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 6
	}
	return s.listProfileReferences(ctx, `
		SELECT id, project_id, profile_id, COALESCE(event_id, ''), COALESCE(trace_id, ''),
		       COALESCE(transaction_name, ''), COALESCE(release, ''), COALESCE(environment, ''),
		       COALESCE(platform, ''), COALESCE(profile_kind, ''), started_at, ended_at, duration_ns,
		       thread_count, sample_count, frame_count, function_count, stack_count,
		       COALESCE(processing_status, 'completed'), COALESCE(ingest_error, ''),
		       COALESCE(raw_blob_key, ''), created_at, COALESCE(payload_json::text, '')
		  FROM telemetry.profile_manifests
		 WHERE project_id = $1
		   AND COALESCE(processing_status, 'completed') = $2
		   AND COALESCE(release, '') = $3
		 ORDER BY duration_ns DESC, COALESCE(started_at, created_at) DESC, created_at DESC
		 LIMIT $4`,
		projectID, string(store.ProfileProcessingStatusCompleted), release, limit,
	)
}

func (s *ProfileReadStore) FindRelatedProfile(ctx context.Context, projectID, traceID, transaction, release string) (*store.ProfileReference, error) {
	if items, err := s.FindProfilesByTrace(ctx, projectID, traceID, 1); err != nil {
		return nil, err
	} else if len(items) > 0 {
		return &items[0], nil
	}
	transaction = strings.TrimSpace(transaction)
	release = strings.TrimSpace(release)
	if transaction != "" && release != "" {
		items, err := s.listProfileReferences(ctx, `
			SELECT id, project_id, profile_id, COALESCE(event_id, ''), COALESCE(trace_id, ''),
			       COALESCE(transaction_name, ''), COALESCE(release, ''), COALESCE(environment, ''),
			       COALESCE(platform, ''), COALESCE(profile_kind, ''), started_at, ended_at, duration_ns,
			       thread_count, sample_count, frame_count, function_count, stack_count,
			       COALESCE(processing_status, 'completed'), COALESCE(ingest_error, ''),
			       COALESCE(raw_blob_key, ''), created_at, COALESCE(payload_json::text, '')
			  FROM telemetry.profile_manifests
			 WHERE project_id = $1
			   AND COALESCE(processing_status, 'completed') = $2
			   AND COALESCE(transaction_name, '') = $3
			   AND COALESCE(release, '') = $4
			 ORDER BY COALESCE(started_at, created_at) DESC, created_at DESC
			 LIMIT 1`,
			projectID, string(store.ProfileProcessingStatusCompleted), transaction, release,
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
		SELECT id, project_id, profile_id, COALESCE(event_id, ''), COALESCE(trace_id, ''),
		       COALESCE(transaction_name, ''), COALESCE(release, ''), COALESCE(environment, ''),
		       COALESCE(platform, ''), COALESCE(profile_kind, ''), started_at, ended_at, duration_ns,
		       thread_count, sample_count, frame_count, function_count, stack_count,
		       COALESCE(processing_status, 'completed'), COALESCE(ingest_error, ''),
		       COALESCE(raw_blob_key, ''), created_at, COALESCE(payload_json::text, '')
		  FROM telemetry.profile_manifests
		 WHERE project_id = $1
		   AND COALESCE(processing_status, 'completed') = $2
		   AND COALESCE(transaction_name, '') = $3
		 ORDER BY COALESCE(started_at, created_at) DESC, created_at DESC
		 LIMIT 1`,
		projectID, string(store.ProfileProcessingStatusCompleted), transaction,
	)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	return &items[0], nil
}

func (s *ProfileReadStore) QueryTopDown(ctx context.Context, projectID string, filter store.ProfileQueryFilter) (*store.ProfileTree, error) {
	return s.queryProfileTree(ctx, projectID, filter, "top_down")
}

func (s *ProfileReadStore) QueryBottomUp(ctx context.Context, projectID string, filter store.ProfileQueryFilter) (*store.ProfileTree, error) {
	return s.queryProfileTree(ctx, projectID, filter, "bottom_up")
}

func (s *ProfileReadStore) QueryFlamegraph(ctx context.Context, projectID string, filter store.ProfileQueryFilter) (*store.ProfileTree, error) {
	return s.queryProfileTree(ctx, projectID, filter, "flamegraph")
}

func (s *ProfileReadStore) QueryHotPath(ctx context.Context, projectID string, filter store.ProfileQueryFilter) (*store.ProfileHotPath, error) {
	tree, err := s.queryProfileTree(ctx, projectID, filter, "top_down")
	if err != nil {
		return nil, err
	}
	return profilequery.BuildHotPath(tree), nil
}

func (s *ProfileReadStore) CompareProfiles(ctx context.Context, projectID string, filter store.ProfileComparisonFilter) (*store.ProfileComparison, error) {
	if strings.TrimSpace(filter.BaselineProfileID) == "" || strings.TrimSpace(filter.CandidateProfileID) == "" {
		return nil, fmt.Errorf("baseline and candidate profiles are required")
	}
	baseline, err := s.GetProfile(ctx, projectID, filter.BaselineProfileID)
	if err != nil {
		return nil, err
	}
	candidate, err := s.GetProfile(ctx, projectID, filter.CandidateProfileID)
	if err != nil {
		return nil, err
	}
	if err := profilequery.EnforceGuard(&baseline.Manifest, 0); err != nil {
		return nil, err
	}
	if err := profilequery.EnforceGuard(&candidate.Manifest, 0); err != nil {
		return nil, err
	}
	baselineThreadID, threadKey, err := profilequery.ResolveThread(baseline, filter.ThreadID)
	if err != nil {
		return nil, err
	}
	candidateThreadID, _, err := profilequery.ResolveThread(candidate, filter.ThreadID)
	if err != nil {
		return nil, err
	}
	baselineWeights := profilequery.LoadFunctionWeights(baseline, baselineThreadID)
	candidateWeights := profilequery.LoadFunctionWeights(candidate, candidateThreadID)
	baselineDurationNS, baselineSampleCount := profilequery.LoadScopeTotals(baseline, baselineThreadID)
	candidateDurationNS, candidateSampleCount := profilequery.LoadScopeTotals(candidate, candidateThreadID)
	return profilequery.BuildComparison(
		&baseline.Manifest,
		&candidate.Manifest,
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

func (s *ProfileReadStore) queryProfileTree(ctx context.Context, projectID string, filter store.ProfileQueryFilter, mode string) (*store.ProfileTree, error) {
	record, err := s.resolveProfileRecord(ctx, projectID, filter)
	if err != nil {
		return nil, err
	}
	if err := profilequery.EnforceGuard(&record.Manifest, filter.MaxNodes); err != nil {
		return nil, err
	}
	threadRowID, threadKey, err := profilequery.ResolveThread(record, filter.ThreadID)
	if err != nil {
		return nil, err
	}
	return profilequery.BuildTree(record.Manifest.ProfileID, threadKey, mode, profilequery.LoadStackAggregates(record, threadRowID), filter), nil
}

func (s *ProfileReadStore) resolveProfileRecord(ctx context.Context, projectID string, filter store.ProfileQueryFilter) (*store.ProfileRecord, error) {
	query := `
		SELECT id, project_id, profile_id, COALESCE(event_id, ''), COALESCE(trace_id, ''),
		       COALESCE(transaction_name, ''), COALESCE(release, ''), COALESCE(environment, ''),
		       COALESCE(platform, ''), COALESCE(profile_kind, ''), started_at, ended_at, duration_ns,
		       thread_count, sample_count, frame_count, function_count, stack_count,
		       COALESCE(processing_status, 'completed'), COALESCE(ingest_error, ''),
		       COALESCE(raw_blob_key, ''), created_at, COALESCE(payload_json::text, '')
		  FROM telemetry.profile_manifests
		 WHERE project_id = $1`
	args := []any{projectID}
	next := 2
	if strings.TrimSpace(filter.ProfileID) != "" {
		query += fmt.Sprintf(` AND profile_id = $%d`, next)
		args = append(args, strings.TrimSpace(filter.ProfileID))
		next++
	} else {
		query += fmt.Sprintf(` AND COALESCE(processing_status, 'completed') = $%d`, next)
		args = append(args, string(store.ProfileProcessingStatusCompleted))
		next++
	}
	if strings.TrimSpace(filter.Transaction) != "" {
		query += fmt.Sprintf(` AND COALESCE(transaction_name, '') = $%d`, next)
		args = append(args, strings.TrimSpace(filter.Transaction))
		next++
	}
	if strings.TrimSpace(filter.Release) != "" {
		query += fmt.Sprintf(` AND COALESCE(release, '') = $%d`, next)
		args = append(args, strings.TrimSpace(filter.Release))
		next++
	}
	if strings.TrimSpace(filter.Environment) != "" {
		query += fmt.Sprintf(` AND COALESCE(environment, '') = $%d`, next)
		args = append(args, strings.TrimSpace(filter.Environment))
		next++
	}
	if !filter.StartedAfter.IsZero() {
		query += fmt.Sprintf(` AND COALESCE(started_at, created_at) >= $%d`, next)
		args = append(args, filter.StartedAfter.UTC())
		next++
	}
	if !filter.EndedBefore.IsZero() {
		query += fmt.Sprintf(` AND COALESCE(ended_at, started_at, created_at) <= $%d`, next)
		args = append(args, filter.EndedBefore.UTC())
	}
	query += ` ORDER BY COALESCE(started_at, created_at) DESC, created_at DESC LIMIT 1`
	return s.loadProfileRecord(ctx, query, args...)
}

func (s *ProfileReadStore) listProfileReferences(ctx context.Context, query string, args ...any) ([]store.ProfileReference, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list bridge profile references: %w", err)
	}
	defer rows.Close()

	var refs []store.ProfileReference
	for rows.Next() {
		manifest, payloadJSON, err := scanBridgeProfileManifest(rows)
		if err != nil {
			return nil, err
		}
		payload, err := s.resolveProfilePayload(ctx, manifest, payloadJSON)
		if err != nil {
			return nil, err
		}
		var topFunction string
		var topFunctionCnt int
		if record, err := hydrateBridgeProfileRecord(manifest, payload); err == nil {
			if items := profilequery.Breakdowns(record, false, 1); len(items) > 0 {
				topFunction = items[0].Name
				topFunctionCnt = items[0].Count
			}
			manifest.FunctionCount = maxInt(manifest.FunctionCount, record.Manifest.FunctionCount)
		}
		refs = append(refs, store.ProfileReference{
			ProjectID:      manifest.ProjectID,
			EventID:        manifest.EventID,
			ProfileID:      manifest.ProfileID,
			TraceID:        manifest.TraceID,
			Transaction:    manifest.Transaction,
			Release:        manifest.Release,
			Environment:    manifest.Environment,
			Platform:       manifest.Platform,
			DurationNS:     manifest.DurationNS,
			SampleCount:    manifest.SampleCount,
			FunctionCount:  manifest.FunctionCount,
			StartedAt:      firstNonZeroTime(manifest.StartedAt, manifest.DateCreated),
			TopFunction:    topFunction,
			TopFunctionCnt: topFunctionCnt,
		})
	}
	return refs, rows.Err()
}

func (s *ProfileReadStore) loadProfileRecord(ctx context.Context, query string, args ...any) (*store.ProfileRecord, error) {
	row := s.db.QueryRowContext(ctx, query, args...)
	manifest, payloadJSON, err := scanBridgeProfileManifest(row)
	if err != nil {
		if err == sql.ErrNoRows || err == store.ErrNotFound {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	payload, err := s.resolveProfilePayload(ctx, manifest, payloadJSON)
	if err != nil {
		return nil, err
	}
	return hydrateBridgeProfileRecord(manifest, payload)
}

func (s *ProfileReadStore) resolveProfilePayload(ctx context.Context, manifest store.ProfileManifest, payloadJSON string) ([]byte, error) {
	if manifest.RawBlobKey != "" && s.blobs != nil {
		body, err := s.blobs.Get(ctx, manifest.RawBlobKey)
		if err == nil && len(body) > 0 {
			return body, nil
		}
	}
	if strings.TrimSpace(payloadJSON) != "" {
		return []byte(payloadJSON), nil
	}
	if manifest.ProcessingStatus == store.ProfileProcessingStatusFailed {
		return nil, nil
	}
	return nil, store.ErrNotFound
}
