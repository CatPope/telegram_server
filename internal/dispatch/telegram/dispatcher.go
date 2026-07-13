package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mymmrac/telego"

	"github.com/CatPope/telegram_server/internal/dispatch"
	"github.com/CatPope/telegram_server/internal/dispatch/strategy"
	"github.com/CatPope/telegram_server/internal/ratelimit"
)

type Dispatcher struct {
	bot     *telego.Bot
	limiter ratelimit.RateLimiter
}

func New(bot *telego.Bot, limiter ratelimit.RateLimiter) *Dispatcher {
	return &Dispatcher{bot: bot, limiter: limiter}
}

func (d *Dispatcher) Send(ctx context.Context, h strategy.RecipientHandle, env strategy.Envelope) (dispatch.DeliveryResult, error) {
	if d.limiter != nil {
		key := fmt.Sprintf("chat:%d", h.ChatID)
		decision, err := d.limiter.Allow(ctx, key)
		if err == nil && !decision.Allowed {
			if decision.RetryAfter > 0 {
				timer := time.NewTimer(decision.RetryAfter)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return dispatch.DeliveryResult{}, ctx.Err()
				case <-timer.C:
				}
			} else {
				return dispatch.DeliveryResult{}, dispatch.ErrRateLimited
			}
		}
	}
	params := &telego.SendMessageParams{
		ChatID: telego.ChatID{ID: h.ChatID},
		Text:   env.Text,
	}
	if h.TopicID > 0 {
		params.MessageThreadID = int(h.TopicID)
	}
	msg, err := d.bot.SendMessage(ctx, params)
	if err != nil {
		return dispatch.DeliveryResult{}, classify(err)
	}
	if msg == nil {
		return dispatch.DeliveryResult{}, dispatch.ErrTransient
	}
	return dispatch.DeliveryResult{
		TelegramMessageID: int64(msg.MessageID),
		DeliveredAt:       time.Now(),
	}, nil
}

func classify(err error) error {
	if err == nil {
		return nil
	}
	low := strings.ToLower(err.Error())
	switch {
	case strings.Contains(low, "chat not found"):
		return fmt.Errorf("%w: %v", dispatch.ErrChatNotFound, err)
	case strings.Contains(low, "bot is not a member"),
		strings.Contains(low, "not enough rights"),
		strings.Contains(low, "have no rights"):
		return fmt.Errorf("%w: %v", dispatch.ErrBotNotAdmin, err)
	case strings.Contains(low, "too many requests"),
		strings.Contains(low, "retry_after"):
		return fmt.Errorf("%w: %v", dispatch.ErrRateLimited, err)
	case strings.Contains(low, "unauthorized"):
		return fmt.Errorf("%w: %v", dispatch.ErrTelegramAuth, err)
	}
	return fmt.Errorf("%w: %v", dispatch.ErrTransient, err)
}
