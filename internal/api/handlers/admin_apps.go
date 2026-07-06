package handlers

import (
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CatPope/telegram_server/internal/api/middleware"
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

var validMinGrades = map[string]bool{
	"user":      true,
	"developer": true,
	"admin":     true,
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
	id, _ := auth.RequesterFrom(r.Context())
	trace := middleware.TraceID(r.Context())
	messageID := uuid.NewString()
	endpoint := r.URL.Path
	cap := string(auth.CapAppsRegister)

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

	var req createAppRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		deny("malformed_json", http.StatusBadRequest)
		return
	}

	if req.ID == "" || req.Name == "" {
		deny("missing_required_fields", http.StatusBadRequest)
		return
	}
	if !appIDRegex.MatchString(req.ID) {
		deny("invalid_app_id", http.StatusBadRequest)
		return
	}
	if req.MinGrade != "" && !validMinGrades[req.MinGrade] {
		deny("invalid_min_grade", http.StatusBadRequest)
		return
	}
	for _, c := range req.Capabilities {
		if forbiddenCapabilities[c] {
			deny("forbidden_capability", http.StatusForbidden)
			return
		}
		if !allowedCapabilities[c] {
			deny("unknown_capability", http.StatusBadRequest)
			return
		}
	}

	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		deny("db_error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	_, err = tx.Exec(ctx,
		`INSERT INTO apps (id, name, description, min_grade, active, capability_set_version)
		 VALUES ($1, $2, $3, COALESCE(NULLIF($4,''),'user'), true, 1)`,
		req.ID, req.Name, req.Description, req.MinGrade,
	)
	if err != nil {
		deny("app_already_exists", http.StatusConflict)
		return
	}

	for _, c := range req.Capabilities {
		if _, capErr := tx.Exec(ctx,
			`INSERT INTO app_capabilities (app_id, capability) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			req.ID, c,
		); capErr != nil {
			deny("db_error", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
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
		Details:          map[string]any{"created_app_id": req.ID},
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
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"id": req.ID})
}

// Patch handles PATCH /admin/apps/{id}
func (h *AdminAppsHandler) Patch(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.RequesterFrom(r.Context())
	trace := middleware.TraceID(r.Context())
	messageID := uuid.NewString()
	endpoint := r.URL.Path
	cap := string(auth.CapAppsRegister)
	appID := chi.URLParam(r, "id")

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

	var req patchAppRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		deny("malformed_json", http.StatusBadRequest)
		return
	}

	if req.MinGrade != nil && !validMinGrades[*req.MinGrade] {
		deny("invalid_min_grade", http.StatusBadRequest)
		return
	}
	// Same grant rules as Create: management capabilities can never be
	// added via the API, and unknown names are rejected. Removals are not
	// gated — deleting an unknown capability is a no-op and stripping a
	// management capability only ever de-escalates.
	for _, c := range req.AddCapabilities {
		if forbiddenCapabilities[c] {
			deny("forbidden_capability", http.StatusForbidden)
			return
		}
		if !allowedCapabilities[c] {
			deny("unknown_capability", http.StatusBadRequest)
			return
		}
	}

	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		deny("db_error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Check app exists
	var exists bool
	if scanErr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM apps WHERE id=$1)`, appID).Scan(&exists); scanErr != nil || !exists {
		deny("app_not_found", http.StatusNotFound)
		return
	}

	// Serialize concurrent mutations with a row-lock
	if _, lockErr := tx.Exec(ctx, `SELECT 1 FROM apps WHERE id=$1 FOR UPDATE`, appID); lockErr != nil {
		deny("db_error", http.StatusInternalServerError)
		return
	}

	// Apply scalar field updates
	if req.Description != nil {
		if _, execErr := tx.Exec(ctx, `UPDATE apps SET description=$1 WHERE id=$2`, *req.Description, appID); execErr != nil {
			deny("db_error", http.StatusInternalServerError)
			return
		}
	}
	if req.MinGrade != nil {
		if _, execErr := tx.Exec(ctx, `UPDATE apps SET min_grade=$1 WHERE id=$2`, *req.MinGrade, appID); execErr != nil {
			deny("db_error", http.StatusInternalServerError)
			return
		}
	}
	if req.Active != nil {
		if _, execErr := tx.Exec(ctx, `UPDATE apps SET active=$1 WHERE id=$2`, *req.Active, appID); execErr != nil {
			deny("db_error", http.StatusInternalServerError)
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
			deny("db_error", http.StatusInternalServerError)
			return
		}
	}
	for _, c := range req.RemoveCapabilities {
		if _, execErr := tx.Exec(ctx,
			`DELETE FROM app_capabilities WHERE app_id=$1 AND capability=$2`,
			appID, c,
		); execErr != nil {
			deny("db_error", http.StatusInternalServerError)
			return
		}
	}
	if capsChanged {
		if _, execErr := tx.Exec(ctx,
			`UPDATE apps SET capability_set_version = capability_set_version + 1 WHERE id=$1`,
			appID,
		); execErr != nil {
			deny("db_error", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
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
		Details:          map[string]any{"patched_app_id": appID, "caps_changed": capsChanged},
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
	_ = json.NewEncoder(w).Encode(map[string]any{"id": appID, "updated": true})
}

// Delete handles DELETE /admin/apps/{id} (soft delete)
func (h *AdminAppsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.RequesterFrom(r.Context())
	trace := middleware.TraceID(r.Context())
	messageID := uuid.NewString()
	endpoint := r.URL.Path
	cap := string(auth.CapAppsRegister)
	appID := chi.URLParam(r, "id")

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

	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		deny("db_error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, lockErr := tx.Exec(ctx, `SELECT 1 FROM apps WHERE id=$1 FOR UPDATE`, appID); lockErr != nil {
		deny("db_error", http.StatusInternalServerError)
		return
	}

	tag, err := tx.Exec(ctx,
		`UPDATE apps SET active=false, capability_set_version = capability_set_version + 1 WHERE id=$1`,
		appID,
	)
	if err != nil {
		deny("db_error", http.StatusInternalServerError)
		return
	}
	if tag.RowsAffected() == 0 {
		deny("app_not_found", http.StatusNotFound)
		return
	}

	if err := tx.Commit(ctx); err != nil {
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
		Details:          map[string]any{"deleted_app_id": appID},
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
	_ = json.NewEncoder(w).Encode(map[string]any{"id": appID, "active": false})
}
