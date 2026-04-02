// Package api implements the Sentry-compatible REST API endpoints.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Pagination
// ---------------------------------------------------------------------------

// defaultPageSize is the default number of items per page.
const defaultPageSize = 100

// Paginate applies cursor-based pagination to a slice. It parses the cursor
// query parameter and generates a Link header pointing to the next page. The
// cursor is a simple 0-based offset encoded as a string.
func Paginate[T any](w http.ResponseWriter, r *http.Request, items []T) []T {
	cursor := r.URL.Query().Get("cursor")
	offset := 0
	if cursor != "" {
		if n, err := strconv.Atoi(cursor); err == nil && n > 0 {
			offset = n
		}
	}

	if offset >= len(items) {
		setLinkHeader(w, r, "", false)
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
		nextCursor = strconv.Itoa(end)
	}
	setLinkHeader(w, r, nextCursor, hasNext)
	return items[offset:end]
}

// setLinkHeader sets a RFC 5988 Link header for pagination.
func setLinkHeader(w http.ResponseWriter, r *http.Request, nextCursor string, hasNext bool) {
	base := r.URL.Path
	parts := []string{
		fmt.Sprintf(`<%s?cursor=0:0:1>; rel="previous"; results="false"; cursor="0:0:1"`, base),
	}
	if hasNext && nextCursor != "" {
		parts = append(parts, fmt.Sprintf(
			`<%s?cursor=%s:0:0>; rel="next"; results="true"; cursor="%s:0:0"`,
			base, nextCursor, nextCursor,
		))
	} else {
		parts = append(parts, fmt.Sprintf(
			`<%s?cursor=0:0:0>; rel="next"; results="false"; cursor="0:0:0"`,
			base,
		))
	}
	w.Header().Set("Link", strings.Join(parts, ", "))
}

// PaginationOpts holds the parsed offset and limit for DB-level pagination.
type PaginationOpts struct {
	Offset int
	Limit  int
}

// ParsePagination extracts pagination parameters from the request.
// It reads cursor (offset) and per_page (limit, default 100, max 100).
func ParsePagination(r *http.Request) PaginationOpts {
	cursor := r.URL.Query().Get("cursor")
	offset := 0
	if cursor != "" {
		if n, err := strconv.Atoi(cursor); err == nil && n > 0 {
			offset = n
		}
	}
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
	if hasNext {
		items = items[:opts.Limit]
	}
	nextCursor := ""
	if hasNext {
		nextCursor = strconv.Itoa(opts.Offset + opts.Limit)
	}
	setLinkHeader(w, r, nextCursor, hasNext)
	return items
}

// ---------------------------------------------------------------------------
// Path parameter helpers
// ---------------------------------------------------------------------------

// PathParam extracts a named wildcard from the request using Go 1.22+ ServeMux.
func PathParam(r *http.Request, name string) string {
	return r.PathValue(name)
}

// decodeJSON reads and decodes a JSON request body into v.
func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return fmt.Errorf("empty request body")
	}
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
