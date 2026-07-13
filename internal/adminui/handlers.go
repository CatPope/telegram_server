package adminui

import (
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/CatPope/telegram_server/internal/adminui/templates"
	"github.com/CatPope/telegram_server/internal/api/middleware"
)

// pageData is the template data shared by every page. Fields not
// applicable to a given page (e.g. Stats on the login page) are left
// zero-valued.
type pageData struct {
	Title         string
	Subtitle      string
	Active        string
	Authenticated bool
	CSRFToken     string
	Error         string
	Success       string
	ServerURL     string

	// SessionRemaining is the static "세션 만료까지 ..." hint in the sidebar
	// footer — rendered once per page load (the CSP allows no JS ticking).
	SessionRemaining string

	// Apps/Users pages (Phase A2).
	Apps                  []App
	App                   *App
	AppID                 string // the {id} path param, set even when App (DB row) is unavailable
	DBUnavailable         bool
	GrantableCapabilities []string
	TelegramID            string
	UnsubAppID            string

	// Dashboard (UXUI redesign → 운영 재구성). Dash carries the 24h flow
	// sections; the health strip fields survive even with a nil store.
	// ChartRanges/ChartTitle drive the requests chart's 일/주/월/연 toggle.
	Stats        *DashboardStats
	LineChart    *LineChart
	ChartErr     bool
	ChartRanges  []ChartRangeLink
	ChartTitle   string
	Dash         *DashboardView
	HealthOK     bool
	HealthDB     string
	AdminUptime  string
	StatusFilter string

	// Keys pages (Phase A3 / UXUI redesign). PlaintextKey is rendered
	// exactly once on key_issued.html and exists nowhere else.
	Keys         []KeyRow
	KeyGroups    []KeyGroup
	KeysView     KeysView
	PlaintextKey string
	IssuedPrefix string
	IssuedLabel  string

	// Users page (UXUI redesign).
	UsersView UsersView

	// Delivery status page.
	Delivery *DeliveryView

	// Test-send console. Never carries the pasted API key.
	TestSend *TestSendView

	// Audit page (Phase A4). AuditVerify is set only by POST /audit/verify.
	// AuditAppOptions empty → the template falls back to a free-text app_id
	// input (nil store or failed lookup).
	// AuditNextURL/AuditFirstURL are the keyset-pagination links ("" hides
	// the control): next cursors on the last displayed row's id.
	AuditFilters    AuditFilters
	AuditStages     []string
	AuditLimits     []string
	AuditAppOptions []string
	AuditRows       []AuditDisplayRow
	AuditNextURL    string
	AuditFirstURL   string
	AuditVerify     *AuditVerifyView
}

// KeyGroup is the group-by-app view of the global keys table.
type KeyGroup struct {
	AppID string
	Keys  []KeyRow
}

// KeysView carries the /keys page's filter state so the toolbar can echo
// it back and build links that preserve it.
type KeysView struct {
	Group   string // "key" | "app"
	Status  string // "active" | "revoked" | "all"
	App     string // "" = all apps
	ShowNew bool
	// Form* echo the operator's issue-panel input back after a failed
	// POST /keys so the panel reopens with what they typed.
	FormAppID  string
	FormPrefix string
	FormLabel  string
}

// UsersView carries the users page state: filters plus the row expanded
// for inline editing (?selected=).
type UsersView struct {
	Grade    string
	App      string
	Query    string
	Users    []UserDisplay
	Count    int
	Selected *UserDisplay
}

// UserDisplay is a UserRow annotated with display strings the template
// would otherwise have to compute.
type UserDisplay struct {
	TelegramID    int64
	Username      string
	Grade         string
	GradeLabel    string
	GradeBadge    string
	Subscriptions []string
}

// gradeLabels maps DB grade values to the UI's Korean labels. The schema
// allows exactly these three grades (users.grade CHECK constraint).
var gradeLabels = map[string]string{
	"user":      "일반 사용자",
	"developer": "개발자",
	"admin":     "관리자",
}

// gradeBadges maps grades to badge color classes.
var gradeBadges = map[string]string{
	"user":      "badge-blue",
	"developer": "badge-teal",
	"admin":     "badge-purple",
}

func gradeLabel(g string) string {
	if l, ok := gradeLabels[g]; ok {
		return l
	}
	return g
}

func gradeBadge(g string) string {
	if b, ok := gradeBadges[g]; ok {
		return b
	}
	return "badge-gray"
}

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
	data := pageData{Title: "운영자 로그인", CSRFToken: token}
	if r.URL.Query().Get("error") != "" {
		data.Error = "비밀번호가 올바르지 않습니다"
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

// render executes a page template, logging (rather than panicking) on
// failure — matches the existing pattern of ignoring the ExecuteTemplate
// error after headers may already be partially written.
func (s *Server) render(w http.ResponseWriter, page string, data pageData) {
	tmpl, err := templates.ParsePage(page)
	if err != nil {
		http.Error(w, `{"error":"internal_error"}`, http.StatusInternalServerError)
		return
	}
	_ = tmpl.ExecuteTemplate(w, "base", data)
}

// basePageData builds the pageData common to every authenticated page.
func (s *Server) basePageData(r *http.Request, title, active string) pageData {
	return pageData{
		Title:            title,
		Active:           active,
		Authenticated:    true,
		CSRFToken:        s.sessions.CSRFToken(SessionNonce(r.Context())),
		SessionRemaining: sessionRemaining(SessionExpiry(r.Context())),
	}
}

// sessionRemaining formats the time left on the session cookie for the
// sidebar footer ("11h 59m" / "29m 41s").
func sessionRemaining(expiry time.Time) string {
	if expiry.IsZero() {
		return ""
	}
	d := time.Until(expiry)
	if d <= 0 {
		return "0s"
	}
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm %ds", m, sec)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
