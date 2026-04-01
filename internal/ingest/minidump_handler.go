package ingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"urgentry/internal/httputil"
	"urgentry/internal/middleware"
	"urgentry/internal/sqlite"
	"urgentry/pkg/id"
)

const maxMinidumpBodySize = 32 << 20 // 32 MB

// MinidumpHandlerWithDeps handles POST /api/{project_id}/minidump/.
// It stages a native crash receipt, stores the raw dump, and enqueues
// async stackwalking so native crashes enter the normal issue workflow
// without blocking the ingest request on symbolication.
func MinidumpHandlerWithDeps(deps IngestDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := middleware.LogFromCtx(r.Context())

		if r.Method != http.MethodPost {
			httputil.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxMinidumpBodySize)
		if err := r.ParseMultipartForm(maxMinidumpBodySize); err != nil {
			if deps.Metrics != nil {
				deps.Metrics.RecordIngest(0, err)
			}
			httputil.WriteError(w, http.StatusBadRequest, "invalid multipart form")
			return
		}

		file, header, err := minidumpFileFromRequest(r)
		if err != nil {
			if deps.Metrics != nil {
				deps.Metrics.RecordIngest(0, err)
			}
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		defer file.Close()

		payload, err := io.ReadAll(file)
		if err != nil {
			if deps.Metrics != nil {
				deps.Metrics.RecordIngest(0, err)
			}
			httputil.WriteError(w, http.StatusBadRequest, "failed to read minidump")
			return
		}
		if len(payload) == 0 {
			if deps.Metrics != nil {
				deps.Metrics.RecordIngest(0, errEmptyBody)
			}
			httputil.WriteError(w, http.StatusBadRequest, "empty minidump payload")
			return
		}

		projectID := r.PathValue("project_id")
		if projectID == "" {
			projectID = "1"
		}
		eventPayload, eventID, err := buildMinidumpEvent(r, header)
		if err != nil {
			if deps.Metrics != nil {
				deps.Metrics.RecordIngest(len(payload), err)
			}
			httputil.WriteError(w, http.StatusBadRequest, "invalid minidump metadata")
			return
		}
		if deps.NativeCrashes == nil {
			if deps.Metrics != nil {
				deps.Metrics.RecordIngest(len(payload), errors.New("native crash store unavailable"))
			}
			httputil.WriteError(w, http.StatusServiceUnavailable, "native crash ingest unavailable")
			return
		}

		crash, _, err := deps.NativeCrashes.IngestMinidump(r.Context(), sqlite.MinidumpReceiptInput{
			ProjectID:   projectID,
			EventID:     eventID,
			Filename:    header.Filename,
			ContentType: minidumpContentType(header),
			Dump:        payload,
			EventJSON:   eventPayload,
		})
		if err != nil {
			if deps.Metrics != nil {
				deps.Metrics.RecordIngest(len(payload), err)
			}
			if errors.Is(err, sqlite.ErrNativeCrashQueueFull) {
				httputil.WriteError(w, http.StatusServiceUnavailable, "native stackwalk queue is full, retry later")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "failed to stage native crash")
			return
		}

		if deps.Metrics != nil {
			deps.Metrics.RecordIngest(len(payload), nil)
		}

		l.Info().
			Str("project_id", projectID).
			Str("event_id", eventID).
			Str("crash_id", crash.ID).
			Str("filename", header.Filename).
			Msg("minidump ingested")
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"id": eventID})
	})
}

func minidumpFileFromRequest(r *http.Request) (multipart.File, *multipart.FileHeader, error) {
	if file, header, err := r.FormFile("upload_file_minidump"); err == nil {
		return file, header, nil
	}
	return nil, nil, fmt.Errorf("missing minidump upload")
}

func buildMinidumpEvent(r *http.Request, header *multipart.FileHeader) ([]byte, string, error) {
	payload := map[string]any{
		"event_id": normalizeEventIDField(r.FormValue("event_id")),
		"platform": "native",
		"level":    "fatal",
		"message":  "Native crash",
		"tags": map[string]string{
			"ingest.kind": "minidump",
		},
		"extra": map[string]any{
			"minidump.filename": header.Filename,
			"minidump.size":     header.Size,
		},
		"exception": map[string]any{
			"values": []map[string]any{{
				"type":  "Minidump",
				"value": "Native crash",
				"mechanism": map[string]any{
					"type":    "minidump",
					"handled": false,
				},
			}},
		},
	}

	if header.Header.Get("Content-Type") != "" {
		payload["tags"].(map[string]string)["minidump.content_type"] = header.Header.Get("Content-Type")
	}

	if sentry := strings.TrimSpace(r.FormValue("sentry")); sentry != "" {
		var incoming map[string]any
		if err := json.Unmarshal([]byte(sentry), &incoming); err != nil {
			return nil, "", err
		}
		mergeMinidumpEvent(payload, incoming)
	}

	if release := strings.TrimSpace(r.FormValue("release")); release != "" {
		payload["release"] = release
	}
	if environment := strings.TrimSpace(r.FormValue("environment")); environment != "" {
		payload["environment"] = environment
	}
	if platform := strings.TrimSpace(r.FormValue("platform")); platform != "" {
		payload["platform"] = platform
	}
	if message := strings.TrimSpace(r.FormValue("message")); message != "" {
		payload["message"] = message
	}
	applyMinidumpHints(payload, r)

	eventID, _ := payload["event_id"].(string)
	if eventID == "" {
		eventID = id.New()
		payload["event_id"] = eventID
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}
	return body, eventID, nil
}

func mergeMinidumpEvent(dst, src map[string]any) {
	for _, key := range []string{"release", "environment", "platform", "message", "level", "dist", "user", "request", "contexts", "debug_meta", "threads", "tags", "extra", "sdk"} {
		if value, ok := src[key]; ok {
			dst[key] = value
		}
	}
	if eventID, ok := src["event_id"].(string); ok {
		dst["event_id"] = normalizeEventIDField(eventID)
	}
	if exception, ok := src["exception"]; ok {
		dst["exception"] = exception
	}
}

func applyMinidumpHints(payload map[string]any, r *http.Request) {
	debugID := strings.TrimSpace(r.FormValue("debug_id"))
	if debugID == "" {
		debugID = strings.TrimSpace(r.FormValue("uuid"))
	}
	codeID := strings.TrimSpace(r.FormValue("code_id"))
	instructionAddr := strings.TrimSpace(r.FormValue("instruction_addr"))
	module := strings.TrimSpace(r.FormValue("module"))
	function := strings.TrimSpace(r.FormValue("function"))
	filename := strings.TrimSpace(r.FormValue("filename"))
	packageID := strings.TrimSpace(r.FormValue("package"))
	if packageID == "" {
		packageID = codeID
	}

	tags, _ := payload["tags"].(map[string]string)
	if tags == nil {
		tags = map[string]string{}
		payload["tags"] = tags
	}
	if debugID != "" {
		tags["minidump.debug_id"] = debugID
	}
	if codeID != "" {
		tags["minidump.code_id"] = codeID
	}

	if instructionAddr == "" && debugID == "" && codeID == "" && module == "" && function == "" && filename == "" {
		return
	}

	values, ok := extractExceptionValues(payload["exception"])
	if !ok || len(values) == 0 {
		values = []map[string]any{{
			"type":  "NativeCrash",
			"value": "Native crash",
			"mechanism": map[string]any{
				"type":    "minidump",
				"handled": false,
			},
		}}
	}

	for i := range values {
		stacktrace, _ := values[i]["stacktrace"].(map[string]any)
		if stacktrace == nil {
			stacktrace = map[string]any{}
		}
		if frames, _ := stacktrace["frames"].([]any); len(frames) > 0 {
			continue
		}
		frame := map[string]any{}
		if filename != "" {
			frame["filename"] = filename
		}
		if module != "" {
			frame["module"] = module
		}
		if function != "" {
			frame["function"] = function
		}
		if instructionAddr != "" {
			frame["instruction_addr"] = instructionAddr
		}
		if debugID != "" {
			frame["debug_id"] = debugID
		}
		if packageID != "" {
			frame["package"] = packageID
		}
		if line := strings.TrimSpace(r.FormValue("lineno")); line != "" {
			if value, err := strconv.Atoi(line); err == nil && value > 0 {
				frame["lineno"] = value
			}
		}
		if len(frame) == 0 {
			continue
		}
		stacktrace["frames"] = []map[string]any{frame}
		values[i]["stacktrace"] = stacktrace
	}
	payload["exception"] = map[string]any{"values": values}
}

func extractExceptionValues(raw any) ([]map[string]any, bool) {
	exception, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	list, ok := exception["values"].([]any)
	if !ok {
		return nil, false
	}
	values := make([]map[string]any, 0, len(list))
	for _, item := range list {
		value, ok := item.(map[string]any)
		if !ok {
			continue
		}
		values = append(values, value)
	}
	return values, len(values) > 0
}

func normalizeEventIDField(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.ReplaceAll(raw, "-", "")
	raw = strings.ToLower(raw)
	if len(raw) > 32 {
		raw = raw[:32]
	}
	return raw
}

func minidumpContentType(header *multipart.FileHeader) string {
	if header == nil {
		return "application/x-dmp"
	}
	if contentType := strings.TrimSpace(header.Header.Get("Content-Type")); contentType != "" && contentType != "application/octet-stream" {
		return contentType
	}
	switch strings.ToLower(filepath.Ext(header.Filename)) {
	case ".dmp", ".mdmp":
		return "application/x-dmp"
	default:
		return "application/octet-stream"
	}
}
