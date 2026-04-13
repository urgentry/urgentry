package web

import (
	"net"
	"net/http"
	"strings"
	"time"

	"urgentry/internal/auth"
)

type loginData struct {
	Title string
	Next  string
	Error string
}

func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	if h.authz == nil {
		http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
		return
	}
	if _, err := h.authz.AuthenticateSessionRequest(r); err == nil {
		http.Redirect(w, r, safeNextPath(r.URL.Query().Get("next")), http.StatusSeeOther)
		return
	}
	h.renderLogin(w, loginData{
		Title: "Sign In",
		Next:  safeNextPath(r.URL.Query().Get("next")),
	})
}

func (h *Handler) loginAction(w http.ResponseWriter, r *http.Request) {
	if h.authz == nil {
		http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderLogin(w, loginData{Title: "Sign In", Error: "Invalid form submission.", Next: "/"})
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	next := safeNextPath(r.FormValue("next"))

	rawToken, principal, err := h.authz.Login(r.Context(), email, password, r.UserAgent(), requestIP(r))
	if err != nil {
		h.renderLogin(w, loginData{
			Title: "Sign In",
			Next:  next,
			Error: "Invalid email or password.",
		})
		return
	}

	setAuthCookies(w, r, h.authz, rawToken, principal.CSRFToken)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if h.authz == nil {
		http.Redirect(w, r, "/login/", http.StatusSeeOther)
		return
	}
	if !h.authz.ValidateCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	_ = h.authz.RevokeSession(r.Context(), auth.PrincipalFromContext(r.Context()))
	clearAuthCookies(w, r, h.authz)
	http.Redirect(w, r, "/login/", http.StatusSeeOther)
}

func (h *Handler) renderLogin(w http.ResponseWriter, data loginData) {
	if err := h.login.ExecuteTemplate(w, "login.html", data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

func setAuthCookies(w http.ResponseWriter, r *http.Request, authz *auth.Authorizer, sessionToken, csrfToken string) {
	secure := requestIsSecure(r)
	http.SetCookie(w, &http.Cookie{
		Name:     authz.SessionCookieName(),
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		MaxAge:   int((30 * 24 * time.Hour).Seconds()),
	})
	http.SetCookie(w, &http.Cookie{
		Name:     authz.CSRFCookieName(),
		Value:    csrfToken,
		Path:     "/",
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		MaxAge:   int((30 * 24 * time.Hour).Seconds()),
	})
}

func clearAuthCookies(w http.ResponseWriter, r *http.Request, authz *auth.Authorizer) {
	secure := requestIsSecure(r)
	for _, name := range []string{authz.SessionCookieName(), authz.CSRFCookieName()} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: name == authz.SessionCookieName(),
			SameSite: http.SameSiteLaxMode,
			Secure:   secure,
			MaxAge:   -1,
		})
	}
}

func safeNextPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return "/"
	}
	return value
}

func requestIsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func requestIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}
