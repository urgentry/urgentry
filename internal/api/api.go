// Package api implements the Sentry-compatible REST API endpoints.
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Pagination
// ---------------------------------------------------------------------------

// defaultPageSize is the default number of items per page.
const defaultPageSize = 100

// parseCompoundCursor extracts the offset from a Sentry-compatible compound
// cursor of the form "{timestamp}:{offset}:{is_prev}". If the cursor is a
// plain integer (legacy format) it is accepted as well for backward
// compatibility. Returns 0 when the cursor is empty or unparseable.
func parseCompoundCursor(raw string) int {
	if raw == "" {
		return 0
	}
	// Try compound format first: ts:offset:isPrev
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) == 3 {
		if n, err := strconv.Atoi(parts[1]); err == nil && n > 0 {
			return n
		}
	}
	// Fall back to plain integer for backward compatibility.
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return n
	}
	return 0
}

// Paginate applies cursor-based pagination to a slice. It parses the cursor
// query parameter (supporting both plain integer and Sentry-compatible compound
// "{ts}:{offset}:{is_prev}" formats) and generates a Link header pointing to
// the next page.
func Paginate[T any](w http.ResponseWriter, r *http.Request, items []T) []T {
	offset := parseCompoundCursor(r.URL.Query().Get("cursor"))
	prevOffset := offset - defaultPageSize
	if prevOffset < 0 {
		prevOffset = 0
	}
	prevCursor := formatPaginationCursor(prevOffset, true)
	hasPrev := offset > 0

	if offset >= len(items) {
		setLinkHeader(w, r, prevCursor, hasPrev, "", false)
		return nil
	}

	end := offset + defaultPageSize
	hasNext := false
	if end < len(items) {
		hasNext = true
	} else {
		end = len(items)
	}

	nextCursor := ""
	if hasNext {
		nextCursor = formatPaginationCursor(end, false)
	}
	setLinkHeader(w, r, prevCursor, hasPrev, nextCursor, hasNext)
	return items[offset:end]
}

// setLinkHeader sets a RFC 5988 Link header for pagination.
func setLinkHeader(w http.ResponseWriter, r *http.Request, prevCursor string, hasPrev bool, nextCursor string, hasNext bool) {
	if prevCursor == "" {
		prevCursor = formatPaginationCursor(0, true)
	}
	if nextCursor == "" {
		nextCursor = formatPaginationCursor(0, false)
	}
	parts := []string{
		fmt.Sprintf(
			`<%s>; rel="previous"; results="%t"; cursor="%s"`,
			paginationLinkPath(r, prevCursor), hasPrev, prevCursor,
		),
	}
	parts = append(parts, fmt.Sprintf(
		`<%s>; rel="next"; results="%t"; cursor="%s"`,
		paginationLinkPath(r, nextCursor), hasNext, nextCursor,
	))
	w.Header().Set("Link", strings.Join(parts, ", "))
}

func formatPaginationCursor(offset int, previous bool) string {
	if offset < 0 {
		offset = 0
	}
	flag := 0
	if previous {
		flag = 1
	}
	return fmt.Sprintf("0:%d:%d", offset, flag)
}

func paginationLinkPath(r *http.Request, cursor string) string {
	query := r.URL.Query()
	query.Set("cursor", cursor)
	return r.URL.Path + "?" + query.Encode()
}

// PaginationOpts holds the parsed offset and limit for DB-level pagination.
type PaginationOpts struct {
	Offset int
	Limit  int
}

// ParsePagination extracts pagination parameters from the request.
// It reads cursor (offset, supporting both plain integer and compound
// "{ts}:{offset}:{is_prev}" formats) and per_page (limit, default 100, max 100).
func ParsePagination(r *http.Request) PaginationOpts {
	offset := parseCompoundCursor(r.URL.Query().Get("cursor"))
	limit := defaultPageSize
	if pp := r.URL.Query().Get("per_page"); pp != "" {
		if n, err := strconv.Atoi(pp); err == nil && n > 0 && n <= defaultPageSize {
			limit = n
		}
	}
	return PaginationOpts{Offset: offset, Limit: limit}
}

// SetPaginationHeaders sets RFC 5988 Link headers based on DB query results.
// count is the number of rows returned (may be limit+1 to detect next page).
func SetPaginationHeaders[T any](w http.ResponseWriter, r *http.Request, items []T, opts PaginationOpts) []T {
	hasNext := len(items) > opts.Limit
	hasPrev := opts.Offset > 0
	if hasNext {
		items = items[:opts.Limit]
	}
	prevOffset := opts.Offset - opts.Limit
	if prevOffset < 0 {
		prevOffset = 0
	}
	nextCursor := ""
	if hasNext {
		nextCursor = formatPaginationCursor(opts.Offset+opts.Limit, false)
	}
	setLinkHeader(w, r, formatPaginationCursor(prevOffset, true), hasPrev, nextCursor, hasNext)
	return items
}

// ---------------------------------------------------------------------------
// Path parameter helpers
// ---------------------------------------------------------------------------

// PathParam extracts a named wildcard from the request using Go 1.22+ ServeMux.
func PathParam(r *http.Request, name string) string {
	return r.PathValue(name)
}

const maxAPIBodySize = 2 << 20 // 2 MB

// decodeJSON reads and decodes a JSON request body into v.
func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return fmt.Errorf("empty request body")
	}
	limited := io.LimitReader(r.Body, maxAPIBodySize)
	defer r.Body.Close()
	return json.NewDecoder(limited).Decode(v)
}
