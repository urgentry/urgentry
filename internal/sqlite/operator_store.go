package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"urgentry/internal/store"
)

type OperatorCheck struct {
	Name  string
	Check func(context.Context) (store.OperatorServiceStatus, error)
}

// BridgeFreshnessFunc returns the current bridge projection freshness state.
type BridgeFreshnessFunc func(context.Context) ([]store.OperatorBridgeFreshness, error)

type OperatorStore struct {
	db              *sql.DB
	runtime         store.OperatorRuntime
	lifecycle       store.LifecycleStore
	audits          store.OperatorAuditStore
	queueDepth      func(context.Context) (int, error)
	bridgeFreshness BridgeFreshnessFunc
	checks          []OperatorCheck
}

func NewOperatorStore(db *sql.DB, runtime store.OperatorRuntime, lifecycle store.LifecycleStore, audits store.OperatorAuditStore, queueDepth func(context.Context) (int, error), checks ...OperatorCheck) *OperatorStore {
	if audits == nil && db != nil {
		audits = NewOperatorAuditStore(db)
	}
	return &OperatorStore{
		db:         db,
		runtime:    runtime,
		lifecycle:  lifecycle,
		audits:     audits,
		queueDepth: queueDepth,
		checks:     checks,
	}
}

// SetBridgeFreshness registers a callback that provides bridge projection
// freshness for the operator overview. Must be called before the store is used.
func (s *OperatorStore) SetBridgeFreshness(fn BridgeFreshnessFunc) {
	if s != nil {
		s.bridgeFreshness = fn
	}
}

func (s *OperatorStore) Overview(ctx context.Context, orgSlug string, limit int) (*store.OperatorOverview, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("operator store is not configured")
	}
	if limit <= 0 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}
	orgID, err := s.organizationID(ctx, orgSlug)
	if err != nil {
		return nil, err
	}
	if orgID == "" {
		return nil, nil
	}
	var installState *store.InstallState
	if s.lifecycle != nil {
		installState, err = s.lifecycle.GetInstallState(ctx)
		if err != nil {
			return nil, fmt.Errorf("load install state: %w", err)
		}
	}
	queueDepth := 0
	if s.queueDepth != nil {
		queueDepth, err = s.queueDepth(ctx)
	} else {
		queueDepth, err = NewJobStore(s.db).Len(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("load queue depth: %w", err)
	}
	backfills, err := s.listBackfills(ctx, orgID, limit)
	if err != nil {
		return nil, err
	}
	retentionOutcomes, err := s.listRetentionOutcomes(ctx, orgSlug, limit)
	if err != nil {
		return nil, err
	}
	installAudits := []store.OperatorAuditEntry(nil)
	if s.audits != nil {
		installAudits, err = s.audits.List(ctx, orgSlug, limit)
		if err != nil {
			return nil, fmt.Errorf("list operator audit logs: %w", err)
		}
	}
	auditLogs, err := NewAuditStore(s.db).ListOrganizationAuditLogs(ctx, orgSlug, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit logs: %w", err)
	}
	services, err := s.runChecks(ctx)
	if err != nil {
		return nil, err
	}
	var bridgeFreshness []store.OperatorBridgeFreshness
	if s.bridgeFreshness != nil {
		bridgeFreshness, _ = s.bridgeFreshness(ctx)
	}
	slos, alerts := store.BuildOperatorHealth(services, store.OperatorQueueStatus{Depth: queueDepth}, backfills, retentionOutcomes, bridgeFreshness)
	return &store.OperatorOverview{
		OrganizationSlug:  orgSlug,
		Install:           installState,
		Runtime:           s.runtime,
		Services:          services,
		Queue:             store.OperatorQueueStatus{Depth: queueDepth},
		SLOs:              slos,
		Alerts:            alerts,
		Backfills:         backfills,
		BridgeFreshness:   bridgeFreshness,
		RetentionOutcomes: retentionOutcomes,
		InstallAudits:     installAudits,
		AuditLogs:         auditLogs,
	}, nil
}

func (s *OperatorStore) runChecks(ctx context.Context) ([]store.OperatorServiceStatus, error) {
	if len(s.checks) == 0 {
		if err := s.db.PingContext(ctx); err != nil {
			return []store.OperatorServiceStatus{{Name: "sqlite", Status: "error", Detail: err.Error()}}, nil
		}
		return []store.OperatorServiceStatus{{Name: "sqlite", Status: "ok", Detail: "reachable"}}, nil
	}
	statuses := make([]store.OperatorServiceStatus, 0, len(s.checks))
	for _, item := range s.checks {
		status, err := item.Check(ctx)
		if err != nil {
			status = store.OperatorServiceStatus{
				Name:   item.Name,
				Status: "error",
				Detail: err.Error(),
			}
		}
		if status.Name == "" {
			status.Name = item.Name
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (s *OperatorStore) organizationID(ctx context.Context, orgSlug string) (string, error) {
	var orgID string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM organizations WHERE slug = ?`, orgSlug).Scan(&orgID); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("resolve organization: %w", err)
	}
	return orgID, nil
}

func (s *OperatorStore) listBackfills(ctx context.Context, orgID string, limit int) ([]store.OperatorBackfillStatus, error) {
	runs, err := NewBackfillStore(s.db).ListRuns(ctx, orgID, limit)
	if err != nil {
		return nil, fmt.Errorf("list backfills: %w", err)
	}
	out := make([]store.OperatorBackfillStatus, 0, len(runs))
	for _, run := range runs {
		out = append(out, store.OperatorBackfillStatus{
			ID:             run.ID,
			Kind:           string(run.Kind),
			Status:         string(run.Status),
			ProjectID:      run.ProjectID,
			ReleaseVersion: run.ReleaseVersion,
			ProcessedItems: int64(run.ProcessedItems),
			TotalItems:     int64(run.TotalItems),
			FailedItems:    int64(run.FailedItems),
			LastError:      run.LastError,
			DateCreated:    run.CreatedAt,
			DateUpdated:    run.UpdatedAt,
		})
	}
	return out, nil
}

func (s *OperatorStore) listRetentionOutcomes(ctx context.Context, orgSlug string, limit int) ([]store.OperatorRetentionOutcome, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.id,
		        a.project_id,
		        p.slug,
		        a.surface,
		        a.record_type,
		        a.record_id,
		        COALESCE(a.archive_key, ''),
		        COALESCE(a.archived_at, ''),
		        COALESCE(a.restored_at, '')
		   FROM telemetry_archives a
		   JOIN projects p ON p.id = a.project_id
		   JOIN organizations o ON o.id = p.organization_id
		  WHERE o.slug = ?
		  ORDER BY a.archived_at DESC, a.id DESC
		  LIMIT ?`,
		orgSlug, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list retention outcomes: %w", err)
	}
	defer rows.Close()

	out := make([]store.OperatorRetentionOutcome, 0, limit)
	for rows.Next() {
		var item store.OperatorRetentionOutcome
		var archivedAt string
		var restoredAt string
		var archiveKey string
		if err := rows.Scan(
			&item.ID,
			&item.ProjectID,
			&item.ProjectSlug,
			&item.Surface,
			&item.RecordType,
			&item.RecordID,
			&archiveKey,
			&archivedAt,
			&restoredAt,
		); err != nil {
			return nil, fmt.Errorf("scan retention outcome: %w", err)
		}
		item.ArchiveKey = archiveKey
		item.BlobPresent = archiveKey != ""
		item.ArchivedAt = parseTime(archivedAt)
		item.RestoredAt = parseTime(restoredAt)
		out = append(out, item)
	}
	return out, rows.Err()
}
