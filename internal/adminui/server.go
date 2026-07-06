package adminui

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/CatPope/telegram_server/internal/adminui/apiclient"
	"github.com/CatPope/telegram_server/internal/api/middleware"
)

type Server struct {
	cfg      Config
	sessions *SessionManager
	limiter  *loginLimiter
	client   *apiclient.Client
}

// NewServer wires the admin UI's chi router. It reuses telegram_server's
// own RequestID/Recover/AccessLog middleware so admin UI requests log in
// the same shape as the main API.
func NewServer(cfg Config) (http.Handler, error) {
	sm, err := NewSessionManager(cfg.CookieSecure)
	if err != nil {
		return nil, err
	}

	s := &Server{
		cfg:      cfg,
		sessions: sm,
		limiter:  newLoginLimiter(),
		client:   apiclient.New(cfg.TelegramServerURL, cfg.APIKey),
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
		r.With(RequireCSRF(sm)).Post("/logout", s.handleLogout)
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
