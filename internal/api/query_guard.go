package api

import (
	"math"
	"net/http"
	"strconv"

	"urgentry/internal/auth"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
)

func enforceQueryGuard(w http.ResponseWriter, r *http.Request, guard sqlite.QueryGuard, organizationID, projectID string, estimate sqlite.QueryEstimate) bool {
	if guard == nil {
		return true
	}
	decision, err := guard.CheckAndRecord(r.Context(), sqlite.QueryGuardRequest{
		Principal:      auth.PrincipalFromContext(r.Context()),
		OrganizationID: organizationID,
		ProjectID:      projectID,
		RequestPath:    r.URL.Path,
		RequestMethod:  r.Method,
		IPAddress:      r.RemoteAddr,
		UserAgent:      r.UserAgent(),
		Estimate:       estimate,
	})
	if err != nil {
		httputil.WriteAPIError(w, httputil.APIError{
			Status: http.StatusInternalServerError,
			Code:   "query_guard_failure",
			Detail: "Failed to apply query guardrails.",
		})
		return false
	}
	if decision.Allowed {
		return true
	}
	if decision.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(decision.RetryAfter.Seconds()))))
	}
	httputil.WriteAPIError(w, httputil.APIError{
		Status: decision.StatusCode,
		Code:   "query_guard_blocked",
		Detail: decision.Reason,
	})
	return false
}
