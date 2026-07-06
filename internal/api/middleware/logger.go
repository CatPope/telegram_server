package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

var secretKeyPattern = regexp.MustCompile(`(?i)(authorization|api[_-]?key|token|secret|password|ssh[_-]?key|bearer)`)

func RedactKey(k string) bool { return secretKeyPattern.MatchString(k) }

type logWriter struct {
	mu  sync.Mutex
	out *json.Encoder
}

func newDefaultLogWriter() *logWriter {
	return &logWriter{out: json.NewEncoder(os.Stdout)}
}

var defaultLogger = newDefaultLogWriter()

// SetLogOutput redirects structured log output to w and returns a restore
// function. Intended for tests that assert on what does — or must not —
// get logged (e.g. adminui's plaintext-key-never-logged check).
func SetLogOutput(w io.Writer) (restore func()) {
	defaultLogger.mu.Lock()
	prev := defaultLogger.out
	defaultLogger.out = json.NewEncoder(w)
	defaultLogger.mu.Unlock()
	return func() {
		defaultLogger.mu.Lock()
		defaultLogger.out = prev
		defaultLogger.mu.Unlock()
	}
}

func (lw *logWriter) emit(fields map[string]any) {
	for k := range fields {
		if RedactKey(k) {
			fields[k] = "[REDACTED]"
		}
	}
	lw.mu.Lock()
	defer lw.mu.Unlock()
	_ = lw.out.Encode(fields)
}

func Log(level, event string, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	fields["level"] = level
	fields["event"] = event
	defaultLogger.emit(fields)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		Log("info", "http_access", map[string]any{
			"trace_id":    TraceID(r.Context()),
			"method":      r.Method,
			"path":        sanitizePath(r.URL.Path),
			"status":      rec.status,
			"bytes":       rec.bytes,
			"duration_ms": time.Since(start).Milliseconds(),
		})
	})
}

func sanitizePath(p string) string {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		return p[:i]
	}
	return p
}
