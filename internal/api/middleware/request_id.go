package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

const HeaderTraceID = "X-Trace-Id"

type ctxKey int

const traceIDCtxKey ctxKey = 0

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get(HeaderTraceID)
		if traceID == "" {
			traceID = uuid.NewString()
		}
		ctx := context.WithValue(r.Context(), traceIDCtxKey, traceID)
		w.Header().Set(HeaderTraceID, traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func TraceID(ctx context.Context) string {
	if v, ok := ctx.Value(traceIDCtxKey).(string); ok {
		return v
	}
	return ""
}

func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDCtxKey, traceID)
}
