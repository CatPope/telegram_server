package adminui

import (
	"net/http"
	"regexp"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// appIDPattern mirrors handlers.appIDRegex (internal/api/handlers/admin_apps.go)
// so a malformed app_id is rejected before it's ever baked into a form action.
var appIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,63}$`)

// handleUsersPage renders the users management page. There is no "list
// users" or "get user" API, so this is a two-step, action-only page: the
// operator enters a telegram_id (GET ?telegram_id=... look-up), which
// reveals the grade/subscribe forms wired to POST /users/{id}/... for
// that id. It never displays current grade or subscriptions — only the
// result of the last action.
//
// Unsubscribe needs both telegram_id and app_id in the path
// (POST /users/{id}/subscriptions/{app}/delete), so its form action can't
// be filled in by the browser from a single text field without JS — and
// the CSP here has no script-src at all. Instead the operator supplies
// both ids via a second GET look-up (?telegram_id=&app_id=), and this
// handler renders the delete form with that concrete, server-built action.
func (s *Server) handleUsersPage(w http.ResponseWriter, r *http.Request) {
	data := s.basePageData(r, "Users", "users")
	if raw := r.URL.Query().Get("telegram_id"); raw != "" {
		if _, err := strconv.ParseInt(raw, 10, 64); err != nil {
			data.Error = "텔레그램 ID 형식이 올바르지 않습니다"
		} else {
			data.TelegramID = raw
		}
	}
	if appID := r.URL.Query().Get("app_id"); appID != "" && data.TelegramID != "" {
		if !appIDPattern.MatchString(appID) {
			data.Error = "앱 ID 형식이 올바르지 않습니다"
		} else {
			data.UnsubAppID = appID
		}
	}
	s.render(w, "users.html", data)
}

func (s *Server) handleUserGrade(w http.ResponseWriter, r *http.Request) {
	telegramIDStr := chi.URLParam(r, "id")
	telegramID, ok := parseTelegramIDParam(w, r, "id")
	if !ok {
		return
	}
	grade := r.FormValue("grade")

	data := s.basePageData(r, "Users", "users")
	data.TelegramID = telegramIDStr
	if err := s.client.PatchUserGrade(r.Context(), telegramID, grade); err != nil {
		data.Error = friendlyAPIError(err)
	} else {
		data.Success = "등급이 변경되었습니다"
	}
	s.render(w, "users.html", data)
}

func (s *Server) handleUserSubscribe(w http.ResponseWriter, r *http.Request) {
	telegramIDStr := chi.URLParam(r, "id")
	telegramID, ok := parseTelegramIDParam(w, r, "id")
	if !ok {
		return
	}
	appID := r.FormValue("app_id")

	data := s.basePageData(r, "Users", "users")
	data.TelegramID = telegramIDStr
	if err := s.client.Subscribe(r.Context(), telegramID, appID); err != nil {
		data.Error = friendlyAPIError(err)
	} else {
		data.Success = "구독이 추가되었습니다"
	}
	s.render(w, "users.html", data)
}

func (s *Server) handleUserUnsubscribe(w http.ResponseWriter, r *http.Request) {
	telegramIDStr := chi.URLParam(r, "id")
	telegramID, ok := parseTelegramIDParam(w, r, "id")
	if !ok {
		return
	}
	appID := chi.URLParam(r, "app")

	data := s.basePageData(r, "Users", "users")
	data.TelegramID = telegramIDStr
	if err := s.client.Unsubscribe(r.Context(), telegramID, appID); err != nil {
		data.Error = friendlyAPIError(err)
	} else {
		data.Success = "구독이 해제되었습니다"
	}
	s.render(w, "users.html", data)
}

// parseTelegramIDParam parses the {key} path segment as a telegram_id,
// writing a 400 and returning ok=false on a malformed value.
func parseTelegramIDParam(w http.ResponseWriter, r *http.Request, key string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, key), 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid_telegram_id"}`, http.StatusBadRequest)
		return 0, false
	}
	return id, true
}
