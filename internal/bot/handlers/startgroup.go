package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mymmrac/telego"

	"github.com/CatPope/telegram_server/internal/registry"
)

// StartgroupHandler consumes the `/start <token>` message that Telegram
// auto-sends when a user adds the bot to a fresh supergroup via the
// startgroup deeplink. It binds the supergroup chat_id to the user that
// requested the token and replies with the next-step instructions.
type StartgroupHandler struct {
	Bot         *telego.Bot
	Supergroups *registry.SupergroupStore
}

func (h *StartgroupHandler) Name() string { return "startgroup" }

func (h *StartgroupHandler) Handle(ctx context.Context, u telego.Update) (bool, error) {
	if u.Message == nil {
		return false, nil
	}
	chatType := u.Message.Chat.Type
	if chatType != "group" && chatType != "supergroup" {
		return false, nil
	}
	text := strings.TrimSpace(u.Message.Text)
	if !strings.HasPrefix(text, "/start ") {
		return false, nil
	}
	token := strings.TrimSpace(strings.TrimPrefix(text, "/start "))
	if token == "" {
		return false, nil
	}

	chatID := u.Message.Chat.ID
	userID, err := h.Supergroups.ConsumeToken(ctx, token)
	if err != nil {
		switch {
		case errors.Is(err, registry.ErrTokenNotFound), errors.Is(err, registry.ErrTokenExpired):
			_, sErr := h.Bot.SendMessage(ctx, &telego.SendMessageParams{
				ChatID: telego.ChatID{ID: chatID},
				Text: "잘못되었거나 만료된 초대 토큰입니다.\n" +
					"개인 DM에서 /start 를 다시 입력해 새 링크를 받아주세요.",
			})
			return true, sErr
		default:
			return true, fmt.Errorf("startgroup: consume token: %w", err)
		}
	}

	if linkErr := h.Supergroups.LinkSupergroup(ctx, userID, chatID); linkErr != nil {
		return true, fmt.Errorf("startgroup: link: %w", linkErr)
	}

	_, sErr := h.Bot.SendMessage(ctx, &telego.SendMessageParams{
		ChatID: telego.ChatID{ID: chatID},
		Text: "개인 그룹이 연결되었습니다.\n" +
			"다음 단계:\n" +
			"1. 그룹 설정에서 Topics 를 켜주세요.\n" +
			"2. 봇에게 Post Messages / Manage Topics / Ban Users 권한을 부여해주세요.\n" +
			"권한이 적용되면 가입된 앱별 토픽이 자동 생성됩니다.",
	})
	return true, sErr
}
