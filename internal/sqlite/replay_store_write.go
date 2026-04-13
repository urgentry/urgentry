package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/store"
)

func (s *ReplayStore) SaveEnvelopeReplay(ctx context.Context, projectID, fallbackEventID string, payload []byte) (string, error) {
	hint, parseErr := parseReplayReceiptHint(payload, fallbackEventID)
	hint.EventID = firstNonEmptyText(hint.EventID, fallbackEventID, generateID())
	hint.ReplayID = firstNonEmptyText(hint.ReplayID, hint.EventID)
	hint.Platform = firstNonEmptyText(hint.Platform, "javascript")
	if hint.OccurredAt.IsZero() {
		hint.OccurredAt = time.Now().UTC()
	}
	receipt := replayReceiptEvent(projectID, hint, payload)
	events := NewEventStore(s.db)
	existing, err := events.GetEventByType(ctx, projectID, hint.ReplayID, "replay")
	if err == nil && existing != nil {
		receipt.ID = existing.ID
	} else if err != nil && err != store.ErrNotFound {
		return "", fmt.Errorf("load replay receipt: %w", err)
	}
	if receipt.ID == "" {
		receipt.ID = generateID()
	}
	if err := events.UpsertEvent(ctx, receipt); err != nil {
		return "", fmt.Errorf("save replay receipt: %w", err)
	}
	status := store.ReplayProcessingStatusPartial
	ingestError := ""
	if parseErr != nil {
		status = store.ReplayProcessingStatusFailed
		ingestError = parseErr.Error()
	}
	if err := s.upsertManifest(ctx, receipt, hint, nil, nil, status, ingestError); err != nil {
		return "", err
	}
	if parseErr != nil {
		if err := events.UpdateProcessingStatus(ctx, receipt.ID, store.EventProcessingStatusFailed, ingestError); err != nil {
			return "", fmt.Errorf("mark replay receipt failed: %w", err)
		}
	}
	return hint.EventID, nil
}

func (s *ReplayStore) upsertManifest(ctx context.Context, evt *store.StoredEvent, hint replayReceiptHint, assets []store.ReplayAssetRef, timeline []store.ReplayTimelineItem, status store.ReplayProcessingStatus, ingestError string) error {
	if evt == nil {
		return store.ErrNotFound
	}
	manifestID, err := s.lookupReplayManifestID(ctx, evt.ProjectID, firstNonEmptyText(hint.ReplayID, evt.EventID), evt.ID)
	if err != nil {
		return err
	}
	if manifestID == "" {
		manifestID = generateID()
	}
	existingManifest := manifestID != ""
	manifest := replayManifestFromEvent(evt, hint, assets, timeline, status, ingestError)
	manifest.ID = manifestID
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replay manifest tx: %w", err)
	}
	userJSON, _ := json.Marshal(manifest.UserRef)
	traceIDsJSON, _ := json.Marshal(manifest.TraceIDs)
	linkedEventsJSON, _ := json.Marshal(manifest.LinkedEventIDs)
	linkedIssuesJSON, _ := json.Marshal(manifest.LinkedIssueIDs)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO replay_manifests
			(id, event_row_id, project_id, replay_id, platform, release, environment,
			 started_at, ended_at, duration_ms, request_url, user_ref_json, trace_ids_json,
			 linked_event_ids_json, linked_issue_ids_json, asset_count, console_count,
			 network_count, click_count, navigation_count, error_marker_count, timeline_start_ms,
			 timeline_end_ms, privacy_policy_version, processing_status, ingest_error, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, replay_id) DO UPDATE SET
			event_row_id = excluded.event_row_id,
			platform = excluded.platform,
			release = excluded.release,
			environment = excluded.environment,
			started_at = excluded.started_at,
			ended_at = excluded.ended_at,
			duration_ms = excluded.duration_ms,
			request_url = excluded.request_url,
			user_ref_json = excluded.user_ref_json,
			trace_ids_json = excluded.trace_ids_json,
			linked_event_ids_json = excluded.linked_event_ids_json,
			linked_issue_ids_json = excluded.linked_issue_ids_json,
			asset_count = excluded.asset_count,
			console_count = excluded.console_count,
			network_count = excluded.network_count,
			click_count = excluded.click_count,
			navigation_count = excluded.navigation_count,
			error_marker_count = excluded.error_marker_count,
			timeline_start_ms = excluded.timeline_start_ms,
			timeline_end_ms = excluded.timeline_end_ms,
			privacy_policy_version = excluded.privacy_policy_version,
			processing_status = excluded.processing_status,
			ingest_error = excluded.ingest_error,
			updated_at = excluded.updated_at`,
		manifest.ID, manifest.EventRowID, manifest.ProjectID, manifest.ReplayID, manifest.Platform, manifest.Release,
		manifest.Environment, formatOptionalTime(manifest.StartedAt), formatOptionalTime(manifest.EndedAt),
		manifest.DurationMS, manifest.RequestURL, string(userJSON), string(traceIDsJSON), string(linkedEventsJSON),
		string(linkedIssuesJSON), manifest.AssetCount, manifest.ConsoleCount, manifest.NetworkCount, manifest.ClickCount,
		manifest.NavigationCount, manifest.ErrorMarkerCount, manifest.TimelineStartMS, manifest.TimelineEndMS,
		manifest.PrivacyPolicyVersion, string(manifest.ProcessingStatus), manifest.IngestError, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("upsert replay manifest: %w", err)
	}
	if existingManifest {
		if _, err := tx.ExecContext(ctx, `DELETE FROM replay_assets WHERE manifest_id = ?`, manifest.ID); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("clear replay assets: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM replay_timeline_items WHERE manifest_id = ?`, manifest.ID); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("clear replay timeline: %w", err)
		}
	}
	for _, asset := range assets {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO replay_assets
				(id, manifest_id, replay_id, attachment_id, kind, name, content_type, size_bytes, object_key, chunk_index, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			asset.ID, manifest.ID, manifest.ReplayID, asset.AttachmentID, asset.Kind, asset.Name,
			asset.ContentType, asset.SizeBytes, asset.ObjectKey, asset.ChunkIndex, asset.CreatedAt.UTC().Format(time.RFC3339),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("insert replay asset: %w", err)
		}
	}
	for _, item := range timeline {
		metaJSON := item.MetaJSON
		if len(metaJSON) == 0 {
			metaJSON = json.RawMessage(`{}`)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO replay_timeline_items
				(id, manifest_id, replay_id, ts_ms, item_index, kind, pane_ref, title, level, message, url,
				 method, status_code, duration_ms, selector, text_value, trace_id, linked_event_id, linked_issue_id,
				 payload_ref, meta_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID, manifest.ID, manifest.ReplayID, item.TSMS, item.ItemIndex, item.Kind, item.Pane, item.Title,
			item.Level, item.Message, item.URL, item.Method, item.StatusCode, item.DurationMS, item.Selector,
			item.Text, item.TraceID, item.LinkedEventID, item.LinkedIssueID, item.PayloadRef, string(metaJSON),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("insert replay timeline item: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replay manifest tx: %w", err)
	}
	return nil
}

func (s *ReplayStore) lookupReplayManifestID(ctx context.Context, projectID, replayID, eventRowID string) (string, error) {
	var manifestID string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM replay_manifests WHERE project_id = ? AND (replay_id = ? OR event_row_id = ?)`,
		projectID, replayID, eventRowID,
	).Scan(&manifestID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup replay manifest: %w", err)
	}
	return manifestID, nil
}

func replayReceiptEvent(projectID string, hint replayReceiptHint, payload []byte) *store.StoredEvent {
	title := "Session replay"
	if hint.RequestURL != "" {
		title = fmt.Sprintf("Replay of %s", hint.RequestURL)
	}
	tags := map[string]string{}
	if hint.ReplayID != "" {
		tags["replay_id"] = hint.ReplayID
	}
	if hint.Release != "" {
		tags["release"] = hint.Release
	}
	if hint.Environment != "" {
		tags["environment"] = hint.Environment
	}
	for _, traceID := range hint.TraceIDs {
		if traceID != "" {
			tags["trace_id"] = traceID
			break
		}
	}
	userIdentifier := firstNonEmptyText(hint.User.ID, hint.User.Email, hint.User.Username)
	return &store.StoredEvent{
		ProjectID:      projectID,
		EventID:        hint.EventID,
		ReleaseID:      hint.Release,
		Environment:    hint.Environment,
		Platform:       hint.Platform,
		Level:          "info",
		EventType:      "replay",
		OccurredAt:     hint.OccurredAt,
		IngestedAt:     time.Now().UTC(),
		Message:        "Session replay",
		Title:          title,
		Culprit:        hint.RequestURL,
		Tags:           tags,
		NormalizedJSON: json.RawMessage(payload),
		UserIdentifier: userIdentifier,
	}
}

func replayManifestFromEvent(evt *store.StoredEvent, hint replayReceiptHint, assets []store.ReplayAssetRef, timeline []store.ReplayTimelineItem, status store.ReplayProcessingStatus, ingestError string) store.ReplayManifest {
	manifest := store.ReplayManifest{
		EventRowID:           evt.ID,
		ProjectID:            evt.ProjectID,
		ReplayID:             firstNonEmptyText(hint.ReplayID, evt.EventID),
		Platform:             firstNonEmptyText(hint.Platform, evt.Platform),
		Release:              firstNonEmptyText(hint.Release, evt.ReleaseID),
		Environment:          firstNonEmptyText(hint.Environment, evt.Environment),
		StartedAt:            firstNonZeroTime(hint.OccurredAt, evt.OccurredAt, evt.IngestedAt),
		RequestURL:           firstNonEmptyText(hint.RequestURL, evt.Culprit),
		UserRef:              hint.User,
		TraceIDs:             append([]string(nil), uniqueReplayStrings(hint.TraceIDs)...),
		AssetCount:           len(assets),
		PrivacyPolicyVersion: hint.PrivacyPolicyVersion,
		ProcessingStatus:     status,
		IngestError:          strings.TrimSpace(ingestError),
		CreatedAt:            evt.IngestedAt,
		UpdatedAt:            time.Now().UTC(),
	}
	if manifest.StartedAt.IsZero() {
		manifest.StartedAt = time.Now().UTC()
	}
	if len(timeline) > 0 {
		manifest.TimelineStartMS = timeline[0].TSMS
		manifest.TimelineEndMS = timeline[len(timeline)-1].TSMS
		manifest.DurationMS = maxInt64(0, manifest.TimelineEndMS-manifest.TimelineStartMS)
		if !manifest.StartedAt.IsZero() {
			manifest.EndedAt = manifest.StartedAt.Add(time.Duration(manifest.TimelineEndMS) * time.Millisecond)
		}
	}
	linkedEvents := make([]string, 0, len(timeline))
	linkedIssues := make([]string, 0, len(timeline))
	traceIDs := append([]string(nil), manifest.TraceIDs...)
	for _, item := range timeline {
		switch item.Kind {
		case "console":
			manifest.ConsoleCount++
		case "network":
			manifest.NetworkCount++
		case "click":
			manifest.ClickCount++
		case "navigation":
			manifest.NavigationCount++
		case "error":
			manifest.ErrorMarkerCount++
		}
		if item.LinkedEventID != "" {
			linkedEvents = append(linkedEvents, item.LinkedEventID)
		}
		if item.LinkedIssueID != "" {
			linkedIssues = append(linkedIssues, item.LinkedIssueID)
		}
		if item.TraceID != "" {
			traceIDs = append(traceIDs, item.TraceID)
		}
	}
	manifest.LinkedEventIDs = uniqueReplayStrings(linkedEvents)
	manifest.LinkedIssueIDs = uniqueReplayStrings(linkedIssues)
	manifest.TraceIDs = uniqueReplayStrings(traceIDs)
	return manifest
}
