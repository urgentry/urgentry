package sqlite

import (
	"context"
	"sort"
	"strings"
	"time"

	sharedstore "urgentry/internal/store"
)

func (s *ReleaseStore) GetReleaseRegression(ctx context.Context, orgSlug, version string) (*sharedstore.ReleaseRegressionSummary, error) {
	current, err := s.GetReleaseBySlug(ctx, orgSlug, version)
	if err != nil || current == nil {
		return nil, err
	}
	previous, err := s.previousRelease(ctx, current.OrganizationID, current.Version)
	if err != nil {
		return nil, err
	}
	summary := &sharedstore.ReleaseRegressionSummary{
		Current:        releaseRegressionSnapshot(*current),
		EventDelta:     releaseCountDelta(current.EventCount, 0),
		SessionDelta:   releaseCountDelta(current.SessionCount, 0),
		CrashFreeDelta: releaseRateDelta(current.CrashFreeRate, 0),
	}
	if previous != nil {
		summary.Previous = ptrReleaseRegressionSnapshot(*previous)
		summary.EventDelta = releaseCountDelta(current.EventCount, previous.EventCount)
		summary.SessionDelta = releaseCountDelta(current.SessionCount, previous.SessionCount)
		summary.CrashFreeDelta = releaseRateDelta(current.CrashFreeRate, previous.CrashFreeRate)
		summary.EnvironmentMovements, err = s.listReleaseEnvironmentMovements(ctx, current.OrganizationID, current.Version, previous.Version, 6)
		if err != nil {
			return nil, err
		}
		summary.TransactionMovements, err = s.listReleaseTransactionMovements(ctx, current.OrganizationID, current.Version, previous.Version, 8)
		if err != nil {
			return nil, err
		}
	}
	summary.LatestDeployImpact, err = s.buildReleaseDeployImpact(ctx, orgSlug, current.OrganizationID, current.Version)
	if err != nil {
		return nil, err
	}
	return summary, nil
}

func (s *ReleaseStore) previousRelease(ctx context.Context, orgID, version string) (*Release, error) {
	items, err := s.ListReleases(ctx, orgID, 1000)
	if err != nil {
		return nil, err
	}
	for i, item := range items {
		if item.Version != version {
			continue
		}
		if i+1 >= len(items) {
			return nil, nil
		}
		previous := items[i+1]
		return &previous, nil
	}
	return nil, nil
}

func releaseRegressionSnapshot(item Release) sharedstore.ReleaseRegressionSnapshot {
	return sharedstore.ReleaseRegressionSnapshot{
		Version:          item.Version,
		CreatedAt:        item.CreatedAt,
		EventCount:       item.EventCount,
		SessionCount:     item.SessionCount,
		ErroredSessions:  item.ErroredSessions,
		CrashedSessions:  item.CrashedSessions,
		AbnormalSessions: item.AbnormalSessions,
		AffectedUsers:    item.AffectedUsers,
		CrashFreeRate:    item.CrashFreeRate,
		LastSessionAt:    item.LastSessionAt,
	}
}

func ptrReleaseRegressionSnapshot(item Release) *sharedstore.ReleaseRegressionSnapshot {
	snapshot := releaseRegressionSnapshot(item)
	return &snapshot
}

func releaseCountDelta(current, previous int) sharedstore.ReleaseCountDelta {
	return sharedstore.ReleaseCountDelta{Current: current, Previous: previous, Delta: current - previous}
}

func releaseRateDelta(current, previous float64) sharedstore.ReleaseRateDelta {
	return sharedstore.ReleaseRateDelta{Current: current, Previous: previous, Delta: current - previous}
}

func (s *ReleaseStore) listReleaseEnvironmentMovements(ctx context.Context, orgID, currentVersion, previousVersion string, limit int) ([]sharedstore.ReleaseEnvironmentRegression, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT COALESCE(e.environment, ''),
		        SUM(CASE WHEN COALESCE(e.release, '') = ? THEN 1 ELSE 0 END),
		        SUM(CASE WHEN COALESCE(e.release, '') = ? THEN 1 ELSE 0 END)
		   FROM events e
		   JOIN projects p ON p.id = e.project_id
		  WHERE p.organization_id = ?
		    AND LOWER(COALESCE(e.event_type, 'error')) = 'error'
		    AND COALESCE(e.release, '') IN (?, ?)
		  GROUP BY COALESCE(e.environment, '')`,
		currentVersion, previousVersion, orgID, currentVersion, previousVersion,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]sharedstore.ReleaseEnvironmentRegression, 0, limit)
	for rows.Next() {
		var item sharedstore.ReleaseEnvironmentRegression
		if err := rows.Scan(&item.Environment, &item.CurrentErrors, &item.PreviousErrors); err != nil {
			return nil, err
		}
		item.Environment = releaseDimensionLabel(item.Environment)
		item.DeltaErrors = item.CurrentErrors - item.PreviousErrors
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		if absInt(items[i].DeltaErrors) != absInt(items[j].DeltaErrors) {
			return absInt(items[i].DeltaErrors) > absInt(items[j].DeltaErrors)
		}
		if items[i].CurrentErrors != items[j].CurrentErrors {
			return items[i].CurrentErrors > items[j].CurrentErrors
		}
		return items[i].Environment < items[j].Environment
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *ReleaseStore) listReleaseTransactionMovements(ctx context.Context, orgID, currentVersion, previousVersion string, limit int) ([]sharedstore.ReleaseTransactionRegression, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT COALESCE(t.transaction_name, ''), COALESCE(t.release, ''), t.duration_ms
		   FROM transactions t
		   JOIN projects p ON p.id = t.project_id
		  WHERE p.organization_id = ?
		    AND COALESCE(t.release, '') IN (?, ?)`,
		orgID, currentVersion, previousVersion,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type transactionStats struct {
		count     int
		durations []float64
	}
	currentStats := map[string]*transactionStats{}
	previousStats := map[string]*transactionStats{}
	for rows.Next() {
		var transactionName string
		var releaseVersion string
		var durationMS float64
		if err := rows.Scan(&transactionName, &releaseVersion, &durationMS); err != nil {
			return nil, err
		}
		transactionName = releaseDimensionLabel(transactionName)
		target := currentStats
		if releaseVersion == previousVersion {
			target = previousStats
		}
		stats := target[transactionName]
		if stats == nil {
			stats = &transactionStats{}
			target[transactionName] = stats
		}
		stats.count++
		stats.durations = append(stats.durations, durationMS)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(currentStats)+len(previousStats))
	items := make([]sharedstore.ReleaseTransactionRegression, 0, len(currentStats)+len(previousStats))
	for transactionName, current := range currentStats {
		seen[transactionName] = struct{}{}
		previous := previousStats[transactionName]
		item := sharedstore.ReleaseTransactionRegression{
			Transaction:   transactionName,
			CurrentP95:    computeP95(current.durations),
			CurrentCount:  current.count,
			PreviousCount: 0,
			PreviousP95:   0,
		}
		if previous != nil {
			item.PreviousP95 = computeP95(previous.durations)
			item.PreviousCount = previous.count
		}
		item.DeltaP95 = item.CurrentP95 - item.PreviousP95
		item.DeltaCount = item.CurrentCount - item.PreviousCount
		items = append(items, item)
	}
	for transactionName, previous := range previousStats {
		if _, ok := seen[transactionName]; ok {
			continue
		}
		item := sharedstore.ReleaseTransactionRegression{
			Transaction:   transactionName,
			PreviousP95:   computeP95(previous.durations),
			PreviousCount: previous.count,
		}
		item.DeltaP95 = -item.PreviousP95
		item.DeltaCount = -item.PreviousCount
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if absFloat(items[i].DeltaP95) != absFloat(items[j].DeltaP95) {
			return absFloat(items[i].DeltaP95) > absFloat(items[j].DeltaP95)
		}
		if absInt(items[i].DeltaCount) != absInt(items[j].DeltaCount) {
			return absInt(items[i].DeltaCount) > absInt(items[j].DeltaCount)
		}
		return items[i].Transaction < items[j].Transaction
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *ReleaseStore) buildReleaseDeployImpact(ctx context.Context, orgSlug, orgID, version string) (*sharedstore.ReleaseDeployImpact, error) {
	deploys, err := s.ListDeploys(ctx, orgSlug, version, 1)
	if err != nil || len(deploys) == 0 {
		return nil, err
	}
	deploy := deploys[0]
	anchorAt := releaseDeployAnchor(deploy)
	if anchorAt.IsZero() {
		return nil, nil
	}
	windowHours := 24
	window := time.Duration(windowHours) * time.Hour
	beforeStart := anchorAt.Add(-window)
	afterEnd := anchorAt.Add(window)
	errorsBefore, err := s.queryReleaseErrorCount(ctx, orgID, version, deploy.Environment, beforeStart, anchorAt)
	if err != nil {
		return nil, err
	}
	errorsAfter, err := s.queryReleaseErrorCount(ctx, orgID, version, deploy.Environment, anchorAt, afterEnd)
	if err != nil {
		return nil, err
	}
	beforeCount, beforeP95, err := s.queryReleaseTransactionStats(ctx, orgID, version, deploy.Environment, beforeStart, anchorAt)
	if err != nil {
		return nil, err
	}
	afterCount, afterP95, err := s.queryReleaseTransactionStats(ctx, orgID, version, deploy.Environment, anchorAt, afterEnd)
	if err != nil {
		return nil, err
	}
	return &sharedstore.ReleaseDeployImpact{
		Deploy:             deploy,
		AnchorAt:           anchorAt,
		WindowHours:        windowHours,
		ErrorsBefore:       errorsBefore,
		ErrorsAfter:        errorsAfter,
		ErrorDelta:         errorsAfter - errorsBefore,
		TransactionsBefore: beforeCount,
		TransactionsAfter:  afterCount,
		TransactionDelta:   afterCount - beforeCount,
		P95Before:          beforeP95,
		P95After:           afterP95,
		P95Delta:           afterP95 - beforeP95,
	}, nil
}

func (s *ReleaseStore) queryReleaseErrorCount(ctx context.Context, orgID, version, environment string, start, end time.Time) (int, error) {
	query := `SELECT COUNT(*)
	            FROM events e
	            JOIN projects p ON p.id = e.project_id
	           WHERE p.organization_id = ?
	             AND LOWER(COALESCE(e.event_type, 'error')) = 'error'
	             AND COALESCE(e.release, '') = ?
	             AND julianday(COALESCE(NULLIF(e.occurred_at, ''), e.ingested_at)) >= julianday(?)
	             AND julianday(COALESCE(NULLIF(e.occurred_at, ''), e.ingested_at)) < julianday(?)`
	args := []any{orgID, version, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339)}
	if strings.TrimSpace(environment) != "" {
		query += ` AND COALESCE(e.environment, '') = ?`
		args = append(args, environment)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *ReleaseStore) queryReleaseTransactionStats(ctx context.Context, orgID, version, environment string, start, end time.Time) (int, float64, error) {
	query := `SELECT t.duration_ms
	            FROM transactions t
	            JOIN projects p ON p.id = t.project_id
	           WHERE p.organization_id = ?
	             AND COALESCE(t.release, '') = ?
	             AND julianday(COALESCE(NULLIF(t.end_timestamp, ''), t.created_at)) >= julianday(?)
	             AND julianday(COALESCE(NULLIF(t.end_timestamp, ''), t.created_at)) < julianday(?)`
	args := []any{orgID, version, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339)}
	if strings.TrimSpace(environment) != "" {
		query += ` AND COALESCE(t.environment, '') = ?`
		args = append(args, environment)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	durations := make([]float64, 0, 16)
	for rows.Next() {
		var durationMS float64
		if err := rows.Scan(&durationMS); err != nil {
			return 0, 0, err
		}
		durations = append(durations, durationMS)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	return len(durations), computeP95(durations), nil
}

func computeP95(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	index := int(float64(len(sorted)-1) * 0.95)
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func releaseDeployAnchor(deploy sharedstore.ReleaseDeploy) time.Time {
	if !deploy.DateFinished.IsZero() {
		return deploy.DateFinished
	}
	if !deploy.DateStarted.IsZero() {
		return deploy.DateStarted
	}
	return deploy.DateCreated
}

func releaseDimensionLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	return value
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
