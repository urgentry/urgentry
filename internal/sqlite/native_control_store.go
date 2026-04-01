package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	authpkg "urgentry/internal/auth"
	"urgentry/internal/normalize"
	sharedstore "urgentry/internal/store"
)

type NativeControlStore struct {
	db     *sql.DB
	blobs  sharedstore.BlobStore
	audits sharedstore.OperatorAuditStore
}

type NativeReleaseSummary struct {
	OrganizationID      string
	ReleaseVersion      string
	ProjectID           string
	TotalEvents         int
	PendingEvents       int
	ProcessingEvents    int
	CompletedEvents     int
	FailedEvents        int
	ResolvedFrames      int
	UnresolvedFrames    int
	LastError           string
	LastRunID           string
	LastRunStatus       string
	LastRunLastError    string
	LastRunUpdatedAt    time.Time
	LastRunProcessed    int
	LastRunUpdatedItems int
	LastRunFailedItems  int
}

type DebugFileProcessing struct {
	File                *DebugFile
	SymbolicationStatus string
	ReprocessRunID      string
	ReprocessStatus     string
	ReprocessLastError  string
	ReprocessUpdatedAt  time.Time
}

type CreateNativeReprocessRun struct {
	OrganizationID string
	ProjectID      string
	ReleaseVersion string
	DebugFileID    string
	StartedAfter   time.Time
	EndedBefore    time.Time
	Principal      *authpkg.Principal
	RequestedVia   string
	RequestPath    string
	RequestMethod  string
	IPAddress      string
	UserAgent      string
}

func NewNativeControlStore(db *sql.DB, blobs sharedstore.BlobStore, audits sharedstore.OperatorAuditStore) *NativeControlStore {
	if audits == nil && db != nil {
		audits = NewOperatorAuditStore(db)
	}
	return &NativeControlStore{db: db, blobs: blobs, audits: audits}
}

func (s *NativeControlStore) CreateRun(ctx context.Context, in CreateNativeReprocessRun) (*BackfillRun, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("native control store is not configured")
	}
	if strings.TrimSpace(in.OrganizationID) == "" {
		return nil, errors.New("organization id is required")
	}
	backfills := NewBackfillStore(s.db)
	var requestedByUserID string
	if in.Principal != nil && in.Principal.User != nil {
		requestedByUserID = in.Principal.User.ID
	}
	run, err := backfills.CreateRun(ctx, CreateBackfillRun{
		Kind:              BackfillKindNativeReprocess,
		OrganizationID:    strings.TrimSpace(in.OrganizationID),
		ProjectID:         strings.TrimSpace(in.ProjectID),
		ReleaseVersion:    strings.TrimSpace(in.ReleaseVersion),
		DebugFileID:       strings.TrimSpace(in.DebugFileID),
		StartedAfter:      in.StartedAfter,
		EndedBefore:       in.EndedBefore,
		RequestedByUserID: requestedByUserID,
		RequestedVia:      strings.TrimSpace(in.RequestedVia),
	})
	if err != nil {
		return nil, err
	}
	_ = NewAuditStore(s.db).Record(ctx, auditRecordForNativeRun("native.reprocess.requested", run, in))
	if s.audits != nil {
		_ = s.audits.Record(ctx, sharedstore.OperatorAuditRecord{
			OrganizationID: strings.TrimSpace(run.OrganizationID),
			ProjectID:      strings.TrimSpace(run.ProjectID),
			Action:         "native.reprocess.requested",
			Status:         "succeeded",
			Source:         firstNonEmptyString(strings.TrimSpace(in.RequestedVia), "system"),
			Actor:          operatorAuditActor(in.Principal),
			Detail:         "requested native reprocess",
			MetadataJSON:   buildNativeRunAuditMetadata(run, in),
		})
	}
	return run, nil
}

func (s *NativeControlStore) ReleaseSummary(ctx context.Context, organizationID, releaseVersion string) (NativeReleaseSummary, error) {
	summary := NativeReleaseSummary{
		OrganizationID: organizationID,
		ReleaseVersion: strings.TrimSpace(releaseVersion),
	}
	if s == nil || s.db == nil {
		return summary, errors.New("native control store is not configured")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.project_id, COALESCE(e.processing_status, 'completed'), COALESCE(e.ingest_error, ''), COALESCE(e.payload_json, '')
		   FROM events e
		   JOIN projects p ON p.id = e.project_id
		  WHERE p.organization_id = ?
		    AND COALESCE(e.release, '') = ?
		    AND COALESCE(e.event_type, 'error') = 'error'
		    AND (
		          instr(COALESCE(e.payload_json, ''), '"instruction_addr"') > 0
		       OR instr(COALESCE(e.payload_json, ''), '"debug_id"') > 0
		       OR instr(COALESCE(e.payload_json, ''), '"code_id"') > 0
		    )
		  ORDER BY COALESCE(e.ingested_at, e.occurred_at, '') DESC, e.id DESC`,
		organizationID, summary.ReleaseVersion,
	)
	if err != nil {
		return summary, fmt.Errorf("list native release events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var projectID, status, ingestError, payload string
		if err := rows.Scan(&projectID, &status, &ingestError, &payload); err != nil {
			return summary, fmt.Errorf("scan native release event: %w", err)
		}
		if summary.ProjectID == "" {
			summary.ProjectID = projectID
		}
		summary.TotalEvents++
		switch sharedstore.EventProcessingStatus(strings.TrimSpace(status)) {
		case sharedstore.EventProcessingStatusPending:
			summary.PendingEvents++
		case sharedstore.EventProcessingStatusProcessing:
			summary.ProcessingEvents++
		case sharedstore.EventProcessingStatusFailed:
			summary.FailedEvents++
		default:
			summary.CompletedEvents++
		}
		resolved, unresolved := normalize.CountNativeFrames(payload)
		summary.ResolvedFrames += resolved
		summary.UnresolvedFrames += unresolved
		if summary.LastError == "" && strings.TrimSpace(ingestError) != "" {
			summary.LastError = strings.TrimSpace(ingestError)
		}
	}
	if err := rows.Err(); err != nil {
		return summary, fmt.Errorf("iterate native release events: %w", err)
	}
	run, err := NewBackfillStore(s.db).LatestScopedRun(ctx, organizationID, "", summary.ReleaseVersion, "")
	if err != nil {
		return summary, err
	}
	if run != nil {
		summary.LastRunID = run.ID
		summary.LastRunStatus = string(run.Status)
		summary.LastRunLastError = run.LastError
		summary.LastRunUpdatedAt = run.UpdatedAt
		summary.LastRunProcessed = run.ProcessedItems
		summary.LastRunUpdatedItems = run.UpdatedItems
		summary.LastRunFailedItems = run.FailedItems
	}
	return summary, nil
}

func (s *NativeControlStore) ListReleaseDebugFiles(ctx context.Context, organizationID, projectID, releaseVersion string) ([]DebugFileProcessing, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("native control store is not configured")
	}
	if s.blobs == nil {
		return nil, errors.New("debug blob store is not configured")
	}
	debugFiles := NewDebugFileStore(s.db, s.blobs)
	files, err := debugFiles.ListByRelease(ctx, projectID, releaseVersion, "")
	if err != nil {
		return nil, err
	}
	backfills := NewBackfillStore(s.db)
	items := make([]DebugFileProcessing, 0, len(files))
	for _, file := range files {
		status, err := debugFiles.SymbolicationStatus(ctx, file)
		if err != nil {
			return nil, err
		}
		item := DebugFileProcessing{
			File:                file,
			SymbolicationStatus: status,
		}
		run, err := backfills.LatestScopedRun(ctx, organizationID, projectID, releaseVersion, file.ID)
		if err != nil {
			return nil, err
		}
		if run != nil {
			item.ReprocessRunID = run.ID
			item.ReprocessStatus = string(run.Status)
			item.ReprocessLastError = run.LastError
			item.ReprocessUpdatedAt = run.UpdatedAt
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *NativeControlStore) ListReleaseRuns(ctx context.Context, organizationID, projectID, releaseVersion string, limit int) ([]BackfillRun, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("native control store is not configured")
	}
	return NewBackfillStore(s.db).ListScopedRuns(ctx, organizationID, projectID, releaseVersion, "", limit)
}

func auditRecordForNativeRun(action string, run *BackfillRun, in CreateNativeReprocessRun) AuditRecord {
	record := AuditRecord{
		Action:         strings.TrimSpace(action),
		ProjectID:      strings.TrimSpace(in.ProjectID),
		OrganizationID: strings.TrimSpace(in.OrganizationID),
		RequestPath:    strings.TrimSpace(in.RequestPath),
		RequestMethod:  strings.TrimSpace(in.RequestMethod),
		IPAddress:      strings.TrimSpace(in.IPAddress),
		UserAgent:      strings.TrimSpace(in.UserAgent),
	}
	if run != nil {
		record.ProjectID = firstNonEmptyString(record.ProjectID, run.ProjectID)
		record.OrganizationID = firstNonEmptyString(record.OrganizationID, run.OrganizationID)
	}
	if in.Principal != nil {
		record.CredentialType = string(in.Principal.Kind)
		record.CredentialID = in.Principal.CredentialID
		if in.Principal.User != nil {
			record.UserID = in.Principal.User.ID
		}
	}
	return record
}

// firstNonEmptyString delegates to firstNonEmptyText (defined in sqlite_helpers.go).
func firstNonEmptyString(values ...string) string {
	return firstNonEmptyText(values...)
}

func buildNativeRunAuditMetadata(run *BackfillRun, in CreateNativeReprocessRun) string {
	payload := map[string]string{}
	if run != nil && strings.TrimSpace(run.ID) != "" {
		payload["runId"] = run.ID
	}
	if version := strings.TrimSpace(in.ReleaseVersion); version != "" {
		payload["releaseVersion"] = version
	}
	if debugFileID := strings.TrimSpace(in.DebugFileID); debugFileID != "" {
		payload["debugFileId"] = debugFileID
	}
	if len(payload) == 0 {
		return "{}"
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func operatorAuditActor(principal *authpkg.Principal) string {
	if principal == nil {
		return "system"
	}
	if principal.User != nil {
		if email := strings.TrimSpace(principal.User.Email); email != "" {
			return email
		}
		if id := strings.TrimSpace(principal.User.ID); id != "" {
			return id
		}
	}
	if id := strings.TrimSpace(principal.CredentialID); id != "" {
		return id
	}
	if kind := strings.TrimSpace(string(principal.Kind)); kind != "" {
		return kind
	}
	return "system"
}
