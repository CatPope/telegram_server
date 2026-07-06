package adminui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestUserGradeSuccess(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/users/42" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"telegram_id":42,"grade":"developer"}`))
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	usersPageReq := httptest.NewRequest(http.MethodGet, "/users?telegram_id=42", nil)
	for _, c := range cookies {
		usersPageReq.AddCookie(c)
	}
	usersPageRec := httptest.NewRecorder()
	handler.ServeHTTP(usersPageRec, usersPageReq)
	token := extractCSRFToken(t, usersPageRec.Body.String())

	form := url.Values{"csrf_token": {token}, "grade": {"developer"}}
	req := httptest.NewRequest(http.MethodPost, "/users/42/grade", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "등급이 변경되었습니다") {
		t.Errorf("expected success message, got: %s", rec.Body.String())
	}
}

func TestUserGradeErrorMapsToFriendlyMessage(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"user_not_found"}`))
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	usersPageReq := httptest.NewRequest(http.MethodGet, "/users", nil)
	for _, c := range cookies {
		usersPageReq.AddCookie(c)
	}
	usersPageRec := httptest.NewRecorder()
	handler.ServeHTTP(usersPageRec, usersPageReq)
	token := extractCSRFToken(t, usersPageRec.Body.String())

	form := url.Values{"csrf_token": {token}, "grade": {"developer"}}
	req := httptest.NewRequest(http.MethodPost, "/users/999/grade", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (re-rendered with error), got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "사용자를 찾을 수 없습니다") {
		t.Errorf("expected friendly error message, got: %s", rec.Body.String())
	}
}

func TestUserGradeWithoutCSRFIsForbidden(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	form := url.Values{"grade": {"developer"}}
	req := httptest.NewRequest(http.MethodPost, "/users/42/grade", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without csrf_token, got %d", rec.Code)
	}
}

func TestUserGradeRejectsMalformedTelegramIDPath(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("target server should not be called for a malformed telegram_id")
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	usersPageReq := httptest.NewRequest(http.MethodGet, "/users", nil)
	for _, c := range cookies {
		usersPageReq.AddCookie(c)
	}
	usersPageRec := httptest.NewRecorder()
	handler.ServeHTTP(usersPageRec, usersPageReq)
	token := extractCSRFToken(t, usersPageRec.Body.String())

	form := url.Values{"csrf_token": {token}, "grade": {"developer"}}
	req := httptest.NewRequest(http.MethodPost, "/users/not-a-number/grade", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a malformed telegram_id, got %d", rec.Code)
	}
}

func TestUsersPageRendersUnsubscribeConfirmationAfterTwoStepLookup(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	req := httptest.NewRequest(http.MethodGet, "/users?telegram_id=7&app_id=ci-notifier", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `action="/users/7/subscriptions/ci-notifier/delete"`) {
		t.Errorf("expected the concrete unsubscribe form action, got: %s", body)
	}
}

func TestUsersPageRejectsMalformedAppIDInLookup(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	req := httptest.NewRequest(http.MethodGet, "/users?telegram_id=7&app_id=UPPER!!", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "앱 ID 형식이 올바르지 않습니다") {
		t.Errorf("expected an app_id format error, got: %s", body)
	}
	if strings.Contains(body, "/subscriptions/UPPER!!/delete") {
		t.Error("malformed app_id must not be baked into a form action")
	}
}

func TestUserSubscribeAndUnsubscribe(t *testing.T) {
	var lastMethod string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastMethod = r.Method
		if r.URL.Path != "/admin/users/7/subscriptions/ci-notifier" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"telegram_id":7,"app_id":"ci-notifier","subscribed":true}`))
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	usersPageReq := httptest.NewRequest(http.MethodGet, "/users?telegram_id=7", nil)
	for _, c := range cookies {
		usersPageReq.AddCookie(c)
	}
	usersPageRec := httptest.NewRecorder()
	handler.ServeHTTP(usersPageRec, usersPageReq)
	token := extractCSRFToken(t, usersPageRec.Body.String())

	subForm := url.Values{"csrf_token": {token}, "app_id": {"ci-notifier"}}
	subReq := httptest.NewRequest(http.MethodPost, "/users/7/subscriptions", strings.NewReader(subForm.Encode()))
	subReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		subReq.AddCookie(c)
	}
	subRec := httptest.NewRecorder()
	handler.ServeHTTP(subRec, subReq)
	if subRec.Code != http.StatusOK {
		t.Fatalf("subscribe: expected 200, got %d: %s", subRec.Code, subRec.Body.String())
	}
	if lastMethod != http.MethodPost {
		t.Errorf("subscribe method = %q", lastMethod)
	}

	unsubForm := url.Values{"csrf_token": {token}}
	unsubReq := httptest.NewRequest(http.MethodPost, "/users/7/subscriptions/ci-notifier/delete", strings.NewReader(unsubForm.Encode()))
	unsubReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		unsubReq.AddCookie(c)
	}
	unsubRec := httptest.NewRecorder()
	handler.ServeHTTP(unsubRec, unsubReq)
	if unsubRec.Code != http.StatusOK {
		t.Fatalf("unsubscribe: expected 200, got %d: %s", unsubRec.Code, unsubRec.Body.String())
	}
	if lastMethod != http.MethodDelete {
		t.Errorf("unsubscribe method = %q", lastMethod)
	}
}
