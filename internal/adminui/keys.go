package adminui

import (
	"errors"
	"net/http"
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

func (s *Server) handleKeysList(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	data := s.keysPageData(r, id)
	if r.URL.Query().Get("revoked") != "" {
		data.Success = "키가 폐기되었습니다"
	}
	s.render(w, "keys.html", data)
}

// handleKeyIssue generates a new API key. The plaintext token exists only
// inside this handler and the response page it renders — it is never
// logged, never stored, and never placed in a redirect URL (which is why
// success renders directly instead of the usual POST→redirect: the page
// itself warns the operator it cannot be shown again).
func (s *Server) handleKeyIssue(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	prefix := strings.TrimSpace(r.FormValue("prefix"))
	label := strings.TrimSpace(r.FormValue("label"))
	if !keyPrefixPattern.MatchString(prefix) {
		s.renderKeysError(w, r, id, "prefix는 영소문자/숫자 4~16자여야 합니다")
		return
	}
	if utf8.RuneCountInString(label) > keyLabelMaxLen {
		s.renderKeysError(w, r, id, "label은 100자 이하여야 합니다")
		return
	}
	if s.keys == nil {
		s.renderKeysError(w, r, id, "키 관리는 DB 연결이 필요합니다")
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

	if err := s.keys.IssueKey(r.Context(), id, prefix, hash, label); err != nil {
		s.renderKeysError(w, r, id, issueErrorMessage(err))
		return
	}

	// Both trails carry the prefix only — never the secret or hash.
	s.writeKeyAudit(r, audit.StageKeyIssued, id, prefix)
	middleware.Log("info", "adminui_key_issued", map[string]any{
		"trace_id":   middleware.TraceID(r.Context()),
		"app_id":     id,
		"key_prefix": prefix,
	})

	// The key row already exists at this point, so a template failure must
	// not hide behind a generic 500 — tell the operator what to check.
	tmpl, err := templates.ParsePage("key_issued.html")
	if err != nil {
		http.Error(w, "키가 생성되었을 수 있습니다 — 키 목록을 확인하고 불필요하면 폐기하세요", http.StatusInternalServerError)
		return
	}
	data := s.basePageData(r, "Key Issued", "apps")
	data.AppID = id
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
	id := chi.URLParam(r, "id")
	prefix := chi.URLParam(r, "prefix")
	if !keyPrefixPattern.MatchString(prefix) {
		s.renderKeysError(w, r, id, "prefix 형식이 올바르지 않습니다")
		return
	}
	// CSP blocks all JS, so there is no confirm() dialog — the revoke form
	// instead requires an explicit confirmation checkbox.
	if r.FormValue("confirm") != "1" {
		s.renderKeysError(w, r, id, "폐기하려면 확인란을 체크하세요")
		return
	}
	if s.keys == nil {
		s.renderKeysError(w, r, id, "키 관리는 DB 연결이 필요합니다")
		return
	}

	if err := s.keys.RevokeKey(r.Context(), id, prefix); err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			s.renderKeysError(w, r, id, "이미 폐기되었거나 존재하지 않는 키입니다")
			return
		}
		s.renderKeysError(w, r, id, "키 폐기에 실패했습니다")
		return
	}

	s.writeKeyAudit(r, audit.StageKeyRevoked, id, prefix)
	middleware.Log("info", "adminui_key_revoked", map[string]any{
		"trace_id":   middleware.TraceID(r.Context()),
		"app_id":     id,
		"key_prefix": prefix,
	})
	http.Redirect(w, r, "/apps/"+id+"/keys?revoked=1", http.StatusSeeOther)
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

// keysPageData assembles the keys page (list + issue form), degrading to
// a DB-unavailable notice when no keystore is configured.
func (s *Server) keysPageData(r *http.Request, appID string) pageData {
	data := s.basePageData(r, "Keys: "+appID, "apps")
	data.AppID = appID
	if s.keys == nil {
		data.DBUnavailable = true
		return data
	}
	keys, err := s.keys.ListKeys(r.Context(), appID)
	if err != nil {
		middleware.Log("error", "adminui_list_keys_failed", map[string]any{
			"trace_id": middleware.TraceID(r.Context()),
			"app_id":   appID,
			"error":    err.Error(),
		})
		data.Error = "키 목록을 불러오지 못했습니다"
		return data
	}
	data.Keys = keys
	return data
}

// renderKeysError re-renders the keys page with an error banner, keeping
// the operator's place (list + form stay visible).
func (s *Server) renderKeysError(w http.ResponseWriter, r *http.Request, appID, message string) {
	data := s.keysPageData(r, appID)
	data.Error = message
	s.render(w, "keys.html", data)
}
