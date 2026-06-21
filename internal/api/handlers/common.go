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

type dispatchOpts struct {
	RequireAppID      bool
	RequireRecipients bool
	AllowMinGrade     bool
	// DefaultChannel is stamped onto audit events for recipients that the
	// resolver skipped (denied stage) so the 4-stage invariant
	// (received → validated → … → denied) carries a consistent
	// delivery_channel value per strategy. For accepted recipients the
	// channel comes from RecipientHandle.Channel.
	DefaultChannel audit.DeliveryChannel
}

type dispatchRequest struct {
	Recipients []int64           `json:"recipients"`
	AppID      string            `json:"app_id"`
	MinGrade   string            `json:"min_grade"`
	Envelope   strategy.Envelope `json:"envelope"`
}

type dispatchReport struct {
	UserID    int64  `json:"user_id"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	MessageID int64  `json:"telegram_message_id,omitempty"`
}

type dispatchResponse struct {
	MessageID  string           `json:"message_id"`
	Delivered  int              `json:"delivered"`
	Skipped    int              `json:"skipped"`
	Failed     int              `json:"failed"`
	Recipients []dispatchReport `json:"recipients"`
}

func runStrategyDispatch(
	w http.ResponseWriter,
	r *http.Request,
	strat strategy.RouteStrategy,
	disp dispatch.Dispatcher,
	auditW audit.Writer,
	cap auth.Capability,
	opts dispatchOpts,
) {
	id, _ := auth.RequesterFrom(r.Context())
	trace := middleware.TraceID(r.Context())
	messageID := uuid.NewString()
	stratName := strat.Name()
	endpoint := r.URL.Path

	denyAndWriteAudit := func(code string, status int) {
		_ = writeAuditSafe(r, auditW, audit.Event{
			TraceID:          trace,
			MessageID:        messageID,
			Stage:            audit.StageDenied,
			Endpoint:         endpoint,
			AppID:            id.AppID,
			Capability:       string(cap),
			CapabilitySetVer: id.CapabilitySetVer,
			RouteStrategy:    stratName,
			ErrorCode:        code,
		})
		writeError(w, status, code)
	}

	if err := writeAuditSafe(r, auditW, audit.Event{
		TraceID:          trace,
		MessageID:        messageID,
		Stage:            audit.StageReceived,
		Endpoint:         endpoint,
		AppID:            id.AppID,
		Capability:       string(cap),
		CapabilitySetVer: id.CapabilitySetVer,
		RouteStrategy:    stratName,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "audit_unavailable")
		return
	}

	var req dispatchRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		denyAndWriteAudit("malformed_json", http.StatusBadRequest)
		return
	}

	if req.Envelope.SchemaVersion == 0 {
		denyAndWriteAudit("missing_envelope_version", http.StatusBadRequest)
		return
	}
	if req.Envelope.SchemaVersion != 1 {
		denyAndWriteAudit("unsupported_envelope_version", http.StatusBadRequest)
		return
	}
	if req.Envelope.Text == "" {
		denyAndWriteAudit("empty_envelope_text", http.StatusBadRequest)
		return
	}
	if opts.RequireAppID && req.AppID == "" {
		denyAndWriteAudit("missing_app_id", http.StatusBadRequest)
		return
	}
	if opts.RequireRecipients && len(req.Recipients) == 0 {
		denyAndWriteAudit("empty_recipients", http.StatusBadRequest)
		return
	}
	if !opts.AllowMinGrade {
		req.MinGrade = ""
	}

	resolved, err := strat.Resolve(r.Context(), strategy.Request{
		AppID:      req.AppID,
		Recipients: req.Recipients,
		MinGrade:   req.MinGrade,
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
		denyAndWriteAudit(code, status)
		return
	}

	if err := writeAuditSafe(r, auditW, audit.Event{
		TraceID:          trace,
		MessageID:        messageID,
		Stage:            audit.StageValidated,
		Endpoint:         endpoint,
		AppID:            id.AppID,
		Capability:       string(cap),
		CapabilitySetVer: id.CapabilitySetVer,
		RouteStrategy:    stratName,
		Details: map[string]any{
			"recipients_requested": len(req.Recipients),
			"recipients_resolved":  len(resolved.Recipients),
			"recipients_skipped":   len(resolved.Skipped),
		},
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "audit_unavailable")
		return
	}

	resp := dispatchResponse{MessageID: messageID}
	for _, rh := range resolved.Recipients {
		channel := audit.DeliveryChannel(rh.Channel)
		_ = writeAuditSafe(r, auditW, audit.Event{
			TraceID:          trace,
			MessageID:        messageID,
			Stage:            audit.StageDispatched,
			Endpoint:         endpoint,
			AppID:            id.AppID,
			Capability:       string(cap),
			CapabilitySetVer: id.CapabilitySetVer,
			RouteStrategy:    stratName,
			DeliveryChannel:  channel,
			RecipientUserID:  rh.UserID,
			RecipientChatID:  rh.ChatID,
		})
		result, sendErr := disp.Send(r.Context(), rh, req.Envelope)
		if sendErr != nil {
			code := classifyDispatchErr(sendErr)
			_ = writeAuditSafe(r, auditW, audit.Event{
				TraceID:          trace,
				MessageID:        messageID,
				Stage:            audit.StageDeferred,
				Endpoint:         endpoint,
				AppID:            id.AppID,
				Capability:       string(cap),
				CapabilitySetVer: id.CapabilitySetVer,
				RouteStrategy:    stratName,
				DeliveryChannel:  channel,
				RecipientUserID:  rh.UserID,
				RecipientChatID:  rh.ChatID,
				ErrorCode:        code,
			})
			resp.Failed++
			resp.Recipients = append(resp.Recipients, dispatchReport{
				UserID: rh.UserID, Status: "failed", Reason: code,
			})
			continue
		}
		_ = writeAuditSafe(r, auditW, audit.Event{
			TraceID:          trace,
			MessageID:        messageID,
			Stage:            audit.StageDelivered,
			Endpoint:         endpoint,
			AppID:            id.AppID,
			Capability:       string(cap),
			CapabilitySetVer: id.CapabilitySetVer,
			RouteStrategy:    stratName,
			DeliveryChannel:  channel,
			RecipientUserID:  rh.UserID,
			RecipientChatID:  rh.ChatID,
		})
		resp.Delivered++
		resp.Recipients = append(resp.Recipients, dispatchReport{
			UserID: rh.UserID, Status: "delivered", MessageID: result.TelegramMessageID,
		})
	}
	for _, sk := range resolved.Skipped {
		_ = writeAuditSafe(r, auditW, audit.Event{
			TraceID:          trace,
			MessageID:        messageID,
			Stage:            audit.StageDenied,
			Endpoint:         endpoint,
			AppID:            id.AppID,
			Capability:       string(cap),
			CapabilitySetVer: id.CapabilitySetVer,
			RouteStrategy:    stratName,
			DeliveryChannel:  opts.DefaultChannel,
			RecipientUserID:  sk.UserID,
			ErrorCode:        sk.Code,
		})
		resp.Skipped++
		resp.Recipients = append(resp.Recipients, dispatchReport{
			UserID: sk.UserID, Status: "skipped", Reason: sk.Code,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func writeAuditSafe(r *http.Request, w audit.Writer, e audit.Event) error {
	if w == nil {
		return nil
	}
	err := w.Write(r.Context(), e)
	if err != nil {
		middleware.Log("error", "audit_write_failed", map[string]any{
			"trace_id": middleware.TraceID(r.Context()),
			"stage":    string(e.Stage),
			"error":    err.Error(),
		})
	}
	return err
}
