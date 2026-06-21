package handlers

import (
	"net/http"

	"github.com/CatPope/telegram_server/internal/audit"
	"github.com/CatPope/telegram_server/internal/auth"
	"github.com/CatPope/telegram_server/internal/dispatch"
	"github.com/CatPope/telegram_server/internal/dispatch/strategy"
)

type BroadcastHandler struct {
	Strategy   strategy.RouteStrategy
	Dispatcher dispatch.Dispatcher
	Audit      audit.Writer
}

func (h *BroadcastHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	runStrategyDispatch(w, r, h.Strategy, h.Dispatcher, h.Audit,
		auth.CapMessagesBroadcast,
		dispatchOpts{
			AllowMinGrade:  true,
			DefaultChannel: audit.ChannelGeneral,
		})
}
