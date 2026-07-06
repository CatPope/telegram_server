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

func TestSessionRevokeInvalidatesVerify(t *testing.T) {
	sm := newTestSessionManager(t)
	rec := httptest.NewRecorder()
	nonce, err := sm.Issue(rec)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	cookie := rec.Result().Cookies()[0]

	if _, ok := sm.verify(cookie.Value); !ok {
		t.Fatal("expected cookie to verify before revocation")
	}
	sm.Revoke(nonce)
	if _, ok := sm.verify(cookie.Value); ok {
		t.Error("expected verify to fail after server-side revocation")
	}
}

func TestSessionRevokeSweepsExpiredEntries(t *testing.T) {
	sm := newTestSessionManager(t)
	sm.revoked["stale"] = time.Now().Add(-time.Minute)

	sm.Revoke("fresh")

	sm.mu.Lock()
	defer sm.mu.Unlock()
	if _, ok := sm.revoked["stale"]; ok {
		t.Error("expected the expired entry to be swept")
	}
	if _, ok := sm.revoked["fresh"]; !ok {
		t.Error("expected the fresh entry to be retained")
	}
}

func TestGlobalBackoffBlocksAfterThresholdAndResets(t *testing.T) {
	current := time.Unix(1000, 0)
	g := newGlobalBackoff()
	g.now = func() time.Time { return current }

	for i := 0; i < globalBackoffThreshold-1; i++ {
		g.RecordFailure()
	}
	if !g.Allow() {
		t.Fatal("expected attempts below the threshold to be allowed")
	}
	g.RecordFailure() // threshold reached
	if g.Allow() {
		t.Fatal("expected attempts to be blocked at the threshold")
	}

	current = current.Add(globalBackoffDelay + time.Second)
	if !g.Allow() {
		t.Fatal("expected the block to lapse after the delay")
	}

	// Any further failure while still over the threshold re-arms the block.
	g.RecordFailure()
	if g.Allow() {
		t.Fatal("expected another failure past the threshold to re-block")
	}

	g.RecordSuccess()
	if !g.Allow() {
		t.Error("expected success to reset the backoff")
	}
	g.RecordFailure()
	if !g.Allow() {
		t.Error("expected a single failure after reset to be allowed")
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
