package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CatPope/telegram_server/internal/audit"
	"github.com/CatPope/telegram_server/internal/auth"
)

var validAuditStages = map[audit.Stage]bool{
	audit.StageReceived:             true,
	audit.StageValidated:            true,
	audit.StageDispatched:           true,
	audit.StageDelivered:            true,
	audit.StageDenied:               true,
	audit.StageRetried:              true,
	audit.StageDeferred:             true,
	audit.StageIntrusionKick:        true,
	audit.StageIntrusionUnmitigated: true,
	audit.StageBotNotAdmin:          true,
	audit.StageTelegramAuthFailed:   true,
	audit.StageKeyIssued:            true,
	audit.StageKeyRevoked:           true,
}

// AdminAuditHandler handles audit log search.
type AdminAuditHandler struct {
	Pool  *pgxpool.Pool
	Audit audit.Writer
}

type auditRow struct {
	ID               int64           `json:"id"`
	At               string          `json:"at"`
	TraceID          *string         `json:"trace_id"`
	MessageID        *string         `json:"message_id"`
	Stage            string          `json:"stage"`
	AppID            *string         `json:"app_id"`
	Capability       *string         `json:"capability"`
	CapabilitySetVer *int64          `json:"capability_set_ver"`
	Endpoint         *string         `json:"endpoint"`
	RouteStrategy    *string         `json:"route_strategy"`
	DeliveryChannel  *string         `json:"delivery_channel"`
	RecipientUserID  *int64          `json:"recipient_user_id"`
	RecipientChatID  *int64          `json:"recipient_chat_id"`
	ErrorCode        *string         `json:"error_code"`
	DetailsJSON      json.RawMessage `json:"details_json"`
}

// Search handles GET /admin/audit/search
func (h *AdminAuditHandler) Search(w http.ResponseWriter, r *http.Request) {
	flow := newAuditFlow(w, r, h.Audit, auth.CapAuditSearch)
	flow.received()

	q := r.URL.Query()

	// Parse limit (default 50, reject outside 1–500)
	limit := 50
	if ls := q.Get("limit"); ls != "" {
		v, err := strconv.Atoi(ls)
		if err != nil || v <= 0 || v > 500 {
			flow.deny("invalid_limit", http.StatusBadRequest)
			return
		}
		limit = v
	}

	// Parse optional time bounds
	var since, until *time.Time
	if s := q.Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			flow.deny("invalid_since", http.StatusBadRequest)
			return
		}
		since = &t
	}
	if u := q.Get("until"); u != "" {
		t, err := time.Parse(time.RFC3339, u)
		if err != nil {
			flow.deny("invalid_until", http.StatusBadRequest)
			return
		}
		until = &t
	}

	// Keyset pagination cursor: return only rows with id < before_id.
	// id is BIGSERIAL (insertion order ≈ at order), so the PK index
	// satisfies both the cursor predicate and the sort.
	var beforeID int64
	if b := q.Get("before_id"); b != "" {
		v, err := strconv.ParseInt(b, 10, 64)
		if err != nil || v <= 0 {
			flow.deny("invalid_before_id", http.StatusBadRequest)
			return
		}
		beforeID = v
	}

	traceID := q.Get("trace_id")
	filterAppID := q.Get("app_id")
	stage := q.Get("stage")
	if stage != "" && !validAuditStages[audit.Stage(stage)] {
		flow.deny("invalid_stage", http.StatusBadRequest)
		return
	}

	// Build dynamic WHERE clauses
	args := []any{}
	where := "WHERE 1=1"
	add := func(cond string, val any) {
		args = append(args, val)
		where += " AND " + cond + " = $" + strconv.Itoa(len(args))
	}
	addGte := func(col string, val any) {
		args = append(args, val)
		where += " AND " + col + " >= $" + strconv.Itoa(len(args))
	}
	addLte := func(col string, val any) {
		args = append(args, val)
		where += " AND " + col + " <= $" + strconv.Itoa(len(args))
	}

	if traceID != "" {
		add("trace_id", traceID)
	}
	if filterAppID != "" {
		add("app_id", filterAppID)
	}
	if stage != "" {
		add("stage", stage)
	}
	if since != nil {
		addGte("at", *since)
	}
	if until != nil {
		addLte("at", *until)
	}
	if beforeID > 0 {
		args = append(args, beforeID)
		where += " AND id < $" + strconv.Itoa(len(args))
	}

	args = append(args, limit)
	// ORDER BY id DESC (not at DESC): id is BIGSERIAL insertion order —
	// effectively the same ordering as at — and lets the before_id keyset
	// walk pages off the PK index without a sort.
	sqlQuery := `SELECT id, at, trace_id, message_id, stage, app_id, capability,
		capability_set_ver, endpoint, route_strategy, delivery_channel,
		recipient_user_id, recipient_chat_id, error_code, details_json
	FROM audit_log ` + where + ` ORDER BY id DESC LIMIT $` + strconv.Itoa(len(args))

	rows, err := h.Pool.Query(r.Context(), sqlQuery, args...)
	if err != nil {
		flow.deny("db_error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	results := []auditRow{}
	for rows.Next() {
		var row auditRow
		var at time.Time
		if scanErr := rows.Scan(
			&row.ID, &at, &row.TraceID, &row.MessageID, &row.Stage,
			&row.AppID, &row.Capability, &row.CapabilitySetVer,
			&row.Endpoint, &row.RouteStrategy, &row.DeliveryChannel,
			&row.RecipientUserID, &row.RecipientChatID,
			&row.ErrorCode, &row.DetailsJSON,
		); scanErr != nil {
			flow.deny("db_error", http.StatusInternalServerError)
			return
		}
		row.At = at.UTC().Format(time.RFC3339)
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		flow.deny("db_error", http.StatusInternalServerError)
		return
	}

	flow.succeed(map[string]any{
		"result_count":     len(results),
		"requested_limit":  limit,
		"filter_trace_id":  traceID != "",
		"filter_app_id":    filterAppID,
		"filter_stage":     stage,
		"filter_since":     since != nil,
		"filter_until":     until != nil,
		"filter_before_id": beforeID,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"results": results,
		"limit":   limit,
	})
}
