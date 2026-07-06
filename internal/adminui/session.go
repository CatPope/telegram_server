package adminui

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookieName = "adminui_session"
	sessionTTL        = 12 * time.Hour
)

// SessionManager signs and verifies the operator's login cookie with an
// HMAC key generated fresh at process startup — no session store needed,
// restarting the binary simply invalidates every outstanding cookie.
type SessionManager struct {
	secret []byte
	secure bool
}

func NewSessionManager(secureCookies bool) (*SessionManager, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("adminui: generate session secret: %w", err)
	}
	return &SessionManager{secret: secret, secure: secureCookies}, nil
}

// Issue signs and sets a fresh session cookie, returning the session nonce
// so callers can immediately derive a CSRF token for the response page.
func (sm *SessionManager) Issue(w http.ResponseWriter) (string, error) {
	nonce, err := randomHex(16)
	if err != nil {
		return "", err
	}
	expiry := time.Now().Add(sessionTTL).Unix()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sm.sign(expiry, nonce),
		Path:     "/",
		HttpOnly: true,
		Secure:   sm.secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Unix(expiry, 0),
	})
	return nonce, nil
}

func (sm *SessionManager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   sm.secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// Middleware requires a valid session cookie, redirecting to /login
// otherwise, and stashes the session nonce in the request context so
// downstream handlers can derive the page's CSRF token.
func (sm *SessionManager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		nonce, ok := sm.verify(cookie.Value)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), sessionNonceCtxKey{}, nonce)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type sessionNonceCtxKey struct{}

func SessionNonce(ctx context.Context) string {
	v, _ := ctx.Value(sessionNonceCtxKey{}).(string)
	return v
}

func (sm *SessionManager) sign(expiry int64, nonce string) string {
	payload := strconv.FormatInt(expiry, 10) + "|" + nonce
	mac := hmac.New(sha256.New, sm.secret)
	mac.Write([]byte(payload))
	return payload + "|" + hex.EncodeToString(mac.Sum(nil))
}

func (sm *SessionManager) verify(value string) (nonce string, ok bool) {
	parts := strings.SplitN(value, "|", 3)
	if len(parts) != 3 {
		return "", false
	}
	expiryStr, nonce, sig := parts[0], parts[1], parts[2]
	expiry, err := strconv.ParseInt(expiryStr, 10, 64)
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, sm.secret)
	mac.Write([]byte(expiryStr + "|" + nonce))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return "", false
	}
	if time.Now().Unix() > expiry {
		return "", false
	}
	return nonce, true
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("adminui: generate nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// loginLimiter is a simple in-memory per-IP token bucket guarding the
// login handler — a single operator UI doesn't need anything sturdier.
type loginLimiter struct {
	mu      sync.Mutex
	buckets map[string]*loginBucket
}

type loginBucket struct {
	tokens   float64
	lastFill time.Time
}

const (
	loginRateLimit  = 5.0
	loginRateWindow = time.Minute
)

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{buckets: make(map[string]*loginBucket)}
}

func (l *loginLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[key]
	if !ok {
		b = &loginBucket{tokens: loginRateLimit, lastFill: now}
		l.buckets[key] = b
	} else {
		elapsed := now.Sub(b.lastFill)
		refill := elapsed.Seconds() * (loginRateLimit / loginRateWindow.Seconds())
		b.tokens = min(loginRateLimit, b.tokens+refill)
		b.lastFill = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
