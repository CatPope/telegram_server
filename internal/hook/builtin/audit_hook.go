package builtin

import (
	"context"
	"fmt"

	"github.com/CatPope/telegram_server/internal/audit"
	"github.com/CatPope/telegram_server/internal/hook"
)

type AuditHook struct {
	Writer     audit.Writer
	auditStage audit.Stage
	hookStage  hook.Stage
}

func NewPostDispatchAuditHook(w audit.Writer) *AuditHook {
	return &AuditHook{
		Writer:     w,
		auditStage: audit.StageDispatched,
		hookStage:  hook.StagePost,
	}
}

func (h *AuditHook) Name() string      { return fmt.Sprintf("audit_%s", h.auditStage) }
func (h *AuditHook) Stage() hook.Stage { return h.hookStage }

func (h *AuditHook) Run(ctx context.Context, req *hook.Request) (hook.Result, error) {
	if h.Writer == nil {
		return hook.Result{Continue: true}, nil
	}
	channel, _ := req.Payload["delivery_channel"].(string)
	recipientUserID, _ := req.Payload["recipient_user_id"].(int64)
	recipientChatID, _ := req.Payload["recipient_chat_id"].(int64)
	if err := h.Writer.Write(ctx, audit.Event{
		TraceID:         req.TraceID,
		MessageID:       req.MessageID,
		Stage:           h.auditStage,
		Endpoint:        req.Endpoint,
		AppID:           req.AppID,
		Capability:      req.Capability,
		RouteStrategy:   req.RouteStrategy,
		DeliveryChannel: audit.DeliveryChannel(channel),
		RecipientUserID: recipientUserID,
		RecipientChatID: recipientChatID,
	}); err != nil {
		return hook.Result{Continue: true}, fmt.Errorf("audit hook write: %w", err)
	}
	return hook.Result{Continue: true}, nil
}
