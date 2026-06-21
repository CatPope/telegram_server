package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/CatPope/telegram_server/internal/api/handlers"
	"github.com/CatPope/telegram_server/internal/api/middleware"
	"github.com/CatPope/telegram_server/internal/audit"
	"github.com/CatPope/telegram_server/internal/auth"
	"github.com/CatPope/telegram_server/internal/dispatch"
	"github.com/CatPope/telegram_server/internal/dispatch/strategy"
	"github.com/CatPope/telegram_server/internal/ratelimit"
	"github.com/jackc/pgx/v5/pgxpool"
)

const HandlerTimeout = 30 * time.Second

type Deps struct {
	Pool       *pgxpool.Pool
	Audit      audit.Writer
	Resolver   middleware.Resolver
	ReqLimit   ratelimit.RateLimiter
	Direct     strategy.RouteStrategy
	Topic      strategy.RouteStrategy
	Broadcast  strategy.RouteStrategy
	DirectDM   strategy.RouteStrategy
	Dispatcher dispatch.Dispatcher
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recover)
	r.Use(middleware.AccessLog)

	r.Get("/healthz", (&handlers.HealthHandler{Pool: d.Pool}).ServeHTTP)

	r.Route("/admin", func(r chi.Router) {
		r.Use(chimw.Timeout(HandlerTimeout))
		r.Use(middleware.Auth(d.Resolver, d.Audit))
		r.Use(middleware.RateLimit(d.ReqLimit))

		r.With(middleware.RequireCapability(auth.CapAppsRegister, d.Audit)).
			Method(http.MethodPost, "/apps", http.HandlerFunc((&handlers.AdminAppsHandler{Pool: d.Pool, Audit: d.Audit}).Create))
		r.With(middleware.RequireCapability(auth.CapAppsRegister, d.Audit)).
			Method(http.MethodPatch, "/apps/{id}", http.HandlerFunc((&handlers.AdminAppsHandler{Pool: d.Pool, Audit: d.Audit}).Patch))
		r.With(middleware.RequireCapability(auth.CapAppsRegister, d.Audit)).
			Method(http.MethodDelete, "/apps/{id}", http.HandlerFunc((&handlers.AdminAppsHandler{Pool: d.Pool, Audit: d.Audit}).Delete))

		r.With(middleware.RequireCapability(auth.CapUsersPromote, d.Audit)).
			Method(http.MethodPatch, "/users/{telegram_id}", http.HandlerFunc((&handlers.AdminUsersHandler{Pool: d.Pool, Audit: d.Audit}).Patch))

		r.With(middleware.RequireCapability(auth.CapAppsRegister, d.Audit)).
			Method(http.MethodPost, "/users/{telegram_id}/subscriptions/{app_id}", http.HandlerFunc((&handlers.AdminSubscriptionsHandler{Pool: d.Pool, Audit: d.Audit}).Subscribe))
		r.With(middleware.RequireCapability(auth.CapAppsRegister, d.Audit)).
			Method(http.MethodDelete, "/users/{telegram_id}/subscriptions/{app_id}", http.HandlerFunc((&handlers.AdminSubscriptionsHandler{Pool: d.Pool, Audit: d.Audit}).Unsubscribe))

		r.With(middleware.RequireCapability(auth.CapAuditSearch, d.Audit)).
			Method(http.MethodGet, "/audit/search", http.HandlerFunc((&handlers.AdminAuditHandler{Pool: d.Pool, Audit: d.Audit}).Search))
	})

	r.Route("/v1", func(r chi.Router) {
		r.Use(chimw.Timeout(HandlerTimeout))
		r.Use(middleware.Auth(d.Resolver, d.Audit))
		r.Use(middleware.RateLimit(d.ReqLimit))

		r.With(middleware.RequireCapability(auth.CapMessagesDirect, d.Audit)).
			Post("/messages/direct", (&handlers.DirectHandler{
				Strategy:   d.Direct,
				Dispatcher: d.Dispatcher,
				Audit:      d.Audit,
			}).ServeHTTP)

		r.With(middleware.RequireCapability(auth.CapMessagesTopic, d.Audit)).
			Post("/messages/topic", (&handlers.TopicHandler{
				Strategy:   d.Topic,
				Dispatcher: d.Dispatcher,
				Audit:      d.Audit,
			}).ServeHTTP)

		r.With(middleware.RequireCapability(auth.CapMessagesBroadcast, d.Audit)).
			Post("/messages/broadcast", (&handlers.BroadcastHandler{
				Strategy:   d.Broadcast,
				Dispatcher: d.Dispatcher,
				Audit:      d.Audit,
			}).ServeHTTP)

		r.With(middleware.RequireCapability(auth.CapMessagesDirectDM, d.Audit)).
			Post("/messages/direct-dm", (&handlers.DirectDMHandler{
				Strategy:   d.DirectDM,
				Dispatcher: d.Dispatcher,
				Audit:      d.Audit,
			}).ServeHTTP)
	})

	return r
}
