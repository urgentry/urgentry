package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"urgentry/internal/httputil"
	"urgentry/internal/migration"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

const maxOrganizationImportBodySize = 128 << 20

// handleImport handles POST /api/0/organizations/{org_slug}/import/.
// It accepts a JSON import payload and applies it atomically.
func handleImport(db *sql.DB, importExport *sqlite.ImportExportStore, auth authFunc) http.HandlerFunc {
	return handleImportWithLimit(db, importExport, auth, maxOrganizationImportBodySize)
}

func handleImportWithLimit(db *sql.DB, importExport *sqlite.ImportExportStore, auth authFunc, maxBodyBytes int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		org, err := getOrganizationFromDB(r, db, orgSlug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load organization.")
			return
		}
		if org == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}

		var payload migration.ImportPayload
		if err := decodeImportPayloadWithLimit(w, r, &payload, maxBodyBytes); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				httputil.WriteError(w, http.StatusRequestEntityTooLarge, "import payload exceeds size limit")
				return
			}
			httputil.WriteError(w, http.StatusBadRequest, "parse import payload: "+err.Error())
			return
		}

		dryRun := truthyQueryValue(r.URL.Query().Get("dry_run"))
		var result *migration.ImportResult
		if dryRun {
			result, err = importExport.ValidateOrganizationPayload(r.Context(), org.ID, orgSlug, payload)
		} else {
			result, err = importExport.ImportOrganizationPayload(r.Context(), org.ID, orgSlug, payload)
		}
		if err != nil {
			if sqlite.IsImportValidationError(err) {
				httputil.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to import organization data.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, result)
	}
}

func decodeImportPayload(w http.ResponseWriter, r *http.Request, v any) error {
	return decodeImportPayloadWithLimit(w, r, v, maxOrganizationImportBodySize)
}

func decodeImportPayloadWithLimit(w http.ResponseWriter, r *http.Request, v any, maxBytes int64) error {
	if r.Body == nil {
		return errors.New("empty request body")
	}
	defer r.Body.Close()

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("request body must contain a single JSON object")
	}
	var trailing any
	if err := dec.Decode(&trailing); err == nil {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

func truthyQueryValue(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// handleExport handles GET /api/0/organizations/{org_slug}/export/.
// It returns all projects and releases for the organization.
func handleExport(db *sql.DB, importExport *sqlite.ImportExportStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		org, err := getOrganizationFromDB(r, db, orgSlug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load organization.")
			return
		}
		if org == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := importExport.WriteOrganizationPayloadJSON(r.Context(), orgSlug, w); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to export organization data.")
			return
		}
	}
}

// searchResult is returned by the search API endpoint.
type searchResult struct {
	Issues  []searchIssue  `json:"issues"`
	Pages   []searchPage   `json:"pages"`
	Actions []searchAction `json:"actions"`
}

type searchIssue struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

type searchPage struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type searchAction struct {
	Title string `json:"title"`
	Key   string `json:"key"`
}

type issueSearcher interface {
	SearchIssues(ctx context.Context, rawQuery string, limit int) ([]store.WebIssue, error)
}

// HandleSearch handles GET /api/search?q= for the command palette.
// It queries the groups table with title LIKE and returns top 5 matches
// plus static page/action entries. Exported for use by the web handler.
func HandleSearch(searcher issueSearcher) http.HandlerFunc {
	staticPages := []searchPage{
		{Title: "Dashboard", URL: "/"},
		{Title: "Discover", URL: "/discover/"},
		{Title: "Dashboards", URL: "/dashboards/"},
		{Title: "Logs", URL: "/logs/"},
		{Title: "Replays", URL: "/replays/"},
		{Title: "Profiles", URL: "/profiles/"},
		{Title: "Issues", URL: "/issues/"},
		{Title: "Releases", URL: "/releases/"},
		{Title: "Alerts", URL: "/alerts/"},
		{Title: "Monitors", URL: "/monitors/"},
		{Title: "Feedback", URL: "/feedback/"},
		{Title: "Operator", URL: "/ops/"},
		{Title: "Settings", URL: "/settings/"},
	}

	staticActions := []searchAction{
		{Title: "Resolve selected", Key: "r"},
		{Title: "Ignore selected", Key: "i"},
		{Title: "Reopen selected", Key: "o"},
	}

	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		result := searchResult{
			Issues:  []searchIssue{},
			Pages:   []searchPage{},
			Actions: []searchAction{},
		}

		// Filter pages matching query.
		for _, p := range staticPages {
			if query == "" || containsInsensitive(p.Title, query) {
				result.Pages = append(result.Pages, p)
			}
		}

		// Filter actions matching query.
		for _, a := range staticActions {
			if query == "" || containsInsensitive(a.Title, query) {
				result.Actions = append(result.Actions, a)
			}
		}

		// Search issues from DB.
		if searcher != nil && query != "" {
			issues, err := searcher.SearchIssues(r.Context(), query, 5)
			if err == nil {
				for _, item := range issues {
					iss := searchIssue{
						ID:    item.ID,
						Title: item.Title,
					}
					iss.URL = "/issues/" + iss.ID + "/"
					result.Issues = append(result.Issues, iss)
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(result)
	}
}

// containsInsensitive checks if s contains substr (case-insensitive).
func containsInsensitive(s, substr string) bool {
	sl := len(s)
	subl := len(substr)
	if subl > sl {
		return false
	}
	for i := 0; i <= sl-subl; i++ {
		match := true
		for j := 0; j < subl; j++ {
			a, b := s[i+j], substr[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
