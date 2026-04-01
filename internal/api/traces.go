package api

import (
	"database/sql"
	"net/http"

	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"urgentry/internal/telemetryquery"
)

func handleListTransactions(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		limit := discoverLimit(r, 100)
		orgSlug := PathParam(r, "org_slug")
		projectID, org, ok := resolveTraceScope(w, r, db, orgSlug)
		if !ok {
			return
		}
		if !enforceQueryGuard(w, r, guard, org.ID, projectID, sqlite.QueryEstimate{
			Workload: sqlite.QueryWorkloadTransactions,
			Limit:    limit,
		}) {
			return
		}
		items, err := queries.ListTransactions(r.Context(), projectID, limit)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list transactions.")
			return
		}
		resp := make([]TransactionSummary, 0, len(items))
		for _, item := range items {
			resp = append(resp, mapTransaction(item))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleGetTrace(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		projectID, org, ok := resolveTraceScope(w, r, db, orgSlug)
		if !ok {
			return
		}
		traceID := PathParam(r, "trace_id")
		if !enforceQueryGuard(w, r, guard, org.ID, projectID, sqlite.QueryEstimate{
			Workload: sqlite.QueryWorkloadTransactions,
			Limit:    1,
			Query:    traceID,
			Detail:   true,
		}) {
			return
		}
		items, err := queries.ListTransactionsByTrace(r.Context(), projectID, traceID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list trace transactions.")
			return
		}
		spans, err := queries.ListTraceSpans(r.Context(), projectID, traceID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load trace spans.")
			return
		}
		detail := TraceDetail{TraceID: traceID}
		for _, item := range items {
			detail.Transactions = append(detail.Transactions, mapTransaction(item))
		}
		for _, span := range spans {
			detail.Spans = append(detail.Spans, mapTraceSpan(span))
		}
		relatedProfiles, err := queries.FindProfilesByTrace(r.Context(), projectID, traceID, 10)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load related profiles.")
			return
		}
		for _, item := range relatedProfiles {
			profile := mapProfileManifest(store.ProfileManifest{
				ProjectID:     item.ProjectID,
				EventID:       item.EventID,
				ProfileID:     item.ProfileID,
				TraceID:       item.TraceID,
				Transaction:   item.Transaction,
				Release:       item.Release,
				Environment:   item.Environment,
				Platform:      item.Platform,
				DurationNS:    item.DurationNS,
				SampleCount:   item.SampleCount,
				FunctionCount: item.FunctionCount,
				StartedAt:     item.StartedAt,
			})
			if item.TopFunction != "" {
				profile.Summary.TopFunctions = []ProfileBreakdown{{
					Name:  item.TopFunction,
					Count: item.TopFunctionCnt,
				}}
			}
			detail.Profiles = append(detail.Profiles, profile)
		}
		if len(detail.Transactions) == 0 && len(detail.Spans) == 0 {
			httputil.WriteError(w, http.StatusNotFound, "Trace not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, detail)
	}
}

func resolveTraceScope(w http.ResponseWriter, r *http.Request, db *sql.DB, orgSlug string) (string, *Organization, bool) {
	if catalog := catalogFromRequest(r); catalog != nil {
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return "", nil, false
		}
		org, ok := getOrganizationFromCatalog(w, r, catalog, orgSlug)
		if !ok {
			return "", nil, false
		}
		return projectID, org, true
	}
	projectID, ok := resolveProjectID(w, r, db)
	if !ok {
		return "", nil, false
	}
	org, err := getOrganizationFromDB(r, db, orgSlug)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "Failed to load organization.")
		return "", nil, false
	}
	if org == nil {
		httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
		return "", nil, false
	}
	return projectID, org, true
}

func mapTransaction(item *store.StoredTransaction) TransactionSummary {
	resp := TransactionSummary{
		ID:           item.ID,
		ProjectID:    item.ProjectID,
		EventID:      item.EventID,
		TraceID:      item.TraceID,
		SpanID:       item.SpanID,
		ParentSpanID: item.ParentSpanID,
		Transaction:  item.Transaction,
		Op:           item.Op,
		Status:       item.Status,
		Platform:     item.Platform,
		Environment:  item.Environment,
		Release:      item.ReleaseID,
		DurationMS:   item.DurationMS,
	}
	if !item.StartTimestamp.IsZero() {
		resp.StartTimestamp = &item.StartTimestamp
	}
	if !item.EndTimestamp.IsZero() {
		resp.EndTimestamp = &item.EndTimestamp
	}
	if len(item.Measurements) > 0 {
		resp.Measurements = make(map[string]Measurement, len(item.Measurements))
		for key, measurement := range item.Measurements {
			resp.Measurements[key] = Measurement{Value: measurement.Value, Unit: measurement.Unit}
		}
	}
	return resp
}

func mapTraceSpan(item store.StoredSpan) TraceSpan {
	resp := TraceSpan{
		ID:                 item.ID,
		ProjectID:          item.ProjectID,
		TransactionEventID: item.TransactionEventID,
		TraceID:            item.TraceID,
		SpanID:             item.SpanID,
		ParentSpanID:       item.ParentSpanID,
		Op:                 item.Op,
		Description:        item.Description,
		Status:             item.Status,
		DurationMS:         item.DurationMS,
		Tags:               item.Tags,
		Data:               item.Data,
	}
	if !item.StartTimestamp.IsZero() {
		resp.StartTimestamp = &item.StartTimestamp
	}
	if !item.EndTimestamp.IsZero() {
		resp.EndTimestamp = &item.EndTimestamp
	}
	return resp
}
