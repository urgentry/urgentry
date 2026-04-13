package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	blobstore "urgentry/internal/blob"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
	sharedstore "urgentry/internal/store"
	"urgentry/internal/telemetryquery"
)

func handleGetReplayManifest(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID, replayID, ok := guardReplayProjectRead(w, r, db, guard, auth, 1, true)
		if !ok {
			return
		}
		record, err := queries.GetReplay(r.Context(), projectID, replayID)
		if err != nil {
			if err == sharedstore.ErrNotFound {
				httputil.WriteError(w, http.StatusNotFound, "Replay not found.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load replay manifest.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapReplayPlaybackManifest(r, record))
	}
}

func handleListReplayTimeline(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return handleReplayTimelineQuery(db, queries, guard, auth, "")
}

func handleListReplayPane(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handleReplayTimelineQuery(db, queries, guard, auth, PathParam(r, "pane"))(w, r)
	}
}

func handleReplayTimelineQuery(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc, fixedPane string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pane := fixedPane
		if pane == "" {
			pane = strings.TrimSpace(r.URL.Query().Get("pane"))
		}
		limit := boundedIntQuery(r, "limit", 100, 1, 500)
		projectID, replayID, ok := guardReplayProjectRead(w, r, db, guard, auth, limit, true)
		if !ok {
			return
		}
		filter := sharedstore.ReplayTimelineFilter{
			Pane:    pane,
			Kind:    strings.TrimSpace(r.URL.Query().Get("kind")),
			StartMS: int64Query(r, "start_ms"),
			EndMS:   int64Query(r, "end_ms"),
			Limit:   limit + 1,
			EventID: strings.TrimSpace(r.URL.Query().Get("event_id")),
			TraceID: strings.TrimSpace(r.URL.Query().Get("trace_id")),
			IssueID: strings.TrimSpace(r.URL.Query().Get("issue_id")),
			Search:  strings.TrimSpace(r.URL.Query().Get("query")),
		}
		items, err := queries.ListReplayTimeline(r.Context(), projectID, replayID, filter)
		if err != nil {
			if err == sharedstore.ErrNotFound {
				httputil.WriteError(w, http.StatusNotFound, "Replay not found.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load replay timeline.")
			return
		}
		page := ReplayTimelinePage{
			ReplayID: replayID,
			Pane:     pane,
			StartMS:  filter.StartMS,
			EndMS:    filter.EndMS,
			Limit:    limit,
			Items:    make([]ReplayTimelineItem, 0, minInt(limit, len(items))),
		}
		if len(items) > limit {
			page.HasMore = true
			page.NextStartMS = items[limit].TSMS
			items = items[:limit]
		}
		for _, item := range items {
			page.Items = append(page.Items, mapReplayTimelineItem(item))
		}
		if len(page.Items) > 0 {
			page.StartMS = page.Items[0].TimestampMS
			page.EndMS = page.Items[len(page.Items)-1].TimestampMS
		}
		httputil.WriteJSON(w, http.StatusOK, page)
	}
}

func handleDownloadReplayAsset(db *sql.DB, queries telemetryquery.Service, blobs sharedstore.BlobStore, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if blobs == nil {
			httputil.WriteError(w, http.StatusServiceUnavailable, "Replay asset store unavailable.")
			return
		}
		projectID, replayID, ok := guardReplayProjectRead(w, r, db, guard, auth, 1, true)
		if !ok {
			return
		}
		record, err := queries.GetReplay(r.Context(), projectID, replayID)
		if err != nil {
			if err == sharedstore.ErrNotFound {
				httputil.WriteError(w, http.StatusNotFound, "Replay not found.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load replay.")
			return
		}
		attachmentID := PathParam(r, "attachment_id")
		var matched *sharedstore.ReplayAssetRef
		for i := range record.Assets {
			if record.Assets[i].AttachmentID == attachmentID || record.Assets[i].ID == attachmentID {
				matched = &record.Assets[i]
				break
			}
		}
		if matched == nil {
			httputil.WriteError(w, http.StatusNotFound, "Replay asset not found.")
			return
		}
		data, err := blobstore.NewResolver(db, blobs).Read(r.Context(), blobstore.Attachment(projectID, matched.AttachmentID, matched.ObjectKey))
		if err != nil {
			if err == sharedstore.ErrNotFound {
				httputil.WriteError(w, http.StatusNotFound, "Replay asset not found.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load replay asset.")
			return
		}
		if data == nil {
			httputil.WriteError(w, http.StatusNotFound, "Replay asset not found.")
			return
		}
		contentType := matched.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", strconv.FormatInt(int64(len(data)), 10))
		w.Header().Set("Content-Disposition", `attachment; filename="`+sanitizeAttachmentFilename(matched.Name)+`"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
}

func guardReplayProjectRead(w http.ResponseWriter, r *http.Request, db *sql.DB, guard sqlite.QueryGuard, auth authFunc, limit int, detail bool) (projectID, replayID string, ok bool) {
	if !auth(w, r) {
		return "", "", false
	}
	projectID, ok = resolveProjectID(w, r, db)
	if !ok {
		return "", "", false
	}
	org, err := getOrganizationFromDB(r, db, PathParam(r, "org_slug"))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "Failed to load organization.")
		return "", "", false
	}
	if org == nil {
		httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
		return "", "", false
	}
	if !enforceQueryGuard(w, r, guard, org.ID, projectID, sqlite.QueryEstimate{
		Workload: sqlite.QueryWorkloadReplays,
		Limit:    limit,
		Detail:   detail,
	}) {
		return "", "", false
	}
	return projectID, PathParam(r, "replay_id"), true
}

func mapReplayPlaybackManifest(r *http.Request, record *sharedstore.ReplayRecord) ReplayPlaybackManifest {
	manifest := ReplayPlaybackManifest{
		ID:               record.Manifest.ID,
		ProjectID:        record.Manifest.ProjectID,
		ReplayID:         record.Manifest.ReplayID,
		Platform:         record.Manifest.Platform,
		Release:          record.Manifest.Release,
		Environment:      record.Manifest.Environment,
		RequestURL:       record.Manifest.RequestURL,
		User:             firstNonEmptyString(record.Manifest.UserRef.ID, record.Manifest.UserRef.Email, record.Manifest.UserRef.Username),
		ProcessingStatus: string(record.Manifest.ProcessingStatus),
		IngestError:      record.Manifest.IngestError,
		DurationMS:       record.Manifest.DurationMS,
		TimelineStartMS:  record.Manifest.TimelineStartMS,
		TimelineEndMS:    record.Manifest.TimelineEndMS,
		Counts: ReplayEventCounts{
			Assets:     record.Manifest.AssetCount,
			Console:    record.Manifest.ConsoleCount,
			Network:    record.Manifest.NetworkCount,
			Clicks:     record.Manifest.ClickCount,
			Navigation: record.Manifest.NavigationCount,
			ErrorMarks: record.Manifest.ErrorMarkerCount,
		},
		TraceIDs:       append([]string(nil), record.Manifest.TraceIDs...),
		LinkedEventIDs: append([]string(nil), record.Manifest.LinkedEventIDs...),
		LinkedIssueIDs: append([]string(nil), record.Manifest.LinkedIssueIDs...),
		Assets:         make([]ReplayAssetRef, 0, len(record.Assets)),
		DateCreated:    record.Manifest.CreatedAt,
	}
	if !record.Manifest.StartedAt.IsZero() {
		startedAt := record.Manifest.StartedAt
		manifest.StartedAt = &startedAt
	}
	if !record.Manifest.EndedAt.IsZero() {
		endedAt := record.Manifest.EndedAt
		manifest.EndedAt = &endedAt
	}
	base := "/api/0/projects/" + PathParam(r, "org_slug") + "/" + PathParam(r, "proj_slug") + "/replays/" + record.Manifest.ReplayID + "/assets/"
	for _, asset := range record.Assets {
		manifest.Assets = append(manifest.Assets, ReplayAssetRef{
			ID:           asset.ID,
			AttachmentID: asset.AttachmentID,
			Kind:         asset.Kind,
			Name:         asset.Name,
			ContentType:  asset.ContentType,
			SizeBytes:    asset.SizeBytes,
			ChunkIndex:   asset.ChunkIndex,
			DateCreated:  asset.CreatedAt,
			DownloadURL:  base + asset.AttachmentID + "/",
		})
	}
	return manifest
}

func mapReplayTimelineItem(item sharedstore.ReplayTimelineItem) ReplayTimelineItem {
	return ReplayTimelineItem{
		ID:            item.ID,
		Anchor:        item.ID,
		ReplayID:      item.ReplayID,
		TimestampMS:   item.TSMS,
		Kind:          item.Kind,
		Pane:          item.Pane,
		Title:         item.Title,
		Level:         item.Level,
		Message:       item.Message,
		URL:           item.URL,
		Method:        item.Method,
		StatusCode:    item.StatusCode,
		DurationMS:    item.DurationMS,
		Selector:      item.Selector,
		Text:          item.Text,
		TraceID:       item.TraceID,
		LinkedEventID: item.LinkedEventID,
		LinkedIssueID: item.LinkedIssueID,
		PayloadRef:    item.PayloadRef,
		Meta:          item.MetaJSON,
	}
}

func boundedIntQuery(r *http.Request, name string, defaultValue, minVal, maxVal int) int {
	value := defaultValue
	if raw := strings.TrimSpace(r.URL.Query().Get(name)); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			value = parsed
		}
	}
	if value < minVal {
		value = minVal
	}
	if value > maxVal {
		value = maxVal
	}
	return value
}

func int64Query(r *http.Request, name string) int64 {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0
	}
	value, _ := strconv.ParseInt(raw, 10, 64)
	return value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
