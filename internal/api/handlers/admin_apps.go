package handlers

import (
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CatPope/telegram_server/internal/audit"
	"github.com/CatPope/telegram_server/internal/auth"
)

var appIDRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,63}$`)

var allowedCapabilities = map[string]bool{
	"messages.direct.send":    true,
	"messages.direct.dm":      true,
	"messages.topic.send":     true,
	"messages.broadcast.send": true,
	"noop.invoke":             true,
}

var forbiddenCapabilities = map[string]bool{
	"apps.register":    true,
	"users.promote":    true,
	"users.deactivate": true,
	"audit.search":     true,
	"audit.freeze":     true,
}

// validateCapabilityGrant enforces the grant rules shared by Create and
// Patch: management capabilities can never be added via the API, and unknown
// names are rejected. Returns an empty code when every capability is
// grantable.
func validateCapabilityGrant(caps []string) (code string, status int) {
	for _, c := range caps {
		if forbiddenCapabilities[c] {
			return "forbidden_capability", http.StatusForbidden
		}
		if !allowedCapabilities[c] {
			return "unknown_capability", http.StatusBadRequest
		}
	}
	return "", 0
}

// AdminAppsHandler handles CRUD operations for apps via the admin API.
type AdminAppsHandler struct {
	Pool  *pgxpool.Pool
	Audit audit.Writer
}

type createAppRequest struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	MinGrade     string   `json:"min_grade"`
	Capabilities []string `json:"capabilities"`
}

type patchAppRequest struct {
	Description        *string  `json:"description"`
	MinGrade           *string  `json:"min_grade"`
	Active             *bool    `json:"active"`
	AddCapabilities    []string `json:"add_capabilities"`
	RemoveCapabilities []string `json:"remove_capabilities"`
}

// Create handles POST /admin/apps
func (h *AdminAppsHandler) Create(w http.ResponseWriter, r *http.Request) {
	flow := newAuditFlow(w, r, h.Audit, auth.CapAppsRegister)
	flow.received()

	var req createAppRequest
	if err := decodeStrict(r, &req); err != nil {
		flow.deny("malformed_json", http.StatusBadRequest)
		return
	}

	if req.ID == "" || req.Name == "" {
		flow.deny("missing_required_fields", http.StatusBadRequest)
		return
	}
	if !appIDRegex.MatchString(req.ID) {
		flow.deny("invalid_app_id", http.StatusBadRequest)
		return
	}
	if req.MinGrade != "" && !validGrades[req.MinGrade] {
		flow.deny("invalid_min_grade", http.StatusBadRequest)
		return
	}
	if code, status := validateCapabilityGrant(req.Capabilities); code != "" {
		flow.deny(code, status)
		return
	}

	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		flow.deny("db_error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// created_by records who registered the app — the authenticated
	// requester's app id ('' if the identity is somehow absent).
	createdBy := ""
	if requester, ok := auth.RequesterFrom(ctx); ok {
		createdBy = requester.AppID
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO apps (id, name, description, min_grade, active, capability_set_version, created_by)
		 VALUES ($1, $2, $3, COALESCE(NULLIF($4,''),'user'), true, 1, $5)`,
		req.ID, req.Name, req.Description, req.MinGrade, createdBy,
	)
	if err != nil {
		flow.deny("app_already_exists", http.StatusConflict)
		return
	}

	for _, c := range req.Capabilities {
		if _, capErr := tx.Exec(ctx,
			`INSERT INTO app_capabilities (app_id, capability) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			req.ID, c,
		); capErr != nil {
			flow.deny("db_error", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		flow.deny("db_error", http.StatusInternalServerError)
		return
	}

	flow.succeed(map[string]any{"created_app_id": req.ID, "created_by": createdBy})
	writeJSON(w, http.StatusCreated, map[string]any{"id": req.ID})
}

// Patch handles PATCH /admin/apps/{id}
func (h *AdminAppsHandler) Patch(w http.ResponseWriter, r *http.Request) {
	flow := newAuditFlow(w, r, h.Audit, auth.CapAppsRegister)
	appID := chi.URLParam(r, "id")
	flow.received()

	var req patchAppRequest
	if err := decodeStrict(r, &req); err != nil {
		flow.deny("malformed_json", http.StatusBadRequest)
		return
	}

	if req.MinGrade != nil && !validGrades[*req.MinGrade] {
		flow.deny("invalid_min_grade", http.StatusBadRequest)
		return
	}
	// Removals are not gated — deleting an unknown capability is a no-op and
	// stripping a management capability only ever de-escalates.
	if code, status := validateCapabilityGrant(req.AddCapabilities); code != "" {
		flow.deny(code, status)
		return
	}

	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		flow.deny("db_error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Check app exists
	var exists bool
	if scanErr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM apps WHERE id=$1)`, appID).Scan(&exists); scanErr != nil || !exists {
		flow.deny("app_not_found", http.StatusNotFound)
		return
	}

	// Serialize concurrent mutations with a row-lock
	if _, lockErr := tx.Exec(ctx, `SELECT 1 FROM apps WHERE id=$1 FOR UPDATE`, appID); lockErr != nil {
		flow.deny("db_error", http.StatusInternalServerError)
		return
	}

	// Apply scalar field updates
	if req.Description != nil {
		if _, execErr := tx.Exec(ctx, `UPDATE apps SET description=$1 WHERE id=$2`, *req.Description, appID); execErr != nil {
			flow.deny("db_error", http.StatusInternalServerError)
			return
		}
	}
	if req.MinGrade != nil {
		if _, execErr := tx.Exec(ctx, `UPDATE apps SET min_grade=$1 WHERE id=$2`, *req.MinGrade, appID); execErr != nil {
			flow.deny("db_error", http.StatusInternalServerError)
			return
		}
	}
	if req.Active != nil {
		if _, execErr := tx.Exec(ctx, `UPDATE apps SET active=$1 WHERE id=$2`, *req.Active, appID); execErr != nil {
			flow.deny("db_error", http.StatusInternalServerError)
			return
		}
	}

	// Capability changes — bump capability_set_version atomically
	capsChanged := len(req.AddCapabilities) > 0 || len(req.RemoveCapabilities) > 0
	for _, c := range req.AddCapabilities {
		if _, execErr := tx.Exec(ctx,
			`INSERT INTO app_capabilities (app_id, capability) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			appID, c,
		); execErr != nil {
			flow.deny("db_error", http.StatusInternalServerError)
			return
		}
	}
	for _, c := range req.RemoveCapabilities {
		if _, execErr := tx.Exec(ctx,
			`DELETE FROM app_capabilities WHERE app_id=$1 AND capability=$2`,
			appID, c,
		); execErr != nil {
			flow.deny("db_error", http.StatusInternalServerError)
			return
		}
	}
	if capsChanged {
		if _, execErr := tx.Exec(ctx,
			`UPDATE apps SET capability_set_version = capability_set_version + 1 WHERE id=$1`,
			appID,
		); execErr != nil {
			flow.deny("db_error", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		flow.deny("db_error", http.StatusInternalServerError)
		return
	}

	flow.succeed(map[string]any{"patched_app_id": appID, "caps_changed": capsChanged})
	writeJSON(w, http.StatusOK, map[string]any{"id": appID, "updated": true})
}

// Delete handles DELETE /admin/apps/{id} (soft delete)
func (h *AdminAppsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	flow := newAuditFlow(w, r, h.Audit, auth.CapAppsRegister)
	appID := chi.URLParam(r, "id")
	flow.received()

	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		flow.deny("db_error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, lockErr := tx.Exec(ctx, `SELECT 1 FROM apps WHERE id=$1 FOR UPDATE`, appID); lockErr != nil {
		flow.deny("db_error", http.StatusInternalServerError)
		return
	}

	tag, err := tx.Exec(ctx,
		`UPDATE apps SET active=false, capability_set_version = capability_set_version + 1 WHERE id=$1`,
		appID,
	)
	if err != nil {
		flow.deny("db_error", http.StatusInternalServerError)
		return
	}
	if tag.RowsAffected() == 0 {
		flow.deny("app_not_found", http.StatusNotFound)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		flow.deny("db_error", http.StatusInternalServerError)
		return
	}

	flow.succeed(map[string]any{"deleted_app_id": appID})
	writeJSON(w, http.StatusOK, map[string]any{"id": appID, "active": false})
}

// Purge handles DELETE /admin/apps/{id}/purge (hard delete). The apps row
// is removed and app_capabilities/app_keys/user_subscriptions/user_topics
// cascade with it; audit_log.app_id has no FK, so the audit hash chain is
// preserved intact.
func (h *AdminAppsHandler) Purge(w http.ResponseWriter, r *http.Request) {
	flow := newAuditFlow(w, r, h.Audit, auth.CapAppsRegister)
	appID := chi.URLParam(r, "id")
	flow.received()

	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		flow.deny("db_error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, lockErr := tx.Exec(ctx, `SELECT 1 FROM apps WHERE id=$1 FOR UPDATE`, appID); lockErr != nil {
		flow.deny("db_error", http.StatusInternalServerError)
		return
	}

	tag, err := tx.Exec(ctx, `DELETE FROM apps WHERE id=$1`, appID)
	if err != nil {
		flow.deny("db_error", http.StatusInternalServerError)
		return
	}
	if tag.RowsAffected() == 0 {
		flow.deny("app_not_found", http.StatusNotFound)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		flow.deny("db_error", http.StatusInternalServerError)
		return
	}

	flow.succeed(map[string]any{"purged_app_id": appID})
	writeJSON(w, http.StatusOK, map[string]any{"id": appID, "purged": true})
}
