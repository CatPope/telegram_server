package dispatch

import (
	"context"
	"errors"
	"time"

	"github.com/CatPope/telegram_server/internal/dispatch/strategy"
)

type DeliveryResult struct {
	TelegramMessageID int64
	DeliveredAt       time.Time
}

type Dispatcher interface {
	Send(ctx context.Context, h strategy.RecipientHandle, env strategy.Envelope) (DeliveryResult, error)
}

var (
	ErrChatNotFound = errors.New("dispatch: chat not found")
	ErrBotNotAdmin  = errors.New("dispatch: bot not chat admin")
	ErrRateLimited  = errors.New("dispatch: rate limited by Telegram")
	ErrTelegramAuth = errors.New("dispatch: telegram auth failed")
	ErrTransient    = errors.New("dispatch: transient failure")
)
