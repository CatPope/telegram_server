package adminui

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
)

const (
	csrfCookieName   = "adminui_csrf"
	csrfCookieMaxAge = 600 // covers the login page's read-then-submit window
)

// CSRFToken derives a per-nonce token via HMAC so no server-side token
// store is needed — verification just recomputes the HMAC from the nonce
// already available (session cookie once logged in, pre-session cookie
// on the login page) and compares.
func (sm *SessionManager) CSRFToken(nonce string) string {
	mac := hmac.New(sha256.New, sm.secret)
	mac.Write([]byte("csrf|" + nonce))
	return hex.EncodeToString(mac.Sum(nil))
}

// IssueCSRFCookie sets a short-lived nonce cookie for unauthenticated
// pages (login) and returns the CSRF token to embed in the form.
func (sm *SessionManager) IssueCSRFCookie(w http.ResponseWriter) (string, error) {
	nonce, err := randomHex(16)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    nonce,
		Path:     "/",
		HttpOnly: true,
		Secure:   sm.secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   csrfCookieMaxAge,
	})
	return sm.CSRFToken(nonce), nil
}

// csrfNonce resolves the nonce a request's submitted CSRF token should be
// checked against: the signed session nonce once logged in, otherwise the
// pre-session cookie set when the login page was rendered.
func csrfNonce(r *http.Request) string {
	if nonce := SessionNonce(r.Context()); nonce != "" {
		return nonce
	}
	if c, err := r.Cookie(csrfCookieName); err == nil {
		return c.Value
	}
	return ""
}

func (sm *SessionManager) VerifyCSRF(r *http.Request) bool {
	nonce := csrfNonce(r)
	token := r.FormValue("csrf_token")
	if nonce == "" || token == "" {
		return false
	}
	return hmac.Equal([]byte(token), []byte(sm.CSRFToken(nonce)))
}

// RequireCSRF rejects any POST whose csrf_token form field doesn't match
// the caller's session (or pre-session, for the login form) nonce.
func RequireCSRF(sm *SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err != nil {
				http.Error(w, `{"error":"bad_form"}`, http.StatusBadRequest)
				return
			}
			if !sm.VerifyCSRF(r) {
				http.Error(w, `{"error":"csrf_invalid"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
