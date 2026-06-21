package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/CatPope/telegram_server/internal/api/middleware"
	"github.com/CatPope/telegram_server/internal/audit"
	"github.com/CatPope/telegram_server/internal/auth"
)

type NoopHandler struct {
	Audit audit.Writer
}

func (h *NoopHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.RequesterFrom(r.Context())
	trace := middleware.TraceID(r.Context())
	if err := h.Audit.Write(r.Context(), audit.Event{
		TraceID:    trace,
		Stage:      audit.StageReceived,
		Endpoint:   r.URL.Path,
		AppID:      id.AppID,
		Capability: string(auth.CapNoopInvoke),
	}); err != nil {
		middleware.Log("error", "audit_write_failed", map[string]any{
			"trace_id": trace,
			"error":    err.Error(),
		})
		http.Error(w, `{"error":"audit_unavailable"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":       true,
		"trace_id": trace,
		"app_id":   id.AppID,
	})
}
