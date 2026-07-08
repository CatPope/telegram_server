package adminui

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/CatPope/telegram_server/internal/adminui/apiclient"
	"github.com/CatPope/telegram_server/internal/api/middleware"
	"github.com/CatPope/telegram_server/internal/audit"
)

type Server struct {
	cfg      Config
	sessions *SessionManager
	limiter  *loginLimiter
	backoff  *globalBackoff
	client   *apiclient.Client
	store    Store
	keys     KeyStore
	audit    audit.Writer
}

// NewServer wires the admin UI's chi router. It reuses telegram_server's
// own RequestID/Recover/AccessLog middleware so admin UI requests log in
// the same shape as the main API. store, keys and auditW may be nil
// (DATABASE_URL unset) — the apps/keys pages degrade to a "DB not
// connected" notice rather than failing to start.
func NewServer(cfg Config, store Store, keys KeyStore, auditW audit.Writer) (http.Handler, error) {
	sm, err := NewSessionManager(cfg.CookieSecure)
	if err != nil {
		return nil, err
	}

	s := &Server{
		cfg:      cfg,
		sessions: sm,
		limiter:  newLoginLimiter(),
		backoff:  newGlobalBackoff(),
		client:   apiclient.New(cfg.TelegramServerURL, cfg.APIKey),
		store:    store,
		keys:     keys,
		audit:    auditW,
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recover)
	r.Use(middleware.AccessLog)
	r.Use(securityHeaders)

	r.Get("/login", s.handleLoginPage)
	r.With(RequireCSRF(sm)).Post("/login", s.handleLoginSubmit)

	r.Group(func(r chi.Router) {
		r.Use(sm.Middleware)
		r.Get("/", s.handleDashboard)
		r.Get("/delivery", s.handleDeliveryPage)
		r.With(RequireCSRF(sm)).Post("/logout", s.handleLogout)

		r.Get("/apps", s.handleAppsList)
		r.Get("/apps/new", s.handleAppNewForm)
		r.With(RequireCSRF(sm)).Post("/apps", s.handleAppCreate)
		r.Get("/apps/{id}", s.handleAppDetail)
		r.With(RequireCSRF(sm)).Post("/apps/{id}/patch", s.handleAppPatch)
		r.With(RequireCSRF(sm)).Post("/apps/{id}/deactivate", s.handleAppDeactivate)

		r.Get("/keys", s.handleKeysPage)
		r.With(RequireCSRF(sm)).Post("/keys", s.handleKeyIssue)
		r.With(RequireCSRF(sm)).Post("/keys/{app}/{prefix}/revoke", s.handleKeyRevoke)
		r.With(RequireCSRF(sm)).Post("/keys/{app}/{prefix}/label", s.handleKeyLabel)
		// Pre-redesign URL, kept alive for bookmarks/app-detail links.
		r.Get("/apps/{id}/keys", s.handleKeysLegacyRedirect)

		r.Get("/audit", s.handleAuditPage)
		r.With(RequireCSRF(sm)).Post("/audit/verify", s.handleAuditVerify)

		r.Get("/test-send", s.handleTestSendPage)
		r.With(RequireCSRF(sm)).Post("/test-send", s.handleTestSendSubmit)

		r.Get("/users", s.handleUsersPage)
		r.With(RequireCSRF(sm)).Post("/users/{id}/grade", s.handleUserGrade)
		r.With(RequireCSRF(sm)).Post("/users/{id}/subscriptions", s.handleUserSubscribe)
		r.With(RequireCSRF(sm)).Post("/users/{id}/subscriptions/{app}/delete", s.handleUserUnsubscribe)
	})

	return r, nil
}

// securityHeaders applies the admin UI's blanket response headers: the UI
// is self-contained (inline CSS only), must never be framed, and no page
// should be cached — operators may share machines.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
