package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CatPope/telegram_server/internal/api/middleware"
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
	id, _ := auth.RequesterFrom(r.Context())
	trace := middleware.TraceID(r.Context())
	messageID := uuid.NewString()
	endpoint := r.URL.Path
	cap := string(auth.CapAuditSearch)

	deny := func(code string, status int) {
		_ = writeAuditSafe(r, h.Audit, audit.Event{
			TraceID:          trace,
			MessageID:        messageID,
			Stage:            audit.StageDenied,
			Endpoint:         endpoint,
			AppID:            id.AppID,
			Capability:       cap,
			CapabilitySetVer: id.CapabilitySetVer,
			ErrorCode:        code,
		})
		writeError(w, status, code)
	}

	_ = writeAuditSafe(r, h.Audit, audit.Event{
		TraceID:          trace,
		MessageID:        messageID,
		Stage:            audit.StageReceived,
		Endpoint:         endpoint,
		AppID:            id.AppID,
		Capability:       cap,
		CapabilitySetVer: id.CapabilitySetVer,
	})

	q := r.URL.Query()

	// Parse limit (default 50, reject outside 1–500)
	limit := 50
	if ls := q.Get("limit"); ls != "" {
		v, err := strconv.Atoi(ls)
		if err != nil || v <= 0 || v > 500 {
			deny("invalid_limit", http.StatusBadRequest)
			return
		}
		limit = v
	}

	// Parse optional time bounds
	var since, until *time.Time
	if s := q.Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			deny("invalid_since", http.StatusBadRequest)
			return
		}
		since = &t
	}
	if u := q.Get("until"); u != "" {
		t, err := time.Parse(time.RFC3339, u)
		if err != nil {
			deny("invalid_until", http.StatusBadRequest)
			return
		}
		until = &t
	}

	traceID := q.Get("trace_id")
	filterAppID := q.Get("app_id")
	stage := q.Get("stage")
	if stage != "" && !validAuditStages[audit.Stage(stage)] {
		deny("invalid_stage", http.StatusBadRequest)
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

	args = append(args, limit)
	sqlQuery := `SELECT id, at, trace_id, message_id, stage, app_id, capability,
		capability_set_ver, endpoint, route_strategy, delivery_channel,
		recipient_user_id, recipient_chat_id, error_code, details_json
	FROM audit_log ` + where + ` ORDER BY at DESC LIMIT $` + strconv.Itoa(len(args))

	ctx := r.Context()
	rows, err := h.Pool.Query(ctx, sqlQuery, args...)
	if err != nil {
		deny("db_error", http.StatusInternalServerError)
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
			deny("db_error", http.StatusInternalServerError)
			return
		}
		row.At = at.UTC().Format(time.RFC3339)
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		deny("db_error", http.StatusInternalServerError)
		return
	}

	_ = writeAuditSafe(r, h.Audit, audit.Event{
		TraceID:          trace,
		MessageID:        messageID,
		Stage:            audit.StageValidated,
		Endpoint:         endpoint,
		AppID:            id.AppID,
		Capability:       cap,
		CapabilitySetVer: id.CapabilitySetVer,
		Details: map[string]any{
			"result_count":    len(results),
			"requested_limit": limit,
			"filter_trace_id": traceID != "",
			"filter_app_id":   filterAppID,
			"filter_stage":    stage,
			"filter_since":    since != nil,
			"filter_until":    until != nil,
		},
	})
	_ = writeAuditSafe(r, h.Audit, audit.Event{
		TraceID:          trace,
		MessageID:        messageID,
		Stage:            audit.StageDelivered,
		Endpoint:         endpoint,
		AppID:            id.AppID,
		Capability:       cap,
		CapabilitySetVer: id.CapabilitySetVer,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"results": results,
		"limit":   limit,
	})
}
