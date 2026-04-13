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
	// Use the forest & gold dark theme to match the rest of the app.
	return "<!DOCTYPE html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>" + statusText + " - Urgentry</title><style>" +
		":root{--bg:#1a1208;--surface:#231a0d;--surface2:#2c2010;--border:#3d2e18;--text:#f0e6d0;--text-muted:#9a8060;--accent:#c8922a;--accent-hover:#e0a840}" +
		"*{box-sizing:border-box}" +
		"body{margin:0;background:var(--bg);color:var(--text);font:15px/1.6 'Space Grotesk',system-ui,sans-serif;min-height:100vh;display:flex;align-items:center;justify-content:center}" +
		"main{width:100%;max-width:480px;padding:2rem}" +
		"section{background:var(--surface);border:1px solid var(--border);border-radius:16px;padding:2.5rem;box-shadow:0 24px 64px rgba(0,0,0,.5)}" +
		"small{display:inline-block;margin-bottom:.75rem;color:var(--accent);text-transform:uppercase;letter-spacing:.1em;font-size:.7rem;font-weight:700}" +
		"h1{margin:.25rem 0 1rem;font-size:1.75rem;line-height:1.15;color:var(--text)}" +
		"p{margin:0;color:var(--text-muted);font-size:.925rem}" +
		"a{display:inline-block;margin-top:1.5rem;color:var(--accent);text-decoration:none;font-weight:600;font-size:.9rem}" +
		"a:hover{color:var(--accent-hover);text-decoration:underline}" +
		"</style></head><body><main><section>" +
		"<small>Urgentry</small>" +
		"<h1>" + statusText + "</h1>" +
		"<p>" + body + "</p>" +
		"<a href=\"javascript:history.back()\">&#8592; Go back</a>" +
		"</section></main></body></html>"
}
