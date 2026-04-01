package sqlite

import (
	"context"

	"urgentry/internal/store"
)

func (s *RetentionStore) applyPolicy(ctx context.Context, projectID string, policy store.TelemetryRetentionPolicy) (int64, int64, error) {
	if policy.StorageTier == store.TelemetryStorageTierHot || policy.RetentionDays <= 0 {
		return 0, 0, nil
	}
	switch policy.Surface {
	case store.TelemetrySurfaceErrors:
		return s.applyEventPolicy(ctx, projectID, policy, false, "error", "security")
	case store.TelemetrySurfaceLogs:
		return s.applyEventPolicy(ctx, projectID, policy, false, "log")
	case store.TelemetrySurfaceReplays:
		return s.applyEventPolicy(ctx, projectID, policy, true, "replay")
	case store.TelemetrySurfaceProfiles:
		return s.applyEventPolicy(ctx, projectID, policy, false, "profile")
	case store.TelemetrySurfaceTraces:
		return s.applyTracePolicy(ctx, projectID, policy)
	case store.TelemetrySurfaceOutcomes:
		return s.applyOutcomePolicy(ctx, projectID, policy)
	case store.TelemetrySurfaceAttachments:
		return s.applyAttachmentPolicy(ctx, projectID, policy)
	case store.TelemetrySurfaceDebugFiles:
		return s.applyDebugFilePolicy(ctx, projectID, policy)
	default:
		return 0, 0, nil
	}
}

func (s *RetentionStore) applyEventPolicy(ctx context.Context, projectID string, policy store.TelemetryRetentionPolicy, includeAttachments bool, eventTypes ...string) (int64, int64, error) {
	if policy.StorageTier == store.TelemetryStorageTierArchive {
		archived, err := s.archiveEventsByTypeOlderThan(ctx, projectID, policy.Surface, includeAttachments, policy.RetentionDays, eventTypes...)
		if err != nil {
			return 0, 0, err
		}
		deleted, err := s.deleteArchivedSurfacePastBoundary(ctx, projectID, policy.Surface, policy.ArchiveRetentionDays)
		if err != nil {
			return archived, 0, err
		}
		return archived, deleted, nil
	}
	deleted, err := s.deleteEventsByTypeOlderThan(ctx, projectID, policy.RetentionDays, includeAttachments, eventTypes...)
	return 0, deleted, err
}

func (s *RetentionStore) applyTracePolicy(ctx context.Context, projectID string, policy store.TelemetryRetentionPolicy) (int64, int64, error) {
	if policy.StorageTier == store.TelemetryStorageTierArchive {
		archived, err := s.archiveTransactionsOlderThan(ctx, projectID, policy.RetentionDays)
		if err != nil {
			return 0, 0, err
		}
		deleted, err := s.deleteArchivedSurfacePastBoundary(ctx, projectID, policy.Surface, policy.ArchiveRetentionDays)
		if err != nil {
			return archived, 0, err
		}
		return archived, deleted, nil
	}
	deleted, err := s.deleteTransactionsOlderThan(ctx, projectID, policy.RetentionDays)
	return 0, deleted, err
}

func (s *RetentionStore) applyOutcomePolicy(ctx context.Context, projectID string, policy store.TelemetryRetentionPolicy) (int64, int64, error) {
	if policy.StorageTier == store.TelemetryStorageTierArchive {
		archived, err := s.archiveOutcomesOlderThan(ctx, projectID, policy.RetentionDays)
		if err != nil {
			return 0, 0, err
		}
		deleted, err := s.deleteArchivedSurfacePastBoundary(ctx, projectID, policy.Surface, policy.ArchiveRetentionDays)
		if err != nil {
			return archived, 0, err
		}
		return archived, deleted, nil
	}
	deleted, err := s.deleteOutcomesOlderThan(ctx, projectID, policy.RetentionDays)
	return 0, deleted, err
}

func (s *RetentionStore) applyAttachmentPolicy(ctx context.Context, projectID string, policy store.TelemetryRetentionPolicy) (int64, int64, error) {
	if policy.StorageTier == store.TelemetryStorageTierArchive {
		archived, err := s.archiveOldAttachments(ctx, projectID, store.TelemetrySurfaceAttachments, policy.RetentionDays)
		if err != nil {
			return 0, 0, err
		}
		deleted, err := s.deleteArchivedSurfacePastBoundary(ctx, projectID, policy.Surface, policy.ArchiveRetentionDays)
		if err != nil {
			return archived, 0, err
		}
		return archived, deleted, nil
	}
	deleted, err := s.deleteAttachmentsOlderThan(ctx, projectID, policy.RetentionDays)
	return 0, deleted, err
}

func (s *RetentionStore) applyDebugFilePolicy(ctx context.Context, projectID string, policy store.TelemetryRetentionPolicy) (int64, int64, error) {
	if policy.StorageTier == store.TelemetryStorageTierArchive {
		archived, err := s.archiveOldDebugFiles(ctx, projectID, policy.RetentionDays)
		if err != nil {
			return 0, 0, err
		}
		deleted, err := s.deleteArchivedSurfacePastBoundary(ctx, projectID, policy.Surface, policy.ArchiveRetentionDays)
		if err != nil {
			return archived, 0, err
		}
		return archived, deleted, nil
	}
	deleted, err := s.deleteDebugFilesOlderThan(ctx, projectID, policy.RetentionDays)
	return 0, deleted, err
}
