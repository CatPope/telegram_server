package adminui

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/CatPope/telegram_server/internal/adminui/apiclient"
	"github.com/CatPope/telegram_server/internal/api/middleware"
)

// auditVerifyTimeout bounds one chain verification walk. Deliberately
// larger than storeQueryTimeout: the walk streams the whole table. When it
// still expires, the page reports how far the chain verified clean and the
// operator re-runs (each run restarts from the genesis row).
const auditVerifyTimeout = 30 * time.Second

// auditStages is the stage filter dropdown, matching the server's
// validAuditStages (internal/api/handlers/admin_audit.go) — anything else
// would just bounce back as invalid_stage.
var auditStages = []string{
	"received",
	"validated",
	"dispatched",
	"delivered",
	"denied",
	"retried",
	"deferred",
	"intrusion_kick",
	"intrusion_unmitigated",
	"bot_not_admin",
	"telegram_auth_failed",
	"key_issued",
	"key_revoked",
}

// auditLimitOptions is the limit dropdown (요청사항: 10/20/30/50). The
// server accepts up to 500; pages beyond the limit are reached via the
// keyset pagination below rather than a bigger page.
var auditLimitOptions = []string{"10", "20", "30", "50"}

// defaultAuditLimit is the page size when the query names none — applied
// UI-side so pagination math never depends on the server's default (50).
const defaultAuditLimit = "10"

// AuditFilters echoes the operator's GET query back into the filter form
// so a submitted search keeps its inputs visible alongside the results.
// Since/Until hold the date-picker values (2006-01-02); they are converted
// to the RFC3339 instants the /admin API expects only when querying.
// BeforeID is the pagination cursor (not a form field): only rows with a
// smaller audit_log id are returned, so "다음" walks back in insertion order.
type AuditFilters struct {
	Limit    string
	Since    string
	Until    string
	TraceID  string
	AppID    string
	Stage    string
	BeforeID string
}

// defaultAuditFilters is the bare-page state (요청사항: 기본적으로 당일
// 데이터): today's window at the smallest page size. Shared by the plain
// GET /audit landing and the verify re-render.
func defaultAuditFilters(now time.Time) AuditFilters {
	return AuditFilters{Since: now.UTC().Format("2006-01-02"), Limit: defaultAuditLimit}
}

// auditDateToRFC3339 converts a date-picker value (2006-01-02) to the
// RFC3339 boundary the /admin API expects — start of day for since, next
// midnight for until, both UTC to match the stored timestamps. The server
// compares at <= until, so next-midnight keeps every fractional-second
// event of the selected day (23:59:59.x) at the cost of also matching an
// event landing exactly on the next midnight instant — the lesser error.
// Anything that isn't a plain date (an old RFC3339 bookmark, garbage)
// passes through untouched: the server stays the single validator.
func auditDateToRFC3339(v string, endOfDay bool) string {
	t, err := time.Parse("2006-01-02", v)
	if err != nil || v == "" {
		return v
	}
	if endOfDay {
		t = t.AddDate(0, 0, 1)
	}
	return t.UTC().Format(time.RFC3339)
}

// normalizeAuditDate collapses a legacy RFC3339 filter value (old bookmark)
// to its date part so the date input can display it — otherwise the browser
// renders an empty box while the filter silently stays active.
func normalizeAuditDate(v string) string {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC().Format("2006-01-02")
	}
	return v
}

// AuditDisplayRow is an AuditRow flattened for the template: nullable
// columns become plain strings ("" when NULL) so the table renders values,
// not pointer addresses. ID is the audit_log PK — the pagination cursor,
// not a rendered column.
type AuditDisplayRow struct {
	ID              int64
	At              string
	Stage           string
	StageBadge      string
	AppID           string
	Endpoint        string
	DeliveryChannel string
	Recipient       string
	ErrorCode       string
	TraceID         string
}

// stageBadge maps a stage to its table badge color family (slide 13):
// red for revocations/denials, green for issued/delivered, blue for app
// lifecycle, gray otherwise.
func stageBadge(stage string) string {
	switch stage {
	case "key_revoked", "denied", "intrusion_kick", "intrusion_unmitigated", "telegram_auth_failed", "bot_not_admin":
		return "badge-red"
	case "key_issued", "delivered", "validated":
		return "badge-green"
	case "received", "dispatched":
		return "badge-blue"
	case "retried", "deferred":
		return "badge-purple"
	default:
		return "badge-gray"
	}
}

// auditTimeLabel condenses the RFC3339 timestamp for the table ("07-06
// 22:58"); unparseable values pass through untouched.
func auditTimeLabel(at string) string {
	if t, err := time.Parse(time.RFC3339, at); err == nil {
		return t.Format("01-02 15:04")
	}
	return at
}

// handleAuditPage renders the audit log viewer. The page is a read-only
// proxy over GET /admin/audit/search — filters pass through verbatim and
// the server stays the single validator, so its 400 codes (invalid_since
// etc.) surface here as Korean banners instead of being re-checked in the
// UI.
func (s *Server) handleAuditPage(w http.ResponseWriter, r *http.Request) {
	// Bare landing (no query string at all) → today's window. An explicit
	// search that cleared the dates (since= present but empty) is a
	// deliberate "all time" request and is respected as-is.
	if r.URL.RawQuery == "" {
		s.render(w, "audit.html", s.auditPageData(r, defaultAuditFilters(time.Now())))
		return
	}
	q := r.URL.Query()
	filters := AuditFilters{
		Limit:    q.Get("limit"),
		Since:    normalizeAuditDate(q.Get("since")),
		Until:    normalizeAuditDate(q.Get("until")),
		TraceID:  q.Get("trace_id"),
		AppID:    q.Get("app_id"),
		Stage:    q.Get("stage"),
		BeforeID: q.Get("before_id"),
	}
	if filters.Limit == "" {
		filters.Limit = defaultAuditLimit
	}
	s.render(w, "audit.html", s.auditPageData(r, filters))
}

// auditPageData builds the audit page (filter form + result table) —
// shared by the GET page and the POST /audit/verify re-render.
func (s *Server) auditPageData(r *http.Request, filters AuditFilters) pageData {
	data := s.basePageData(r, "로그", "audit")
	data.Subtitle = "/admin/audit/search"
	data.AuditFilters = filters
	data.AuditStages = auditStages

	// Dropdown options. The app list degrades quietly (nil store or a
	// failed lookup → the template falls back to a text input); a limit
	// from an old URL that isn't a dropdown step stays selectable.
	data.AuditLimits = auditLimitOptions
	if filters.Limit != "" && !slices.Contains(data.AuditLimits, filters.Limit) {
		data.AuditLimits = append([]string{filters.Limit}, data.AuditLimits...)
	}
	if s.store != nil {
		if apps, err := s.store.ListApps(r.Context()); err == nil {
			for _, a := range apps {
				data.AuditAppOptions = append(data.AuditAppOptions, a.ID)
			}
			if filters.AppID != "" && !slices.Contains(data.AuditAppOptions, filters.AppID) {
				data.AuditAppOptions = append(data.AuditAppOptions, filters.AppID)
				sort.Strings(data.AuditAppOptions)
			}
		}
	}

	// Pagination: ask for one row beyond the page size — the surplus row
	// proves a next page exists without a count query. A non-numeric limit
	// passes through untouched so the server stays the single validator
	// (its invalid_limit comes back as the usual Korean banner). The +1 is
	// clamped to the server's max (500) so a legacy ?limit=500 bookmark
	// still returns results — it merely loses the has-next probe.
	fetchLimit := filters.Limit
	pageSize := 0
	if n, convErr := strconv.Atoi(filters.Limit); convErr == nil && n > 0 {
		pageSize = n
		fetchLimit = strconv.Itoa(min(n+1, 500))
	}

	rows, err := s.client.SearchAudit(r.Context(), apiclient.AuditSearchParams{
		Limit:    fetchLimit,
		Since:    auditDateToRFC3339(filters.Since, false),
		Until:    auditDateToRFC3339(filters.Until, true),
		TraceID:  filters.TraceID,
		AppID:    filters.AppID,
		Stage:    filters.Stage,
		BeforeID: filters.BeforeID,
	})
	if err != nil {
		data.Error = friendlyAPIError(err)
		return data
	}

	hasNext := pageSize > 0 && len(rows) > pageSize
	if hasNext {
		rows = rows[:pageSize]
	}

	data.AuditRows = make([]AuditDisplayRow, 0, len(rows))
	for _, row := range rows {
		data.AuditRows = append(data.AuditRows, AuditDisplayRow{
			ID:              row.ID,
			At:              auditTimeLabel(row.At),
			Stage:           row.Stage,
			StageBadge:      stageBadge(row.Stage),
			AppID:           strOrEmpty(row.AppID),
			Endpoint:        strOrEmpty(row.Endpoint),
			DeliveryChannel: strOrEmpty(row.DeliveryChannel),
			Recipient:       recipientLabel(row),
			ErrorCode:       strOrEmpty(row.ErrorCode),
			TraceID:         strOrEmpty(row.TraceID),
		})
	}

	// Keyset links: "다음" cursors on the last displayed row's id (rows are
	// id-descending); "처음" drops the cursor. Both preserve every filter.
	if hasNext && len(data.AuditRows) > 0 {
		last := data.AuditRows[len(data.AuditRows)-1]
		data.AuditNextURL = auditPageURL(filters, strconv.FormatInt(last.ID, 10))
	}
	if filters.BeforeID != "" {
		data.AuditFirstURL = auditPageURL(filters, "")
	}
	return data
}

// auditPageURL rebuilds /audit with the filter state and the given
// pagination cursor ("" = first page). Empty filters stay out of the URL.
func auditPageURL(f AuditFilters, beforeID string) string {
	v := url.Values{}
	set := func(k, val string) {
		if val != "" {
			v.Set(k, val)
		}
	}
	set("stage", f.Stage)
	set("app_id", f.AppID)
	set("trace_id", f.TraceID)
	set("limit", f.Limit)
	set("since", f.Since)
	set("until", f.Until)
	set("before_id", beforeID)
	if len(v) == 0 {
		return "/audit"
	}
	return "/audit?" + v.Encode()
}

// AuditVerifyView is one verification run's outcome, rendered in the
// integrity card. Hash values are 8-hex-char prefixes — enough to see the
// mismatch, nothing more.
type AuditVerifyView struct {
	OK      bool
	Partial bool // deadline hit: Rows verified clean, rest unchecked
	Rows    int64
	Elapsed string
	Break   *AuditVerifyBreak
}

// AuditVerifyBreak is the first broken row, flattened for the template.
type AuditVerifyBreak struct {
	ID       int64
	At       string
	Stage    string
	Column   string // "prev_hash" or "row_hash"
	Expected string
	Stored   string
}

// handleAuditVerify walks the audit hash chain (read-only) and re-renders
// the audit page with the outcome. POST→render directly, like key
// issuance: the result never travels through a query parameter, so it
// cannot be forged by crafting a URL.
func (s *Server) handleAuditVerify(w http.ResponseWriter, r *http.Request) {
	// The verify POST re-renders the bare page under the result card, so it
	// uses the same today-window defaults as the plain GET landing.
	data := s.auditPageData(r, defaultAuditFilters(time.Now()))
	if s.store == nil {
		data.Error = "무결성 검증은 DB 연결이 필요합니다"
		s.render(w, "audit.html", data)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), auditVerifyTimeout)
	defer cancel()
	start := time.Now()
	res, err := s.store.VerifyAuditChain(ctx)
	elapsed := time.Since(start).Round(time.Millisecond)

	timedOut := errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil
	if err != nil && !timedOut {
		middleware.Log("error", "adminui_audit_verify_failed", map[string]any{
			"trace_id": middleware.TraceID(r.Context()),
			"error":    err.Error(),
		})
		data.Error = "무결성 검증 쿼리에 실패했습니다 — DB 상태를 확인하세요"
		s.render(w, "audit.html", data)
		return
	}

	view := &AuditVerifyView{
		OK:      err == nil && res.OK,
		Partial: err != nil,
		Rows:    res.Rows,
		Elapsed: elapsed.String(),
	}
	if res.Break != nil {
		view.Break = &AuditVerifyBreak{
			ID:       res.Break.ID,
			At:       res.Break.At.UTC().Format(time.RFC3339),
			Stage:    res.Break.Stage,
			Column:   res.Break.Column,
			Expected: hashPrefix(res.Break.Expected),
			Stored:   hashPrefix(res.Break.Stored),
		}
	}
	data.AuditVerify = view
	s.render(w, "audit.html", data)
}

// hashPrefix renders the first 8 hex chars of a chain hash for display.
func hashPrefix(h []byte) string {
	s := hex.EncodeToString(h)
	if len(s) > 8 {
		s = s[:8]
	}
	return s
}

func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// recipientLabel condenses the two recipient columns into one cell:
// user id when present, otherwise the chat id marked as such.
func recipientLabel(row apiclient.AuditRow) string {
	if row.RecipientUserID != nil {
		return strconv.FormatInt(*row.RecipientUserID, 10)
	}
	if row.RecipientChatID != nil {
		return "chat:" + strconv.FormatInt(*row.RecipientChatID, 10)
	}
	return ""
}
