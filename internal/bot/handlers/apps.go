package handlers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mymmrac/telego"

	"github.com/CatPope/telegram_server/internal/bot"
	"github.com/CatPope/telegram_server/internal/dispatch/strategy"
	"github.com/CatPope/telegram_server/internal/registry"
)

// AppsHandler implements the DM-side app catalogue: /apps shows what the
// user can subscribe to (filtered by users.grade vs apps.min_grade), and
// /subscribe <app_id> / /unsubscribe <app_id> mutate user_subscriptions
// then drive the topic provisioner inside the user's personal supergroup.
type AppsHandler struct {
	Bot         *telego.Bot
	Pool        *pgxpool.Pool
	Users       *registry.UserStore
	Provisioner *bot.TopicProvisioner
}

func (h *AppsHandler) Name() string { return "apps" }

func (h *AppsHandler) Handle(ctx context.Context, u telego.Update) (bool, error) {
	if u.Message == nil || u.Message.From == nil {
		return false, nil
	}
	if u.Message.Chat.Type != "private" {
		return false, nil
	}
	text := strings.TrimSpace(u.Message.Text)
	switch {
	case text == "/apps":
		return true, h.list(ctx, u)
	case strings.HasPrefix(text, "/subscribe "):
		return true, h.toggle(ctx, u, strings.TrimSpace(strings.TrimPrefix(text, "/subscribe ")), true)
	case strings.HasPrefix(text, "/unsubscribe "):
		return true, h.toggle(ctx, u, strings.TrimSpace(strings.TrimPrefix(text, "/unsubscribe ")), false)
	default:
		return false, nil
	}
}

type appRow struct {
	ID         string
	Name       string
	MinGrade   string
	Subscribed bool
}

func (h *AppsHandler) list(ctx context.Context, u telego.Update) error {
	user, err := h.Users.GetByTelegramID(ctx, u.Message.From.ID)
	if err != nil {
		return h.replyPlain(ctx, u.Message.Chat.ID, "먼저 DM에서 /start 와 /agree 를 진행해주세요.")
	}
	const q = `
		SELECT a.id, a.name, a.min_grade, s.user_id IS NOT NULL AS subscribed
		FROM apps a
		LEFT JOIN user_subscriptions s ON s.app_id = a.id AND s.user_id = $1
		WHERE a.active = true
		ORDER BY a.id`
	rows, qErr := h.Pool.Query(ctx, q, user.ID)
	if qErr != nil {
		return fmt.Errorf("apps list: %w", qErr)
	}
	defer rows.Close()
	var entries []appRow
	for rows.Next() {
		var e appRow
		if scanErr := rows.Scan(&e.ID, &e.Name, &e.MinGrade, &e.Subscribed); scanErr != nil {
			return fmt.Errorf("apps scan: %w", scanErr)
		}
		entries = append(entries, e)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("apps rows: %w", rowsErr)
	}

	// Split by visibility: visible (user.grade >= app.min_grade) vs locked.
	userRank := strategy.GradeRankExported(user.Grade)
	var visible, locked []appRow
	for _, e := range entries {
		if strategy.GradeRankExported(e.MinGrade) <= userRank {
			visible = append(visible, e)
		} else {
			locked = append(locked, e)
		}
	}
	sort.Slice(visible, func(i, j int) bool { return visible[i].ID < visible[j].ID })
	sort.Slice(locked, func(i, j int) bool { return locked[i].ID < locked[j].ID })

	var sb strings.Builder
	sb.WriteString("가입 가능한 앱 목록:\n")
	for _, e := range visible {
		marker := "□"
		hint := "/subscribe " + e.ID
		if e.Subscribed {
			marker = "■"
			hint = "/unsubscribe " + e.ID
		}
		fmt.Fprintf(&sb, "%s %s (%s)\n   %s\n", marker, e.ID, e.Name, hint)
	}
	if len(locked) > 0 {
		sb.WriteString("\n등급이 부족하여 가입 불가:\n")
		for _, e := range locked {
			fmt.Fprintf(&sb, "  - %s (%s, requires %s)\n", e.ID, e.Name, e.MinGrade)
		}
	}
	return h.replyPlain(ctx, u.Message.Chat.ID, sb.String())
}

func (h *AppsHandler) toggle(ctx context.Context, u telego.Update, appID string, subscribe bool) error {
	if appID == "" {
		return h.replyPlain(ctx, u.Message.Chat.ID, "사용: /subscribe <app_id> 또는 /unsubscribe <app_id>")
	}
	user, err := h.Users.GetByTelegramID(ctx, u.Message.From.ID)
	if err != nil {
		return h.replyPlain(ctx, u.Message.Chat.ID, "먼저 /start 후 /agree 를 진행해주세요.")
	}
	// Verify app existence + visibility for this user.
	var appMinGrade string
	var active bool
	err = h.Pool.QueryRow(ctx,
		`SELECT min_grade, active FROM apps WHERE id = $1`, appID,
	).Scan(&appMinGrade, &active)
	if err != nil {
		return h.replyPlain(ctx, u.Message.Chat.ID, fmt.Sprintf("앱 %q 을 찾을 수 없습니다.", appID))
	}
	if !active {
		return h.replyPlain(ctx, u.Message.Chat.ID, fmt.Sprintf("앱 %q 은 비활성 상태입니다.", appID))
	}
	if strategy.GradeRankExported(user.Grade) < strategy.GradeRankExported(appMinGrade) {
		return h.replyPlain(ctx, u.Message.Chat.ID, "이 앱을 구독할 수 있는 등급이 아닙니다.")
	}

	if subscribe {
		if _, exErr := h.Pool.Exec(ctx,
			`INSERT INTO user_subscriptions (user_id, app_id) VALUES ($1, $2)
			 ON CONFLICT (user_id, app_id) DO NOTHING`,
			user.ID, appID,
		); exErr != nil {
			return fmt.Errorf("subscribe: %w", exErr)
		}
		if user.PersonalSupergroupID != nil && user.BotIsAdmin && h.Provisioner != nil {
			_, _ = h.Provisioner.EnsureForSubscribedApps(ctx, user.ID, *user.PersonalSupergroupID)
		}
		return h.replyPlain(ctx, u.Message.Chat.ID,
			fmt.Sprintf("앱 %q 구독을 추가했습니다. 개인 그룹에 토픽이 생성되었습니다.", appID))
	}

	// unsubscribe path
	if user.PersonalSupergroupID != nil && user.BotIsAdmin && h.Provisioner != nil {
		_ = h.Provisioner.Close(ctx, user.ID, *user.PersonalSupergroupID, appID)
	}
	if _, exErr := h.Pool.Exec(ctx,
		`DELETE FROM user_subscriptions WHERE user_id = $1 AND app_id = $2`,
		user.ID, appID,
	); exErr != nil {
		return fmt.Errorf("unsubscribe: %w", exErr)
	}
	return h.replyPlain(ctx, u.Message.Chat.ID,
		fmt.Sprintf("앱 %q 구독을 해지했습니다. 토픽은 보관(archived) 처리되었습니다.", appID))
}

func (h *AppsHandler) replyPlain(ctx context.Context, chatID int64, text string) error {
	_, err := h.Bot.SendMessage(ctx, &telego.SendMessageParams{
		ChatID: telego.ChatID{ID: chatID},
		Text:   text,
	})
	return err
}
