package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	attachmentstore "urgentry/internal/attachment"
	"urgentry/internal/issue"
	"urgentry/internal/minidump"
	"urgentry/internal/normalize"
	"urgentry/internal/runtimeasync"
	sharedstore "urgentry/internal/store"
)

const (
	NativeCrashStatusPending    = "pending"
	NativeCrashStatusProcessing = "processing"
	NativeCrashStatusCompleted  = "completed"
	NativeCrashStatusFailed     = "failed"
)

var ErrNativeCrashQueueFull = errors.New("native stackwalk queue is full")

type NativeCrash struct {
	ID              string
	ProjectID       string
	EventID         string
	EventRowID      string
	ReleaseID       string
	Platform        string
	Status          string
	IngestError     string
	PayloadJSON     string
	RawAttachmentID string
	RawBlobKey      string
	Filename        string
	ContentType     string
	Size            int64
	Attempts        int
	ProcessedAt     time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type MinidumpReceiptInput struct {
	ProjectID   string
	EventID     string
	Filename    string
	ContentType string
	Dump        []byte
	EventJSON   []byte
}

type nativeStackwalkJobPayload struct {
	CrashID string `json:"crashId"`
}

type NativeCrashStore struct {
	db          *sql.DB
	attachments *AttachmentStore
	jobs        runtimeasync.Queue
	debugFiles  *DebugFileStore
	queueLimit  int
}

type permanentNativeError struct {
	err error
}

func (e permanentNativeError) Error() string {
	return e.err.Error()
}

func (e permanentNativeError) Unwrap() error {
	return e.err
}

func (e permanentNativeError) Permanent() bool {
	return true
}

func NewNativeCrashStore(db *sql.DB, blobs sharedstore.BlobStore, jobs runtimeasync.Queue, queueLimit int) *NativeCrashStore {
	if queueLimit <= 0 {
		queueLimit = 1000
	}
	return &NativeCrashStore{
		db:          db,
		attachments: NewAttachmentStore(db, blobs),
		jobs:        jobs,
		debugFiles:  NewDebugFileStore(db, blobs),
		queueLimit:  queueLimit,
	}
}

func (s *NativeCrashStore) Get(ctx context.Context, crashID string) (*NativeCrash, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, event_id, event_row_id, release_version, platform, status, ingest_error,
		        payload_json, raw_attachment_id, raw_blob_key, filename, content_type, size_bytes, attempts,
		        COALESCE(processed_at, ''), COALESCE(created_at, ''), COALESCE(updated_at, '')
		   FROM native_crashes
		  WHERE id = ?`,
		crashID,
	)
	return scanNativeCrash(row)
}

func (s *NativeCrashStore) GetByEventID(ctx context.Context, projectID, eventID string) (*NativeCrash, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, event_id, event_row_id, release_version, platform, status, ingest_error,
		        payload_json, raw_attachment_id, raw_blob_key, filename, content_type, size_bytes, attempts,
		        COALESCE(processed_at, ''), COALESCE(created_at, ''), COALESCE(updated_at, '')
		   FROM native_crashes
		  WHERE project_id = ? AND event_id = ?`,
		projectID, eventID,
	)
	return scanNativeCrash(row)
}

func (s *NativeCrashStore) IngestMinidump(ctx context.Context, in MinidumpReceiptInput) (*NativeCrash, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, errors.New("native crash store is not configured")
	}
	if s.attachments == nil || s.jobs == nil || s.debugFiles == nil {
		return nil, false, errors.New("native crash dependencies are not configured")
	}
	if strings.TrimSpace(in.ProjectID) == "" || strings.TrimSpace(in.EventID) == "" {
		return nil, false, errors.New("native crash project_id and event_id are required")
	}
	if len(in.Dump) == 0 {
		return nil, false, errors.New("native crash dump is required")
	}
	evt, err := normalize.Normalize(in.EventJSON)
	if err != nil {
		return nil, false, fmt.Errorf("normalize native receipt: %w", err)
	}
	normalizedPayload, err := json.Marshal(evt)
	if err != nil {
		return nil, false, fmt.Errorf("marshal native receipt: %w", err)
	}
	canonicalEventID := strings.TrimSpace(evt.EventID)
	if canonicalEventID == "" {
		return nil, false, errors.New("normalized native event_id is required")
	}
	parsedDump, _ := minidump.Parse(in.Dump)

	now := time.Now().UTC()
	crash := &NativeCrash{
		ID:          generateID(),
		ProjectID:   in.ProjectID,
		EventID:     canonicalEventID,
		ReleaseID:   evt.Release,
		Platform:    evt.Platform,
		Status:      NativeCrashStatusPending,
		PayloadJSON: string(normalizedPayload),
		Filename:    strings.TrimSpace(in.Filename),
		ContentType: strings.TrimSpace(in.ContentType),
		Size:        int64(len(in.Dump)),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if crash.Filename == "" {
		crash.Filename = "crash.dmp"
	}

	created, err := s.insertNativeCrash(ctx, crash)
	if err != nil {
		return nil, false, err
	}
	if !created {
		existing, err := s.GetByEventID(ctx, in.ProjectID, canonicalEventID)
		if err != nil || existing == nil {
			return existing, false, err
		}
		switch existing.Status {
		case NativeCrashStatusCompleted:
			return existing, false, nil
		case NativeCrashStatusFailed:
			if err := s.restageNativeCrash(ctx, existing, evt, normalizedPayload, in, parsedDump); err != nil {
				return nil, false, err
			}
			updated, err := s.Get(ctx, existing.ID)
			return updated, false, err
		default:
			if err := s.ensureNativeStackwalkJob(ctx, existing.ID, existing.ProjectID); err != nil {
				return nil, false, err
			}
			return existing, false, nil
		}
	}

	if err := s.storeRawMinidumpAttachment(ctx, crash, in.Dump); err != nil {
		s.cleanupNativeCrashIngest(ctx, crash, "", nil)
		return nil, false, fmt.Errorf("save raw minidump: %w", err)
	}
	if err := s.refreshNativeCrashImages(ctx, crash, in.EventJSON, parsedDump); err != nil {
		s.cleanupNativeCrashIngest(ctx, crash, "", nil)
		return nil, false, err
	}

	receipt, err := s.savePendingNativeEvent(ctx, crash, evt, normalizedPayload)
	if err != nil {
		s.cleanupNativeCrashIngest(ctx, crash, "", nil)
		return nil, false, err
	}
	crash.EventRowID = receipt.ID
	if err := s.bindNativeCrashEvent(ctx, crash.ID, receipt.ID); err != nil {
		s.cleanupNativeCrashIngest(ctx, crash, receipt.ID, nil)
		return nil, false, err
	}
	if err := s.ensureNativeStackwalkJob(ctx, crash.ID, crash.ProjectID); err != nil {
		s.cleanupNativeCrashIngest(ctx, crash, receipt.ID, nil)
		if errors.Is(err, ErrNativeCrashQueueFull) {
			return nil, false, err
		}
		return nil, false, err
	}
	return crash, true, nil
}

func (s *NativeCrashStore) ProcessStackwalkJob(ctx context.Context, processor *issue.Processor, _ string, payload []byte) error {
	if s == nil || s.db == nil {
		return errors.New("native crash store is not configured")
	}
	if processor == nil {
		return errors.New("issue processor is not configured")
	}
	var job nativeStackwalkJobPayload
	if err := json.Unmarshal(payload, &job); err != nil {
		return permanentNativeError{err: fmt.Errorf("invalid native stackwalk job payload: %w", err)}
	}
	crash, err := s.Get(ctx, strings.TrimSpace(job.CrashID))
	if err != nil {
		return err
	}
	if crash == nil {
		return nil
	}
	if crash.Status == NativeCrashStatusCompleted {
		return nil
	}
	receipt, err := NewEventStore(s.db).GetEventByRowID(ctx, crash.EventRowID)
	if err == nil && receipt != nil && receipt.ProcessingStatus == sharedstore.EventProcessingStatusCompleted && strings.TrimSpace(receipt.GroupID) != "" {
		return s.markNativeCrashCompleted(ctx, crash)
	}
	if err != nil && !errors.Is(err, sharedstore.ErrNotFound) {
		return err
	}

	if err := s.markNativeCrashProcessing(ctx, crash); err != nil {
		return err
	}

	attachmentMeta, dump, err := s.attachments.GetAttachment(ctx, crash.RawAttachmentID)
	if err != nil {
		_ = s.markNativeCrashPending(ctx, crash, fmt.Sprintf("load raw minidump: %v", err))
		return err
	}
	if attachmentMeta == nil || len(dump) == 0 {
		return s.failNativeCrashPermanent(ctx, crash, "missing raw minidump payload")
	}
	parsedDump, err := minidump.Parse(dump)
	if err != nil {
		return s.failNativeCrashPermanent(ctx, crash, fmt.Sprintf("malformed minidump payload: %v", err))
	}

	images, err := s.debugFiles.ListNativeCrashImages(ctx, crash.ProjectID, crash.EventID)
	if err != nil {
		_ = s.markNativeCrashPending(ctx, crash, fmt.Sprintf("load native crash images: %v", err))
		return err
	}
	eventPayload, err := s.buildStackwalkPayload(crash, images, parsedDump)
	if err != nil {
		return s.failNativeCrashPermanent(ctx, crash, err.Error())
	}
	if _, err := processor.ProcessExisting(ctx, crash.ProjectID, eventPayload, crash.EventRowID); err != nil {
		_ = s.markNativeCrashPending(ctx, crash, fmt.Sprintf("process native event: %v", err))
		return err
	}
	if err := s.markNativeCrashCompleted(ctx, crash); err != nil {
		return err
	}
	return nil
}

func (s *NativeCrashStore) buildStackwalkPayload(crash *NativeCrash, images []NativeCrashImage, dump *minidump.File) ([]byte, error) {
	if crash == nil || strings.TrimSpace(crash.PayloadJSON) == "" {
		return nil, fmt.Errorf("missing native crash payload")
	}
	evt, err := normalize.Normalize([]byte(crash.PayloadJSON))
	if err != nil {
		return nil, fmt.Errorf("parse stored native payload: %w", err)
	}
	injectNativeFrames(evt, images, dump)
	body, err := json.Marshal(evt)
	if err != nil {
		return nil, fmt.Errorf("marshal stackwalk payload: %w", err)
	}
	return body, nil
}

func injectNativeFrames(evt *normalize.Event, images []NativeCrashImage, dump *minidump.File) {
	if evt == nil {
		return
	}
	if evt.Exception == nil {
		evt.Exception = &normalize.ExceptionList{}
	}
	if len(evt.Exception.Values) == 0 {
		handled := false
		evt.Exception.Values = []normalize.Exception{{
			Type:  "Minidump",
			Value: "Native crash",
			Mechanism: &normalize.Mechanism{
				Type:    "minidump",
				Handled: &handled,
			},
		}}
	}
	target := &evt.Exception.Values[0]
	if target.Stacktrace == nil {
		target.Stacktrace = &normalize.Stacktrace{}
	}

	frames := buildNativeFrames(images, dump)
	if len(frames) > 0 {
		target.Stacktrace.Frames = frames
	}
}

func (s *NativeCrashStore) savePendingNativeEvent(ctx context.Context, crash *NativeCrash, evt *normalize.Event, payload []byte) (*sharedstore.StoredEvent, error) {
	receipt := &sharedstore.StoredEvent{
		ID:               firstNonEmptyText(crash.EventRowID, generateID()),
		ProjectID:        crash.ProjectID,
		EventID:          crash.EventID,
		ReleaseID:        evt.Release,
		Environment:      evt.Environment,
		Platform:         evt.Platform,
		Level:            evt.Level,
		EventType:        evt.EventType(),
		OccurredAt:       evt.Timestamp,
		IngestedAt:       time.Now().UTC(),
		Message:          evt.Message,
		Title:            evt.Title(),
		Culprit:          evt.Culprit(),
		Tags:             evt.Tags,
		NormalizedJSON:   json.RawMessage(payload),
		ProcessingStatus: sharedstore.EventProcessingStatusPending,
		IngestError:      "",
	}
	if receipt.OccurredAt.IsZero() {
		receipt.OccurredAt = receipt.IngestedAt
	}
	if err := NewEventStore(s.db).UpsertEvent(ctx, receipt); err != nil {
		return nil, fmt.Errorf("save pending native event: %w", err)
	}
	return receipt, nil
}

func (s *NativeCrashStore) insertNativeCrash(ctx context.Context, crash *NativeCrash) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO native_crashes
			(id, project_id, event_id, event_row_id, release_version, platform, status, ingest_error, payload_json,
			 raw_attachment_id, raw_blob_key, filename, content_type, size_bytes, attempts, processed_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)`,
		crash.ID, crash.ProjectID, crash.EventID, crash.EventRowID, crash.ReleaseID, crash.Platform, crash.Status, crash.IngestError, crash.PayloadJSON,
		crash.RawAttachmentID, crash.RawBlobKey, crash.Filename, crash.ContentType, crash.Size, crash.Attempts, formatOptionalCrashTime(crash.ProcessedAt), crash.CreatedAt.UTC().Format(time.RFC3339), crash.UpdatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return false, fmt.Errorf("insert native crash: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("insert native crash rows affected: %w", err)
	}
	return affected == 1, nil
}

func (s *NativeCrashStore) bindNativeCrashAttachment(ctx context.Context, crashID, attachmentID, objectKey, contentType string, size int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE native_crashes
		    SET raw_attachment_id = ?, raw_blob_key = ?, content_type = ?, size_bytes = ?, updated_at = ?
		  WHERE id = ?`,
		attachmentID, objectKey, contentType, size, time.Now().UTC().Format(time.RFC3339), crashID,
	)
	if err != nil {
		return fmt.Errorf("bind native crash attachment: %w", err)
	}
	return nil
}

func (s *NativeCrashStore) bindNativeCrashEvent(ctx context.Context, crashID, eventRowID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE native_crashes
		    SET event_row_id = ?, updated_at = ?
		  WHERE id = ?`,
		eventRowID, time.Now().UTC().Format(time.RFC3339), crashID,
	)
	if err != nil {
		return fmt.Errorf("bind native crash event: %w", err)
	}
	return nil
}

func (s *NativeCrashStore) markNativeCrashProcessing(ctx context.Context, crash *NativeCrash) error {
	return s.transitionNativeCrash(ctx, crash.ID, crash.EventRowID, NativeCrashStatusProcessing, sharedstore.EventProcessingStatusProcessing, "", true, time.Time{})
}

func (s *NativeCrashStore) markNativeCrashPending(ctx context.Context, crash *NativeCrash, reason string) error {
	return s.transitionNativeCrash(ctx, crash.ID, crash.EventRowID, NativeCrashStatusPending, sharedstore.EventProcessingStatusPending, reason, false, time.Time{})
}

func (s *NativeCrashStore) markNativeCrashCompleted(ctx context.Context, crash *NativeCrash) error {
	return s.transitionNativeCrash(ctx, crash.ID, crash.EventRowID, NativeCrashStatusCompleted, sharedstore.EventProcessingStatusCompleted, "", false, time.Now().UTC())
}

func (s *NativeCrashStore) failNativeCrashPermanent(ctx context.Context, crash *NativeCrash, reason string) error {
	if err := s.transitionNativeCrash(ctx, crash.ID, crash.EventRowID, NativeCrashStatusFailed, sharedstore.EventProcessingStatusFailed, reason, false, time.Time{}); err != nil {
		return err
	}
	return permanentNativeError{err: errors.New(strings.TrimSpace(reason))}
}

func (s *NativeCrashStore) restageNativeCrash(ctx context.Context, crash *NativeCrash, evt *normalize.Event, payload []byte, in MinidumpReceiptInput, parsedDump *minidump.File) error {
	crash.PayloadJSON = string(payload)
	crash.ReleaseID = evt.Release
	crash.Platform = evt.Platform
	crash.Filename = firstNonEmptyText(strings.TrimSpace(in.Filename), crash.Filename, "crash.dmp")
	crash.ContentType = firstNonEmptyText(strings.TrimSpace(in.ContentType), crash.ContentType)
	crash.Size = int64(len(in.Dump))
	if err := s.storeRawMinidumpAttachment(ctx, crash, in.Dump); err != nil {
		return err
	}
	if err := s.refreshNativeCrashImages(ctx, crash, in.EventJSON, parsedDump); err != nil {
		return err
	}
	receipt, err := s.savePendingNativeEvent(ctx, crash, evt, payload)
	if err != nil {
		return err
	}
	if crash.EventRowID == "" || crash.EventRowID != receipt.ID {
		crash.EventRowID = receipt.ID
		if err := s.bindNativeCrashEvent(ctx, crash.ID, receipt.ID); err != nil {
			return err
		}
	}
	if err := s.transitionNativeCrash(ctx, crash.ID, crash.EventRowID, NativeCrashStatusPending, sharedstore.EventProcessingStatusPending, "", false, time.Time{}); err != nil {
		return err
	}
	return s.ensureNativeStackwalkJob(ctx, crash.ID, crash.ProjectID)
}

func (s *NativeCrashStore) storeRawMinidumpAttachment(ctx context.Context, crash *NativeCrash, dump []byte) error {
	att := &attachmentstore.Attachment{
		ID:          crash.RawAttachmentID,
		EventID:     crash.EventID,
		ProjectID:   crash.ProjectID,
		Name:        firstNonEmptyText(crash.Filename, "crash.dmp"),
		ContentType: crash.ContentType,
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.attachments.SaveAttachment(ctx, att, dump); err != nil {
		return err
	}
	crash.RawAttachmentID = att.ID
	crash.RawBlobKey = att.ObjectKey
	return s.bindNativeCrashAttachment(ctx, crash.ID, att.ID, att.ObjectKey, crash.ContentType, int64(len(dump)))
}

func (s *NativeCrashStore) refreshNativeCrashImages(ctx context.Context, crash *NativeCrash, eventPayload []byte, parsedDump *minidump.File) error {
	if err := rebuildNativeCrashImagesWithQuerier(ctx, s.db, crash.ProjectID, crash.EventID, eventPayload); err != nil {
		return err
	}
	if parsedDump != nil {
		if err := mergeMinidumpCrashImagesWithQuerier(ctx, s.db, crash.ProjectID, crash.EventID, crash.ReleaseID, crash.Platform, parsedDump); err != nil {
			return err
		}
	}
	return nil
}

func (s *NativeCrashStore) ensureNativeStackwalkJob(ctx context.Context, crashID, projectID string) error {
	if strings.TrimSpace(crashID) == "" {
		return errors.New("native stackwalk crash id is required")
	}
	jobPayload, err := json.Marshal(nativeStackwalkJobPayload{CrashID: crashID})
	if err != nil {
		return fmt.Errorf("marshal native stackwalk job: %w", err)
	}
	var ok bool
	if keyed, okKeyed := s.jobs.(runtimeasync.KeyedEnqueuer); okKeyed {
		ok, err = keyed.EnqueueKeyed(ctx, JobKindNativeStackwalk, projectID, crashID, jobPayload, s.queueLimit)
	} else {
		exists, existsErr := s.hasNativeStackwalkJob(ctx, crashID)
		if existsErr != nil {
			return existsErr
		}
		if exists {
			return nil
		}
		ok, err = s.jobs.Enqueue(ctx, JobKindNativeStackwalk, projectID, jobPayload, s.queueLimit)
	}
	if err != nil {
		return err
	}
	if !ok {
		return ErrNativeCrashQueueFull
	}
	return nil
}

func (s *NativeCrashStore) hasNativeStackwalkJob(ctx context.Context, crashID string) (bool, error) {
	if _, ok := s.jobs.(*JobStore); !ok {
		return false, nil
	}
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*)
		   FROM jobs
		  WHERE kind = ?
		    AND status IN ('pending', 'processing')
		    AND COALESCE(json_extract(payload, '$.crashId'), '') = ?`,
		JobKindNativeStackwalk, crashID,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("count native stackwalk jobs: %w", err)
	}
	return count > 0, nil
}

func (s *NativeCrashStore) transitionNativeCrash(ctx context.Context, crashID, eventRowID, crashStatus string, eventStatus sharedstore.EventProcessingStatus, ingestError string, incrementAttempts bool, processedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin native crash transition: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	setAttempts := ""
	if incrementAttempts {
		setAttempts = ", attempts = attempts + 1"
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE native_crashes
		    SET status = ?, ingest_error = ?, processed_at = NULLIF(?, ''), updated_at = ?`+setAttempts+`
		  WHERE id = ?`,
		crashStatus,
		strings.TrimSpace(ingestError),
		formatOptionalCrashTime(processedAt),
		time.Now().UTC().Format(time.RFC3339),
		crashID,
	); err != nil {
		return fmt.Errorf("update native crash status: %w", err)
	}
	if strings.TrimSpace(eventRowID) != "" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE events
			    SET processing_status = ?, ingest_error = ?
			  WHERE id = ?`,
			string(eventStatus), strings.TrimSpace(ingestError), eventRowID,
		); err != nil {
			return fmt.Errorf("update native event status: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit native crash transition: %w", err)
	}
	return nil
}

func buildNativeFrames(images []NativeCrashImage, dump *minidump.File) []normalize.Frame {
	if len(images) == 0 && dump == nil {
		return nil
	}
	capHint := len(images)
	if capHint < 1 {
		capHint = 1
	}
	frames := make([]normalize.Frame, 0, capHint)
	used := map[int]struct{}{}
	if dump != nil && dump.Exception.Address != 0 {
		for i, image := range images {
			if !imageContainsAddress(image, dump.Exception.Address) {
				continue
			}
			frames = append(frames, nativeFrameFromImage(image, fmt.Sprintf("0x%x", dump.Exception.Address)))
			used[i] = struct{}{}
			break
		}
		if len(frames) == 0 {
			for _, module := range dump.Modules {
				if !containsAddress(module, dump.Exception.Address) {
					continue
				}
				frames = append(frames, normalize.Frame{
					InstructionAddr: strings.ToLower(fmt.Sprintf("0x%x", dump.Exception.Address)),
					Module:          strings.TrimSpace(module.Name),
				})
				break
			}
		}
	}
	for i, image := range images {
		if _, ok := used[i]; ok {
			continue
		}
		instructionAddr := firstNonEmptyText(image.InstructionAddr, image.ImageAddr)
		if strings.TrimSpace(instructionAddr) == "" {
			continue
		}
		frames = append(frames, nativeFrameFromImage(image, instructionAddr))
	}
	return frames
}

func nativeFrameFromImage(image NativeCrashImage, instructionAddr string) normalize.Frame {
	return normalize.Frame{
		InstructionAddr: strings.ToLower(strings.TrimSpace(instructionAddr)),
		DebugID:         image.DebugID,
		Package:         image.CodeID,
		Module:          firstNonEmptyText(image.ModuleName, image.ImageName),
	}
}

func imageContainsAddress(image NativeCrashImage, addr uint64) bool {
	base, ok := parseNativeAddress(image.ImageAddr)
	if !ok {
		return false
	}
	size, ok := parseNativeAddress(image.ImageSize)
	if !ok || size == 0 {
		return false
	}
	return addr >= base && addr < base+size
}

func parseNativeAddress(raw string) (uint64, bool) {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" {
		return 0, false
	}
	var addr uint64
	if _, err := fmt.Sscanf(value, "0x%x", &addr); err == nil {
		return addr, true
	}
	if _, err := fmt.Sscanf(value, "%x", &addr); err == nil {
		return addr, true
	}
	return 0, false
}

func (s *NativeCrashStore) cleanupNativeCrashIngest(ctx context.Context, crash *NativeCrash, eventRowID string, attachment *attachmentstore.Attachment) {
	if attachment != nil {
		if attachment.ObjectKey != "" && s.attachments != nil && s.attachments.blobs != nil {
			_ = s.attachments.blobs.Delete(ctx, attachment.ObjectKey)
		}
		_, _ = s.db.ExecContext(ctx, `DELETE FROM event_attachments WHERE id = ?`, attachment.ID)
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM native_crash_images WHERE project_id = ? AND event_id = ?`, crash.ProjectID, crash.EventID)
	_ = NewEventStore(s.db).DeleteEvent(ctx, eventRowID)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM native_crashes WHERE id = ?`, crash.ID)
}

func scanNativeCrash(scanner interface{ Scan(dest ...any) error }) (*NativeCrash, error) {
	var item NativeCrash
	var processedAt, createdAt, updatedAt string
	if err := scanner.Scan(
		&item.ID, &item.ProjectID, &item.EventID, &item.EventRowID, &item.ReleaseID, &item.Platform, &item.Status, &item.IngestError,
		&item.PayloadJSON, &item.RawAttachmentID, &item.RawBlobKey, &item.Filename, &item.ContentType, &item.Size, &item.Attempts,
		&processedAt, &createdAt, &updatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan native crash: %w", err)
	}
	item.ProcessedAt = parseTime(processedAt)
	item.CreatedAt = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)
	return &item, nil
}

func formatOptionalCrashTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
