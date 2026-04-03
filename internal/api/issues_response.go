package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/controlplane"
	"urgentry/internal/normalize"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

func apiIssueFromWebIssueWithExtras(row store.WebIssue, extras issueResponseExtras) Issue {
	shortID := row.ID
	if row.ShortID > 0 {
		shortID = fmt.Sprintf("GENTRY-%d", row.ShortID)
	}
	stats := extras.Stats
	if stats.Last24Hours == nil {
		stats.Last24Hours = []IssueSeriesPoint{}
	}
	if stats.Last30Days == nil {
		stats.Last30Days = []IssueSeriesPoint{}
	}
	return Issue{
		ID:                  row.ID,
		ShortID:             shortID,
		Title:               row.Title,
		Culprit:             row.Culprit,
		Level:               row.Level,
		Status:              row.Status,
		Type:                extras.Type,
		AssignedTo:          extras.AssignedTo,
		HasSeen:             extras.HasSeen,
		IsBookmarked:        extras.IsBookmarked,
		IsPublic:            extras.IsPublic,
		IsSubscribed:        extras.IsSubscribed,
		Priority:            extras.Priority,
		Substatus:           extras.Substatus,
		Logger:              nil,
		Metadata:            extras.Metadata,
		Annotations:         []IssueAnnotation{},
		NumComments:         extras.NumComments,
		UserCount:           extras.UserCount,
		Stats:               stats,
		Permalink:           "",
		PluginActions:       [][]string{},
		PluginContexts:      []string{},
		PluginIssues:        []Metadata{},
		ShareID:             nil,
		StatusDetails:       issueStatusDetails(row),
		SubscriptionDetails: Metadata{},
		ResolvedInRelease:   row.ResolvedInRelease,
		MergedIntoIssueID:   row.MergedIntoGroupID,
		FirstSeen:           row.FirstSeen,
		LastSeen:            row.LastSeen,
		Count:               apiIssueCount(row.Count),
		Activity:            []IssueActivitySummary{},
		Tags:                []IssueTagFacet{},
		SeenBy:              []IssueUser{},
		Participants:        []IssueUser{},
	}
}

func apiIssueCount(count int64) string {
	return strconv.FormatInt(count, 10)
}

func apiProjectRef(id, slug, name, platform string) ProjectRef {
	return ProjectRef{
		ID:       id,
		Slug:     slug,
		Name:     name,
		Platform: platform,
	}
}

func apiProjectRefFromProject(project *store.Project) ProjectRef {
	if project == nil {
		return ProjectRef{}
	}
	return apiProjectRef(project.ID, project.Slug, project.Name, project.Platform)
}

func projectRefForIssue(ctx context.Context, db *sql.DB, issueID string) (ProjectRef, error) {
	var ref ProjectRef
	err := db.QueryRowContext(ctx,
		`SELECT p.id, p.slug, COALESCE(p.name, ''), COALESCE(p.platform, '')
		 FROM groups g
		 JOIN projects p ON p.id = g.project_id
		 WHERE g.id = ?`,
		issueID,
	).Scan(&ref.ID, &ref.Slug, &ref.Name, &ref.Platform)
	if err != nil {
		if err == sql.ErrNoRows {
			return ProjectRef{}, nil
		}
		return ProjectRef{}, err
	}
	return ref, nil
}

func defaultIssueResponseExtras(row store.WebIssue) issueResponseExtras {
	return issueResponseExtras{
		AssignedTo: apiIssueAssignee(row.Assignee),
		HasSeen:    true,
		Priority:   row.Priority,
		Substatus:  row.ResolutionSubstatus,
		Metadata:   issueMetadata(row),
		Type:       "error",
	}
}

func loadIssueResponseExtras(ctx context.Context, db *sql.DB, issues controlplane.IssueWorkflowStore, userID string, rows []store.WebIssue) map[string]issueResponseExtras {
	extras := make(map[string]issueResponseExtras, len(rows))
	groupIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		extras[row.ID] = defaultIssueResponseExtras(row)
		groupIDs = append(groupIDs, row.ID)
	}

	if db != nil && len(groupIDs) > 0 {
		webStore := sqlite.NewWebStore(db)
		if counts, err := webStore.BatchUserCounts(ctx, groupIDs); err == nil {
			for groupID, count := range counts {
				item := extras[groupID]
				item.UserCount = count
				extras[groupID] = item
			}
		}
		now := time.Now().UTC()
		if sparklines, err := webStore.BatchSparklines(ctx, groupIDs, 24, 24*time.Hour); err == nil {
			for _, groupID := range groupIDs {
				item := extras[groupID]
				item.Stats.Last24Hours = issueSparklinePoints(sparklines[groupID], time.Hour, now)
				extras[groupID] = item
			}
		}
		if sparklines, err := webStore.BatchSparklines(ctx, groupIDs, 30, 30*24*time.Hour); err == nil {
			for _, groupID := range groupIDs {
				item := extras[groupID]
				item.Stats.Last30Days = issueSparklinePoints(sparklines[groupID], 24*time.Hour, now)
				extras[groupID] = item
			}
		}
	}

	if issues == nil {
		return extras
	}

	var commentCounts map[string]int
	if counter, ok := any(issues).(issueWorkflowCommentCounter); ok {
		if counts, err := counter.BatchIssueCommentCounts(ctx, groupIDs); err == nil {
			commentCounts = counts
		}
	}

	// Batch workflow state: single query instead of N queries.
	var batchStates map[string]store.IssueWorkflowState
	if batchReader, ok := any(issues).(issueWorkflowBatchStateReader); ok {
		batchStates, _ = batchReader.BatchIssueWorkflowStates(ctx, groupIDs, userID)
	}
	var stateReader issueWorkflowStateReader
	if batchStates == nil {
		if reader, ok := any(issues).(issueWorkflowStateReader); ok {
			stateReader = reader
		}
	}

	for _, row := range rows {
		item := extras[row.ID]
		if commentCounts != nil {
			item.NumComments = commentCounts[row.ID]
		} else if comments, err := issues.ListIssueComments(ctx, row.ID, 100); err == nil {
			item.NumComments = len(comments)
		}
		if batchStates != nil {
			if state, ok := batchStates[row.ID]; ok {
				item.IsBookmarked = state.Bookmarked
				item.IsSubscribed = state.Subscribed
				if item.Substatus == "" {
					item.Substatus = state.ResolutionSubstatus
				}
			}
		} else if stateReader != nil {
			if state, err := stateReader.GetIssueWorkflowState(ctx, row.ID, userID); err == nil {
				item.IsBookmarked = state.Bookmarked
				item.IsSubscribed = state.Subscribed
				if item.Substatus == "" {
					item.Substatus = state.ResolutionSubstatus
				}
			}
		}
		extras[row.ID] = item
	}

	return extras
}

func apiIssueAssignee(value string) *IssueUser {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if slug, ok := strings.CutPrefix(value, "team:"); ok {
		slug = strings.TrimSpace(slug)
		if slug == "" {
			return nil
		}
		return &IssueUser{
			ID:       slug,
			Type:     "team",
			Name:     slug,
			Username: slug,
		}
	}
	user := &IssueUser{
		ID:   value,
		Type: "user",
		Name: value,
	}
	if strings.Contains(value, "@") {
		user.Email = value
		user.Username = strings.TrimSpace(strings.SplitN(value, "@", 2)[0])
		return user
	}
	user.Username = value
	return user
}

func issueEmptyStats() IssueStats {
	return IssueStats{
		Last24Hours: []IssueSeriesPoint{},
		Last30Days:  []IssueSeriesPoint{},
	}
}

func issueSparklinePoints(counts []int, step time.Duration, end time.Time) []IssueSeriesPoint {
	if len(counts) == 0 {
		return []IssueSeriesPoint{}
	}
	points := make([]IssueSeriesPoint, 0, len(counts))
	start := end.Add(-time.Duration(len(counts)) * step)
	for i, count := range counts {
		ts := start.Add(time.Duration(i) * step).Unix()
		points = append(points, IssueSeriesPoint{ts, int64(count)})
	}
	return points
}

func issueStatusDetails(row store.WebIssue) Metadata {
	status := Metadata{}
	if row.ResolutionSubstatus == "next_release" {
		status["inNextRelease"] = true
	}
	if strings.TrimSpace(row.ResolvedInRelease) != "" {
		status["inRelease"] = row.ResolvedInRelease
	}
	return status
}

func finalizeIssueResponse(issue *Issue, orgSlug string) {
	if issue == nil {
		return
	}
	issue.Permalink = issuePermalink(orgSlug, issue.ID)
	if issue.Activity == nil {
		issue.Activity = []IssueActivitySummary{}
	}
	if issue.Annotations == nil {
		issue.Annotations = []IssueAnnotation{}
	}
	if issue.Tags == nil {
		issue.Tags = []IssueTagFacet{}
	}
	if issue.SeenBy == nil {
		issue.SeenBy = []IssueUser{}
	}
	if issue.Participants == nil {
		issue.Participants = []IssueUser{}
	}
	if issue.PluginActions == nil {
		issue.PluginActions = [][]string{}
	}
	if issue.PluginContexts == nil {
		issue.PluginContexts = []string{}
	}
	if issue.PluginIssues == nil {
		issue.PluginIssues = []Metadata{}
	}
}

func issuePermalink(orgSlug, issueID string) string {
	if strings.TrimSpace(orgSlug) == "" || strings.TrimSpace(issueID) == "" {
		return ""
	}
	return "/organizations/" + orgSlug + "/issues/" + issueID + "/"
}

func issueMetadata(row store.WebIssue) Metadata {
	return issueMetadataFromParts(row.Title, row.Culprit)
}

func issueMetadataFromParts(title, culprit string) Metadata {
	meta := Metadata{}
	title = strings.TrimSpace(title)
	if prefix, value, ok := strings.Cut(title, ":"); ok {
		if prefix = strings.TrimSpace(prefix); prefix != "" {
			meta["type"] = prefix
		}
		if value = strings.TrimSpace(value); value != "" {
			meta["value"] = value
		}
	} else if title != "" {
		meta["value"] = title
	}
	if culprit = strings.TrimSpace(culprit); culprit != "" {
		meta["culprit"] = culprit
	}
	return meta
}

func apiEventsFromWebEvents(rows []store.WebEvent) []*Event {
	events := make([]*Event, 0, len(rows))
	for _, row := range rows {
		events = append(events, apiEventFromWebEvent(row))
	}
	return events
}

func apiEventTags(tags map[string]string) []EventTag {
	if len(tags) == 0 {
		return nil
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]EventTag, 0, len(keys))
	for _, key := range keys {
		items = append(items, EventTag{Key: key, Value: tags[key]})
	}
	return items
}

func apiEventFromWebEvent(row store.WebEvent) *Event {
	resolvedFrames, unresolvedFrames := normalize.CountNativeFrames(row.NormalizedJSON)
	extras := eventResponseExtrasFromWebEvent(row)
	return &Event{
		ID:               row.EventID,
		EventID:          row.EventID,
		IssueID:          row.GroupID,
		Title:            row.Title,
		Message:          row.Message,
		Level:            row.Level,
		Platform:         row.Platform,
		Culprit:          row.Culprit,
		ProcessingStatus: string(row.ProcessingStatus),
		IngestError:      row.IngestError,
		ResolvedFrames:   resolvedFrames,
		UnresolvedFrames: unresolvedFrames,
		DateCreated:      row.Timestamp,
		Tags:             apiEventTags(row.Tags),
		Entries:          extras.Entries,
		Contexts:         extras.Contexts,
		SDK:              extras.SDK,
		User:             extras.User,
		Fingerprints:     extras.Fingerprints,
		Errors:           extras.Errors,
		Packages:         extras.Packages,
		Measurements:     extras.Measurements,
	}
}

func eventResponseExtrasFromWebEvent(row store.WebEvent) eventResponseExtras {
	extras := eventResponseExtras{}
	if message := strings.TrimSpace(row.Message); message != "" {
		extras.Entries = append(extras.Entries, EventEntry{
			Type: "message",
			Data: Metadata{"message": message},
		})
	}
	if strings.TrimSpace(row.NormalizedJSON) == "" {
		return extras
	}

	var payload storedEventPayload
	if err := json.Unmarshal([]byte(row.NormalizedJSON), &payload); err != nil {
		return extras
	}

	if len(payload.Exception) > 0 {
		extras.Entries = append(extras.Entries, EventEntry{Type: "exception", Data: payload.Exception})
	}
	if len(payload.Request) > 0 {
		extras.Entries = append(extras.Entries, EventEntry{Type: "request", Data: payload.Request})
	}
	if len(payload.Breadcrumbs) > 0 {
		extras.Entries = append(extras.Entries, EventEntry{Type: "breadcrumbs", Data: payload.Breadcrumbs})
	}
	extras.Contexts = payload.Contexts
	extras.SDK = payload.SDK
	extras.User = payload.User
	extras.Fingerprints = append([]string(nil), payload.Fingerprint...)
	extras.Errors = payload.Errors
	extras.Measurements = payload.Measurements
	extras.Packages = payload.Packages
	if len(extras.Packages) == 0 && len(payload.Modules) > 0 {
		extras.Packages = make(Metadata, len(payload.Modules))
		for name, version := range payload.Modules {
			extras.Packages[name] = version
		}
	}
	return extras
}

func issueCommentFromStore(item store.IssueComment) IssueComment {
	return IssueComment{
		ID:          item.ID,
		IssueID:     item.GroupID,
		ProjectID:   item.ProjectID,
		UserID:      item.UserID,
		UserEmail:   item.UserEmail,
		UserName:    item.UserName,
		Body:        item.Body,
		DateCreated: item.DateCreated,
	}
}

func issueActivityFromStore(item store.IssueActivityEntry) IssueActivity {
	return IssueActivity{
		ID:          item.ID,
		IssueID:     item.GroupID,
		ProjectID:   item.ProjectID,
		UserID:      item.UserID,
		UserEmail:   item.UserEmail,
		UserName:    item.UserName,
		Kind:        item.Kind,
		Summary:     item.Summary,
		Details:     item.Details,
		DateCreated: item.DateCreated,
	}
}
