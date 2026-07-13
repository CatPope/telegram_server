package handlers

import (
	"net/http"

	"github.com/CatPope/telegram_server/internal/audit"
	"github.com/CatPope/telegram_server/internal/auth"
	"github.com/CatPope/telegram_server/internal/dispatch"
	"github.com/CatPope/telegram_server/internal/dispatch/strategy"
)

type DirectHandler struct {
	Strategy   strategy.RouteStrategy
	Dispatcher dispatch.Dispatcher
	Audit      audit.Writer
}

func (h *DirectHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	runStrategyDispatch(w, r, h.Strategy, h.Dispatcher, h.Audit,
		auth.CapMessagesDirect,
		dispatchOpts{
			RequireAppID:      true,
			RequireRecipients: true,
			DefaultChannel:    audit.ChannelSupergroup,
		})
}
