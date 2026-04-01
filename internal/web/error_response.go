package web

import (
	"html"
	"net/http"
	"strings"
)

func writeWebError(w http.ResponseWriter, r *http.Request, status int, message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		message = http.StatusText(status)
	}
	if r != nil && r.Header.Get("HX-Request") == "true" {
		http.Error(w, message, status)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(webErrorHTML(status, message)))
}

func writeWebUnavailable(w http.ResponseWriter, r *http.Request, message string) {
	writeWebError(w, r, http.StatusServiceUnavailable, message)
}

func writeWebForbidden(w http.ResponseWriter, r *http.Request) {
	writeWebError(w, r, http.StatusForbidden, "Forbidden")
}

func writeWebBadRequest(w http.ResponseWriter, r *http.Request, message string) {
	writeWebError(w, r, http.StatusBadRequest, message)
}

func writeWebUnauthorized(w http.ResponseWriter, r *http.Request) {
	writeWebError(w, r, http.StatusUnauthorized, "Unauthorized")
}

func writeWebNotFound(w http.ResponseWriter, r *http.Request, message string) {
	writeWebError(w, r, http.StatusNotFound, message)
}

func writeWebInternal(w http.ResponseWriter, r *http.Request, message string) {
	writeWebError(w, r, http.StatusInternalServerError, message)
}

func webErrorHTML(status int, message string) string {
	statusText := html.EscapeString(http.StatusText(status))
	body := html.EscapeString(message)
	return "<!DOCTYPE html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>" + statusText + " - Urgentry</title><style>body{margin:0;background:#f4efe5;color:#1f1710;font:16px/1.5 Inter,system-ui,sans-serif}main{max-width:42rem;margin:10vh auto;padding:2rem}section{background:#fffaf1;border:1px solid #e8dcc7;border-radius:18px;padding:2rem;box-shadow:0 20px 60px rgba(56,38,18,.08)}small{display:inline-block;margin-bottom:.75rem;color:#8c6d47;text-transform:uppercase;letter-spacing:.08em;font-weight:700}h1{margin:.2rem 0 1rem;font-size:2rem;line-height:1.1}p{margin:0;color:#4a3728}a{display:inline-block;margin-top:1.25rem;color:#7a3d00;text-decoration:none;font-weight:600}</style></head><body><main><section><small>Urgentry</small><h1>" + statusText + "</h1><p>" + body + "</p><a href=\"javascript:history.back()\">Go back</a></section></main></body></html>"
}
