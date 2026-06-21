package middleware

import (
	"fmt"
	"net/http"

	"github.com/CatPope/telegram_server/internal/auth"
	"github.com/CatPope/telegram_server/internal/ratelimit"
)

func RateLimit(rl ratelimit.RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rl == nil {
				next.ServeHTTP(w, r)
				return
			}
			id, ok := auth.RequesterFrom(r.Context())
			key := r.RemoteAddr
			if ok {
				key = "app:" + id.AppID
			}
			d, err := rl.Allow(r.Context(), key)
			if err != nil || !d.Allowed {
				if d.RetryAfter > 0 {
					w.Header().Set("Retry-After", fmt.Sprintf("%.0f", d.RetryAfter.Seconds()))
				}
				http.Error(w, `{"error":"rate_limited"}`, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
