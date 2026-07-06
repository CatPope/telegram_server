package adminui

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"time"

	"github.com/CatPope/telegram_server/internal/adminui/templates"
	"github.com/CatPope/telegram_server/internal/api/middleware"
)

// pageData is the template data shared by every page. Fields not
// applicable to a given page (e.g. Health on the login page) are left
// zero-valued.
type pageData struct {
	Title         string
	Active        string
	Authenticated bool
	CSRFToken     string
	Error         string
	Success       string
	Health        string
	ServerURL     string

	// Apps/Users pages (Phase A2).
	Apps                  []App
	App                   *App
	AppID                 string // the {id} path param, set even when App (DB row) is unavailable
	DBUnavailable         bool
	GrantableCapabilities []string
	TelegramID            string
	UnsubAppID            string

	// Keys pages (Phase A3). PlaintextKey is rendered exactly once on
	// key_issued.html and exists nowhere else.
	Keys         []KeyRow
	PlaintextKey string

	// Audit page (Phase A4).
	AuditFilters AuditFilters
	AuditStages  []string
	AuditRows    []AuditDisplayRow
}

const healthCheckTimeout = 5 * time.Second

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	tmpl, err := templates.ParsePage("login.html")
	if err != nil {
		http.Error(w, `{"error":"internal_error"}`, http.StatusInternalServerError)
		return
	}
	token, err := s.sessions.IssueCSRFCookie(w)
	if err != nil {
		http.Error(w, `{"error":"internal_error"}`, http.StatusInternalServerError)
		return
	}
	data := pageData{Title: "Login", CSRFToken: token}
	if r.URL.Query().Get("error") != "" {
		data.Error = "Invalid password"
	}
	_ = tmpl.ExecuteTemplate(w, "base", data)
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.limiter.Allow(ip) {
		http.Error(w, `{"error":"rate_limited"}`, http.StatusTooManyRequests)
		return
	}
	if !s.backoff.Allow() {
		http.Error(w, `{"error":"rate_limited"}`, http.StatusTooManyRequests)
		return
	}

	password := r.FormValue("password")
	if subtle.ConstantTimeCompare([]byte(password), []byte(s.cfg.Password)) != 1 {
		s.backoff.RecordFailure()
		middleware.Log("info", "adminui_login_failed", map[string]any{
			"trace_id": middleware.TraceID(r.Context()),
			"ip":       ip,
		})
		http.Redirect(w, r, "/login?error=1", http.StatusSeeOther)
		return
	}
	s.backoff.RecordSuccess()

	if _, err := s.sessions.Issue(w); err != nil {
		http.Error(w, `{"error":"internal_error"}`, http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.sessions.Revoke(SessionNonce(r.Context()))
	s.sessions.Clear(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	tmpl, err := templates.ParsePage("dashboard.html")
	if err != nil {
		http.Error(w, `{"error":"internal_error"}`, http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), healthCheckTimeout)
	defer cancel()
	status := "UNREACHABLE"
	if healthy, err := s.client.Health(ctx); err == nil && healthy {
		status = "OK"
	}

	nonce := SessionNonce(r.Context())
	data := pageData{
		Title:         "Dashboard",
		Active:        "dashboard",
		Authenticated: true,
		CSRFToken:     s.sessions.CSRFToken(nonce),
		Health:        status,
		ServerURL:     s.cfg.TelegramServerURL,
	}
	_ = tmpl.ExecuteTemplate(w, "base", data)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
