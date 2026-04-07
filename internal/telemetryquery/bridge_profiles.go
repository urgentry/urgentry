package telemetryquery

import (
	"context"

	"urgentry/internal/store"
	"urgentry/internal/telemetrybridge"
)

func (s *bridgeService) ListProfiles(ctx context.Context, projectID string, limit int) ([]store.ProfileManifest, error) {
	if err := s.syncProfileScope(ctx, projectID); err != nil {
		return nil, err
	}
	return s.sourceProfiles.ListProfiles(ctx, projectID, limit)
}

func (s *bridgeService) GetProfile(ctx context.Context, projectID, profileID string) (*store.ProfileRecord, error) {
	if err := s.syncProfileScope(ctx, projectID); err != nil {
		return nil, err
	}
	if record, ok := s.cachedProfile(projectID, profileID); ok {
		return record, nil
	}
	record, err := s.sourceProfiles.GetProfile(ctx, projectID, profileID)
	if err != nil {
		return nil, err
	}
	s.storeProfileCache(projectID, profileID, record)
	return record, nil
}

func (s *bridgeService) FindProfilesByTrace(ctx context.Context, projectID, traceID string, limit int) ([]store.ProfileReference, error) {
	if err := s.syncProfileScope(ctx, projectID); err != nil {
		return nil, err
	}
	return s.sourceProfiles.FindProfilesByTrace(ctx, projectID, traceID, limit)
}

func (s *bridgeService) ListReleaseProfileHighlights(ctx context.Context, projectID, release string, limit int) ([]store.ProfileReference, error) {
	if err := s.syncProfileScope(ctx, projectID); err != nil {
		return nil, err
	}
	return s.sourceProfiles.ListReleaseProfileHighlights(ctx, projectID, release, limit)
}

func (s *bridgeService) FindRelatedProfile(ctx context.Context, projectID, traceID, transaction, release string) (*store.ProfileReference, error) {
	if err := s.syncProfileScope(ctx, projectID); err != nil {
		return nil, err
	}
	return s.sourceProfiles.FindRelatedProfile(ctx, projectID, traceID, transaction, release)
}

func (s *bridgeService) QueryTopDown(ctx context.Context, projectID string, filter store.ProfileQueryFilter) (*store.ProfileTree, error) {
	if err := s.syncProfileScope(ctx, projectID); err != nil {
		return nil, err
	}
	return s.sourceProfiles.QueryTopDown(ctx, projectID, filter)
}

func (s *bridgeService) QueryBottomUp(ctx context.Context, projectID string, filter store.ProfileQueryFilter) (*store.ProfileTree, error) {
	if err := s.syncProfileScope(ctx, projectID); err != nil {
		return nil, err
	}
	return s.sourceProfiles.QueryBottomUp(ctx, projectID, filter)
}

func (s *bridgeService) QueryFlamegraph(ctx context.Context, projectID string, filter store.ProfileQueryFilter) (*store.ProfileTree, error) {
	if err := s.syncProfileScope(ctx, projectID); err != nil {
		return nil, err
	}
	return s.sourceProfiles.QueryFlamegraph(ctx, projectID, filter)
}

func (s *bridgeService) QueryHotPath(ctx context.Context, projectID string, filter store.ProfileQueryFilter) (*store.ProfileHotPath, error) {
	if err := s.syncProfileScope(ctx, projectID); err != nil {
		return nil, err
	}
	return s.sourceProfiles.QueryHotPath(ctx, projectID, filter)
}

func (s *bridgeService) CompareProfiles(ctx context.Context, projectID string, filter store.ProfileComparisonFilter) (*store.ProfileComparison, error) {
	if err := s.syncProfileScope(ctx, projectID); err != nil {
		return nil, err
	}
	return s.sourceProfiles.CompareProfiles(ctx, projectID, filter)
}

func (s *bridgeService) syncProfileScope(ctx context.Context, projectID string) error {
	scope, err := s.projectScope(ctx, projectID)
	if err != nil {
		return err
	}
	return s.ensureSurfaceFresh(ctx, QuerySurfaceProfiles, scope, telemetrybridge.FamilyProfiles)
}
