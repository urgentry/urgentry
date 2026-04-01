package web

import (
	"context"
	"database/sql"
	"math"
	"net/http"
	"strconv"
	"strings"

	"urgentry/internal/auth"
	"urgentry/internal/sqlite"
)

type pageScope struct {
	ProjectID        string
	ProjectSlug      string
	OrganizationID   string
	OrganizationSlug string
}

type pageRequestState struct {
	defaultScope pageScopeResult
	replayScopes map[string]pageScopeResult
	metrics      map[string]int
}

type pageScopeResult struct {
	loaded bool
	scope  pageScope
	err    error
}

type pageRequestStateKey struct{}

func withPageRequestState(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if pageRequestStateFromContext(r.Context()) != nil {
			handler.ServeHTTP(w, r)
			return
		}
		state := &pageRequestState{
			replayScopes: make(map[string]pageScopeResult),
			metrics:      make(map[string]int),
		}
		handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), pageRequestStateKey{}, state)))
	})
}

func pageRequestStateFromContext(ctx context.Context) *pageRequestState {
	state, _ := ctx.Value(pageRequestStateKey{}).(*pageRequestState)
	return state
}

func (s *pageRequestState) inc(metric string) {
	if s == nil || metric == "" {
		return
	}
	s.metrics[metric]++
}

func (h *Handler) defaultPageScope(ctx context.Context) (pageScope, error) {
	if h == nil || h.db == nil {
		return pageScope{}, nil
	}
	if state := pageRequestStateFromContext(ctx); state != nil && state.defaultScope.loaded {
		state.inc("default_page_scope.cache_hit")
		return state.defaultScope.scope, state.defaultScope.err
	}

	var (
		scope pageScope
		err   error
	)
	if principal := auth.PrincipalFromContext(ctx); principal != nil && principal.User != nil && principal.User.ID != "" {
		err = h.db.QueryRowContext(ctx,
			`SELECT p.id, p.slug, o.id, o.slug
			 FROM organization_members m
			 JOIN organizations o ON o.id = m.organization_id
			 JOIN projects p ON p.organization_id = o.id
			 WHERE m.user_id = ?
			 ORDER BY COALESCE(p.created_at, ''), p.id
			 LIMIT 1`,
			principal.User.ID,
		).Scan(&scope.ProjectID, &scope.ProjectSlug, &scope.OrganizationID, &scope.OrganizationSlug)
		if err == nil {
			return h.cacheDefaultPageScope(ctx, scope, nil)
		}
		if err != sql.ErrNoRows {
			return h.cacheDefaultPageScope(ctx, pageScope{}, err)
		}

		err = h.db.QueryRowContext(ctx,
			`SELECT o.id, o.slug
			 FROM organization_members m
			 JOIN organizations o ON o.id = m.organization_id
			 WHERE m.user_id = ?
			 ORDER BY COALESCE(m.created_at, ''), o.id
			 LIMIT 1`,
			principal.User.ID,
		).Scan(&scope.OrganizationID, &scope.OrganizationSlug)
		if err == nil {
			return h.cacheDefaultPageScope(ctx, scope, nil)
		}
		if err != sql.ErrNoRows {
			return h.cacheDefaultPageScope(ctx, pageScope{}, err)
		}
		return h.cacheDefaultPageScope(ctx, pageScope{}, nil)
	}

	if h.webStore == nil {
		return h.cacheDefaultPageScope(ctx, pageScope{}, nil)
	}
	projectID, err := h.webStore.DefaultProjectID(ctx)
	if err != nil || projectID == "" {
		return h.cacheDefaultPageScope(ctx, pageScope{}, err)
	}
	err = h.db.QueryRowContext(ctx,
		`SELECT p.id, p.slug, o.id, o.slug
		 FROM projects p
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE p.id = ?`,
		projectID,
	).Scan(&scope.ProjectID, &scope.ProjectSlug, &scope.OrganizationID, &scope.OrganizationSlug)
	if err == sql.ErrNoRows {
		return h.cacheDefaultPageScope(ctx, pageScope{}, nil)
	}
	if err != nil {
		return h.cacheDefaultPageScope(ctx, pageScope{}, err)
	}
	return h.cacheDefaultPageScope(ctx, scope, nil)
}

func (h *Handler) cacheDefaultPageScope(ctx context.Context, scope pageScope, err error) (pageScope, error) {
	if state := pageRequestStateFromContext(ctx); state != nil {
		state.defaultScope = pageScopeResult{loaded: true, scope: scope, err: err}
		state.inc("default_page_scope.query")
	}
	return scope, err
}

func (h *Handler) guardProjectQueryPage(w http.ResponseWriter, r *http.Request, workload sqlite.QueryWorkload, limit int, query string, detail bool) (pageScope, bool) {
	scope, err := h.defaultPageScope(r.Context())
	if err != nil {
		http.Error(w, "Failed to resolve default project scope.", http.StatusInternalServerError)
		return pageScope{}, false
	}
	if h.authz == nil {
		return scope, true
	}
	if err := h.authz.AuthorizeProject(r, scope.ProjectID, auth.ScopeProjectRead); err != nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return pageScope{}, false
	}
	decision, err := h.queryGuard.CheckAndRecord(r.Context(), sqlite.QueryGuardRequest{
		Principal:      auth.PrincipalFromContext(r.Context()),
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		RequestPath:    r.URL.Path,
		RequestMethod:  r.Method,
		IPAddress:      r.RemoteAddr,
		UserAgent:      r.UserAgent(),
		Estimate: sqlite.QueryEstimate{
			Workload: workload,
			Limit:    limit,
			Query:    query,
			Detail:   detail,
		},
	})
	if err != nil {
		http.Error(w, "Failed to apply query guardrails.", http.StatusInternalServerError)
		return pageScope{}, false
	}
	if decision.Allowed {
		return scope, true
	}
	if decision.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(decision.RetryAfter.Seconds()))))
	}
	http.Error(w, decision.Reason, decision.StatusCode)
	return pageScope{}, false
}

func (h *Handler) resolveReplayScope(ctx context.Context, replayID string) (pageScope, error) {
	if h == nil || h.db == nil || strings.TrimSpace(replayID) == "" {
		return pageScope{}, nil
	}
	if state := pageRequestStateFromContext(ctx); state != nil {
		if result, ok := state.replayScopes[replayID]; ok && result.loaded {
			state.inc("replay_scope.cache_hit")
			return result.scope, result.err
		}
	}
	principal := auth.PrincipalFromContext(ctx)
	var scope pageScope
	var err error
	if principal != nil && principal.User != nil && principal.User.ID != "" {
		err = h.db.QueryRowContext(ctx,
			`SELECT p.id, p.slug, o.id, o.slug
			 FROM replay_manifests rm
			 JOIN projects p ON p.id = rm.project_id
			 JOIN organizations o ON o.id = p.organization_id
			 JOIN organization_members m ON m.organization_id = o.id
			 WHERE rm.replay_id = ? AND m.user_id = ?
			 ORDER BY COALESCE(rm.updated_at, rm.created_at, ''), rm.id
			 LIMIT 1`,
			replayID, principal.User.ID,
		).Scan(&scope.ProjectID, &scope.ProjectSlug, &scope.OrganizationID, &scope.OrganizationSlug)
		if err == sql.ErrNoRows {
			return h.cacheReplayScope(ctx, replayID, pageScope{}, nil)
		}
		return h.cacheReplayScope(ctx, replayID, scope, err)
	}
	err = h.db.QueryRowContext(ctx,
		`SELECT p.id, p.slug, o.id, o.slug
		 FROM replay_manifests rm
		 JOIN projects p ON p.id = rm.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE rm.replay_id = ?
		 ORDER BY COALESCE(rm.updated_at, rm.created_at, ''), rm.id
		 LIMIT 1`,
		replayID,
	).Scan(&scope.ProjectID, &scope.ProjectSlug, &scope.OrganizationID, &scope.OrganizationSlug)
	if err == sql.ErrNoRows {
		return h.cacheReplayScope(ctx, replayID, pageScope{}, nil)
	}
	return h.cacheReplayScope(ctx, replayID, scope, err)
}

func (h *Handler) cacheReplayScope(ctx context.Context, replayID string, scope pageScope, err error) (pageScope, error) {
	if state := pageRequestStateFromContext(ctx); state != nil {
		state.replayScopes[replayID] = pageScopeResult{loaded: true, scope: scope, err: err}
		state.inc("replay_scope.query")
	}
	return scope, err
}

func (h *Handler) guardReplayQueryPage(w http.ResponseWriter, r *http.Request, replayID string, limit int, detail bool) (pageScope, bool) {
	scope, err := h.resolveReplayScope(r.Context(), replayID)
	if err != nil {
		http.Error(w, "Failed to resolve replay scope.", http.StatusInternalServerError)
		return pageScope{}, false
	}
	if strings.TrimSpace(scope.ProjectID) == "" {
		http.NotFound(w, r)
		return pageScope{}, false
	}
	if h.authz == nil {
		return scope, true
	}
	if err := h.authz.AuthorizeProject(r, scope.ProjectID, auth.ScopeProjectRead); err != nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return pageScope{}, false
	}
	decision, err := h.queryGuard.CheckAndRecord(r.Context(), sqlite.QueryGuardRequest{
		Principal:      auth.PrincipalFromContext(r.Context()),
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		RequestPath:    r.URL.Path,
		RequestMethod:  r.Method,
		IPAddress:      r.RemoteAddr,
		UserAgent:      r.UserAgent(),
		Estimate: sqlite.QueryEstimate{
			Workload: sqlite.QueryWorkloadReplays,
			Limit:    limit,
			Query:    replayID,
			Detail:   detail,
		},
	})
	if err != nil {
		http.Error(w, "Failed to apply query guardrails.", http.StatusInternalServerError)
		return pageScope{}, false
	}
	if decision.Allowed {
		return scope, true
	}
	if decision.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(decision.RetryAfter.Seconds()))))
	}
	http.Error(w, decision.Reason, decision.StatusCode)
	return pageScope{}, false
}
