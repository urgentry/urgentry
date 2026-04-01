package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/grouping"
	"urgentry/internal/issue"
	"urgentry/internal/nativesym"
	"urgentry/internal/normalize"
	"urgentry/internal/sqlutil"
)

type NativeReprocessFilter struct {
	OrganizationID string
	ProjectID      string
	ReleaseVersion string
	StartedAfter   time.Time
	EndedBefore    time.Time
}

type NativeReprocessBatch struct {
	Filter      NativeReprocessFilter
	AfterRowID  int64
	Limit       int
	RunID       string
	UserID      string
	DebugFileID string
}

type NativeReprocessBatchResult struct {
	NextRowID int64
	Processed int
	Updated   int
	Failed    int
	Done      bool
	LastError string
}

type nativeReprocessCandidate struct {
	rowID      int64
	eventRow   string
	project    string
	groupID    string
	payload    string
	occurredAt string
}

func (s *DebugFileStore) CountNativeReprocessCandidates(ctx context.Context, filter NativeReprocessFilter) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("debug file store is not configured")
	}
	query, args := nativeReprocessBaseQuery(filter, true)
	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count native reprocess candidates: %w", err)
	}
	return count, nil
}

func (s *DebugFileStore) ReprocessNativeEventBatch(ctx context.Context, batch NativeReprocessBatch) (NativeReprocessBatchResult, error) {
	if s == nil || s.db == nil {
		return NativeReprocessBatchResult{}, errors.New("debug file store is not configured")
	}
	limit := batch.Limit
	if limit <= 0 {
		limit = 25
	}
	query, args := nativeReprocessBaseQuery(batch.Filter, false)
	query += ` AND e.rowid > ? ORDER BY e.rowid ASC LIMIT ?`
	args = append(args, batch.AfterRowID, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return NativeReprocessBatchResult{}, fmt.Errorf("query native reprocess batch: %w", err)
	}
	candidates := make([]nativeReprocessCandidate, 0, limit)
	for rows.Next() {
		var item nativeReprocessCandidate
		if err := rows.Scan(&item.rowID, &item.eventRow, &item.project, &item.groupID, &item.payload, &item.occurredAt); err != nil {
			rows.Close()
			return NativeReprocessBatchResult{}, fmt.Errorf("scan native reprocess batch: %w", err)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Close(); err != nil {
		return NativeReprocessBatchResult{}, fmt.Errorf("close native reprocess batch: %w", err)
	}
	if err := rows.Err(); err != nil {
		return NativeReprocessBatchResult{}, fmt.Errorf("iterate native reprocess batch: %w", err)
	}

	resolver := nativesym.NewResolver(nativeDebugFileLookup{store: s})
	result := NativeReprocessBatchResult{}
	touchedGroups := make(map[string]struct{})
	for _, item := range candidates {
		result.NextRowID = item.rowID
		result.Processed++
		if strings.TrimSpace(item.payload) == "" {
			result.Failed++
			if result.LastError == "" {
				result.LastError = "skipped empty native event payload"
			}
			continue
		}

		var evt normalize.Event
		if err := json.Unmarshal([]byte(item.payload), &evt); err != nil {
			result.Failed++
			if result.LastError == "" {
				result.LastError = "skipped invalid native event payload"
			}
			continue
		}
		if evt.Timestamp.IsZero() {
			evt.Timestamp = sqlutil.ParseDBTime(item.occurredAt)
		}
		beforeGrouping := grouping.ComputeGrouping(&evt)
		if !issue.ApplyEventResolvers(ctx, item.project, &evt, nil, nil, resolver) {
			continue
		}
		afterGrouping := grouping.ComputeGrouping(&evt)

		normalized, err := json.Marshal(&evt)
		if err != nil {
			return result, fmt.Errorf("marshal reprocessed native event: %w", err)
		}
		tagsJSON, _ := json.Marshal(evt.Tags)
		if beforeGrouping.Version != afterGrouping.Version || beforeGrouping.GroupingKey != afterGrouping.GroupingKey {
			targetGroupID, err := moveReprocessedEventToGroup(ctx, s.db, item, &evt, afterGrouping, normalized, tagsJSON)
			if err != nil {
				return result, err
			}
			if strings.TrimSpace(targetGroupID) != "" {
				touchedGroups[targetGroupID] = struct{}{}
			}
			result.Updated++
			continue
		}
		if _, err := s.db.ExecContext(ctx,
			`UPDATE events
			    SET title = ?, culprit = ?, message = ?, level = ?, platform = ?, release = ?, environment = ?,
			        tags_json = ?, payload_json = ?
			  WHERE id = ?`,
			evt.Title(), evt.Culprit(), evt.Message, evt.Level, evt.Platform, evt.Release, evt.Environment,
			string(tagsJSON), string(normalized), item.eventRow,
		); err != nil {
			return result, fmt.Errorf("update native event: %w", err)
		}
		if strings.TrimSpace(item.groupID) != "" {
			touchedGroups[item.groupID] = struct{}{}
		}
		result.Updated++
	}
	for groupID := range touchedGroups {
		if err := syncGroupFromLatestEvent(ctx, s.db, groupID); err != nil {
			return result, err
		}
		if err := NewGroupStore(s.db).RecordIssueActivity(ctx, groupID, batch.UserID, "native_reprocess", nativeReprocessActivitySummary(batch), batch.RunID); err != nil {
			return result, err
		}
	}
	result.Done = result.Processed < limit
	return result, nil
}

func nativeReprocessActivitySummary(batch NativeReprocessBatch) string {
	if strings.TrimSpace(batch.DebugFileID) != "" {
		return "Native symbols reprocessed from debug file upload"
	}
	return "Native symbols reprocessed for release"
}

func nativeReprocessBaseQuery(filter NativeReprocessFilter, countOnly bool) (string, []any) {
	selectClause := `SELECT e.rowid, e.id, e.project_id, COALESCE(e.group_id, ''), COALESCE(e.payload_json, ''), COALESCE(e.occurred_at, e.ingested_at, '')`
	if countOnly {
		selectClause = `SELECT COUNT(*)`
	}
	query := selectClause + `
	   FROM events e
	   JOIN projects p ON p.id = e.project_id
	  WHERE p.organization_id = ?
	    AND COALESCE(e.event_type, 'error') = 'error'
	    AND (
	          instr(COALESCE(e.payload_json, ''), '"debug_id"') > 0
	       OR instr(COALESCE(e.payload_json, ''), '"code_id"') > 0
	       OR instr(COALESCE(e.payload_json, ''), '"instruction_addr"') > 0
	    )`
	args := []any{filter.OrganizationID}
	if filter.ProjectID != "" {
		query += ` AND e.project_id = ?`
		args = append(args, filter.ProjectID)
	}
	if filter.ReleaseVersion != "" {
		query += ` AND COALESCE(e.release, '') = ?`
		args = append(args, filter.ReleaseVersion)
	}
	if !filter.StartedAfter.IsZero() {
		query += ` AND COALESCE(e.occurred_at, e.ingested_at, '') >= ?`
		args = append(args, filter.StartedAfter.UTC().Format(time.RFC3339))
	}
	if !filter.EndedBefore.IsZero() {
		query += ` AND COALESCE(e.occurred_at, e.ingested_at, '') <= ?`
		args = append(args, filter.EndedBefore.UTC().Format(time.RFC3339))
	}
	return query, args
}

type groupQueryRunner interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func syncGroupFromLatestEvent(ctx context.Context, db *sql.DB, groupID string) error {
	return syncGroupFromLatestEventRunner(ctx, db, groupID)
}

func syncGroupFromLatestEventRunner(ctx context.Context, runner groupQueryRunner, groupID string) error {
	var (
		eventID, title, culprit, level, seenAt sql.NullString
	)
	err := runner.QueryRowContext(ctx,
		`SELECT id, COALESCE(title, ''), COALESCE(culprit, ''), COALESCE(level, ''), COALESCE(occurred_at, ingested_at, '')
		   FROM events
		  WHERE group_id = ?
		  ORDER BY COALESCE(occurred_at, ingested_at, '') DESC, id DESC
		  LIMIT 1`,
		groupID,
	).Scan(&eventID, &title, &culprit, &level, &seenAt)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load latest group event: %w", err)
	}
	if _, err := runner.ExecContext(ctx,
		`UPDATE groups
		    SET title = ?, culprit = ?, level = ?, last_event_id = ?, last_seen = ?
		  WHERE id = ?`,
		nullStr(title),
		nullStr(culprit),
		nullStr(level),
		nullStr(eventID),
		nullStr(seenAt),
		groupID,
	); err != nil {
		return fmt.Errorf("sync group summary: %w", err)
	}
	return nil
}

func moveReprocessedEventToGroup(ctx context.Context, db *sql.DB, item nativeReprocessCandidate, evt *normalize.Event, groupingResult grouping.Result, normalizedJSON, tagsJSON []byte) (string, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin regroup tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	eventTime := evt.Timestamp
	if eventTime.IsZero() {
		eventTime = sqlutil.ParseDBTime(item.occurredAt)
	}
	if eventTime.IsZero() {
		eventTime = time.Now().UTC()
	}

	targetGroupID, err := ensureTargetGroup(ctx, tx, item.project, evt, groupingResult, eventTime)
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE events
		    SET group_id = ?, title = ?, culprit = ?, message = ?, level = ?, platform = ?, release = ?, environment = ?,
		        tags_json = ?, payload_json = ?
		  WHERE id = ?`,
		targetGroupID,
		evt.Title(),
		evt.Culprit(),
		evt.Message,
		evt.Level,
		evt.Platform,
		evt.Release,
		evt.Environment,
		string(tagsJSON),
		string(normalizedJSON),
		item.eventRow,
	); err != nil {
		return "", fmt.Errorf("update regrouped native event: %w", err)
	}
	if strings.TrimSpace(item.groupID) != "" && item.groupID != targetGroupID {
		var remaining int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE group_id = ?`, item.groupID).Scan(&remaining); err != nil {
			return "", fmt.Errorf("count old group events: %w", err)
		}
		if remaining == 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM groups WHERE id = ?`, item.groupID); err != nil {
				return "", fmt.Errorf("delete empty old group: %w", err)
			}
		} else if err := syncGroupFromLatestEventRunner(ctx, tx, item.groupID); err != nil {
			return "", err
		}
	}
	if err := syncGroupFromLatestEventRunner(ctx, tx, targetGroupID); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit regroup tx: %w", err)
	}
	return targetGroupID, nil
}

func ensureTargetGroup(ctx context.Context, tx *sql.Tx, projectID string, evt *normalize.Event, groupingResult grouping.Result, eventTime time.Time) (string, error) {
	var groupID string
	err := tx.QueryRowContext(ctx,
		`SELECT id
		   FROM groups
		  WHERE project_id = ? AND grouping_version = ? AND grouping_key = ?`,
		projectID, groupingResult.Version, groupingResult.GroupingKey,
	).Scan(&groupID)
	if err == sql.ErrNoRows {
		groupID = generateID()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen, last_event_id)
			 VALUES (?, ?, ?, ?, ?, ?, ?, 'unresolved', ?, ?, 1, ?)`,
			groupID,
			projectID,
			groupingResult.Version,
			groupingResult.GroupingKey,
			evt.Title(),
			evt.Culprit(),
			evt.Level,
			eventTime.UTC().Format(time.RFC3339),
			eventTime.UTC().Format(time.RFC3339),
			evt.EventID,
		); err != nil {
			return "", fmt.Errorf("insert regrouped group: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE groups SET short_id = (SELECT COALESCE(MAX(short_id), 0) + 1 FROM groups) WHERE id = ? AND short_id IS NULL`,
			groupID,
		); err != nil {
			return "", fmt.Errorf("assign regrouped short id: %w", err)
		}
		return groupID, nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup regrouped group: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE groups
		    SET times_seen = times_seen + 1
		  WHERE id = ?`,
		groupID,
	); err != nil {
		return "", fmt.Errorf("increment regrouped group times_seen: %w", err)
	}
	return groupID, nil
}
