package postgrescontrol

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"urgentry/internal/issue"
	sharedstore "urgentry/internal/store"
	"urgentry/pkg/id"
)

const groupShortIDLockKey int64 = 0x6772747953484f52

type rowScanner interface {
	Scan(dest ...any) error
}

type issueActivityMetadata struct {
	Details   string `json:"details,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
}

// GroupStore is a PostgreSQL-backed implementation of issue.GroupStore.
type GroupStore struct {
	db *sql.DB
}

// NewGroupStore creates a GroupStore backed by PostgreSQL control-plane tables.
func NewGroupStore(db *sql.DB) *GroupStore {
	return &GroupStore{db: db}
}

// UpsertGroup atomically creates or updates a grouped issue row.
func (s *GroupStore) UpsertGroup(ctx context.Context, g *issue.Group) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, groupShortIDLockKey); err != nil {
		return err
	}

	var existingID string
	err = tx.QueryRowContext(ctx,
		`SELECT id
		   FROM groups
		  WHERE project_id = $1 AND grouping_version = $2 AND grouping_key = $3`,
		g.ProjectID, g.GroupingVersion, g.GroupingKey,
	).Scan(&existingID)
	switch {
	case err == sql.ErrNoRows:
		if g.ID == "" {
			g.ID = id.New()
		}
		if g.TimesSeen == 0 {
			g.TimesSeen = 1
		}
		if strings.TrimSpace(g.Status) == "" {
			g.Status = "unresolved"
		}
		shortID, err := nextGroupShortID(ctx, tx)
		if err != nil {
			return err
		}
		assigneeUserID, assigneeTeamID := encodeAssignee(g.Assignee)
		_, err = tx.ExecContext(ctx,
			`INSERT INTO groups
				(id, project_id, grouping_version, grouping_key, title, culprit, level, status, substatus, resolved_in_release,
				 merged_into_group_id, first_seen, last_seen, times_seen, last_event_id, assignee_user_id, assignee_team_id,
				 short_id, created_at, updated_at)
			 VALUES
				($1, $2, $3, $4, $5, $6, $7, $8, '', '', '', $9, $10, $11, $12, $13, $14, $15, now(), now())`,
			g.ID, g.ProjectID, g.GroupingVersion, g.GroupingKey, g.Title, g.Culprit, g.Level, g.Status,
			nullableTime(g.FirstSeen), nullableTime(g.LastSeen), g.TimesSeen, strings.TrimSpace(g.LastEventID),
			assigneeUserID, assigneeTeamID, shortID,
		)
		if err != nil {
			return err
		}
		if err := upsertGroupStateTx(ctx, tx, g.ID, g.Status, ""); err != nil {
			return err
		}
		if err := recordGroupOccurrenceTx(ctx, tx, g.ID, g.LastEventID, g.LastSeen); err != nil {
			return err
		}
		return tx.Commit()
	case err != nil:
		return err
	}

	g.ID = existingID
	_, err = tx.ExecContext(ctx,
		`UPDATE groups
		    SET last_seen = $1,
		        times_seen = times_seen + 1,
		        last_event_id = $2,
		        title = $3,
		        culprit = $4,
		        level = $5,
		        updated_at = now()
		  WHERE id = $6`,
		nullableTime(g.LastSeen), strings.TrimSpace(g.LastEventID), g.Title, g.Culprit, g.Level, existingID,
	)
	if err != nil {
		return err
	}
	if err := recordGroupOccurrenceTx(ctx, tx, existingID, g.LastEventID, g.LastSeen); err != nil {
		return err
	}
	return tx.Commit()
}

// GetGroup retrieves a group by ID.
func (s *GroupStore) GetGroup(ctx context.Context, id string) (*issue.Group, error) {
	row := s.db.QueryRowContext(ctx, groupSelectSQL+` WHERE g.id = $1`, id)
	return scanGroup(row)
}

// GetGroupByKey retrieves a group by its unique grouping identity.
func (s *GroupStore) GetGroupByKey(ctx context.Context, projectID, version, key string) (*issue.Group, error) {
	row := s.db.QueryRowContext(ctx, groupSelectSQL+` WHERE g.project_id = $1 AND g.grouping_version = $2 AND g.grouping_key = $3`, projectID, version, key)
	return scanGroup(row)
}

// ListGroups returns groups for a project with optional filtering and pagination.
func (s *GroupStore) ListGroups(ctx context.Context, projectID string, opts issue.ListOpts) ([]*issue.Group, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 25
	}

	query := groupSelectSQL + ` WHERE g.project_id = $1`
	args := []any{projectID}
	if strings.TrimSpace(opts.Status) != "" {
		query += ` AND g.status = $2`
		args = append(args, strings.TrimSpace(opts.Status))
	}

	switch opts.Sort {
	case "last_seen_asc":
		query += ` ORDER BY g.last_seen ASC NULLS FIRST`
	case "first_seen_desc":
		query += ` ORDER BY g.first_seen DESC NULLS LAST`
	case "first_seen_asc":
		query += ` ORDER BY g.first_seen ASC NULLS FIRST`
	case "times_seen_desc":
		query += ` ORDER BY g.times_seen DESC, g.last_seen DESC NULLS LAST`
	default:
		query += ` ORDER BY g.last_seen DESC NULLS LAST`
	}
	query += ` LIMIT $` + itoa(len(args)+1)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := make([]*issue.Group, 0, limit)
	for rows.Next() {
		item, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, item)
	}
	return groups, rows.Err()
}

// UpdateStatus updates the top-level issue status and clears merge/release metadata.
func (s *GroupStore) UpdateStatus(ctx context.Context, id, status string) error {
	empty := ""
	return s.PatchIssue(ctx, id, sharedstore.IssuePatch{
		Status:              &status,
		ResolutionSubstatus: &empty,
		ResolvedInRelease:   &empty,
		MergedIntoGroupID:   &empty,
	})
}

// UpdateAssignee updates the issue assignee.
func (s *GroupStore) UpdateAssignee(ctx context.Context, id, assignee string) error {
	return s.PatchIssue(ctx, id, sharedstore.IssuePatch{Assignee: &assignee})
}

// PatchIssue updates mutable issue metadata.
func (s *GroupStore) PatchIssue(ctx context.Context, id string, patch sharedstore.IssuePatch) error {
	updates := make([]string, 0, 8)
	args := make([]any, 0, 10)
	status := ""
	if patch.Status != nil {
		status = strings.TrimSpace(*patch.Status)
		updates = append(updates, `status = $`+itoa(len(args)+1))
		args = append(args, status)
	}
	if patch.Assignee != nil {
		userID, teamID := encodeAssignee(*patch.Assignee)
		updates = append(updates,
			`assignee_user_id = $`+itoa(len(args)+1),
			`assignee_team_id = $`+itoa(len(args)+2),
		)
		args = append(args, userID, teamID)
	}
	if patch.Priority != nil {
		updates = append(updates, `priority = $`+itoa(len(args)+1))
		args = append(args, *patch.Priority)
	}
	if patch.ResolutionSubstatus != nil {
		updates = append(updates, `substatus = $`+itoa(len(args)+1))
		args = append(args, strings.TrimSpace(*patch.ResolutionSubstatus))
	}
	if patch.ResolvedInRelease != nil {
		updates = append(updates, `resolved_in_release = $`+itoa(len(args)+1))
		args = append(args, strings.TrimSpace(*patch.ResolvedInRelease))
	}
	if patch.MergedIntoGroupID != nil {
		updates = append(updates, `merged_into_group_id = $`+itoa(len(args)+1))
		args = append(args, strings.TrimSpace(*patch.MergedIntoGroupID))
	}
	if len(updates) == 0 {
		return nil
	}
	updates = append(updates, `updated_at = now()`)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	args = append(args, id)
	result, err := tx.ExecContext(ctx,
		`UPDATE groups SET `+strings.Join(updates, ", ")+` WHERE id = $`+itoa(len(args)),
		args...,
	)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return sharedstore.ErrNotFound
	}
	if patch.Status != nil {
		if err := upsertGroupStateTx(ctx, tx, id, status, ""); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AddIssueComment persists a comment and matching timeline entry.
func (s *GroupStore) AddIssueComment(ctx context.Context, groupID, userID, body string) (sharedstore.IssueComment, error) {
	body = strings.TrimSpace(body)
	userID = strings.TrimSpace(userID)
	if body == "" {
		return sharedstore.IssueComment{}, errors.New("comment body is required")
	}
	if userID == "" {
		return sharedstore.IssueComment{}, errors.New("user id is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return sharedstore.IssueComment{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var projectID string
	if err := tx.QueryRowContext(ctx, `SELECT project_id FROM groups WHERE id = $1`, groupID).Scan(&projectID); err != nil {
		if err == sql.ErrNoRows {
			return sharedstore.IssueComment{}, sharedstore.ErrNotFound
		}
		return sharedstore.IssueComment{}, err
	}

	comment := sharedstore.IssueComment{
		ID:          id.New(),
		GroupID:     groupID,
		ProjectID:   projectID,
		UserID:      userID,
		Body:        body,
		DateCreated: time.Now().UTC(),
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO issue_comments (id, group_id, user_id, body, created_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		comment.ID, comment.GroupID, comment.UserID, comment.Body, comment.DateCreated,
	); err != nil {
		return sharedstore.IssueComment{}, err
	}
	if err := recordIssueActivityTx(ctx, tx, projectID, groupID, userID, "comment", "Comment added", body); err != nil {
		return sharedstore.IssueComment{}, err
	}
	if err := tx.Commit(); err != nil {
		return sharedstore.IssueComment{}, err
	}
	return comment, nil
}

// RecordIssueActivity appends a workflow event to the issue timeline.
func (s *GroupStore) RecordIssueActivity(ctx context.Context, groupID, userID, kind, summary, details string) error {
	group, err := s.GetGroup(ctx, groupID)
	if err != nil {
		return err
	}
	if group == nil {
		return sharedstore.ErrNotFound
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO issue_activity (id, group_id, user_id, kind, body, metadata_json, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6::jsonb, now())`,
		id.New(), groupID, controlNullIfEmpty(strings.TrimSpace(userID)), strings.TrimSpace(kind), strings.TrimSpace(summary),
		mustJSON(issueActivityMetadata{Details: strings.TrimSpace(details), ProjectID: group.ProjectID}),
	)
	return err
}

// ListIssueComments returns recent comments for a group.
func (s *GroupStore) ListIssueComments(ctx context.Context, groupID string, limit int) ([]sharedstore.IssueComment, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT c.id, c.group_id, g.project_id, c.user_id, COALESCE(u.email, ''), COALESCE(u.display_name, ''), c.body, c.created_at
		   FROM issue_comments c
		   JOIN groups g ON g.id = c.group_id
		   LEFT JOIN users u ON u.id = c.user_id
		  WHERE c.group_id = $1
		  ORDER BY c.created_at DESC, c.id DESC
		  LIMIT $2`,
		groupID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]sharedstore.IssueComment, 0, limit)
	for rows.Next() {
		var item sharedstore.IssueComment
		var userID, email, name sql.NullString
		var createdAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.GroupID, &item.ProjectID, &userID, &email, &name, &item.Body, &createdAt); err != nil {
			return nil, err
		}
		item.UserID = nullString(userID)
		item.UserEmail = nullString(email)
		item.UserName = nullString(name)
		item.DateCreated = nullTime(createdAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

// ListIssueActivity returns the recent activity timeline for a group.
func (s *GroupStore) ListIssueActivity(ctx context.Context, groupID string, limit int) ([]sharedstore.IssueActivityEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.id, a.group_id, g.project_id, a.user_id, COALESCE(u.email, ''), COALESCE(u.display_name, ''),
		        a.kind, a.body, a.metadata_json, a.created_at
		   FROM issue_activity a
		   JOIN groups g ON g.id = a.group_id
		   LEFT JOIN users u ON u.id = a.user_id
		  WHERE a.group_id = $1
		  ORDER BY a.created_at DESC, a.id DESC
		  LIMIT $2`,
		groupID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]sharedstore.IssueActivityEntry, 0, limit)
	for rows.Next() {
		var item sharedstore.IssueActivityEntry
		var userID, email, name sql.NullString
		var metadataJSON []byte
		var createdAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.GroupID, &item.ProjectID, &userID, &email, &name, &item.Kind, &item.Summary, &metadataJSON, &createdAt); err != nil {
			return nil, err
		}
		item.UserID = nullString(userID)
		item.UserEmail = nullString(email)
		item.UserName = nullString(name)
		item.DateCreated = nullTime(createdAt)
		var metadata issueActivityMetadata
		_ = json.Unmarshal(metadataJSON, &metadata)
		item.Details = metadata.Details
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetIssueWorkflowState loads bookmark, subscription, and merge metadata.
func (s *GroupStore) GetIssueWorkflowState(ctx context.Context, groupID, userID string) (sharedstore.IssueWorkflowState, error) {
	var state sharedstore.IssueWorkflowState
	if strings.TrimSpace(userID) == "" {
		return state, nil
	}

	var bookmarked, subscribed bool
	err := s.db.QueryRowContext(ctx,
		`SELECT
			EXISTS(SELECT 1 FROM issue_bookmarks WHERE group_id = $1 AND user_id = $2),
			EXISTS(SELECT 1 FROM issue_subscriptions WHERE group_id = $1 AND user_id = $2),
			COALESCE(merged_into_group_id, ''),
			COALESCE(substatus, ''),
			COALESCE(resolved_in_release, '')
		   FROM groups
		  WHERE id = $1`,
		groupID, userID,
	).Scan(&bookmarked, &subscribed, &state.MergedIntoGroupID, &state.ResolutionSubstatus, &state.ResolvedInRelease)
	if err == sql.ErrNoRows {
		return state, sharedstore.ErrNotFound
	}
	state.Bookmarked = bookmarked
	state.Subscribed = subscribed
	return state, err
}

// ToggleIssueBookmark stores or removes a user bookmark.
func (s *GroupStore) ToggleIssueBookmark(ctx context.Context, groupID, userID string, bookmarked bool) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return errors.New("user id is required")
	}
	if bookmarked {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO issue_bookmarks (group_id, user_id, created_at)
			 VALUES ($1, $2, now())
			 ON CONFLICT (group_id, user_id) DO UPDATE SET created_at = EXCLUDED.created_at`,
			groupID, userID,
		)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM issue_bookmarks WHERE group_id = $1 AND user_id = $2`, groupID, userID)
	return err
}

// ToggleIssueSubscription stores or removes a user subscription.
func (s *GroupStore) ToggleIssueSubscription(ctx context.Context, groupID, userID string, subscribed bool) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return errors.New("user id is required")
	}
	if subscribed {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO issue_subscriptions (group_id, user_id, created_at)
			 VALUES ($1, $2, now())
			 ON CONFLICT (group_id, user_id) DO UPDATE SET created_at = EXCLUDED.created_at`,
			groupID, userID,
		)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM issue_subscriptions WHERE group_id = $1 AND user_id = $2`, groupID, userID)
	return err
}

// MergeIssue links one issue into another and records the transition.
func (s *GroupStore) MergeIssue(ctx context.Context, sourceGroupID, targetGroupID, userID string) error {
	if strings.TrimSpace(sourceGroupID) == "" || strings.TrimSpace(targetGroupID) == "" {
		return errors.New("source and target issue ids are required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var projectID string
	if err := tx.QueryRowContext(ctx, `SELECT project_id FROM groups WHERE id = $1`, sourceGroupID).Scan(&projectID); err != nil {
		if err == sql.ErrNoRows {
			return sharedstore.ErrNotFound
		}
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM groups WHERE id = $1`, targetGroupID).Scan(new(string)); err != nil {
		if err == sql.ErrNoRows {
			return sharedstore.ErrNotFound
		}
		return err
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE groups
		    SET status = 'ignored',
		        merged_into_group_id = $1,
		        substatus = 'merged',
		        resolved_in_release = '',
		        updated_at = now()
		  WHERE id = $2`,
		targetGroupID, sourceGroupID,
	); err != nil {
		return err
	}
	if err := upsertGroupStateTx(ctx, tx, sourceGroupID, "ignored", strings.TrimSpace(userID)); err != nil {
		return err
	}
	if err := recordIssueActivityTx(ctx, tx, projectID, sourceGroupID, userID, "merge", "Merged issue", "Merged into "+targetGroupID); err != nil {
		return err
	}
	return tx.Commit()
}

// UnmergeIssue clears merge metadata and restores the issue to unresolved.
func (s *GroupStore) UnmergeIssue(ctx context.Context, sourceGroupID, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var projectID string
	if err := tx.QueryRowContext(ctx, `SELECT project_id FROM groups WHERE id = $1`, sourceGroupID).Scan(&projectID); err != nil {
		if err == sql.ErrNoRows {
			return sharedstore.ErrNotFound
		}
		return err
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE groups
		    SET status = 'unresolved',
		        merged_into_group_id = '',
		        substatus = '',
		        resolved_in_release = '',
		        updated_at = now()
		  WHERE id = $1`,
		sourceGroupID,
	); err != nil {
		return err
	}
	if err := upsertGroupStateTx(ctx, tx, sourceGroupID, "unresolved", ""); err != nil {
		return err
	}
	if err := recordIssueActivityTx(ctx, tx, projectID, sourceGroupID, userID, "unmerge", "Unmerged issue", ""); err != nil {
		return err
	}
	return tx.Commit()
}

const groupSelectSQL = `SELECT g.id, g.project_id, g.grouping_version, g.grouping_key, g.title, g.culprit, g.level,
       COALESCE(NULLIF(g.assignee_user_id, ''), NULLIF(g.assignee_team_id, ''), ''),
       g.status, COALESCE(g.substatus, ''), COALESCE(g.resolved_in_release, ''), COALESCE(g.merged_into_group_id, ''),
       g.first_seen, g.last_seen, g.times_seen, COALESCE(g.last_event_id, '')
  FROM groups g`

func scanGroup(scanner rowScanner) (*issue.Group, error) {
	var item issue.Group
	var title, culprit, level, assignee, substatus, resolvedInRelease, mergedIntoGroupID, lastEventID sql.NullString
	var firstSeen, lastSeen sql.NullTime
	err := scanner.Scan(
		&item.ID, &item.ProjectID, &item.GroupingVersion, &item.GroupingKey,
		&title, &culprit, &level, &assignee, &item.Status, &substatus, &resolvedInRelease, &mergedIntoGroupID,
		&firstSeen, &lastSeen, &item.TimesSeen, &lastEventID,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	item.Title = nullString(title)
	item.Culprit = nullString(culprit)
	item.Level = nullString(level)
	item.Assignee = nullString(assignee)
	item.ResolutionSubstatus = nullString(substatus)
	item.ResolvedInRelease = nullString(resolvedInRelease)
	item.MergedIntoGroupID = nullString(mergedIntoGroupID)
	item.FirstSeen = nullTime(firstSeen)
	item.LastSeen = nullTime(lastSeen)
	item.LastEventID = nullString(lastEventID)
	return &item, nil
}

func nextGroupShortID(ctx context.Context, tx *sql.Tx) (int64, error) {
	var next int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(short_id), 0) + 1 FROM groups`).Scan(&next)
	return next, err
}

func upsertGroupStateTx(ctx context.Context, tx *sql.Tx, groupID, status, userID string) error {
	status = strings.TrimSpace(status)
	isResolved := status == "resolved"
	isIgnored := status == "ignored"
	var resolvedAt any
	var resolvedByUserID any
	if isResolved {
		resolvedAt = time.Now().UTC()
		resolvedByUserID = controlNullIfEmpty(strings.TrimSpace(userID))
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO group_states (id, group_id, is_resolved, is_ignored, is_muted, resolved_at, resolved_by_user_id, updated_at)
		 VALUES ($1, $2, $3, $4, FALSE, $5, COALESCE($6, ''), now())
		 ON CONFLICT (group_id) DO UPDATE SET
			is_resolved = EXCLUDED.is_resolved,
			is_ignored = EXCLUDED.is_ignored,
			is_muted = EXCLUDED.is_muted,
			resolved_at = EXCLUDED.resolved_at,
			resolved_by_user_id = COALESCE(EXCLUDED.resolved_by_user_id, ''),
			updated_at = now()`,
		id.New(), groupID, isResolved, isIgnored, resolvedAt, resolvedByUserID,
	)
	return err
}

func recordGroupOccurrenceTx(ctx context.Context, tx *sql.Tx, groupID, eventID string, occurredAt time.Time) error {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return nil
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO group_occurrences (id, group_id, event_id, occurred_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (group_id, event_id) DO NOTHING`,
		id.New(), groupID, eventID, occurredAt.UTC(),
	)
	return err
}

func recordIssueActivityTx(ctx context.Context, tx *sql.Tx, projectID, groupID, userID, kind, summary, details string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO issue_activity (id, group_id, user_id, kind, body, metadata_json, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6::jsonb, now())`,
		id.New(), groupID, controlNullIfEmpty(strings.TrimSpace(userID)), strings.TrimSpace(kind), strings.TrimSpace(summary),
		mustJSON(issueActivityMetadata{Details: strings.TrimSpace(details), ProjectID: projectID}),
	)
	return err
}

func encodeAssignee(assignee string) (string, string) {
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return "", ""
	}
	if strings.HasPrefix(assignee, "team:") {
		return "", assignee
	}
	return assignee, ""
}

func nullableTime(ts time.Time) any {
	if ts.IsZero() {
		return nil
	}
	return ts.UTC()
}

// controlNullIfEmpty is an alias for nullIfEmpty used in group-domain code.
func controlNullIfEmpty(value string) any {
	return nullIfEmpty(strings.TrimSpace(value))
}
