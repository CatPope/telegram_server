package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/CatPope/telegram_server/internal/audit"
	"github.com/CatPope/telegram_server/internal/auth"
)

type Resolver interface {
	Resolve(ctx context.Context, bearer string) (auth.RequesterIdentity, error)
}

func Auth(resolver Resolver, auditW audit.Writer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hdr := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(hdr, prefix) {
				denyAndAudit(w, r, auditW, "missing_bearer", http.StatusUnauthorized)
				return
			}
			bearer := strings.TrimSpace(hdr[len(prefix):])
			if bearer == "" {
				denyAndAudit(w, r, auditW, "empty_bearer", http.StatusUnauthorized)
				return
			}
			id, err := resolver.Resolve(r.Context(), bearer)
			if err != nil {
				code := "invalid_bearer"
				status := http.StatusUnauthorized
				switch {
				case errors.Is(err, auth.ErrBearerMalformed):
					code = "malformed_bearer"
				case errors.Is(err, auth.ErrKeyNotFound):
					code = "unknown_bearer"
				}
				denyAndAudit(w, r, auditW, code, status)
				return
			}
			ctx := auth.WithRequester(r.Context(), id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func denyAndAudit(w http.ResponseWriter, r *http.Request, aw audit.Writer, code string, status int) {
	if aw != nil {
		_ = aw.Write(r.Context(), audit.Event{
			TraceID:   TraceID(r.Context()),
			Stage:     audit.StageDenied,
			Endpoint:  r.URL.Path,
			ErrorCode: code,
		})
	}
	Log("info", "auth_denied", map[string]any{
		"trace_id": TraceID(r.Context()),
		"reason":   code,
		"path":     r.URL.Path,
	})
	http.Error(w, `{"error":"`+code+`"}`, status)
}

func RequireCapability(c auth.Capability, auditW audit.Writer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := auth.Require(r.Context(), c); err != nil {
				code := "forbidden"
				status := http.StatusForbidden
				if errors.Is(err, auth.ErrUnauthorized) {
					code = "unauthenticated"
					status = http.StatusUnauthorized
				}
				id, _ := auth.RequesterFrom(r.Context())
				if auditW != nil {
					_ = auditW.Write(r.Context(), audit.Event{
						TraceID:    TraceID(r.Context()),
						Stage:      audit.StageDenied,
						Endpoint:   r.URL.Path,
						AppID:      id.AppID,
						Capability: string(c),
						ErrorCode:  code,
					})
				}
				http.Error(w, `{"error":"`+code+`"}`, status)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
