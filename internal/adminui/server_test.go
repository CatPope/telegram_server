package adminui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testConfig(t *testing.T, targetURL string) Config {
	t.Helper()
	return Config{
		ListenAddr:        "127.0.0.1:0",
		Password:          "correct-horse-battery-staple",
		APIKey:            "test-api-key",
		TelegramServerURL: targetURL,
	}
}

func TestDashboardRedirectsWithoutSession(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	handler, err := NewServer(testConfig(t, target.URL), nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect to /login, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

func TestLoginPageRendersCSRFCookieAndForm(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	handler, err := NewServer(testConfig(t, target.URL), nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `name="csrf_token"`) {
		t.Error("expected login form to include a csrf_token field")
	}
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == csrfCookieName {
			found = true
		}
	}
	if !found {
		t.Error("expected a csrf pre-session cookie to be set")
	}
}

func TestLoginFlowSuccessThenDashboardThenLogout(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// GET /login to obtain a CSRF cookie + token.
	loginPageReq := httptest.NewRequest(http.MethodGet, "/login", nil)
	loginPageRec := httptest.NewRecorder()
	handler.ServeHTTP(loginPageRec, loginPageReq)
	csrfCookies := loginPageRec.Result().Cookies()
	body := loginPageRec.Body.String()
	token := extractCSRFToken(t, body)

	// POST /login with the correct password.
	form := url.Values{"csrf_token": {token}, "password": {cfg.Password}}
	loginReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range csrfCookies {
		loginReq.AddCookie(c)
	}
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusSeeOther {
		t.Fatalf("expected login redirect, got %d: %s", loginRec.Code, loginRec.Body.String())
	}
	sessionCookies := loginRec.Result().Cookies()

	// GET / with the session cookie should render the dashboard with healthy status.
	dashReq := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range sessionCookies {
		dashReq.AddCookie(c)
	}
	dashRec := httptest.NewRecorder()
	handler.ServeHTTP(dashRec, dashReq)
	if dashRec.Code != http.StatusOK {
		t.Fatalf("expected dashboard 200, got %d", dashRec.Code)
	}
	if !strings.Contains(dashRec.Body.String(), "OK") {
		t.Error("expected dashboard to report healthy target server")
	}

	// POST /logout with the dashboard's CSRF token clears the session.
	logoutToken := extractCSRFToken(t, dashRec.Body.String())
	logoutForm := url.Values{"csrf_token": {logoutToken}}
	logoutReq := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(logoutForm.Encode()))
	logoutReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range sessionCookies {
		logoutReq.AddCookie(c)
	}
	logoutRec := httptest.NewRecorder()
	handler.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusSeeOther {
		t.Fatalf("expected logout redirect, got %d", logoutRec.Code)
	}

	// The pre-logout session cookie must be dead server-side, not just
	// cleared in the browser — replaying it should bounce to /login.
	replayReq := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range sessionCookies {
		replayReq.AddCookie(c)
	}
	replayRec := httptest.NewRecorder()
	handler.ServeHTTP(replayRec, replayReq)
	if replayRec.Code != http.StatusSeeOther || replayRec.Header().Get("Location") != "/login" {
		t.Errorf("expected replayed cookie to be rejected after logout, got %d %q",
			replayRec.Code, replayRec.Header().Get("Location"))
	}
}

func TestLoginFailureWrongPassword(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	loginPageReq := httptest.NewRequest(http.MethodGet, "/login", nil)
	loginPageRec := httptest.NewRecorder()
	handler.ServeHTTP(loginPageRec, loginPageReq)
	csrfCookies := loginPageRec.Result().Cookies()
	token := extractCSRFToken(t, loginPageRec.Body.String())

	form := url.Values{"csrf_token": {token}, "password": {"wrong-password"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range csrfCookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect back to login, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login?error=1" {
		t.Errorf("Location = %q, want /login?error=1", loc)
	}
}

func TestLoginRateLimited(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	loginPageReq := httptest.NewRequest(http.MethodGet, "/login", nil)
	loginPageRec := httptest.NewRecorder()
	handler.ServeHTTP(loginPageRec, loginPageReq)
	csrfCookies := loginPageRec.Result().Cookies()
	token := extractCSRFToken(t, loginPageRec.Body.String())

	attempt := func() int {
		form := url.Values{"csrf_token": {token}, "password": {"wrong-password"}}
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "9.9.9.9:1234"
		for _, c := range csrfCookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	for i := 0; i < int(loginRateLimit); i++ {
		if code := attempt(); code != http.StatusSeeOther {
			t.Fatalf("attempt %d: expected redirect, got %d", i+1, code)
		}
	}
	if code := attempt(); code != http.StatusTooManyRequests {
		t.Errorf("expected 429 once the rate limit is exceeded, got %d", code)
	}
}

func TestLoginGlobalBackoffAcrossIPs(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	loginPageReq := httptest.NewRequest(http.MethodGet, "/login", nil)
	loginPageRec := httptest.NewRecorder()
	handler.ServeHTTP(loginPageRec, loginPageReq)
	csrfCookies := loginPageRec.Result().Cookies()
	token := extractCSRFToken(t, loginPageRec.Body.String())

	attempt := func(ip string) int {
		form := url.Values{"csrf_token": {token}, "password": {"wrong-password"}}
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = ip + ":1234"
		for _, c := range csrfCookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	// Spread failures over distinct IPs so the per-IP bucket never trips —
	// only the global counter sees them all.
	for i := 0; i < globalBackoffThreshold; i++ {
		ip := fmt.Sprintf("10.0.%d.%d", i/250, i%250+1)
		if code := attempt(ip); code != http.StatusSeeOther {
			t.Fatalf("attempt %d: expected redirect, got %d", i+1, code)
		}
	}
	if code := attempt("172.16.0.1"); code != http.StatusTooManyRequests {
		t.Errorf("expected 429 from a fresh IP once the global threshold is hit, got %d", code)
	}
}

func extractCSRFToken(t *testing.T, html string) string {
	t.Helper()
	const marker = `name="csrf_token" value="`
	i := strings.Index(html, marker)
	if i < 0 {
		t.Fatalf("csrf_token field not found in body: %s", html)
	}
	rest := html[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("malformed csrf_token field in body: %s", html)
	}
	return rest[:j]
}
