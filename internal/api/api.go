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
