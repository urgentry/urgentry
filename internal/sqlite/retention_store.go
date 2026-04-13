package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/store"
)

type archivedEvent struct {
	ID             string `json:"id"`
	ProjectID      string `json:"project_id"`
	EventID        string `json:"event_id"`
	GroupID        string `json:"group_id"`
	Release        string `json:"release"`
	Environment    string `json:"environment"`
	Platform       string `json:"platform"`
	Level          string `json:"level"`
	EventType      string `json:"event_type"`
	Title          string `json:"title"`
	Culprit        string `json:"culprit"`
	Message        string `json:"message"`
	TagsJSON       string `json:"tags_json"`
	PayloadJSON    string `json:"payload_json"`
	OccurredAt     string `json:"occurred_at"`
	IngestedAt     string `json:"ingested_at"`
	UserIdentifier string `json:"user_identifier"`
	PayloadKey     string `json:"payload_key"`
}

type archivedAttachment struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	EventID     string `json:"event_id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	ObjectKey   string `json:"object_key"`
	CreatedAt   string `json:"created_at"`
}

type archivedTransaction struct {
	ID               string  `json:"id"`
	ProjectID        string  `json:"project_id"`
	EventID          string  `json:"event_id"`
	TraceID          string  `json:"trace_id"`
	SpanID           string  `json:"span_id"`
	ParentSpanID     string  `json:"parent_span_id"`
	TransactionName  string  `json:"transaction_name"`
	Op               string  `json:"op"`
	Status           string  `json:"status"`
	Platform         string  `json:"platform"`
	Environment      string  `json:"environment"`
	Release          string  `json:"release"`
	StartTimestamp   string  `json:"start_timestamp"`
	EndTimestamp     string  `json:"end_timestamp"`
	DurationMS       float64 `json:"duration_ms"`
	TagsJSON         string  `json:"tags_json"`
	MeasurementsJSON string  `json:"measurements_json"`
	PayloadJSON      string  `json:"payload_json"`
	PayloadKey       string  `json:"payload_key"`
	CreatedAt        string  `json:"created_at"`
}

type archivedSpan struct {
	ID                 string  `json:"id"`
	ProjectID          string  `json:"project_id"`
	TransactionEventID string  `json:"transaction_event_id"`
	TraceID            string  `json:"trace_id"`
	SpanID             string  `json:"span_id"`
	ParentSpanID       string  `json:"parent_span_id"`
	Op                 string  `json:"op"`
	Description        string  `json:"description"`
	Status             string  `json:"status"`
	StartTimestamp     string  `json:"start_timestamp"`
	EndTimestamp       string  `json:"end_timestamp"`
	DurationMS         float64 `json:"duration_ms"`
	TagsJSON           string  `json:"tags_json"`
	DataJSON           string  `json:"data_json"`
	CreatedAt          string  `json:"created_at"`
}

type archivedOutcome struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	EventID     string `json:"event_id"`
	Category    string `json:"category"`
	Reason      string `json:"reason"`
	Quantity    int    `json:"quantity"`
	Source      string `json:"source"`
	Release     string `json:"release"`
	Environment string `json:"environment"`
	PayloadJSON string `json:"payload_json"`
	RecordedAt  string `json:"recorded_at"`
	CreatedAt   string `json:"created_at"`
}

type archivedDebugFile struct {
	ID             string `json:"id"`
	ProjectID      string `json:"project_id"`
	ReleaseVersion string `json:"release_version"`
	UUID           string `json:"uuid"`
	CodeID         string `json:"code_id"`
	Name           string `json:"name"`
	ObjectKey      string `json:"object_key"`
	SizeBytes      int64  `json:"size_bytes"`
	Checksum       string `json:"checksum"`
	CreatedAt      string `json:"created_at"`
	Kind           string `json:"kind"`
	ContentType    string `json:"content_type"`
}

type telemetryArchiveRow struct {
	ID         string
	Surface    store.TelemetrySurface
	RecordType string
	RecordID   string
	ArchiveKey string
	Metadata   string
	ArchivedAt time.Time
	RestoredAt time.Time
}

type RetentionArchiveEntry struct {
	ID          string
	ProjectID   string
	Surface     store.TelemetrySurface
	RecordType  string
	RecordID    string
	ArchiveKey  string
	ArchivedAt  time.Time
	RestoredAt  time.Time
	BlobPresent bool
}

type RetentionSurfaceExecution struct {
	ProjectID            string
	Surface              store.TelemetrySurface
	StorageTier          store.TelemetryStorageTier
	RetentionDays        int
	ArchiveRetentionDays int
	Archived             int64
	Deleted              int64
}

// RetentionReport describes work completed by a telemetry retention sweep.
type RetentionReport struct {
	ErrorsDeleted       int64
	ErrorsArchived      int64
	LogsDeleted         int64
	LogsArchived        int64
	ReplaysDeleted      int64
	ReplaysArchived     int64
	ProfilesDeleted     int64
	ProfilesArchived    int64
	TracesDeleted       int64
	TracesArchived      int64
	OutcomesDeleted     int64
	OutcomesArchived    int64
	AttachmentsDeleted  int64
	AttachmentsArchived int64
	DebugFilesDeleted   int64
	DebugFilesArchived  int64
	GroupsDeleted       int64
}

// RetentionStore applies telemetry retention policies.
type RetentionStore struct {
	db    *sql.DB
	blobs store.BlobStore
}

func NewRetentionStore(db *sql.DB, blobs store.BlobStore) *RetentionStore {
	return &RetentionStore{db: db, blobs: blobs}
}

func (s *RetentionStore) Apply(ctx context.Context) (*RetentionReport, error) {
	if s == nil || s.db == nil {
		return &RetentionReport{}, nil
	}
	projects, err := ListProjects(ctx, s.db, "")
	if err != nil {
		return nil, fmt.Errorf("list project retention: %w", err)
	}
	report := &RetentionReport{}
	for _, project := range projects {
		policies, err := listProjectTelemetryPolicies(ctx, s.db, project)
		if err != nil {
			return nil, err
		}
		for _, policy := range policies {
			archived, deleted, err := s.applyPolicy(ctx, project.ID, policy)
			if err != nil {
				return nil, err
			}
			report.recordArchived(policy.Surface, archived)
			report.recordDeleted(policy.Surface, deleted)
		}
		deleted, err := s.deleteOrphanGroups(ctx, project.ID)
		if err != nil {
			return nil, err
		}
		report.GroupsDeleted += deleted
	}
	return report, nil
}

func (r *RetentionReport) recordDeleted(surface store.TelemetrySurface, count int64) {
	switch surface {
	case store.TelemetrySurfaceErrors:
		r.ErrorsDeleted += count
	case store.TelemetrySurfaceLogs:
		r.LogsDeleted += count
	case store.TelemetrySurfaceReplays:
		r.ReplaysDeleted += count
	case store.TelemetrySurfaceProfiles:
		r.ProfilesDeleted += count
	case store.TelemetrySurfaceTraces:
		r.TracesDeleted += count
	case store.TelemetrySurfaceOutcomes:
		r.OutcomesDeleted += count
	case store.TelemetrySurfaceAttachments:
		r.AttachmentsDeleted += count
	case store.TelemetrySurfaceDebugFiles:
		r.DebugFilesDeleted += count
	}
}

func (r *RetentionReport) recordArchived(surface store.TelemetrySurface, count int64) {
	switch surface {
	case store.TelemetrySurfaceErrors:
		r.ErrorsArchived += count
	case store.TelemetrySurfaceLogs:
		r.LogsArchived += count
	case store.TelemetrySurfaceReplays:
		r.ReplaysArchived += count
	case store.TelemetrySurfaceProfiles:
		r.ProfilesArchived += count
	case store.TelemetrySurfaceTraces:
		r.TracesArchived += count
	case store.TelemetrySurfaceOutcomes:
		r.OutcomesArchived += count
	case store.TelemetrySurfaceAttachments:
		r.AttachmentsArchived += count
	case store.TelemetrySurfaceDebugFiles:
		r.DebugFilesArchived += count
	}
}

func (s *RetentionStore) RestoreSurface(ctx context.Context, projectID string, surface store.TelemetrySurface, limit int) (int64, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, surface, record_type, record_id, COALESCE(archive_key, ''), COALESCE(metadata_json, '{}'),
		        COALESCE(archived_at, ''), COALESCE(restored_at, '')
		 FROM telemetry_archives
		 WHERE project_id = ? AND surface = ? AND restored_at IS NULL
		 ORDER BY archived_at ASC, id ASC
		 LIMIT ?`,
		projectID, string(surface), limit,
	)
	if err != nil {
		return 0, fmt.Errorf("list archives to restore: %w", err)
	}
	defer rows.Close()

	var archives []telemetryArchiveRow
	for rows.Next() {
		var item telemetryArchiveRow
		var archivedAt, restoredAt string
		if err := rows.Scan(&item.ID, &item.Surface, &item.RecordType, &item.RecordID, &item.ArchiveKey, &item.Metadata, &archivedAt, &restoredAt); err != nil {
			return 0, fmt.Errorf("scan archive row: %w", err)
		}
		item.ArchivedAt = parseTime(archivedAt)
		item.RestoredAt = parseTime(restoredAt)
		archives = append(archives, item)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate archive rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close archive rows: %w", err)
	}

	var restored int64
	for _, archive := range archives {
		if err := s.restoreArchiveRow(ctx, archive); err != nil {
			return restored, err
		}
		restored++
	}
	return restored, nil
}

func (s *RetentionStore) ExecuteSurface(ctx context.Context, projectID string, surface store.TelemetrySurface) (*RetentionSurfaceExecution, error) {
	project, err := getProjectByID(ctx, s.db, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, sql.ErrNoRows
	}
	policies, err := listProjectTelemetryPolicies(ctx, s.db, *project)
	if err != nil {
		return nil, fmt.Errorf("load project retention policies: %w", err)
	}
	for _, policy := range policies {
		if policy.Surface != surface {
			continue
		}
		archived, deleted, err := s.applyPolicy(ctx, projectID, policy)
		if err != nil {
			return nil, err
		}
		return &RetentionSurfaceExecution{
			ProjectID:            projectID,
			Surface:              surface,
			StorageTier:          policy.StorageTier,
			RetentionDays:        policy.RetentionDays,
			ArchiveRetentionDays: policy.ArchiveRetentionDays,
			Archived:             archived,
			Deleted:              deleted,
		}, nil
	}
	return nil, fmt.Errorf("retention policy not found for surface %s", surface)
}

func (s *RetentionStore) ListSurfaceArchives(ctx context.Context, projectID string, surface store.TelemetrySurface, limit int) ([]RetentionArchiveEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, surface, record_type, record_id, COALESCE(archive_key, ''), COALESCE(archived_at, ''), COALESCE(restored_at, '')
		 FROM telemetry_archives
		 WHERE project_id = ? AND surface = ?
		 ORDER BY archived_at DESC, id DESC
		 LIMIT ?`,
		projectID, string(surface), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list telemetry archives: %w", err)
	}
	defer rows.Close()

	entries := make([]RetentionArchiveEntry, 0, limit)
	for rows.Next() {
		var item RetentionArchiveEntry
		var archivedAt, restoredAt string
		if err := rows.Scan(&item.ID, &item.Surface, &item.RecordType, &item.RecordID, &item.ArchiveKey, &archivedAt, &restoredAt); err != nil {
			return nil, fmt.Errorf("scan telemetry archive: %w", err)
		}
		item.ProjectID = projectID
		item.ArchivedAt = parseTime(archivedAt)
		item.RestoredAt = parseTime(restoredAt)
		item.BlobPresent = item.ArchiveKey != ""
		if item.ArchiveKey != "" && s.blobs != nil {
			if _, err := s.blobs.Get(ctx, item.ArchiveKey); err != nil {
				item.BlobPresent = false
			}
		}
		entries = append(entries, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate telemetry archives: %w", err)
	}
	return entries, nil
}

func (s *RetentionStore) deleteOrphanGroups(ctx context.Context, projectID string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM groups
		 WHERE project_id = ?
		   AND id NOT IN (SELECT DISTINCT group_id FROM events WHERE project_id = ? AND group_id IS NOT NULL AND group_id != '')`,
		projectID, projectID,
	)
	if err != nil {
		return 0, fmt.Errorf("delete orphan groups: %w", err)
	}
	return res.RowsAffected()
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

func stringArgs(items []string) []any {
	args := make([]any, 0, len(items))
	for _, item := range items {
		args = append(args, item)
	}
	return args
}
