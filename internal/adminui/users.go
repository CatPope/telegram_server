package adminui

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/CatPope/telegram_server/internal/api/middleware"
)

// appIDPattern mirrors handlers.appIDRegex (internal/api/handlers/admin_apps.go)
// so a malformed app_id is rejected before it's ever baked into a form action.
var appIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,63}$`)

// handleUsersPage renders the users management page (UXUI slides 11-12):
// the full user list (read straight from the DB — there is no "list
// users" API) with grade/subscription/id filters, and an inline edit
// panel for the ?selected= row. Under the no-JS CSP the server renders
// every concrete form action — selecting a row is a plain GET.
//
// Without a DB (store == nil) the list can't render; the page degrades to
// the pre-redesign manual flow: the operator enters a telegram_id
// (?telegram_id=), which reveals action forms for that id.
func (s *Server) handleUsersPage(w http.ResponseWriter, r *http.Request) {
	data := s.basePageData(r, "사용자", "users")
	data.Subtitle = "권한 · 구독"
	q := r.URL.Query()

	switch q.Get("saved") {
	case "grade":
		data.Success = "등급이 변경되었습니다"
	case "sub":
		data.Success = "구독이 추가되었습니다"
	case "unsub":
		data.Success = "구독이 해제되었습니다"
	}

	if s.store == nil {
		data.DBUnavailable = true
		s.legacyUsersLookup(&data, q)
		s.render(w, "users.html", data)
		return
	}

	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		middleware.Log("error", "adminui_list_users_failed", map[string]any{
			"trace_id": middleware.TraceID(r.Context()),
			"error":    err.Error(),
		})
		data.Error = "사용자 목록을 불러오지 못했습니다"
		s.render(w, "users.html", data)
		return
	}

	view := UsersView{
		Grade: q.Get("grade"),
		App:   q.Get("app"),
		Query: strings.TrimSpace(q.Get("q")),
	}
	if _, ok := gradeLabels[view.Grade]; !ok {
		view.Grade = ""
	}
	if view.App != "" && !appIDPattern.MatchString(view.App) {
		view.App = ""
	}

	selected := q.Get("selected")
	for _, u := range users {
		if view.Grade != "" && u.Grade != view.Grade {
			continue
		}
		if view.App != "" && !slices.Contains(u.Subscriptions, view.App) {
			continue
		}
		idStr := strconv.FormatInt(u.TelegramID, 10)
		if view.Query != "" && !strings.Contains(idStr, view.Query) && !strings.Contains(u.Username, view.Query) {
			continue
		}
		d := UserDisplay{
			TelegramID:    u.TelegramID,
			Username:      u.Username,
			Grade:         u.Grade,
			GradeLabel:    gradeLabel(u.Grade),
			GradeBadge:    gradeBadge(u.Grade),
			Subscriptions: u.Subscriptions,
		}
		view.Users = append(view.Users, d)
		if selected == idStr {
			sel := d
			view.Selected = &sel
		}
	}
	view.Count = len(view.Users)
	data.UsersView = view

	// The inline panel's "구독 추가" select needs the app list; degrade to
	// nothing if it can't load.
	if apps, err := s.store.ListApps(r.Context()); err == nil {
		data.Apps = apps
	}

	s.render(w, "users.html", data)
}

// legacyUsersLookup validates the manual-flow query params (pre-redesign
// UX kept as the DB-less fallback).
func (s *Server) legacyUsersLookup(data *pageData, q url.Values) {
	if raw := q.Get("telegram_id"); raw != "" {
		if _, err := strconv.ParseInt(raw, 10, 64); err != nil {
			data.Error = "텔레그램 ID 형식이 올바르지 않습니다"
		} else {
			data.TelegramID = raw
		}
	}
	if appID := q.Get("app_id"); appID != "" && data.TelegramID != "" {
		if !appIDPattern.MatchString(appID) {
			data.Error = "앱 ID 형식이 올바르지 않습니다"
		} else {
			data.UnsubAppID = appID
		}
	}
}

func (s *Server) handleUserGrade(w http.ResponseWriter, r *http.Request) {
	telegramIDStr := chi.URLParam(r, "id")
	telegramID, ok := parseTelegramIDParam(w, r, "id")
	if !ok {
		return
	}
	grade := r.FormValue("grade")

	if err := s.client.PatchUserGrade(r.Context(), telegramID, grade); err != nil {
		s.renderUsersError(w, r, telegramIDStr, friendlyAPIError(err))
		return
	}
	http.Redirect(w, r, "/users?selected="+telegramIDStr+"&saved=grade", http.StatusSeeOther)
}

func (s *Server) handleUserSubscribe(w http.ResponseWriter, r *http.Request) {
	telegramIDStr := chi.URLParam(r, "id")
	telegramID, ok := parseTelegramIDParam(w, r, "id")
	if !ok {
		return
	}
	appID := r.FormValue("app_id")

	if err := s.client.Subscribe(r.Context(), telegramID, appID); err != nil {
		s.renderUsersError(w, r, telegramIDStr, friendlyAPIError(err))
		return
	}
	http.Redirect(w, r, "/users?selected="+telegramIDStr+"&saved=sub", http.StatusSeeOther)
}

func (s *Server) handleUserUnsubscribe(w http.ResponseWriter, r *http.Request) {
	telegramIDStr := chi.URLParam(r, "id")
	telegramID, ok := parseTelegramIDParam(w, r, "id")
	if !ok {
		return
	}
	appID := chi.URLParam(r, "app")

	if err := s.client.Unsubscribe(r.Context(), telegramID, appID); err != nil {
		s.renderUsersError(w, r, telegramIDStr, friendlyAPIError(err))
		return
	}
	http.Redirect(w, r, "/users?selected="+telegramIDStr+"&saved=unsub", http.StatusSeeOther)
}

// renderUsersError re-renders the users page (list + selected panel when
// possible) with an error banner, keeping the operator's place after a
// failed action.
func (s *Server) renderUsersError(w http.ResponseWriter, r *http.Request, telegramID, message string) {
	data := s.basePageData(r, "사용자", "users")
	data.Subtitle = "권한 · 구독"
	data.Error = message
	if s.store == nil {
		data.DBUnavailable = true
		data.TelegramID = telegramID
		s.render(w, "users.html", data)
		return
	}
	users, err := s.store.ListUsers(r.Context())
	if err == nil {
		view := UsersView{}
		for _, u := range users {
			d := UserDisplay{
				TelegramID:    u.TelegramID,
				Username:      u.Username,
				Grade:         u.Grade,
				GradeLabel:    gradeLabel(u.Grade),
				GradeBadge:    gradeBadge(u.Grade),
				Subscriptions: u.Subscriptions,
			}
			view.Users = append(view.Users, d)
			if strconv.FormatInt(u.TelegramID, 10) == telegramID {
				sel := d
				view.Selected = &sel
			}
		}
		view.Count = len(view.Users)
		data.UsersView = view
		if apps, appsErr := s.store.ListApps(r.Context()); appsErr == nil {
			data.Apps = apps
		}
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
