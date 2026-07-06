package adminui

import (
	"net/http/httptest"
	"testing"
	"time"
)

func newTestSessionManager(t *testing.T) *SessionManager {
	t.Helper()
	sm, err := NewSessionManager(false)
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}
	return sm
}

func TestSessionSignVerifyRoundTrip(t *testing.T) {
	sm := newTestSessionManager(t)
	expiry := time.Now().Add(time.Hour).Unix()
	value := sm.sign(expiry, "abc123")

	nonce, ok := sm.verify(value)
	if !ok {
		t.Fatal("expected verify to succeed")
	}
	if nonce != "abc123" {
		t.Errorf("nonce = %q, want %q", nonce, "abc123")
	}
}

func TestSessionVerifyRejectsExpired(t *testing.T) {
	sm := newTestSessionManager(t)
	expired := time.Now().Add(-time.Minute).Unix()
	value := sm.sign(expired, "abc123")

	if _, ok := sm.verify(value); ok {
		t.Error("expected verify to fail for expired cookie")
	}
}

func TestSessionVerifyRejectsTamperedSignature(t *testing.T) {
	sm := newTestSessionManager(t)
	value := sm.sign(time.Now().Add(time.Hour).Unix(), "abc123")
	tampered := value[:len(value)-1] + "0"

	if _, ok := sm.verify(tampered); ok {
		t.Error("expected verify to fail for tampered signature")
	}
}

func TestSessionVerifyRejectsDifferentSecret(t *testing.T) {
	sm1 := newTestSessionManager(t)
	sm2 := newTestSessionManager(t)
	value := sm1.sign(time.Now().Add(time.Hour).Unix(), "abc123")

	if _, ok := sm2.verify(value); ok {
		t.Error("expected verify to fail across different session secrets")
	}
}

func TestSessionIssueAndMiddleware(t *testing.T) {
	sm := newTestSessionManager(t)
	rec := httptest.NewRecorder()
	nonce, err := sm.Issue(rec)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if nonce == "" {
		t.Fatal("expected non-empty nonce")
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName {
		t.Fatalf("expected a %s cookie, got %+v", sessionCookieName, cookies)
	}

	verifiedNonce, ok := sm.verify(cookies[0].Value)
	if !ok || verifiedNonce != nonce {
		t.Fatalf("issued cookie did not verify back to the same nonce: ok=%v nonce=%q want=%q", ok, verifiedNonce, nonce)
	}
}

func TestLoginLimiterAllowsThenBlocks(t *testing.T) {
	l := newLoginLimiter()
	for i := 0; i < int(loginRateLimit); i++ {
		if !l.Allow("1.2.3.4") {
			t.Fatalf("expected attempt %d to be allowed", i+1)
		}
	}
	if l.Allow("1.2.3.4") {
		t.Error("expected attempt beyond the limit to be blocked")
	}
}

func TestLoginLimiterTracksKeysIndependently(t *testing.T) {
	l := newLoginLimiter()
	for i := 0; i < int(loginRateLimit); i++ {
		l.Allow("1.2.3.4")
	}
	if !l.Allow("5.6.7.8") {
		t.Error("expected a different key to have its own budget")
	}
}
