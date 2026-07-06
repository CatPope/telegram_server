package adminui

import (
	"net/http"
	"strconv"

	"github.com/CatPope/telegram_server/internal/adminui/apiclient"
)

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

// AuditFilters echoes the operator's GET query back into the filter form
// so a submitted search keeps its inputs visible alongside the results.
type AuditFilters struct {
	Limit   string
	Since   string
	Until   string
	TraceID string
	AppID   string
	Stage   string
}

// AuditDisplayRow is an AuditRow flattened for the template: nullable
// columns become plain strings ("" when NULL) so the table renders values,
// not pointer addresses.
type AuditDisplayRow struct {
	At              string
	Stage           string
	AppID           string
	Endpoint        string
	DeliveryChannel string
	Recipient       string
	ErrorCode       string
	TraceID         string
}

// handleAuditPage renders the audit log viewer. The page is a read-only
// proxy over GET /admin/audit/search — filters pass through verbatim and
// the server stays the single validator, so its 400 codes (invalid_since
// etc.) surface here as Korean banners instead of being re-checked in the
// UI.
func (s *Server) handleAuditPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filters := AuditFilters{
		Limit:   q.Get("limit"),
		Since:   q.Get("since"),
		Until:   q.Get("until"),
		TraceID: q.Get("trace_id"),
		AppID:   q.Get("app_id"),
		Stage:   q.Get("stage"),
	}

	data := s.basePageData(r, "Audit", "audit")
	data.AuditFilters = filters
	data.AuditStages = auditStages

	rows, err := s.client.SearchAudit(r.Context(), apiclient.AuditSearchParams{
		Limit:   filters.Limit,
		Since:   filters.Since,
		Until:   filters.Until,
		TraceID: filters.TraceID,
		AppID:   filters.AppID,
		Stage:   filters.Stage,
	})
	if err != nil {
		data.Error = friendlyAPIError(err)
		s.render(w, "audit.html", data)
		return
	}

	data.AuditRows = make([]AuditDisplayRow, 0, len(rows))
	for _, row := range rows {
		data.AuditRows = append(data.AuditRows, AuditDisplayRow{
			At:              row.At,
			Stage:           row.Stage,
			AppID:           strOrEmpty(row.AppID),
			Endpoint:        strOrEmpty(row.Endpoint),
			DeliveryChannel: strOrEmpty(row.DeliveryChannel),
			Recipient:       recipientLabel(row),
			ErrorCode:       strOrEmpty(row.ErrorCode),
			TraceID:         strOrEmpty(row.TraceID),
		})
	}
	s.render(w, "audit.html", data)
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
