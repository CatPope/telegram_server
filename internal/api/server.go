package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/CatPope/telegram_server/internal/api/handlers"
	"github.com/CatPope/telegram_server/internal/api/middleware"
	"github.com/CatPope/telegram_server/internal/audit"
	"github.com/CatPope/telegram_server/internal/auth"
	"github.com/CatPope/telegram_server/internal/dispatch"
	"github.com/CatPope/telegram_server/internal/dispatch/strategy"
	"github.com/CatPope/telegram_server/internal/ratelimit"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Deps struct {
	Pool       *pgxpool.Pool
	Audit      audit.Writer
	Resolver   middleware.Resolver
	ReqLimit   ratelimit.RateLimiter
	Direct     strategy.RouteStrategy
	Dispatcher dispatch.Dispatcher
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recover)
	r.Use(middleware.AccessLog)

	r.Get("/healthz", (&handlers.HealthHandler{Pool: d.Pool}).ServeHTTP)

	r.Route("/v1", func(r chi.Router) {
		r.Use(middleware.Auth(d.Resolver, d.Audit))
		r.Use(middleware.RateLimit(d.ReqLimit))
		r.With(middleware.RequireCapability(auth.CapMessagesDirect, d.Audit)).
			Post("/messages/direct", (&handlers.DirectHandler{
				Strategy:   d.Direct,
				Dispatcher: d.Dispatcher,
				Audit:      d.Audit,
			}).ServeHTTP)
	})

	return r
}
