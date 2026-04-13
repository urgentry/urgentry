package httputil

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
)

// APIError is the canonical JSON error contract for API and middleware callers.
type APIError struct {
	Status int
	Code   string
	Detail string
}

// APIErrorBody is the JSON response envelope emitted for API errors.
type APIErrorBody struct {
	Detail string `json:"detail"`
	Code   string `json:"code,omitempty"`
}

// WriteAPIError writes a JSON error response with a stable envelope.
func WriteAPIError(w http.ResponseWriter, err APIError) {
	status := err.Status
	if status < 400 || status > 599 {
		status = http.StatusInternalServerError
	}
	detail := strings.TrimSpace(err.Detail)
	if detail == "" {
		detail = http.StatusText(status)
	}
	code := strings.TrimSpace(err.Code)
	if code == "" {
		code = defaultErrorCode(status)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Sentry-Error", detail)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(APIErrorBody{
		Detail: detail,
		Code:   code,
	}); err != nil {
		log.Error().Err(err).Msg("httputil: failed to encode API error response")
		http.Error(w, detail, http.StatusInternalServerError)
	}
}

func defaultErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusConflict:
		return "conflict"
	case http.StatusTooManyRequests:
		return "rate_limit"
	case http.StatusInternalServerError:
		return "internal"
	default:
		return ""
	}
}
