package handlers

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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

// validGrades is shared with the apps admin handlers (min_grade validation).
var validGrades = map[string]bool{
	"user":      true,
	"developer": true,
	"admin":     true,
}

// Patch handles PATCH /admin/users/{telegram_id}
func (h *AdminUsersHandler) Patch(w http.ResponseWriter, r *http.Request) {
	flow := newAuditFlow(w, r, h.Audit, auth.CapUsersPromote)

	telegramID, err := strconv.ParseInt(chi.URLParam(r, "telegram_id"), 10, 64)
	if err != nil {
		flow.deny("invalid_telegram_id", http.StatusBadRequest)
		return
	}

	flow.received()

	var req patchUserRequest
	if err := decodeStrict(r, &req); err != nil {
		flow.deny("malformed_json", http.StatusBadRequest)
		return
	}

	if !validGrades[req.Grade] {
		flow.deny("invalid_grade", http.StatusBadRequest)
		return
	}

	tag, err := h.Pool.Exec(r.Context(),
		`UPDATE users SET grade=$1 WHERE telegram_id=$2`,
		req.Grade, telegramID,
	)
	if err != nil {
		flow.deny("db_error", http.StatusInternalServerError)
		return
	}
	if tag.RowsAffected() == 0 {
		flow.deny("user_not_found", http.StatusNotFound)
		return
	}

	flow.succeed(map[string]any{"telegram_id": telegramID, "grade": req.Grade})
	writeJSON(w, http.StatusOK, map[string]any{"telegram_id": telegramID, "grade": req.Grade})
}
