package handlers

import (
	"context"
	"fmt"

	"github.com/mymmrac/telego"

	"github.com/CatPope/telegram_server/internal/api/middleware"
	"github.com/CatPope/telegram_server/internal/audit"
	"github.com/CatPope/telegram_server/internal/bot"
	"github.com/CatPope/telegram_server/internal/registry"
)

// PromoteHandler watches my_chat_member updates for the bot's own status
// changes inside personal supergroups. When the user grants the required
// admin permissions it flips users.bot_is_admin_in_supergroup=true and
// drives the topic provisioner over every subscribed app. When the bot is
// demoted, removed, or the supergroup is deleted it flips the same flag
// back to false so dispatch fails fast with bot_not_admin.
type PromoteHandler struct {
	Bot         *telego.Bot
	Supergroups *registry.SupergroupStore
	Provisioner *bot.TopicProvisioner
	Audit       audit.Writer
}

func (h *PromoteHandler) Name() string { return "promote" }

func (h *PromoteHandler) Handle(ctx context.Context, u telego.Update) (bool, error) {
	if u.MyChatMember == nil {
		return false, nil
	}
	chat := u.MyChatMember.Chat
	if chat.Type != "supergroup" && chat.Type != "group" {
		return true, nil
	}
	userID, err := h.Supergroups.FindUserByChatID(ctx, chat.ID)
	if err != nil {
		// Unknown chat — bot was added somewhere we did not track. Ignore.
		return true, nil
	}
	newStatus, hasAllAdminRights := classifyMyChatMember(u.MyChatMember.NewChatMember)
	switch newStatus {
	case "administrator":
		if !hasAllAdminRights {
			h.warnMissingRights(ctx, chat.ID)
			return true, h.markNotAdmin(ctx, userID, chat.ID, "intrusion_unmitigated")
		}
		if err := h.Supergroups.SetBotIsAdmin(ctx, userID, true); err != nil {
			return true, err
		}
		if h.Provisioner != nil {
			created, provErr := h.Provisioner.EnsureForSubscribedApps(ctx, userID, chat.ID)
			if provErr != nil {
				middleware.Log("error", "provisioner_ensure_failed", map[string]any{
					"user_id": userID,
					"chat_id": chat.ID,
					"error":   provErr.Error(),
				})
			}
			h.sendReady(ctx, chat.ID, created)
		}
		return true, nil
	case "member", "restricted", "left", "kicked":
		return true, h.markNotAdmin(ctx, userID, chat.ID, "bot_not_admin")
	default:
		return true, nil
	}
}

func (h *PromoteHandler) markNotAdmin(ctx context.Context, userID, chatID int64, errorCode string) error {
	if err := h.Supergroups.SetBotIsAdmin(ctx, userID, false); err != nil {
		return err
	}
	if h.Audit != nil {
		_ = h.Audit.Write(ctx, audit.Event{
			Stage:           audit.StageBotNotAdmin,
			Endpoint:        "my_chat_member",
			RouteStrategy:   "bot",
			DeliveryChannel: audit.ChannelSupergroup,
			RecipientUserID: userID,
			RecipientChatID: chatID,
			ErrorCode:       errorCode,
		})
	}
	return nil
}

func (h *PromoteHandler) warnMissingRights(ctx context.Context, chatID int64) {
	_, _ = h.Bot.SendMessage(ctx, &telego.SendMessageParams{
		ChatID: telego.ChatID{ID: chatID},
		Text: "봇 권한 중 일부가 부족합니다.\n" +
			"Post Messages / Manage Topics / Ban Users 모두 켜져야 토픽을 생성하고 침입을 막을 수 있습니다.",
	})
}

func (h *PromoteHandler) sendReady(ctx context.Context, chatID int64, created []string) {
	body := "준비 완료. 알림이 활성화되었습니다."
	if len(created) > 0 {
		body = fmt.Sprintf("준비 완료. 생성된 토픽 %d개: %v", len(created), created)
	}
	_, _ = h.Bot.SendMessage(ctx, &telego.SendMessageParams{
		ChatID: telego.ChatID{ID: chatID},
		Text:   body,
	})
}

// classifyMyChatMember normalises telego's ChatMember union into a status
// string and a single bool for "has all three required admin rights"
// (Post Messages / Manage Topics / Ban Users / CanRestrictMembers).
func classifyMyChatMember(member telego.ChatMember) (status string, hasAllRights bool) {
	if member == nil {
		return "", false
	}
	status = member.MemberStatus()
	switch m := member.(type) {
	case *telego.ChatMemberAdministrator:
		hasAllRights = (m.CanPostMessages || m.CanPostStories) &&
			m.CanManageTopics &&
			m.CanRestrictMembers
	default:
		hasAllRights = false
	}
	return status, hasAllRights
}
