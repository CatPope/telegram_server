package handlers

import (
	"context"

	"github.com/mymmrac/telego"

	"github.com/CatPope/telegram_server/internal/audit"
	"github.com/CatPope/telegram_server/internal/registry"
)

// IntrusionHandler enforces the one-user-per-supergroup rule by watching
// chat_member updates inside personal supergroups. Any new member that is
// neither the bot itself nor the owner gets banChatMember-ed, and the
// outcome is written to audit_log as intrusion_kick (success) or
// intrusion_unmitigated (ban failed - usually missing Ban Users right).
type IntrusionHandler struct {
	Bot         *telego.Bot
	Supergroups *registry.SupergroupStore
	Audit       audit.Writer
	BotID       int64 // self id from getMe; updates from this id are ignored
}

func (h *IntrusionHandler) Name() string { return "intrusion" }

func (h *IntrusionHandler) Handle(ctx context.Context, u telego.Update) (bool, error) {
	cm := u.ChatMember
	if cm == nil {
		return false, nil
	}
	chatType := cm.Chat.Type
	if chatType != "supergroup" && chatType != "group" {
		return true, nil
	}
	// Only act when we own this chat (a registered user's personal supergroup).
	userID, err := h.Supergroups.FindUserByChatID(ctx, cm.Chat.ID)
	if err != nil {
		return true, nil
	}
	newMember := cm.NewChatMember
	if newMember == nil {
		return true, nil
	}
	// Ignore the bot's own status changes (PromoteHandler owns those).
	intruderID := newMember.MemberUser().ID
	if h.BotID != 0 && intruderID == h.BotID {
		return true, nil
	}
	// Owner / admin / creator transitions are normal; only act on a
	// non-bot member joining a personal supergroup.
	status := newMember.MemberStatus()
	if status != "member" && status != "restricted" {
		return true, nil
	}

	banErr := h.Bot.BanChatMember(ctx, &telego.BanChatMemberParams{
		ChatID: telego.ChatID{ID: cm.Chat.ID},
		UserID: intruderID,
	})
	if banErr != nil {
		if h.Audit != nil {
			_ = h.Audit.Write(ctx, audit.Event{
				Stage:           audit.StageIntrusionUnmitigated,
				Endpoint:        "chat_member",
				RouteStrategy:   "bot",
				DeliveryChannel: audit.ChannelSupergroup,
				RecipientUserID: userID,
				RecipientChatID: cm.Chat.ID,
				ErrorCode:       "ban_failed",
				Details: map[string]any{
					"intruder_id": intruderID,
					"error":       banErr.Error(),
				},
			})
		}
		return true, banErr
	}
	if h.Audit != nil {
		_ = h.Audit.Write(ctx, audit.Event{
			Stage:           audit.StageIntrusionKick,
			Endpoint:        "chat_member",
			RouteStrategy:   "bot",
			DeliveryChannel: audit.ChannelSupergroup,
			RecipientUserID: userID,
			RecipientChatID: cm.Chat.ID,
			Details: map[string]any{
				"intruder_id": intruderID,
			},
		})
	}
	return true, nil
}
