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
// Logout additionally revokes the session server-side (revoked set), so a
// captured cookie stops working the moment the operator logs out.
type SessionManager struct {
	secret []byte
	secure bool

	mu      sync.Mutex
	revoked map[string]time.Time // logged-out nonce → sweep-after time
}

func NewSessionManager(secureCookies bool) (*SessionManager, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("adminui: generate session secret: %w", err)
	}
	return &SessionManager{
		secret:  secret,
		secure:  secureCookies,
		revoked: make(map[string]time.Time),
	}, nil
}

// Revoke invalidates a session server-side. The cookie's HMAC stays valid
// until its expiry, so verify() also consults this set. Entries older than
// the maximum possible cookie lifetime are swept lazily on each call —
// a single-operator UI never accumulates more than a handful.
func (sm *SessionManager) Revoke(nonce string) {
	if nonce == "" {
		return
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	now := time.Now()
	for n, sweepAfter := range sm.revoked {
		if now.After(sweepAfter) {
			delete(sm.revoked, n)
		}
	}
	sm.revoked[nonce] = now.Add(sessionTTL)
}

func (sm *SessionManager) isRevoked(nonce string) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	_, ok := sm.revoked[nonce]
	return ok
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
		nonce, expiry, ok := sm.verify(cookie.Value)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), sessionNonceCtxKey{}, nonce)
		ctx = context.WithValue(ctx, sessionExpiryCtxKey{}, expiry)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type sessionNonceCtxKey struct{}
type sessionExpiryCtxKey struct{}

func SessionNonce(ctx context.Context) string {
	v, _ := ctx.Value(sessionNonceCtxKey{}).(string)
	return v
}

// SessionExpiry returns the session cookie's expiry so pages can render a
// static "time remaining" hint (no JS ticking under the CSP).
func SessionExpiry(ctx context.Context) time.Time {
	v, _ := ctx.Value(sessionExpiryCtxKey{}).(time.Time)
	return v
}

func (sm *SessionManager) sign(expiry int64, nonce string) string {
	payload := strconv.FormatInt(expiry, 10) + "|" + nonce
	mac := hmac.New(sha256.New, sm.secret)
	mac.Write([]byte(payload))
	return payload + "|" + hex.EncodeToString(mac.Sum(nil))
}

func (sm *SessionManager) verify(value string) (nonce string, expiry time.Time, ok bool) {
	parts := strings.SplitN(value, "|", 3)
	if len(parts) != 3 {
		return "", time.Time{}, false
	}
	expiryStr, nonce, sig := parts[0], parts[1], parts[2]
	expiryUnix, err := strconv.ParseInt(expiryStr, 10, 64)
	if err != nil {
		return "", time.Time{}, false
	}
	mac := hmac.New(sha256.New, sm.secret)
	mac.Write([]byte(expiryStr + "|" + nonce))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return "", time.Time{}, false
	}
	if time.Now().Unix() > expiryUnix {
		return "", time.Time{}, false
	}
	if sm.isRevoked(nonce) {
		return "", time.Time{}, false
	}
	return nonce, time.Unix(expiryUnix, 0), true
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
	// Lazy sweep (same pattern as the session revoked map): a bucket that
	// has refilled to full carries no state, so drop it rather than letting
	// one entry per client IP accumulate forever.
	for k, b := range l.buckets {
		if k == key {
			continue
		}
		refill := now.Sub(b.lastFill).Seconds() * (loginRateLimit / loginRateWindow.Seconds())
		if b.tokens+refill >= loginRateLimit {
			delete(l.buckets, k)
		}
	}
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

// globalBackoff is a second, IP-agnostic brake on login brute force:
// after globalBackoffThreshold consecutive failures (across all IPs),
// every further attempt is rejected until globalBackoffDelay has passed
// since the most recent failure. A successful login resets it, and the
// failure streak decays after globalBackoffDecay of quiet — an operator's
// occasional typo weeks apart must never accumulate into a lockout. This
// bounds distributed guessing the per-IP bucket can't see.
type globalBackoff struct {
	mu           sync.Mutex
	failures     int
	lastFailure  time.Time
	blockedUntil time.Time
	now          func() time.Time // injectable for tests
}

const (
	globalBackoffThreshold = 20
	globalBackoffDelay     = 30 * time.Second
	globalBackoffDecay     = 10 * time.Minute
)

func newGlobalBackoff() *globalBackoff {
	return &globalBackoff{now: time.Now}
}

func (g *globalBackoff) Allow() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.now().Before(g.blockedUntil)
}

func (g *globalBackoff) RecordFailure() {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if !g.lastFailure.IsZero() && now.Sub(g.lastFailure) >= globalBackoffDecay {
		g.failures = 0
	}
	g.lastFailure = now
	g.failures++
	if g.failures >= globalBackoffThreshold {
		g.blockedUntil = now.Add(globalBackoffDelay)
	}
}

func (g *globalBackoff) RecordSuccess() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failures = 0
	g.blockedUntil = time.Time{}
}
