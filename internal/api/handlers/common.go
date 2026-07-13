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

// --- shared HTTP helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": code})
}

// decodeStrict decodes the request body into v, rejecting unknown fields.
func decodeStrict(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
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

// --- auditFlow ---

// auditFlow bundles the per-request fields every audit event of a handler
// shares (trace, message id, endpoint, requester identity, capability) so
// each stage write is a one-liner instead of a repeated struct literal.
type auditFlow struct {
	w     http.ResponseWriter
	r     *http.Request
	audit audit.Writer
	base  audit.Event
}

func newAuditFlow(w http.ResponseWriter, r *http.Request, auditW audit.Writer, cap auth.Capability) *auditFlow {
	id, _ := auth.RequesterFrom(r.Context())
	return &auditFlow{
		w:     w,
		r:     r,
		audit: auditW,
		base: audit.Event{
			TraceID:          middleware.TraceID(r.Context()),
			MessageID:        uuid.NewString(),
			Endpoint:         r.URL.Path,
			AppID:            id.AppID,
			Capability:       string(cap),
			CapabilitySetVer: id.CapabilitySetVer,
		},
	}
}

// event returns a copy of the base event stamped with the given stage.
func (f *auditFlow) event(stage audit.Stage) audit.Event {
	e := f.base
	e.Stage = stage
	return e
}

func (f *auditFlow) received() {
	_ = writeAuditSafe(f.r, f.audit, f.event(audit.StageReceived))
}

// deny writes a denied audit event and the matching JSON error response.
func (f *auditFlow) deny(code string, status int) {
	e := f.event(audit.StageDenied)
	e.ErrorCode = code
	_ = writeAuditSafe(f.r, f.audit, e)
	writeError(f.w, status, code)
}

// succeed writes the validated (with details) and delivered stages.
func (f *auditFlow) succeed(details map[string]any) {
	e := f.event(audit.StageValidated)
	e.Details = details
	_ = writeAuditSafe(f.r, f.audit, e)
	_ = writeAuditSafe(f.r, f.audit, f.event(audit.StageDelivered))
}

// --- strategy dispatch pipeline ---

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
	flow := newAuditFlow(w, r, auditW, cap)
	flow.base.RouteStrategy = strat.Name()

	if err := writeAuditSafe(r, auditW, flow.event(audit.StageReceived)); err != nil {
		writeError(w, http.StatusInternalServerError, "audit_unavailable")
		return
	}

	var req dispatchRequest
	if err := decodeStrict(r, &req); err != nil {
		flow.deny("malformed_json", http.StatusBadRequest)
		return
	}

	if req.Envelope.SchemaVersion == 0 {
		flow.deny("missing_envelope_version", http.StatusBadRequest)
		return
	}
	if req.Envelope.SchemaVersion != 1 {
		flow.deny("unsupported_envelope_version", http.StatusBadRequest)
		return
	}
	if req.Envelope.Text == "" {
		flow.deny("empty_envelope_text", http.StatusBadRequest)
		return
	}
	if opts.RequireAppID && req.AppID == "" {
		flow.deny("missing_app_id", http.StatusBadRequest)
		return
	}
	if opts.RequireRecipients && len(req.Recipients) == 0 {
		flow.deny("empty_recipients", http.StatusBadRequest)
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
		flow.deny(code, status)
		return
	}

	validated := flow.event(audit.StageValidated)
	validated.Details = map[string]any{
		"recipients_requested": len(req.Recipients),
		"recipients_resolved":  len(resolved.Recipients),
		"recipients_skipped":   len(resolved.Skipped),
	}
	if err := writeAuditSafe(r, auditW, validated); err != nil {
		writeError(w, http.StatusInternalServerError, "audit_unavailable")
		return
	}

	recipientEvent := func(stage audit.Stage, channel audit.DeliveryChannel, userID, chatID int64, code string) audit.Event {
		e := flow.event(stage)
		e.DeliveryChannel = channel
		e.RecipientUserID = userID
		e.RecipientChatID = chatID
		e.ErrorCode = code
		return e
	}

	resp := dispatchResponse{MessageID: flow.base.MessageID}
	for _, rh := range resolved.Recipients {
		channel := audit.DeliveryChannel(rh.Channel)
		_ = writeAuditSafe(r, auditW, recipientEvent(audit.StageDispatched, channel, rh.UserID, rh.ChatID, ""))
		result, sendErr := disp.Send(r.Context(), rh, req.Envelope)
		if sendErr != nil {
			code := classifyDispatchErr(sendErr)
			_ = writeAuditSafe(r, auditW, recipientEvent(audit.StageDeferred, channel, rh.UserID, rh.ChatID, code))
			resp.Failed++
			resp.Recipients = append(resp.Recipients, dispatchReport{
				UserID: rh.UserID, Status: "failed", Reason: code,
			})
			continue
		}
		_ = writeAuditSafe(r, auditW, recipientEvent(audit.StageDelivered, channel, rh.UserID, rh.ChatID, ""))
		resp.Delivered++
		resp.Recipients = append(resp.Recipients, dispatchReport{
			UserID: rh.UserID, Status: "delivered", MessageID: result.TelegramMessageID,
		})
	}
	for _, sk := range resolved.Skipped {
		_ = writeAuditSafe(r, auditW, recipientEvent(audit.StageDenied, opts.DefaultChannel, sk.UserID, 0, sk.Code))
		resp.Skipped++
		resp.Recipients = append(resp.Recipients, dispatchReport{
			UserID: sk.UserID, Status: "skipped", Reason: sk.Code,
		})
	}

	writeJSON(w, http.StatusOK, resp)
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
