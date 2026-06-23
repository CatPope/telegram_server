// Package bot owns the long-polling lifecycle and update dispatch. Handlers
// are registered as Route values; the poller routes each Update to the first
// matching Route. Context cancellation propagates into telego's
// UpdatesViaLongPolling so SIGTERM drains within the shutdown window
// (Pre-mortem #4 mitigation).
package bot

import (
	"context"
	"sync"

	"github.com/mymmrac/telego"

	"github.com/CatPope/telegram_server/internal/api/middleware"
)

type Update = telego.Update

// Handler returns true if it consumed the update. Returning false lets the
// poller try the next registered handler in order.
type Handler interface {
	Name() string
	Handle(ctx context.Context, u Update) (handled bool, err error)
}

type Poller struct {
	bot      *telego.Bot
	handlers []Handler
	mu       sync.Mutex
	running  bool
}

func NewPoller(bot *telego.Bot, handlers ...Handler) *Poller {
	return &Poller{bot: bot, handlers: handlers}
}

// Run starts the long-poll loop until ctx is cancelled. Returns after the
// telego channel is drained.
func (p *Poller) Run(ctx context.Context) error {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return nil
	}
	p.running = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
	}()

	params := &telego.GetUpdatesParams{
		Timeout: 30,
		AllowedUpdates: []string{
			"message", "edited_message", "callback_query",
			"my_chat_member", "chat_member",
		},
	}
	updates, err := p.bot.UpdatesViaLongPolling(ctx, params)
	if err != nil {
		return err
	}
	middleware.Log("info", "bot_poller_started", map[string]any{
		"handlers": len(p.handlers),
	})
	for update := range updates {
		p.dispatch(ctx, update)
	}
	middleware.Log("info", "bot_poller_stopped", nil)
	return nil
}

func (p *Poller) dispatch(ctx context.Context, u Update) {
	for _, h := range p.handlers {
		handled, err := h.Handle(ctx, u)
		if err != nil {
			middleware.Log("error", "bot_handler_error", map[string]any{
				"handler": h.Name(),
				"error":   err.Error(),
				"update":  updateSummary(u),
			})
			return
		}
		if handled {
			return
		}
	}
}

func updateSummary(u Update) map[string]any {
	out := map[string]any{}
	switch {
	case u.Message != nil:
		out["kind"] = "message"
		out["chat_id"] = u.Message.Chat.ID
		out["from_id"] = 0
		if u.Message.From != nil {
			out["from_id"] = u.Message.From.ID
		}
	case u.MyChatMember != nil:
		out["kind"] = "my_chat_member"
		out["chat_id"] = u.MyChatMember.Chat.ID
	case u.ChatMember != nil:
		out["kind"] = "chat_member"
		out["chat_id"] = u.ChatMember.Chat.ID
	case u.CallbackQuery != nil:
		out["kind"] = "callback_query"
	default:
		out["kind"] = "other"
	}
	return out
}
