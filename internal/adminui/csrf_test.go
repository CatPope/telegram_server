package adminui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestCSRFVerifyPreSessionCookie(t *testing.T) {
	sm := newTestSessionManager(t)
	rec := httptest.NewRecorder()
	token, err := sm.IssueCSRFCookie(rec)
	if err != nil {
		t.Fatalf("IssueCSRFCookie: %v", err)
	}

	form := url.Values{"csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}

	if !sm.VerifyCSRF(req) {
		t.Error("expected CSRF verification to succeed with matching pre-session cookie")
	}
}

func TestCSRFVerifyRejectsMismatchedToken(t *testing.T) {
	sm := newTestSessionManager(t)
	rec := httptest.NewRecorder()
	if _, err := sm.IssueCSRFCookie(rec); err != nil {
		t.Fatalf("IssueCSRFCookie: %v", err)
	}

	form := url.Values{"csrf_token": {"not-the-right-token"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	_ = req.ParseForm()

	if sm.VerifyCSRF(req) {
		t.Error("expected CSRF verification to fail for mismatched token")
	}
}

func TestCSRFVerifyRejectsMissingCookie(t *testing.T) {
	sm := newTestSessionManager(t)
	token := sm.CSRFToken("some-nonce")

	form := url.Values{"csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = req.ParseForm()

	if sm.VerifyCSRF(req) {
		t.Error("expected CSRF verification to fail without any nonce cookie")
	}
}

func TestRequireCSRFMiddleware(t *testing.T) {
	sm := newTestSessionManager(t)
	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})
	mw := RequireCSRF(sm)(next)

	// Get a valid pre-session token first.
	setupRec := httptest.NewRecorder()
	token, err := sm.IssueCSRFCookie(setupRec)
	if err != nil {
		t.Fatalf("IssueCSRFCookie: %v", err)
	}
	cookies := setupRec.Result().Cookies()

	t.Run("valid token passes", func(t *testing.T) {
		handlerCalled = false
		form := url.Values{"csrf_token": {token}}
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !handlerCalled {
			t.Errorf("expected 200 and handler invoked, got %d handlerCalled=%v", rec.Code, handlerCalled)
		}
	})

	t.Run("missing token is rejected", func(t *testing.T) {
		handlerCalled = false
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(""))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden || handlerCalled {
			t.Errorf("expected 403 and handler skipped, got %d handlerCalled=%v", rec.Code, handlerCalled)
		}
	})
}
