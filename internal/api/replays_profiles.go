package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
	sharedstore "urgentry/internal/store"
	"urgentry/internal/telemetryquery"
)

func handleListReplays(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectID(w, r, db)
		if !ok {
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
		if !enforceQueryGuard(w, r, guard, org.ID, projectID, sqlite.QueryEstimate{
			Workload: sqlite.QueryWorkloadReplays,
			Limit:    100,
		}) {
			return
		}
		items, err := queries.ListReplays(r.Context(), projectID, 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list replays.")
			return
		}
		resp := make([]Replay, 0, len(items))
		for _, item := range items {
			resp = append(resp, mapReplayManifest(item))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleGetReplay(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectID(w, r, db)
		if !ok {
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
		if !enforceQueryGuard(w, r, guard, org.ID, projectID, sqlite.QueryEstimate{
			Workload: sqlite.QueryWorkloadReplays,
			Limit:    1,
			Detail:   true,
		}) {
			return
		}
		item, err := queries.GetReplay(r.Context(), projectID, PathParam(r, "replay_id"))
		if err != nil {
			if errors.Is(err, sharedstore.ErrNotFound) {
				httputil.WriteError(w, http.StatusNotFound, "Replay not found.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load replay.")
			return
		}
		if item == nil {
			httputil.WriteError(w, http.StatusNotFound, "Replay not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapReplayRecord(item, true))
	}
}

func handleListProfiles(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectID(w, r, db)
		if !ok {
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
		if !enforceQueryGuard(w, r, guard, org.ID, projectID, sqlite.QueryEstimate{
			Workload: sqlite.QueryWorkloadProfiles,
			Limit:    100,
		}) {
			return
		}
		items, err := queries.ListProfiles(r.Context(), projectID, 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list profiles.")
			return
		}
		resp := make([]Profile, 0, len(items))
		for _, item := range items {
			resp = append(resp, mapProfileManifest(item))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleGetProfile(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectID(w, r, db)
		if !ok {
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
		if !enforceQueryGuard(w, r, guard, org.ID, projectID, sqlite.QueryEstimate{
			Workload: sqlite.QueryWorkloadProfiles,
			Limit:    1,
			Detail:   true,
		}) {
			return
		}
		item, err := queries.GetProfile(r.Context(), projectID, PathParam(r, "profile_id"))
		if err != nil {
			if errors.Is(err, sharedstore.ErrNotFound) {
				httputil.WriteError(w, http.StatusNotFound, "Profile not found.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load profile.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapProfileRecord(item))
	}
}

func mapReplayManifest(item sharedstore.ReplayManifest) Replay {
	var startedAt, finishedAt *time.Time
	if !item.StartedAt.IsZero() {
		t := item.StartedAt
		startedAt = &t
	}
	if !item.EndedAt.IsZero() {
		t := item.EndedAt
		finishedAt = &t
	}
	urls := replayURLs(item)
	releases := replayReleases(item)
	resp := Replay{
		ID:              item.ReplayID,
		Title:           replayManifestTitle(item),
		ProjectID:       item.ProjectID,
		TraceIDs:        nonNilStringSlice(item.TraceIDs),
		ErrorIDs:        nonNilStringSlice(item.LinkedEventIDs),
		URLs:            urls,
		Releases:        releases,
		ReplayType:      replayType(item),
		Platform:        item.Platform,
		Environment:     item.Environment,
		Duration:        item.DurationMS / 1000,
		CountErrors:     item.ErrorMarkerCount,
		CountSegments:   item.AssetCount,
		CountURLs:       len(urls),
		CountDeadClicks: 0,
		CountRageClicks: 0,
		Activity:        replayActivity(item),
		StartedAt:       startedAt,
		FinishedAt:      finishedAt,
		User:            replayUser(item.UserRef),
		SDK:             nil,
		OS:              nil,
		Browser:         nil,
		Device:          nil,
	}
	return resp
}

func mapReplayRecord(item *sharedstore.ReplayRecord, includePayload bool) Replay {
	resp := mapReplayManifest(item.Manifest)
	resp.Attachments = mapReplayAttachments(item.Manifest, item.Assets)
	if len(item.Assets) > 0 {
		resp.CountSegments = len(item.Assets)
	}
	if includePayload {
		resp.Payload = item.Payload
	}
	return resp
}

func replayManifestTitle(item sharedstore.ReplayManifest) string {
	if strings.TrimSpace(item.RequestURL) != "" {
		return "Replay of " + strings.TrimSpace(item.RequestURL)
	}
	return "Replay"
}

func replayUser(ref sharedstore.ReplayUserRef) *ReplayUser {
	if ref.ID == "" && ref.Email == "" && ref.Username == "" {
		return nil
	}
	name := ref.Username
	if name == "" {
		name = ref.Email
	}
	return &ReplayUser{
		ID:       ref.ID,
		Email:    ref.Email,
		Username: ref.Username,
		Name:     name,
	}
}

func replayURLs(item sharedstore.ReplayManifest) []string {
	if item.RequestURL != "" {
		return []string{item.RequestURL}
	}
	return []string{}
}

func replayReleases(item sharedstore.ReplayManifest) []string {
	if item.Release != "" {
		return []string{item.Release}
	}
	return nil
}

func replayType(item sharedstore.ReplayManifest) string {
	if item.AssetCount > 0 {
		return "session"
	}
	return "buffer"
}

func replayActivity(item sharedstore.ReplayManifest) float64 {
	total := item.ClickCount + item.NavigationCount + item.ErrorMarkerCount + item.ConsoleCount
	if total == 0 {
		return 0
	}
	score := float64(total) / 10.0
	if score > 10 {
		score = 10
	}
	return score
}

func nonNilStringSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func mapReplayAttachments(manifest sharedstore.ReplayManifest, assets []sharedstore.ReplayAssetRef) []Attachment {
	result := make([]Attachment, 0, len(assets))
	for _, asset := range assets {
		result = append(result, Attachment{
			ID:          asset.AttachmentID,
			EventID:     manifest.ReplayID,
			ProjectID:   manifest.ProjectID,
			Name:        asset.Name,
			ContentType: asset.ContentType,
			Size:        asset.SizeBytes,
			DateCreated: asset.CreatedAt,
		})
	}
	return result
}

func mapProfileManifest(item sharedstore.ProfileManifest) Profile {
	resp := Profile{
		ID:          item.ProfileID,
		ProjectID:   item.ProjectID,
		ProfileID:   item.ProfileID,
		Transaction: item.Transaction,
		TraceID:     item.TraceID,
		Platform:    item.Platform,
		Release:     item.Release,
		Environment: item.Environment,
		DurationNS:  strconv.FormatInt(item.DurationNS, 10),
		DateCreated: item.StartedAt,
		Summary: ProfileSummary{
			Transaction:   item.Transaction,
			TraceID:       item.TraceID,
			Platform:      item.Platform,
			Release:       item.Release,
			Environment:   item.Environment,
			DurationNS:    strconv.FormatInt(item.DurationNS, 10),
			SampleCount:   item.SampleCount,
			FrameCount:    item.FrameCount,
			FunctionCount: item.FunctionCount,
		},
	}
	if resp.DateCreated.IsZero() {
		resp.DateCreated = item.DateCreated
	}
	return resp
}

func mapProfileRecord(item *sharedstore.ProfileRecord) Profile {
	resp := mapProfileManifest(item.Manifest)
	resp.Payload = item.RawPayload
	resp.Summary.TopFrames = mapProfileBreakdowns(item.TopFrames)
	resp.Summary.TopFunctions = mapProfileBreakdowns(item.TopFunctions)
	return resp
}

func mapProfileBreakdowns(items []sharedstore.ProfileBreakdown) []ProfileBreakdown {
	result := make([]ProfileBreakdown, 0, len(items))
	for _, item := range items {
		result = append(result, ProfileBreakdown(item))
	}
	return result
}


func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
