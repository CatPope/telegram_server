package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
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
	flow := newAuditFlow(w, r, h.Audit, auth.CapAppsRegister)
	appID := chi.URLParam(r, "app_id")

	telegramID, err := strconv.ParseInt(chi.URLParam(r, "telegram_id"), 10, 64)
	if err != nil {
		flow.deny("invalid_telegram_id", http.StatusBadRequest)
		return
	}

	flow.received()

	ctx := r.Context()

	// Validate app exists and is active
	var appActive bool
	if scanErr := h.Pool.QueryRow(ctx, `SELECT active FROM apps WHERE id=$1`, appID).Scan(&appActive); scanErr != nil {
		if errors.Is(scanErr, pgx.ErrNoRows) {
			flow.deny("app_not_found", http.StatusNotFound)
			return
		}
		flow.deny("db_error", http.StatusInternalServerError)
		return
	}
	if !appActive {
		flow.deny("app_inactive", http.StatusBadRequest)
		return
	}

	// Resolve users.id from telegram_id
	var userID int64
	if scanErr := h.Pool.QueryRow(ctx,
		`SELECT id FROM users WHERE telegram_id=$1`, telegramID,
	).Scan(&userID); scanErr != nil {
		flow.deny("user_not_found", http.StatusNotFound)
		return
	}

	_, err = h.Pool.Exec(ctx,
		`INSERT INTO user_subscriptions (user_id, app_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		userID, appID,
	)
	if err != nil {
		flow.deny("db_error", http.StatusInternalServerError)
		return
	}

	// Operator note: topic provisioning is bot-driven; user must run /apps for
	// the topic to materialise in the supergroup.
	middleware.Log("info", "admin_subscription_created", map[string]any{
		"telegram_id": telegramID,
		"app_id":      appID,
		"note":        "topic not yet provisioned; user must run /apps",
	})

	flow.succeed(map[string]any{"telegram_id": telegramID, "app_id": appID})
	writeJSON(w, http.StatusOK, map[string]any{"telegram_id": telegramID, "app_id": appID, "subscribed": true})
}

// Unsubscribe handles DELETE /admin/users/{telegram_id}/subscriptions/{app_id}
func (h *AdminSubscriptionsHandler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	flow := newAuditFlow(w, r, h.Audit, auth.CapAppsRegister)
	appID := chi.URLParam(r, "app_id")

	telegramID, err := strconv.ParseInt(chi.URLParam(r, "telegram_id"), 10, 64)
	if err != nil {
		flow.deny("invalid_telegram_id", http.StatusBadRequest)
		return
	}

	flow.received()

	tag, err := h.Pool.Exec(r.Context(),
		`DELETE FROM user_subscriptions
		 WHERE user_id = (SELECT id FROM users WHERE telegram_id=$1)
		   AND app_id = $2`,
		telegramID, appID,
	)
	if err != nil {
		flow.deny("db_error", http.StatusInternalServerError)
		return
	}
	if tag.RowsAffected() == 0 {
		flow.deny("subscription_not_found", http.StatusNotFound)
		return
	}

	flow.succeed(map[string]any{"telegram_id": telegramID, "app_id": appID})
	writeJSON(w, http.StatusOK, map[string]any{"telegram_id": telegramID, "app_id": appID, "subscribed": false})
}
