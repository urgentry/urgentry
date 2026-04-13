package sqlite

import (
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/store"
)

func (s *ReplayStore) IndexReplay(ctx context.Context, projectID, replayID string) error {
	evt, err := NewEventStore(s.db).GetEventByType(ctx, projectID, replayID, "replay")
	if err != nil {
		if err == store.ErrNotFound {
			return nil
		}
		return fmt.Errorf("load replay receipt: %w", err)
	}
	var (
		hint     replayReceiptHint
		parseErr error
	)
	manifest, manifestErr := s.lookupReplayManifest(ctx, projectID, replayID)
	if manifestErr != nil && manifestErr != store.ErrNotFound {
		return manifestErr
	}
	if manifest != nil && manifest.ProcessingStatus != store.ReplayProcessingStatusFailed && !bytes.Contains(evt.NormalizedJSON, []byte(`"policy_drop_reason"`)) {
		hint = replayHintFromManifest(evt, manifest)
	} else {
		hint, parseErr = parseReplayReceiptHint(evt.NormalizedJSON, evt.EventID)
		hint.EventID = firstNonEmptyText(evt.EventID, hint.EventID, replayID)
		hint.ReplayID = firstNonEmptyText(hint.ReplayID, evt.EventID, replayID)
		hint.Platform = firstNonEmptyText(evt.Platform, hint.Platform, "javascript")
		hint.Release = firstNonEmptyText(evt.ReleaseID, hint.Release)
		hint.Environment = firstNonEmptyText(evt.Environment, hint.Environment)
		hint.OccurredAt = firstNonZeroTime(hint.OccurredAt, evt.OccurredAt, evt.IngestedAt, time.Now().UTC())
	}

	assets, err := s.loadReplayAssets(ctx, evt.EventID, hint.ReplayID)
	if err != nil {
		return err
	}
	timeline, parseIssues, err := s.buildReplayTimeline(ctx, projectID, evt, assets, hint)
	if err != nil {
		return err
	}
	status := store.ReplayProcessingStatusReady
	var ingestIssues []string
	if parseErr != nil {
		status = store.ReplayProcessingStatusFailed
		ingestIssues = append(ingestIssues, parseErr.Error())
	}
	if len(assets) == 0 {
		status = store.ReplayProcessingStatusPartial
		ingestIssues = append(ingestIssues, "replay recording not uploaded")
	}
	if len(parseIssues) > 0 {
		ingestIssues = append(ingestIssues, parseIssues...)
		if len(timeline) == 0 {
			status = store.ReplayProcessingStatusFailed
		} else if status == store.ReplayProcessingStatusReady {
			status = store.ReplayProcessingStatusPartial
		}
	}
	if hint.PolicyError != "" {
		ingestIssues = append(ingestIssues, hint.PolicyError)
		if status == store.ReplayProcessingStatusReady {
			status = store.ReplayProcessingStatusPartial
		}
	}
	ingestError := strings.Join(ingestIssues, "; ")
	if err := s.upsertManifest(ctx, evt, hint, assets, timeline, status, ingestError); err != nil {
		return err
	}
	eventStatus := store.EventProcessingStatusCompleted
	if status == store.ReplayProcessingStatusFailed {
		eventStatus = store.EventProcessingStatusFailed
	}
	if err := NewEventStore(s.db).UpdateProcessingStatus(ctx, evt.ID, eventStatus, ingestError); err != nil {
		return fmt.Errorf("update replay receipt status: %w", err)
	}
	return nil
}

func (s *ReplayStore) loadReplayAssets(ctx context.Context, eventID, replayID string) ([]store.ReplayAssetRef, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, COALESCE(content_type, ''), size_bytes, object_key, COALESCE(created_at, '')
		  FROM event_attachments
		 WHERE event_id = ?
		 ORDER BY created_at ASC, id ASC`,
		eventID,
	)
	if err != nil {
		return nil, fmt.Errorf("list replay attachments: %w", err)
	}
	defer rows.Close()

	var assets []store.ReplayAssetRef
	for rows.Next() {
		var attachmentID, name, contentType, objectKey, createdAt string
		var sizeBytes int64
		if err := rows.Scan(&attachmentID, &name, &contentType, &sizeBytes, &objectKey, &createdAt); err != nil {
			return nil, err
		}
		assets = append(assets, store.ReplayAssetRef{
			ID:           generateID(),
			ReplayID:     replayID,
			AttachmentID: attachmentID,
			Kind:         replayAssetKind(name, contentType),
			Name:         name,
			ContentType:  contentType,
			SizeBytes:    sizeBytes,
			ObjectKey:    objectKey,
			ChunkIndex:   replayChunkIndex(name),
			CreatedAt:    parseTime(createdAt),
		})
	}
	sort.SliceStable(assets, func(i, j int) bool {
		if assets[i].ChunkIndex != assets[j].ChunkIndex {
			return assets[i].ChunkIndex < assets[j].ChunkIndex
		}
		if !assets[i].CreatedAt.Equal(assets[j].CreatedAt) {
			return assets[i].CreatedAt.Before(assets[j].CreatedAt)
		}
		return assets[i].Name < assets[j].Name
	})
	return assets, nil
}

func (s *ReplayStore) buildReplayTimeline(ctx context.Context, projectID string, evt *store.StoredEvent, assets []store.ReplayAssetRef, hint replayReceiptHint) ([]store.ReplayTimelineItem, []string, error) {
	var timeline []store.ReplayTimelineItem
	var issues []string
	indexBase := 0
	for _, asset := range assets {
		if asset.Kind != "recording" && asset.Kind != "snapshot" {
			continue
		}
		body, err := s.loadBlob(ctx, evt.ProjectID, asset.ObjectKey, "replay", asset.AttachmentID)
		if err != nil {
			return nil, nil, fmt.Errorf("load replay asset %s: %w", asset.Name, err)
		}
		items, itemErr := extractReplayTimeline(body, hint, asset, indexBase)
		if itemErr != nil {
			issues = append(issues, fmt.Sprintf("%s: %v", asset.Name, itemErr))
			continue
		}
		indexBase += len(items)
		timeline = append(timeline, items...)
	}
	if len(timeline) == 0 {
		return timeline, issues, nil
	}
	if err := s.resolveReplayTimelineLinks(ctx, projectID, timeline); err != nil {
		return nil, nil, err
	}
	sort.SliceStable(timeline, func(i, j int) bool {
		if timeline[i].TSMS != timeline[j].TSMS {
			return timeline[i].TSMS < timeline[j].TSMS
		}
		return timeline[i].ItemIndex < timeline[j].ItemIndex
	})
	for i := range timeline {
		timeline[i].ItemIndex = i
	}
	return timeline, issues, nil
}

func (s *ReplayStore) resolveReplayTimelineLinks(ctx context.Context, projectID string, timeline []store.ReplayTimelineItem) error {
	cache := map[string]string{}
	for i := range timeline {
		eventID := strings.TrimSpace(timeline[i].LinkedEventID)
		if eventID == "" {
			continue
		}
		if issueID, ok := cache[eventID]; ok {
			timeline[i].LinkedIssueID = issueID
			continue
		}
		var issueID string
		err := s.db.QueryRowContext(ctx,
			`SELECT COALESCE(group_id, '') FROM events WHERE project_id = ? AND event_id = ?`,
			projectID, eventID,
		).Scan(&issueID)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("resolve replay linked issue: %w", err)
		}
		issueID = strings.TrimSpace(issueID)
		cache[eventID] = issueID
		timeline[i].LinkedIssueID = issueID
	}
	return nil
}

func (s *ReplayStore) loadBlob(ctx context.Context, projectID, objectKey, recordType, recordID string) ([]byte, error) {
	if s.blobs == nil {
		return nil, store.ErrNotFound
	}
	body, err := s.blobs.Get(ctx, objectKey)
	if err == nil {
		return body, nil
	}
	if restoreErr := restoreArchivedBlob(ctx, s.db, s.blobs, projectID, recordType, recordID, objectKey); restoreErr != nil {
		return nil, err
	}
	return s.blobs.Get(ctx, objectKey)
}

func replayHintFromManifest(evt *store.StoredEvent, manifest *store.ReplayManifest) replayReceiptHint {
	if manifest == nil {
		return replayReceiptHint{}
	}
	hint := replayReceiptHint{
		EventID:              firstNonEmptyText(evt.EventID, manifest.ReplayID),
		ReplayID:             firstNonEmptyText(manifest.ReplayID, evt.EventID),
		OccurredAt:           firstNonZeroTime(manifest.StartedAt, evt.OccurredAt, evt.IngestedAt, time.Now().UTC()),
		Platform:             firstNonEmptyText(manifest.Platform, evt.Platform, "javascript"),
		Release:              firstNonEmptyText(manifest.Release, evt.ReleaseID),
		Environment:          firstNonEmptyText(manifest.Environment, evt.Environment),
		RequestURL:           manifest.RequestURL,
		User:                 manifest.UserRef,
		TraceIDs:             append([]string(nil), manifest.TraceIDs...),
		PrivacyPolicyVersion: manifest.PrivacyPolicyVersion,
	}
	return hint
}

func extractReplayTimeline(body []byte, hint replayReceiptHint, asset store.ReplayAssetRef, startIndex int) ([]store.ReplayTimelineItem, error) {
	rawEvents, err := decodeReplayRecording(body)
	if err != nil {
		return nil, err
	}
	items := make([]store.ReplayTimelineItem, 0, len(rawEvents))
	for i, raw := range rawEvents {
		item, ok := parseReplayTimelineItem(raw, hint, asset, startIndex+i)
		if ok {
			items = append(items, item)
		}
	}
	return items, nil
}

func decodeReplayRecording(body []byte) ([]json.RawMessage, error) {
	body = bytesTrimSpace(body)
	if len(body) == 0 {
		return nil, fmt.Errorf("empty replay recording payload")
	}
	if body[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, err
		}
		return items, nil
	}
	var envelope replayRecordingEnvelope
	if err := json.Unmarshal(body, &envelope); err == nil && len(envelope.Events) > 0 {
		return envelope.Events, nil
	}
	var single json.RawMessage
	if err := json.Unmarshal(body, &single); err == nil && len(single) > 0 {
		return []json.RawMessage{single}, nil
	}
	return nil, fmt.Errorf("unsupported replay recording payload")
}

func parseReplayTimelineItem(raw json.RawMessage, hint replayReceiptHint, asset store.ReplayAssetRef, index int) (store.ReplayTimelineItem, bool) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return store.ReplayTimelineItem{}, false
	}
	kind := replayTimelineKind(payload)
	if kind == "" {
		return store.ReplayTimelineItem{}, false
	}
	tsMS := replayTimelineTSMS(payload, hint.OccurredAt)
	item := store.ReplayTimelineItem{
		ReplayID:   asset.ReplayID,
		TSMS:       tsMS,
		ItemIndex:  index,
		Kind:       kind,
		Pane:       replayPaneForKind(kind),
		PayloadRef: asset.ObjectKey,
		MetaJSON:   compactReplayMeta(payload),
	}
	data := firstJSONObject(firstNonNil(payload["data"], payload["payload"], payload["message"], payload["meta"]))
	item.Title = firstNonEmptyText(stringFromAny(payload["title"]), stringFromAny(data["title"]))
	item.Level = firstNonEmptyText(strings.ToLower(stringFromAny(payload["level"])), strings.ToLower(stringFromAny(data["level"])))
	item.Message = firstNonEmptyText(stringFromAny(payload["message"]), stringFromAny(data["message"]), stringFromAny(data["text"]))
	item.URL = firstNonEmptyText(stringFromAny(payload["url"]), stringFromAny(data["url"]), stringFromAny(data["to"]), stringFromAny(data["href"]))
	item.Method = strings.ToUpper(firstNonEmptyText(stringFromAny(payload["method"]), stringFromAny(data["method"])))
	item.StatusCode = intValue(firstNonNil(payload["status_code"], payload["statusCode"], data["status_code"], data["statusCode"]))
	item.DurationMS = int64Value(firstNonNil(payload["duration_ms"], payload["durationMs"], data["duration_ms"], data["durationMs"]))
	item.Selector = firstNonEmptyText(stringFromAny(payload["selector"]), stringFromAny(data["selector"]), stringFromAny(data["target"]))
	item.Text = firstNonEmptyText(stringFromAny(payload["text"]), stringFromAny(data["text"]), stringFromAny(data["label"]))
	item.TraceID = firstNonEmptyText(stringFromAny(payload["trace_id"]), stringFromAny(payload["traceId"]), stringFromAny(data["trace_id"]), stringFromAny(data["traceId"]))
	item.LinkedEventID = firstNonEmptyText(stringFromAny(payload["event_id"]), stringFromAny(payload["linked_event_id"]), stringFromAny(data["event_id"]), stringFromAny(data["linked_event_id"]))
	if item.Kind == "snapshot" && item.Title == "" {
		item.Title = "DOM snapshot"
	}
	if item.Kind == "navigation" && item.Title == "" {
		item.Title = firstNonEmptyText(item.URL, "Navigation")
	}
	if item.Kind == "click" && item.Title == "" {
		item.Title = firstNonEmptyText(item.Text, item.Selector, "Click")
	}
	if item.Kind == "network" && item.Title == "" {
		item.Title = firstNonEmptyText(item.Method+" "+item.URL, item.URL, "Network")
	}
	if item.Kind == "console" && item.Title == "" {
		item.Title = firstNonEmptyText(item.Message, "Console")
	}
	if item.Kind == "error" && item.Title == "" {
		item.Title = firstNonEmptyText(item.Message, item.LinkedEventID, "Error")
	}
	item.ID = replayTimelineAnchor(asset, item)
	return item, true
}

func replayTimelineKind(payload map[string]any) string {
	candidates := []string{
		strings.ToLower(stringFromAny(payload["kind"])),
		strings.ToLower(stringFromAny(payload["type"])),
		strings.ToLower(stringFromAny(payload["category"])),
		strings.ToLower(stringFromAny(payload["source"])),
	}
	data := firstJSONObject(firstNonNil(payload["data"], payload["payload"], payload["message"], payload["meta"]))
	candidates = append(candidates,
		strings.ToLower(stringFromAny(data["kind"])),
		strings.ToLower(stringFromAny(data["type"])),
		strings.ToLower(stringFromAny(data["category"])),
	)
	joined := strings.Join(candidates, " ")
	switch {
	case strings.Contains(joined, "snapshot"):
		return "snapshot"
	case strings.Contains(joined, "console"):
		return "console"
	case strings.Contains(joined, "network") || strings.Contains(joined, "fetch") || strings.Contains(joined, "xhr") || strings.Contains(joined, "request"):
		return "network"
	case strings.Contains(joined, "click") || strings.Contains(joined, "tap"):
		return "click"
	case strings.Contains(joined, "navigation") || strings.Contains(joined, "route"):
		return "navigation"
	case strings.Contains(joined, "error") || strings.Contains(joined, "exception"):
		return "error"
	default:
		return ""
	}
}

func replayTimelineTSMS(payload map[string]any, startedAt time.Time) int64 {
	for _, key := range []string{"ts_ms", "offset_ms", "offsetMs"} {
		if value, ok := int64FromAny(payload[key]); ok {
			return maxInt64(0, value)
		}
	}
	for _, key := range []string{"timestamp", "ts"} {
		if value, ok := int64FromAny(payload[key]); ok {
			return normalizeReplayTimestamp(value, startedAt)
		}
	}
	data := firstJSONObject(firstNonNil(payload["data"], payload["payload"], payload["message"], payload["meta"]))
	for _, key := range []string{"offset_ms", "offsetMs", "timestamp", "ts"} {
		if value, ok := int64FromAny(data[key]); ok {
			return normalizeReplayTimestamp(value, startedAt)
		}
	}
	return 0
}

func normalizeReplayTimestamp(value int64, startedAt time.Time) int64 {
	if value <= 0 {
		return 0
	}
	if value >= 1_000_000_000_000 {
		if startedAt.IsZero() {
			return value
		}
		return maxInt64(0, value-startedAt.UnixMilli())
	}
	return value
}

func replayPaneForKind(kind string) string {
	switch kind {
	case "console":
		return "console"
	case "network":
		return "network"
	case "click":
		return "clicks"
	case "error":
		return "errors"
	default:
		return "timeline"
	}
}

func replayAssetKind(name, contentType string) string {
	lowerName := strings.ToLower(strings.TrimSpace(name))
	lowerType := strings.ToLower(strings.TrimSpace(contentType))
	switch {
	case strings.Contains(lowerName, "snapshot"):
		return "snapshot"
	case strings.Contains(lowerName, "video") || strings.Contains(lowerType, "video/"):
		return "video"
	case strings.Contains(lowerName, "recording") || strings.Contains(lowerName, ".rrweb") || strings.Contains(lowerType, "json"):
		return "recording"
	default:
		return "asset"
	}
}

func replayChunkIndex(name string) int {
	lower := strings.ToLower(name)
	for _, marker := range []string{"segment-", "chunk-", "part-"} {
		idx := strings.Index(lower, marker)
		if idx < 0 {
			continue
		}
		start := idx + len(marker)
		end := start
		for end < len(lower) && lower[end] >= '0' && lower[end] <= '9' {
			end++
		}
		if value, err := strconv.Atoi(lower[start:end]); err == nil {
			return value
		}
	}
	return 0
}

func compactReplayMeta(payload map[string]any) json.RawMessage {
	body, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(body)
}

func replayTimelineAnchor(asset store.ReplayAssetRef, item store.ReplayTimelineItem) string {
	sum := sha1.Sum([]byte(strings.Join([]string{
		asset.ReplayID,
		asset.AttachmentID,
		item.Kind,
		strconv.FormatInt(item.TSMS, 10),
		item.Title,
		item.Message,
		item.URL,
		item.Selector,
		item.LinkedEventID,
		item.TraceID,
	}, "\x00")))
	return fmt.Sprintf("rt_%x", sum[:10])
}
