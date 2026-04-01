package httputil

import (
	"encoding/json"
	"net/http"

	"github.com/rs/zerolog/log"
)

// WriteJSON writes a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Error().Err(err).Msg("httputil: failed to encode JSON response")
		http.Error(w, "internal server error: failed to encode response", http.StatusInternalServerError)
	}
}

// WriteError writes a JSON error response with X-Sentry-Error header.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteAPIError(w, APIError{Status: status, Detail: msg})
}
