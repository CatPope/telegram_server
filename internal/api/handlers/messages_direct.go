package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/CatPope/telegram_server/internal/api/middleware"
	"github.com/CatPope/telegram_server/internal/audit"
	"github.com/CatPope/telegram_server/internal/auth"
	"github.com/CatPope/telegram_server/internal/dispatch"
	"github.com/CatPope/telegram_server/internal/dispatch/strategy"
)

type DirectHandler struct {
	Strategy   strategy.RouteStrategy
	Dispatcher dispatch.Dispatcher
	Audit      audit.Writer
}

type directRequest struct {
	Recipients []int64           `json:"recipients"`
	AppID      string            `json:"app_id"`
	Envelope   strategy.Envelope `json:"envelope"`
}

type recipientReport struct {
	UserID    int64  `json:"user_id"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	MessageID int64  `json:"telegram_message_id,omitempty"`
}

type directResponse struct {
	MessageID  string            `json:"message_id"`
	Delivered  int               `json:"delivered"`
	Skipped    int               `json:"skipped"`
	Failed     int               `json:"failed"`
	Recipients []recipientReport `json:"recipients"`
}

func (h *DirectHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.RequesterFrom(r.Context())
	trace := middleware.TraceID(r.Context())
	messageID := uuid.NewString()

	if err := h.writeAudit(r, audit.Event{
		TraceID:          trace,
		MessageID:        messageID,
		Stage:            audit.StageReceived,
		Endpoint:         r.URL.Path,
		AppID:            id.AppID,
		Capability:       string(auth.CapMessagesDirect),
		CapabilitySetVer: id.CapabilitySetVer,
		RouteStrategy:    h.Strategy.Name(),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "audit_unavailable")
		return
	}

	var req directRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		h.writeAuditDenied(r, id, messageID, trace, "malformed_json")
		writeError(w, http.StatusBadRequest, "malformed_json")
		return
	}

	if req.Envelope.SchemaVersion == 0 {
		h.writeAuditDenied(r, id, messageID, trace, "missing_envelope_version")
		writeError(w, http.StatusBadRequest, "missing_envelope_version")
		return
	}
	if req.Envelope.SchemaVersion != 1 {
		h.writeAuditDenied(r, id, messageID, trace, "unsupported_envelope_version")
		writeError(w, http.StatusBadRequest, "unsupported_envelope_version")
		return
	}
	if req.Envelope.Text == "" {
		h.writeAuditDenied(r, id, messageID, trace, "empty_envelope_text")
		writeError(w, http.StatusBadRequest, "empty_envelope_text")
		return
	}
	if req.AppID == "" {
		h.writeAuditDenied(r, id, messageID, trace, "missing_app_id")
		writeError(w, http.StatusBadRequest, "missing_app_id")
		return
	}
	if len(req.Recipients) == 0 {
		h.writeAuditDenied(r, id, messageID, trace, "empty_recipients")
		writeError(w, http.StatusBadRequest, "empty_recipients")
		return
	}

	resolved, err := h.Strategy.Resolve(r.Context(), strategy.Request{
		AppID:      req.AppID,
		Recipients: req.Recipients,
		Envelope:   req.Envelope,
	})
	if err != nil {
		code := "resolver_error"
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, strategy.ErrAppNotFound):
			code = "app_not_found"
			status = http.StatusBadRequest
		case errors.Is(err, strategy.ErrEmptyRecipients):
			code = "empty_recipients"
			status = http.StatusBadRequest
		}
		h.writeAuditDenied(r, id, messageID, trace, code)
		writeError(w, status, code)
		return
	}

	if err := h.writeAudit(r, audit.Event{
		TraceID:          trace,
		MessageID:        messageID,
		Stage:            audit.StageValidated,
		Endpoint:         r.URL.Path,
		AppID:            id.AppID,
		Capability:       string(auth.CapMessagesDirect),
		CapabilitySetVer: id.CapabilitySetVer,
		RouteStrategy:    h.Strategy.Name(),
		Details: map[string]any{
			"recipients_requested": len(req.Recipients),
			"recipients_resolved":  len(resolved.Recipients),
			"recipients_skipped":   len(resolved.Skipped),
		},
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "audit_unavailable")
		return
	}

	resp := directResponse{
		MessageID: messageID,
	}

	for _, rh := range resolved.Recipients {
		_ = h.writeAudit(r, audit.Event{
			TraceID:          trace,
			MessageID:        messageID,
			Stage:            audit.StageDispatched,
			Endpoint:         r.URL.Path,
			AppID:            id.AppID,
			Capability:       string(auth.CapMessagesDirect),
			CapabilitySetVer: id.CapabilitySetVer,
			RouteStrategy:    h.Strategy.Name(),
			DeliveryChannel:  audit.ChannelSupergroup,
			RecipientUserID:  rh.UserID,
			RecipientChatID:  rh.ChatID,
		})
		result, sendErr := h.Dispatcher.Send(r.Context(), rh, req.Envelope)
		if sendErr != nil {
			code := classifyDispatchErr(sendErr)
			_ = h.writeAudit(r, audit.Event{
				TraceID:          trace,
				MessageID:        messageID,
				Stage:            audit.StageDeferred,
				Endpoint:         r.URL.Path,
				AppID:            id.AppID,
				Capability:       string(auth.CapMessagesDirect),
				CapabilitySetVer: id.CapabilitySetVer,
				RouteStrategy:    h.Strategy.Name(),
				DeliveryChannel:  audit.ChannelSupergroup,
				RecipientUserID:  rh.UserID,
				RecipientChatID:  rh.ChatID,
				ErrorCode:        code,
			})
			resp.Failed++
			resp.Recipients = append(resp.Recipients, recipientReport{
				UserID: rh.UserID, Status: "failed", Reason: code,
			})
			continue
		}
		_ = h.writeAudit(r, audit.Event{
			TraceID:          trace,
			MessageID:        messageID,
			Stage:            audit.StageDelivered,
			Endpoint:         r.URL.Path,
			AppID:            id.AppID,
			Capability:       string(auth.CapMessagesDirect),
			CapabilitySetVer: id.CapabilitySetVer,
			RouteStrategy:    h.Strategy.Name(),
			DeliveryChannel:  audit.ChannelSupergroup,
			RecipientUserID:  rh.UserID,
			RecipientChatID:  rh.ChatID,
		})
		resp.Delivered++
		resp.Recipients = append(resp.Recipients, recipientReport{
			UserID: rh.UserID, Status: "delivered", MessageID: result.TelegramMessageID,
		})
	}
	for _, sk := range resolved.Skipped {
		_ = h.writeAudit(r, audit.Event{
			TraceID:          trace,
			MessageID:        messageID,
			Stage:            audit.StageDenied,
			Endpoint:         r.URL.Path,
			AppID:            id.AppID,
			Capability:       string(auth.CapMessagesDirect),
			CapabilitySetVer: id.CapabilitySetVer,
			RouteStrategy:    h.Strategy.Name(),
			RecipientUserID:  sk.UserID,
			ErrorCode:        sk.Code,
		})
		resp.Skipped++
		resp.Recipients = append(resp.Recipients, recipientReport{
			UserID: sk.UserID, Status: "skipped", Reason: sk.Code,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func classifyDispatchErr(err error) string {
	switch {
	case errors.Is(err, dispatch.ErrChatNotFound):
		return "chat_not_found"
	case errors.Is(err, dispatch.ErrBotNotAdmin):
		return "bot_not_admin"
	case errors.Is(err, dispatch.ErrRateLimited):
		return "telegram_rate_limited"
	case errors.Is(err, dispatch.ErrTelegramAuth):
		return "telegram_auth_failed"
	default:
		return "telegram_transient"
	}
}

func (h *DirectHandler) writeAudit(r *http.Request, e audit.Event) error {
	if h.Audit == nil {
		return nil
	}
	err := h.Audit.Write(r.Context(), e)
	if err != nil {
		middleware.Log("error", "audit_write_failed", map[string]any{
			"trace_id": middleware.TraceID(r.Context()),
			"stage":    string(e.Stage),
			"error":    err.Error(),
		})
	}
	return err
}

func (h *DirectHandler) writeAuditDenied(r *http.Request, requester auth.RequesterIdentity, messageID, trace, code string) {
	_ = h.writeAudit(r, audit.Event{
		TraceID:          trace,
		MessageID:        messageID,
		Stage:            audit.StageDenied,
		Endpoint:         r.URL.Path,
		AppID:            requester.AppID,
		Capability:       string(auth.CapMessagesDirect),
		CapabilitySetVer: requester.CapabilitySetVer,
		RouteStrategy:    h.Strategy.Name(),
		ErrorCode:        code,
	})
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": code})
}
