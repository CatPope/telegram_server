package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CatPope/telegram_server/internal/api/middleware"
	"github.com/CatPope/telegram_server/internal/audit"
	"github.com/CatPope/telegram_server/internal/auth"
)

// AdminSubscriptionsHandler handles admin subscription management.
// NOTE: This handler does NOT call the topic provisioner. Provisioning only
// runs inside the bot context (/apps command). After subscribing a user via
// this API, the operator must ask the user to run /apps so the topic
// materialises in the supergroup.
type AdminSubscriptionsHandler struct {
	Pool  *pgxpool.Pool
	Audit audit.Writer
}

// Subscribe handles POST /admin/users/{telegram_id}/subscriptions/{app_id}
func (h *AdminSubscriptionsHandler) Subscribe(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.RequesterFrom(r.Context())
	trace := middleware.TraceID(r.Context())
	messageID := uuid.NewString()
	endpoint := r.URL.Path
	cap := string(auth.CapAppsRegister)
	telegramIDStr := chi.URLParam(r, "telegram_id")
	appID := chi.URLParam(r, "app_id")

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

	ctx := r.Context()

	// Validate app exists and is active
	var appActive bool
	if scanErr := h.Pool.QueryRow(ctx, `SELECT active FROM apps WHERE id=$1`, appID).Scan(&appActive); scanErr != nil {
		if errors.Is(scanErr, pgx.ErrNoRows) {
			deny("app_not_found", http.StatusNotFound)
			return
		}
		deny("db_error", http.StatusInternalServerError)
		return
	}
	if !appActive {
		deny("app_inactive", http.StatusBadRequest)
		return
	}

	// Resolve users.id from telegram_id
	var userID int64
	if scanErr := h.Pool.QueryRow(ctx,
		`SELECT id FROM users WHERE telegram_id=$1`, telegramID,
	).Scan(&userID); scanErr != nil {
		deny("user_not_found", http.StatusNotFound)
		return
	}

	_, err = h.Pool.Exec(ctx,
		`INSERT INTO user_subscriptions (user_id, app_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		userID, appID,
	)
	if err != nil {
		deny("db_error", http.StatusInternalServerError)
		return
	}

	// Operator note: topic provisioning is bot-driven; user must run /apps for
	// the topic to materialise in the supergroup.
	middleware.Log("info", "admin_subscription_created", map[string]any{
		"telegram_id": telegramID,
		"app_id":      appID,
		"note":        "topic not yet provisioned; user must run /apps",
	})

	_ = writeAuditSafe(r, h.Audit, audit.Event{
		TraceID:          trace,
		MessageID:        messageID,
		Stage:            audit.StageValidated,
		Endpoint:         endpoint,
		AppID:            id.AppID,
		Capability:       cap,
		CapabilitySetVer: id.CapabilitySetVer,
		Details:          map[string]any{"telegram_id": telegramID, "app_id": appID},
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
	_ = json.NewEncoder(w).Encode(map[string]any{"telegram_id": telegramID, "app_id": appID, "subscribed": true})
}

// Unsubscribe handles DELETE /admin/users/{telegram_id}/subscriptions/{app_id}
func (h *AdminSubscriptionsHandler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.RequesterFrom(r.Context())
	trace := middleware.TraceID(r.Context())
	messageID := uuid.NewString()
	endpoint := r.URL.Path
	cap := string(auth.CapAppsRegister)
	telegramIDStr := chi.URLParam(r, "telegram_id")
	appID := chi.URLParam(r, "app_id")

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

	ctx := r.Context()

	tag, err := h.Pool.Exec(ctx,
		`DELETE FROM user_subscriptions
		 WHERE user_id = (SELECT id FROM users WHERE telegram_id=$1)
		   AND app_id = $2`,
		telegramID, appID,
	)
	if err != nil {
		deny("db_error", http.StatusInternalServerError)
		return
	}
	if tag.RowsAffected() == 0 {
		deny("subscription_not_found", http.StatusNotFound)
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
		Details:          map[string]any{"telegram_id": telegramID, "app_id": appID},
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
	_ = json.NewEncoder(w).Encode(map[string]any{"telegram_id": telegramID, "app_id": appID, "subscribed": false})
}
