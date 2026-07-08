package adminui

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/CatPope/telegram_server/internal/adminui/templates"
	"github.com/CatPope/telegram_server/internal/api/middleware"
	"github.com/CatPope/telegram_server/internal/audit"
	"github.com/CatPope/telegram_server/internal/auth"
)

// keyPrefixPattern is the operator-chosen part of a token
// (tg_<prefix>_<secret>). Lowercase alphanumeric only — no underscore,
// since auth.ParseBearer splits the prefix at the first '_'.
var keyPrefixPattern = regexp.MustCompile(`^[a-z0-9]{4,16}$`)

const (
	// keySecretBytes of crypto/rand entropy per key → 48 hex chars, same
	// order of magnitude as the dev seed keys.
	keySecretBytes = 24
	// keyLabelMaxLen bounds the free-text label form field.
	keyLabelMaxLen = 100
)

// handleKeysPage renders the global API key console (UXUI slides 3-6):
// every app's keys in one table with group/status/app filters, an inline
// issue panel (?new=1), and per-key edit/revoke modals (CSS :target).
func (s *Server) handleKeysPage(w http.ResponseWriter, r *http.Request) {
	data := s.keysPageData(r)
	switch {
	case r.URL.Query().Get("revoked") != "":
		data.Success = "키가 폐기되었습니다"
	case r.URL.Query().Get("labeled") != "":
		data.Success = "라벨이 변경되었습니다"
	}
	s.render(w, "keys.html", data)
}

// handleKeysLegacyRedirect preserves the pre-redesign per-app URL
// (/apps/{id}/keys) by sending it to the global page filtered to that app.
func (s *Server) handleKeysLegacyRedirect(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	http.Redirect(w, r, "/keys?app="+url.QueryEscape(id), http.StatusSeeOther)
}

// handleKeyIssue generates a new API key. The plaintext token exists only
// inside this handler and the response page it renders — it is never
// logged, never stored, and never placed in a redirect URL (which is why
// success renders directly instead of the usual POST→redirect: the page
// itself warns the operator it cannot be shown again).
func (s *Server) handleKeyIssue(w http.ResponseWriter, r *http.Request) {
	appID := strings.TrimSpace(r.FormValue("app_id"))
	prefix := strings.TrimSpace(r.FormValue("prefix"))
	label := strings.TrimSpace(r.FormValue("label"))
	if !appIDPattern.MatchString(appID) {
		s.renderIssueError(w, r, "앱 ID 형식이 올바르지 않습니다", appID, prefix, label)
		return
	}
	if !keyPrefixPattern.MatchString(prefix) {
		s.renderIssueError(w, r, "prefix는 영소문자/숫자 4~16자여야 합니다", appID, prefix, label)
		return
	}
	if utf8.RuneCountInString(label) > keyLabelMaxLen {
		s.renderIssueError(w, r, "label은 100자 이하여야 합니다", appID, prefix, label)
		return
	}
	if s.keys == nil {
		s.renderIssueError(w, r, "키 관리는 DB 연결이 필요합니다", appID, prefix, label)
		return
	}

	secret, err := randomHex(keySecretBytes)
	if err != nil {
		http.Error(w, `{"error":"internal_error"}`, http.StatusInternalServerError)
		return
	}
	plaintext := "tg_" + prefix + "_" + secret
	hash, err := auth.HashAPIKey(plaintext)
	if err != nil {
		http.Error(w, `{"error":"internal_error"}`, http.StatusInternalServerError)
		return
	}

	if err := s.keys.IssueKey(r.Context(), appID, prefix, hash, label); err != nil {
		s.renderIssueError(w, r, issueErrorMessage(err), appID, prefix, label)
		return
	}

	// Both trails carry the prefix only — never the secret or hash.
	s.writeKeyAudit(r, audit.StageKeyIssued, appID, prefix)
	middleware.Log("info", "adminui_key_issued", map[string]any{
		"trace_id":   middleware.TraceID(r.Context()),
		"app_id":     appID,
		"key_prefix": prefix,
	})

	// The key row already exists at this point, so a template failure must
	// not hide behind a generic 500 — tell the operator what to check.
	tmpl, err := templates.ParsePage("key_issued.html")
	if err != nil {
		http.Error(w, "키가 생성되었을 수 있습니다 — 키 목록을 확인하고 불필요하면 폐기하세요", http.StatusInternalServerError)
		return
	}
	data := s.basePageData(r, "API 키", "keys")
	data.Subtitle = "발급 완료"
	data.AppID = appID
	data.IssuedPrefix = prefix
	data.IssuedLabel = label
	data.PlaintextKey = plaintext
	_ = tmpl.ExecuteTemplate(w, "base", data)
}

func issueErrorMessage(err error) string {
	switch {
	case errors.Is(err, ErrAppNotFound):
		return "앱을 찾을 수 없습니다"
	case errors.Is(err, ErrAppInactive):
		return "비활성화된 앱에는 키를 발급할 수 없습니다"
	case errors.Is(err, ErrPrefixTaken):
		return "이미 사용 중인 prefix입니다"
	default:
		return "키 발급에 실패했습니다"
	}
}

func (s *Server) handleKeyRevoke(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "app")
	prefix := chi.URLParam(r, "prefix")
	if !appIDPattern.MatchString(appID) {
		s.renderKeysError(w, r, "앱 ID 형식이 올바르지 않습니다")
		return
	}
	if !keyPrefixPattern.MatchString(prefix) {
		s.renderKeysError(w, r, "prefix 형식이 올바르지 않습니다")
		return
	}
	// CSP blocks all JS, so there is no confirm() dialog — the revoke modal
	// is the confirmation step, and its form carries confirm=1 explicitly.
	if r.FormValue("confirm") != "1" {
		s.renderKeysError(w, r, "폐기하려면 확인 단계를 거쳐야 합니다")
		return
	}
	if s.keys == nil {
		s.renderKeysError(w, r, "키 관리는 DB 연결이 필요합니다")
		return
	}

	if err := s.keys.RevokeKey(r.Context(), appID, prefix); err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			s.renderKeysError(w, r, "이미 폐기되었거나 존재하지 않는 키입니다")
			return
		}
		s.renderKeysError(w, r, "키 폐기에 실패했습니다")
		return
	}

	s.writeKeyAudit(r, audit.StageKeyRevoked, appID, prefix)
	middleware.Log("info", "adminui_key_revoked", map[string]any{
		"trace_id":   middleware.TraceID(r.Context()),
		"app_id":     appID,
		"key_prefix": prefix,
	})
	http.Redirect(w, r, "/keys?revoked=1", http.StatusSeeOther)
}

// handleKeyLabel renames a key's label (slide 5's edit modal). The prefix
// is immutable — it is part of the issued token — so only the label moves.
// Cosmetic change: no audit event, matching every other non-lifecycle edit.
func (s *Server) handleKeyLabel(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "app")
	prefix := chi.URLParam(r, "prefix")
	label := strings.TrimSpace(r.FormValue("label"))
	if !appIDPattern.MatchString(appID) {
		s.renderKeysError(w, r, "앱 ID 형식이 올바르지 않습니다")
		return
	}
	if !keyPrefixPattern.MatchString(prefix) {
		s.renderKeysError(w, r, "prefix 형식이 올바르지 않습니다")
		return
	}
	if utf8.RuneCountInString(label) > keyLabelMaxLen {
		s.renderKeysError(w, r, "label은 100자 이하여야 합니다")
		return
	}
	if s.keys == nil {
		s.renderKeysError(w, r, "키 관리는 DB 연결이 필요합니다")
		return
	}

	if err := s.keys.UpdateKeyLabel(r.Context(), appID, prefix, label); err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			s.renderKeysError(w, r, "이미 폐기되었거나 존재하지 않는 키입니다")
			return
		}
		s.renderKeysError(w, r, "라벨 변경에 실패했습니다")
		return
	}
	http.Redirect(w, r, "/keys?labeled=1", http.StatusSeeOther)
}

// writeKeyAudit records a key lifecycle event on the durable audit_log
// chain via the shared audit.Writer. A write failure must not fail the
// operation the operator already completed, but it must never be silent
// either (root R2) — it is logged at error level.
func (s *Server) writeKeyAudit(r *http.Request, stage audit.Stage, appID, prefix string) {
	if s.audit == nil {
		return
	}
	err := s.audit.Write(r.Context(), audit.Event{
		TraceID: middleware.TraceID(r.Context()),
		Stage:   stage,
		AppID:   appID,
		Details: map[string]any{"key_prefix": prefix},
	})
	if err != nil {
		middleware.Log("error", "adminui_audit_write_failed", map[string]any{
			"trace_id":   middleware.TraceID(r.Context()),
			"stage":      string(stage),
			"app_id":     appID,
			"key_prefix": prefix,
			"error":      err.Error(),
		})
	}
}

// keysPageData assembles the global keys page: filter state from the
// query string, the key list (grouped if requested), and the apps list
// for the filter/issue selects. Degrades to a DB-unavailable notice when
// no keystore is configured.
func (s *Server) keysPageData(r *http.Request) pageData {
	q := r.URL.Query()
	view := KeysView{
		Group:   q.Get("group"),
		Status:  q.Get("status"),
		App:     q.Get("app"),
		ShowNew: q.Get("new") != "",
	}
	if view.Group != "app" {
		view.Group = "key"
	}
	if view.Status != "revoked" && view.Status != "all" {
		view.Status = "active"
	}
	if view.App != "" && !appIDPattern.MatchString(view.App) {
		view.App = ""
	}

	data := s.basePageData(r, "API 키", "keys")
	data.Subtitle = view.App
	data.KeysView = view
	if s.keys == nil {
		data.DBUnavailable = true
		return data
	}

	keys, err := s.keys.ListKeys(r.Context(), view.App)
	if err != nil {
		middleware.Log("error", "adminui_list_keys_failed", map[string]any{
			"trace_id": middleware.TraceID(r.Context()),
			"app_id":   view.App,
			"error":    err.Error(),
		})
		data.Error = "키 목록을 불러오지 못했습니다"
		return data
	}
	keys = filterKeysByStatus(keys, view.Status)
	data.Keys = keys
	if view.Group == "app" {
		data.KeyGroups = groupKeysByApp(keys)
	}

	// Apps for the filter dropdown and the issue panel's app select. A
	// failure here only degrades the selects, not the key table.
	if s.store != nil {
		if apps, err := s.store.ListApps(r.Context()); err == nil {
			data.Apps = apps
		}
	}
	return data
}

func filterKeysByStatus(keys []KeyRow, status string) []KeyRow {
	if status == "all" {
		return keys
	}
	wantRevoked := status == "revoked"
	var out []KeyRow
	for _, k := range keys {
		if (k.RevokedAt != nil) == wantRevoked {
			out = append(out, k)
		}
	}
	return out
}

// groupKeysByApp partitions keys into per-app sections, apps in first-seen
// order (the list is already newest-first overall).
func groupKeysByApp(keys []KeyRow) []KeyGroup {
	index := map[string]int{}
	var groups []KeyGroup
	for _, k := range keys {
		i, ok := index[k.AppID]
		if !ok {
			i = len(groups)
			index[k.AppID] = i
			groups = append(groups, KeyGroup{AppID: k.AppID})
		}
		groups[i].Keys = append(groups[i].Keys, k)
	}
	return groups
}

// renderKeysError re-renders the keys page with an error banner, keeping
// the operator's place (list + filters stay visible).
func (s *Server) renderKeysError(w http.ResponseWriter, r *http.Request, message string) {
	data := s.keysPageData(r)
	data.Error = message
	s.render(w, "keys.html", data)
}

// renderIssueError is renderKeysError for a failed POST /keys: the issue
// panel reopens with the operator's input echoed so nothing is retyped.
func (s *Server) renderIssueError(w http.ResponseWriter, r *http.Request, message, appID, prefix, label string) {
	data := s.keysPageData(r)
	data.Error = message
	data.KeysView.ShowNew = true
	data.KeysView.FormAppID = appID
	data.KeysView.FormPrefix = prefix
	data.KeysView.FormLabel = label
	s.render(w, "keys.html", data)
}
