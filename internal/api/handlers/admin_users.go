package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CatPope/telegram_server/internal/api/middleware"
	"github.com/CatPope/telegram_server/internal/audit"
	"github.com/CatPope/telegram_server/internal/auth"
)

// AdminUsersHandler handles user admin operations.
type AdminUsersHandler struct {
	Pool  *pgxpool.Pool
	Audit audit.Writer
}

type patchUserRequest struct {
	Grade string `json:"grade"`
}

var validGrades = map[string]bool{
	"user":      true,
	"developer": true,
	"admin":     true,
}

// Patch handles PATCH /admin/users/{telegram_id}
func (h *AdminUsersHandler) Patch(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.RequesterFrom(r.Context())
	trace := middleware.TraceID(r.Context())
	messageID := uuid.NewString()
	endpoint := r.URL.Path
	cap := string(auth.CapUsersPromote)
	telegramIDStr := chi.URLParam(r, "telegram_id")

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

	telegramID, err := strconv.ParseInt(telegramIDStr, 10, 64)
	if err != nil {
		deny("invalid_telegram_id", http.StatusBadRequest)
		return
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

	var req patchUserRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		deny("malformed_json", http.StatusBadRequest)
		return
	}

	if !validGrades[req.Grade] {
		deny("invalid_grade", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	tag, err := h.Pool.Exec(ctx,
		`UPDATE users SET grade=$1 WHERE telegram_id=$2`,
		req.Grade, telegramID,
	)
	if err != nil {
		deny("db_error", http.StatusInternalServerError)
		return
	}
	if tag.RowsAffected() == 0 {
		deny("user_not_found", http.StatusNotFound)
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
		Details:          map[string]any{"telegram_id": telegramID, "grade": req.Grade},
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
	_ = json.NewEncoder(w).Encode(map[string]any{"telegram_id": telegramID, "grade": req.Grade})
}
