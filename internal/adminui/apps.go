package adminui

import (
	"errors"
	"net/http"
	"net/url"
	"slices"

	"github.com/go-chi/chi/v5"

	"github.com/CatPope/telegram_server/internal/adminui/apiclient"
	"github.com/CatPope/telegram_server/internal/adminui/templates"
)

// grantableCapabilities are the capabilities an operator may grant via
// POST /admin/apps or add/remove via PATCH /admin/apps/{id} — the server
// rejects management capabilities (apps.register etc.) on both routes
// with forbidden_capability/unknown_capability, and filterGrantable below
// keeps the UI from even submitting them, so the two layers agree.
var grantableCapabilities = []string{
	"messages.direct.send",
	"messages.direct.dm",
	"messages.topic.send",
	"messages.broadcast.send",
	"noop.invoke",
}

// filterGrantable drops any submitted capability outside the grantable
// set — the server rejects them anyway (forbidden_capability), this is a
// second layer so a UI form can never even attempt a management grant.
func filterGrantable(vals []string) []string {
	var out []string
	for _, v := range vals {
		if slices.Contains(grantableCapabilities, v) {
			out = append(out, v)
		}
	}
	return out
}

// apiErrorMessages maps /admin error codes to operator-friendly Korean
// messages. Unmapped codes fall back to the raw code.
var apiErrorMessages = map[string]string{
	"malformed_json":          "요청 형식이 올바르지 않습니다",
	"missing_required_fields": "필수 항목이 누락되었습니다",
	"invalid_app_id":          "앱 ID 형식이 올바르지 않습니다 (영소문자/숫자/-/_, 3~64자)",
	"invalid_min_grade":       "등급 값이 올바르지 않습니다",
	"invalid_grade":           "등급 값이 올바르지 않습니다",
	"unknown_capability":      "알 수 없는 capability입니다",
	"forbidden_capability":    "부여할 수 없는 capability입니다",
	"app_already_exists":      "이미 존재하는 앱 ID입니다",
	"app_not_found":           "앱을 찾을 수 없습니다",
	"app_inactive":            "비활성화된 앱입니다",
	"user_not_found":          "사용자를 찾을 수 없습니다",
	"invalid_telegram_id":     "텔레그램 ID 형식이 올바르지 않습니다",
	"subscription_not_found":  "구독 정보를 찾을 수 없습니다",
	"forbidden":               "이 키에 권한이 없습니다",
	"unauthenticated":         "인증되지 않았습니다",
	"rate_limited":            "요청이 너무 많습니다. 잠시 후 다시 시도하세요",
	"db_error":                "서버 내부 오류가 발생했습니다",
	"invalid_limit":           "limit은 1~500 사이 숫자여야 합니다",
	"invalid_since":           "since는 RFC3339 형식이어야 합니다 (예: 2026-07-06T00:00:00Z)",
	"invalid_until":           "until은 RFC3339 형식이어야 합니다 (예: 2026-07-06T00:00:00Z)",
	"invalid_stage":           "stage 값이 올바르지 않습니다",
}

// friendlyAPIError renders an apiclient error for display on a page. It
// recognizes *apiclient.APIError (server-reported {"error":code}) and maps
// known codes to Korean text; anything else (network failure, timeout)
// gets a generic message so the raw error text — which may embed the
// target URL — never reaches the browser.
func friendlyAPIError(err error) string {
	var apiErr *apiclient.APIError
	if errors.As(err, &apiErr) {
		if msg, ok := apiErrorMessages[apiErr.Code]; ok {
			return msg
		}
		return apiErr.Code
	}
	return "대상 서버에 연결할 수 없습니다"
}

func (s *Server) handleAppsList(w http.ResponseWriter, r *http.Request) {
	data := s.basePageData(r, "Apps", "apps")

	if s.store == nil {
		data.DBUnavailable = true
		s.render(w, "apps_list.html", data)
		return
	}

	apps, err := s.store.ListApps(r.Context())
	if err != nil {
		data.Error = "앱 목록을 불러오지 못했습니다"
		s.render(w, "apps_list.html", data)
		return
	}
	data.Apps = apps
	s.render(w, "apps_list.html", data)
}

func (s *Server) handleAppNewForm(w http.ResponseWriter, r *http.Request) {
	data := s.basePageData(r, "New App", "apps")
	data.GrantableCapabilities = grantableCapabilities
	s.render(w, "app_new.html", data)
}

func (s *Server) handleAppCreate(w http.ResponseWriter, r *http.Request) {
	req := apiclient.CreateAppRequest{
		ID:           r.FormValue("id"),
		Name:         r.FormValue("name"),
		Description:  r.FormValue("description"),
		MinGrade:     r.FormValue("min_grade"),
		Capabilities: filterGrantable(r.Form["capabilities"]),
	}

	if err := s.client.CreateApp(r.Context(), req); err != nil {
		data := s.basePageData(r, "New App", "apps")
		data.GrantableCapabilities = grantableCapabilities
		data.Error = friendlyAPIError(err)
		s.render(w, "app_new.html", data)
		return
	}
	http.Redirect(w, r, "/apps/"+url.PathEscape(req.ID), http.StatusSeeOther)
}

func (s *Server) handleAppDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	data := s.basePageData(r, "App: "+id, "apps")
	data.AppID = id
	data.GrantableCapabilities = grantableCapabilities

	if s.store == nil {
		data.DBUnavailable = true
		s.render(w, "app_detail.html", data)
		return
	}

	app, err := s.store.GetApp(r.Context(), id)
	if errors.Is(err, ErrAppNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		data.Error = "앱 정보를 불러오지 못했습니다"
		s.render(w, "app_detail.html", data)
		return
	}
	data.App = &app
	switch {
	case r.URL.Query().Get("saved") != "":
		data.Success = "저장되었습니다"
	case r.URL.Query().Get("deactivated") != "":
		data.Success = "비활성화되었습니다"
	}
	s.render(w, "app_detail.html", data)
}

func (s *Server) handleAppPatch(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	description := r.FormValue("description")
	minGrade := r.FormValue("min_grade")
	active := r.FormValue("active") == "on"

	req := apiclient.PatchAppRequest{
		Description:        &description,
		MinGrade:           &minGrade,
		Active:             &active,
		AddCapabilities:    filterGrantable(r.Form["add_capabilities"]),
		RemoveCapabilities: filterGrantable(r.Form["remove_capabilities"]),
	}

	if err := s.client.PatchApp(r.Context(), id, req); err != nil {
		s.renderAppDetailError(w, r, id, friendlyAPIError(err))
		return
	}
	http.Redirect(w, r, "/apps/"+id+"?saved=1", http.StatusSeeOther)
}

func (s *Server) handleAppDeactivate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.client.DeleteApp(r.Context(), id); err != nil {
		s.renderAppDetailError(w, r, id, friendlyAPIError(err))
		return
	}
	http.Redirect(w, r, "/apps/"+id+"?deactivated=1", http.StatusSeeOther)
}

// renderAppDetailError re-fetches the app (if a store is configured) and
// re-renders app_detail.html with an error banner, so a failed patch/
// deactivate doesn't lose the operator's place.
func (s *Server) renderAppDetailError(w http.ResponseWriter, r *http.Request, id, message string) {
	data := s.basePageData(r, "App: "+id, "apps")
	data.AppID = id
	data.GrantableCapabilities = grantableCapabilities
	data.Error = message
	if s.store != nil {
		if app, err := s.store.GetApp(r.Context(), id); err == nil {
			data.App = &app
		}
	} else {
		data.DBUnavailable = true
	}
	s.render(w, "app_detail.html", data)
}

// render executes a page template, logging (rather than panicking) on
// failure — matches the existing handlers.go pattern of ignoring the
// ExecuteTemplate error after headers may already be partially written.
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
		Title:         title,
		Active:        active,
		Authenticated: true,
		CSRFToken:     s.sessions.CSRFToken(SessionNonce(r.Context())),
	}
}
