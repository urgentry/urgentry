package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
	sharedstore "urgentry/internal/store"
	"urgentry/internal/telemetryquery"
	"urgentry/pkg/id"
)

// handleListOrgReplays lists replays across all projects in an organization.
func handleListOrgReplays(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, err := getOrganizationFromDB(r, db, PathParam(r, "org_slug"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load organization.")
			return
		}
		if org == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		if !enforceQueryGuard(w, r, guard, org.ID, "", sqlite.QueryEstimate{
			Workload: sqlite.QueryWorkloadReplays,
			Limit:    100,
		}) {
			return
		}

		// Attempt org-level replay listing. The SQLite replay store has
		// ListOrgReplays but the telemetry query service interface only
		// exposes project-scoped reads. We query all org projects and
		// merge results.
		projects, err := listProjectIDsForOrg(db, org.ID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list projects.")
			return
		}
		var allItems []sharedstore.ReplayManifest
		for _, pid := range projects {
			items, err := queries.ListReplays(r.Context(), pid, 100)
			if err != nil {
				continue // best effort per project
			}
			allItems = append(allItems, items...)
		}
		// Sort by started_at descending, limit to 100.
		sortReplayManifestsByDate(allItems)
		if len(allItems) > 100 {
			allItems = allItems[:100]
		}
		resp := make([]Replay, 0, len(allItems))
		for _, item := range allItems {
			resp = append(resp, mapReplayManifest(item))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

// handleGetOrgReplay gets a single replay by ID across an organization.
func handleGetOrgReplay(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, err := getOrganizationFromDB(r, db, PathParam(r, "org_slug"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load organization.")
			return
		}
		if org == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		if !enforceQueryGuard(w, r, guard, org.ID, "", sqlite.QueryEstimate{
			Workload: sqlite.QueryWorkloadReplays,
			Limit:    1,
			Detail:   true,
		}) {
			return
		}
		replayID := PathParam(r, "replay_id")
		projects, err := listProjectIDsForOrg(db, org.ID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list projects.")
			return
		}
		for _, pid := range projects {
			item, err := queries.GetReplay(r.Context(), pid, replayID)
			if err != nil {
				if errors.Is(err, sharedstore.ErrNotFound) {
					continue
				}
				continue
			}
			if item != nil {
				httputil.WriteJSON(w, http.StatusOK, mapReplayRecord(item, true))
				return
			}
		}
		httputil.WriteError(w, http.StatusNotFound, "Replay not found.")
	}
}

// handleDeleteOrgReplay deletes a replay by ID across an organization.
func handleDeleteOrgReplay(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, err := getOrganizationFromDB(r, db, PathParam(r, "org_slug"))
		if err != nil || org == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		replayID := PathParam(r, "replay_id")
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete replay.")
			return
		}
		var manifestID string
		err = tx.QueryRowContext(r.Context(),
			`SELECT rm.id FROM replay_manifests rm
			 JOIN projects p ON p.id = rm.project_id
			 WHERE p.organization_id = ? AND rm.replay_id = ?`,
			org.ID, replayID,
		).Scan(&manifestID)
		if err != nil {
			_ = tx.Rollback()
			if err == sql.ErrNoRows {
				httputil.WriteError(w, http.StatusNotFound, "Replay not found.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete replay.")
			return
		}
		tx.ExecContext(r.Context(), `DELETE FROM replay_timeline_items WHERE manifest_id = ?`, manifestID)
		tx.ExecContext(r.Context(), `DELETE FROM replay_assets WHERE manifest_id = ?`, manifestID)
		tx.ExecContext(r.Context(), `DELETE FROM replay_manifests WHERE id = ?`, manifestID)
		if err := tx.Commit(); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete replay.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleGetReplayCount returns replay counts per project for an organization.
func handleGetReplayCount(db *sql.DB, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, err := getOrganizationFromDB(r, db, PathParam(r, "org_slug"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load organization.")
			return
		}
		if org == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		if !enforceQueryGuard(w, r, guard, org.ID, "", sqlite.QueryEstimate{
			Workload: sqlite.QueryWorkloadReplays,
			Limit:    1,
		}) {
			return
		}
		rows, err := db.QueryContext(r.Context(), `
			SELECT rm.project_id, COUNT(*)
			  FROM replay_manifests rm
			  JOIN projects p ON p.id = rm.project_id
			 WHERE p.organization_id = ?
			 GROUP BY rm.project_id`,
			org.ID,
		)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to count replays.")
			return
		}
		defer rows.Close()
		counts := make(map[string]int)
		for rows.Next() {
			var projectID string
			var count int
			if err := rows.Scan(&projectID, &count); err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to scan replay counts.")
				return
			}
			counts[projectID] = count
		}
		httputil.WriteJSON(w, http.StatusOK, counts)
	}
}

// handleGetReplaySelectors returns distinct CSS selectors from replay clicks.
func handleGetReplaySelectors(db *sql.DB, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, err := getOrganizationFromDB(r, db, PathParam(r, "org_slug"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load organization.")
			return
		}
		if org == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		if !enforceQueryGuard(w, r, guard, org.ID, "", sqlite.QueryEstimate{
			Workload: sqlite.QueryWorkloadReplays,
			Limit:    100,
		}) {
			return
		}
		rows, err := db.QueryContext(r.Context(), `
			SELECT DISTINCT rti.selector
			  FROM replay_timeline_items rti
			  JOIN replay_manifests rm ON rm.id = rti.manifest_id
			  JOIN projects p ON p.id = rm.project_id
			 WHERE p.organization_id = ?
			   AND rti.kind = 'click'
			   AND rti.selector != ''
			 ORDER BY rti.selector ASC
			 LIMIT 100`,
			org.ID,
		)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list selectors.")
			return
		}
		defer rows.Close()
		selectors := make([]string, 0)
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to scan selector.")
				return
			}
			selectors = append(selectors, s)
		}
		httputil.WriteJSON(w, http.StatusOK, selectors)
	}
}

// handleDeleteReplay deletes a single replay from a project.
func handleDeleteReplay(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectID(w, r, db)
		if !ok {
			return
		}
		replayID := PathParam(r, "replay_id")
		// Delete manifest, timeline items, asset refs.
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete replay.")
			return
		}
		var manifestID string
		err = tx.QueryRowContext(r.Context(),
			`SELECT id FROM replay_manifests WHERE project_id = ? AND replay_id = ?`,
			projectID, replayID,
		).Scan(&manifestID)
		if err != nil {
			_ = tx.Rollback()
			if err == sql.ErrNoRows {
				httputil.WriteError(w, http.StatusNotFound, "Replay not found.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete replay.")
			return
		}
		tx.ExecContext(r.Context(), `DELETE FROM replay_timeline_items WHERE manifest_id = ?`, manifestID)
		tx.ExecContext(r.Context(), `DELETE FROM replay_assets WHERE manifest_id = ?`, manifestID)
		tx.ExecContext(r.Context(), `DELETE FROM replay_manifests WHERE id = ?`, manifestID)
		if err := tx.Commit(); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete replay.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ReplayDeletionJob is the API response for a deletion job.
type ReplayDeletionJob struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	ReplayIDs []string  `json:"replayIds"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"dateCreated"`
	UpdatedAt time.Time `json:"dateUpdated"`
}

type replayDeletionRequest struct {
	ReplayIDs []string `json:"replayIds"`
}

// handleReplayDeletionJobs handles GET (list) and POST (create) for deletion jobs.
func handleReplayDeletionJobs(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectID(w, r, db)
		if !ok {
			return
		}
		switch r.Method {
		case http.MethodGet:
			handleListReplayDeletionJobs(w, r, db, projectID)
		case http.MethodPost:
			handleCreateReplayDeletionJob(w, r, db, projectID)
		default:
			httputil.WriteError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		}
	}
}

func handleListReplayDeletionJobs(w http.ResponseWriter, r *http.Request, db *sql.DB, projectID string) {
	rows, err := db.QueryContext(r.Context(),
		`SELECT id, project_id, replay_ids_json, status, created_at, updated_at
		 FROM replay_deletion_jobs
		 WHERE project_id = ?
		 ORDER BY created_at DESC
		 LIMIT 50`,
		projectID,
	)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "Failed to list deletion jobs.")
		return
	}
	defer rows.Close()
	jobs := make([]ReplayDeletionJob, 0)
	for rows.Next() {
		job, err := scanReplayDeletionJob(rows)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to scan deletion job.")
			return
		}
		jobs = append(jobs, job)
	}
	httputil.WriteJSON(w, http.StatusOK, jobs)
}

func handleCreateReplayDeletionJob(w http.ResponseWriter, r *http.Request, db *sql.DB, projectID string) {
	var body replayDeletionRequest
	if err := decodeJSON(r, &body); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
		return
	}
	if len(body.ReplayIDs) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "At least one replay ID is required.")
		return
	}
	now := time.Now().UTC()
	jobID := generateAPIID()
	idsJSON, _ := json.Marshal(body.ReplayIDs)
	_, err := db.ExecContext(r.Context(),
		`INSERT INTO replay_deletion_jobs (id, project_id, replay_ids_json, status, created_at, updated_at)
		 VALUES (?, ?, ?, 'pending', ?, ?)`,
		jobID, projectID, string(idsJSON), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "Failed to create deletion job.")
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, ReplayDeletionJob{
		ID:        jobID,
		ProjectID: projectID,
		ReplayIDs: body.ReplayIDs,
		Status:    "pending",
		CreatedAt: now,
		UpdatedAt: now,
	})
}

// handleGetReplayDeletionJob returns a single deletion job.
func handleGetReplayDeletionJob(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectID(w, r, db)
		if !ok {
			return
		}
		jobID := PathParam(r, "job_id")
		row := db.QueryRowContext(r.Context(),
			`SELECT id, project_id, replay_ids_json, status, created_at, updated_at
			 FROM replay_deletion_jobs
			 WHERE project_id = ? AND id = ?`,
			projectID, jobID,
		)
		job, err := scanReplayDeletionJob(row)
		if err != nil {
			if err == sql.ErrNoRows {
				httputil.WriteError(w, http.StatusNotFound, "Deletion job not found.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load deletion job.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, job)
	}
}

// handleListReplayClicks returns click events for a replay.
func handleListReplayClicks(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID, replayID, ok := guardReplayProjectRead(w, r, db, guard, auth, 100, false)
		if !ok {
			return
		}
		items, err := queries.ListReplayTimeline(r.Context(), projectID, replayID, sharedstore.ReplayTimelineFilter{
			Kind:  "click",
			Limit: 100,
		})
		if err != nil {
			if errors.Is(err, sharedstore.ErrNotFound) {
				httputil.WriteError(w, http.StatusNotFound, "Replay not found.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list replay clicks.")
			return
		}
		resp := make([]ReplayTimelineItem, 0, len(items))
		for _, item := range items {
			resp = append(resp, mapReplayTimelineItem(item))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

// handleListReplayRecordingSegments returns recording segments for a replay.
func handleListReplayRecordingSegments(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID, replayID, ok := guardReplayProjectRead(w, r, db, guard, auth, 1, true)
		if !ok {
			return
		}
		record, err := queries.GetReplay(r.Context(), projectID, replayID)
		if err != nil {
			if errors.Is(err, sharedstore.ErrNotFound) {
				httputil.WriteError(w, http.StatusNotFound, "Replay not found.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load replay.")
			return
		}
		type RecordingSegment struct {
			ID          string    `json:"id"`
			ReplayID    string    `json:"replayId"`
			SegmentID   int       `json:"segmentId"`
			DateCreated time.Time `json:"dateCreated"`
		}
		segments := make([]RecordingSegment, 0, len(record.Assets))
		for _, asset := range record.Assets {
			segments = append(segments, RecordingSegment{
				ID:          asset.ID,
				ReplayID:    replayID,
				SegmentID:   asset.ChunkIndex,
				DateCreated: asset.CreatedAt,
			})
		}
		httputil.WriteJSON(w, http.StatusOK, segments)
	}
}

// -- helpers --

func listProjectIDsForOrg(db *sql.DB, orgID string) ([]string, error) {
	rows, err := db.Query(`SELECT id FROM projects WHERE organization_id = ?`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func sortReplayManifestsByDate(items []sharedstore.ReplayManifest) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0; j-- {
			a := replayManifestSortDate(items[j])
			b := replayManifestSortDate(items[j-1])
			if a.After(b) {
				items[j], items[j-1] = items[j-1], items[j]
			} else {
				break
			}
		}
	}
}

func replayManifestSortDate(item sharedstore.ReplayManifest) time.Time {
	if !item.StartedAt.IsZero() {
		return item.StartedAt
	}
	return item.CreatedAt
}

type replayDeletionScanner interface {
	Scan(dest ...any) error
}

func scanReplayDeletionJob(scanner replayDeletionScanner) (ReplayDeletionJob, error) {
	var job ReplayDeletionJob
	var idsJSON, createdAt, updatedAt string
	if err := scanner.Scan(&job.ID, &job.ProjectID, &idsJSON, &job.Status, &createdAt, &updatedAt); err != nil {
		return ReplayDeletionJob{}, err
	}
	_ = json.Unmarshal([]byte(idsJSON), &job.ReplayIDs)
	if job.ReplayIDs == nil {
		job.ReplayIDs = []string{}
	}
	if createdAt != "" {
		job.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	}
	if updatedAt != "" {
		job.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	}
	return job, nil
}

func generateAPIID() string {
	return id.New()
}
