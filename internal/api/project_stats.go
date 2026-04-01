package api

import (
	"database/sql"
	"net/http"
	"time"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
)

// statBucket is a single time-series bucket returned from the stats endpoint.
type statBucket struct {
	Timestamp int64 `json:"ts"`
	Count     int   `json:"count"`
}

// handleGetProjectStats handles GET /api/0/projects/{org}/{proj}/stats/.
// Returns event counts bucketed by day (or hour via ?resolution=1h) for the
// last 30 days.
func handleGetProjectStats(
	db *sql.DB,
	catalog controlplane.CatalogStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		project, ok := getProjectFromCatalog(w, r, catalog, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
		if !ok {
			return
		}

		resolution := r.URL.Query().Get("resolution")
		if resolution == "" {
			resolution = "1d"
		}

		days := 30
		var sqlFmt string // SQLite strftime format
		var goFmt string  // Go time.Format equivalent
		var step time.Duration
		switch resolution {
		case "1h":
			sqlFmt = "%Y-%m-%d %H"
			goFmt = "2006-01-02 15"
			step = time.Hour
			days = 2 // limit hourly to 48 hours
		default:
			sqlFmt = "%Y-%m-%d"
			goFmt = "2006-01-02"
			step = 24 * time.Hour
		}

		since := time.Now().UTC().Truncate(step).Add(-time.Duration(days) * 24 * time.Hour)

		rows, err := db.QueryContext(r.Context(),
			`SELECT strftime(?, ingested_at) AS bucket, COUNT(*)
			 FROM events
			 WHERE project_id = ? AND ingested_at >= ?
			 GROUP BY bucket ORDER BY bucket`,
			sqlFmt, project.ID, since.Format(time.RFC3339))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to query event stats.")
			return
		}
		defer rows.Close()

		counts := map[string]int{}
		for rows.Next() {
			var bucket string
			var count int
			if err := rows.Scan(&bucket, &count); err != nil {
				continue
			}
			counts[bucket] = count
		}

		// Build a contiguous series.
		var result []statBucket
		for t := since; t.Before(time.Now().UTC()); t = t.Add(step) {
			key := t.Format(goFmt)
			result = append(result, statBucket{
				Timestamp: t.Unix(),
				Count:     counts[key],
			})
		}
		if result == nil {
			result = []statBucket{}
		}
		httputil.WriteJSON(w, http.StatusOK, result)
	}
}

// projectUserResponse is a distinct user seen in events.
type projectUserResponse struct {
	ID         string `json:"id"`
	Username   string `json:"username,omitempty"`
	Email      string `json:"email,omitempty"`
	IPAddress  string `json:"ipAddress,omitempty"`
	Name       string `json:"name,omitempty"`
	AvatarURL  string `json:"avatarUrl,omitempty"`
	DateSeen   string `json:"dateSeen"`
	EventCount int    `json:"eventCount"`
}

// handleListProjectUsers handles GET /api/0/projects/{org}/{proj}/users/.
// Returns distinct user identifiers from events.
func handleListProjectUsers(
	db *sql.DB,
	catalog controlplane.CatalogStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		project, ok := getProjectFromCatalog(w, r, catalog, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
		if !ok {
			return
		}

		rows, err := db.QueryContext(r.Context(),
			`SELECT
				COALESCE(user_identifier, ''),
				MIN(ingested_at) AS first_seen,
				COUNT(*) AS cnt
			 FROM events
			 WHERE project_id = ? AND COALESCE(user_identifier, '') != ''
			 GROUP BY user_identifier
			 ORDER BY cnt DESC
			 LIMIT 100`, project.ID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to query project users.")
			return
		}
		defer rows.Close()

		var users []projectUserResponse
		for rows.Next() {
			var u projectUserResponse
			var firstSeen sql.NullString
			if err := rows.Scan(&u.ID, &firstSeen, &u.EventCount); err != nil {
				continue
			}
			if firstSeen.Valid {
				u.DateSeen = firstSeen.String
			}
			users = append(users, u)
		}
		if users == nil {
			users = []projectUserResponse{}
		}
		httputil.WriteJSON(w, http.StatusOK, users)
	}
}
