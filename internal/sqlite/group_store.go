package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"urgentry/internal/issue"
	"urgentry/internal/sqlutil"
	sharedstore "urgentry/internal/store"
)

// GroupStore is a SQLite-backed implementation of issue.GroupStore.
type GroupStore struct {
	db *sql.DB
}

// NewGroupStore creates a GroupStore backed by the given database.
func NewGroupStore(db *sql.DB) *GroupStore {
	return &GroupStore{db: db}
}

// UpsertGroup atomically creates or updates a group.
// If a group with the same (project_id, grouping_version, grouping_key) exists,
// it updates last_seen, times_seen, and last_event_id. Otherwise it inserts.
// The group's ID field is set to the actual (possibly pre-existing) row ID.
func (s *GroupStore) UpsertGroup(ctx context.Context, g *issue.Group) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Check for existing group.
	var existingID string
	err = tx.QueryRowContext(ctx,
		"SELECT id FROM groups WHERE project_id = ? AND grouping_version = ? AND grouping_key = ?",
		g.ProjectID, g.GroupingVersion, g.GroupingKey,
	).Scan(&existingID)

	if err == sql.ErrNoRows {
		// Insert new group.
		if g.ID == "" {
			g.ID = generateID()
		}
		if g.TimesSeen == 0 {
			g.TimesSeen = 1
		}
		if g.Status == "" {
			g.Status = "unresolved"
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, culprit, level, assignee, status, first_seen, last_seen, times_seen, last_event_id)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			g.ID, g.ProjectID, g.GroupingVersion, g.GroupingKey,
			g.Title, g.Culprit, g.Level, g.Assignee, g.Status,
			g.FirstSeen.UTC().Format(time.RFC3339),
			g.LastSeen.UTC().Format(time.RFC3339),
			g.TimesSeen, g.LastEventID,
		)
		if err != nil {
			return err
		}
		// Assign the next sequential short_id.
		_, err = tx.ExecContext(ctx,
			`UPDATE groups SET short_id = (SELECT COALESCE(MAX(short_id), 0) + 1 FROM groups) WHERE id = ? AND short_id IS NULL`,
			g.ID,
		)
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	if err != nil {
		return err
	}

	// Update existing group.
	g.ID = existingID
	_, err = tx.ExecContext(ctx,
		`UPDATE groups SET
			last_seen = ?,
			times_seen = times_seen + 1,
			last_event_id = ?,
			title = ?,
			culprit = ?,
			level = ?
		 WHERE id = ?`,
		g.LastSeen.UTC().Format(time.RFC3339),
		g.LastEventID, g.Title, g.Culprit, g.Level,
		existingID,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// GetGroup retrieves a group by its ID.
func (s *GroupStore) GetGroup(ctx context.Context, id string) (*issue.Group, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, grouping_version, grouping_key, title, culprit, level, COALESCE(assignee, ''),
				status, resolution_substatus, resolved_in_release, merged_into_group_id, first_seen, last_seen, times_seen, last_event_id
		 FROM groups WHERE id = ?`, id,
	)
	return scanGroup(row)
}

func (s *GroupStore) GetIssue(ctx context.Context, id string) (*sharedstore.WebIssue, error) {
	return GetIssue(ctx, s.db, id)
}

// GetGroupByKey retrieves a group by its unique (project, version, key) triple.
func (s *GroupStore) GetGroupByKey(ctx context.Context, projectID, version, key string) (*issue.Group, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, grouping_version, grouping_key, title, culprit, level, COALESCE(assignee, ''),
				status, resolution_substatus, resolved_in_release, merged_into_group_id, first_seen, last_seen, times_seen, last_event_id
		 FROM groups WHERE project_id = ? AND grouping_version = ? AND grouping_key = ?`,
		projectID, version, key,
	)
	return scanGroup(row)
}

// ListGroups returns groups for a project with optional filtering and pagination.
func (s *GroupStore) ListGroups(ctx context.Context, projectID string, opts issue.ListOpts) ([]*issue.Group, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 25
	}

	query := `SELECT id, project_id, grouping_version, grouping_key, title, culprit, level, COALESCE(assignee, ''),
				status, resolution_substatus, resolved_in_release, merged_into_group_id, first_seen, last_seen, times_seen, last_event_id
			  FROM groups WHERE project_id = ?`
	args := []any{projectID}

	if opts.Status != "" {
		query += ` AND status = ?`
		args = append(args, opts.Status)
	}

	switch opts.Sort {
	case "last_seen_asc":
		query += ` ORDER BY last_seen ASC`
	case "first_seen_desc":
		query += ` ORDER BY first_seen DESC`
	case "first_seen_asc":
		query += ` ORDER BY first_seen ASC`
	case "times_seen_desc":
		query += ` ORDER BY times_seen DESC`
	default:
		query += ` ORDER BY last_seen DESC`
	}

	query += ` LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []*issue.Group
	for rows.Next() {
		g, err := scanGroupRows(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func (s *GroupStore) SearchProjectIssues(ctx context.Context, projectID, filter, rawQuery string, limit int) ([]sharedstore.WebIssue, error) {
	return SearchProjectIssues(ctx, s.db, projectID, filter, rawQuery, limit)
}

func (s *GroupStore) SearchDiscoverIssues(ctx context.Context, orgSlug, filter, rawQuery string, limit int) ([]sharedstore.DiscoverIssue, error) {
	return SearchDiscoverIssues(ctx, s.db, orgSlug, filter, rawQuery, limit)
}

// UpdateStatus changes the status of a group (e.g. "resolved", "ignored").
func (s *GroupStore) UpdateStatus(ctx context.Context, id string, status string) error {
	empty := ""
	return s.PatchIssue(ctx, id, sharedstore.IssuePatch{
		Status:              &status,
		ResolutionSubstatus: &empty,
		ResolvedInRelease:   &empty,
		MergedIntoGroupID:   &empty,
	})
}

func (s *GroupStore) UpdateAssignee(ctx context.Context, id string, assignee string) error {
	return s.PatchIssue(ctx, id, sharedstore.IssuePatch{Assignee: &assignee})
}

// PatchIssue updates mutable issue metadata through one write-side seam.
func (s *GroupStore) PatchIssue(ctx context.Context, id string, patch sharedstore.IssuePatch) error {
	updates := make([]string, 0, 3)
	args := make([]any, 0, 4)
	if patch.Status != nil {
		updates = append(updates, "status = ?")
		args = append(args, strings.TrimSpace(*patch.Status))
	}
	if patch.Assignee != nil {
		updates = append(updates, "assignee = ?")
		args = append(args, strings.TrimSpace(*patch.Assignee))
	}
	if patch.Priority != nil {
		updates = append(updates, "priority = ?")
		args = append(args, *patch.Priority)
	}
	if patch.ResolutionSubstatus != nil {
		updates = append(updates, "resolution_substatus = ?")
		args = append(args, strings.TrimSpace(*patch.ResolutionSubstatus))
	}
	if patch.ResolvedInRelease != nil {
		updates = append(updates, "resolved_in_release = ?")
		args = append(args, strings.TrimSpace(*patch.ResolvedInRelease))
	}
	if patch.MergedIntoGroupID != nil {
		updates = append(updates, "merged_into_group_id = ?")
		args = append(args, strings.TrimSpace(*patch.MergedIntoGroupID))
	}
	if len(updates) == 0 {
		return nil
	}
	args = append(args, id)
	_, err := s.db.ExecContext(ctx, `UPDATE groups SET `+strings.Join(updates, ", ")+` WHERE id = ?`, args...)
	return err
}

// AddIssueComment persists a comment and timeline entry in a single transaction.
func (s *GroupStore) AddIssueComment(ctx context.Context, groupID, userID, body string) (sharedstore.IssueComment, error) {
	if strings.TrimSpace(body) == "" {
		return sharedstore.IssueComment{}, errors.New("comment body is required")
	}
	grp, err := s.GetGroup(ctx, groupID)
	if err != nil {
		return sharedstore.IssueComment{}, err
	}
	if grp == nil {
		return sharedstore.IssueComment{}, sharedstore.ErrNotFound
	}
	comment := sharedstore.IssueComment{
		ID:        generateID(),
		GroupID:   groupID,
		ProjectID: grp.ProjectID,
		UserID:    strings.TrimSpace(userID),
		Body:      strings.TrimSpace(body),
	}
	activity := sharedstore.IssueActivityEntry{
		ID:        generateID(),
		GroupID:   groupID,
		ProjectID: grp.ProjectID,
		UserID:    strings.TrimSpace(userID),
		Kind:      "comment",
		Summary:   "Comment added",
		Details:   comment.Body,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return sharedstore.IssueComment{}, err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO issue_comments (id, group_id, project_id, user_id, body)
		 VALUES (?, ?, ?, ?, ?)`,
		comment.ID, comment.GroupID, comment.ProjectID, nullIfEmpty(comment.UserID), comment.Body,
	)
	if err != nil {
		return sharedstore.IssueComment{}, err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO issue_activity (id, group_id, project_id, user_id, kind, summary, details)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		activity.ID, activity.GroupID, activity.ProjectID, nullIfEmpty(activity.UserID), activity.Kind, activity.Summary, activity.Details,
	)
	if err != nil {
		return sharedstore.IssueComment{}, err
	}
	if err := tx.Commit(); err != nil {
		return sharedstore.IssueComment{}, err
	}
	return comment, nil
}

// RecordIssueActivity appends one workflow event to the issue timeline.
func (s *GroupStore) RecordIssueActivity(ctx context.Context, groupID, userID, kind, summary, details string) error {
	grp, err := s.GetGroup(ctx, groupID)
	if err != nil {
		return err
	}
	if grp == nil {
		return sharedstore.ErrNotFound
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO issue_activity (id, group_id, project_id, user_id, kind, summary, details)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		generateID(), groupID, grp.ProjectID, nullIfEmpty(userID), strings.TrimSpace(kind), strings.TrimSpace(summary), strings.TrimSpace(details),
	)
	return err
}

// ListIssueComments returns recent comments for a group.
func (s *GroupStore) ListIssueComments(ctx context.Context, groupID string, limit int) ([]sharedstore.IssueComment, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT c.id, c.group_id, c.project_id, c.user_id, COALESCE(u.email, ''), COALESCE(u.display_name, ''), c.body, c.created_at
		 FROM issue_comments c
		 LEFT JOIN users u ON u.id = c.user_id
		 WHERE c.group_id = ?
		 ORDER BY c.created_at DESC, c.id DESC
		 LIMIT ?`,
		groupID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]sharedstore.IssueComment, 0, limit)
	for rows.Next() {
		var item sharedstore.IssueComment
		var userID, email, name, createdAt sql.NullString
		if err := rows.Scan(&item.ID, &item.GroupID, &item.ProjectID, &userID, &email, &name, &item.Body, &createdAt); err != nil {
			return nil, err
		}
		item.UserID = nullStr(userID)
		item.UserEmail = nullStr(email)
		item.UserName = nullStr(name)
		item.DateCreated = parseTime(nullStr(createdAt))
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
		`SELECT a.id, a.group_id, a.project_id, a.user_id, COALESCE(u.email, ''), COALESCE(u.display_name, ''), a.kind, a.summary, a.details, a.created_at
		 FROM issue_activity a
		 LEFT JOIN users u ON u.id = a.user_id
		 WHERE a.group_id = ?
		 ORDER BY a.created_at DESC, a.id DESC
		 LIMIT ?`,
		groupID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]sharedstore.IssueActivityEntry, 0, limit)
	for rows.Next() {
		var item sharedstore.IssueActivityEntry
		var userID, email, name, createdAt sql.NullString
		if err := rows.Scan(&item.ID, &item.GroupID, &item.ProjectID, &userID, &email, &name, &item.Kind, &item.Summary, &item.Details, &createdAt); err != nil {
			return nil, err
		}
		item.UserID = nullStr(userID)
		item.UserEmail = nullStr(email)
		item.UserName = nullStr(name)
		item.DateCreated = parseTime(nullStr(createdAt))
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetIssueWorkflowState loads bookmark/subscription/merge metadata for one issue and user.
func (s *GroupStore) GetIssueWorkflowState(ctx context.Context, groupID, userID string) (sharedstore.IssueWorkflowState, error) {
	var state sharedstore.IssueWorkflowState
	if strings.TrimSpace(userID) == "" {
		return state, nil
	}
	var bookmarked, subscribed int64
	err := s.db.QueryRowContext(ctx,
		`SELECT
			COALESCE((SELECT 1 FROM issue_bookmarks WHERE group_id = ? AND user_id = ?), 0),
			COALESCE((SELECT 1 FROM issue_subscriptions WHERE group_id = ? AND user_id = ?), 0),
			COALESCE(merged_into_group_id, ''),
			COALESCE(resolution_substatus, ''),
			COALESCE(resolved_in_release, '')
		 FROM groups WHERE id = ?`,
		groupID, userID, groupID, userID, groupID,
	).Scan(&bookmarked, &subscribed, &state.MergedIntoGroupID, &state.ResolutionSubstatus, &state.ResolvedInRelease)
	if err == sql.ErrNoRows {
		return state, sharedstore.ErrNotFound
	}
	state.Bookmarked = bookmarked != 0
	state.Subscribed = subscribed != 0
	return state, err
}

// ToggleIssueBookmark stores or removes a user bookmark.
func (s *GroupStore) ToggleIssueBookmark(ctx context.Context, groupID, userID string, bookmarked bool) error {
	if strings.TrimSpace(userID) == "" {
		return errors.New("user id is required")
	}
	if bookmarked {
		_, err := s.db.ExecContext(ctx,
			`INSERT OR REPLACE INTO issue_bookmarks (user_id, group_id, created_at)
			 VALUES (?, ?, datetime('now'))`,
			userID, groupID,
		)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM issue_bookmarks WHERE user_id = ? AND group_id = ?`, userID, groupID)
	return err
}

// ToggleIssueSubscription stores or removes a user subscription.
func (s *GroupStore) ToggleIssueSubscription(ctx context.Context, groupID, userID string, subscribed bool) error {
	if strings.TrimSpace(userID) == "" {
		return errors.New("user id is required")
	}
	if subscribed {
		_, err := s.db.ExecContext(ctx,
			`INSERT OR REPLACE INTO issue_subscriptions (user_id, group_id, created_at)
			 VALUES (?, ?, datetime('now'))`,
			userID, groupID,
		)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM issue_subscriptions WHERE user_id = ? AND group_id = ?`, userID, groupID)
	return err
}

// MergeIssue links one issue into another and records the transition.
func (s *GroupStore) MergeIssue(ctx context.Context, sourceGroupID, targetGroupID, userID string) error {
	if sourceGroupID == "" || targetGroupID == "" {
		return errors.New("source and target issue ids are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var currentStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM groups WHERE id = ?`, sourceGroupID).Scan(&currentStatus); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE groups
		 SET status = 'ignored',
		     merged_into_group_id = ?,
		     merged_at = datetime('now'),
		     merged_by_user_id = ?,
		     resolution_substatus = 'merged',
		     resolved_in_release = ''
		 WHERE id = ?`,
		targetGroupID, nullIfEmpty(userID), sourceGroupID,
	)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO issue_activity (id, group_id, project_id, user_id, kind, summary, details)
		 SELECT ?, g.id, g.project_id, ?, 'merge', 'Merged issue', ?
		 FROM groups g WHERE g.id = ?`,
		generateID(), nullIfEmpty(userID), "Merged into "+targetGroupID, sourceGroupID,
	)
	if err != nil {
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
	_, err = tx.ExecContext(ctx,
		`UPDATE groups
		 SET status = 'unresolved',
		     merged_into_group_id = '',
		     merged_at = NULL,
		     merged_by_user_id = NULL,
		     resolution_substatus = '',
		     resolved_in_release = '',
		     resolved_at = NULL,
		     resolved_by_user_id = NULL
		 WHERE id = ?`,
		sourceGroupID,
	)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO issue_activity (id, group_id, project_id, user_id, kind, summary, details)
		 SELECT ?, g.id, g.project_id, ?, 'unmerge', 'Unmerged issue', ''
		 FROM groups g WHERE g.id = ?`,
		generateID(), nullIfEmpty(userID), sourceGroupID,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteGroup removes a group and all associated data (cascade).
func (s *GroupStore) DeleteGroup(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := deleteGroupCascadeSQLiteTx(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

// BulkDeleteGroups removes multiple groups and all associated data.
func (s *GroupStore) BulkDeleteGroups(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, gid := range ids {
		if err := deleteGroupCascadeSQLiteTx(ctx, tx, gid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// BulkMutateGroups applies a patch to multiple groups in a single transaction.
func (s *GroupStore) BulkMutateGroups(ctx context.Context, ids []string, patch sharedstore.IssuePatch) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, gid := range ids {
		updates := make([]string, 0, 6)
		args := make([]any, 0, 7)
		if patch.Status != nil {
			updates = append(updates, "status = ?")
			args = append(args, strings.TrimSpace(*patch.Status))
		}
		if patch.Assignee != nil {
			updates = append(updates, "assignee = ?")
			args = append(args, strings.TrimSpace(*patch.Assignee))
		}
		if patch.Priority != nil {
			updates = append(updates, "priority = ?")
			args = append(args, *patch.Priority)
		}
		if patch.ResolutionSubstatus != nil {
			updates = append(updates, "resolution_substatus = ?")
			args = append(args, strings.TrimSpace(*patch.ResolutionSubstatus))
		}
		if patch.ResolvedInRelease != nil {
			updates = append(updates, "resolved_in_release = ?")
			args = append(args, strings.TrimSpace(*patch.ResolvedInRelease))
		}
		if patch.MergedIntoGroupID != nil {
			updates = append(updates, "merged_into_group_id = ?")
			args = append(args, strings.TrimSpace(*patch.MergedIntoGroupID))
		}
		if len(updates) == 0 {
			continue
		}
		args = append(args, gid)
		if _, err := tx.ExecContext(ctx,
			`UPDATE groups SET `+strings.Join(updates, ", ")+` WHERE id = ?`,
			args...,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func deleteGroupCascadeSQLiteTx(ctx context.Context, tx *sql.Tx, groupID string) error {
	cascadeTables := []string{
		"issue_subscriptions",
		"issue_bookmarks",
		"issue_activity",
		"issue_comments",
	}
	for _, table := range cascadeTables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE group_id = ?`, groupID); err != nil {
			return err
		}
	}
	// Delete events associated with this group.
	if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE group_id = ?`, groupID); err != nil {
		return err
	}
	// Delete event attachments for events in this group.
	if _, err := tx.ExecContext(ctx, `DELETE FROM event_attachments WHERE event_id IN (SELECT event_id FROM events WHERE group_id = ?)`, groupID); err != nil {
		// Table may not exist in all configurations; ignore.
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM groups WHERE id = ?`, groupID)
	return err
}

// nullStr is a package-level alias for sqlutil.NullStr.
func nullStr(ns sql.NullString) string { return sqlutil.NullStr(ns) }

func scanGroup(row *sql.Row) (*issue.Group, error) {
	var g issue.Group
	var title, culprit, level, assignee sql.NullString
	var resolutionSubstatus, resolvedInRelease, mergedIntoGroupID sql.NullString
	var firstSeen, lastSeen, lastEventID sql.NullString
	err := row.Scan(
		&g.ID, &g.ProjectID, &g.GroupingVersion, &g.GroupingKey,
		&title, &culprit, &level, &assignee, &g.Status,
		&resolutionSubstatus, &resolvedInRelease, &mergedIntoGroupID,
		&firstSeen, &lastSeen, &g.TimesSeen, &lastEventID,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	g.Title = nullStr(title)
	g.Culprit = nullStr(culprit)
	g.Level = nullStr(level)
	g.Assignee = nullStr(assignee)
	g.ResolutionSubstatus = nullStr(resolutionSubstatus)
	g.ResolvedInRelease = nullStr(resolvedInRelease)
	g.MergedIntoGroupID = nullStr(mergedIntoGroupID)
	g.LastEventID = nullStr(lastEventID)
	g.FirstSeen = parseTime(nullStr(firstSeen))
	g.LastSeen = parseTime(nullStr(lastSeen))
	return &g, nil
}

func scanGroupRows(rows *sql.Rows) (*issue.Group, error) {
	var g issue.Group
	var title, culprit, level, assignee sql.NullString
	var resolutionSubstatus, resolvedInRelease, mergedIntoGroupID sql.NullString
	var firstSeen, lastSeen, lastEventID sql.NullString
	err := rows.Scan(
		&g.ID, &g.ProjectID, &g.GroupingVersion, &g.GroupingKey,
		&title, &culprit, &level, &assignee, &g.Status,
		&resolutionSubstatus, &resolvedInRelease, &mergedIntoGroupID,
		&firstSeen, &lastSeen, &g.TimesSeen, &lastEventID,
	)
	if err != nil {
		return nil, err
	}
	g.Title = nullStr(title)
	g.Culprit = nullStr(culprit)
	g.Level = nullStr(level)
	g.Assignee = nullStr(assignee)
	g.ResolutionSubstatus = nullStr(resolutionSubstatus)
	g.ResolvedInRelease = nullStr(resolvedInRelease)
	g.MergedIntoGroupID = nullStr(mergedIntoGroupID)
	g.LastEventID = nullStr(lastEventID)
	g.FirstSeen = parseTime(nullStr(firstSeen))
	g.LastSeen = parseTime(nullStr(lastSeen))
	return &g, nil
}

// parseTime is a package-level alias for sqlutil.ParseDBTime.
func parseTime(s string) time.Time { return sqlutil.ParseDBTime(s) }
