package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/mymmrac/telego"

	"github.com/CatPope/telegram_server/internal/audit"
	"github.com/CatPope/telegram_server/internal/registry"
)

const (
	startgroupTokenTTL = 30 * time.Minute
	startgroupTokenLen = 16 // bytes -> 32 hex chars
)

// StartHandler implements the /start and /agree commands plus the
// post-agree onboarding step (issuing the startgroup deeplink).
type StartHandler struct {
	Bot         *telego.Bot
	BotUsername string
	Users       *registry.UserStore
	Supergroups *registry.SupergroupStore
	Audit       audit.Writer
	TokenTTL    time.Duration
}

func (h *StartHandler) Name() string { return "start" }

func (h *StartHandler) ttl() time.Duration {
	if h.TokenTTL > 0 {
		return h.TokenTTL
	}
	return startgroupTokenTTL
}

func (h *StartHandler) Handle(ctx context.Context, u telego.Update) (bool, error) {
	if u.Message == nil || u.Message.From == nil {
		return false, nil
	}
	text := strings.TrimSpace(u.Message.Text)
	switch {
	case text == "/start" || strings.HasPrefix(text, "/start "):
		return true, h.handleStart(ctx, u)
	case text == "/agree":
		return true, h.handleAgree(ctx, u)
	default:
		return false, nil
	}
}

func (h *StartHandler) handleStart(ctx context.Context, u telego.Update) error {
	from := u.Message.From
	user, err := h.Users.UpsertOnStart(ctx, from.ID, displayName(from), from.LanguageCode)
	if err != nil {
		return fmt.Errorf("start: upsert: %w", err)
	}

	chatID := telego.ChatID{ID: u.Message.Chat.ID}
	if user.PersonalSupergroupID != nil && user.BotIsAdmin {
		_, err := h.Bot.SendMessage(ctx, &telego.SendMessageParams{
			ChatID: chatID,
			Text:   "이미 등록되셨습니다. 개인 그룹이 연결되어 있습니다.",
		})
		return err
	}
	if user.AgreedAt == nil {
		_, err := h.Bot.SendMessage(ctx, &telego.SendMessageParams{
			ChatID: chatID,
			Text: "안녕하세요. 알림 봇입니다.\n" +
				"본 봇은 알림 전달을 위해 Telegram 사용자 ID를 보관합니다.\n" +
				"동의하시면 /agree 를 입력해주세요.",
		})
		return err
	}

	// Already agreed → issue (or reuse) a startgroup deeplink.
	return h.issueStartgroupDeeplink(ctx, user.ID, chatID)
}

func (h *StartHandler) handleAgree(ctx context.Context, u telego.Update) error {
	from := u.Message.From
	user, err := h.Users.GetByTelegramID(ctx, from.ID)
	if err == registry.ErrUserNotFound {
		// /agree before /start: bootstrap the row.
		user, err = h.Users.UpsertOnStart(ctx, from.ID, displayName(from), from.LanguageCode)
	}
	if err != nil {
		return fmt.Errorf("agree: get/upsert: %w", err)
	}
	transitioned, err := h.Users.MarkAgreed(ctx, user.ID)
	if err != nil {
		return err
	}
	if transitioned && h.Audit != nil {
		_ = h.Audit.Write(ctx, audit.Event{
			Stage:    audit.StageValidated,
			Endpoint: "/agree",
			Details:  map[string]any{"telegram_id": from.ID, "user_id": user.ID},
		})
	}
	chatID := telego.ChatID{ID: u.Message.Chat.ID}
	return h.issueStartgroupDeeplink(ctx, user.ID, chatID)
}

func (h *StartHandler) issueStartgroupDeeplink(ctx context.Context, userID int64, chatID telego.ChatID) error {
	if h.BotUsername == "" {
		_, err := h.Bot.SendMessage(ctx, &telego.SendMessageParams{
			ChatID: chatID,
			Text:   "봇 username이 설정되지 않았습니다. 운영자에게 문의해주세요.",
		})
		return err
	}
	token, err := newToken()
	if err != nil {
		return fmt.Errorf("token: %w", err)
	}
	if err := h.Supergroups.IssueToken(ctx, token, userID, h.ttl()); err != nil {
		return err
	}
	url := fmt.Sprintf("https://t.me/%s?startgroup=%s", h.BotUsername, token)
	_, err = h.Bot.SendMessage(ctx, &telego.SendMessageParams{
		ChatID: chatID,
		Text: fmt.Sprintf(
			"개인 알림용 슈퍼그룹을 만들어주세요.\n%s\n"+
				"버튼이 안 보이면 위 링크를 눌러 새 그룹을 만들고 봇을 admin으로 추가해주세요.\n"+
				"권한: Post Messages / Manage Topics / Ban Users 모두 필요합니다.",
			url),
	})
	return err
}

func newToken() (string, error) {
	buf := make([]byte, startgroupTokenLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func displayName(u *telego.User) string {
	if u.Username != "" {
		return u.Username
	}
	return strings.TrimSpace(u.FirstName + " " + u.LastName)
}
