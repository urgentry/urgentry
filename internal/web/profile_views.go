package web

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	sharedstore "urgentry/internal/store"
)

type profileListFilters struct {
	Transaction string
	Release     string
	Environment string
	Start       string
	End         string

	startedAfter time.Time
	endedBefore  time.Time
}

type profileLink struct {
	Label string
	URL   string
	Value string
}

type profileOption struct {
	ID       string
	Label    string
	Selected bool
}

type profileTreeRow struct {
	Name            string
	IndentPX        int
	InclusiveWeight int
	SelfWeight      int
	SampleCount     int
	Percent         string
	BarWidth        int
}

type profileHotPathRow struct {
	Step            int
	Name            string
	InclusiveWeight int
	SampleCount     int
	Percent         string
}

type profileComparisonRow struct {
	Name            string
	BaselineWeight  int
	CandidateWeight int
	DeltaWeight     int
	DeltaLabel      string
	DeltaClass      string
}

type profileComparisonData struct {
	CandidateLabel string
	Confidence     string
	Notes          []string
	Regressions    []profileComparisonRow
	Improvements   []profileComparisonRow
}

type profileDetailControls struct {
	ThreadID   string
	Frame      string
	MaxDepth   int
	MaxNodes   int
	CompareID  string
	Threads    []profileOption
	Comparands []profileOption
}

func parseProfileListFilters(r *http.Request) profileListFilters {
	filters := profileListFilters{
		Transaction: strings.TrimSpace(r.URL.Query().Get("transaction")),
		Release:     strings.TrimSpace(r.URL.Query().Get("release")),
		Environment: strings.TrimSpace(r.URL.Query().Get("environment")),
		Start:       strings.TrimSpace(r.URL.Query().Get("start")),
		End:         strings.TrimSpace(r.URL.Query().Get("end")),
	}
	if filters.Start != "" {
		if parsed, err := time.Parse(time.RFC3339, filters.Start); err == nil {
			filters.startedAfter = parsed
		}
	}
	if filters.End != "" {
		if parsed, err := time.Parse(time.RFC3339, filters.End); err == nil {
			filters.endedBefore = parsed
		}
	}
	return filters
}

func filterProfileManifests(items []sharedstore.ProfileManifest, filters profileListFilters) []sharedstore.ProfileManifest {
	filtered := make([]sharedstore.ProfileManifest, 0, len(items))
	for _, item := range items {
		if filters.Transaction != "" && !strings.EqualFold(strings.TrimSpace(item.Transaction), filters.Transaction) {
			continue
		}
		if filters.Release != "" && !strings.EqualFold(strings.TrimSpace(item.Release), filters.Release) {
			continue
		}
		if filters.Environment != "" && !strings.EqualFold(strings.TrimSpace(item.Environment), filters.Environment) {
			continue
		}
		seenAt := firstNonZeroTime(item.StartedAt, item.DateCreated)
		if !filters.startedAfter.IsZero() && seenAt.Before(filters.startedAfter) {
			continue
		}
		if !filters.endedBefore.IsZero() && seenAt.After(filters.endedBefore) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func profileListQueryString(filters profileListFilters) string {
	parts := make([]string, 0, 5)
	for _, value := range []string{filters.Transaction, filters.Release, filters.Environment, filters.Start, filters.End} {
		if strings.TrimSpace(value) != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " ")
}

func parseProfileDetailFilter(r *http.Request, profileID string) (sharedstore.ProfileQueryFilter, string) {
	filter := sharedstore.ProfileQueryFilter{
		ProfileID:   profileID,
		ThreadID:    strings.TrimSpace(r.URL.Query().Get("thread")),
		FrameFilter: strings.TrimSpace(r.URL.Query().Get("frame")),
		MaxDepth:    64,
		MaxNodes:    512,
	}
	if value, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("max_depth"))); err == nil && value > 0 {
		if value > 256 {
			value = 256
		}
		filter.MaxDepth = value
	}
	if value, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("max_nodes"))); err == nil && value > 0 {
		if value > 2048 {
			value = 2048
		}
		filter.MaxNodes = value
	}
	return filter, strings.TrimSpace(r.URL.Query().Get("compare"))
}

func profileDetailQueryLimit(filter sharedstore.ProfileQueryFilter, compareID string) int {
	limit := filter.MaxNodes
	limit = max(1, limit/16)
	if strings.TrimSpace(compareID) != "" {
		limit = max(limit, 40)
	}
	return limit
}

func profileDetailQueryString(filter sharedstore.ProfileQueryFilter, compareID string) string {
	parts := make([]string, 0, 5)
	for _, value := range []string{filter.ProfileID, filter.ThreadID, filter.FrameFilter, compareID} {
		if strings.TrimSpace(value) != "" {
			parts = append(parts, value)
		}
	}
	if filter.MaxDepth > 0 {
		parts = append(parts, fmt.Sprintf("depth:%d", filter.MaxDepth))
	}
	if filter.MaxNodes > 0 {
		parts = append(parts, fmt.Sprintf("nodes:%d", filter.MaxNodes))
	}
	return strings.Join(parts, " ")
}

func profileThreadOptions(threads []sharedstore.ProfileThread, selected string) []profileOption {
	options := []profileOption{{
		ID:       "",
		Label:    "All threads",
		Selected: strings.TrimSpace(selected) == "",
	}}
	for _, thread := range threads {
		label := strings.TrimSpace(thread.ThreadName)
		if label == "" {
			label = strings.TrimSpace(thread.ThreadKey)
		}
		if label == "" {
			label = "Thread"
		}
		if thread.IsMain {
			label += " · main"
		} else if strings.TrimSpace(thread.ThreadRole) != "" && !strings.EqualFold(thread.ThreadRole, "unknown") {
			label += " · " + strings.TrimSpace(thread.ThreadRole)
		}
		label += fmt.Sprintf(" · %d samples", thread.SampleCount)
		options = append(options, profileOption{
			ID:       thread.ThreadKey,
			Label:    label,
			Selected: thread.ThreadKey == selected,
		})
	}
	return options
}

func profileCompareOptions(items []sharedstore.ProfileManifest, currentID, selected string) []profileOption {
	options := []profileOption{{
		ID:       "",
		Label:    "No comparison",
		Selected: strings.TrimSpace(selected) == "",
	}}
	for _, item := range items {
		if item.ProfileID == currentID {
			continue
		}
		label := strings.TrimSpace(item.Transaction)
		if label == "" {
			label = item.ProfileID
		}
		if strings.TrimSpace(item.Release) != "" {
			label += " · " + strings.TrimSpace(item.Release)
		}
		seenAt := firstNonZeroTime(item.StartedAt, item.DateCreated)
		if !seenAt.IsZero() {
			label += " · " + seenAt.Format("2006-01-02 15:04")
		}
		options = append(options, profileOption{
			ID:       item.ProfileID,
			Label:    label,
			Selected: item.ProfileID == selected,
		})
	}
	return options
}

func flattenProfileTree(tree *sharedstore.ProfileTree) []profileTreeRow {
	if tree == nil {
		return nil
	}
	var rows []profileTreeRow
	appendProfileTreeRows(&rows, tree.Root.Children, tree.TotalWeight, 0)
	return rows
}

func appendProfileTreeRows(rows *[]profileTreeRow, nodes []sharedstore.ProfileTreeNode, totalWeight, depth int) {
	for _, node := range nodes {
		width := 0
		percent := "0.0%"
		if totalWeight > 0 {
			pct := (float64(node.InclusiveWeight) / float64(totalWeight)) * 100
			percent = fmt.Sprintf("%.1f%%", pct)
			width = int(pct)
			if node.InclusiveWeight > 0 && width < 4 {
				width = 4
			}
			if width > 100 {
				width = 100
			}
		}
		*rows = append(*rows, profileTreeRow{
			Name:            node.Name,
			IndentPX:        depth * 18,
			InclusiveWeight: node.InclusiveWeight,
			SelfWeight:      node.SelfWeight,
			SampleCount:     node.SampleCount,
			Percent:         percent,
			BarWidth:        width,
		})
		appendProfileTreeRows(rows, node.Children, totalWeight, depth+1)
	}
}

func mapProfileHotPathRows(path *sharedstore.ProfileHotPath) []profileHotPathRow {
	if path == nil {
		return nil
	}
	rows := make([]profileHotPathRow, 0, len(path.Frames))
	for idx, frame := range path.Frames {
		rows = append(rows, profileHotPathRow{
			Step:            idx + 1,
			Name:            frame.Name,
			InclusiveWeight: frame.InclusiveWeight,
			SampleCount:     frame.SampleCount,
			Percent:         fmt.Sprintf("%.1f%%", frame.Percent),
		})
	}
	return rows
}

func mapProfileComparisonData(items []sharedstore.ProfileManifest, compareID string, comparison *sharedstore.ProfileComparison) *profileComparisonData {
	if comparison == nil {
		return nil
	}
	candidateLabel := compareID
	for _, item := range items {
		if item.ProfileID != compareID {
			continue
		}
		candidateLabel = firstNonEmptyText(strings.TrimSpace(item.Transaction), item.ProfileID)
		if strings.TrimSpace(item.Release) != "" {
			candidateLabel += " · " + strings.TrimSpace(item.Release)
		}
		break
	}
	return &profileComparisonData{
		CandidateLabel: candidateLabel,
		Confidence:     comparison.Confidence,
		Notes:          append([]string{}, comparison.Notes...),
		Regressions:    mapProfileComparisonRows(comparison.TopRegressions),
		Improvements:   mapProfileComparisonRows(comparison.TopImprovements),
	}
}

func mapProfileComparisonRows(items []sharedstore.ProfileComparisonDelta) []profileComparisonRow {
	rows := make([]profileComparisonRow, 0, len(items))
	for _, item := range items {
		deltaClass := "muted"
		if item.DeltaWeight > 0 {
			deltaClass = "error"
		} else if item.DeltaWeight < 0 {
			deltaClass = "ok"
		}
		deltaLabel := strconv.Itoa(item.DeltaWeight)
		if item.DeltaWeight > 0 {
			deltaLabel = "+" + deltaLabel
		}
		rows = append(rows, profileComparisonRow{
			Name:            item.Name,
			BaselineWeight:  item.BaselineWeight,
			CandidateWeight: item.CandidateWeight,
			DeltaWeight:     item.DeltaWeight,
			DeltaLabel:      deltaLabel,
			DeltaClass:      deltaClass,
		})
	}
	return rows
}

func lookupProfileIssueID(ctx context.Context, db *sql.DB, projectID, eventRowID, traceID string) (string, error) {
	if db == nil {
		return "", nil
	}
	if strings.TrimSpace(eventRowID) != "" {
		var groupID string
		err := db.QueryRowContext(ctx, `SELECT COALESCE(group_id, '') FROM events WHERE id = ?`, eventRowID).Scan(&groupID)
		if err == nil && strings.TrimSpace(groupID) != "" {
			return strings.TrimSpace(groupID), nil
		}
		if err != nil && err != sql.ErrNoRows {
			return "", err
		}
	}
	if strings.TrimSpace(traceID) == "" {
		return "", nil
	}
	var groupID string
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(group_id, '')
		 FROM events
		 WHERE project_id = ?
		   AND COALESCE(group_id, '') <> ''
		   AND COALESCE(json_extract(payload_json, '$.contexts.trace.trace_id'), '') = ?
		 ORDER BY occurred_at DESC, ingested_at DESC
		 LIMIT 1`,
		projectID,
		traceID,
	).Scan(&groupID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(groupID), nil
}

func buildProfileLinks(manifest sharedstore.ProfileManifest, issueID string) []profileLink {
	links := make([]profileLink, 0, 3)
	if strings.TrimSpace(manifest.TraceID) != "" {
		links = append(links, profileLink{
			Label: "Trace",
			URL:   "/traces/" + manifest.TraceID + "/",
			Value: manifest.TraceID,
		})
	}
	if strings.TrimSpace(issueID) != "" {
		links = append(links, profileLink{
			Label: "Issue",
			URL:   "/issues/" + issueID + "/",
			Value: issueID,
		})
	}
	if strings.TrimSpace(manifest.Release) != "" {
		links = append(links, profileLink{
			Label: "Release",
			URL:   "/releases/" + manifest.Release + "/",
			Value: manifest.Release,
		})
	}
	return links
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}
