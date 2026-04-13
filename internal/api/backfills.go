package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/auth"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

type createBackfillRequest struct {
	Kind           string `json:"kind"`
	ProjectSlug    string `json:"projectSlug"`
	ReleaseVersion string `json:"releaseVersion"`
	StartedAfter   string `json:"startedAfter"`
	EndedBefore    string `json:"endedBefore"`
}

func handleListBackfills(db *sql.DB, backfills *sqlite.BackfillStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		var org *Organization
		if catalog := catalogFromRequest(r); catalog != nil {
			var ok bool
			org, ok = getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
			if !ok {
				return
			}
		} else {
			var err error
			org, err = getOrganizationFromDB(r, db, PathParam(r, "org_slug"))
			if err != nil || org == nil {
				httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
				return
			}
		}
		limit := 50
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
				limit = parsed
			}
		}
		runs, err := backfills.ListRuns(r.Context(), org.ID, limit)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list backfill runs.")
			return
		}
		resp := make([]BackfillRun, 0, len(runs))
		for _, run := range runs {
			resp = append(resp, mapBackfillRun(run))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleCreateBackfill(db *sql.DB, backfills *sqlite.BackfillStore, native *sqlite.NativeControlStore, auditStore *sqlite.AuditStore, audits store.OperatorAuditStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		var org *Organization
		if catalog := catalogFromRequest(r); catalog != nil {
			var ok bool
			org, ok = getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
			if !ok {
				return
			}
		} else {
			var err error
			org, err = getOrganizationFromDB(r, db, PathParam(r, "org_slug"))
			if err != nil || org == nil {
				httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
				return
			}
		}
		var body createBackfillRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusBadRequest,
				Code:   "invalid_request_body",
				Detail: "Invalid request body.",
			})
			return
		}
		kind := sqlite.BackfillKind(strings.TrimSpace(body.Kind))
		if kind == "" {
			kind = sqlite.BackfillKindNativeReprocess
		}
		if kind != sqlite.BackfillKindNativeReprocess && kind != sqlite.BackfillKindTelemetryRebuild {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusBadRequest,
				Code:   "invalid_backfill_kind",
				Detail: "Unsupported backfill kind.",
			})
			return
		}
		projectID := ""
		if projectSlug := strings.TrimSpace(body.ProjectSlug); projectSlug != "" {
			resolvedID, err := projectIDFromSlugs(r, db, org.Slug, projectSlug)
			if err != nil || resolvedID == "" {
				httputil.WriteError(w, http.StatusNotFound, "Project not found.")
				return
			}
			projectID = resolvedID
		}
		startedAfter, err := parseOptionalRFC3339(body.StartedAfter)
		if err != nil {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusBadRequest,
				Code:   "invalid_started_after",
				Detail: "startedAfter must be RFC3339.",
			})
			return
		}
		endedBefore, err := parseOptionalRFC3339(body.EndedBefore)
		if err != nil {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusBadRequest,
				Code:   "invalid_ended_before",
				Detail: "endedBefore must be RFC3339.",
			})
			return
		}
		if !startedAfter.IsZero() && !endedBefore.IsZero() && endedBefore.Before(startedAfter) {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusBadRequest,
				Code:   "invalid_time_window",
				Detail: "endedBefore must be after startedAfter.",
			})
			return
		}
		if kind == sqlite.BackfillKindNativeReprocess && projectID == "" && strings.TrimSpace(body.ReleaseVersion) == "" && startedAfter.IsZero() && endedBefore.IsZero() {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusBadRequest,
				Code:   "backfill_scope_required",
				Detail: "Backfills must be bounded by project, release, or time range.",
			})
			return
		}
		principal := auth.PrincipalFromContext(r.Context())
		var run *sqlite.BackfillRun
		switch kind {
		case sqlite.BackfillKindNativeReprocess:
			run, err = native.CreateRun(r.Context(), sqlite.CreateNativeReprocessRun{
				OrganizationID: org.ID,
				ProjectID:      projectID,
				ReleaseVersion: strings.TrimSpace(body.ReleaseVersion),
				StartedAfter:   startedAfter,
				EndedBefore:    endedBefore,
				Principal:      principal,
				RequestedVia:   "api",
				RequestPath:    r.URL.Path,
				RequestMethod:  r.Method,
				IPAddress:      r.RemoteAddr,
				UserAgent:      r.UserAgent(),
			})
		case sqlite.BackfillKindTelemetryRebuild:
			if strings.TrimSpace(body.ReleaseVersion) != "" || !startedAfter.IsZero() || !endedBefore.IsZero() {
				httputil.WriteAPIError(w, httputil.APIError{
					Status: http.StatusBadRequest,
					Code:   "invalid_telemetry_rebuild_scope",
					Detail: "Telemetry rebuilds support organization or project scope only.",
				})
				return
			}
			requestedByUserID := userID(principal)
			run, err = backfills.CreateRun(r.Context(), sqlite.CreateBackfillRun{
				Kind:              kind,
				OrganizationID:    org.ID,
				ProjectID:         projectID,
				RequestedByUserID: requestedByUserID,
				RequestedVia:      "api",
			})
			if err == nil {
				_ = auditStore.Record(r.Context(), sqlite.AuditRecord{
					CredentialType: credentialKind(principal),
					CredentialID:   credentialID(principal),
					UserID:         requestedByUserID,
					ProjectID:      projectID,
					OrganizationID: org.ID,
					Action:         "telemetry.rebuild.requested",
					RequestPath:    r.URL.Path,
					RequestMethod:  r.Method,
					IPAddress:      r.RemoteAddr,
					UserAgent:      r.UserAgent(),
				})
				recordOperatorBackfillAudit(r, audits, org.ID, projectID, "telemetry.rebuild.requested", "api", "requested telemetry rebuild", `{"runId":"`+run.ID+`"}`, principal)
			}
		}
		if err != nil {
			if sqlite.IsBackfillConflict(err) {
				httputil.WriteAPIError(w, httputil.APIError{
					Status: http.StatusConflict,
					Code:   "backfill_conflict",
					Detail: "A conflicting backfill run is already active.",
				})
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create backfill run.")
			return
		}
		httputil.WriteJSON(w, http.StatusAccepted, mapBackfillRun(*run))
	}
}

func handleGetBackfill(db *sql.DB, backfills *sqlite.BackfillStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		var org *Organization
		if catalog := catalogFromRequest(r); catalog != nil {
			var ok bool
			org, ok = getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
			if !ok {
				return
			}
		} else {
			var err error
			org, err = getOrganizationFromDB(r, db, PathParam(r, "org_slug"))
			if err != nil || org == nil {
				httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
				return
			}
		}
		run, err := backfills.GetRun(r.Context(), org.ID, PathParam(r, "run_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load backfill run.")
			return
		}
		if run == nil {
			httputil.WriteError(w, http.StatusNotFound, "Backfill run not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapBackfillRun(*run))
	}
}

func handleCancelBackfill(db *sql.DB, backfills *sqlite.BackfillStore, auditStore *sqlite.AuditStore, audits store.OperatorAuditStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		var org *Organization
		if catalog := catalogFromRequest(r); catalog != nil {
			var ok bool
			org, ok = getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
			if !ok {
				return
			}
		} else {
			var err error
			org, err = getOrganizationFromDB(r, db, PathParam(r, "org_slug"))
			if err != nil || org == nil {
				httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
				return
			}
		}
		run, err := backfills.CancelRun(r.Context(), org.ID, PathParam(r, "run_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to cancel backfill run.")
			return
		}
		if run == nil {
			httputil.WriteError(w, http.StatusNotFound, "Backfill run not found.")
			return
		}
		if run.Status != sqlite.BackfillStatusCancelled {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusConflict,
				Code:   "backfill_cancel_conflict",
				Detail: "Only pending backfill runs can be cancelled.",
			})
			return
		}
		if run.Kind == sqlite.BackfillKindNativeReprocess || run.Kind == sqlite.BackfillKindTelemetryRebuild {
			principal := auth.PrincipalFromContext(r.Context())
			action := "native.reprocess.cancelled"
			if run.Kind == sqlite.BackfillKindTelemetryRebuild {
				action = "telemetry.rebuild.cancelled"
			}
			_ = auditStore.Record(r.Context(), sqlite.AuditRecord{
				CredentialType: credentialKind(principal),
				CredentialID:   credentialID(principal),
				UserID:         userID(principal),
				ProjectID:      run.ProjectID,
				OrganizationID: run.OrganizationID,
				Action:         action,
				RequestPath:    r.URL.Path,
				RequestMethod:  r.Method,
				IPAddress:      r.RemoteAddr,
				UserAgent:      r.UserAgent(),
			})
			recordOperatorBackfillAudit(r, audits, run.OrganizationID, run.ProjectID, action, "api", "cancelled backfill", `{"runId":"`+run.ID+`"}`, principal)
		}
		httputil.WriteJSON(w, http.StatusAccepted, mapBackfillRun(*run))
	}
}

func recordOperatorBackfillAudit(r *http.Request, audits store.OperatorAuditStore, organizationID, projectID, action, source, detail, metadata string, principal *auth.Principal) {
	if audits == nil {
		return
	}
	_ = audits.Record(r.Context(), store.OperatorAuditRecord{
		OrganizationID: organizationID,
		ProjectID:      projectID,
		Action:         action,
		Status:         "succeeded",
		Source:         strings.TrimSpace(source),
		Actor:          operatorAuditActor(principal),
		Detail:         strings.TrimSpace(detail),
		MetadataJSON:   strings.TrimSpace(metadata),
	})
}

func operatorAuditActor(principal *auth.Principal) string {
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

func credentialKind(principal *auth.Principal) string {
	if principal == nil {
		return ""
	}
	return string(principal.Kind)
}

func credentialID(principal *auth.Principal) string {
	if principal == nil {
		return ""
	}
	return principal.CredentialID
}

func userID(principal *auth.Principal) string {
	if principal == nil || principal.User == nil {
		return ""
	}
	return principal.User.ID
}

func mapBackfillRun(run sqlite.BackfillRun) BackfillRun {
	resp := BackfillRun{
		ID:                run.ID,
		Kind:              string(run.Kind),
		Status:            string(run.Status),
		OrganizationID:    run.OrganizationID,
		ProjectID:         run.ProjectID,
		ReleaseVersion:    run.ReleaseVersion,
		DebugFileID:       run.DebugFileID,
		TotalItems:        run.TotalItems,
		ProcessedItems:    run.ProcessedItems,
		UpdatedItems:      run.UpdatedItems,
		FailedItems:       run.FailedItems,
		RequestedByUserID: run.RequestedByUserID,
		RequestedVia:      run.RequestedVia,
		WorkerID:          run.WorkerID,
		LastError:         run.LastError,
		DateCreated:       run.CreatedAt,
		DateUpdated:       run.UpdatedAt,
	}
	if !run.StartedAfter.IsZero() {
		value := run.StartedAfter
		resp.StartedAfter = &value
	}
	if !run.EndedBefore.IsZero() {
		value := run.EndedBefore
		resp.EndedBefore = &value
	}
	if !run.StartedAt.IsZero() {
		value := run.StartedAt
		resp.DateStarted = &value
	}
	if !run.FinishedAt.IsZero() {
		value := run.FinishedAt
		resp.DateFinished = &value
	}
	return resp
}

func parseOptionalRFC3339(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, value)
}
