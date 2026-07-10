package adminui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/CatPope/telegram_server/internal/audit"
)

// fakeStore is an in-memory Store double so page-handler tests don't need
// a real database — Phase A2's plan explicitly abstracts Store behind an
// interface for this reason.
type fakeStore struct {
	apps         map[string]App
	users        []UserRow
	stageCounts  []AppStageCount
	kpi          KPICounts
	kpiErr       error
	pipeline     []StageCount
	pipelineErr  error
	causes       []ErrorCodeCount
	causesErr    error
	latency      LatencyStats
	latencyErr   error
	failures     []FailureRow
	failuresErr  error
	verifyResult audit.VerifyResult
	verifyErr    error
	err          error
}

func (f *fakeStore) ListApps(context.Context) ([]App, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []App
	for _, a := range f.apps {
		out = append(out, a)
	}
	return out, nil
}

func (f *fakeStore) GetApp(_ context.Context, id string) (App, error) {
	if f.err != nil {
		return App{}, f.err
	}
	a, ok := f.apps[id]
	if !ok {
		return App{}, ErrAppNotFound
	}
	return a, nil
}

func (f *fakeStore) ListUsers(context.Context) ([]UserRow, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.users, nil
}

func (f *fakeStore) DashboardStats(context.Context) (DashboardStats, error) {
	if f.err != nil {
		return DashboardStats{}, f.err
	}
	return DashboardStats{TotalApps: len(f.apps), Users: len(f.users)}, nil
}

func (f *fakeStore) RequestSeries(context.Context, int) ([]AppDayCount, error) {
	return nil, f.err
}

func (f *fakeStore) DeliveryKPICounts(context.Context) (KPICounts, error) {
	if f.kpiErr != nil {
		return KPICounts{}, f.kpiErr
	}
	return f.kpi, nil
}

func (f *fakeStore) StageCounts(context.Context, int) ([]AppStageCount, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.stageCounts, nil
}

func (f *fakeStore) PipelineStageCounts(context.Context) ([]StageCount, error) {
	if f.pipelineErr != nil {
		return nil, f.pipelineErr
	}
	return f.pipeline, nil
}

func (f *fakeStore) FailureCauseCounts(context.Context) ([]ErrorCodeCount, error) {
	if f.causesErr != nil {
		return nil, f.causesErr
	}
	return f.causes, nil
}

func (f *fakeStore) DeliveryLatency(context.Context) (LatencyStats, error) {
	if f.latencyErr != nil {
		return LatencyStats{}, f.latencyErr
	}
	return f.latency, nil
}

func (f *fakeStore) RecentFailures(context.Context, int, int) ([]FailureRow, error) {
	if f.failuresErr != nil {
		return nil, f.failuresErr
	}
	return f.failures, nil
}

func (f *fakeStore) VerifyAuditChain(context.Context) (audit.VerifyResult, error) {
	return f.verifyResult, f.verifyErr
}

// loginSession drives the login flow against handler and returns the
// resulting session cookies, so page-handler tests can jump straight to
// an authenticated request.
func loginSession(t *testing.T, handler http.Handler, cfg Config) []*http.Cookie {
	t.Helper()

	loginPageReq := httptest.NewRequest(http.MethodGet, "/login", nil)
	loginPageRec := httptest.NewRecorder()
	handler.ServeHTTP(loginPageRec, loginPageReq)
	csrfCookies := loginPageRec.Result().Cookies()
	token := extractCSRFToken(t, loginPageRec.Body.String())

	form := url.Values{"csrf_token": {token}, "password": {cfg.Password}}
	loginReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range csrfCookies {
		loginReq.AddCookie(c)
	}
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusSeeOther {
		t.Fatalf("login failed: %d %s", loginRec.Code, loginRec.Body.String())
	}
	return loginRec.Result().Cookies()
}

func TestAppsListShowsDBUnavailableWithoutStore(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	req := httptest.NewRequest(http.MethodGet, "/apps", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "DB 미연결") {
		t.Error("expected DB-unavailable notice in body")
	}
}

func TestAppsListRendersAppsFromStore(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	store := &fakeStore{apps: map[string]App{
		"ci-notifier": {ID: "ci-notifier", Name: "CI Notifier", MinGrade: "user", Active: true, Capabilities: []string{"messages.direct.send"}},
	}}
	handler, err := NewServer(cfg, store, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	req := httptest.NewRequest(http.MethodGet, "/apps", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ci-notifier") {
		t.Error("expected app id in rendered list")
	}
}

func TestAppDetailNotFound(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, &fakeStore{apps: map[string]App{}}, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	req := httptest.NewRequest(http.MethodGet, "/apps/missing", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestAppCreateWithoutCSRFIsForbidden(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	form := url.Values{"id": {"noapp"}, "name": {"No App"}}
	req := httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(form.Encode()))
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

func TestAppPatchAndDeactivateSuccess(t *testing.T) {
	var lastMethod, lastPath string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastMethod, lastPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"ci-notifier"}`))
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	store := &fakeStore{apps: map[string]App{
		"ci-notifier": {ID: "ci-notifier", Name: "CI Notifier", MinGrade: "user", Active: true},
	}}
	handler, err := NewServer(cfg, store, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	detailReq := httptest.NewRequest(http.MethodGet, "/apps/ci-notifier", nil)
	for _, c := range cookies {
		detailReq.AddCookie(c)
	}
	detailRec := httptest.NewRecorder()
	handler.ServeHTTP(detailRec, detailReq)
	token := extractCSRFToken(t, detailRec.Body.String())

	patchForm := url.Values{"csrf_token": {token}, "description": {"updated"}, "min_grade": {"user"}}
	patchReq := httptest.NewRequest(http.MethodPost, "/apps/ci-notifier/patch", strings.NewReader(patchForm.Encode()))
	patchReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		patchReq.AddCookie(c)
	}
	patchRec := httptest.NewRecorder()
	handler.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusSeeOther {
		t.Fatalf("patch: expected redirect, got %d: %s", patchRec.Code, patchRec.Body.String())
	}
	if lastMethod != http.MethodPatch || lastPath != "/admin/apps/ci-notifier" {
		t.Errorf("patch: got %s %s", lastMethod, lastPath)
	}
	if loc := patchRec.Header().Get("Location"); loc != "/apps/ci-notifier?saved=1" {
		t.Errorf("patch: Location = %q", loc)
	}

	savedReq := httptest.NewRequest(http.MethodGet, "/apps/ci-notifier?saved=1", nil)
	for _, c := range cookies {
		savedReq.AddCookie(c)
	}
	savedRec := httptest.NewRecorder()
	handler.ServeHTTP(savedRec, savedReq)
	if !strings.Contains(savedRec.Body.String(), "저장되었습니다") {
		t.Errorf("expected saved success message, got: %s", savedRec.Body.String())
	}

	deactivateForm := url.Values{"csrf_token": {token}}
	deactivateReq := httptest.NewRequest(http.MethodPost, "/apps/ci-notifier/deactivate", strings.NewReader(deactivateForm.Encode()))
	deactivateReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		deactivateReq.AddCookie(c)
	}
	deactivateRec := httptest.NewRecorder()
	handler.ServeHTTP(deactivateRec, deactivateReq)
	if deactivateRec.Code != http.StatusSeeOther {
		t.Fatalf("deactivate: expected redirect, got %d: %s", deactivateRec.Code, deactivateRec.Body.String())
	}
	if lastMethod != http.MethodDelete {
		t.Errorf("deactivate method = %q", lastMethod)
	}
	if loc := deactivateRec.Header().Get("Location"); loc != "/apps/ci-notifier?deactivated=1" {
		t.Errorf("deactivate: Location = %q", loc)
	}

	deactivatedReq := httptest.NewRequest(http.MethodGet, "/apps/ci-notifier?deactivated=1", nil)
	for _, c := range cookies {
		deactivatedReq.AddCookie(c)
	}
	deactivatedRec := httptest.NewRecorder()
	handler.ServeHTTP(deactivatedRec, deactivatedReq)
	if !strings.Contains(deactivatedRec.Body.String(), "비활성화되었습니다") {
		t.Errorf("expected deactivated success message, got: %s", deactivatedRec.Body.String())
	}
}

func TestAppDetailDBUnavailableHidesForms(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	req := httptest.NewRequest(http.MethodGet, "/apps/ci-notifier", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "수정/비활성화 폼을 표시하지 않습니다") {
		t.Error("expected a DB-unavailable notice explaining the forms are hidden")
	}
	if strings.Contains(body, `action="/apps/ci-notifier/patch"`) {
		t.Error("patch form should not render when DB is unavailable")
	}
	if strings.Contains(body, `action="/apps/ci-notifier/deactivate"`) {
		t.Error("deactivate form should not render when DB is unavailable")
	}
}

func TestAppCreateMapsServerErrorToFriendlyMessage(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"app_already_exists"}`))
	}))
	defer target.Close()

	cfg := testConfig(t, target.URL)
	handler, err := NewServer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cookies := loginSession(t, handler, cfg)

	newFormReq := httptest.NewRequest(http.MethodGet, "/apps/new", nil)
	for _, c := range cookies {
		newFormReq.AddCookie(c)
	}
	newFormRec := httptest.NewRecorder()
	handler.ServeHTTP(newFormRec, newFormReq)
	token := extractCSRFToken(t, newFormRec.Body.String())

	form := url.Values{"csrf_token": {token}, "id": {"dup"}, "name": {"Dup"}}
	req := httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (re-rendered form with error), got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "이미 존재하는 앱 ID입니다") {
		t.Errorf("expected friendly error message, got: %s", rec.Body.String())
	}
}
