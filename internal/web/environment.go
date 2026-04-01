package web

import (
	"encoding/json"
	"net/http"
	"strings"
)

// setEnvironment handles POST /settings/environment.
// It persists the selected environment in a cookie and redirects back.
func (h *Handler) setEnvironment(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeWebBadRequest(w, r, "Invalid form")
		return
	}
	env := strings.TrimSpace(r.FormValue("environment"))

	// Persist the selection in a cookie. An empty value or "all" clears the filter.
	value := env
	if value == "" {
		value = "all"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "urgentry_environment",
		Value:    value,
		Path:     "/",
		MaxAge:   86400 * 30, // 30 days
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	// Redirect back to the referring page, or home.
	target := strings.TrimSpace(r.FormValue("return_to"))
	if target == "" {
		target = r.Referer()
	}
	if target == "" || !strings.HasPrefix(target, "/") {
		target = "/"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// listEnvironments handles GET /api/ui/environments.
// Returns a JSON object with distinct environment names and the currently
// selected environment (read from the cookie).
func (h *Handler) listEnvironments(w http.ResponseWriter, r *http.Request) {
	if h.webStore == nil {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"environments":[],"selected":""}`))
		return
	}
	envs, err := h.webStore.ListEnvironments(r.Context())
	if err != nil {
		http.Error(w, `{"error":"failed to load environments"}`, http.StatusInternalServerError)
		return
	}
	if envs == nil {
		envs = []string{}
	}
	// Read the current selection from the cookie so the JS client doesn't
	// need to access an HttpOnly cookie directly.
	selected := ""
	if c, cerr := r.Cookie("urgentry_environment"); cerr == nil && c.Value != "" && c.Value != "all" {
		selected = c.Value
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Environments []string `json:"environments"`
		Selected     string   `json:"selected"`
	}{
		Environments: envs,
		Selected:     selected,
	})
}
